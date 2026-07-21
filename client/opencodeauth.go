package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrOpencodeAuthNotFound is returned by HarvestOpencodeAuth when no opencode
// auth.json exists at the resolved host path. The message carries the
// remediation (log in with opencode locally) so a caller can surface it
// verbatim; the harvest wraps it with the path it looked at. It never carries
// any credential bytes — there are none to carry on this branch.
var ErrOpencodeAuthNotFound = errors.New("sandbox: no local opencode auth.json found — run `opencode auth login <provider>` on this machine")

// OpencodeAuthEntry names a single provider credential present in the local
// opencode auth.json, WITHOUT its secret value: Provider is the auth.json entry
// key (e.g. "anthropic", "openai", "opencode") and Type is that entry's declared
// "type" (e.g. "oauth", "api"). It is metadata a caller can display or filter on
// — the credential value itself lives only in OpencodeAuthMaterial.JSON and never
// in an Entry.
type OpencodeAuthEntry struct {
	Provider string
	Type     string
}

// OpencodeAuthMaterial is the harvested local opencode auth.json: JSON holds the
// document bytes (the seed a session provisions via CreateOptions.OpencodeAuthJSON),
// and Entries is the value-free index of which providers it carries (sorted by
// Provider). JSON is the ONLY field that holds credential values — Entries,
// errors, and logs are all value-free — so a consumer can inspect Entries or log
// the material freely while treating JSON as the sole secret.
type OpencodeAuthMaterial struct {
	JSON    []byte
	Entries []OpencodeAuthEntry
}

// HarvestOpencodeAuth reads the host's own opencode login — the auth.json the
// `opencode auth login` command maintains — into seed Material a caller can hand
// to CreateOptions.OpencodeAuthJSON (optionally narrowed with Filter first). It
// is the opencode analogue of cred.SystemMaterial: host-side, read-only, and
// fail-closed. A missing file maps to ErrOpencodeAuthNotFound; an unreadable or
// non-object document to a descriptive error that names the PATH but never any
// credential bytes.
func HarvestOpencodeAuth() (OpencodeAuthMaterial, error) {
	return harvestOpencodeAuth(os.Getenv, os.UserHomeDir, os.ReadFile)
}

// harvestOpencodeAuth is the injectable core of HarvestOpencodeAuth: tests supply
// fake getenv/home/readFile funcs to exercise the XDG and home-fallback paths and
// every parse-failure branch without a real opencode login on disk. It mirrors
// cred.systemMaterial's seam style (see client/cred/system.go).
func harvestOpencodeAuth(getenv func(string) string, home func() (string, error), readFile func(string) ([]byte, error)) (OpencodeAuthMaterial, error) {
	path, err := opencodeAuthPath(getenv, home)
	if err != nil {
		return OpencodeAuthMaterial{}, err
	}

	raw, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// Wrap the sentinel with the path we looked at — errors.Is still matches,
		// and the caller learns both the remediation and where we checked.
		return OpencodeAuthMaterial{}, fmt.Errorf("%w (looked at %s)", ErrOpencodeAuthNotFound, path)
	}
	if err != nil {
		// An OS read error (permissions, IO) names the path but never file
		// content — safe to wrap.
		return OpencodeAuthMaterial{}, fmt.Errorf("sandbox: read opencode auth.json at %s: %w", path, err)
	}

	// Parse as a bare provider→entry map. We deliberately do NOT wrap the json
	// error: a syntax error can echo a byte of the (secret) document, so the
	// parse-failure branches name the path and a generic reason only. A JSON
	// "null" unmarshals into a nil map without error, so it is caught explicitly.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return OpencodeAuthMaterial{}, fmt.Errorf("sandbox: opencode auth.json at %s is not a JSON object", path)
	}

	entries := make([]OpencodeAuthEntry, 0, len(doc))
	for provider, entryRaw := range doc {
		// Read only the "type" field; every other field of the entry (the secret
		// token/key material) is left in the raw document and never lifted into an
		// Entry. A missing or non-string type is an error that names the provider
		// key and path — never the entry's contents.
		var entry struct {
			Type *string `json:"type"`
		}
		if err := json.Unmarshal(entryRaw, &entry); err != nil || entry.Type == nil {
			return OpencodeAuthMaterial{}, fmt.Errorf("sandbox: opencode auth.json at %s: provider %q entry has no string \"type\" field", path, provider)
		}
		entries = append(entries, OpencodeAuthEntry{Provider: provider, Type: *entry.Type})
	}
	// Sort by Provider for a stable, deterministic index (map iteration order is
	// random). JSON stays the ORIGINAL bytes — never re-marshaled — so fields this
	// package does not model survive verbatim into the pod.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Provider < entries[j].Provider })
	return OpencodeAuthMaterial{JSON: raw, Entries: entries}, nil
}

// opencodeAuthPath resolves the host auth.json location, mirroring opencode's own
// XDG layout: $XDG_DATA_HOME/opencode/auth.json when XDG_DATA_HOME is set, else
// ~/.local/share/opencode/auth.json.
func opencodeAuthPath(getenv func(string) string, home func() (string, error)) (string, error) {
	if xdg := getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode", "auth.json"), nil
	}
	h, err := home()
	if err != nil {
		return "", fmt.Errorf("sandbox: resolve home for opencode auth.json: %w", err)
	}
	return filepath.Join(h, ".local", "share", "opencode", "auth.json"), nil
}

// Filter narrows harvested material to the named providers, returning a new
// Material whose JSON is just those entries and whose Entries is the matching
// subset. It is how a caller seeds a session with ONLY the provider that session
// selected instead of shipping the whole multi-provider auth.json into the pod.
//
// Zero providers is an error (an empty filter almost certainly means a bug, and
// a session must never be seeded with an empty document). An unknown provider
// name is an error that lists the AVAILABLE provider names (names only, never
// values). The result JSON is json.Marshal of the subset map — Go sorts map keys,
// so it is deterministic — and Entries is the value-free subset.
func (m OpencodeAuthMaterial) Filter(providers ...string) (OpencodeAuthMaterial, error) {
	if len(providers) == 0 {
		return OpencodeAuthMaterial{}, errors.New("sandbox: empty seed filter (name at least one provider)")
	}
	// Re-parse the original document to recover each provider's raw entry bytes
	// (Entries is value-free by design, so the values live only in JSON).
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(m.JSON, &doc); err != nil || doc == nil {
		return OpencodeAuthMaterial{}, errors.New("sandbox: opencode auth material is not a JSON object")
	}

	want := make(map[string]bool, len(providers))
	subset := make(map[string]json.RawMessage, len(providers))
	for _, p := range providers {
		raw, ok := doc[p]
		if !ok {
			return OpencodeAuthMaterial{}, fmt.Errorf("sandbox: unknown opencode provider %q in seed filter (available: %s)",
				p, strings.Join(availableProviders(m.Entries), ", "))
		}
		subset[p] = raw
		want[p] = true
	}

	filtered, err := json.Marshal(subset)
	if err != nil {
		// Only reachable if a RawMessage is invalid JSON, which the harvest already
		// ruled out; keep it value-free regardless.
		return OpencodeAuthMaterial{}, errors.New("sandbox: marshal opencode auth seed subset")
	}

	// Preserve the sorted order of the source Entries.
	var entries []OpencodeAuthEntry
	for _, e := range m.Entries {
		if want[e.Provider] {
			entries = append(entries, e)
		}
	}
	return OpencodeAuthMaterial{JSON: filtered, Entries: entries}, nil
}

// availableProviders lists the provider NAMES in the material (never their
// values), for the unknown-provider error. Entries is already sorted by Provider.
func availableProviders(entries []OpencodeAuthEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Provider)
	}
	return names
}

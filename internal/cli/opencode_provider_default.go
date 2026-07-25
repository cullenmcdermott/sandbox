package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// opencode_provider_default.go picks the session's effective opencode provider
// when --provider is empty, and remembers the provider a session actually
// launched with so the next launch defaults to it ("remember what I used last").
// Context: the selected provider is only an AUTH GATE (client.Create's
// fail-closed seed check + the runner's assertOpencodeAuthUsable) — the default
// D4 seed carries the user's WHOLE local login into the pod, so every logged-in
// provider stays switchable in-session via /model regardless of which one is
// selected here. That is what makes a smart default safe: it never narrows what
// the session can authenticate to.

// opencodeProviderPreference orders the auto-pick, most-preferred first, as
// (auth.json entry key, --provider wire value) pairs. Anthropic leads to
// preserve the long-standing default for users logged into it. Only providers in
// this list are selectable: client.Create validates OpencodeProvider against the
// OpencodeProvider* enum fail-closed, so auth.json entries outside it
// (github-copilot, opencode-go, ...) can NOT be the selected provider — they
// still ride along in the seed, usable in-session.
var opencodeProviderPreference = []struct {
	entryKey string
	wire     string
}{
	{"anthropic", session.OpencodeProviderAnthropic},
	{"opencode", session.OpencodeProviderZen},
	{"openai", session.OpencodeProviderOpenAI},
}

// defaultOpencodeProvider picks the effective provider for a session whose
// --provider flag is empty, given the harvested local login. The remembered
// last-used provider wins when it is still logged in (and inside the
// --seed-providers filter when one is given); else the preference order above;
// else "" — meaning nothing selectable is logged in, so the caller keeps the
// anthropic default and the existing fail-closed remediation (the login
// passthrough, or client.Create's seed-validation error) surfaces exactly as
// before. A --seed-providers filter narrows the candidate set: defaulting to a
// provider the filter would exclude would only re-error in seedFromMaterial.
func defaultOpencodeProvider(material client.OpencodeAuthMaterial, lastUsed, seedProviders string) string {
	var filter map[string]bool
	if strings.TrimSpace(seedProviders) != "" {
		keys, err := parseSeedProviders(seedProviders)
		if err != nil {
			// The bad filter itself errors downstream in seedFromMaterial with
			// the accepted spellings; there is no valid pick to make here.
			return ""
		}
		filter = make(map[string]bool, len(keys))
		for _, k := range keys {
			filter[k] = true
		}
	}
	allowed := func(entryKey string) bool {
		return filter == nil || filter[entryKey]
	}
	if isSelectableOpencodeProvider(lastUsed) {
		if key := opencodeProviderEntryKey(lastUsed); materialHasEntry(material, key) && allowed(key) {
			return lastUsed
		}
	}
	for _, p := range opencodeProviderPreference {
		if materialHasEntry(material, p.entryKey) && allowed(p.entryKey) {
			return p.wire
		}
	}
	return ""
}

// isSelectableOpencodeProvider reports whether p is a valid --provider wire
// value (the client.Create enum). It guards the remembered-preference read so a
// corrupt or hand-edited pref file is ignored rather than surfacing as a
// create-time ErrInvalidOpencodeProvider.
func isSelectableOpencodeProvider(p string) bool {
	switch p {
	case session.OpencodeProviderAnthropic, session.OpencodeProviderOpenAI, session.OpencodeProviderZen:
		return true
	}
	return false
}

// lastOpencodeProviderPath resolves the remembered-provider preference file:
// ~/.local/share/sandbox/last-opencode-provider — the same local share root the
// session index uses (internal/index). The file holds a single provider wire
// value; it is NOT a secret.
func lastOpencodeProviderPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "sandbox", "last-opencode-provider"), nil
}

// readLastOpencodeProvider returns the remembered provider, or "" when none is
// recorded or the file is unreadable/corrupt. The preference is strictly
// best-effort: it must never block or error a launch.
func readLastOpencodeProvider() string {
	path, err := lastOpencodeProviderPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(b))
	if !isSelectableOpencodeProvider(p) {
		return ""
	}
	return p
}

// writeLastOpencodeProvider records the provider a session actually launched
// with so the next launch defaults to it. Best-effort: a write failure is
// silently dropped — a missing/stale preference only means the auto-pick order
// applies next time. Anything outside the selectable enum (including "", the
// shared-Secret fallback path — an absence of choice, not a choice) is never
// recorded.
func writeLastOpencodeProvider(provider string) {
	if !isSelectableOpencodeProvider(provider) {
		return
	}
	path, err := lastOpencodeProviderPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(provider+"\n"), 0o644)
}

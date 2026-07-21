package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// opencode_auth.go wires the create-time UX for opencode multi-provider auth
// (change tasks 5.1–5.3): `sandbox opencode` harvests the host's own opencode
// login (auth.json) and seeds it per-session, so the primary path no longer
// depends on the cluster's shared opencode-credentials Secret. The shared Secret
// stays only as the fallback for a machine with no local opencode login. The
// client SDK (client.HarvestOpencodeAuth / OpencodeAuthMaterial.Filter) and the
// k8s/runner transport are owned by parallel tasks; this file is the CLI glue.

// errOpencodeProviderNotHarvested is an INTERNAL sentinel: seedFromMaterial
// returns it when the session's selected provider has no entry in the harvested
// auth.json, so resolve can branch to the login-passthrough path (D5) instead of
// surfacing it as a terminal error. It never escapes this file.
var errOpencodeProviderNotHarvested = errors.New("selected provider not in local opencode login")

// opencodeSeedDeps carries resolveOpencodeSeed's external dependencies behind
// function seams so the resolver is hermetically testable: harvest reads the
// host auth.json, isTTY gates the interactive login passthrough, runLogin shells
// out to `opencode auth login`, and stdin feeds the yes/no confirmation. The real
// wiring lives in defaultOpencodeSeedDeps; tests substitute fakes so no test ever
// touches a real opencode binary or the user's terminal.
type opencodeSeedDeps struct {
	harvest  func() (client.OpencodeAuthMaterial, error)
	isTTY    func() bool
	runLogin func(ctx context.Context, entryKey string) error
	stdin    io.Reader
}

// defaultOpencodeSeedDeps wires the production dependencies: the SDK harvest, a
// stdin+stderr TTY probe (both must be a terminal before we prompt), and an
// interactive `opencode auth login` passthrough that inherits all three std
// streams so opencode's own device-code/browser flow owns the terminal.
func defaultOpencodeSeedDeps() opencodeSeedDeps {
	return opencodeSeedDeps{
		harvest: client.HarvestOpencodeAuth,
		isTTY: func() bool {
			// Prompt only when we can both ASK (stderr) and READ an answer (stdin);
			// a piped stdin or redirected stderr must fail closed, not hang.
			return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
		},
		runLogin: func(ctx context.Context, entryKey string) error {
			// Interactive passthrough: opencode's login flow drives the terminal
			// itself, so inherit stdin/stdout/stderr rather than capturing them.
			c := exec.CommandContext(ctx, "opencode", "auth", "login", entryKey)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
		stdin: os.Stdin,
	}
}

// resolveOpencodeSeed decides what (if anything) `sandbox opencode` seeds into a
// new session from the host's local opencode login, given the session's default
// --provider and the --seed-providers filter. It returns the seed document bytes
// for CreateOptions.OpencodeAuthJSON, or nil to signal "leave it empty and take
// the shared-Secret fallback". ctx is threaded through solely for the login
// passthrough (exec.CommandContext); it is NOT in the change's abstract D4/D5/D6
// signature but is required to make the passthrough cancellable and mirror the
// rest of runStartSession, which already carries ctx.
func resolveOpencodeSeed(ctx context.Context, provider, seedProviders string, stderr io.Writer) ([]byte, error) {
	return defaultOpencodeSeedDeps().resolve(ctx, provider, seedProviders, stderr)
}

// resolve implements the D4/D5/D6 case logic. See resolveOpencodeSeed for the
// contract; the branches are commented inline (Case A/B/C + the exclusion error).
func (d opencodeSeedDeps) resolve(ctx context.Context, provider, seedProviders string, stderr io.Writer) ([]byte, error) {
	material, err := d.harvest()
	if errors.Is(err, client.ErrOpencodeAuthNotFound) {
		// Case A (D6): no local opencode login → return nil so runStartSession
		// leaves OpencodeAuthJSON empty and the unchanged shared-Secret fallback
		// path takes over. One stderr line so the fallback is visible, never silent.
		fmt.Fprintf(stderr, "opencode: no local opencode login found; using the shared cluster Secret for provider %q\n", opencodeProviderLabel(provider))
		return nil, nil
	}
	if err != nil {
		// Case B: a present-but-broken store (unreadable / non-object / bad entry)
		// fails closed. A corrupt local login must NOT silently fall back to the
		// shared Secret — that would mask the breakage and ship the wrong creds.
		return nil, err
	}

	seed, err := d.seedFromMaterial(material, provider, seedProviders)
	if err == nil {
		return seed, nil
	}
	if !errors.Is(err, errOpencodeProviderNotHarvested) {
		// Unknown --seed-providers value, an excluded selected provider, or a
		// Filter error — all terminal, surface as-is.
		return nil, err
	}

	// Case C (D5): the session's provider is not in the local login. The user HAS
	// a local store (Case A already handled its absence), so the shared Secret is
	// likely unpopulated for this provider — never fall back to it here.
	entryKey := opencodeProviderEntryKey(provider)
	if !d.isTTY() {
		return nil, opencodeLoginRemediation(provider, entryKey)
	}
	prompt := fmt.Sprintf("provider %q is not in your local opencode login — run `opencode auth login %s` now? [y/N] ",
		opencodeProviderLabel(provider), entryKey)
	if !promptYesNo(d.stdin, stderr, prompt) {
		return nil, opencodeLoginRemediation(provider, entryKey)
	}
	if lerr := d.runLogin(ctx, entryKey); lerr != nil {
		return nil, fmt.Errorf("opencode auth login %s: %w", entryKey, lerr)
	}
	// Re-harvest and recompute exactly ONCE — the login just wrote a fresh entry.
	material, err = d.harvest()
	if err != nil {
		return nil, err
	}
	seed, err = d.seedFromMaterial(material, provider, seedProviders)
	if err != nil {
		// Still missing after a login (cancelled, or a different provider chosen)
		// → fail closed rather than loop.
		return nil, err
	}
	return seed, nil
}

// seedFromMaterial computes the seed document from harvested material for the
// given session provider and --seed-providers filter, WITHOUT any I/O. It returns
// errOpencodeProviderNotHarvested (an internal sentinel) when the selected
// provider is absent from the local login so resolve can offer the login
// passthrough; every other failure (unknown filter value, the selected provider
// excluded by the filter, a Filter error) is a terminal error.
func (d opencodeSeedDeps) seedFromMaterial(material client.OpencodeAuthMaterial, provider, seedProviders string) ([]byte, error) {
	selKey := opencodeProviderEntryKey(provider)

	// The selected provider must be present in the local login at all; if not,
	// signal Case C. This check comes first so "present but filtered out" (below)
	// is distinguishable from "not logged in".
	if !materialHasEntry(material, selKey) {
		return nil, errOpencodeProviderNotHarvested
	}

	if strings.TrimSpace(seedProviders) == "" {
		// D4 default: seed EVERYTHING the user logged into — the whole auth.json.
		// Narrowing is opt-in via --seed-providers (the security lever).
		return material.JSON, nil
	}

	entryKeys, err := parseSeedProviders(seedProviders)
	if err != nil {
		return nil, err
	}
	// The session's own provider MUST be in the seed set, else the pod would get
	// an auth.json with no credential for the provider it is about to use.
	if !containsString(entryKeys, selKey) {
		return nil, fmt.Errorf("sandbox: --seed-providers must include the session's --provider %q", opencodeProviderLabel(provider))
	}
	filtered, err := material.Filter(entryKeys...)
	if err != nil {
		// Filter's error already lists the available providers (names only).
		return nil, err
	}
	return filtered.JSON, nil
}

// parseSeedProviders splits the comma-separated --seed-providers value, maps each
// wire spelling to its auth.json entry key, and rejects any unknown value (listing
// the accepted spellings). Blank items are skipped; an all-blank value is an error
// (an explicit but empty filter is almost certainly a mistake).
func parseSeedProviders(csv string) ([]string, error) {
	var keys []string
	for _, raw := range strings.Split(csv, ",") {
		wire := strings.TrimSpace(raw)
		if wire == "" {
			continue
		}
		key, err := opencodeSeedWireEntryKey(wire)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("sandbox: --seed-providers named no providers")
	}
	return keys, nil
}

// opencodeProviderEntryKey maps our OpencodeProvider wire vocabulary to the key
// opencode itself uses INSIDE auth.json (Zen's credential is stored under
// "opencode", not the wire value "opencode-zen"). It delegates to the shared
// session.OpencodeProviderEntryKey so the CLI, the client SDK, and the k8s
// transport all agree on one mapping.
func opencodeProviderEntryKey(provider string) string {
	return session.OpencodeProviderEntryKey(provider)
}

// opencodeSeedWireEntryKey maps ONE --seed-providers wire value to its auth.json
// entry key, rejecting unknown values with the accepted spellings. Unlike
// opencodeProviderEntryKey it has no empty default (blank items are dropped before
// this call) so a stray comma never silently expands the seed set to "anthropic".
func opencodeSeedWireEntryKey(wire string) (string, error) {
	switch wire {
	case session.OpencodeProviderAnthropic:
		return "anthropic", nil
	case session.OpencodeProviderOpenAI:
		return "openai", nil
	case session.OpencodeProviderZen:
		return "opencode", nil
	default:
		return "", fmt.Errorf("sandbox: unknown --seed-providers value %q (accepted: %q, %q, %q)",
			wire, session.OpencodeProviderAnthropic, session.OpencodeProviderOpenAI, session.OpencodeProviderZen)
	}
}

// opencodeProviderLabel renders a provider for a human message: empty is shown as
// its default wire value ("anthropic") so error/prompt text never says provider "".
func opencodeProviderLabel(provider string) string {
	if provider == "" {
		return session.OpencodeProviderAnthropic
	}
	return provider
}

// opencodeLoginRemediation is the fail-closed error when the selected provider is
// absent from the local login and we can't (or the user won't) log in now. It
// names both remedies: log in on this machine, or seed a provider you already have.
func opencodeLoginRemediation(provider, entryKey string) error {
	return fmt.Errorf("sandbox: provider %q is not in your local opencode login — run `opencode auth login %s` on this machine, or pass --seed-providers/--provider for a provider you're logged into",
		opencodeProviderLabel(provider), entryKey)
}

// materialHasEntry reports whether the harvested material carries an entry for the
// given auth.json key (Entries is value-free, so this reads no credential bytes).
func materialHasEntry(material client.OpencodeAuthMaterial, key string) bool {
	for _, e := range material.Entries {
		if e.Provider == key {
			return true
		}
	}
	return false
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// promptYesNo writes prompt to stderr and reads a single line from stdin, treating
// only "y"/"yes" (case-insensitive) as yes — anything else, including EOF, is no.
func promptYesNo(stdin io.Reader, stderr io.Writer, prompt string) bool {
	fmt.Fprint(stderr, prompt)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

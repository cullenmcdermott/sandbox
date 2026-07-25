package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// testMaterial builds an OpencodeAuthMaterial carrying an entry for each given
// auth.json entry key (NB: the Zen entry key is "opencode", not the wire value
// "opencode-zen"). JSON is real, Filter-parseable bytes; Entries is the sorted,
// value-free index harvest would produce.
func testMaterial(t *testing.T, entryKeys ...string) client.OpencodeAuthMaterial {
	t.Helper()
	doc := map[string]map[string]string{}
	var entries []client.OpencodeAuthEntry
	for _, k := range entryKeys {
		doc[k] = map[string]string{"type": "api", "key": "secret-" + k}
		entries = append(entries, client.OpencodeAuthEntry{Provider: k, Type: "api"})
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal test material: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Provider < entries[j].Provider })
	return client.OpencodeAuthMaterial{JSON: raw, Entries: entries}
}

// harvestResult is one scripted return of the harvest seam.
type harvestResult struct {
	m   client.OpencodeAuthMaterial
	err error
}

// harvestSeq returns a harvest func that yields the scripted results in order and
// panics if called more than scripted (so an unexpected extra harvest is loud).
func harvestSeq(results ...harvestResult) func() (client.OpencodeAuthMaterial, error) {
	i := 0
	return func() (client.OpencodeAuthMaterial, error) {
		if i >= len(results) {
			panic("harvest called more times than scripted")
		}
		r := results[i]
		i++
		return r.m, r.err
	}
}

// seedKeys parses a seed document into the set of provider keys it carries.
func seedKeys(t *testing.T, seed []byte) map[string]bool {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(seed, &doc); err != nil {
		t.Fatalf("seed is not a JSON object: %v", err)
	}
	got := map[string]bool{}
	for k := range doc {
		got[k] = true
	}
	return got
}

// Case A: no local opencode login → nil seed (shared-Secret fallback) plus a
// single visible stderr line. The TTY/login seams must never be consulted.
func TestResolveOpencodeSeedNoStoreFallsBack(t *testing.T) {
	var stderr bytes.Buffer
	deps := opencodeSeedDeps{
		harvest: harvestSeq(harvestResult{err: client.ErrOpencodeAuthNotFound}),
		isTTY:   func() bool { t.Fatal("isTTY must not be consulted on the no-store path"); return false },
		runLogin: func(context.Context, string) error {
			t.Fatal("runLogin must not fire on the no-store path")
			return nil
		},
	}
	provider, seed, err := deps.resolve(context.Background(), "", "", &stderr)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if provider != "" {
		t.Fatalf("provider = %q, want \"\" (no login to default FROM — pass through)", provider)
	}
	if seed != nil {
		t.Fatalf("seed = %q, want nil (shared-Secret fallback)", seed)
	}
	if !strings.Contains(stderr.String(), "no local opencode login found") || !strings.Contains(stderr.String(), "anthropic") {
		t.Fatalf("stderr = %q, want the fallback notice naming the provider", stderr.String())
	}
}

// Case B: a present-but-broken store fails closed — the error surfaces and no
// login is attempted (never a silent shared-Secret fallback).
func TestResolveOpencodeSeedCorruptStoreSurfaces(t *testing.T) {
	boom := errors.New("sandbox: opencode auth.json at /x is not a JSON object")
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{err: boom}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("runLogin must not fire on a corrupt store"); return nil },
	}
	_, _, err := deps.resolve(context.Background(), "", "", &bytes.Buffer{})
	if !errors.Is(err, boom) {
		t.Fatalf("resolve err = %v, want the harvest error", err)
	}
}

// Default (--seed-providers empty) seeds EVERYTHING the user logged into: the
// returned seed is the harvested document verbatim.
func TestResolveOpencodeSeedDefaultSeedsAll(t *testing.T) {
	m := testMaterial(t, "anthropic", "openai")
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("no login expected"); return nil },
	}
	provider, seed, err := deps.resolve(context.Background(), "", "", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if provider != session.OpencodeProviderAnthropic {
		t.Fatalf("provider = %q, want the auto-picked anthropic (preference order)", provider)
	}
	if !bytes.Equal(seed, m.JSON) {
		t.Fatalf("seed = %q, want the whole harvested document %q", seed, m.JSON)
	}
	if got := seedKeys(t, seed); !got["anthropic"] || !got["openai"] {
		t.Fatalf("seed keys = %v, want anthropic+openai", got)
	}
}

// A --seed-providers subset maps wire values to entry keys and narrows the seed
// to exactly those providers (the security lever).
func TestResolveOpencodeSeedSubsetFilters(t *testing.T) {
	m := testMaterial(t, "anthropic", "openai", "opencode")
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("no login expected"); return nil },
	}
	_, seed, err := deps.resolve(context.Background(), session.OpencodeProviderAnthropic, "anthropic,openai", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := seedKeys(t, seed)
	if !got["anthropic"] || !got["openai"] {
		t.Fatalf("seed keys = %v, want anthropic+openai", got)
	}
	if got["opencode"] {
		t.Fatalf("seed keys = %v, must NOT carry the excluded opencode entry", got)
	}
}

// An unknown --seed-providers wire value is rejected, listing the accepted
// spellings.
func TestResolveOpencodeSeedUnknownSeedProviderErrors(t *testing.T) {
	m := testMaterial(t, "anthropic")
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("no login expected"); return nil },
	}
	_, _, err := deps.resolve(context.Background(), "", "bogus", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown --seed-providers value") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err = %v, want an unknown-value error naming bogus", err)
	}
}

// If --seed-providers excludes the session's EXPLICITLY selected provider, the
// pod would get an auth.json with no credential for the provider it uses — a
// hard error. (An empty --provider instead auto-picks a provider INSIDE the
// filter — see TestResolveOpencodeSeedEmptyProviderFilterNarrowsPick.)
func TestResolveOpencodeSeedExcludedSelectedProviderErrors(t *testing.T) {
	m := testMaterial(t, "anthropic", "openai")
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("no login expected"); return nil },
	}
	// Explicit --provider anthropic; the filter names only openai.
	_, _, err := deps.resolve(context.Background(), session.OpencodeProviderAnthropic, "openai", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "must include the session's --provider") || !strings.Contains(err.Error(), "anthropic") {
		t.Fatalf("err = %v, want the exclusion error naming anthropic", err)
	}
}

// Selected provider absent + non-TTY → fail closed with remediation, no login
// attempt (we can't prompt on a pipe).
func TestResolveOpencodeSeedMissingProviderNonTTYFailsClosed(t *testing.T) {
	m := testMaterial(t, "anthropic") // no openai
	loginCalls := 0
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return false },
		runLogin: func(context.Context, string) error { loginCalls++; return nil },
	}
	_, _, err := deps.resolve(context.Background(), session.OpencodeProviderOpenAI, "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "opencode auth login openai") {
		t.Fatalf("err = %v, want remediation for openai", err)
	}
	if loginCalls != 0 {
		t.Fatalf("login fired %d times on a non-TTY, want 0", loginCalls)
	}
}

// Selected provider absent + TTY + a "y" answer → the login passthrough fires,
// then a single re-harvest recomputes the seed from the now-present provider.
func TestResolveOpencodeSeedMissingProviderTTYYesLogsInThenSeeds(t *testing.T) {
	before := testMaterial(t, "anthropic")          // openai missing
	after := testMaterial(t, "anthropic", "openai") // login added it
	var loginCalls []string
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: before}, harvestResult{m: after}),
		isTTY:    func() bool { return true },
		runLogin: func(_ context.Context, entryKey string) error { loginCalls = append(loginCalls, entryKey); return nil },
		stdin:    strings.NewReader("y\n"),
	}
	_, seed, err := deps.resolve(context.Background(), session.OpencodeProviderOpenAI, "", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(loginCalls) != 1 || loginCalls[0] != "openai" {
		t.Fatalf("login calls = %v, want exactly [openai]", loginCalls)
	}
	if got := seedKeys(t, seed); !got["openai"] {
		t.Fatalf("seed keys = %v, want the freshly logged-in openai", got)
	}
}

// Selected provider absent + TTY but the user declines → fail closed, no login.
func TestResolveOpencodeSeedMissingProviderTTYNoDeclines(t *testing.T) {
	m := testMaterial(t, "anthropic")
	loginCalls := 0
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { loginCalls++; return nil },
		stdin:    strings.NewReader("n\n"),
	}
	_, _, err := deps.resolve(context.Background(), session.OpencodeProviderOpenAI, "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "opencode auth login openai") {
		t.Fatalf("err = %v, want remediation for openai", err)
	}
	if loginCalls != 0 {
		t.Fatalf("login fired %d times after a 'no', want 0", loginCalls)
	}
}

// The Zen wire value ("opencode-zen") maps to the "opencode" auth.json entry key
// end-to-end: both the presence check and the --seed-providers filter resolve it.
func TestResolveOpencodeSeedZenWireMapsToOpencodeEntryKey(t *testing.T) {
	m := testMaterial(t, "anthropic", "opencode") // "opencode" is Zen's entry key
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("no login expected — zen is present"); return nil },
	}
	_, seed, err := deps.resolve(context.Background(), session.OpencodeProviderZen, "opencode-zen", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := seedKeys(t, seed)
	if !got["opencode"] {
		t.Fatalf("seed keys = %v, want the opencode (Zen) entry", got)
	}
	if got["anthropic"] {
		t.Fatalf("seed keys = %v, must NOT carry anthropic (only opencode-zen was requested)", got)
	}
}

// Flag plumbing: `sandbox opencode --seed-providers=...` registers the flag,
// defaults it to empty, and binds the value — the string runStartSession then
// hands straight to resolveOpencodeSeed.
func TestOpencodeCmdSeedProvidersFlagPlumbing(t *testing.T) {
	cmd := newOpencodeCmd()
	f := cmd.Flags().Lookup("seed-providers")
	if f == nil {
		t.Fatal("opencode command is missing the --seed-providers flag")
	}
	if f.DefValue != "" {
		t.Fatalf("--seed-providers default = %q, want empty (seed all)", f.DefValue)
	}
	if err := cmd.Flags().Parse([]string{"--seed-providers=openai,opencode-zen"}); err != nil {
		t.Fatalf("parse --seed-providers: %v", err)
	}
	if got, _ := cmd.Flags().GetString("seed-providers"); got != "openai,opencode-zen" {
		t.Fatalf("bound --seed-providers = %q, want the parsed CSV", got)
	}
}

// The other backends do NOT expose --seed-providers (it is opencode-only).
func TestSeedProvidersFlagIsOpencodeOnly(t *testing.T) {
	if newClaudeRemoteCmd().Flags().Lookup("seed-providers") != nil {
		t.Error("claude must not expose --seed-providers")
	}
	if newCodexCmd().Flags().Lookup("seed-providers") != nil {
		t.Error("codex must not expose --seed-providers")
	}
}

// Empty --provider + a local login WITHOUT anthropic auto-picks a selectable
// provider from the harvest (instead of the old hardcoded-anthropic fail-closed)
// and announces the pick on stderr. Non-selectable entries (github-copilot)
// still ride along in the seed; they just can't BE the pick.
func TestResolveOpencodeSeedEmptyProviderAutoPicks(t *testing.T) {
	m := testMaterial(t, "github-copilot", "opencode") // no anthropic
	var stderr bytes.Buffer
	deps := opencodeSeedDeps{
		harvest: harvestSeq(harvestResult{m: m}),
		isTTY:   func() bool { return true },
		runLogin: func(context.Context, string) error {
			t.Fatal("no login expected — a selectable provider is present")
			return nil
		},
		readPref: func() string { return "" },
	}
	provider, seed, err := deps.resolve(context.Background(), "", "", &stderr)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if provider != session.OpencodeProviderZen {
		t.Fatalf("provider = %q, want %q (the only selectable harvested provider)", provider, session.OpencodeProviderZen)
	}
	if got := seedKeys(t, seed); !got["opencode"] || !got["github-copilot"] {
		t.Fatalf("seed keys = %v, want the whole login (opencode + github-copilot)", got)
	}
	if !strings.Contains(stderr.String(), `defaulting to "opencode-zen"`) {
		t.Fatalf("stderr = %q, want the announced default", stderr.String())
	}
}

// The remembered last-used provider beats the preference order when it is still
// logged in.
func TestResolveOpencodeSeedEmptyProviderPrefersLastUsed(t *testing.T) {
	m := testMaterial(t, "anthropic", "openai")
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("no login expected"); return nil },
		readPref: func() string { return session.OpencodeProviderOpenAI },
	}
	provider, _, err := deps.resolve(context.Background(), "", "", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if provider != session.OpencodeProviderOpenAI {
		t.Fatalf("provider = %q, want the remembered openai (beating anthropic preference)", provider)
	}
}

// A remembered provider the user has since LOGGED OUT of is ignored; the pick
// falls through to the preference order.
func TestResolveOpencodeSeedEmptyProviderIgnoresLoggedOutPref(t *testing.T) {
	m := testMaterial(t, "opencode") // anthropic remembered, no longer logged in
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("no login expected"); return nil },
		readPref: func() string { return session.OpencodeProviderAnthropic },
	}
	provider, _, err := deps.resolve(context.Background(), "", "", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if provider != session.OpencodeProviderZen {
		t.Fatalf("provider = %q, want %q (logged-out pref ignored)", provider, session.OpencodeProviderZen)
	}
}

// Empty --provider + a --seed-providers filter picks a provider INSIDE the
// filter (defaulting to an excluded provider would only re-error downstream).
func TestResolveOpencodeSeedEmptyProviderFilterNarrowsPick(t *testing.T) {
	m := testMaterial(t, "anthropic", "openai")
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("no login expected"); return nil },
		readPref: func() string { return "" },
	}
	provider, seed, err := deps.resolve(context.Background(), "", "openai", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if provider != session.OpencodeProviderOpenAI {
		t.Fatalf("provider = %q, want openai (the in-filter provider)", provider)
	}
	got := seedKeys(t, seed)
	if !got["openai"] || got["anthropic"] {
		t.Fatalf("seed keys = %v, want ONLY openai (the filter)", got)
	}
}

// Empty --provider with NOTHING selectable in the login keeps the original
// anthropic-default remediation verbatim (fail closed, no login attempt on a
// non-TTY).
func TestResolveOpencodeSeedEmptyProviderNothingSelectableKeepsRemediation(t *testing.T) {
	m := testMaterial(t, "github-copilot") // no anthropic/openai/opencode
	loginCalls := 0
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return false },
		runLogin: func(context.Context, string) error { loginCalls++; return nil },
		readPref: func() string { return "" },
	}
	provider, _, err := deps.resolve(context.Background(), "", "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "opencode auth login anthropic") {
		t.Fatalf("err = %v, want the anthropic remediation (nothing selectable to default to)", err)
	}
	if provider != "" {
		t.Fatalf("provider = %q, want \"\" on the error path", provider)
	}
	if loginCalls != 0 {
		t.Fatalf("login fired %d times on a non-TTY, want 0", loginCalls)
	}
}

// An explicit --provider skips the pick entirely: the pref seam is never
// consulted and no default is announced.
func TestResolveOpencodeSeedExplicitProviderSkipsPick(t *testing.T) {
	m := testMaterial(t, "opencode")
	var stderr bytes.Buffer
	deps := opencodeSeedDeps{
		harvest:  harvestSeq(harvestResult{m: m}),
		isTTY:    func() bool { return true },
		runLogin: func(context.Context, string) error { t.Fatal("no login expected"); return nil },
		readPref: func() string { t.Fatal("readPref must not be consulted for an explicit --provider"); return "" },
	}
	provider, _, err := deps.resolve(context.Background(), session.OpencodeProviderZen, "", &stderr)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if provider != session.OpencodeProviderZen {
		t.Fatalf("provider = %q, want the explicit %q", provider, session.OpencodeProviderZen)
	}
	if strings.Contains(stderr.String(), "defaulting to") {
		t.Fatalf("stderr = %q, must NOT announce a default for an explicit --provider", stderr.String())
	}
}

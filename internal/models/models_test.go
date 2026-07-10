package models

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// minimal but real-shaped models.dev JSON.
const sampleAPI = `{
  "anthropic": {
    "name": "Anthropic",
    "models": {
      "claude-opus-4-8": {
        "id": "claude-opus-4-8",
        "name": "Claude Opus 4.8",
        "limit": { "context": 1000000, "output": 64000 },
        "cost": { "input": 5, "output": 25, "cache_read": 0.5, "cache_write": 6.25 }
      },
      "claude-sonnet-4-6": {
        "id": "claude-sonnet-4-6",
        "name": "Claude Sonnet 4.6",
        "limit": { "context": 1000000, "output": 64000 },
        "cost": { "input": 3, "output": 15 }
      }
    }
  }
}`

func newTestProvider(url, dir string) *provider {
	return &provider{
		apiURL:    url,
		cachePath: func() string { return filepath.Join(dir, "models.json") },
		ttl:       time.Hour,
	}
}

// prime triggers the provider's first load — which, on a cold/stale cache,
// serves the fallback and kicks the async network refresh (C10) — and waits
// for that refresh to settle, so tests can assert on fetched values
// deterministically.
func prime(p *provider) {
	_ = p.limit("prime")
	p.awaitRefresh()
}

func TestOnlineFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleAPI))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL, t.TempDir())
	prime(p)
	got := p.limit("claude-opus-4-8")
	if got.ContextLimit != 1000000 {
		t.Errorf("ContextLimit = %d, want 1000000", got.ContextLimit)
	}
	if got.InputPrice != 5 || got.OutputPrice != 25 {
		t.Errorf("prices = %v/%v, want 5/25", got.InputPrice, got.OutputPrice)
	}
}

func TestOfflineFallback(t *testing.T) {
	// Unreachable URL, empty temp cache dir: must fall back to static table.
	p := newTestProvider("http://127.0.0.1:0", t.TempDir())

	// Opus gets 1M context (S5).
	got := p.limit("claude-opus-4-8")
	if got.ContextLimit != 1000000 {
		t.Errorf("opus ContextLimit = %d, want 1000000", got.ContextLimit)
	}
	if got.InputPrice != 5 || got.OutputPrice != 25 {
		t.Errorf("opus prices = %v/%v, want 5/25", got.InputPrice, got.OutputPrice)
	}

	// Sonnet gets 200k context.
	got = p.limit("claude-sonnet-4-8")
	if got.ContextLimit != staticContextLimit {
		t.Errorf("sonnet ContextLimit = %d, want %d", got.ContextLimit, staticContextLimit)
	}
	if got.InputPrice != 3 || got.OutputPrice != 15 {
		t.Errorf("sonnet prices = %v/%v, want 3/15", got.InputPrice, got.OutputPrice)
	}

	// Haiku gets 200k context.
	got = p.limit("claude-haiku-4-8")
	if got.ContextLimit != staticContextLimit {
		t.Errorf("haiku ContextLimit = %d, want %d", got.ContextLimit, staticContextLimit)
	}
	if got.InputPrice != 1 || got.OutputPrice != 5 {
		t.Errorf("haiku prices = %v/%v, want 1/5", got.InputPrice, got.OutputPrice)
	}
}

// multiProviderAPI mirrors the real models.dev shape where ONE model id is listed
// by several providers with conflicting context limits and prices — the case that
// made limit() nondeterministic (a random provider's entry won).
const multiProviderAPI = `{
  "azure":     {"name":"Azure","models":{"claude-opus-4-8":{"id":"claude-opus-4-8","limit":{"context":200000,"output":64000},"cost":{"input":5,"output":25}}}},
  "venice":    {"name":"Venice","models":{"claude-opus-4-8":{"id":"claude-opus-4-8","limit":{"context":1000000,"output":64000},"cost":{"input":6,"output":30}}}},
  "anthropic": {"name":"Anthropic","models":{"claude-opus-4-8":{"id":"claude-opus-4-8","limit":{"context":1000000,"output":64000},"cost":{"input":5,"output":25}}}},
  "llmgateway":{"name":"LLMGateway","models":{"claude-opus-4-8":{"id":"claude-opus-4-8","limit":{"context":1000000,"output":64000},"cost":{"input":5,"output":25}}}}
}`

// A model listed by many providers must resolve to ONE stable, canonical entry —
// anthropic's (1M context, 5/25), never azure's stale 200k or venice's 6/30 — on
// EVERY call. Pre-fix, the map range returned a random provider, so the reported
// context limit and prices flickered between runs (a real status-line bug caught
// by the cross-backend UX-parity test).
func TestMultiProviderDeterministicCanonical(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(multiProviderAPI))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL, t.TempDir())
	prime(p)
	for i := 0; i < 64; i++ { // many calls: the old map-range nondeterminism would surface
		got := p.limit("claude-opus-4-8")
		if got.ContextLimit != 1000000 || got.InputPrice != 5 || got.OutputPrice != 25 {
			t.Fatalf("call %d: got %+v, want anthropic's {1000000, 5, 25} (deterministic + canonical)", i, got)
		}
	}
}

// nonClaudeAPI mirrors how models.dev keys opencode/openai models: the model id
// verbatim (lowercased), dots preserved, NO claude- prefix. Pre-fix, normalize()
// prefixed every id with claude- and swapped dots for dashes, so none of these
// keys could ever match and every opencode/openai session hit the 200k/$0 static
// fallback (wrong ctx% + cost in the TUI).
const nonClaudeAPI = `{
  "opencode": {"name":"opencode","models":{
    "grok-code": {"id":"grok-code","limit":{"context":256000,"output":8192},"cost":{"input":0.2,"output":1.5}},
    "gpt-4.1":   {"id":"gpt-4.1","limit":{"context":1047576,"output":32768},"cost":{"input":2,"output":8}}
  }},
  "openai": {"name":"OpenAI","models":{
    "gpt-4o": {"id":"gpt-4o","limit":{"context":128000,"output":16384},"cost":{"input":2.5,"output":10}}
  }},
  "anthropic": {"name":"Anthropic","models":{
    "claude-opus-4-8":   {"id":"claude-opus-4-8","limit":{"context":1000000,"output":64000},"cost":{"input":5,"output":25}},
    "claude-sonnet-4-6": {"id":"claude-sonnet-4-6","limit":{"context":1000000,"output":64000},"cost":{"input":3,"output":15}}
  }}
}`

// A model id is resolved to its real entry regardless of vendor: non-Claude ids
// (opencode/openai, keyed verbatim) must NOT be forced through the claude- alias.
// Claude ids and Anthropic shorthand must still resolve exactly as before.
func TestLookupCoversNonClaudeAndClaude(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(nonClaudeAPI))
	}))
	defer srv.Close()

	cases := []struct {
		name string
		id   string
		want Info
	}{
		// Non-Claude ids resolve to their real limits AND pricing (the bug).
		{"opencode grok-code", "grok-code", Info{ContextLimit: 256000, InputPrice: 0.2, OutputPrice: 1.5}},
		{"opencode gpt-4.1 dotted", "gpt-4.1", Info{ContextLimit: 1047576, InputPrice: 2, OutputPrice: 8}},
		{"openai gpt-4o", "gpt-4o", Info{ContextLimit: 128000, InputPrice: 2.5, OutputPrice: 10}},
		{"non-claude case-insensitive", "GROK-CODE", Info{ContextLimit: 256000, InputPrice: 0.2, OutputPrice: 1.5}},
		// Claude exact id + shorthand still resolve identically (no regression).
		{"claude exact", "claude-opus-4-8", Info{ContextLimit: 1000000, InputPrice: 5, OutputPrice: 25}},
		{"claude dated suffix", "claude-opus-4-8-20260101", Info{ContextLimit: 1000000, InputPrice: 5, OutputPrice: 25}},
		{"claude shorthand dotted", "opus-4.8", Info{ContextLimit: 1000000, InputPrice: 5, OutputPrice: 25}},
		{"claude shorthand sonnet", "sonnet-4.6", Info{ContextLimit: 1000000, InputPrice: 3, OutputPrice: 15}},
	}
	for _, c := range cases {
		// fresh provider per case so each performs its own resolution.
		p := newTestProvider(srv.URL, t.TempDir())
		prime(p)
		if got := p.limit(c.id); got != c.want {
			t.Errorf("%s: limit(%q) = %+v, want %+v", c.name, c.id, got, c.want)
		}
	}
}

// An id absent from every provider table still hits the documented static
// fallback (200k ctx, $0) — even a non-Claude id that no longer gets prefixed.
func TestUnknownNonClaudeStillFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(nonClaudeAPI))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL, t.TempDir())
	prime(p)
	got := p.limit("big-pickle") // real-world example from the review
	if got != (Info{ContextLimit: staticContextLimit}) {
		t.Errorf("limit(big-pickle) = %+v, want {200000, 0, 0}", got)
	}
}

func TestLookupKeysFunc(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"gpt-4.1", []string{"gpt-4.1", "claude-gpt-4-1"}},        // raw first, alias fallback
		{"grok-code", []string{"grok-code", "claude-grok-code"}},  // dots absent: still both forms
		{"claude-opus-4-8", []string{"claude-opus-4-8"}},          // already canonical: single key
		{"claude-opus-4-8-20260101", []string{"claude-opus-4-8"}}, // dated suffix stripped
		{"opus-4.8", []string{"opus-4.8", "claude-opus-4-8"}},     // shorthand: raw then alias
		{"  ", nil}, // blank
	}
	for _, c := range cases {
		got := lookupKeys(c.in)
		if len(got) != len(c.want) {
			t.Errorf("lookupKeys(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("lookupKeys(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestUnknownIDFallback(t *testing.T) {
	p := newTestProvider("http://127.0.0.1:0", t.TempDir())
	got := p.limit("gpt-4o")
	if got.ContextLimit != staticContextLimit {
		t.Errorf("ContextLimit = %d, want %d", got.ContextLimit, staticContextLimit)
	}
	if got.InputPrice != 0 || got.OutputPrice != 0 {
		t.Errorf("prices = %v/%v, want 0/0", got.InputPrice, got.OutputPrice)
	}
}

func TestNormalizationOnline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleAPI))
	}))
	defer srv.Close()

	cases := []struct {
		id   string
		want int
	}{
		{"claude-opus-4-8", 1000000},
		{"claude-opus-4-8-20260101", 1000000}, // dated suffix stripped
		{"opus-4.8", 1000000},                 // short alias
		{"OPUS-4.8", 1000000},                 // case-insensitive
		{"sonnet-4.6", 1000000},
	}
	for _, c := range cases {
		// fresh provider per case so each performs its own resolution.
		p := newTestProvider(srv.URL, t.TempDir())
		prime(p)
		if got := p.limit(c.id).ContextLimit; got != c.want {
			t.Errorf("limit(%q).ContextLimit = %d, want %d", c.id, got, c.want)
		}
	}
}

func TestNormalizeFunc(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-8":          "claude-opus-4-8",
		"claude-opus-4-8-20260101": "claude-opus-4-8",
		"opus-4.8":                 "claude-opus-4-8",
		"sonnet-4.6":               "claude-sonnet-4-6",
		"  Claude-Opus-4-8 ":       "claude-opus-4-8",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCacheHitAfterServerStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleAPI))
	}))
	dir := t.TempDir()

	// First provider fetches online (async, C10) and writes the cache.
	p1 := newTestProvider(srv.URL, dir)
	prime(p1)
	if got := p1.limit("claude-opus-4-8").ContextLimit; got != 1000000 {
		t.Fatalf("online ContextLimit = %d, want 1000000", got)
	}

	cacheFile := filepath.Join(dir, "models.json")
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}

	srv.Close() // server gone; fresh cache within TTL must still serve 1000000.

	p2 := newTestProvider(srv.URL, dir)
	if got := p2.limit("claude-opus-4-8").ContextLimit; got != 1000000 {
		t.Errorf("cached ContextLimit = %d, want 1000000 (should not hit static fallback)", got)
	}
}

func TestStaleCacheUsedWhenFetchFails(t *testing.T) {
	dir := t.TempDir()
	// Write a cache file but make it older than the TTL.
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(sampleAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "models.json"), old, old); err != nil {
		t.Fatal(err)
	}

	// Stale cache + unreachable server: fetch fails, stale cache is used —
	// synchronously, on the very first call (it's disk-local).
	p := newTestProvider("http://127.0.0.1:0", dir)
	if got := p.limit("claude-opus-4-8").ContextLimit; got != 1000000 {
		t.Errorf("stale-cache ContextLimit = %d, want 1000000", got)
	}
}

// C10 regression: the first limit() on a cold cache must NOT block on the
// network — it is called from TUI reducer paths, and a slow/hanging models.dev
// froze the UI up to the 5s HTTP timeout. The static fallback is served
// immediately; the refresh happens in the background.
func TestColdLimitDoesNotBlockOnNetwork(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hang until the test ends
	}))
	defer func() { close(release); srv.Close() }()

	p := newTestProvider(srv.URL, t.TempDir())
	start := time.Now()
	got := p.limit("claude-opus-4-8")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("cold limit() blocked %v on the hanging fetch", d)
	}
	if got.ContextLimit != 1000000 { // static fallback knows opus is 1M
		t.Errorf("fallback ContextLimit = %d, want 1000000", got.ContextLimit)
	}
}

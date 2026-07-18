package models

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Info is the resolved context limit and per-million-token USD prices for a model.
type Info struct {
	ContextLimit int     // tokens; e.g. 200000 or 1000000
	InputPrice   float64 // USD per million input tokens
	OutputPrice  float64 // USD per million output tokens
}

// Limit returns the Info for a model id (as emitted by session.started.model).
// It consults a cached copy of the models.dev table (fetched + cached on first
// use with a TTL), falling back to a static table (200k context + known Claude
// prices) when offline or when the id is unknown.
func Limit(modelID string) Info { return defaultProvider.limit(modelID) }

const staticContextLimit = 200000

// apiURL is the models.dev endpoint. Overridable for tests.
var apiURL = "https://models.dev/api.json"

// cachePath returns the on-disk path of the cached models table. Overridable for
// tests. Errors (e.g. no home dir) yield an empty path, which disables caching.
var cachePath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "sandbox", "models.json")
}

// cacheTTL bounds how long a cache file is considered fresh. Reachable from tests.
var cacheTTL = 24 * time.Hour

// httpTimeout bounds a single fetch so Limit never blocks forever.
var httpTimeout = 5 * time.Second

// provider resolves model info from models.dev with an on-disk cache and a
// static fallback. It reads the package vars lazily (when its own fields are
// unset) so tests can override apiURL/cachePath/cacheTTL before first use.
type provider struct {
	apiURL    string        // when "", read apiURL var
	cachePath func() string // when nil, read cachePath var
	ttl       time.Duration // when 0, read cacheTTL var
	client    *http.Client  // when nil, build from httpTimeout

	mu          sync.Mutex
	table       table         // parsed models.dev table, nil until first load
	loaded      bool          // whether an in-memory load was attempted this process
	refreshDone chan struct{} // non-nil once an async refresh was kicked; closed when it settles
}

// defaultProvider is the package-level instance backing Limit.
var defaultProvider = &provider{}

// table is provider id -> model id (lowercase) -> model entry.
type table map[string]map[string]modelEntry

type modelEntry struct {
	ContextLimit int
	InputPrice   float64
	OutputPrice  float64
}

// --- models.dev JSON shapes (only the fields we need) ---

type apiProvider struct {
	Models map[string]apiModel `json:"models"`
}

type apiModel struct {
	Limit apiLimit `json:"limit"`
	Cost  apiCost  `json:"cost"`
}

type apiLimit struct {
	Context int `json:"context"`
}

type apiCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

func (p *provider) effAPIURL() string {
	if p.apiURL != "" {
		return p.apiURL
	}
	return apiURL
}

func (p *provider) effCachePath() string {
	if p.cachePath != nil {
		return p.cachePath()
	}
	return cachePath()
}

func (p *provider) effTTL() time.Duration {
	if p.ttl != 0 {
		return p.ttl
	}
	return cacheTTL
}

func (p *provider) effClient() *http.Client {
	if p.client != nil {
		return p.client
	}
	return &http.Client{Timeout: httpTimeout}
}

// canonicalProviders are models.dev provider ids to trust first when one model id
// is listed by several providers. The same model is commonly mirrored by
// aggregators/clouds (azure, venice, llmgateway, …) — sometimes with a STALE
// context limit (azure lists claude-opus-4-8 at 200k vs anthropic's 1M) or
// different prices (venice: 6/30 vs anthropic: 5/25). The model's true vendor is
// authoritative, so prefer it; `opencode` is the canonical source for this CLI's
// opencode backend models.
var canonicalProviders = []string{"anthropic", "opencode", "openai", "google", "google-vertex"}

// providerRank orders providers for a deterministic pick: canonical providers in
// listed order first, every other provider after (compared lexicographically by
// name, below). Lower is preferred.
func providerRank(prov string) int {
	for i, c := range canonicalProviders {
		if c == prov {
			return i
		}
	}
	return len(canonicalProviders)
}

// preferProvider reports whether a should win over b for the same model id: lower
// rank first, then lexicographically smaller name. Total order ⇒ deterministic.
func preferProvider(a, b string) bool {
	ra, rb := providerRank(a), providerRank(b)
	if ra != rb {
		return ra < rb
	}
	return a < b
}

func (p *provider) limit(modelID string) Info {
	tbl := p.load()
	if tbl != nil {
		// Try each candidate key (raw id first, claude- alias second) so
		// non-Claude ids resolve to their real limits/prices instead of the
		// static fallback. Both context limit AND pricing come from the single
		// Info returned here, so covering this path covers both.
		for _, key := range lookupKeys(modelID) {
			if info, ok := lookupEntry(tbl, key); ok {
				return info
			}
		}
	}
	return staticFallback(normalize(modelID))
}

// lookupEntry finds the best provider entry for an exact table key.
// models.dev groups by provider and the SAME model id appears under many
// providers (anthropic, azure, venice, …) with differing limits/prices.
// Ranging the provider map returned a RANDOM match, so a model's reported
// context limit and prices flickered between runs (a real status-line bug).
// Pick deterministically, preferring the model's true vendor, and use that
// one provider's complete entry so limit + prices stay coherent.
func lookupEntry(tbl table, key string) (Info, bool) {
	best := ""
	for prov := range tbl {
		if _, ok := tbl[prov][key]; !ok {
			continue
		}
		if best == "" || preferProvider(prov, best) {
			best = prov
		}
	}
	if best == "" {
		return Info{}, false
	}
	return Info(tbl[best][key]), true // modelEntry and Info are field-identical
}

// load returns the parsed table, reading the disk cache and kicking an async
// network refresh as needed. It never returns an error: when no table is
// available yet it returns nil and callers use the static fallback. It never
// blocks on the network (C10): Limit is called from TUI reducer paths, and
// holding p.mu across a fetch froze the UI up to httpTimeout on the first
// cold/stale-cache call of the day.
func (p *provider) load() table {
	p.mu.Lock()

	if p.loaded {
		defer p.mu.Unlock()
		return p.table
	}
	p.loaded = true

	cp := p.effCachePath()

	// Fresh cache within TTL: use it, no fetch needed. Disk-local ⇒ fast
	// enough to stay synchronous.
	if cp != "" {
		if info, err := os.Stat(cp); err == nil && time.Since(info.ModTime()) < p.effTTL() {
			if tbl, err := readCache(cp); err == nil {
				p.table = tbl
				p.mu.Unlock()
				return tbl
			}
		}
	}

	// Stale or missing cache: serve the stale cache (or nil → static fallback)
	// NOW and refresh from the network in the background. Later Limit calls
	// pick up the refreshed table.
	if cp != "" {
		if tbl, err := readCache(cp); err == nil {
			p.table = tbl
		}
	}
	tbl := p.table
	done := make(chan struct{})
	p.refreshDone = done
	p.mu.Unlock()

	go func() {
		defer close(done)
		p.refresh(cp)
	}()
	return tbl
}

// refresh fetches models.dev, rewrites the cache, and swaps the in-memory
// table. On failure the stale cache / static fallback already being served
// stays in force — same silent degradation as before, just off the caller's
// goroutine.
func (p *provider) refresh(cp string) {
	raw, tbl, err := p.fetch()
	if err != nil {
		return
	}
	if cp != "" {
		writeCache(cp, raw)
	}
	p.mu.Lock()
	p.table = tbl
	p.mu.Unlock()
}

// awaitRefresh blocks until the async refresh kicked by the first load (if
// any) settles. Test seam; production callers never need it.
func (p *provider) awaitRefresh() {
	p.mu.Lock()
	done := p.refreshDone
	p.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (p *provider) fetch() ([]byte, table, error) {
	resp, err := p.effClient().Get(p.effAPIURL())
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("models.dev: unexpected status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	tbl, err := parseTable(raw)
	if err != nil {
		return nil, nil, err
	}
	return raw, tbl, nil
}

func parseTable(raw []byte) (table, error) {
	var providers map[string]apiProvider
	if err := json.Unmarshal(raw, &providers); err != nil {
		return nil, err
	}
	tbl := make(table, len(providers))
	for pid, ap := range providers {
		if len(ap.Models) == 0 {
			continue
		}
		models := make(map[string]modelEntry, len(ap.Models))
		for mid, m := range ap.Models {
			models[strings.ToLower(mid)] = modelEntry{
				ContextLimit: m.Limit.Context,
				InputPrice:   m.Cost.Input,
				OutputPrice:  m.Cost.Output,
			}
		}
		tbl[pid] = models
	}
	return tbl, nil
}

func readCache(path string) (table, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseTable(raw)
}

func writeCache(path string, raw []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o644)
}

// datedSuffix matches a trailing "-YYYYMMDD" snapshot suffix.
var datedSuffix = regexp.MustCompile(`-\d{8}$`)

// lookupKeys returns the models.dev table keys to try for a model id, in order.
// The RAW lowercase id (dated suffix stripped, dots preserved) is tried FIRST so
// non-Claude ids — which models.dev keys verbatim, e.g. opencode/openai's
// "gpt-4.1", "grok-code" — resolve to their real limits/prices. The claude-
// alias form from normalize (dots→dashes, claude- prefix) is only a fallback,
// so Anthropic shorthand like "opus-4.8" still maps to "claude-opus-4-8".
// Blindly prefixing every id with claude- (the old behavior) made every
// non-Anthropic lookup miss and silently hit the 200k/$0 static fallback.
func lookupKeys(modelID string) []string {
	raw := datedSuffix.ReplaceAllString(strings.ToLower(strings.TrimSpace(modelID)), "")
	if raw == "" {
		return nil
	}
	keys := []string{raw}
	if norm := normalize(modelID); norm != raw {
		keys = append(keys, norm)
	}
	return keys
}

// normalize maps a session.started model id to a models.dev key (lowercase).
//   - exact ids pass through: claude-opus-4-8
//   - dated suffixes are stripped: claude-opus-4-8-20260101 -> claude-opus-4-8
//   - short aliases gain the claude- prefix and dots become dashes:
//     opus-4.8 -> claude-opus-4-8, sonnet-4.6 -> claude-sonnet-4-6
func normalize(modelID string) string {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return id
	}
	id = datedSuffix.ReplaceAllString(id, "")
	id = strings.ReplaceAll(id, ".", "-")
	if !strings.HasPrefix(id, "claude-") {
		id = "claude-" + id
	}
	return id
}

// staticFallback returns deterministic, network-free info for a normalized key.
// Known Claude families get their published prices and context limits; anything
// else gets 200k context and zero prices.
func staticFallback(key string) Info {
	in, out := 0.0, 0.0
	ctx := staticContextLimit
	switch {
	case strings.Contains(key, "opus"):
		in, out = 5, 25
		ctx = 1000000 // claude-opus-4-8 is 1M; future opuses likely stay 1M+
	case strings.Contains(key, "sonnet"):
		in, out = 3, 15
	case strings.Contains(key, "haiku"):
		in, out = 1, 5
	}
	return Info{ContextLimit: ctx, InputPrice: in, OutputPrice: out}
}

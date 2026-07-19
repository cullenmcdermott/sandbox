package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

func TestFooterItemImplementsItem(t *testing.T) {
	var _ list.Item = (*FooterItem)(nil)
}

// A fully-populated footer renders the per-turn outcome grammar, in order and
// with the same segment vocabulary as the dashboard turnFooter.
func TestFooterRendersOutcome(t *testing.T) {
	restoreTheme(t)
	f := NewFooterItem(&TurnFooter{
		Model: "Opus 4.8", Backend: "anthropic", Elapsed: 12 * time.Second,
		InputTokens: 3100, OutputTokens: 820, CostUSD: 0.04,
	})
	got := ansi.Strip(f.Render(120))
	want := "◇ Opus 4.8 · via anthropic · 12s · ↑3.1k ↓820 · $0.04"
	if got != want {
		t.Fatalf("footer mismatch:\n got %q\nwant %q", got, want)
	}
}

// An all-zero footer has nothing to summarize and renders "" — never a bare
// "◇" or an orphan focus gutter.
func TestFooterEmptySafe(t *testing.T) {
	restoreTheme(t)
	if got := NewFooterItem(&TurnFooter{}).Render(80); got != "" {
		t.Errorf("empty footer rendered %q, want \"\"", got)
	}
	if got := NewFooterItem(nil).Render(80); got != "" {
		t.Errorf("nil footer rendered %q, want \"\"", got)
	}
	e := NewFooterItem(&TurnFooter{})
	e.SetFocused(true)
	if got := e.Render(80); got != "" {
		t.Errorf("focused empty footer rendered %q, want \"\"", got)
	}
}

// Each segment is independently omitted when its field is zero.
func TestFooterPartialSegments(t *testing.T) {
	restoreTheme(t)
	cases := []struct {
		name string
		f    TurnFooter
		want string
	}{
		{"model-only", TurnFooter{Model: "Opus 4.8"}, "◇ Opus 4.8"},
		{"backend-only", TurnFooter{Backend: "opencode"}, "◇ via opencode"},
		{"elapsed-only", TurnFooter{Elapsed: 90 * time.Second}, "◇ 1m30s"},
		{"tokens-in-only", TurnFooter{InputTokens: 12000}, "◇ ↑12k ↓0"},
		{"tokens-out-only", TurnFooter{OutputTokens: 500}, "◇ ↑0 ↓500"},
		{"cost-only", TurnFooter{CostUSD: 1.5}, "◇ $1.50"},
		{"zero-cost-dropped", TurnFooter{Model: "m", CostUSD: 0}, "◇ m"},
		{"negative-cost-dropped", TurnFooter{Model: "m", CostUSD: -1}, "◇ m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ansi.Strip(NewFooterItem(&c.f).Render(120))
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The footer truncates to the declared width, blurred or focused, down to the
// suite's minimum focus-safe width, and stays single-line.
func TestFooterWidthSafe(t *testing.T) {
	restoreTheme(t)
	f := NewFooterItem(&TurnFooter{
		Model: "Opus 4.8", Backend: "anthropic", Elapsed: 3 * time.Minute,
		InputTokens: 1_250_000, OutputTokens: 84000, CostUSD: 12.34,
	})
	for _, w := range []int{8, 20, 40, 80, 140} {
		for _, focused := range []bool{false, true} {
			f.SetFocused(focused)
			out := f.Render(w)
			widthSafe(t, "footer", out, w)
			if strings.Contains(out, "\n") {
				t.Errorf("footer is multi-line at width %d: %q", w, out)
			}
		}
	}
	// Degenerate widths (below the focus-gutter budget) stay safe when blurred.
	f.SetFocused(false)
	for _, w := range []int{1, 3} {
		widthSafe(t, "footer-narrow", f.Render(w), w)
	}
}

// A theme swap re-skins the cached footer (epoch fold): same structure, new ANSI.
func TestFooterThemeSwapReskins(t *testing.T) {
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	theme.ApplyForBackground(true)
	f := NewFooterItem(&TurnFooter{Model: "Opus 4.8", CostUSD: 0.02})
	dark := f.Render(80)
	theme.ApplyForBackground(false)
	light := f.Render(80)
	if dark == light {
		t.Error("theme swap did not change footer ANSI (stale palette re-served)")
	}
	if ansi.Strip(dark) != ansi.Strip(light) {
		t.Error("theme swap changed footer structure (should be color-only)")
	}
}

// Focus adds the shared gutter bar and keeps the line within width.
func TestFooterFocusGutter(t *testing.T) {
	restoreTheme(t)
	f := NewFooterItem(&TurnFooter{Model: "Opus 4.8", Backend: "anthropic"})
	f.SetFocused(true)
	got := ansi.Strip(f.Render(80))
	if !strings.HasPrefix(got, "▌ ") {
		t.Errorf("focused footer missing gutter bar: %q", got)
	}
	widthSafe(t, "footer-focused", f.Render(80), 80)
}

// COUNTER: the footer caches its render and only bumps its version when the
// outcome (or focus) actually changes; a no-op set does not bump.
func TestFooterCachingAndBump(t *testing.T) {
	restoreTheme(t)
	f := NewFooterItem(&TurnFooter{Model: "Opus 4.8"})
	v0 := f.Version()
	_ = f.Render(80)
	f.SetFocused(false) // no-op (already blurred)
	if f.Version() != v0 {
		t.Errorf("no-op SetFocused bumped version")
	}
	f.SetFooter(&TurnFooter{Model: "Opus 4.8", CostUSD: 0.10})
	if f.Version() == v0 {
		t.Errorf("SetFooter did not bump version")
	}
	// An unchanged re-render returns the cached string byte-for-byte.
	a := f.Render(80)
	b := f.Render(80)
	if a != b {
		t.Errorf("cached re-render differs: %q vs %q", a, b)
	}
}

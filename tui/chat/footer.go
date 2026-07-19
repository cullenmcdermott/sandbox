package chat

// footer.go — the per-turn outcome footer: a dim one-line summary of a completed
// turn, e.g. "◇ Opus 4.8 · via anthropic · 12s · ↑3.1k ↓820 · $0.04". It mirrors
// the dashboard's turnFooter (§D): a "◇" diamond, the model, the backend, the
// elapsed clock, token in/out, and cost — each segment omitted when it has
// nothing to say, and the whole line collapsing to "" when there is nothing to
// summarize at all (never a bare "◇ —", which reads as a glitch before
// session.started delivers the model). The host resolves the display-ready model
// + backend labels — this package never names a session backend — and the footer
// only formats and frames them, reusing the same kit.FormatTokens /
// kit.FormatCost the dashboard footer uses so the numbers read identically.

import (
	"strings"
	"time"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// TurnFooter is the plain data struct the transcript reducer mutates as a turn
// progresses (tokens and cost accrue, the clock grows) and freezes when the turn
// ends. Every field is optional: a zero field drops its segment, and an all-zero
// TurnFooter renders "".
type TurnFooter struct {
	// Model is the display-ready model name (e.g. "Opus 4.8"), already shortened
	// by the host. Empty drops the model segment.
	Model string
	// Backend is the display-ready backend label (e.g. "anthropic"), already
	// resolved by the host. Empty drops the "via <backend>" segment.
	Backend string
	// Elapsed is the turn's wall-clock duration. Zero (or negative) drops the
	// clock segment.
	Elapsed time.Duration
	// InputTokens and OutputTokens are the turn's token counts. Both zero drops
	// the "↑… ↓…" segment.
	InputTokens  int
	OutputTokens int
	// CostUSD is the turn's cost in US dollars. Zero (or negative) drops the cost
	// segment.
	CostUSD float64
}

// FooterItem is the per-turn outcome footer block.
type FooterItem struct {
	*list.Versioned

	f       *TurnFooter
	focused bool

	cache section
}

// NewFooterItem builds a per-turn outcome footer from the given data.
func NewFooterItem(f *TurnFooter) *FooterItem {
	return &FooterItem{Versioned: list.NewVersioned(), f: f}
}

// SetFooter replaces the footer data (a turn-outcome update) and bumps.
func (f *FooterItem) SetFooter(nf *TurnFooter) {
	f.f = nf
	f.cache.valid = false
	f.Bump()
}

// SetFocused marks the footer focused (a left gutter bar).
func (f *FooterItem) SetFocused(b bool) {
	if f.focused == b {
		return
	}
	f.focused = b
	f.cache.valid = false
	f.Bump()
}

// Focused reports the focus state.
func (f *FooterItem) Focused() bool { return f.focused }

// Render draws the one-line footer within width columns, or "" when there is
// nothing to summarize.
func (f *FooterItem) Render(width int) string {
	if width < 1 {
		width = 1
	}
	// The summary string fully captures every data field, so it doubles as the
	// content hash — matching the other items' fnv-over-content cache keys.
	text := footerSummary(f.f)
	srcHash := fnv64(text)
	extra := extraKey(theme.Epoch(), flagBits(f.focused))
	if f.cache.hit(width, srcHash, extra) {
		return f.cache.out
	}
	var out string
	if text != "" {
		// Render (and truncate) at the focus-reduced width, then add the gutter, so
		// a focused footer's total stays within the caller's width. An empty footer
		// gets no gutter — an orphan "▌ " reads as a glitch.
		out = clampFocus(styFooter.Render(truncate(text, focusWidth(width, f.focused))), f.focused)
	}
	f.cache.store(width, srcHash, extra, out)
	return out
}

// footerSummary builds the unstyled "◇ …" summary, omitting empty segments and
// returning "" when nothing is worth showing.
func footerSummary(f *TurnFooter) string {
	if f == nil {
		return ""
	}
	var parts []string
	if f.Model != "" {
		parts = append(parts, f.Model)
	}
	if f.Backend != "" {
		parts = append(parts, "via "+f.Backend)
	}
	if f.Elapsed > 0 {
		parts = append(parts, fmtElapsed(f.Elapsed))
	}
	if f.InputTokens > 0 || f.OutputTokens > 0 {
		parts = append(parts, "↑"+kit.FormatTokens(f.InputTokens)+" ↓"+kit.FormatTokens(f.OutputTokens))
	}
	if f.CostUSD > 0 {
		parts = append(parts, kit.FormatCost(f.CostUSD))
	}
	if len(parts) == 0 {
		return ""
	}
	return "◇ " + strings.Join(parts, " · ")
}

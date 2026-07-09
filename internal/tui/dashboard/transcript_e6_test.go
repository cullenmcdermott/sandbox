package dashboard

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// §4 E6: renderLiveReasoning used to word-wrap the ENTIRE accumulated think every
// frame (O(buffer) per delta → O(buffer²) over a long think). The prefix-cache
// (Option A) wraps each complete line once and re-wraps only the trailing partial
// line each frame. These tests pin: (1) incremental output is byte-identical to a
// single full wrap, (2) the per-frame re-wrap is bounded (not O(buffer)), (3) a
// width change and (4) a theme swap re-render fresh (no stale wrap).

// fullReasoningWrap reproduces the pre-E6 whole-buffer wrap: the reference the
// incremental cache must match exactly.
func fullReasoningWrap(text string, w int) string {
	return lipgloss.NewStyle().Foreground(theme.TextMuted).Italic(true).Width(w).Render(strings.TrimSpace(text))
}

// The cache must produce output byte-identical to a single .Width(w).Render over
// the whole buffer — for text with complete lines, blank lines, and a trailing
// partial (the shape a live think takes; TrimSpace guarantees no trailing '\n').
func TestLiveReasoningIncrementalMatchesFullWrap(t *testing.T) {
	m := reasoningModel(t)
	w := m.assistantWrapWidth()

	cases := []string{
		"a single long line of reasoning that certainly wraps well past the boundary here",
		"first complete line that is long enough to wrap around the width boundary set here\n" +
			"second line also long enough to wrap across the configured width more than once\n" +
			"trailing partial being typed",
		"para one wraps here across the width\n\nafter a blank line another paragraph continues on",
		"short",
	}
	for _, tx := range cases {
		m.resetReasoningWrapCache()
		got := m.wrapLiveReasoning(strings.TrimSpace(tx))
		want := fullReasoningWrap(tx, w)
		if got != want {
			t.Errorf("incremental wrap != full wrap for %q\n got: %q\nwant: %q", tx, got, want)
		}
	}
}

// Growing the buffer one delta at a time must ALSO stay byte-identical to a fresh
// full wrap at each step — the cache extension must not corrupt earlier lines.
func TestLiveReasoningIncrementalMatchesAcrossGrowth(t *testing.T) {
	m := reasoningModel(t)
	w := m.assistantWrapWidth()
	full := "line one is long enough that it wraps across the width more than once for sure\n" +
		"line two similarly wraps across the configured width boundary a couple of times\n" +
		"line three\n" +
		"a trailing partial line still being streamed in"

	var buf strings.Builder
	for _, r := range full {
		buf.WriteRune(r)
		text := strings.TrimSpace(buf.String())
		if text == "" {
			continue
		}
		got := m.wrapLiveReasoning(text)
		if want := fullReasoningWrap(buf.String(), w); got != want {
			t.Fatalf("incremental wrap diverged from full wrap after %q\n got: %q\nwant: %q", text, got, want)
		}
	}
}

// The per-frame cost pin: after rendering a HUGE multi-line think, the un-cached
// region (the trailing partial line re-wrapped each frame) must be bounded by one
// line's length — not the whole buffer. reasoningWrapLen covers every complete
// line, so len(text)-reasoningWrapLen is the only text re-wrapped per frame.
func TestLiveReasoningReWrapCostBounded(t *testing.T) {
	m := reasoningModel(t)

	// ~200 complete lines (~40 chars each ≈ 8KB) plus a short trailing partial.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("thinking about the trade-offs in this step\n")
	}
	b.WriteString("final partial")
	text := strings.TrimSpace(b.String())

	m.renderLiveReasoning(text) // populates the cache through the real render path

	uncached := len(text) - m.reasoningWrapLen
	if uncached > 64 {
		t.Errorf("per-frame re-wrap covers %d bytes, want ≤ 64 (bounded to the trailing partial, not O(buffer)=%d)", uncached, len(text))
	}
	if m.reasoningWrapLen < len(text)-64 {
		t.Errorf("cache covers only %d/%d bytes; nearly the whole buffer should be cached", m.reasoningWrapLen, len(text))
	}
}

// A width change must re-render fresh (invalidate the cache) — never emit a wrap
// stale at the old width. The re-render must equal a full wrap at the new width.
func TestLiveReasoningRerendersOnWidthChange(t *testing.T) {
	m := reasoningModel(t)
	text := strings.TrimSpace(strings.Repeat("the quick brown fox jumps over the lazy dog ", 8))

	m.width = 70
	m.layout()
	wide := m.wrapLiveReasoning(text)
	if wide != fullReasoningWrap(text, m.assistantWrapWidth()) {
		t.Fatal("first render must equal a full wrap at the wide width")
	}

	m.width = 40
	m.layout()
	narrow := m.wrapLiveReasoning(text)
	if narrow == wide {
		t.Error("a width change must re-wrap (got the stale wide wrap)")
	}
	if narrow != fullReasoningWrap(text, m.assistantWrapWidth()) {
		t.Error("post-resize render must equal a full wrap at the NEW width, not a stale cache")
	}
}

// A theme swap bumps theme.Epoch and re-derives styles; the cache is keyed on the
// epoch, so it must re-render with the new palette rather than emit the old one.
func TestLiveReasoningRerendersOnThemeSwap(t *testing.T) {
	start := theme.Active()
	t.Cleanup(func() {
		if th, ok := theme.ByName(start); ok {
			theme.ApplyTheme(th)
		}
	})

	m := reasoningModel(t)
	text := strings.TrimSpace(strings.Repeat("weighing the options carefully here ", 6))

	midnight, _ := theme.ByName("Midnight")
	daylight, _ := theme.ByName("Daylight")

	theme.ApplyTheme(midnight)
	first := m.wrapLiveReasoning(text)

	theme.ApplyTheme(daylight) // different TextMuted → different ANSI, and a new epoch
	second := m.wrapLiveReasoning(text)

	if second == first {
		t.Error("a theme swap must re-render the live reasoning with the new palette, not the cached one")
	}
	if second != fullReasoningWrap(text, m.assistantWrapWidth()) {
		t.Error("post-swap render must equal a fresh full wrap under the new theme")
	}
	if m.reasoningWrapEpoch != theme.Epoch() {
		t.Errorf("cache epoch = %d, want %d (must track the active theme epoch)", m.reasoningWrapEpoch, theme.Epoch())
	}
}

// End-to-end behavior pin through the real streamItem tail: a long, multi-line
// think must still show its TRAILING content under the "∴ Thinking" header (the
// incremental cache must not drop the tail).
func TestLiveReasoningTailShowsTrailingContent(t *testing.T) {
	m := reasoningModel(t)
	sendEvent(m, session.EventReasoningStarted, nil)
	for i := 0; i < 30; i++ {
		sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "considering an option\n"})
	}
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "the FINAL trailing thought"})

	rendered := m.streamItem.Render(m.width - 1)
	if !strings.Contains(rendered, "Thinking") {
		t.Error("live reasoning tail must show the Thinking header")
	}
	if !strings.Contains(rendered, "the FINAL trailing thought") {
		t.Errorf("live reasoning tail must show the trailing content, got %q", rendered)
	}
}

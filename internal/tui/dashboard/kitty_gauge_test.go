package dashboard

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/terminal"
)

// On Kitty-capable terminals the gauge must be a placeholder run of exactly
// kittyGaugeCols display columns — no layout shift versus the block bar.
func TestCtxGaugeKittyWidth(t *testing.T) {
	m := &TranscriptModel{caps: terminal.Caps{KittyGraphics: true}}
	for _, frac := range []float64{0, 0.5, 1} {
		got := lipgloss.Width(m.ctxGaugeKitty(frac))
		if got != kittyGaugeCols {
			t.Errorf("frac=%v: gauge width %d, want %d", frac, got, kittyGaugeCols)
		}
	}
}

// The gauge transmits exactly once per fill bucket: the first call queues a
// transmission, an identical-bucket second call does not, and a changed bucket
// re-transmits under a new image id.
func TestCtxGaugeKittyTransmitOnChange(t *testing.T) {
	m := &TranscriptModel{caps: terminal.Caps{KittyGraphics: true}}

	_ = m.ctxGaugeKitty(0.50)
	first := m.takePendingKitty()
	if first == "" {
		t.Fatal("first render must queue a transmission")
	}
	id1 := m.kittyGaugeID

	// Same bucket → no new transmission.
	_ = m.ctxGaugeKitty(0.504) // still 50%
	if got := m.takePendingKitty(); got != "" {
		t.Fatalf("same bucket must not re-transmit, got %q", got[:min(len(got), 20)])
	}
	if m.kittyGaugeID != id1 {
		t.Fatal("same bucket must keep the same image id")
	}

	// Changed bucket → new transmission under a new id.
	_ = m.ctxGaugeKitty(0.77)
	if m.takePendingKitty() == "" {
		t.Fatal("changed bucket must re-transmit")
	}
	if m.kittyGaugeID == id1 {
		t.Fatal("changed bucket must use a new image id")
	}
}

// Degradation: with no Kitty capability the status line never emits placeholder
// cells or queues a transmission — the block-bar path is taken, identical to
// today. App.View must not prepend any APC for a non-Kitty transcript.
func TestCtxGaugeDegradesWithoutKitty(t *testing.T) {
	m := &TranscriptModel{} // zero caps
	m.Model = "claude-opus-4-8"
	m.CtxLimit = 200000
	line := m.renderStatusLine()
	if strings.Contains(line, "\U0010EEEE") {
		t.Fatal("non-Kitty status line must not contain placeholder cells")
	}
	if strings.Contains(line, "\x1b_G") {
		t.Fatal("non-Kitty status line must not contain an APC transmission")
	}
	if m.pendingKitty != "" {
		t.Fatal("non-Kitty render must not queue a Kitty transmission")
	}
}

// The global off switch must suppress the Stage 3 raster gauge even on a
// Kitty-capable terminal: the block-bar path is taken and nothing is queued.
func TestCtxGaugeKittySuppressedByReduceMotion(t *testing.T) {
	m := &TranscriptModel{caps: terminal.Caps{KittyGraphics: true, ReduceMotion: true}}
	m.Model = "claude-opus-4-8"
	m.CtxLimit = 200000
	line := m.renderStatusLine()
	if strings.Contains(line, "\U0010EEEE") {
		t.Fatal("ReduceMotion must suppress placeholder cells")
	}
	if m.pendingKitty != "" {
		t.Fatal("ReduceMotion must not queue a Kitty transmission")
	}
}

// withTerminalSignals must drain a queued Kitty transmission into the frame
// prefix (so it precedes the placeholder cells in the body) and clear it so the
// next frame does not re-emit it.
func TestWithTerminalSignalsDrainsKitty(t *testing.T) {
	a := &App{screen: ScreenTranscript, dashboard: New(nil)}
	tm := &TranscriptModel{caps: terminal.Caps{KittyGraphics: true}}
	rgba := terminal.GaugeRGBA(0.5, 80, 16, terminal.RGB{R: 0, G: 255, B: 0}, terminal.RGB{R: 10, G: 10, B: 10})
	tm.pendingKitty = terminal.KittyTransmitRGBA(1, 10, 1, 80, 16, rgba)
	a.transcript = tm

	body := "BODY " + terminal.KittyPlaceholders(1, 10, 1)
	out := a.withTerminalSignals(tea.NewView(body)).Content
	apc := strings.Index(out, "\x1b_G")
	cell := strings.Index(out, "\U0010EEEE")
	if apc < 0 {
		t.Fatal("expected the Kitty transmission prepended to the frame")
	}
	if apc > cell {
		t.Fatalf("transmission (%d) must precede placeholder cells (%d)", apc, cell)
	}
	if tm.pendingKitty != "" {
		t.Fatal("transmission must be drained after one frame")
	}
	// A subsequent frame with nothing queued must not re-emit a transmission.
	out2 := a.withTerminalSignals(tea.NewView(body)).Content
	if strings.Contains(out2, "\x1b_G") {
		t.Fatal("no transmission should be emitted once drained")
	}
}

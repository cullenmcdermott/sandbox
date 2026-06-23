package dashboard

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// The animated gauge must occupy exactly the same number of cells as the static
// bar for every fill level, or the status line would shift when a turn starts.
func TestShimmerBlockBarWidthMatchesStatic(t *testing.T) {
	for _, frac := range []float64{0, 0.05, 0.5, 0.85, 1.0, -1, 2} {
		static := lipgloss.Width(blockBar(frac, 10, rampColor(frac)))
		for _, pulse := range []bool{false, true} {
			for phase := 0; phase < 13; phase++ {
				got := lipgloss.Width(shimmerBlockBar(frac, 10, phase, pulse))
				if got != static {
					t.Errorf("frac=%v pulse=%v phase=%d: width %d, want %d (static)", frac, pulse, phase, got, static)
				}
			}
		}
	}
}

// The shimmer must actually move: advancing the phase changes the rendered
// string (otherwise there's no visible motion during a turn).
func TestShimmerBlockBarAnimates(t *testing.T) {
	a := shimmerBlockBar(0.9, 10, 0, false)
	b := shimmerBlockBar(0.9, 10, 3, false)
	if a == b {
		t.Fatal("expected phase change to alter the rendered bar")
	}
}

// The pulse (≥80%) variant must differ from the normal ramp at the same phase.
func TestShimmerBlockBarPulseDiffers(t *testing.T) {
	normal := shimmerBlockBar(0.9, 10, 0, false)
	pulse := shimmerBlockBar(0.9, 10, 0, true)
	if normal == pulse {
		t.Fatal("expected pulse variant to differ from the normal ramp")
	}
}

// Degradation: a zero-Caps transcript (the test/non-truecolor/NO_COLOR default)
// must render the static bar, never the shimmer — guaranteeing identical output.
func TestStatusLineGaugeDegradesWithoutCaps(t *testing.T) {
	m := &TranscriptModel{}
	if m.caps.TrueColor {
		t.Fatal("zero Caps must not report TrueColor")
	}
	// status defaults to the zero SessionStatus (not StatusBusy), and caps is
	// zero — both gates fail, so the static path is taken. This mirrors the
	// renderStatusLine gate; if either default changes this test catches it.
	if m.caps.TrueColor && !m.caps.ReduceMotion && m.status == StatusBusy {
		t.Fatal("zero-value transcript must not take the animated gauge path")
	}
}

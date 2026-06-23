package dashboard

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// REGRESSION (D7): blockBar must render █/░ blocks; pct must be clamped to
// 100; 80%+ must show a warning indicator.
func TestBlockBarRendersBlocks(t *testing.T) {
	out := blockBar(0.5, 10, lipgloss.Color("#fff"))
	if strings.Contains(out, "●") || strings.Contains(out, "○") {
		t.Fatal("blockBar should use █/░, not ●/○")
	}
	if !strings.Contains(out, "█") || !strings.Contains(out, "░") {
		t.Fatalf("blockBar should contain █ and ░, got %q", out)
	}
}

func TestBlockBarClampsOverrun(t *testing.T) {
	out := blockBar(1.5, 10, lipgloss.Color("#fff"))
	// All 10 should be filled (clamped to 1.0).
	if strings.Count(out, "░") != 0 {
		t.Fatalf("expected 0 empty blocks when frac=1.5, got %q", out)
	}
}

func TestBlockBarClampsUnderrun(t *testing.T) {
	out := blockBar(-0.5, 10, lipgloss.Color("#fff"))
	if strings.Count(out, "█") != 0 {
		t.Fatalf("expected 0 filled blocks when frac=-0.5, got %q", out)
	}
}

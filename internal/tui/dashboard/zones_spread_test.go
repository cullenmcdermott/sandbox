package dashboard

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// spread must never overflow the target width and must always keep the right
// segment (status glyph / affordance) fully visible — long titles get clipped,
// not the thing on the right edge.
func TestSpreadNeverOverflowsAndKeepsRight(t *testing.T) {
	const w = 20
	cases := []struct {
		name        string
		left, right string
	}{
		{"fits", "title", "esc"},
		{"left overflows", strings.Repeat("x", 40), "esc sends"},
		{"exact fill", strings.Repeat("x", 15), "abcde"},
		{"right nearly fills", "title", strings.Repeat("r", 19)},
		{"right exactly fills", "", strings.Repeat("r", 20)},
		{"right overflows", "title", strings.Repeat("r", 30)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := spread(c.left, c.right, w)
			if gw := lipgloss.Width(got); gw > w {
				t.Errorf("spread width = %d, want <= %d (%q)", gw, w, got)
			}
			// The right segment survives whenever it fits within the row on its own.
			if lipgloss.Width(c.right) <= w && !strings.HasSuffix(got, c.right) {
				t.Errorf("spread(%q,%q,%d) = %q; right segment not preserved", c.left, c.right, w, got)
			}
		})
	}
}

func TestSpreadZeroWidth(t *testing.T) {
	if got := spread("left", "right", 0); got != "" {
		t.Errorf("spread with width 0 = %q, want empty", got)
	}
}

// clampLines must produce exactly h lines each exactly w columns wide — an
// over-wide input line is clipped, not allowed to escape the band.
func TestClampLinesEnforcesExactBox(t *testing.T) {
	const w, h = 12, 3
	in := "short\n" + strings.Repeat("y", 40) + "\nmid"
	out := clampLines(in, w, h)
	lines := strings.Split(out, "\n")
	if len(lines) != h {
		t.Fatalf("clampLines produced %d lines, want %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw != w {
			t.Errorf("line %d width = %d, want exactly %d (%q)", i, lw, w, l)
		}
	}
}

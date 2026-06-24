package theme

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestMascotLinesEqualWidth guards against the connect-splash shear (T7): the
// raw mascot sprites are hand-drawn with unequal line widths (the "OC" monogram
// is 7/5/7 cells), and the splash centers the logo with JoinVertical(Center) /
// lipgloss.Place(Center), which pad each line independently — so a ragged block
// gets its narrow lines shifted sideways. gradientBlock must right-pad every
// line to a common display width so the sprite stays rectangular and centers as
// one block.
func TestMascotLinesEqualWidth(t *testing.T) {
	for name, render := range map[string]func() string{
		"ClaudeMascot":   ClaudeMascot,
		"OpenCodeMascot": OpenCodeMascot,
	} {
		lines := strings.Split(render(), "\n")
		if len(lines) < 2 {
			t.Fatalf("%s: expected a multi-line sprite, got %d line(s)", name, len(lines))
		}
		want := lipgloss.Width(lines[0])
		for i, ln := range lines {
			if got := lipgloss.Width(ln); got != want {
				t.Errorf("%s line %d display width = %d, want %d (sprite must be rectangular so it centers without shearing)", name, i, got, want)
			}
		}
	}
}

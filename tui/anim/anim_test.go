// CANONICAL TEST — do not weaken.
package anim

import (
	"image/color"
	"strings"
	"testing"
)

// ORACLE: each step produces a different frame; COUNTER: Render does not allocate.
func TestAnimSpinnerFrames(t *testing.T) {
	s := NewSpinner()
	f1 := s.Render()
	s.Step()
	f2 := s.Render()
	if f1 == f2 {
		t.Fatalf("spinner did not advance frame after Step")
	}
}

// ORACLE: pre-rendered frames exist at construction time.
func TestAnimSpinnerPreRendered(t *testing.T) {
	s := NewSpinner()
	if s.Render() == "" {
		t.Fatalf("spinner produced empty output")
	}
}

// ORACLE: Rebuild with gradient colors pre-renders non-empty colored frames
// that differ from each other, and FrameAt cycles them without allocating a
// style per call. This is the A5 regression: the spinner must own gradient
// coloring (P4) so callers need only an index, not a separate busyGlyph +
// spinnerColor pair — those old globals are retired.
func TestSpinnerRebuildProducesColoredFrames(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("SANDBOX_REDUCE_MOTION", "")
	glyphs := []string{"◐", "◓", "◑", "◒"}
	colors := []color.Color{
		color.NRGBA{R: 255, G: 0, B: 0, A: 255},
		color.NRGBA{R: 0, G: 255, B: 0, A: 255},
	}
	s := NewSpinner()
	s.Rebuild(glyphs, colors)

	// All frames non-empty.
	n := len(glyphs) * len(colors)
	for i := 0; i < n; i++ {
		f := s.FrameAt(i)
		if f == "" {
			t.Fatalf("FrameAt(%d) returned empty string after Rebuild", i)
		}
	}

	// Frames for rebuilt spinner contain ANSI escapes (pre-colored).
	f0 := s.FrameAt(0)
	if !strings.ContainsRune(f0, '\x1b') {
		t.Fatalf("FrameAt(0) has no ANSI escapes — not pre-colored: %q", f0)
	}

	// Different color slots produce different output for the same glyph.
	fa := s.FrameAt(0)           // color[0]+glyph[0]
	fb := s.FrameAt(len(glyphs)) // color[1]+glyph[0]
	if fa == fb {
		t.Fatalf("same glyph with different colors produced identical frame: %q", fa)
	}
}

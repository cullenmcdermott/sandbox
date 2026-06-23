// Package anim implements pre-rendered spinner components.
package anim

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// defaultFrames is the fallback braille rotation used before Rebuild is called.
var defaultFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner is a pre-rendered gradient spinner. Frames are colored strings built
// once at construction/rebuild time; rendering is a single slice index with no
// per-frame allocation (P4, chat-rendering-architecture §2.5).
type Spinner struct {
	frames []string
	idx    int
}

// NewSpinner creates a Spinner with the default braille frames (no color). Call
// Rebuild to supply gradient-colored frames for the live UI.
func NewSpinner() *Spinner {
	return &Spinner{
		frames: defaultFrames,
	}
}

// Rebuild pre-renders frames from (glyphs × colors). Called by rebuildStyles so
// the spinner tracks the current theme. Each (glyph, color) pair is rendered
// into a styled string once, so Frame() is O(1) allocation-free.
// A rebuild produces len(glyphs)*len(colors) frames cycling glyphs through each
// color so hue and shape advance together.
func (s *Spinner) Rebuild(glyphs []string, colors []color.Color) {
	if len(glyphs) == 0 || len(colors) == 0 {
		s.frames = defaultFrames
		return
	}
	frames := make([]string, len(glyphs)*len(colors))
	for ci, c := range colors {
		for gi, g := range glyphs {
			frames[ci*len(glyphs)+gi] = lipgloss.NewStyle().Foreground(c).Render(g)
		}
	}
	s.frames = frames
}

// FrameAt returns the pre-colored glyph string for frame index idx, wrapping
// modulo the frame count. Does NOT apply reduce-motion — callers that need
// reduce-motion behaviour should use Frame(reduceMotion bool) from transition.go
// or check ReduceMotion() before calling. Returns "◐" if no frames are built yet.
func (s *Spinner) FrameAt(idx int) string {
	if len(s.frames) == 0 {
		return "◐"
	}
	return s.frames[idx%len(s.frames)]
}

// Render returns the current spinner frame (based on internal idx counter).
func (s *Spinner) Render() string {
	return s.FrameAt(s.idx)
}

// Step advances the internal idx counter by one frame.
func (s *Spinner) Step() {
	s.idx++
}

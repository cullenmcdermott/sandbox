package anim

// transition.go — the motion primitives for Tier 3 (chat-styling-and-motion.md
// §C/§E). These signatures are the immutable contract pinned by the Tier-3
// canonical tests; the bodies here are placeholders awaiting the S2/S3/S5
// feature increments, so the canonical tests start red (TDD).

import (
	"image/color"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// EaseOutCubic eases t in [0,1]: fast start, settling finish — 1-(1-t)³. It pins
// its endpoints (0→0, 1→1), bows above the linear diagonal for interior t, and is
// monotonic non-decreasing. Inputs outside [0,1] clamp to the nearest endpoint.
func EaseOutCubic(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	u := 1 - t
	return 1 - u*u*u
}

// Progress returns the eased fraction of elapsed over total, clamped to [0,1].
// A non-positive total is already "done" (1); easing is ease-out cubic (§C.1).
func Progress(elapsed, total time.Duration) float64 {
	if total <= 0 {
		return 1
	}
	t := float64(elapsed) / float64(total)
	return EaseOutCubic(t)
}

// lerpResolution is the ramp size LerpColor indexes for interior blends; higher
// resolution makes the perceptual interpolation smooth without per-call cost.
const lerpResolution = 256

// LerpColor blends from→to by t in [0,1] in a perceptual space via a Blend1D
// ramp (§C.1). Endpoints are pinned exactly (t<=0→from, t>=1→to); interior t
// indexes the ramp so the midpoint lands strictly between the two colors.
func LerpColor(from, to color.Color, t float64) color.Color {
	if t <= 0 {
		return from
	}
	if t >= 1 {
		return to
	}
	ramp := lipgloss.Blend1D(lerpResolution, from, to)
	idx := int(t*float64(lerpResolution-1) + 0.5)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(ramp) {
		idx = len(ramp) - 1
	}
	return ramp[idx]
}

// ReduceMotion reports whether motion should collapse to its end state, from
// SANDBOX_REDUCE_MOTION=1 or NO_COLOR. This env read is stable; the collapse
// behavior it gates (Transition.At, Spinner.Frame) lands in S2/S5.
func ReduceMotion() bool {
	return os.Getenv("SANDBOX_REDUCE_MOTION") == "1" || os.Getenv("NO_COLOR") != ""
}

// Transition is a from/to interpolation over Total, read (never driven) at
// render time.
type Transition struct {
	Total time.Duration
}

// At returns the eased progress in [0,1] for elapsed since the transition began,
// collapsing to 1 immediately when ReduceMotion is on (§C.1, §E).
func (tr Transition) At(elapsed time.Duration) float64 {
	if ReduceMotion() {
		return 1
	}
	return Progress(elapsed, tr.Total)
}

// Engine tracks active transitions and running spinners so the TUI can schedule
// a single ~30fps tick loop only while motion is active (the gating contract in
// §C.2 — idle sessions schedule no tick).
type Engine struct {
	ends     []time.Time
	spinners int
}

func NewEngine() *Engine { return &Engine{} }

// StartTransition registers a transition that finishes at end.
func (e *Engine) StartTransition(end time.Time) { e.ends = append(e.ends, end) }

// SetSpinners records how many spinners are currently running.
func (e *Engine) SetSpinners(n int) { e.spinners = n }

// AnyMotionActive reports whether any registered transition is still unfinished
// at now, or any spinner is running. When false the caller stops scheduling the
// tick loop (§C.2 — idle sessions schedule no tick).
func (e *Engine) AnyMotionActive(now time.Time) bool {
	if e.spinners > 0 {
		return true
	}
	for _, end := range e.ends {
		if now.Before(end) {
			return true
		}
	}
	return false
}

// Ellipsis returns the animated "thinking" ellipsis for step, cycling
// "", ".", "..", "..." every four steps (§C.3).
func Ellipsis(step int) string {
	n := step % 4
	if n < 0 {
		n += 4
	}
	return strings.Repeat(".", n)
}

// Frame returns the spinner's current frame, or its first (static) frame when
// reduceMotion is set, so motion-off renders are byte-stable across ticks (§E).
func (s *Spinner) Frame(reduceMotion bool) string {
	if reduceMotion {
		if len(s.frames) == 0 {
			return ""
		}
		return s.frames[0]
	}
	return s.Render()
}

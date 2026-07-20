package dashboard

// transitions.go — the transition catalog (chat-styling-and-motion §C.3). Each
// entry is a from/to interpolation expressed in engine terms: a duration plus
// an eased fraction read at render time (never driven). Durations follow the
// spec's "settled, not animated" budget — 150–300ms, ease-out.

import (
	"image/color"
	"time"

	"github.com/cullenmcdermott/sandbox/tui/anim"
)

const (
	rowEnterDur    = 180 * time.Millisecond // new row fades in
	statusFlashDur = 300 * time.Millisecond // brief bg pulse when a row changes status
)

// rowEnter returns the eased 0→1 reveal fraction for a row that first appeared
// at `since` (a fresh session/message fades in over rowEnterDur), collapsing to
// 1 immediately under reduce-motion. It is 1 once the window has elapsed.
func rowEnter(since time.Time) float64 {
	if since.IsZero() || nowFunc().Sub(since) >= rowEnterDur {
		return 1
	}
	tr := anim.Transition{Total: rowEnterDur}
	return tr.At(nowFunc().Sub(since))
}

// statusFlash returns the strength (1→0) of a fading status-change highlight for
// a row whose status changed at `since`: brightest right after the change,
// decaying to nothing over statusFlashDur. It is 0 when no flash is active or
// under reduce-motion (the engine's Transition collapses to its end state).
func statusFlash(since time.Time) float64 {
	if since.IsZero() {
		return 0
	}
	el := nowFunc().Sub(since)
	if el >= statusFlashDur {
		return 0
	}
	tr := anim.Transition{Total: statusFlashDur}
	return 1 - tr.At(el)
}

// flashBg tints base toward accent by the active status-flash strength, returning
// ok=false when no flash is in flight so the caller leaves the row background
// untouched. The tint tops out well short of the full accent so it reads as a
// pulse, not a solid fill.
func flashBg(base, accent color.Color, since time.Time) (color.Color, bool) {
	f := statusFlash(since)
	if f <= 0 {
		return nil, false
	}
	return anim.LerpColor(base, accent, f*0.4), true
}

package anim

import (
	"image/color"
	"testing"
)

// ORACLE: interior blends land strictly between the endpoints (per channel, on
// a monotone grayscale pair) and increase with t; nil endpoints degrade to the
// other color instead of panicking.
func TestLerpColorInteriorAndNil(t *testing.T) {
	from := color.RGBA{R: 0x20, G: 0x20, B: 0x20, A: 0xff}
	to := color.RGBA{R: 0xe0, G: 0xe0, B: 0xe0, A: 0xff}
	rf, _, _, _ := from.RGBA()
	rt, _, _, _ := to.RGBA()
	prev := rf
	for _, tt := range []float64{0.25, 0.5, 0.75} {
		r, _, _, _ := LerpColor(from, to, tt).RGBA()
		if r <= rf || r >= rt {
			t.Fatalf("LerpColor(t=%v) red = %v, want strictly between %v and %v", tt, r, rf, rt)
		}
		if r <= prev {
			t.Fatalf("LerpColor not increasing at t=%v (red %v <= %v)", tt, r, prev)
		}
		prev = r
	}
	if got := LerpColor(nil, to, 0.5); got != color.Color(to) {
		t.Fatalf("LerpColor(nil, to, 0.5) = %v, want to", got)
	}
	if got := LerpColor(from, nil, 0.5); got != color.Color(from) {
		t.Fatalf("LerpColor(from, nil, 0.5) = %v, want from", got)
	}
}

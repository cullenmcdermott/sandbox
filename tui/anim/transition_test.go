// CANONICAL TEST — do not weaken.
package anim

import (
	"image/color"
	"testing"
	"time"
)

// ORACLE: ease-out cubic pins its endpoints and bows above the linear diagonal
// (eased value > t for interior t); COUNTER: it is monotonic non-decreasing, so
// it can't be the identity placeholder. [S2]
func TestEaseOutCubicShape(t *testing.T) {
	if got := EaseOutCubic(0); got != 0 {
		t.Fatalf("EaseOutCubic(0) = %v, want 0", got)
	}
	if got := EaseOutCubic(1); got != 1 {
		t.Fatalf("EaseOutCubic(1) = %v, want 1", got)
	}
	if got := EaseOutCubic(0.5); got <= 0.5 {
		t.Fatalf("EaseOutCubic(0.5) = %v, want > 0.5 (ease-out bows above linear)", got)
	}
	prev := EaseOutCubic(0)
	for i := 1; i <= 10; i++ {
		cur := EaseOutCubic(float64(i) / 10)
		if cur < prev {
			t.Fatalf("EaseOutCubic not monotonic at %d/10: %v < %v", i, cur, prev)
		}
		prev = cur
	}
}

// ORACLE: progress clamps to [0,1] and is strictly between at the midpoint;
// COUNTER: a zero-duration transition is already done (1), not 0. [S2]
func TestProgressClamps(t *testing.T) {
	d := 200 * time.Millisecond
	if got := Progress(0, d); got != 0 {
		t.Fatalf("Progress(0,d) = %v, want 0", got)
	}
	if got := Progress(d, d); got != 1 {
		t.Fatalf("Progress(d,d) = %v, want 1", got)
	}
	if got := Progress(2*d, d); got != 1 {
		t.Fatalf("Progress(2d,d) = %v, want clamp to 1", got)
	}
	if got := Progress(d/2, d); got <= 0 || got >= 1 {
		t.Fatalf("Progress(d/2,d) = %v, want in (0,1)", got)
	}
	if got := Progress(time.Second, 0); got != 1 {
		t.Fatalf("Progress with zero duration = %v, want 1 (already done)", got)
	}
}

// ORACLE: lerp pins endpoints and lands strictly between at t=0.5. [S2]
func TestLerpColorEndpoints(t *testing.T) {
	from := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	to := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	r0, g0, b0, _ := LerpColor(from, to, 0).RGBA()
	rf, gf, bf, _ := from.RGBA()
	if r0 != rf || g0 != gf || b0 != bf {
		t.Fatalf("LerpColor(_,_,0) = %v, want from %v", []uint32{r0, g0, b0}, []uint32{rf, gf, bf})
	}
	r1, g1, b1, _ := LerpColor(from, to, 1).RGBA()
	rt, gt, bt, _ := to.RGBA()
	if r1 != rt || g1 != gt || b1 != bt {
		t.Fatalf("LerpColor(_,_,1) = %v, want to %v", []uint32{r1, g1, b1}, []uint32{rt, gt, bt})
	}
	rm, _, _, _ := LerpColor(from, to, 0.5).RGBA()
	if rm <= rf || rm >= rt {
		t.Fatalf("LerpColor midpoint red = %v, want strictly between %v and %v", rm, rf, rt)
	}
}

// ORACLE: with reduce-motion the transition reads its end state (1) immediately;
// COUNTER: with motion on, the same elapsed reads strictly less than 1. [S2/S5]
func TestTransitionReduceMotionCollapses(t *testing.T) {
	tr := Transition{Total: 200 * time.Millisecond}

	t.Setenv("NO_COLOR", "")
	t.Setenv("SANDBOX_REDUCE_MOTION", "")
	if got := tr.At(0); got >= 1 {
		t.Fatalf("motion on: At(0) = %v, want < 1 (animating)", got)
	}

	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	if got := tr.At(0); got != 1 {
		t.Fatalf("reduce-motion: At(0) = %v, want 1 (collapsed to end state)", got)
	}
}

// COUNTER: an idle engine reports no motion (so no tick is scheduled); ORACLE: a
// transition is active until its end time, and a running spinner keeps it active. [S2]
func TestAnyMotionActiveGating(t *testing.T) {
	now := time.Unix(1000, 0)
	e := NewEngine()
	if e.AnyMotionActive(now) {
		t.Fatalf("fresh engine reports motion active; idle must schedule no tick")
	}

	e.StartTransition(now.Add(100 * time.Millisecond))
	if !e.AnyMotionActive(now) {
		t.Fatalf("transition not active before its end time")
	}
	if e.AnyMotionActive(now.Add(200 * time.Millisecond)) {
		t.Fatalf("transition still active after its end time")
	}

	e2 := NewEngine()
	e2.SetSpinners(1)
	if !e2.AnyMotionActive(now) {
		t.Fatalf("running spinner must keep motion active")
	}
}

// ORACLE: the ellipsis cycles ""→"."→".."→"..." and wraps. [S3]
func TestEllipsisCycles(t *testing.T) {
	want := []string{"", ".", "..", "..."}
	for step := 0; step < 8; step++ {
		if got := Ellipsis(step); got != want[step%4] {
			t.Fatalf("Ellipsis(%d) = %q, want %q", step, got, want[step%4])
		}
	}
}

// COUNTER: under reduce-motion a stepped spinner renders a stable frame, so
// motion-off views are byte-identical across ticks; ORACLE: with motion on the
// frame advances after Step. [S2/S5]
func TestSpinnerReduceMotionStatic(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	s := NewSpinner()
	first := s.Frame(true)
	s.Step()
	if s.Frame(true) != first {
		t.Fatalf("reduce-motion spinner advanced: %q != %q", s.Frame(true), first)
	}

	m := NewSpinner()
	a := m.Frame(false)
	m.Step()
	if m.Frame(false) == a {
		t.Fatalf("motion-on spinner did not advance after Step")
	}
}

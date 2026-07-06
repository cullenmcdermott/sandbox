package list

import (
	"strings"
	"testing"
)

// TestResizeCoalescesRerenders is the regression for uncoalesced resize: a burst
// of SetSize calls during a terminal drag must not re-render the tail once per
// intermediate width. SetSize is O(1) (it only records the size + defers the
// follow re-pin); the single Render that follows rebuilds the visible tail once,
// at the final width. The old path called GotoBottom inside every SetSize, so it
// re-rendered the tail on every one of the drag's WindowSizeMsgs.
func TestResizeCoalescesRerenders(t *testing.T) {
	items, cs := build(40)
	l := New(items...)
	l.SetSize(80, 10)
	l.GotoBottom()
	_ = l.Render() // warm the visible tail at width 80

	total := func() int {
		s := 0
		for _, c := range cs {
			s += c.renders
		}
		return s
	}
	before := total()

	// Simulate a drag: many distinct widths, no Render between them.
	for w := 79; w >= 60; w-- {
		l.SetSize(w, 10)
	}
	if mid := total(); mid != before {
		t.Fatalf("SetSize during a drag eagerly re-rendered %d items; want 0 (resize not coalesced)", mid-before)
	}

	out := l.Render() // one rebuild at the final width (60)
	after := total() - before
	if after > l.Height()+2 {
		t.Fatalf("coalesced resize re-rendered %d items; want <= %d (a single tail rebuild)", after, l.Height()+2)
	}
	if !l.AtBottom() || !l.Following() {
		t.Fatal("follow re-pin lost after coalesced resize")
	}
	if got := strings.Count(out, "\n") + 1; got != 10 {
		t.Fatalf("expected 10 visible lines after resize, got %d", got)
	}
}

// TestResizeWidthReturnsToCachedNoRerender verifies the lazy (no eager cache
// drop) invalidation: if the width changes and returns to a previously-rendered
// value with no Render in between, the surviving cache entries are re-hit and
// nothing re-renders. The old SetSize dropped the whole cache on any width
// change, forcing a cold re-render even when the width oscillated back.
func TestResizeWidthReturnsToCachedNoRerender(t *testing.T) {
	items, cs := build(5)
	l := New(items...)
	l.SetSize(40, 10)
	l.GotoBottom()
	_ = l.Render() // cache all items at width 40

	base := make([]int, len(cs))
	for i, c := range cs {
		base[i] = c.renders
	}

	// Drag out to 60 and back to 40 with no Render between.
	l.SetSize(60, 10)
	l.SetSize(40, 10)
	_ = l.Render()

	for i, c := range cs {
		if c.renders != base[i] {
			t.Fatalf("item %d re-rendered (%d -> %d) after width returned to a cached value with no intervening render",
				i, base[i], c.renders)
		}
	}
}

// TestResizeDeferredRepinSurvivesShrink guards that deferring the re-pin does not
// break the viewport-shrink follow behavior: SetSize while following, then an
// offset read, must observe the re-pin at the final size. Mirrors
// TestFollowSurvivesViewportShrink but exercises the deferred path with several
// stacked SetSize calls before the first read.
func TestResizeDeferredRepinSurvivesShrink(t *testing.T) {
	items := make([]Item, 0, 20)
	for i := 0; i < 20; i++ {
		items = append(items, newCountingItem("x"))
	}
	l := New(items...)
	l.SetSize(40, 8)
	l.GotoBottom()

	// Composer grows in steps; body viewport shrinks 8 -> 6 -> 4 -> 2, no read.
	l.SetSize(40, 6)
	l.SetSize(40, 4)
	l.SetSize(40, 2)

	if !l.AtBottom() {
		t.Fatalf("deferred re-pin did not settle at the bottom after stacked shrinks (offset=%d)", l.Offset())
	}
	if !l.Following() {
		t.Fatal("follow flag lost across stacked deferred resizes")
	}
}

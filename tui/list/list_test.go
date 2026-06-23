// CANONICAL TEST — do not weaken. internal/tui/dashboard/list/list_test.go
package list

import (
	"fmt"
	"strings"
	"testing"
)

// countingItem records how many times Render was called — the behavioral counter.
type countingItem struct {
	*Versioned
	body    string // single logical line of content
	renders int
}

func newCountingItem(body string) *countingItem {
	return &countingItem{Versioned: NewVersioned(), body: body}
}
func (c *countingItem) Render(width int) string { c.renders++; return c.body }
func (c *countingItem) Finished() bool          { return true }

func build(n int) ([]Item, []*countingItem) {
	items := make([]Item, n)
	cs := make([]*countingItem, n)
	for i := range items {
		ci := newCountingItem(fmt.Sprintf("row-%d", i))
		items[i], cs[i] = ci, ci
	}
	return items, cs
}

// ORACLE+COUNTER: with 1000 items height-1 and a height-20 viewport, exactly 20
// lines are returned (oracle) AND at most height+1 items were ever rendered
// (counter). A render-all-then-slice impl fails the counter; an empty impl fails
// the oracle.
func TestRenderOnlyVisibleAndExactHeight(t *testing.T) {
	items, cs := build(1000)
	l := New(items...)
	l.SetSize(40, 20)
	out := l.Render()

	if got := strings.Count(out, "\n") + 1; got != 20 {
		t.Fatalf("expected exactly 20 visible lines, got %d", got)
	}
	rendered := 0
	for _, c := range cs {
		if c.renders > 0 {
			rendered++
		}
	}
	if rendered > 21 {
		t.Fatalf("rendered %d items; expected <= 21 (viewport-bounded)", rendered)
	}
}

// COUNTER: a second Render with nothing changed re-renders nothing.
func TestNoReRenderWhenUnchanged(t *testing.T) {
	items, cs := build(50)
	l := New(items...)
	l.SetSize(40, 20)
	_ = l.Render()
	before := make([]int, len(cs))
	for i, c := range cs {
		before[i] = c.renders
	}
	_ = l.Render()
	for i, c := range cs {
		if c.renders != before[i] {
			t.Fatalf("item %d re-rendered with no change (%d -> %d)", i, before[i], c.renders)
		}
	}
}

// COUNTER: bumping one visible item re-renders exactly that item.
func TestBumpInvalidatesOnlyThatItem(t *testing.T) {
	items, cs := build(10)
	l := New(items...)
	l.SetSize(40, 20)
	_ = l.Render()
	base := make([]int, len(cs))
	for i, c := range cs {
		base[i] = c.renders
	}
	cs[3].body = "changed"
	cs[3].Bump()
	_ = l.Render()
	for i, c := range cs {
		delta := c.renders - base[i]
		if i == 3 && delta != 1 {
			t.Fatalf("bumped item rendered %d times, want 1", delta)
		}
		if i != 3 && delta != 0 {
			t.Fatalf("item %d re-rendered (%d) after unrelated bump", i, delta)
		}
	}
}

// COUNTER: same width => no invalidation; new width => visible items re-render.
func TestWidthInvalidation(t *testing.T) {
	items, cs := build(10)
	l := New(items...)
	l.SetSize(40, 20)
	_ = l.Render()
	r0 := cs[0].renders
	l.SetSize(40, 20) // same width
	_ = l.Render()
	if cs[0].renders != r0 {
		t.Fatalf("same-width SetSize caused re-render")
	}
	l.SetSize(60, 20) // new width
	_ = l.Render()
	if cs[0].renders == r0 {
		t.Fatalf("width change did not invalidate")
	}
}

// ORACLE: GotoBottom implies AtBottom; round-trip scroll returns to bottom.
func TestScrollRoundTrip(t *testing.T) {
	items, _ := build(200)
	l := New(items...)
	l.SetSize(40, 20)
	l.GotoBottom()
	if !l.AtBottom() {
		t.Fatalf("GotoBottom did not reach bottom")
	}
	l.ScrollBy(-1000)
	if l.AtBottom() {
		t.Fatalf("scrolled up but still AtBottom")
	}
	if l.Offset() != 0 {
		t.Fatalf("scrolling up by more than content should clamp to top (offset=%d)", l.Offset())
	}
	l.ScrollBy(100000)
	if !l.AtBottom() {
		t.Fatalf("scrolling down past end should clamp to bottom")
	}
}

// ORACLE (fuzz): Render never returns more than `height` lines, at any scroll
// position, for varied item heights.
func TestRenderNeverExceedsHeight(t *testing.T) {
	var items []Item
	for i := 0; i < 60; i++ {
		ci := newCountingItem(strings.Repeat("x\n", i%5)) // 1..5 lines
		items = append(items, ci)
	}
	l := New(items...)
	l.SetSize(30, 12)
	for _, off := range []int{0, 1, 5, 13, 37, 999} {
		l.GotoTop()
		l.ScrollBy(off)
		out := l.Render()
		lines := 0
		if out != "" {
			lines = strings.Count(out, "\n") + 1
		}
		if lines > 12 {
			t.Fatalf("offset %d: %d lines > height 12", off, lines)
		}
	}
}

package list

import (
	"strings"
	"testing"
)

// mkLines builds a height-`lines` item.
func mkLines(lines int) Item {
	return newCountingItem(strings.TrimRight(strings.Repeat("x\n", lines), "\n"))
}

// TestFollowSurvivesViewportShrink is the regression for the reported
// "doesn't stick to the bottom" bug: while pinned to the bottom, shrinking the
// viewport (the composer growing, a permission box or palette opening) must keep
// the latest content in view. The old code re-measured AtBottom AFTER the shrink
// against the unchanged offset, read false, and silently stopped following.
func TestFollowSurvivesViewportShrink(t *testing.T) {
	items := []Item{mkLines(1), mkLines(1), mkLines(1), mkLines(1), mkLines(1), mkLines(1)}
	l := New(items...)
	l.SetSize(40, 4)
	l.GotoBottom()
	if !l.AtBottom() || !l.Following() {
		t.Fatalf("precondition: want AtBottom+Following after GotoBottom")
	}

	// The composer grows → the body viewport shrinks from 4 rows to 2.
	l.SetSize(40, 2)
	if !l.AtBottom() {
		t.Fatalf("follow dropped on viewport shrink: AtBottom=false (offset=%d)", l.Offset())
	}
	if !l.Following() {
		t.Fatal("follow flag cleared on viewport shrink")
	}
}

// TestScrollUpStopsFollowAndResizePreservesIt verifies the inverse: once the
// user scrolls up, follow is cleared and a later resize must NOT yank them back
// to the bottom.
func TestScrollUpStopsFollowAndResizePreservesIt(t *testing.T) {
	items := make([]Item, 0, 50)
	for i := 0; i < 50; i++ {
		items = append(items, mkLines(1))
	}
	l := New(items...)
	l.SetSize(40, 10)
	l.GotoBottom()

	l.ScrollBy(-5)
	if l.Following() {
		t.Fatal("scrolling up should clear follow")
	}
	off := l.Offset()

	// A resize while scrolled up must not re-pin to the bottom.
	l.SetSize(40, 8)
	if l.AtBottom() {
		t.Fatalf("resize re-pinned to bottom despite not following (offset went %d -> %d)", off, l.Offset())
	}
}

// TestFollowRePinsOnAppendAndSetItems verifies appends/reconciles re-pin to the
// new bottom while following, without the caller doing the AtBottom→GotoBottom
// dance.
func TestFollowRePinsOnAppendAndSetItems(t *testing.T) {
	items := []Item{mkLines(1), mkLines(1), mkLines(1)}
	l := New(items...)
	l.SetSize(40, 2)
	l.GotoBottom() // follow=true

	l.AppendItems(mkLines(1), mkLines(1))
	if !l.AtBottom() {
		t.Fatal("AppendItems while following did not re-pin to the new bottom")
	}

	bigger := append([]Item{}, l.items...)
	bigger = append(bigger, mkLines(1), mkLines(1), mkLines(1))
	l.SetItems(bigger...)
	if !l.AtBottom() {
		t.Fatal("SetItems while following did not re-pin to the new bottom")
	}

	// After scrolling up, SetItems must NOT re-pin.
	l.ScrollBy(-3)
	if l.Following() {
		t.Fatal("scroll up did not clear follow")
	}
	l.SetItems(append(bigger, mkLines(1))...)
	if l.AtBottom() {
		t.Fatal("SetItems re-pinned to bottom despite not following")
	}
}

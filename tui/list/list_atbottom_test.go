package list

import (
	"strings"
	"testing"
)

// TestAtBottomTallItemAboveShorter is a regression for the AtBottom early-exit
// that ignored offsetLine: when the first visible item is taller than the
// viewport and a shorter item follows, GotoBottom() must still report AtBottom().
// The canonical TestScrollRoundTrip uses uniform height-1 items and cannot catch
// this; the 01-list.md reference algorithm carries the same flaw.
func TestAtBottomTallItemAboveShorter(t *testing.T) {
	mk := func(lines int) Item {
		return newCountingItem(strings.TrimRight(strings.Repeat("x\n", lines), "\n"))
	}
	// Heights [1, 1, 100, 5] with a height-20 viewport.
	l := New(mk(1), mk(1), mk(100), mk(5))
	l.SetSize(40, 20)

	l.GotoBottom()
	if !l.AtBottom() {
		t.Fatalf("GotoBottom did not report AtBottom with a tall item above a shorter one (offset=%d)", l.Offset())
	}
	if out := l.Render(); strings.Count(out, "\n")+1 != 20 {
		t.Fatalf("expected 20 visible lines at bottom, got %d", strings.Count(out, "\n")+1)
	}

	// Scrolling up must flip AtBottom to false.
	l.ScrollBy(-3)
	if l.AtBottom() {
		t.Fatal("AtBottom should be false after scrolling up")
	}

	// Scrolling back down to the end must report AtBottom again.
	l.GotoBottom()
	if !l.AtBottom() {
		t.Fatal("AtBottom should be true after returning to the bottom")
	}
}

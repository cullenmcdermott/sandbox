package list

import (
	"fmt"
	"strings"
	"testing"
)

// list_scenario_test.go — the two transcript-host scenarios the public
// importability goal calls out explicitly: a viewport RESIZE while the reader is
// scrolled back through history, and a REPLAY (append) arriving while the reader
// is detached from the bottom. Both must leave the reader where they are — a
// transcript that yanks to the newest line on every reconnect/resize is unusable
// while reading history. These are stronger than the sibling follow tests: they
// assert the absolute offset AND the visible top row are preserved, not merely
// that AtBottom stayed false.

// TestResizeWhileScrolledBackPreservesPosition — the reader has scrolled up to
// read earlier output; the composer grows (or a palette opens), shrinking the
// body viewport at the same width. The anchor must not move and follow must not
// re-engage.
func TestResizeWhileScrolledBackPreservesPosition(t *testing.T) {
	items, _ := build(50) // row-0 .. row-49, each height 1
	l := New(items...)
	l.SetSize(20, 10)
	l.GotoBottom()

	l.ScrollBy(-15) // leave the bottom to read history
	if l.Following() {
		t.Fatal("precondition: scrolling up should clear follow")
	}
	offBefore := l.Offset()
	topBefore := strings.Split(l.Render(), "\n")[0]

	// Body viewport shrinks 10 -> 6 (composer grew) at the same width.
	l.SetSize(20, 6)

	if l.Following() {
		t.Error("resize while scrolled back re-engaged follow mode")
	}
	if l.AtBottom() {
		t.Error("resize while scrolled back jumped to the bottom")
	}
	if got := l.Offset(); got != offBefore {
		t.Errorf("resize moved the anchor: offset %d -> %d", offBefore, got)
	}
	if topAfter := strings.Split(l.Render(), "\n")[0]; topAfter != topBefore {
		t.Errorf("resize changed the visible top row: %q -> %q", topBefore, topAfter)
	}
}

// TestReplayWhileDetachedDoesNotJumpToBottom — on reconnect the SSE stream
// replays events (many items appended). If the reader is scrolled up, the
// appended tail must NOT pull the viewport to the bottom; the reader stays on the
// history they were reading. The sibling suite covers this for SetItems; this
// pins it for AppendItems, the replay path.
func TestReplayWhileDetachedDoesNotJumpToBottom(t *testing.T) {
	items, _ := build(30) // row-0 .. row-29
	l := New(items...)
	l.SetSize(20, 8)
	l.GotoBottom()

	l.ScrollBy(-10) // detach: reading history
	if l.Following() {
		t.Fatal("precondition: scrolling up should clear follow")
	}
	offBefore := l.Offset()
	topBefore := strings.Split(l.Render(), "\n")[0]

	// Reconnect replay: a burst of appended items while detached.
	more := make([]Item, 0, 12)
	for i := 30; i < 42; i++ {
		more = append(more, newCountingItem(fmt.Sprintf("row-%d", i)))
	}
	l.AppendItems(more...)

	if l.Following() {
		t.Error("append re-engaged follow mode")
	}
	if l.AtBottom() {
		t.Error("append while detached jumped to the bottom")
	}
	if got := l.Offset(); got != offBefore {
		t.Errorf("append moved the anchor: offset %d -> %d", offBefore, got)
	}
	rendered := l.Render()
	if topAfter := strings.Split(rendered, "\n")[0]; topAfter != topBefore {
		t.Errorf("append changed the visible top row: %q -> %q", topBefore, topAfter)
	}
	if strings.Contains(rendered, "row-41") {
		t.Error("the appended tail is visible — the viewport jumped to the bottom")
	}
}

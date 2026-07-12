package list

import (
	"fmt"
	"strings"
	"testing"
)

// mutableItem is a test item whose rendered height can change between renders.
type mutableItem struct {
	*Versioned
	lines []string
}

func (m *mutableItem) Render(width int) string { return strings.Join(m.lines, "\n") }

// REGRESSION: an anchor item that re-renders shorter than the recorded
// offsetLine must not be skipped. Before normalize(), a 10-line item scrolled
// to hidden-line 6 that shrank to 2 lines was dropped from Render entirely,
// Offset() could exceed TotalHeight(), and AtBottom() flipped true with content
// still below the viewport (breaking bottom-pin auto-scroll).
func TestAnchorItemShrinksBelowOffsetLine(t *testing.T) {
	big := &mutableItem{Versioned: NewVersioned()}
	for i := range 10 {
		big.lines = append(big.lines, fmt.Sprintf("big-%d", i))
	}
	items := []Item{big}
	for i := range 6 {
		items = append(items, newCountingItem(fmt.Sprintf("below-%d", i)))
	}
	l := New(items...)
	l.SetSize(40, 4)
	l.ScrollBy(6) // anchor: item 0 with 6 lines hidden above the viewport
	if first := strings.Split(l.Render(), "\n")[0]; first != "big-6" {
		t.Fatalf("setup: first visible line = %q, want big-6", first)
	}

	big.lines = big.lines[:2] // the anchor item shrinks under the offset
	big.Bump()

	if l.AtBottom() {
		t.Fatal("AtBottom() spuriously true after the anchor item shrank")
	}
	if off, total := l.Offset(), l.TotalHeight(); off > total {
		t.Fatalf("Offset() = %d > TotalHeight() = %d", off, total)
	}
	if first := strings.Split(l.Render(), "\n")[0]; first != "big-1" {
		t.Fatalf("anchor item skipped after shrink: first visible line %q, want big-1", first)
	}

	// All content is still reachable by scrolling from the top.
	l.GotoTop()
	seen := l.Render()
	for i := 0; !l.AtBottom() && i < 100; i++ {
		l.ScrollBy(1)
		seen += "\n" + l.Render()
	}
	for _, want := range []string{"big-0", "big-1", "below-0", "below-5"} {
		if !strings.Contains(seen, want) {
			t.Fatalf("content %q unreachable after shrink", want)
		}
	}
}

// REGRESSION: New must copy the variadic slice (like SetItems) so a caller
// passing a slice via New(s...) can keep mutating s without aliasing the list.
func TestNewCopiesVariadicSlice(t *testing.T) {
	s := []Item{newCountingItem("original")}
	l := New(s...)
	l.SetSize(20, 5)
	s[0] = newCountingItem("mutated")
	if out := l.Render(); out != "original" {
		t.Fatalf("Render() = %q; caller slice mutation leaked into the list", out)
	}
}

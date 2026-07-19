package list

import "testing"

// countItem re-renders to a fixed string but counts how many times Render runs,
// so a test can observe the cache: the list must NOT call Render on a cache hit,
// and MUST call it again after InvalidateAll.
type countItem struct {
	*Versioned
	n *int
}

func (c countItem) Render(width int) string { *c.n++; return "x" }

// TestInvalidateAll proves InvalidateAll drops the render cache so the next
// Render recomputes every item — the mechanism a host uses to re-skin the whole
// list on a theme swap (which bumps no item version).
func TestInvalidateAll(t *testing.T) {
	n := 0
	it := countItem{Versioned: NewVersioned(), n: &n}
	l := New(it)
	l.SetSize(10, 5)

	_ = l.Render()
	_ = l.Render()
	if n != 1 {
		t.Fatalf("stable item rendered %d times, want 1 (cache miss)", n)
	}

	l.InvalidateAll()
	_ = l.Render()
	if n != 2 {
		t.Fatalf("after InvalidateAll rendered %d times, want 2 (cache not dropped)", n)
	}
}

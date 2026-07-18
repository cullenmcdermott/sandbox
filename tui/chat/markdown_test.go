// CANONICAL TEST — do not weaken.
package chat

import (
	"testing"

	glamour "charm.land/glamour/v2"
)

// countingPool tracks constructor calls.
type countingPool struct {
	calls int
	pool  map[int]*glamour.TermRenderer
}

func (c *countingPool) renderer(width int) *glamour.TermRenderer {
	if r, ok := c.pool[width]; ok {
		return r
	}
	c.calls++
	r, _ := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(width))
	c.pool[width] = r
	return r
}

// ORACLE: same width returns the same renderer pointer; COUNTER: constructor called once.
func TestRendererPoolMemoizesByWidth(t *testing.T) {
	cp := &countingPool{pool: make(map[int]*glamour.TermRenderer)}
	r1 := cp.renderer(80)
	r2 := cp.renderer(80)
	if r1 != r2 {
		t.Fatalf("same width returned different renderer pointers")
	}
	if cp.calls != 1 {
		t.Fatalf("expected 1 constructor call, got %d", cp.calls)
	}
}

// ORACLE: after invalidation, a new renderer is created.
func TestRendererPoolInvalidated(t *testing.T) {
	cp := &countingPool{pool: make(map[int]*glamour.TermRenderer)}
	r1 := cp.renderer(80)
	cp.pool = make(map[int]*glamour.TermRenderer) // simulate InvalidateRenderers
	r2 := cp.renderer(80)
	if r1 == r2 {
		t.Fatalf("after invalidate, expected a new renderer pointer")
	}
	if cp.calls != 2 {
		t.Fatalf("expected 2 constructor calls, got %d", cp.calls)
	}
}

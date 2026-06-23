package chat

import (
	"sync"

	glamour "charm.land/glamour/v2"
)

var (
	pool   = make(map[int]*glamour.TermRenderer)
	poolMu sync.RWMutex
)

// MarkdownRenderer returns a memoized glamour renderer for the given width.
func MarkdownRenderer(width int) *glamour.TermRenderer {
	poolMu.RLock()
	if r, ok := pool[width]; ok {
		poolMu.RUnlock()
		return r
	}
	poolMu.RUnlock()

	poolMu.Lock()
	defer poolMu.Unlock()
	if r, ok := pool[width]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		// Fallback: return a zero-value renderer (caller should handle nil).
		return nil
	}
	pool[width] = r
	return r
}

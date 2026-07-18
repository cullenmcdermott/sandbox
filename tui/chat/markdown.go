package chat

import (
	"sync"

	glamour "charm.land/glamour/v2"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

var (
	pool   = make(map[int]*glamour.TermRenderer)
	poolMu sync.RWMutex
)

// init drops the renderer pool whenever the theme swaps so a /theme change
// re-skins markdown immediately. OnChange also runs once now (a no-op on the
// empty pool); the renderers are (re)built lazily on the next MarkdownRenderer
// call against the active palette.
func init() { theme.OnChange(InvalidateRenderers) }

// MarkdownRenderer returns a memoized glamour renderer for the given width. The
// renderer paints with the theme-derived style (themedStyleConfig) so markdown
// coloring tracks the active palette instead of glamour's stock dark ANSI.
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
		glamour.WithStyles(themedStyleConfig()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		// Fallback: return a zero-value renderer (caller should handle nil).
		return nil
	}
	pool[width] = r
	return r
}

// InvalidateRenderers drops every pooled renderer so the next MarkdownRenderer
// call rebuilds against the current theme. Registered as a theme.OnChange hook
// (above); also safe to call directly. The streaming renderer reuses this pool,
// so it inherits the reskin for free.
func InvalidateRenderers() {
	poolMu.Lock()
	defer poolMu.Unlock()
	pool = make(map[int]*glamour.TermRenderer)
}

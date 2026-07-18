package chat

import "testing"

// A1: a /theme swap must not serve stale-colored markdown. MarkdownRenderer
// memoizes per width; InvalidateRenderers (registered as a theme.OnChange hook)
// drops the pool so the next render rebuilds against the new palette. Oracle:
// same width memoizes to one pointer; counter: after invalidation the pointer
// differs (a fresh renderer carrying the current themedStyleConfig).
func TestInvalidateRenderersDropsPool(t *testing.T) {
	r1 := MarkdownRenderer(80)
	if r1 == nil {
		t.Fatal("MarkdownRenderer returned nil")
	}
	if MarkdownRenderer(80) != r1 {
		t.Fatal("MarkdownRenderer did not memoize by width")
	}

	InvalidateRenderers()

	if r2 := MarkdownRenderer(80); r2 == r1 {
		t.Fatal("InvalidateRenderers did not drop the pool — same renderer after swap")
	}
}

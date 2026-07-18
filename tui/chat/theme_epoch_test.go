package chat

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// A theme swap must invalidate the AssistantItem section caches — the second key
// slot carries the theme epoch, so the same content yields a different key after
// ApplyTheme (otherwise the old-palette ANSI would be re-served).
func TestThemeEpochInvalidatesSectionKeys(t *testing.T) {
	// Restore a known palette so a later theme-sensitive test isn't contaminated.
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	a := NewAssistantItem(&AssistantMessage{Content: "hello", Thinking: "why", Errored: true, ErrText: "boom"})
	_, ce1 := a.contentKey()
	_, te1 := a.thinkingKey()
	_, ee1 := a.errorKey()

	theme.ApplyForBackground(false) // swap → bumps the epoch

	_, ce2 := a.contentKey()
	_, te2 := a.thinkingKey()
	_, ee2 := a.errorKey()

	if ce1 == ce2 {
		t.Error("content key epoch slot did not change after theme swap")
	}
	if te1 == te2 {
		t.Error("thinking key epoch slot did not change after theme swap")
	}
	if ee1 == ee2 {
		t.Error("error key epoch slot did not change after theme swap")
	}
}

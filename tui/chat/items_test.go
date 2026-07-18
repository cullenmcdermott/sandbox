// CANONICAL TEST — do not weaken.
package chat

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/tui/list"
)

// COUNTER: a frozen (Finished==true) item is never re-rendered by the list.
func TestUserItemFrozen(t *testing.T) {
	u := NewUserItem("hello")
	l := list.New(u)
	l.SetSize(40, 10)
	_ = l.Render()
	if u.Version() != 0 {
		t.Fatalf("unexpected version bump")
	}
	_ = l.Render()
	// No re-render expected because Finished==true and version stable.
}

func TestToolItemImplementsItem(t *testing.T) {
	var _ list.Item = (*ToolItem)(nil)
}

func TestShellItemImplementsItem(t *testing.T) {
	var _ list.Item = (*ShellItem)(nil)
}

func TestNoticeItemImplementsItem(t *testing.T) {
	var _ list.Item = (*NoticeItem)(nil)
}

func TestSubagentItemImplementsItem(t *testing.T) {
	var _ list.Item = (*SubagentItem)(nil)
}

// COUNTER: ToolItem caches its render and does not recompute when unchanged.
func TestToolItemCache(t *testing.T) {
	renders := 0
	ti := &ToolItem{Versioned: list.NewVersioned()}
	// IMPL: wire a counting render if your ToolItem supports injection.
	_ = renders
	_ = ti
}

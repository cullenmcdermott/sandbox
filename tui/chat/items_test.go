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

// COUNTER: ToolItem caches its render and does not recompute (nor bump its
// version) when unchanged; a mutation bumps the version so the list re-renders.
func TestToolItemCache(t *testing.T) {
	ti := NewToolItem(&ToolCall{ID: "t", Name: "Bash", Arg: "ls", Status: ToolOK, Summary: "3 lines"})
	l := list.New(ti)
	l.SetSize(60, 10)

	v0 := ti.Version()
	first := l.Render()
	// A stable item re-renders to the exact same frame and never bumps its version
	// (so the list stays a cache hit).
	if l.Render() != first {
		t.Fatal("stable ToolItem produced a different frame on re-render")
	}
	if ti.Version() != v0 {
		t.Fatalf("stable ToolItem bumped its version: %d -> %d", v0, ti.Version())
	}

	// A real mutation bumps the version and changes the frame.
	ti.SetStatus(ToolError, "boom")
	if ti.Version() == v0 {
		t.Fatal("SetStatus did not bump the version")
	}
	if l.Render() == first {
		t.Fatal("mutated ToolItem produced an unchanged frame")
	}
}

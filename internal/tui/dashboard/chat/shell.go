package chat

import "github.com/cullenmcdermott/sandbox/tui/list"

type ShellItem struct {
	*list.Versioned
}

func NewShellItem() *ShellItem {
	return &ShellItem{Versioned: list.NewVersioned()}
}

func (s *ShellItem) Render(width int) string { return "" }
func (s *ShellItem) Finished() bool          { return true }

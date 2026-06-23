package chat

import "github.com/cullenmcdermott/sandbox/tui/list"

type SubagentItem struct {
	*list.Versioned
}

func NewSubagentItem() *SubagentItem {
	return &SubagentItem{Versioned: list.NewVersioned()}
}

func (s *SubagentItem) Render(width int) string { return "" }
func (s *SubagentItem) Finished() bool          { return true }

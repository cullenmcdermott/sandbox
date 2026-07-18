package chat

import "github.com/cullenmcdermott/sandbox/tui/list"

type ToolItem struct {
	*list.Versioned
}

func NewToolItem() *ToolItem {
	return &ToolItem{Versioned: list.NewVersioned()}
}

func (t *ToolItem) Render(width int) string { return "" }
func (t *ToolItem) Finished() bool          { return true }

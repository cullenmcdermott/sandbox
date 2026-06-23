package chat

import "github.com/cullenmcdermott/sandbox/tui/list"

type NoticeItem struct {
	*list.Versioned
}

func NewNoticeItem() *NoticeItem {
	return &NoticeItem{Versioned: list.NewVersioned()}
}

func (n *NoticeItem) Render(width int) string { return "" }
func (n *NoticeItem) Finished() bool          { return true }

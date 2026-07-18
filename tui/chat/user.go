package chat

import "github.com/cullenmcdermott/sandbox/tui/list"

type UserItem struct {
	*list.Versioned
	Text string
}

func NewUserItem(text string) *UserItem {
	return &UserItem{Versioned: list.NewVersioned(), Text: text}
}

func (u *UserItem) Render(width int) string { return "" }
func (u *UserItem) Finished() bool          { return true }

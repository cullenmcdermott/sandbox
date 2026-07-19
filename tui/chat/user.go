package chat

// user.go — the user's own prompt, rendered as the quietest element in the
// transcript: word-wrapped body under a dim "> " quote with a hanging indent, so
// a long prompt aligns as one column instead of clipping at the edge. This is the
// §2c user grammar from the production transcript, made self-contained.

import (
	"strings"

	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// UserItem is one user prompt block. It is a list.Item: it caches its render and
// only recomputes when the width, text, focus, or theme epoch changes.
type UserItem struct {
	*list.Versioned

	Text    string
	focused bool

	cache section
}

// NewUserItem builds a user prompt block.
func NewUserItem(text string) *UserItem {
	return &UserItem{Versioned: list.NewVersioned(), Text: text}
}

// SetText replaces the prompt text (e.g. an edited/queued prompt committing) and
// invalidates the cache via a version bump.
func (u *UserItem) SetText(text string) {
	if u.Text == text {
		return
	}
	u.Text = text
	u.cache.valid = false
	u.Bump()
}

// SetFocused marks the block focused (a left gutter bar) for keyboard-driven
// navigation. Bumps only on a real change so the list cache is not disturbed.
func (u *UserItem) SetFocused(b bool) {
	if u.focused == b {
		return
	}
	u.focused = b
	u.cache.valid = false
	u.Bump()
}

// Focused reports the focus state.
func (u *UserItem) Focused() bool { return u.focused }

// Render draws the quoted, wrapped prompt within width columns.
func (u *UserItem) Render(width int) string {
	if width < 1 {
		width = 1
	}
	srcHash := fnv64(u.Text)
	extra := extraKey(theme.Epoch(), flagBits(u.focused))
	if u.cache.hit(width, srcHash, extra) {
		return u.cache.out
	}
	out := fitWidth(u.render(width), width)
	u.cache.store(width, srcHash, extra, out)
	return out
}

func (u *UserItem) render(width int) string {
	w := focusWidth(width, u.focused)
	// Reserve the hanging-indent columns so the "> " prefix + wrapped text fits w.
	wrapW := w - msgIndent
	if wrapW < 1 {
		wrapW = 1
	}
	body := styUserBody.Render(wrapPlain(strings.TrimRight(u.Text, "\n"), wrapW))
	quoted := Quote(body)
	return clampFocus(quoted, u.focused)
}

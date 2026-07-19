package chat

// notice.go — a transcript notice: an info, warning, or error line that is not a
// tool call. Info is the quietest tone (a degradation hint), warning sits between
// info and error (a pod reschedule), and error is the loudest (a turn failure).
// Text is word-wrapped to the width so a long notice reads as one block. This
// mirrors the dashboard's blockInfo / blockWarn / blockError tones.

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// NoticeKind is a notice's tone.
type NoticeKind int

const (
	// NoticeInfo is a quiet informational line (muted).
	NoticeInfo NoticeKind = iota
	// NoticeWarn is a warning line (warning tone).
	NoticeWarn
	// NoticeError is an error line (coral).
	NoticeError
)

func (k NoticeKind) style() lipgloss.Style {
	switch k {
	case NoticeWarn:
		return styNoticeWarn
	case NoticeError:
		return styNoticeErr
	default:
		return styNoticeInfo
	}
}

// NoticeItem is one notice block.
type NoticeItem struct {
	*list.Versioned

	Kind    NoticeKind
	Text    string
	focused bool

	cache section
}

// NewNoticeItem builds a notice of the given tone.
func NewNoticeItem(kind NoticeKind, text string) *NoticeItem {
	return &NoticeItem{Versioned: list.NewVersioned(), Kind: kind, Text: text}
}

// SetText replaces the notice text.
func (n *NoticeItem) SetText(text string) {
	if n.Text == text {
		return
	}
	n.Text = text
	n.cache.valid = false
	n.Bump()
}

// SetFocused marks the notice focused (a left gutter bar).
func (n *NoticeItem) SetFocused(b bool) {
	if n.focused == b {
		return
	}
	n.focused = b
	n.cache.valid = false
	n.Bump()
}

// Focused reports the focus state.
func (n *NoticeItem) Focused() bool { return n.focused }

// Render draws the wrapped notice within width columns.
func (n *NoticeItem) Render(width int) string {
	if width < 1 {
		width = 1
	}
	srcHash := fnvFields([]byte(n.Text), u64b(uint64(n.Kind)))
	extra := extraKey(theme.Epoch(), flagBits(n.focused))
	if n.cache.hit(width, srcHash, extra) {
		return n.cache.out
	}
	out := fitWidth(n.render(width), width)
	n.cache.store(width, srcHash, extra, out)
	return out
}

func (n *NoticeItem) render(width int) string {
	w := focusWidth(width, n.focused)
	body := n.Kind.style().Render(wrapPlain(strings.TrimRight(n.Text, "\n"), w))
	return clampFocus(body, n.focused)
}

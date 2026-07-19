package chat

// shell.go — a verbatim, pre-styled multi-line block: a one-shot `!` shell
// result, or a calm "⎿  <text>" elbow notice (an interruption / attached system
// note). The text is rendered as given — sanitized for in-frame display, tabs
// expanded, basic ANSI remapped onto the palette, and each line width-clamped —
// so arbitrary captured output can never smear or overflow the transcript. This
// is the dashboard's blockShell tone (render verbatim) plus its appendElbowNotice
// idiom, made self-contained.

import (
	"strings"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// ShellItem is a verbatim pre-styled block.
type ShellItem struct {
	*list.Versioned

	Text    string
	focused bool

	cache section
}

// NewShellItem builds a verbatim block from pre-styled (or plain) text.
func NewShellItem(text string) *ShellItem {
	return &ShellItem{Versioned: list.NewVersioned(), Text: text}
}

// NewElbowNotice builds a coral "⎿  <text>" elbow notice — the calm-chrome idiom
// for an interruption or attached system note (it reads as attached under the
// block above it), replacing bracketed "[interrupted]"-style debug lines.
func NewElbowNotice(text string) *ShellItem {
	return NewShellItem(styElbowCoral.Render(toolElbow + "  " + text))
}

// SetText replaces the block text.
func (s *ShellItem) SetText(text string) {
	if s.Text == text {
		return
	}
	s.Text = text
	s.cache.valid = false
	s.Bump()
}

// SetFocused marks the block focused (a left gutter bar).
func (s *ShellItem) SetFocused(b bool) {
	if s.focused == b {
		return
	}
	s.focused = b
	s.cache.valid = false
	s.Bump()
}

// Focused reports the focus state.
func (s *ShellItem) Focused() bool { return s.focused }

// Render draws the sanitized, width-clamped block within width columns.
func (s *ShellItem) Render(width int) string {
	if width < 1 {
		width = 1
	}
	srcHash := fnv64(s.Text)
	extra := extraKey(theme.Epoch(), flagBits(s.focused))
	if s.cache.hit(width, srcHash, extra) {
		return s.cache.out
	}
	out := s.render(width)
	s.cache.store(width, srcHash, extra, out)
	return out
}

func (s *ShellItem) render(width int) string {
	w := focusWidth(width, s.focused)
	sanitized := sanitizeToolOutput(strings.TrimRight(s.Text, "\n"))
	lines := strings.Split(sanitized, "\n")
	for i, l := range lines {
		lines[i] = truncate(kit.RemapANSI(expandTabs(l)), w)
	}
	return clampFocus(strings.Join(lines, "\n"), s.focused)
}

package chat

// reasoning.go — a thinking/reasoning block rendered as a muted "∴ Thought"
// (committed) or "∴ Thinking" (live) section. A single raw line renders as a
// compact inline summary ("∴ Thought: …"); a multi-line think shows an italic
// muted body capped at reasoningCapLines. A committed collapse caps the FIRST
// lines with a "… +N lines (ctrl+o)" trailer; a live think tails the LAST lines
// (terminal-style) with a "… +N earlier lines" marker. This is the §2b/§2c
// reasoning grammar from the production transcript, made self-contained.
//
// The "∴" glyph keeps the transcript's geometric glyph vocabulary (◐◆❯○▌◇…);
// emoji broke the set and rendered double-width in some terminals.

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// reasoningCapLines is how many wrapped body lines a thinking block shows before
// it caps — the same window committed or live so the block keeps one shape.
const reasoningCapLines = 6

// ReasoningItem is a thinking/reasoning block.
type ReasoningItem struct {
	*list.Versioned

	Text      string
	Streaming bool // live "∴ Thinking" (tail newest) vs committed "∴ Thought" (head)
	expanded  bool
	focused   bool

	cache section
}

// NewReasoningItem builds a reasoning block. Streaming=true renders the live
// "∴ Thinking" form (tails the newest lines); false renders the committed form.
func NewReasoningItem(text string, streaming bool) *ReasoningItem {
	return &ReasoningItem{Versioned: list.NewVersioned(), Text: text, Streaming: streaming}
}

func (r *ReasoningItem) invalidate() {
	r.cache.valid = false
	r.Bump()
}

// SetText replaces the reasoning text (e.g. a streaming delta appending).
func (r *ReasoningItem) SetText(text string) {
	if r.Text == text {
		return
	}
	r.Text = text
	r.invalidate()
}

// SetStreaming flips between the live and committed forms (e.g. on
// reasoning.completed).
func (r *ReasoningItem) SetStreaming(b bool) {
	if r.Streaming == b {
		return
	}
	r.Streaming = b
	r.invalidate()
}

// SetExpanded toggles the ctrl+o expansion (committed form only; a live think
// always tails).
func (r *ReasoningItem) SetExpanded(b bool) {
	if r.expanded == b {
		return
	}
	r.expanded = b
	r.invalidate()
}

// Expanded reports the expansion state.
func (r *ReasoningItem) Expanded() bool { return r.expanded }

// SetFocused marks the block focused (a left gutter bar).
func (r *ReasoningItem) SetFocused(b bool) {
	if r.focused == b {
		return
	}
	r.focused = b
	r.invalidate()
}

// Focused reports the focus state.
func (r *ReasoningItem) Focused() bool { return r.focused }

// Render draws the reasoning block within width columns.
func (r *ReasoningItem) Render(width int) string {
	if width < 1 {
		width = 1
	}
	srcHash := fnv64(r.Text)
	extra := extraKey(theme.Epoch(), flagBits(r.Streaming, r.expanded, r.focused))
	if r.cache.hit(width, srcHash, extra) {
		return r.cache.out
	}
	out := fitWidth(clampFocus(r.render(focusWidth(width, r.focused)), r.focused), width)
	r.cache.store(width, srcHash, extra, out)
	return out
}

func (r *ReasoningItem) render(width int) string {
	text := strings.TrimSpace(r.Text)
	labelText := "∴ Thought"
	if r.Streaming {
		labelText = "∴ Thinking"
	}
	label := styReasonLabel.Render(labelText)
	if text == "" {
		return truncate(label, width) // a live think that has produced no text yet
	}
	if !r.Streaming && strings.Count(text, "\n") == 0 {
		// Compact inline summary for a one-line committed think — but only when it
		// actually fits; a long single line falls through to the wrapped form.
		if inline := label + styReasonBody.Render(": "+text); lipgloss.Width(inline) <= width {
			return inline
		}
	}

	// Wrapped body: wrapPlain guarantees each line ≤ width; the label and trailer
	// are truncated as a backstop for very narrow widths.
	lines := strings.Split(styReasonBody.Render(wrapPlain(text, width)), "\n")
	out := []string{truncate(label, width)}
	if r.Streaming {
		// Live: tail the LAST reasoningCapLines with a "… +N earlier lines" marker.
		if len(lines) > reasoningCapLines {
			hidden := len(lines) - reasoningCapLines
			out = append(out, truncate(styReasonTrail.Render("… +"+formatInt(hidden)+" earlier lines"), width))
			lines = lines[len(lines)-reasoningCapLines:]
		}
		out = append(out, lines...)
		return strings.Join(out, "\n")
	}
	// Committed: cap the FIRST reasoningCapLines with a "… +N lines (ctrl+o)"
	// trailer unless expanded.
	if r.expanded || len(lines) <= reasoningCapLines {
		out = append(out, lines...)
		return strings.Join(out, "\n")
	}
	hidden := len(lines) - reasoningCapLines
	out = append(out, lines[:reasoningCapLines]...)
	out = append(out, truncate(styReasonTrail.Render("… +"+formatInt(hidden)+" lines (ctrl+o)"), width))
	return strings.Join(out, "\n")
}

// Expandable reports whether ctrl+o would reveal hidden lines at the given width
// (committed multi-line think only).
func (r *ReasoningItem) Expandable(width int) bool {
	text := strings.TrimSpace(r.Text)
	if r.Streaming || text == "" || strings.Count(text, "\n") == 0 {
		return false
	}
	w := focusWidth(width, r.focused)
	lines := strings.Split(styReasonBody.Render(wrapPlain(text, w)), "\n")
	return len(lines) > reasoningCapLines
}

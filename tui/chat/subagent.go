package chat

// subagent.go — a dispatched Task subagent rendered as an expandable card:
//
//	⊟ Task  investigate flake  · Explore · 3 tools  ✓
//	  ├ ✓ Grep  spdy  · 7 matches
//	  ├ ✓ Read  client.go  · 120 lines
//	  └ ◐ Bash  go test ./…  · running
//	     └ found the culprit in the reconnect path
//
// The header carries the Task label, prompt, agent name, tool count, an optional
// live elapsed clock, and a status glyph; its children are the subagent's own
// tool calls (reusing ToolCall) shown as an indented tree; the last line is the
// subagent's latest utterance (narration). Collapsed, only the header shows. This
// is the slice-4b nested-subagent grammar from the production transcript, made
// self-contained.

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/list"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// Subagent is the protocol-neutral data behind a SubagentItem.
type Subagent struct {
	// ID is a stable identifier (the Task tool_use id).
	ID string
	// AgentName is the dispatched agent's name ("Explore", "builder", …).
	AgentName string
	// Prompt is the Task's short label (its description or first prompt line).
	Prompt string
	// Children are the subagent's own tool calls, rendered as a nested tree.
	Children []*ToolCall
	// Status is the Task lifecycle (running until the Task tool result).
	Status ToolStatus
	// Narration is the subagent's latest utterance (its streamed reply); only the
	// last non-empty line is shown, as one italic line under the child tree.
	Narration string
	// Elapsed is the running duration, shown as "· 12s" on the header once ~2s.
	Elapsed time.Duration
}

// SubagentItem renders a Subagent as a list.Item.
type SubagentItem struct {
	*list.Versioned

	sub          *Subagent
	collapsed    bool
	focused      bool
	spinnerFrame int

	cache section
}

// NewSubagentItem builds a Task card for the given subagent.
func NewSubagentItem(sub *Subagent) *SubagentItem {
	return &SubagentItem{Versioned: list.NewVersioned(), sub: sub}
}

// Subagent returns the underlying data for read/in-place mutation. Call Bump
// after an in-place mutation.
func (s *SubagentItem) Subagent() *Subagent { return s.sub }

func (s *SubagentItem) invalidate() {
	s.cache.valid = false
	s.Bump()
}

// AddChild appends a child tool call to the subagent tree.
func (s *SubagentItem) AddChild(c *ToolCall) {
	if s.sub == nil || c == nil {
		return
	}
	s.sub.Children = append(s.sub.Children, c)
	s.invalidate()
}

// SetStatus updates the Task lifecycle state.
func (s *SubagentItem) SetStatus(status ToolStatus) {
	if s.sub == nil || s.sub.Status == status {
		return
	}
	s.sub.Status = status
	s.invalidate()
}

// SetNarration updates the subagent's latest-utterance line.
func (s *SubagentItem) SetNarration(text string) {
	if s.sub == nil || s.sub.Narration == text {
		return
	}
	s.sub.Narration = text
	s.invalidate()
}

// SetElapsed updates the running elapsed clock (bumps on a whole-second change).
func (s *SubagentItem) SetElapsed(d time.Duration) {
	if s.sub == nil {
		return
	}
	if int(s.sub.Elapsed.Seconds()) == int(d.Seconds()) {
		s.sub.Elapsed = d
		return
	}
	s.sub.Elapsed = d
	s.invalidate()
}

// SetSpinnerFrame advances the running spinner/child-icon frame (host-driven, so
// motion is opt-in and goldens stay deterministic at frame 0).
func (s *SubagentItem) SetSpinnerFrame(frame int) {
	if s.spinnerFrame == frame {
		return
	}
	s.spinnerFrame = frame
	s.invalidate()
}

// SetCollapsed collapses the card to its header only.
func (s *SubagentItem) SetCollapsed(b bool) {
	if s.collapsed == b {
		return
	}
	s.collapsed = b
	s.invalidate()
}

// Collapsed reports the collapse state.
func (s *SubagentItem) Collapsed() bool { return s.collapsed }

// SetFocused marks the card focused (a left gutter bar).
func (s *SubagentItem) SetFocused(b bool) {
	if s.focused == b {
		return
	}
	s.focused = b
	s.invalidate()
}

// Focused reports the focus state.
func (s *SubagentItem) Focused() bool { return s.focused }

// Render draws the Task card within width columns.
func (s *SubagentItem) Render(width int) string {
	if s.sub == nil {
		return ""
	}
	if width < 4 {
		width = 4
	}
	sub := s.sub
	// The child statuses/args + narration + elapsed drive the hash.
	fields := [][]byte{
		[]byte(sub.ID),
		[]byte(sub.AgentName),
		[]byte(sub.Prompt),
		[]byte(sub.Narration),
		u64b(uint64(sub.Status)),
		u64b(uint64(int64(sub.Elapsed.Seconds()))),
		u64b(uint64(s.spinnerFrame)),
	}
	for _, c := range sub.Children {
		if c == nil {
			continue
		}
		fields = append(fields,
			[]byte(c.Name), []byte(c.Arg), []byte(c.Summary), u64b(uint64(c.Status)))
	}
	srcHash := fnvFields(fields...)
	extra := extraKey(theme.Epoch(), flagBits(s.collapsed, s.focused))
	if s.cache.hit(width, srcHash, extra) {
		return s.cache.out
	}
	out := clampFocus(s.renderCard(focusWidth(width, s.focused)), s.focused)
	s.cache.store(width, srcHash, extra, out)
	return out
}

func (s *SubagentItem) renderCard(width int) string {
	sub := s.sub
	glyph := "⊟"
	if s.collapsed {
		glyph = "⊞"
	}
	agent := sub.AgentName
	if agent == "" {
		agent = "subagent"
	}

	header := stySubHeader.Render(glyph+" Task") + "  " +
		stySubPrompt.Render(truncate(sub.Prompt, max(8, width/2))) +
		stySubMeta.Render("  · "+agent+" · "+formatInt(len(sub.Children))+" tools")

	switch sub.Status {
	case ToolRunning:
		if sub.Elapsed >= elapsedClockMin {
			header += stySubMeta.Render(" · " + fmtElapsed(sub.Elapsed))
		}
		header += " " + theme.SpinnerFrame(s.spinnerFrame)
	case ToolOK:
		header += " " + stySubOK.Render("✓")
	case ToolError:
		header += " " + stySubErr.Render("✗")
	}
	if lipgloss.Width(header) > width {
		header = truncate(header, width)
	}

	if s.collapsed {
		return header
	}

	lines := []string{header}
	narr := lastNonEmptyLine(sub.Narration)
	// Filter out nil children (a host mutates the exported Children slice
	// directly) so the tree render and the last-child "└" branch stay correct.
	children := make([]*ToolCall, 0, len(sub.Children))
	for _, c := range sub.Children {
		if c != nil {
			children = append(children, c)
		}
	}
	for i, c := range children {
		branch := "├"
		if i == len(children)-1 && narr == "" {
			branch = "└"
		}
		lines = append(lines, s.renderChildTool(branch, c, width))
	}
	if narr != "" {
		line := stySubTree.Render("   └ ") +
			stySubNarr.Render(truncate(narr, max(1, width-6)))
		if lipgloss.Width(line) > width {
			line = truncate(line, width)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// renderChildTool renders one indented child tool line:
//
//	├ ✓ Grep  spdy  · 7 matches
//
// Width is budgeted by construction like the top-level card: the tree chrome +
// status icon + name take what they need, then the arg and result each get
// whatever columns are left, with a final truncate backstop.
func (s *SubagentItem) renderChildTool(branch string, c *ToolCall, width int) string {
	if width < 4 {
		width = 4
	}
	var iconStyled string
	switch c.Status {
	case ToolOK:
		iconStyled = stySubIconOK.Render("✓")
	case ToolError:
		iconStyled = stySubIconErr.Render("✗")
	default: // running
		iconStyled = theme.SpinnerFrame(s.spinnerFrame)
	}

	line := stySubTree.Render("   "+branch+" ") + iconStyled + " "
	used := lipgloss.Width(line)

	nameStr := c.Name
	if nameStr == "" {
		nameStr = "tool"
	}
	name := truncate(nameStr, max(1, width-used))
	line += stySubChildNm.Render(name)
	used += lipgloss.Width(name)

	if c.Arg != "" {
		const sep = "  "
		avail := width - used - len(sep)
		if avail >= 3 {
			a := truncate(collapseSpaces(c.Arg), avail)
			line += stySubChildArg.Render(sep + a)
			used += len(sep) + lipgloss.Width(a)
		}
	}

	detail := ""
	switch {
	case c.Summary != "":
		detail = "· " + c.Summary
	case c.Status == ToolRunning:
		detail = "· running"
	}
	if detail != "" {
		const sep = "  "
		avail := width - used - len(sep)
		if avail >= 3 {
			line += stySubChildArg.Render(sep + truncate(detail, avail))
		}
	}

	if lipgloss.Width(line) > width {
		line = truncate(line, width)
	}
	return line
}

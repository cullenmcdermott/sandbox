package dashboard

// subagent.go — nested rendering of dispatched Task subagents (slice 4b /
// Mockup A renderSubagent). A `Task` tool call renders as an expandable card
// (⊟ Task <prompt> · <agent> · N tools <status>) with the subagent's own tool
// calls shown as an indented child tree, keyed off the toolUseId/parentToolUseId
// ids the runner threads through tool events (slice 4a). Parallel Tasks render
// as several cards. Child tool cards stay static like flat cards; the header +
// in-flight child spinner animate only while a subagent runs (a bounded cost).

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// subagentCard is a dispatched Task: a header plus the subagent's child tool
// calls as a nested tree. status is toolRunning until the Task tool result.
type subagentCard struct {
	toolUseID string
	agentName string
	prompt    string
	children  []*toolCard
	collapsed bool
	status    toolStatus
	// card is the list card that renders this subagent (the Task header + its child
	// tree). Mutating the subagent (a new child, a status change, collapse) bumps
	// card's version so the list re-renders.
	card *blockCard
}

// startSubagent creates (once) a subagent card for a Task dispatch. tool.started
// is emitted twice (streaming + full message), so creation is deduped by id.
func (m *TranscriptModel) startSubagent(p session.ToolPayload) {
	if p.ToolUseID == "" || m.subagents[p.ToolUseID] != nil {
		return
	}
	sub := &subagentCard{
		toolUseID: p.ToolUseID,
		agentName: p.AgentName,
		prompt:    taskPrompt(p.Input),
		status:    toolRunning,
	}
	if m.subagents == nil {
		m.subagents = map[string]*subagentCard{}
	}
	m.subagents[p.ToolUseID] = sub
	card := m.newBlockCard(blockSubagent, "")
	card.sub = sub
	sub.card = card
	m.blocks = append(m.blocks, card)
	m.syncItems()
}

// startSubagentChild nests a child tool under its parent Task card (deduped).
func (m *TranscriptModel) startSubagentChild(p session.ToolPayload) {
	sub := m.subagents[p.ParentToolUseID]
	if sub == nil {
		return
	}
	if m.childIndex == nil {
		m.childIndex = map[string]*toolCard{}
	}
	if p.ToolUseID != "" && m.childIndex[p.ToolUseID] != nil {
		return
	}
	child := &toolCard{tool: p.Tool, arg: toolArg(p.Tool, p.Input), status: toolRunning, card: sub.card}
	sub.children = append(sub.children, child)
	if p.ToolUseID != "" {
		m.childIndex[p.ToolUseID] = child
	}
	sub.card.Bump()
	m.syncItems()
}

// finishNested resolves a Task completion or a subagent child completion. It
// returns true when p belongs to a subagent (so the flat FIFO path is skipped).
func (m *TranscriptModel) finishNested(p session.ToolPayload, status toolStatus, summary string) bool {
	if p.ToolUseID != "" {
		if sub := m.subagents[p.ToolUseID]; sub != nil {
			sub.status = status
			sub.card.Bump()
			m.syncItems()
			return true
		}
		if child := m.childIndex[p.ToolUseID]; child != nil {
			child.status = status
			child.summary = summary
			if child.card != nil {
				child.card.Bump()
			}
			m.syncItems()
			return true
		}
	}
	// Defensive: a result that only names its parent still belongs to that
	// subagent — resolve the oldest running child rather than popping a flat card.
	if p.ParentToolUseID != "" {
		if sub := m.subagents[p.ParentToolUseID]; sub != nil {
			for _, c := range sub.children {
				if c.status == toolRunning {
					c.status = status
					c.summary = summary
					break
				}
			}
			sub.card.Bump()
			m.syncItems()
			return true
		}
	}
	return false
}

// toggleSubagents collapses/expands every subagent card (space on an empty
// prompt). Per-card collapse needs transcript card-navigation (slice 5i); a
// global toggle is the smallest correct alternative here. Returns whether any
// card was toggled.
func (m *TranscriptModel) toggleSubagents() bool {
	if len(m.subagents) == 0 {
		return false
	}
	// Collapse all if any is expanded, else expand all.
	anyExpanded := false
	for _, b := range m.blocks {
		if b.kind == blockSubagent && b.sub != nil && !b.sub.collapsed {
			anyExpanded = true
			break
		}
	}
	for _, b := range m.blocks {
		if b.kind == blockSubagent && b.sub != nil && b.sub.collapsed != anyExpanded {
			b.sub.collapsed = anyExpanded
			b.Bump()
		}
	}
	m.syncItems()
	return true
}

// taskPrompt extracts a short label from a Task tool's input (its description,
// falling back to the first line of the prompt).
func taskPrompt(input json.RawMessage) string {
	var t struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	_ = json.Unmarshal(input, &t)
	if t.Description != "" {
		return t.Description
	}
	return firstLine(t.Prompt)
}

// renderSubagentCard renders the Task header and, when expanded, its child tool
// tree (adapted from the original statusline renderSubagent prototype).
func (m *TranscriptModel) renderSubagentCard(sub *subagentCard, width int) string {
	glyph := "⊟"
	if sub.collapsed {
		glyph = "⊞"
	}
	agent := sub.agentName
	if agent == "" {
		agent = "subagent"
	}

	header := lipgloss.NewStyle().Foreground(theme.Hazy).Bold(true).Render(glyph+" Task") + "  " +
		lipgloss.NewStyle().Foreground(theme.TextBody).Render(truncate(sub.prompt, max(8, width/2))) +
		lipgloss.NewStyle().Foreground(theme.TextMuted).Render(fmt.Sprintf("  · %s · %d tools", agent, len(sub.children)))

	switch sub.status {
	case toolRunning:
		header += " " + theme.SpinnerFrame(m.workFrame)
	case toolOK:
		header += " " + lipgloss.NewStyle().Foreground(theme.Guac).Render("✓")
	case toolErr:
		header += " " + lipgloss.NewStyle().Foreground(theme.Coral).Render("✗")
	}

	if sub.collapsed {
		return header
	}
	lines := []string{header}
	for i, c := range sub.children {
		branch := "├"
		if i == len(sub.children)-1 {
			branch = "└"
		}
		lines = append(lines, m.renderChildTool(branch, c, width))
	}
	return strings.Join(lines, "\n")
}

// renderChildTool renders one indented child tool line of a subagent card.
func (m *TranscriptModel) renderChildTool(branch string, c *toolCard, width int) string {
	var icon string
	iconStyle := lipgloss.NewStyle().Foreground(theme.Malibu)
	switch c.status {
	case toolRunning:
		icon = theme.SpinnerFrame(m.workFrame)
		iconStyle = lipgloss.NewStyle() // pre-colored; no extra styling
	case toolOK:
		icon = "✓"
		iconStyle = lipgloss.NewStyle().Foreground(theme.Guac)
	case toolErr:
		icon = "✗"
		iconStyle = lipgloss.NewStyle().Foreground(theme.Coral)
	}
	// A2.4 (Calm), same treatment as the flat tool card: name TextSecondary
	// (not bold Malibu), arg TextMuted — only the status icon keeps its color.
	line := lipgloss.NewStyle().Foreground(theme.TextDim).Render("   "+branch+" ") +
		iconStyle.Render(icon) + " " +
		lipgloss.NewStyle().Foreground(theme.TextSecondary).Render(c.tool)
	if c.arg != "" {
		line += "  " + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(truncate(c.arg, max(8, width/2)))
	}
	switch {
	case c.summary != "":
		line += lipgloss.NewStyle().Foreground(theme.TextMuted).Render("  · " + truncate(c.summary, max(8, width/3)))
	case c.status == toolRunning:
		line += lipgloss.NewStyle().Foreground(theme.TextMuted).Render("  · running")
	}
	return line
}

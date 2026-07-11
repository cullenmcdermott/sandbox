package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func toolEvent(typ session.EventType, p session.ToolPayload) session.Event {
	b, _ := json.Marshal(p)
	return session.Event{Type: typ, Payload: b}
}

func TestSubagentNesting(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 100, 30

	task := session.ToolPayload{Tool: "Task", ToolUseID: "tu_1", AgentName: "general-purpose", Input: json.RawMessage(`{"description":"map SPDY usage"}`)}
	m.handleEvent(toolEvent(session.EventToolStarted, task))
	m.handleEvent(toolEvent(session.EventToolStarted, task)) // duplicate (streaming + full) deduped

	subBlocks := 0
	for _, b := range m.blocks {
		if b.kind == blockSubagent {
			subBlocks++
		}
	}
	if subBlocks != 1 {
		t.Fatalf("expected 1 subagent block, got %d", subBlocks)
	}
	sub := m.subagents["tu_1"]
	if sub == nil {
		t.Fatal("Task did not register a subagent card")
	}

	// Two nested children (one duplicated).
	child1 := session.ToolPayload{Tool: "Grep", ToolUseID: "tu_2", ParentToolUseID: "tu_1", Input: json.RawMessage(`{"pattern":"spdy"}`)}
	child2 := session.ToolPayload{Tool: "Read", ToolUseID: "tu_3", ParentToolUseID: "tu_1", Input: json.RawMessage(`{"file_path":"a.go"}`)}
	m.handleEvent(toolEvent(session.EventToolStarted, child1))
	m.handleEvent(toolEvent(session.EventToolStarted, child1))
	m.handleEvent(toolEvent(session.EventToolStarted, child2))
	if len(sub.children) != 2 {
		t.Fatalf("nested children = %d, want 2", len(sub.children))
	}

	// A flat tool (no parent) still renders as a flat card.
	m.handleEvent(toolEvent(session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "tu_flat", Input: json.RawMessage(`{"command":"ls"}`)}))
	flat := 0
	for _, b := range m.blocks {
		if b.kind == blockToolCard {
			flat++
		}
	}
	if flat != 1 {
		t.Errorf("flat tool not rendered as a flat card: %d", flat)
	}

	// Child completion updates that child (not a flat card via FIFO).
	m.handleEvent(toolEvent(session.EventToolCompleted, session.ToolPayload{ToolUseID: "tu_2", ParentToolUseID: "tu_1", Output: "7 matches"}))
	if sub.children[0].status != toolOK {
		t.Errorf("child Grep not marked ok")
	}
	// Task completion marks the subagent done.
	m.handleEvent(toolEvent(session.EventToolCompleted, session.ToolPayload{ToolUseID: "tu_1", Output: "done"}))
	if sub.status != toolOK {
		t.Errorf("subagent not marked ok")
	}

	out := m.renderSubagentCard(sub, 100)
	for _, want := range []string{"Task", "map SPDY usage", "general-purpose", "Grep", "Read"} {
		if !strings.Contains(out, want) {
			t.Errorf("subagent render missing %q:\n%s", want, out)
		}
	}

	// Collapse hides the children; expand restores them.
	m.toggleSubagents()
	if !sub.collapsed {
		t.Fatal("toggle did not collapse")
	}
	if strings.Contains(m.renderSubagentCard(sub, 100), "Grep") {
		t.Errorf("collapsed card still shows children")
	}
	m.toggleSubagents()
	if sub.collapsed {
		t.Errorf("toggle did not expand")
	}
}

func TestSubagentParallelNestUnderCorrectParent(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 100, 30

	m.handleEvent(toolEvent(session.EventToolStarted, session.ToolPayload{Tool: "Task", ToolUseID: "tu_a", AgentName: "alpha", Input: json.RawMessage(`{"description":"A"}`)}))
	m.handleEvent(toolEvent(session.EventToolStarted, session.ToolPayload{Tool: "Task", ToolUseID: "tu_b", AgentName: "beta", Input: json.RawMessage(`{"description":"B"}`)}))

	// Children arrive interleaved; each must nest under its own Task by id.
	m.handleEvent(toolEvent(session.EventToolStarted, session.ToolPayload{Tool: "Read", ToolUseID: "tu_b1", ParentToolUseID: "tu_b", Input: json.RawMessage(`{"file_path":"b.go"}`)}))
	m.handleEvent(toolEvent(session.EventToolStarted, session.ToolPayload{Tool: "Grep", ToolUseID: "tu_a1", ParentToolUseID: "tu_a", Input: json.RawMessage(`{"pattern":"x"}`)}))

	a, b := m.subagents["tu_a"], m.subagents["tu_b"]
	if a == nil || b == nil {
		t.Fatal("both Task cards should exist")
	}
	if len(a.children) != 1 || a.children[0].tool != "Grep" {
		t.Errorf("tu_a children = %+v, want [Grep]", a.children)
	}
	if len(b.children) != 1 || b.children[0].tool != "Read" {
		t.Errorf("tu_b children = %+v, want [Read]", b.children)
	}
}

// TestSubagentChildToolWidthSafe pins §1c: a subagent child tool line — with a
// long arg AND a long result summary — must never exceed the render width, at any
// terminal width (the old arg≤w/2 + summary≤w/3 budgeting could sum past it).
func TestSubagentChildToolWidthSafe(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)

	longArg := "internal/tui/dashboard/very/deep/path/to/some/file/that/keeps/going.go"
	longSummary := "142 matches across 37 files in the workspace and then even more text"
	children := []*toolCard{
		{tool: "Grep", arg: longArg, summary: longSummary, status: toolOK},
		{tool: "Bash", arg: longArg, status: toolRunning},                       // running detail path
		{tool: "MultiEditWithAVeryLongToolName", arg: longArg, status: toolErr}, // name floods the budget
		{tool: "", arg: "", status: toolOK},                                     // degenerate: no name/arg
	}
	for _, width := range []int{8, 12, 20, 30, 40, 80} {
		m.width, m.height = width, 30
		for _, c := range children {
			line := m.renderChildTool("├", c, width)
			if strings.Contains(line, "\n") {
				t.Errorf("width=%d tool=%q: child line contains a newline", width, c.tool)
			}
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width=%d tool=%q: rendered width %d exceeds budget\n%q", width, c.tool, got, line)
			}
		}
	}
}

func TestSubagentSpaceToggleKey(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 100, 30
	m.handleEvent(toolEvent(session.EventToolStarted, session.ToolPayload{Tool: "Task", ToolUseID: "tu_1", AgentName: "x", Input: json.RawMessage(`{"description":"d"}`)}))

	// space on an empty prompt collapses the cards.
	m.handleKey(keyMsg(" "))
	if !m.subagents["tu_1"].collapsed {
		t.Errorf("space did not collapse the subagent card")
	}
	if m.input.Value() != "" {
		t.Errorf("space typed into the prompt instead of toggling: %q", m.input.Value())
	}
}

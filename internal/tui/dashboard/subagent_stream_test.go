package dashboard

// §2b gap 1 "Subagent output flattens into the main transcript": a running
// Task's message.*/reasoning.* events carry ParentToolUseID; the reducer must
// route them to the Task's subagentCard (narration) and never into the main
// streaming buffers — previously a subagent's narration interleaved into
// assistantBuf and corrupted the main streaming reply.

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func subagentStreamModel(t *testing.T) *TranscriptModel {
	t.Helper()
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	return m
}

// ORACLE (the corruption itself): a subagent's message.started must not reset
// the main stream buffer mid-reply, its deltas must not feed it, and its
// completed text must land on the Task card — never as a main assistant block
// or in lastAssistantText (the /goal sentinel scan).
func TestParentedMessageStreamKeptOffMainReply(t *testing.T) {
	m := subagentStreamModel(t)

	// Task dispatch creates the subagent card keyed by its tool_use id.
	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Task", ToolUseID: "task_9", AgentName: "explorer"})

	// Main reply streaming…
	sendEvent(m, session.EventMessageStarted, session.MessagePayload{Role: "assistant"})
	sendEvent(m, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "main ", Delta: true})

	// …the running Task narrates mid-reply (started used to RESET the main
	// buffer; deltas used to interleave)…
	sendEvent(m, session.EventMessageStarted, session.MessagePayload{Role: "assistant", ParentToolUseID: "task_9"})
	sendEvent(m, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "SUBAGENT NOISE", Delta: true, ParentToolUseID: "task_9"})

	// …and the main reply continues.
	sendEvent(m, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "reply", Delta: true})

	if got := m.assistantBuf.String(); got != "main reply" {
		t.Errorf("main stream buffer corrupted by subagent events: %q, want %q", got, "main reply")
	}

	sendEvent(m, session.EventMessageCompleted, session.MessagePayload{Role: "assistant", Content: "sub done", ParentToolUseID: "task_9"})
	sub := m.subagents["task_9"]
	if sub == nil {
		t.Fatal("subagent card missing after Task tool.started")
	}
	if sub.narration != "sub done" {
		t.Errorf("subagent narration = %q, want %q", sub.narration, "sub done")
	}
	if m.lastAssistantText != "" {
		t.Errorf("subagent completion must not claim lastAssistantText, got %q", m.lastAssistantText)
	}
	for _, b := range m.blocks {
		if b.kind == blockAssistant && strings.Contains(b.text, "sub done") {
			t.Error("subagent completion rendered as a main assistant block")
		}
	}
}

// A subagent's thinking must not start, feed, or flush the MAIN reasoning tail.
func TestParentedReasoningKeptOffMainTail(t *testing.T) {
	m := subagentStreamModel(t)
	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Task", ToolUseID: "task_9", AgentName: "explorer"})

	// Main think in flight.
	sendEvent(m, session.EventReasoningStarted, nil)
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "main think"})

	// Subagent think interleaves — including a completed, which used to flush
	// (and commit) the main tail.
	sendEvent(m, session.EventReasoningStarted, session.MessagePayload{ParentToolUseID: "task_9"})
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: " SUB THINK", ParentToolUseID: "task_9"})
	sendEvent(m, session.EventReasoningCompleted, session.MessagePayload{Content: "SUB THINK", ParentToolUseID: "task_9"})

	if !m.reasoning {
		t.Error("parented reasoning.completed must not end the main think")
	}
	if got := m.reasoningBuf.String(); got != "main think" {
		t.Errorf("main reasoning buffer = %q, want %q", got, "main think")
	}
	for _, b := range m.blocks {
		if b.kind == blockReasoning {
			t.Errorf("subagent think committed a main reasoning block: %q", b.text)
		}
	}
}

// Narration streams live onto the card: deltas accumulate, narrationLine shows
// the last non-empty line, the expanded card renders it width-safe.
func TestSubagentNarrationRendersLive(t *testing.T) {
	m := subagentStreamModel(t)
	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Task", ToolUseID: "task_9", AgentName: "explorer"})
	sendEvent(m, session.EventMessageStarted, session.MessagePayload{Role: "assistant", ParentToolUseID: "task_9"})
	sendEvent(m, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "scanning the tree\nfound 3 matches", Delta: true, ParentToolUseID: "task_9"})

	sub := m.subagents["task_9"]
	if got := sub.narrationLine(); got != "found 3 matches" {
		t.Errorf("narrationLine = %q, want the last non-empty line", got)
	}
	const width = 60
	out := m.renderSubagentCard(sub, width)
	if !strings.Contains(out, "found 3 matches") {
		t.Errorf("expanded card must show the live narration line:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("card line overflows: width %d > %d: %q", w, width, line)
		}
	}
	// Collapsed cards stay a single header line (no narration leak).
	sub.collapsed = true
	if out := m.renderSubagentCard(sub, width); strings.Contains(out, "found 3 matches") {
		t.Error("collapsed card must not render the narration line")
	}
}

// A parented role:user message (the Task prompt injection) is subagent-internal:
// no main user block, no narration.
func TestParentedUserEchoDropped(t *testing.T) {
	m := subagentStreamModel(t)
	sendEvent(m, session.EventToolStarted, session.ToolPayload{Tool: "Task", ToolUseID: "task_9", AgentName: "explorer"})
	before := len(m.blocks)
	sendEvent(m, session.EventMessageCompleted, session.MessagePayload{Role: "user", Content: "do the subtask", ParentToolUseID: "task_9"})
	if len(m.blocks) != before {
		t.Error("parented user echo appended a main transcript block")
	}
	if sub := m.subagents["task_9"]; sub.narration != "" {
		t.Errorf("user echo must not become narration, got %q", sub.narration)
	}
}

// A parented event whose Task card is unknown (e.g. replay raced past the
// dispatch) is dropped — never fed to the main buffers, never a panic.
func TestParentedEventWithUnknownTaskDropped(t *testing.T) {
	m := subagentStreamModel(t)
	sendEvent(m, session.EventMessageStarted, session.MessagePayload{Role: "assistant", ParentToolUseID: "ghost"})
	sendEvent(m, session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "orphan", Delta: true, ParentToolUseID: "ghost"})
	sendEvent(m, session.EventMessageCompleted, session.MessagePayload{Role: "assistant", Content: "orphan", ParentToolUseID: "ghost"})
	if got := m.assistantBuf.String(); got != "" {
		t.Errorf("orphan parented delta reached the main buffer: %q", got)
	}
	if m.lastAssistantText != "" {
		t.Errorf("orphan parented completion claimed lastAssistantText: %q", m.lastAssistantText)
	}
}

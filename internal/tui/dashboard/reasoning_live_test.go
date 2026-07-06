package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §2b gap 3 "Thinking invisible until complete": reasoning.delta used to buffer
// silently and only render on reasoning.completed, so a long think showed a bare
// spinner. The fix streams the thinking text into an ephemeral live tail (the
// same streamItem machinery as the assistant message), collapsing to the compact
// finalized "∴ Thought" summary when the block commits.

func reasoningModel(t *testing.T) *TranscriptModel {
	t.Helper()
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 60, 24
	m.layout()
	return m
}

// While a think is in flight (started + deltas, no completed yet), a live tail
// must exist, be flagged as the reasoning tail, and render the streaming thinking
// text under the "∴ Thinking" header.
func TestReasoningStreamsLiveTail(t *testing.T) {
	m := reasoningModel(t)

	sendEvent(m, session.EventReasoningStarted, nil)
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "weighing the "})
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "trade-offs"})

	if m.streamItem == nil {
		t.Fatal("a live reasoning tail must exist while thinking streams")
	}
	if !m.streamItem.streamReasoning {
		t.Error("the live tail must be flagged as the reasoning tail, not the assistant tail")
	}
	rendered := m.streamItem.Render(m.width - 1)
	if !strings.Contains(rendered, "Thinking") {
		t.Errorf("live reasoning tail must show the Thinking header, got %q", rendered)
	}
	if !strings.Contains(rendered, "weighing the trade-offs") {
		t.Errorf("live reasoning tail must stream the thinking text, got %q", rendered)
	}
	// It must NOT yet be committed as a finalized block.
	for _, b := range m.blocks {
		if b.kind == blockReasoning {
			t.Fatal("reasoning must not commit a finalized block until reasoning.completed")
		}
	}
}

// reasoning.started alone (before any delta) shows the header as a live indicator
// rather than a bare spinner.
func TestReasoningStartedShowsHeaderImmediately(t *testing.T) {
	m := reasoningModel(t)
	sendEvent(m, session.EventReasoningStarted, nil)
	if m.streamItem == nil || !m.streamItem.streamReasoning {
		t.Fatal("reasoning.started must show the live thinking tail immediately")
	}
	if !strings.Contains(m.streamItem.Render(m.width-1), "Thinking") {
		t.Error("empty-buffer reasoning tail must still render the Thinking header")
	}
}

// On reasoning.completed the live tail is torn down and the compact finalized
// blockReasoning summary is committed (existing behavior preserved).
func TestReasoningTailClearsAndCommitsOnCompleted(t *testing.T) {
	m := reasoningModel(t)
	sendEvent(m, session.EventReasoningStarted, nil)
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "step one, step two"})
	if m.streamItem == nil {
		t.Fatal("precondition: live tail should exist mid-think")
	}

	sendEvent(m, session.EventReasoningCompleted, session.MessagePayload{Content: "step one, step two"})

	if m.reasoning {
		t.Error("m.reasoning must be false after completed")
	}
	if m.streamItem != nil {
		t.Error("the live reasoning tail must be torn down on completed")
	}
	found := false
	for _, b := range m.blocks {
		if b.kind == blockReasoning && strings.Contains(b.text, "step one, step two") {
			found = true
		}
	}
	if !found {
		t.Error("reasoning.completed must commit a finalized blockReasoning")
	}
}

// An empty reasoning.completed (no text on the wire and an empty buffer) must
// still tear the live tail down — it takes the no-appendBlock path, so the
// handler's explicit syncItems is what clears it.
func TestReasoningEmptyCompletedClearsTail(t *testing.T) {
	m := reasoningModel(t)
	sendEvent(m, session.EventReasoningStarted, nil)
	if m.streamItem == nil {
		t.Fatal("precondition: started should arm the live tail")
	}
	sendEvent(m, session.EventReasoningCompleted, nil) // empty → no block committed
	if m.streamItem != nil {
		t.Error("an empty reasoning.completed must still tear down the live tail")
	}
}

// Thinking→text handoff: after a think completes and the assistant message
// streams, the single streamItem must switch to the assistant tail (not stay
// flagged as reasoning), so the message renders as markdown, not muted italic.
func TestReasoningToAssistantTailHandoff(t *testing.T) {
	m := reasoningModel(t)
	sendEvent(m, session.EventReasoningStarted, nil)
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "thinking…"})
	sendEvent(m, session.EventReasoningCompleted, session.MessagePayload{Content: "thinking…"})

	// The assistant text block now streams.
	m.handleEvent(session.Event{Type: session.EventMessageStarted, Payload: json.RawMessage(`{}`)})
	sendEvent(m, session.EventMessageDelta, session.MessagePayload{Content: "Here is the answer."})

	if m.streamItem == nil {
		t.Fatal("assistant streaming must have a live tail")
	}
	if m.streamItem.streamReasoning {
		t.Error("the tail must switch to the assistant mode for the streamed message")
	}
	// It must render as the assistant message (markdown gutter), NOT the muted
	// "∴ Thinking" reasoning tail. (The message text itself is split across styled
	// ANSI spans by the streaming markdown renderer, so assert on the header, which
	// unambiguously distinguishes the two tail modes.)
	if strings.Contains(m.streamItem.Render(m.width-1), "Thinking") {
		t.Error("assistant tail must not render the reasoning Thinking header")
	}
}

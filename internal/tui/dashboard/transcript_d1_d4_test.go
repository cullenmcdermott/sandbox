package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// d1Model builds a laid-out transcript model for the tool-completion matching
// and turn-boundary drain tests (D1) and the interrupt-mid-think test (D4).
func d1Model(t *testing.T) *TranscriptModel {
	t.Helper()
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	return m
}

func toolStart(m *TranscriptModel, tool, id string) {
	m.handleEvent(session.Event{Type: session.EventToolStarted,
		Payload: json.RawMessage(`{"tool":"` + tool + `","toolUseId":"` + id + `","input":{}}`)})
}

func toolCards(m *TranscriptModel) []*toolCard {
	var cards []*toolCard
	for _, b := range m.blocks {
		if b.kind == blockToolCard && b.tool != nil {
			cards = append(cards, b.tool)
		}
	}
	return cards
}

// D1: a tool.completed carrying a toolUseId must close the EXACT card that id
// names — even when it is not the oldest pending one — and leave the other
// pending cards untouched. This is what stops parallel tool_use from landing
// results on the wrong card.
func TestToolCompletedByToolUseIDClosesNonHeadCard(t *testing.T) {
	m := d1Model(t)
	toolStart(m, "Bash", "tu_1") // head of the pending FIFO
	toolStart(m, "Read", "tu_2") // second

	if len(m.pendingTools) != 2 {
		t.Fatalf("precondition: want 2 pending tools, got %d", len(m.pendingTools))
	}

	// Complete the SECOND tool (not the FIFO head) by its id.
	m.handleEvent(session.Event{Type: session.EventToolCompleted,
		Payload: json.RawMessage(`{"toolUseId":"tu_2","output":"read output"}`)})

	cards := toolCards(m)
	if len(cards) != 2 {
		t.Fatalf("want 2 tool cards, got %d", len(cards))
	}
	// cards[0] is Bash (tu_1) — still running; cards[1] is Read (tu_2) — closed.
	if cards[0].status != toolRunning {
		t.Errorf("head Bash card should stay running (its completion has not arrived), got %v", cards[0].status)
	}
	if cards[1].status != toolOK {
		t.Errorf("Read card (tu_2) should be closed OK, got %v", cards[1].status)
	}
	if cards[1].output != "read output" {
		t.Errorf("Read card got wrong output %q — result landed on the wrong card", cards[1].output)
	}
	if len(m.pendingTools) != 1 {
		t.Fatalf("want 1 pending tool left (Bash), got %d", len(m.pendingTools))
	}
	// The one remaining pending index must be Bash's card, not a stale slot.
	if idx := m.pendingTools[0]; idx < 0 || idx >= len(m.blocks) || m.blocks[idx].tool != cards[0] {
		t.Errorf("remaining pending slot does not point at the still-running Bash card")
	}
}

// D1 back-compat: a tool.completed with NO toolUseId (older runners, the
// PreToolUse-hook synthetic failure) must still FIFO-pop the oldest pending card.
func TestToolCompletedWithoutIDFIFOPops(t *testing.T) {
	m := d1Model(t)
	// Start two tools WITHOUT ids (older-runner shape).
	m.handleEvent(session.Event{Type: session.EventToolStarted, Payload: json.RawMessage(`{"tool":"Bash","input":{}}`)})
	m.handleEvent(session.Event{Type: session.EventToolStarted, Payload: json.RawMessage(`{"tool":"Read","input":{}}`)})
	if len(m.pendingTools) != 2 {
		t.Fatalf("precondition: want 2 pending tools, got %d", len(m.pendingTools))
	}

	// Idless completion → pop the FIFO head (Bash).
	m.handleEvent(session.Event{Type: session.EventToolCompleted, Payload: json.RawMessage(`{"output":"ok"}`)})

	cards := toolCards(m)
	if len(cards) != 2 {
		t.Fatalf("want 2 tool cards, got %d", len(cards))
	}
	if cards[0].status != toolOK {
		t.Errorf("FIFO head (Bash) should be closed, got %v", cards[0].status)
	}
	if cards[1].status != toolRunning {
		t.Errorf("second card (Read) should still be running, got %v", cards[1].status)
	}
	if len(m.pendingTools) != 1 {
		t.Fatalf("want 1 pending tool left, got %d", len(m.pendingTools))
	}
}

// D1: turn.interrupted with a still-running tool must drain it to a terminal
// state, and a SUBSEQUENT turn's tool.completed must not FIFO-mis-pop the
// stranded card (the off-by-one cascade the review describes).
func TestTurnInterruptedDrainsPendingAndNextTurnIsClean(t *testing.T) {
	m := d1Model(t)
	toolStart(m, "Bash", "tu_1") // interrupted before its result arrives

	m.handleEvent(session.Event{Type: session.EventTurnInterrupted, Payload: json.RawMessage(`{}`)})

	cards := toolCards(m)
	if len(cards) != 1 {
		t.Fatalf("want 1 tool card after interrupt, got %d", len(cards))
	}
	if cards[0].status != toolErr {
		t.Errorf("interrupted Bash card should be drained to a terminal (failed) state, got %v", cards[0].status)
	}
	if cards[0].summary != "interrupted" {
		t.Errorf("drained card summary = %q, want %q", cards[0].summary, "interrupted")
	}
	if len(m.pendingTools) != 0 {
		t.Fatalf("pendingTools must be drained on interrupt, got %d entries", len(m.pendingTools))
	}

	// Next turn: a fresh tool completes. It must close its OWN card, not touch the
	// stranded Bash card.
	m.handleEvent(session.Event{Type: session.EventTurnStarted, Payload: json.RawMessage(`{}`)})
	toolStart(m, "Read", "tu_2")
	m.handleEvent(session.Event{Type: session.EventToolCompleted,
		Payload: json.RawMessage(`{"toolUseId":"tu_2","output":"fresh"}`)})

	cards = toolCards(m)
	if len(cards) != 2 {
		t.Fatalf("want 2 tool cards, got %d", len(cards))
	}
	if cards[0].status != toolErr || cards[0].summary != "interrupted" {
		t.Errorf("the drained Bash card was mutated by the next turn: status=%v summary=%q", cards[0].status, cards[0].summary)
	}
	if cards[1].status != toolOK || cards[1].output != "fresh" {
		t.Errorf("Read card (tu_2) not closed correctly: status=%v output=%q", cards[1].status, cards[1].output)
	}
}

// D4: an interrupt mid-reasoning must tear down the live "∴ Thinking" tail and
// clear the reasoning buffer, so it doesn't render forever and doesn't leak into
// the next turn.
func TestTurnInterruptedMidReasoningClearsTail(t *testing.T) {
	m := d1Model(t)
	sendEvent(m, session.EventReasoningStarted, nil)
	sendEvent(m, session.EventReasoningDelta, session.MessagePayload{Content: "weighing options…"})
	if m.streamItem == nil || !m.reasoning {
		t.Fatal("precondition: a live reasoning tail should exist mid-think")
	}
	if m.reasoningBuf.Len() == 0 {
		t.Fatal("precondition: reasoning buffer should hold the streamed delta")
	}

	// No reasoning.completed ever arrives on abort — only turn.interrupted.
	m.handleEvent(session.Event{Type: session.EventTurnInterrupted, Payload: json.RawMessage(`{}`)})

	if m.reasoning {
		t.Error("m.reasoning must be false after an interrupt")
	}
	if m.reasoningBuf.Len() != 0 {
		t.Errorf("reasoning buffer must be cleared after an interrupt, still holds %d bytes", m.reasoningBuf.Len())
	}
	if m.streamItem != nil {
		t.Error("the live reasoning tail must be torn down after an interrupt")
	}

	// Next turn starts with an empty reasoning buffer.
	m.handleEvent(session.Event{Type: session.EventTurnStarted, Payload: json.RawMessage(`{}`)})
	if m.reasoningBuf.Len() != 0 {
		t.Errorf("next turn must start with an empty reasoning buffer, got %d bytes", m.reasoningBuf.Len())
	}
}

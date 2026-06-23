package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// RV17: session.terminating must NOT trigger an immediate reconnect (it would
// port-forward to the still-dying pod and flap [reconnected]/[connection lost]).
// The reconnect is deferred to stream-end, when the old pod is actually gone.
func TestTerminatingDefersReconnectToStreamEnd(t *testing.T) {
	reconnect := func(context.Context) (RunnerClient, error) { return &fakeRunnerClient{}, nil }
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), reconnect)
	m.width, m.height = 80, 24
	m.layout()

	cmd := m.handleEvent(session.Event{Type: session.EventSessionTerminating, Payload: json.RawMessage(`{"reason":"node drain"}`)})

	if cmd != nil {
		t.Error("session.terminating must NOT reconnect immediately (RV17 — would flap against the dying pod)")
	}
	if !m.terminating {
		t.Error("terminating flag should be set")
	}
	if !m.reconnecting {
		t.Error("reconnecting flag should be set so the later stream-end doesn't double-message")
	}
}

// RV (error-resilience HIGH, found by two agents): a failed StartTurn must roll
// back the optimistic busy state. submitText() calls beginTurn() before the POST
// is confirmed; if StartTurn errors, the turnErrMsg handler must clear turnActive
// (and the working spinner) so the next prompt actually sends instead of being
// silently queued behind a phantom active turn — the wedge that previously
// required a detach/reattach to escape.
func TestTurnErrMsgRollsBackOptimisticBusyState(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	// Simulate the optimistic start submitText performs before StartTurn resolves.
	m.beginTurn()
	if !m.turnActive {
		t.Fatal("precondition: beginTurn should set turnActive")
	}

	m.Update(turnErrMsg{err: errors.New("start session: connection refused")})

	if m.turnActive {
		t.Error("turnActive must be cleared after a failed turn start (wedge bug)")
	}
	if m.status != StatusNeedsInput {
		t.Errorf("status after failed start = %v, want StatusNeedsInput", m.status)
	}
	if m.working {
		t.Error("working spinner must stop after a failed turn start")
	}

	// With the state rolled back, a subsequent submit must SEND (not queue).
	m.input.SetValue("retry me")
	m.submit()
	if m.queuedPrompt != "" {
		t.Errorf("prompt was silently queued (%q) instead of sent — wedge not fixed", m.queuedPrompt)
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockUser || last.text != "retry me" {
		t.Errorf("expected the retried prompt to be sent as a user block, got kind=%v text=%q", last.kind, last.text)
	}
}

// RV (error-resilience HIGH): when the SSE stream fails to OPEN on attach, the
// open-error must route into the same stream-ended/reconnect path a mid-stream
// drop uses (which shows "[connection lost — reconnecting…]" and retries) rather
// than returning a nil Cmd and leaving a connected-looking but inert transcript.
func TestStartEventStreamOpenFailureRoutesToReconnect(t *testing.T) {
	fc := &fakeRunnerClient{eventsErr: errors.New("runner events: status 502")}
	m := NewTranscript(fc, transcriptSession(), nil)

	cmd := m.startEventStream()
	if cmd == nil {
		t.Fatal("startEventStream must return a Cmd on open failure (not nil), so reconnect engages")
	}
	if _, ok := cmd().(tStreamEndedMsg); !ok {
		t.Errorf("open-failure Cmd should yield tStreamEndedMsg to drive the reconnect path, got %T", cmd())
	}
}

// RV29: reconnect backoff grows and caps at 30s (no flat-3s-forever loop).
func TestReconnectBackoffGrowsAndCaps(t *testing.T) {
	want := []int{3, 3, 6, 12, 24, 30, 30, 30} // index 0 unused; attempt 1..7
	for attempt := 1; attempt <= 7; attempt++ {
		got := reconnectBackoff(attempt)
		if int(got.Seconds()) != want[attempt] {
			t.Errorf("reconnectBackoff(%d) = %s, want %ds", attempt, got, want[attempt])
		}
	}
}

// RV29: a successful reconnect resets the attempt counter so the next drop
// starts the backoff over from the bottom.
func TestReconnectedResetsAttempts(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), func(context.Context) (RunnerClient, error) {
		return &fakeRunnerClient{}, nil
	})
	m.width, m.height = 80, 24
	m.layout()
	m.reconnectAttempts = 5
	m.handleEvent(session.Event{Type: session.EventSessionStarted}) // no-op-ish to ensure model usable
	m.Update(tReconnectedMsg{client: &fakeRunnerClient{}})
	if m.reconnectAttempts != 0 {
		t.Errorf("reconnectAttempts after reconnect = %d, want 0", m.reconnectAttempts)
	}
}

// RV9: when the SSE stream drops mid-message, the partial is committed (B9) and
// the replayed message.completed (full text, higher seq) must REPLACE that block
// in place — not append a second copy, which made the user see the reply twice.
func TestMidStreamDropDoesNotDuplicateAssistantMessage(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.handleEvent(session.Event{Type: session.EventMessageStarted})
	m.handleEvent(session.Event{Type: session.EventMessageDelta, Payload: json.RawMessage(`{"content":"Hello, par"}`)})

	// Simulate a mid-message SSE drop exactly as the tStreamEndedMsg handler does:
	// commit the partial and remember the block for in-place replacement.
	if idx := m.finalizeStreaming(); idx >= 0 {
		m.droppedPartialIdx = idx
	}

	countAssistant := func() (n int, lastText string) {
		for _, b := range m.blocks {
			if b.kind == blockAssistant {
				n++
				lastText = b.text
			}
		}
		return
	}
	if n, txt := countAssistant(); n != 1 || txt != "Hello, par" {
		t.Fatalf("after drop: got %d assistant blocks (last=%q), want 1 partial", n, txt)
	}

	// Reconnect replays the full message.completed at a higher seq.
	m.handleEvent(session.Event{Type: session.EventMessageCompleted, Payload: json.RawMessage(`{"content":"Hello, partial and the rest."}`)})

	n, txt := countAssistant()
	if n != 1 {
		t.Errorf("after replayed completed: got %d assistant blocks, want 1 (no duplicate reply)", n)
	}
	if txt != "Hello, partial and the rest." {
		t.Errorf("assistant block text = %q, want the full replayed text", txt)
	}
	if m.droppedPartialIdx != -1 {
		t.Errorf("droppedPartialIdx = %d, want -1 (consumed)", m.droppedPartialIdx)
	}
}

// RV9 guard: a normal (no-drop) completed still appends a fresh block.
func TestNormalCompletedAppendsBlock(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.handleEvent(session.Event{Type: session.EventMessageStarted})
	m.handleEvent(session.Event{Type: session.EventMessageDelta, Payload: json.RawMessage(`{"content":"hi"}`)})
	m.handleEvent(session.Event{Type: session.EventMessageCompleted, Payload: json.RawMessage(`{"content":"hi there"}`)})

	n := 0
	for _, b := range m.blocks {
		if b.kind == blockAssistant {
			n++
		}
	}
	if n != 1 {
		t.Errorf("normal turn produced %d assistant blocks, want 1", n)
	}
}

// RV (event-model/error-resilience): turn.failed with a result-path payload that
// has no `message` field must not render a bare "✗" with no reason.
func TestTurnFailedWithoutMessageShowsFallback(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	// The {subtype, errors} shape carries no `message` the Go side decodes.
	m.handleEvent(session.Event{Type: session.EventTurnFailed, Payload: json.RawMessage(`{"subtype":"error_max_turns","errors":[]}`)})

	var errText string
	for _, b := range m.blocks {
		if b.kind == blockError {
			errText = b.text
		}
	}
	if errText == "" || errText == "✗ " {
		t.Fatalf("turn.failed rendered a blank error line: %q", errText)
	}
}

// RV (event-model HIGH): a tool.failed event whose reason lives only in `output`
// (the SDK is_error path emits no `error`) must still render a card summary —
// previously the failed card showed a bare "✗" with the reason silently dropped.
func TestToolFailedFallsBackToOutputForSummary(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.handleEvent(session.Event{Type: session.EventToolStarted, Payload: json.RawMessage(`{"tool":"Bash","input":{"command":"go test"}}`)})
	// Failure carries its reason in `output`, with NO `error` field (SDK path).
	m.handleEvent(session.Event{Type: session.EventToolFailed, Payload: json.RawMessage(`{"output":"command failed: boom"}`)})

	var card *toolCard
	for _, b := range m.blocks {
		if b.kind == blockToolCard {
			card = b.tool
		}
	}
	if card == nil {
		t.Fatal("expected a tool card")
	}
	if card.status != toolErr {
		t.Errorf("status = %v, want toolErr", card.status)
	}
	if card.summary != "command failed: boom" {
		t.Errorf("summary = %q, want the output text (reason must not be dropped)", card.summary)
	}
}

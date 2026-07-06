package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// M3: in the non-streaming case only reasoning.completed arrives (no started/
// delta), carrying the full thinking text in its payload. The handler must use
// that content, not the (empty) delta buffer.
func TestReasoningCompletedUsesPayloadContent(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.handleEvent(session.Event{Type: session.EventReasoningCompleted, Payload: json.RawMessage(`{"content":"let me think about this"}`)})

	found := false
	for _, b := range m.blocks {
		if b.kind == blockReasoning && strings.Contains(b.text, "let me think about this") {
			found = true
		}
	}
	if !found {
		t.Fatal("reasoning.completed content was not rendered (M3 regression)")
	}
}

// M1: session.status_changed carries a reason; an error reason must be surfaced
// to the user rather than silently dropped.
func TestSessionStatusErrorReasonSurfaced(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.handleEvent(session.Event{Type: session.EventSessionStatusChanged, Payload: json.RawMessage(`{"status":"error","reason":"model overloaded"}`)})

	if m.DashStatus != StatusFailed {
		t.Errorf("status should be Failed, got %v", m.DashStatus)
	}
	found := false
	for _, b := range m.blocks {
		if b.kind == blockError && strings.Contains(b.text, "model overloaded") {
			found = true
		}
	}
	if !found {
		t.Fatal("session error reason not surfaced (M1 regression)")
	}
}

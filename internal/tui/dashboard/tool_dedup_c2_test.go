package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// C2: with includePartialMessages the runner emits tool.started TWICE for one
// flat (non-subagent) tool — first from the streaming content_block_start (empty
// input), then from the full assistant message (complete input) — both carrying
// the same toolUseId. The subagent path dedupes this by toolUseId; the flat path
// must too, or every Bash/Read/Edit renders a duplicate card with the second
// stuck "running". The surviving card must show the full input.
func TestFlatToolStartedDedupedByToolUseID(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.handleEvent(session.Event{Type: session.EventToolStarted, Payload: json.RawMessage(`{"tool":"Bash","toolUseId":"tu_1","input":{}}`)})
	m.handleEvent(session.Event{Type: session.EventToolStarted, Payload: json.RawMessage(`{"tool":"Bash","toolUseId":"tu_1","input":{"command":"go test ./..."}}`)})

	var cards []*toolCard
	for _, b := range m.blocks {
		if b.kind == blockToolCard {
			cards = append(cards, b.tool)
		}
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 tool card for duplicate tool.started (same toolUseId), got %d", len(cards))
	}
	if len(m.pendingTools) != 1 {
		t.Fatalf("want 1 pending tool slot, got %d", len(m.pendingTools))
	}
	if cards[0].arg == "" {
		t.Errorf("card arg empty; want it filled from the full (second) tool.started input")
	}
}

package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func countFooters(m *TranscriptModel) int {
	n := 0
	for _, b := range m.blocks {
		if b.kind == blockFooter {
			n++
		}
	}
	return n
}

// A2.2: only the latest turn keeps a footer. A new interactive turn drops the
// prior turn's footer; a replayed/streamed turn.started does the same.
func TestFooterOnLatestTurnOnly(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200

	// Turn 1 finished with a footer.
	m.appendBlock(blockUser, "q1")
	m.appendBlock(blockAssistant, "a1")
	m.appendBlock(blockFooter, "◇ turn-1 footer")

	// Interactive: starting turn 2 drops turn 1's footer (kept: user q1, a1, q2).
	m.submitText("q2")
	if got := countFooters(m); got != 0 {
		t.Fatalf("interactive new turn left %d footers, want 0", got)
	}
	if m.blocks[len(m.blocks)-1].kind != blockUser {
		t.Fatalf("the new user prompt should be the trailing block, got %v", m.blocks[len(m.blocks)-1].kind)
	}

	// Replay/streamed: the prior turn completed (turnActive false) leaving a
	// footer; the next turn.started drops it.
	m.turnActive = false
	m.appendBlock(blockFooter, "◇ turn-2 footer")
	m.handleEvent(session.Event{Seq: 9, Type: session.EventTurnStarted, Payload: jraw(`{"prompt":"q3"}`)})
	if got := countFooters(m); got != 0 {
		t.Fatalf("replayed turn.started left %d footers, want 0", got)
	}
}

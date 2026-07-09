package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// D3: turn.failed now carries a real TurnFailedPayload (message + optional
// subtype/errors) instead of being decoded through the coincidental `message`
// key on ErrorPayload. The reducer must render the payload's message.
func TestTurnFailedRendersRealPayloadMessage(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.beginTurn() // a turn is in flight
	m.handleEvent(mkEvent(session.EventTurnFailed, session.TurnFailedPayload{
		Message: "usage limit reached",
		Subtype: "error_max_turns",
		Errors:  []string{"usage limit reached"},
	}))

	got, ok := lastBlockOfKind(m, blockError)
	if !ok || got != "✗ usage limit reached" {
		t.Fatalf("turn.failed did not render the payload message: got=%q ok=%v", got, ok)
	}
	if m.turnActive {
		t.Error("turn.failed should clear turnActive")
	}
}

// D3 defensive: a turn.failed with an empty message (should not happen for real
// emitters, but the wire could carry it) renders a fallback rather than a bare
// "✗".
func TestTurnFailedEmptyMessageFallsBack(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.handleEvent(mkEvent(session.EventTurnFailed, session.TurnFailedPayload{}))
	if got, ok := lastBlockOfKind(m, blockError); !ok || got != "✗ turn failed" {
		t.Fatalf("empty turn.failed message did not fall back: got=%q ok=%v", got, ok)
	}
}

// userBlockIdx / assistantBlockIdx return the index of the first block of that
// kind whose text matches, or -1.
func blockIdx(m *TranscriptModel, kind tblockKind, text string) int {
	for i, b := range m.blocks {
		if b.kind == kind && b.text == text {
			return i
		}
	}
	return -1
}

// D5: an opencode-shaped replay/attach stream carries the prompt as a
// role:"user" message (emitted by the opencode turn adapter, mirroring Claude's
// user-echo path) BEFORE the assistant answer. On a cold attach there is no
// optimistic user block, so the reducer must render the question first, then the
// answer — otherwise the transcript is a wall of answers with no questions.
func TestOpencodeReplayShowsUserPromptThenAssistant(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)

	// The exact event order the opencode adapter emits on a turn (opencode-turn.ts):
	// turn.started{prompt}, then the role:"user" echo, then the assistant reply,
	// then turn.completed{}.
	m.handleEvent(mkEvent(session.EventTurnStarted, session.TurnStartedPayload{Prompt: "what is 2+2?"}))
	m.handleEvent(mkEvent(session.EventMessageStarted, session.MessagePayload{Role: "user", Content: "what is 2+2?"}))
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{Role: "user", Content: "what is 2+2?"}))
	m.handleEvent(mkEvent(session.EventMessageStarted, session.MessagePayload{Role: "assistant"}))
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{Role: "assistant", Content: "4"}))
	m.handleEvent(mkEvent(session.EventTurnCompleted, session.TurnCompletedPayload{}))

	uIdx := blockIdx(m, blockUser, "what is 2+2?")
	aIdx := blockIdx(m, blockAssistant, "4")
	if uIdx < 0 {
		t.Fatal("replay produced no user block for the prompt (the question is missing)")
	}
	if aIdx < 0 {
		t.Fatal("replay produced no assistant block for the answer")
	}
	if uIdx >= aIdx {
		t.Fatalf("user block (idx %d) must render before the assistant block (idx %d)", uIdx, aIdx)
	}
}

// D5 dedup: a LIVE foreground flow already appended the user's prompt as an
// optimistic block at submit. The runner-driven user echo (message.completed
// role:"user") that follows must NOT double-print it. This is the invariant that
// lets the opencode adapter emit the same user-echo shape as Claude without the
// TUI special-casing either backend.
func TestLiveFlowDoesNotDoublePrintPrompt(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.appendBlock(blockUser, "deploy the app") // optimistic block from interactive submit

	m.handleEvent(mkEvent(session.EventTurnStarted, session.TurnStartedPayload{Prompt: "deploy the app"}))
	m.handleEvent(mkEvent(session.EventMessageStarted, session.MessagePayload{Role: "user", Content: "deploy the app"}))
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{Role: "user", Content: "deploy the app"}))

	n := 0
	for _, b := range m.blocks {
		if b.kind == blockUser && b.text == "deploy the app" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("live prompt double-printed: %d user blocks, want 1 (turn.started prompt + user echo must dedupe against the optimistic block)", n)
	}
}

package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// A message.completed carrying role:"user" (the runner echoing a string user
// message) must render with the user's styling, not as assistant markdown, and
// must not pollute lastAssistantText (the /goal sentinel scan reads that).
func TestMessageCompletedUserRoleRendersAsUser(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "user",
		Content: "echoed user text",
	}))

	if got, ok := lastBlockOfKind(m, blockUser); !ok || got != "echoed user text" {
		t.Fatalf("user-role message did not render as a user block: got=%q ok=%v", got, ok)
	}
	if _, ok := lastBlockOfKind(m, blockAssistant); ok {
		t.Fatal("user-role message.completed wrongly appended an assistant block")
	}
	if m.lastAssistantText == "echoed user text" {
		t.Fatal("user-role text leaked into lastAssistantText (would poison /goal sentinel scan)")
	}
}

// The optimistic user block appended at submit must not be duplicated when the
// runner echoes the same prompt back as a role:"user" message.completed.
func TestMessageCompletedUserRoleDedupsOptimisticBlock(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.appendBlock(blockUser, "my prompt") // optimistic block from submit
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "user",
		Content: "my prompt",
	}))

	n := 0
	for _, b := range m.blocks {
		if b.kind == blockUser && b.text == "my prompt" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("echoed user prompt duplicated the optimistic block: %d user blocks, want 1", n)
	}
}

// A distinct role:"user" message (not a duplicate of the previous block) is
// still appended — dedup must only collapse an exact repeat of the last block.
func TestMessageCompletedUserRoleAppendsDistinct(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.appendBlock(blockUser, "first")
	m.handleEvent(mkEvent(session.EventMessageCompleted, session.MessagePayload{
		Role:    "user",
		Content: "second",
	}))

	n := 0
	for _, b := range m.blocks {
		if b.kind == blockUser {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("distinct user message not appended: %d user blocks, want 2", n)
	}
}

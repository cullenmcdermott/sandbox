package dashboard

import (
	"strings"
	"testing"
)

// parityFixture is a fixed, representative transcript: a user turn, a markdown
// assistant reply, a resolved tool card, and an info notice. It exercises every
// single- and multi-line block kind the body assembles.
func parityFixture() []tblock {
	return []tblock{
		{kind: blockUser, text: "Hello, world!"},
		{kind: blockAssistant, text: "Here is **bold** text and a list:\n\n- one\n- two\n"},
		{kind: blockToolCard, tool: &toolCard{tool: "Read", arg: "main.go", status: toolOK, summary: "10 lines"}},
		{kind: blockInfo, text: "[reconnected]"},
	}
}

// TestTranscriptParitySnapshot is the parity oracle for the list rewrite: the
// new list-backed body must render byte-for-byte what the pre-rewrite path did.
// The pre-rewrite rebuild() assembled the transcript by joining renderBlock over
// every block with "\n" (no per-block trimming), pinned to the bottom. We
// recompute that join independently here and assert the list's Render matches.
func TestTranscriptParitySnapshot(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200 // tall enough that the whole fixture is on-screen
	m.blocks = parityFixture()
	m.layout()

	// Independent oracle: the pre-rewrite assembly (join of per-block renders).
	var parts []string
	for _, b := range m.blocks {
		parts = append(parts, m.renderBlock(b))
	}
	oracle := strings.Join(parts, "\n")

	got := m.body.Render()
	if got != oracle {
		t.Fatalf("list body does not match pre-rewrite render.\n--- got ---\n%q\n--- want ---\n%q", got, oracle)
	}
}

// TestTranscriptParityWithUnread asserts the unread divider lands in the same
// place as the pre-rewrite path: rebuild() emitted the divider immediately
// before the block at unreadIndex (when >0).
func TestTranscriptParityWithUnread(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.blocks = parityFixture()
	m.unreadIndex = 2
	m.layout()

	var parts []string
	for i, b := range m.blocks {
		if i == m.unreadIndex && m.unreadIndex > 0 {
			parts = append(parts, m.renderUnreadDivider()+"\n"+m.renderBlock(b))
			continue
		}
		parts = append(parts, m.renderBlock(b))
	}
	oracle := strings.Join(parts, "\n")

	if got := m.body.Render(); got != oracle {
		t.Fatalf("unread-divider placement diverged from pre-rewrite.\n--- got ---\n%q\n--- want ---\n%q", got, oracle)
	}
}

// TestTranscriptBodyWindowed is the behavioral counter: with a viewport smaller
// than the content, the list renders only the visible window (O(viewport)), not
// the whole transcript — and that window is the top slice of the full oracle.
func TestTranscriptBodyWindowed(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.blocks = parityFixture()
	m.layout()

	full := m.body.Render()
	fullLines := strings.Split(full, "\n")
	if len(fullLines) < 4 {
		t.Fatalf("fixture too small to exercise windowing: %d lines", len(fullLines))
	}

	// Shrink the viewport to fewer lines than the content and pin to the top.
	const window = 2
	m.body.SetSize(80, window)
	m.body.GotoTop()
	got := m.body.Render()
	gotLines := strings.Split(got, "\n")
	if len(gotLines) != window {
		t.Fatalf("windowed render returned %d lines, want exactly %d (not O(content))", len(gotLines), window)
	}
	for i := 0; i < window; i++ {
		if gotLines[i] != fullLines[i] {
			t.Fatalf("windowed line %d = %q, want top-slice %q", i, gotLines[i], fullLines[i])
		}
	}
}

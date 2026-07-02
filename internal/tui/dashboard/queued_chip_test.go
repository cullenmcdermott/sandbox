package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// A queued prompt must be visible: submit-during-turn used to make the text
// vanish with no cue that it would send later (and silently changed what esc
// does — steer vs interrupt). The hint row under the composer surfaces it.
func TestQueuedPromptChipVisible(t *testing.T) {
	sess := Session{State: session.State{ID: "s1"}}
	m := NewTranscript(&fakeRunnerClient{}, sess, nil)
	m.width, m.height = 100, 30
	m.layout()

	m.turnActive = true
	m.input.SetValue("follow-up question")
	if cmd := m.submit(); cmd != nil {
		t.Fatalf("submit during a turn should queue, not start a turn")
	}
	if m.queuedPrompt != "follow-up question" {
		t.Fatalf("queuedPrompt = %q, want the submitted text", m.queuedPrompt)
	}

	view := m.renderTranscript(m.width, m.height)
	if !strings.Contains(view, "queued:") {
		t.Errorf("queued prompt not surfaced in the view:\n%s", view)
	}
	if !strings.Contains(view, "follow-up question") {
		t.Errorf("queued prompt text missing from the chip")
	}
	if !strings.Contains(view, "esc sends now") {
		t.Errorf("steer affordance missing from the chip")
	}
}

// The inline permission box must show what is being approved (the Bash
// command, file path, …), and only advertise [↵] when a diff exists.
func TestPermissionBoxShowsArg(t *testing.T) {
	sess := Session{State: session.State{ID: "s1"}}
	m := NewTranscript(&fakeRunnerClient{}, sess, nil)
	m.width, m.height = 100, 30
	m.layout()

	m.pending = &transcriptPermission{id: "p1", tool: "Bash", arg: "rm -rf build/"}
	box := m.buildPermissionBox(m.width)
	if !strings.Contains(box, "rm -rf build/") {
		t.Errorf("permission box hides the command being approved:\n%s", box)
	}
	if strings.Contains(box, "view diff") {
		t.Errorf("permission box advertises [↵] view diff with no diff to show")
	}

	m.pending = &transcriptPermission{id: "p2", tool: "Edit", arg: "main.go", adds: 1, dels: 1, diffLines: []string{"+a", "−b"}}
	box = m.buildPermissionBox(m.width)
	if !strings.Contains(box, "view diff") {
		t.Errorf("permission box with a diff should advertise [↵] view diff:\n%s", box)
	}
}

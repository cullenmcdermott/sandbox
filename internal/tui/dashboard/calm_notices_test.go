package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// calm_notices_test.go — §2c de-bracketed system notices + §2b pinned todo
// widget. The old bracketed strings ("[interrupted]", "▤ todo list", …) scanned
// like debug logs and (for todos) appended a fresh block per update. These pin
// the calm replacements: a Coral ⎿ elbow for interruptions and a single pinned,
// mutated-in-place todo checklist.

// TestInterruptNoticeIsCoralElbow: a turn.interrupted appends the "⎿ Interrupted
// by user" elbow line (not the old "[interrupted]" debug string).
func TestInterruptNoticeIsCoralElbow(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.handleEvent(session.Event{Type: session.EventTurnInterrupted, Payload: json.RawMessage("{}")})

	if len(m.blocks) == 0 {
		t.Fatal("interrupt produced no notice block")
	}
	last := m.blocks[len(m.blocks)-1]
	got := stripANSICodes(m.renderBlock(last))
	if !strings.Contains(got, "⎿  Interrupted by user") {
		t.Errorf("interrupt notice = %q, want it to contain %q", got, "⎿  Interrupted by user")
	}
	if strings.Contains(got, "[interrupted]") {
		t.Errorf("interrupt notice still uses the old bracketed string: %q", got)
	}
	// The elbow line is Coral-styled (not the dim blockInfo tone), so it carries ANSI.
	if raw := m.renderBlock(last); raw == stripANSICodes(raw) {
		t.Errorf("interrupt elbow is unstyled; want Coral styling: %q", raw)
	}
}

// TestTodoUpdatedPinsSingleBlock: two todo.updated events mutate ONE pinned block
// rather than appending a second checklist; the block reflects the latest list and
// its version bumps between updates.
func TestTodoUpdatedPinsSingleBlock(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.handleEvent(session.Event{Type: session.EventTodoUpdated, Payload: json.RawMessage(
		`{"todos":[{"content":"first","status":"in_progress","activeForm":"doing first"}]}`)})
	if m.todoBlock == nil {
		t.Fatal("first todo.updated did not pin a todo block")
	}
	v1 := m.todoBlock.Version()

	m.handleEvent(session.Event{Type: session.EventTodoUpdated, Payload: json.RawMessage(
		`{"todos":[{"content":"first","status":"completed"},{"content":"second","status":"in_progress"}]}`)})

	// Exactly one blockTodos in the transcript.
	var todoBlocks int
	for _, b := range m.blocks {
		if b.kind == blockTodos {
			todoBlocks++
		}
	}
	if todoBlocks != 1 {
		t.Fatalf("want exactly 1 pinned todo block, got %d", todoBlocks)
	}
	// Reflects the SECOND list.
	if len(m.todoItems) != 2 || m.todoItems[0].Status != "completed" || m.todoItems[1].Content != "second" {
		t.Errorf("todo block did not update to the second list: %+v", m.todoItems)
	}
	if v2 := m.todoBlock.Version(); v2 <= v1 {
		t.Errorf("todo block version not bumped on update: v1=%d v2=%d", v1, v2)
	}
}

// TestRenderTodosGlyphStyles: completed/in_progress/pending each get their glyph,
// in_progress prefers ActiveForm, and completed is styled differently from pending.
func TestRenderTodosGlyphStyles(t *testing.T) {
	out := renderTodos([]session.TodoItem{
		{Content: "write the code", Status: "completed"},
		{Content: "run the tests", Status: "in_progress", ActiveForm: "running the tests"},
		{Content: "open a PR", Status: "pending"},
	})
	plain := stripANSICodes(out)
	for _, want := range []string{"✓ write the code", "▸ running the tests", "○ open a PR"} {
		if !strings.Contains(plain, want) {
			t.Errorf("renderTodos missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "todo list") {
		t.Errorf("renderTodos should no longer emit a '▤ todo list' header:\n%s", plain)
	}
	// Completed and pending must differ by styling, not just glyph: render the same
	// content in each state and compare the raw (styled) output.
	done := renderTodos([]session.TodoItem{{Content: "x", Status: "completed"}})
	pend := renderTodos([]session.TodoItem{{Content: "x", Status: "pending"}})
	if done == pend {
		t.Errorf("completed and pending render identically; want distinct styling")
	}
	if done == stripANSICodes(done) {
		t.Errorf("completed item is unstyled; want strikethrough dim-green: %q", done)
	}
}

// TestRenderTodosClearedMutatesToDimLine: an empty list collapses to the single
// dim "todos cleared" line.
func TestRenderTodosClearedMutatesToDimLine(t *testing.T) {
	out := stripANSICodes(renderTodos(nil))
	if out != "todos cleared" {
		t.Errorf("empty todo list = %q, want %q", out, "todos cleared")
	}
}

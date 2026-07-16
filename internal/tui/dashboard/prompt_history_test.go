package dashboard

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// These tests pin the §2d prompt-history recall state machine: ↑/↓ walk the
// prompts the user actually submitted this attach, a non-empty draft keeps the
// keys' scroll meaning, consecutive repeats collapse, and only user-origin
// submits (submit(), never the driver / initialPrompt path) land in history.

// newHistoryModel builds a focused, sized transcript for the recall tests.
func newHistoryModel(t *testing.T) *TranscriptModel {
	t.Helper()
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.input.Focus()
	return m
}

// submitViaEnter drives the full compose→submit dispatch for one prompt.
func submitViaEnter(m *TranscriptModel, text string) {
	m.input.SetValue(text)
	m.handleKey(keyMsg("enter"))
}

var (
	keyUp   = tea.KeyPressMsg{Code: tea.KeyUp}
	keyDown = tea.KeyPressMsg{Code: tea.KeyDown}
)

// TestPromptHistoryRecallAndRestore pins the core ↑/↑/clamp then ↓/↓/restore
// walk: ↑ shows newest→oldest (clamping at the oldest), ↓ walks back and past
// the newest restores the (empty) draft and exits nav.
func TestPromptHistoryRecallAndRestore(t *testing.T) {
	m := newHistoryModel(t)
	submitViaEnter(m, "first")
	submitViaEnter(m, "second")

	if got := m.promptHistory; len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("promptHistory = %v, want [first second]", got)
	}
	if m.input.Value() != "" {
		t.Fatalf("composer not cleared after submit: %q", m.input.Value())
	}

	// ↑ recalls the newest, then the oldest, then clamps.
	m.handleKey(keyUp)
	if got := m.input.Value(); got != "second" {
		t.Fatalf("first ↑ = %q, want second", got)
	}
	m.handleKey(keyUp)
	if got := m.input.Value(); got != "first" {
		t.Fatalf("second ↑ = %q, want first", got)
	}
	m.handleKey(keyUp) // clamp at oldest
	if got := m.input.Value(); got != "first" {
		t.Fatalf("third ↑ (clamp) = %q, want first", got)
	}

	// ↓ walks back toward the draft, and past the newest restores it.
	m.handleKey(keyDown)
	if got := m.input.Value(); got != "second" {
		t.Fatalf("first ↓ = %q, want second", got)
	}
	m.handleKey(keyDown) // past newest → restore the (empty) draft, exit nav
	if got := m.input.Value(); got != "" {
		t.Fatalf("↓ past newest = %q, want empty draft", got)
	}
	if m.histIdx != -1 {
		t.Fatalf("histIdx = %d after restoring draft, want -1 (nav exited)", m.histIdx)
	}
}

// TestPromptHistoryDraftPreservation pins the gate: a non-empty draft keeps ↑'s
// scroll meaning (no recall), an empty composer recalls, and editing a recalled
// entry exits nav so the typed text becomes the live draft.
func TestPromptHistoryDraftPreservation(t *testing.T) {
	m := newHistoryModel(t)
	submitViaEnter(m, "hello")

	// Non-empty draft (not from history): ↑ must NOT recall — it scrolls, so the
	// composer text is left untouched.
	m.input.SetValue("draft")
	m.handleKey(keyUp)
	if got := m.input.Value(); got != "draft" {
		t.Fatalf("↑ with a draft recalled %q, want the draft left as \"draft\"", got)
	}
	if m.histIdx != -1 {
		t.Fatalf("↑ with a draft entered nav (histIdx=%d), want -1", m.histIdx)
	}

	// Empty composer: ↑ recalls the newest entry.
	m.input.SetValue("")
	m.handleKey(keyUp)
	if got := m.input.Value(); got != "hello" {
		t.Fatalf("↑ on empty composer = %q, want hello", got)
	}
	if m.histIdx != 0 {
		t.Fatalf("histIdx = %d after recall, want 0", m.histIdx)
	}

	// Typing over the recalled entry exits nav (the edited text is now the draft).
	m.handleKey(keyMsg("x"))
	if got := m.input.Value(); got != "hellox" {
		t.Fatalf("after typing over recall = %q, want hellox", got)
	}
	if m.histIdx != -1 {
		t.Fatalf("histIdx = %d after editing recalled entry, want -1 (nav exited)", m.histIdx)
	}

	// With the edited (non-empty) draft, ↑ scrolls again — no recall.
	m.handleKey(keyUp)
	if got := m.input.Value(); got != "hellox" {
		t.Fatalf("↑ with edited draft recalled %q, want hellox untouched", got)
	}

	// Clearing the composer lets ↑ start recall from the newest entry again.
	m.input.SetValue("")
	m.handleKey(keyUp)
	if got := m.input.Value(); got != "hello" {
		t.Fatalf("↑ after clearing = %q, want hello (restart from newest)", got)
	}
}

// TestPromptHistoryDedupesConsecutive pins that submitting the same text twice in
// a row yields a single history entry.
func TestPromptHistoryDedupesConsecutive(t *testing.T) {
	m := newHistoryModel(t)
	submitViaEnter(m, "x")
	submitViaEnter(m, "x")
	if got := m.promptHistory; len(got) != 1 || got[0] != "x" {
		t.Fatalf("promptHistory = %v, want [x] (consecutive dupe collapsed)", got)
	}
}

// TestPromptHistoryExcludesDriverSubmits pins that the shared submitText sink —
// the path the auto initialPrompt and /loop-/goal driver ticks take — does NOT
// record history; only the user-origin submit() does.
func TestPromptHistoryExcludesDriverSubmits(t *testing.T) {
	m := newHistoryModel(t)
	// Driver/initial path: straight to submitText, bypassing submit().
	m.submitText("auto-driver-prompt")
	if len(m.promptHistory) != 0 {
		t.Fatalf("promptHistory = %v, want empty (driver/initial submits are excluded)", m.promptHistory)
	}
	// Contrast: a user submit is recorded.
	submitViaEnter(m, "typed")
	if got := m.promptHistory; len(got) != 1 || got[0] != "typed" {
		t.Fatalf("promptHistory = %v, want [typed]", got)
	}
}

// TestPromptHistorySubmitRecalledResetsNav pins that submitting a recalled entry
// sends it normally and resets recall state.
func TestPromptHistorySubmitRecalledResetsNav(t *testing.T) {
	m := newHistoryModel(t)
	submitViaEnter(m, "one")

	m.handleKey(keyUp) // recall "one"
	if got := m.input.Value(); got != "one" {
		t.Fatalf("recall = %q, want one", got)
	}
	m.handleKey(keyMsg("enter")) // submit the recalled entry

	if m.histIdx != -1 {
		t.Fatalf("histIdx = %d after submitting a recalled entry, want -1", m.histIdx)
	}
	if m.input.Value() != "" {
		t.Fatalf("composer = %q after submit, want empty", m.input.Value())
	}
	// Dedupe holds across the recall→submit round-trip.
	if got := m.promptHistory; len(got) != 1 || got[0] != "one" {
		t.Fatalf("promptHistory = %v, want [one]", got)
	}
}

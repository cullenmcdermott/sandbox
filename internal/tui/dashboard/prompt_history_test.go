package dashboard

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// These tests pin the §2d prompt-history recall state machine and its §2d-followup
// arrow-ownership contract: in the composer ↑/↓ ALWAYS belong to history recall and
// cursor movement and NEVER scroll the transcript. ↑ walks history when navigating
// or on the first line, otherwise moves the cursor up; ↓ mirrors it. Consecutive
// repeats collapse, and only user-origin submits (submit(), never the driver /
// initialPrompt path) land in history.

// newHistoryModel builds a focused, sized transcript for the recall tests.
func newHistoryModel(t *testing.T) *TranscriptModel {
	t.Helper()
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.input.Focus()
	return m
}

// scrollableBody fills the transcript body with enough one-line blocks to be
// scrollable, sizes it, and parks the viewport off the bottom. It returns the
// resulting (non-zero) offset so a test can assert the arrows leave it untouched.
func scrollableBody(t *testing.T, m *TranscriptModel) int {
	t.Helper()
	texts := make([]string, 60)
	for i := range texts {
		texts[i] = fmt.Sprintf("line %d", i)
	}
	m.blocks = infoCards(m, texts...)
	m.syncItems()
	m.layout()
	m.body.GotoBottom()
	m.body.ScrollBy(-3) // move up off the bottom so a stray ↑ (or ↓) would be visible
	off := m.body.Offset()
	if off <= 0 {
		t.Fatalf("setup: body offset = %d, want a scrollable, off-bottom body", off)
	}
	return off
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

// TestPromptHistoryDraftPreservation pins the NEW contract (the flip from the old
// scroll-with-a-draft behavior): a single-line draft + ↑ RECALLS with the draft
// saved, ↓ past the newest restores that draft, and editing a recalled entry exits
// nav so the typed text becomes the live draft.
func TestPromptHistoryDraftPreservation(t *testing.T) {
	m := newHistoryModel(t)
	submitViaEnter(m, "hello")

	// A single-line draft (cursor on the only line): ↑ now RECALLS, saving the
	// draft — it no longer scrolls.
	m.input.SetValue("draft")
	m.handleKey(keyUp)
	if got := m.input.Value(); got != "hello" {
		t.Fatalf("↑ with a single-line draft = %q, want recall of hello", got)
	}
	if m.histIdx != 0 {
		t.Fatalf("histIdx = %d after recall, want 0", m.histIdx)
	}

	// ↓ past the newest restores the saved draft and exits nav.
	m.handleKey(keyDown)
	if got := m.input.Value(); got != "draft" {
		t.Fatalf("↓ past newest = %q, want the saved draft \"draft\"", got)
	}
	if m.histIdx != -1 {
		t.Fatalf("histIdx = %d after restoring draft, want -1 (nav exited)", m.histIdx)
	}

	// Recall again, then type over it: editing a recalled entry exits nav and the
	// typed text becomes the live draft.
	m.handleKey(keyUp) // recall "hello" (draft "draft" saved again)
	if got := m.input.Value(); got != "hello" {
		t.Fatalf("second recall = %q, want hello", got)
	}
	m.handleKey(keyMsg("x"))
	if got := m.input.Value(); got != "hellox" {
		t.Fatalf("after typing over recall = %q, want hellox", got)
	}
	if m.histIdx != -1 {
		t.Fatalf("histIdx = %d after editing recalled entry, want -1 (nav exited)", m.histIdx)
	}
}

// TestPromptHistoryMultiLineDraftUpMovesCursorThenRecalls pins that ↑ inside a
// multi-line draft moves the cursor up a logical line (no recall) until it reaches
// the first line, and only then enters history recall.
func TestPromptHistoryMultiLineDraftUpMovesCursorThenRecalls(t *testing.T) {
	m := newHistoryModel(t)
	submitViaEnter(m, "recalled")

	m.input.SetValue("line one\nline two") // cursor at end → line 1 (the 2nd line)
	if got := m.input.Line(); got != 1 {
		t.Fatalf("setup: cursor line = %d, want 1 (end of a 2-line draft)", got)
	}

	// ↑ from line 2: the textarea moves the cursor up a line — draft untouched, no nav.
	m.handleKey(keyUp)
	if got := m.input.Value(); got != "line one\nline two" {
		t.Fatalf("↑ on line 2 changed the draft to %q, want it untouched", got)
	}
	if got := m.input.Line(); got != 0 {
		t.Fatalf("↑ on line 2 cursor line = %d, want 0", got)
	}
	if m.histIdx != -1 {
		t.Fatalf("↑ on line 2 entered nav (histIdx=%d), want -1", m.histIdx)
	}

	// ↑ from the first line: now recall the newest entry.
	m.handleKey(keyUp)
	if got := m.input.Value(); got != "recalled" {
		t.Fatalf("↑ on first line = %q, want recall of recalled", got)
	}
	if m.histIdx != 0 {
		t.Fatalf("histIdx = %d after recall, want 0", m.histIdx)
	}
}

// TestPromptHistoryMultiLineDraftDownMovesCursorNotScroll pins that ↓ inside a
// multi-line draft moves the cursor down a line, and ↓ on the last line is a
// consumed no-op — neither ever scrolls the transcript.
func TestPromptHistoryMultiLineDraftDownMovesCursorNotScroll(t *testing.T) {
	m := newHistoryModel(t)
	beforeOff := scrollableBody(t, m)

	m.input.SetValue("line one\nline two")
	m.input.CursorUp() // park the cursor on the first line
	if got := m.input.Line(); got != 0 {
		t.Fatalf("setup: cursor line = %d, want 0", got)
	}

	// ↓ from the first line: cursor moves down; no recall, no scroll.
	m.handleKey(keyDown)
	if got := m.input.Line(); got != 1 {
		t.Fatalf("↓ cursor line = %d, want 1", got)
	}
	if got := m.input.Value(); got != "line one\nline two" {
		t.Fatalf("↓ changed the draft to %q, want it untouched", got)
	}
	if m.histIdx != -1 {
		t.Fatalf("↓ entered nav (histIdx=%d), want -1", m.histIdx)
	}
	if got := m.body.Offset(); got != beforeOff {
		t.Fatalf("↓ within the draft scrolled the transcript: offset %d -> %d", beforeOff, got)
	}

	// ↓ on the last line: consumed no-op — cursor stays, nothing scrolls.
	m.handleKey(keyDown)
	if got := m.input.Line(); got != 1 {
		t.Fatalf("↓ on last line moved the cursor to %d, want 1", got)
	}
	if got := m.input.Value(); got != "line one\nline two" {
		t.Fatalf("↓ on last line changed the draft to %q", got)
	}
	if got := m.body.Offset(); got != beforeOff {
		t.Fatalf("↓ on last line scrolled the transcript: offset %d -> %d", beforeOff, got)
	}
}

// TestPromptHistoryUpNoHistoryDoesNotScroll pins that ↑ on an empty composer with
// no history is a consumed no-op that must NOT fall through to the scroll handler.
func TestPromptHistoryUpNoHistoryDoesNotScroll(t *testing.T) {
	m := newHistoryModel(t)
	before := scrollableBody(t, m)

	// Empty composer, no history: ↑ is consumed and must leave the viewport put.
	m.handleKey(keyUp)
	if got := m.body.Offset(); got != before {
		t.Fatalf("↑ with no history scrolled the transcript: offset %d -> %d", before, got)
	}
	if m.input.Value() != "" {
		t.Fatalf("↑ changed the composer to %q, want empty", m.input.Value())
	}
	if m.histIdx != -1 {
		t.Fatalf("↑ entered nav (histIdx=%d) with no history, want -1", m.histIdx)
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

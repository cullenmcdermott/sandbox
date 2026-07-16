package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// ORACLE: openModelPicker builds a Default row plus one row per model. With no
// models.available yet it uses the STATIC fallback (Fable present, id
// claude-fable-5, leading the models); with availableModels set the rows come
// from the event data.
func TestModelPickerRowsFallbackAndDynamic(t *testing.T) {
	// Fallback (no models.available): Default + the 4 static models.
	m := &TranscriptModel{}
	m.openModelPicker()
	rows := m.modelPicker.rows
	wantNames := []string{"Default", "Fable 5", "Opus 4.8", "Sonnet 5", "Haiku 4.5"}
	if len(rows) != len(wantNames) {
		t.Fatalf("fallback rows = %d, want %d (%v)", len(rows), len(wantNames), wantNames)
	}
	for i, w := range wantNames {
		if rows[i].name != w {
			t.Errorf("fallback row[%d].name = %q, want %q", i, rows[i].name, w)
		}
	}
	if rows[0].id != "" {
		t.Errorf("Default row id = %q, want empty", rows[0].id)
	}
	if rows[1].id != "claude-fable-5" {
		t.Errorf("Fable row id = %q, want claude-fable-5 (must be reachable pre-models.available)", rows[1].id)
	}

	// Dynamic (models.available landed): rows come from the event data.
	m2 := &TranscriptModel{}
	m2.availableModels = []session.ModelInfo{
		{Value: "claude-opus-4-8", DisplayName: "Opus 4.8", Description: "most capable"},
		{Value: "claude-haiku-4-5", DisplayName: "Haiku 4.5"},
	}
	m2.openModelPicker()
	rows2 := m2.modelPicker.rows
	wantDyn := []struct{ name, id string }{
		{"Default", ""},
		{"Opus 4.8", "claude-opus-4-8"},
		{"Haiku 4.5", "claude-haiku-4-5"},
	}
	if len(rows2) != len(wantDyn) {
		t.Fatalf("dynamic rows = %d, want %d", len(rows2), len(wantDyn))
	}
	for i, w := range wantDyn {
		if rows2[i].name != w.name || rows2[i].id != w.id {
			t.Errorf("dynamic row[%d] = {%q %q}, want {%q %q}", i, rows2[i].name, rows2[i].id, w.name, w.id)
		}
	}
}

// ORACLE: the pure key grammar — nav clamps, digits jump+select, enter selects.
func TestModelPickerKeyGrammar(t *testing.T) {
	const n = 5
	cases := []struct {
		key        string
		sel        int
		wantSel    int
		wantChoose int
		wantHandle bool
	}{
		{"down", 0, 1, -1, true},
		{"down", 4, 4, -1, true}, // clamp at bottom
		{"up", 2, 1, -1, true},
		{"up", 0, 0, -1, true}, // clamp at top
		{"enter", 3, 3, 3, true},
		{"1", 4, 0, 0, true},   // digit jumps + selects row 0
		{"5", 0, 4, 4, true},   // digit jumps + selects row 4
		{"9", 2, 2, -1, true},  // out-of-range digit: swallowed, no-op
		{"x", 2, 2, -1, false}, // non-grammar key: unhandled
	}
	for _, c := range cases {
		gotSel, gotChoose, gotHandled := modelPickerKey(c.key, c.sel, n)
		if gotSel != c.wantSel || gotChoose != c.wantChoose || gotHandled != c.wantHandle {
			t.Errorf("modelPickerKey(%q, sel=%d) = (%d, %d, %v), want (%d, %d, %v)",
				c.key, c.sel, gotSel, gotChoose, gotHandled, c.wantSel, c.wantChoose, c.wantHandle)
		}
	}
}

// COUNTER: esc closes the picker without committing a selection.
func TestModelPickerEscCloses(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.openModelPicker()
	m.modelPicker.sel = 2 // would pick Opus if it committed
	m.handleKey(keyMsg("esc"))
	if m.modelPicker.open {
		t.Error("esc should close the picker")
	}
	if m.modelOverride != "" {
		t.Errorf("esc committed a selection: modelOverride = %q, want empty", m.modelOverride)
	}
}

// ORACLE: selecting Fable records the Fable override, appends a confirm block,
// and closes the picker; selecting Default clears the override and restores the
// display model to the account default.
func TestModelPickerSelectFableAndDefault(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.defaultModel = "claude-haiku-4-5"
	m.Model = "claude-haiku-4-5"

	// Fable is index 1 in the static fallback rows.
	m.openModelPicker()
	m.modelPicker.sel = 1
	m.handleKey(keyMsg("enter"))
	if m.modelOverride != "claude-fable-5" {
		t.Fatalf("modelOverride after Fable = %q, want claude-fable-5", m.modelOverride)
	}
	if m.modelPicker.open {
		t.Error("picker should be closed after selecting")
	}
	if !hasInfoBlock(m, "model → Fable 5") {
		t.Errorf("no confirm block for the Fable selection: %+v", m.blocks)
	}

	// Default row (index 0) clears the override and restores the display model.
	m.openModelPicker()
	m.modelPicker.sel = 0
	m.handleKey(keyMsg("enter"))
	if m.modelOverride != "" {
		t.Errorf("modelOverride after Default = %q, want empty", m.modelOverride)
	}
	if m.Model != "claude-haiku-4-5" {
		t.Errorf("Model after Default = %q, want claude-haiku-4-5 (account default)", m.Model)
	}
}

// ORACLE: the current-choice mark (and initial selection) follows m.modelOverride.
func TestModelPickerCurrentMarking(t *testing.T) {
	// No override ⇒ the Default row is current and pre-selected.
	m := &TranscriptModel{}
	m.openModelPicker()
	if !m.modelPicker.rows[0].current {
		t.Error("with no override, the Default row should be marked current")
	}
	if m.modelPicker.sel != 0 {
		t.Errorf("initial sel = %d, want 0 (Default)", m.modelPicker.sel)
	}

	// An override ⇒ its matching row is current and pre-selected.
	m2 := &TranscriptModel{}
	m2.modelOverride = "claude-opus-4-8" // index 2 in the fallback rows
	m2.openModelPicker()
	if !m2.modelPicker.rows[2].current {
		t.Error("with the Opus override, the Opus row should be marked current")
	}
	if m2.modelPicker.rows[0].current {
		t.Error("the Default row should not be current when an override is set")
	}
	if m2.modelPicker.sel != 2 {
		t.Errorf("initial sel = %d, want 2 (the current row)", m2.modelPicker.sel)
	}
}

// COUNTER: escapeConsumes is true while the picker is open, so a bare esc closes
// the picker instead of detaching.
func TestModelPickerEscapeConsumes(t *testing.T) {
	m := &TranscriptModel{}
	if m.escapeConsumes() {
		t.Fatal("precondition: escapeConsumes should be false with the picker closed")
	}
	m.openModelPicker()
	if !m.escapeConsumes() {
		t.Error("escapeConsumes should be true while the picker is open")
	}
}

// COUNTER: the palette's Model group is exactly one /model entry (the per-model
// commands moved into the picker).
func TestModelPaletteSingleEntry(t *testing.T) {
	cmds := modelGroupCmds(&TranscriptModel{})
	if len(cmds) != 1 || cmds[0].name != "/model" {
		t.Fatalf("Model group = %v, want a single /model entry", cmds)
	}
	// nil m (static help reference) yields the same single entry.
	if got := modelGroupCmds(nil); len(got) != 1 || got[0].name != "/model" {
		t.Errorf("modelGroupCmds(nil) = %v, want [/model]", got)
	}
}

// hasInfoBlock reports whether an info block containing sub was appended.
func hasInfoBlock(m *TranscriptModel, sub string) bool {
	for _, b := range m.blocks {
		if b.kind == blockInfo && strings.Contains(b.text, sub) {
			return true
		}
	}
	return false
}

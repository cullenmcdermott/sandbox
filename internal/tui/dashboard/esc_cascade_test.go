package dashboard

import (
	"reflect"
	"testing"
)

// The esc priority order is encoded once, in escCascade; escapeConsumes and the
// esc key handler both read it. These tests pin the order and the derivation so
// the two readers can never drift.

// TestEscCascadeOrder pins the exact ordered step names. Reordering the cascade
// (or renaming a step) is a behavior change and must fail here.
func TestEscCascadeOrder(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	var names []string
	for _, step := range m.escCascade() {
		names = append(names, step.name)
	}
	want := []string{"search", "palette", "steer", "interrupt", "driver", "vim-insert"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("escCascade order = %v, want %v", names, want)
	}
}

// TestEscCascadeFirstApplicableOnly verifies only the first applicable step
// runs: with search open AND a turn active, esc closes/delegates to search and
// does NOT interrupt the turn.
func TestEscCascadeFirstApplicableOnly(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.openSearch()
	m.turnActive = true

	_, cmd := m.handleKey(keyMsg("esc"))
	execCmd(cmd)

	if m.search.open {
		t.Error("esc did not close search (first cascade step should have handled it)")
	}
	if !m.turnActive {
		t.Error("esc interrupted the turn instead of stopping at the search step")
	}
	if fc.interrupts != 0 {
		t.Fatalf("esc produced %d interrupts, want 0 (search consumed esc first)", fc.interrupts)
	}
}

// TestEscapeConsumesDerivesFromCascade checks that for each single-state case
// escapeConsumes() equals (showHelp || any cascade step applies) — the two are
// the same encoding, not two hand-maintained lists.
func TestEscapeConsumesDerivesFromCascade(t *testing.T) {
	anyStepApplies := func(m *TranscriptModel) bool {
		for _, step := range m.escCascade() {
			if step.applies() {
				return true
			}
		}
		return false
	}

	cases := []struct {
		name  string
		setup func(m *TranscriptModel)
		want  bool
	}{
		{"all-off", func(*TranscriptModel) {}, false},
		{"search-open", func(m *TranscriptModel) { m.search.open = true }, true},
		{"palette-open", func(m *TranscriptModel) { m.input.SetValue("/mod") }, true},
		{"queued-prompt", func(m *TranscriptModel) { m.queuedPrompt = "x" }, true},
		{"turn-active", func(m *TranscriptModel) { m.turnActive = true }, true},
		{"driver-active", func(m *TranscriptModel) { m.runnerDriver.active = true }, true},
		{"vim-insert", func(m *TranscriptModel) { m.vimEnabled = true; m.imode = modeInsert }, true},
		{"help", func(m *TranscriptModel) { m.showHelp = true }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
			m.width, m.height = 80, 24
			tc.setup(m)
			got := m.escapeConsumes()
			derived := m.showHelp || anyStepApplies(m)
			if got != derived {
				t.Fatalf("escapeConsumes()=%v but (showHelp || anyStepApplies)=%v", got, derived)
			}
			if got != tc.want {
				t.Fatalf("escapeConsumes()=%v, want %v", got, tc.want)
			}
		})
	}
}

// TestEscSteerAfterTurnEnded is the newly-consumed case: with a queued prompt
// and no active turn, esc must be consumed transcript-side (steer) — it flushes
// the queued prompt as a fresh turn rather than falling through to the App's
// detach.
func TestEscSteerAfterTurnEnded(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.queuedPrompt = "x"
	m.turnActive = false

	if !m.escapeConsumes() {
		t.Fatal("queued prompt + no active turn should consume esc (steer, not detach)")
	}

	_, cmd := m.handleKey(keyMsg("esc"))
	if cmd == nil {
		t.Fatal("esc with a queued prompt (turn ended) produced no command")
	}
	// queueSteer with no active turn submits the queued prompt as a new turn.
	if m.queuedPrompt != "" {
		t.Fatalf("steer left queuedPrompt = %q, want flushed", m.queuedPrompt)
	}
	execCmd(cmd) // drives startTurnCmd → StartTurn
	if len(fc.startedPrompts) != 1 || fc.startedPrompts[0] != "x" {
		t.Fatalf("steer did not POST the queued prompt: startedPrompts = %v", fc.startedPrompts)
	}
}

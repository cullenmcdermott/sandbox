package dashboard

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// These tests pin the §2a input-context port: the sub-context resolver's
// precedence, the fact that globals run ahead of the sub-context switch, and the
// global table's ordered binding set. Byte-for-byte behavior is otherwise
// covered by the existing permission/permprompt/modes/esc-cascade suites.

// TestTranscriptSubContextResolution pins the resolver order: search preempts a
// pending permission, permission preempts the palette, and so on down to compose.
func TestTranscriptSubContextResolution(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *TranscriptModel)
		want  tctx
	}{
		{"search-beats-pending", func(m *TranscriptModel) {
			m.search.open = true
			m.pending = &transcriptPermission{tool: "Bash", since: nowFunc()}
		}, tctxSearch},
		{"pending-alone", func(m *TranscriptModel) {
			m.pending = &transcriptPermission{tool: "Bash", since: nowFunc()}
		}, tctxPermission},
		{"palette", func(m *TranscriptModel) {
			m.input.SetValue("/x")
		}, tctxPalette},
		{"vim-normal", func(m *TranscriptModel) {
			m.vimEnabled = true
			m.imode = modeNormal
		}, tctxNormal},
		{"default-compose", func(*TranscriptModel) {}, tctxCompose},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
			m.width, m.height = 80, 24
			tc.setup(m)
			if got := m.activeSubContext(); got != tc.want {
				t.Fatalf("activeSubContext() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestGlobalsRunBeforeSubContext pins that the global table fires ahead of the
// sub-context: with a (non-plan) permission pending, shift+tab still cycles the
// permission mode instead of being swallowed by the permission handler.
func TestGlobalsRunBeforeSubContext(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.pending = &transcriptPermission{tool: "Bash", since: nowFunc()}
	before := m.mode

	m.handleKey(keyMsg("shift+tab"))

	if m.mode == before {
		t.Fatalf("shift+tab did not cycle permission mode while pending (mode stayed %v)", before)
	}
	if m.pending == nil {
		t.Fatal("shift+tab resolved the pending permission instead of just cycling mode")
	}
}

// TestSpaceFallsThroughToComposerWithoutSubagents pins the space try-entry: with
// an empty prompt and no subagent cards, the global reports unhandled and the
// space is typed into the composer.
func TestSpaceFallsThroughToComposerWithoutSubagents(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.input.Focus() // vim-off default is a focused INSERT prompt (focus Cmd runs at init)
	// No subagent cards present, prompt empty.
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	m.handleKey(space)
	if got := m.input.Value(); got != " " {
		t.Fatalf("space with no subagent cards left input = %q, want %q", got, " ")
	}
}

// TestGlobalTablePrecedence pins the global table's ordered primary keys — the
// first-match precedence is data, so a reorder is a behavior change and must
// fail here.
func TestGlobalTablePrecedence(t *testing.T) {
	var got []string
	for _, e := range transcriptGlobalTable() {
		got = append(got, e.binding.Keys()[0])
	}
	want := []string{"?", "esc", "ctrl+]", "space", "shift+tab", "ctrl+f"}
	if len(got) != len(want) {
		t.Fatalf("global table keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("global table order = %v, want %v", got, want)
		}
	}
}

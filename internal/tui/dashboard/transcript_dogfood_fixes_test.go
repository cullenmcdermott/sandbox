package dashboard

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// --------------------------------------------------------------------------
// Bug 1 — user prompt lines wrap into the frame instead of clipping.
// --------------------------------------------------------------------------

// TestUserBlockWraps asserts a user prompt wider than the frame renders as
// multiple lines, all within the frame width, with the first line quoted "> ".
func TestUserBlockWraps(t *testing.T) {
	m := toolCardTM(t)                  // width 80
	long := strings.Repeat("word ", 60) // ~300 cols, far wider than 80
	m.appendBlock(blockUser, strings.TrimSpace(long))
	b := m.blocks[len(m.blocks)-1]

	out := m.renderBlock(b)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("long user prompt did not wrap: got %d line(s):\n%s", len(lines), out)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("wrapped user line %d width %d exceeds frame %d: %q", i, w, m.width, stripANSICodes(l))
		}
	}
	if !strings.HasPrefix(stripANSICodes(lines[0]), "> ") {
		t.Errorf("first user line not quoted with '> ': %q", stripANSICodes(lines[0]))
	}
}

// --------------------------------------------------------------------------
// Bug 2 — the composer grows on soft wrap, not only on hard newlines.
// --------------------------------------------------------------------------

// TestInputRowsSoftWrap covers the wrap-aware inputRows(): a value with no
// newlines that soft-wraps still grows the box; a hard newline adds to it; the
// maxInputRows cap holds.
func TestInputRowsSoftWrap(t *testing.T) {
	m := toolCardTM(t)
	w := m.input.Width()
	if w <= 0 {
		t.Fatalf("textarea content width not set: %d", w)
	}

	// A single logical line (no '\n') that spans just over two content widths →
	// three visual rows. LineCount() would report 1; inputRows() must report 3.
	m.input.SetValue(strings.Repeat("x", 2*w+1))
	if got := m.inputRows(); got != 3 {
		t.Errorf("soft-wrap inputRows() = %d, want 3 (content width %d)", got, w)
	}
	if lc := m.input.LineCount(); lc != 1 {
		t.Fatalf("precondition: textarea LineCount() = %d, want 1 (proves the soft-wrap bug)", lc)
	}

	// Add a hard newline: the extra logical line adds one more visual row.
	m.input.SetValue(strings.Repeat("x", 2*w+1) + "\ny")
	if got := m.inputRows(); got != 4 {
		t.Errorf("soft-wrap + hard-newline inputRows() = %d, want 4", got)
	}

	// Far past the cap collapses to maxInputRows.
	m.input.SetValue(strings.Repeat("x", w*maxInputRows*3))
	if got := m.inputRows(); got != maxInputRows {
		t.Errorf("over-cap inputRows() = %d, want %d", got, maxInputRows)
	}
}

// --------------------------------------------------------------------------
// Bug 3 — ctrl+o is expand-only; $EDITOR composition moved to ctrl+e.
// --------------------------------------------------------------------------

// TestCtrlOExpandsWithDraft: ctrl+o expands the latest tool card even with a
// draft in the composer, leaves the draft untouched, and produces no editor cmd.
func TestCtrlOExpandsWithDraft(t *testing.T) {
	m := toolCardTM(t)
	m.startToolCard("Bash", "make")
	m.finishToolCard(toolOK, "3 lines", "Bash", "one\ntwo\nthree", "")
	card := m.blocks[len(m.blocks)-1]
	m.input.SetValue("a draft prompt")

	_, cmd := m.handleKey(keyMsg("ctrl+o"))
	if !card.tool.expanded {
		t.Error("ctrl+o did not expand the tool card while a draft was present")
	}
	if m.input.Value() != "a draft prompt" {
		t.Errorf("ctrl+o mutated the draft: %q", m.input.Value())
	}
	if cmd != nil {
		t.Error("ctrl+o produced a cmd (expected no editor launch)")
	}
}

// TestCtrlONoExpandableIsNoop: on an empty composer with nothing expandable,
// ctrl+o is a swallowed no-op — no editor, no draft change.
func TestCtrlONoExpandableIsNoop(t *testing.T) {
	m := toolCardTM(t)
	m.startToolCard("Bash", "true")
	m.finishToolCard(toolOK, "done", "Bash", "", "") // content-less: nothing to expand
	card := m.blocks[len(m.blocks)-1]

	_, cmd := m.handleKey(keyMsg("ctrl+o"))
	if card.tool.expanded {
		t.Error("ctrl+o expanded a content-less card")
	}
	if m.input.Value() != "" {
		t.Errorf("ctrl+o mutated the composer: %q", m.input.Value())
	}
	if cmd != nil {
		t.Error("ctrl+o on nothing-expandable produced a cmd (expected a no-op)")
	}
}

// TestCtrlEComposesInEditor: ctrl+e produces the $EDITOR cmd regardless of
// whether a draft is present, and never expands a card.
func TestCtrlEComposesInEditor(t *testing.T) {
	for _, draft := range []string{"", "some draft"} {
		m := toolCardTM(t)
		m.startToolCard("Bash", "make")
		m.finishToolCard(toolOK, "3 lines", "Bash", "one\ntwo\nthree", "")
		card := m.blocks[len(m.blocks)-1]
		m.input.SetValue(draft)

		_, cmd := m.handleKey(keyMsg("ctrl+e"))
		if cmd == nil {
			t.Errorf("ctrl+e (draft=%q) produced no cmd; expected the $EDITOR launch", draft)
		}
		if card.tool.expanded {
			t.Errorf("ctrl+e (draft=%q) wrongly expanded a tool card", draft)
		}
	}
}

// --------------------------------------------------------------------------
// Bug 4 — bracketed paste reaches the composer (and is dropped elsewhere).
// --------------------------------------------------------------------------

// TestPasteInComposeGrowsBox: a multi-line paste in the compose context lands in
// the input and grows the box (inputRows).
func TestPasteInComposeGrowsBox(t *testing.T) {
	m := toolCardTM(t)
	m.input.Focus() // the live transcript focuses the composer in Init; the textarea
	//               drops input (incl. paste) while blurred.
	before := m.inputRows()

	pasted := "first line\nsecond line\nthird line"
	m.Update(tea.PasteMsg{Content: pasted})

	if !strings.Contains(m.input.Value(), "second line") {
		t.Errorf("pasted text did not reach the composer: %q", m.input.Value())
	}
	if after := m.inputRows(); after <= before {
		t.Errorf("multi-line paste did not grow the box: inputRows %d → %d", before, after)
	}
}

// TestPasteDroppedWhilePermissionPending: a paste while a permission is pending
// must not touch the composer (that context owns no text field).
func TestPasteDroppedWhilePermissionPending(t *testing.T) {
	m := toolCardTM(t)
	m.pending = &transcriptPermission{id: "p1", tool: "Edit", since: nowFunc()}

	m.Update(tea.PasteMsg{Content: "should not land"})

	if m.input.Value() != "" {
		t.Errorf("paste leaked into the composer while a permission was pending: %q", m.input.Value())
	}
}

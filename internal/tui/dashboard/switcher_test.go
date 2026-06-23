package dashboard

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// REGRESSION (D2): the quick-switcher must allow typing 'j' and 'k' into the
// fuzzy query. Prior code captured them as up/down navigation keys.
func TestSwitcherQueryCanTypeJ(t *testing.T) {
	m := &Model{}
	m.openSwitcher()

	for _, r := range "jquery" {
		msg := tea.KeyPressMsg{Code: r, Text: string(r)}
		m.switcherKey(msg)
	}

	if got, want := m.switcher.query, "jquery"; got != want {
		t.Fatalf("query = %q, want %q (j was captured as down key)", got, want)
	}
}

func TestSwitcherQueryCanTypeK(t *testing.T) {
	m := &Model{}
	m.openSwitcher()

	for _, r := range "kafka" {
		msg := tea.KeyPressMsg{Code: r, Text: string(r)}
		m.switcherKey(msg)
	}

	if got, want := m.switcher.query, "kafka"; got != want {
		t.Fatalf("query = %q, want %q (k was captured as up key)", got, want)
	}
}

// up/down still navigate selection (bounded by match count).
func TestSwitcherUpDownNavigates(t *testing.T) {
	m := &Model{}
	m.openSwitcher()
	m.switcher.sel = 1

	m.switcherKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.switcher.sel != 0 {
		t.Fatalf("up should decrement sel, got %d", m.switcher.sel)
	}

	// With no sessions, down is a no-op (bounded).
	m.switcherKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.switcher.sel != 0 {
		t.Fatalf("down should be no-op with empty list, got %d", m.switcher.sel)
	}
}
func TestSwitcherEscCloses(t *testing.T) {
	m := &Model{}
	m.openSwitcher()

	m.switcherKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.switcher.open {
		t.Fatal("esc should close switcher")
	}
}

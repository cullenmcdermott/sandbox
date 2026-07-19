package picker

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/theme"
)

func items() []Item {
	return []Item{
		{ID: "", Name: "Default", Desc: "account default"},
		{ID: "claude-fable-5", Name: "Fable 5", Desc: "most capable"},
		{ID: "claude-opus-4-8", Name: "Opus 4.8", Current: true},
		{ID: "claude-haiku-4-5", Name: "Haiku 4.5", Desc: "fastest"},
	}
}

func key(code rune) tea.KeyPressMsg  { return tea.KeyPressMsg{Code: code} }
func runeKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func TestNewPreselectsCurrent(t *testing.T) {
	m := New("Select model", items())
	if m.Selected() != 2 {
		t.Errorf("current item not pre-selected: sel=%d, want 2", m.Selected())
	}
	if m.SelectedItem().ID != "claude-opus-4-8" {
		t.Errorf("SelectedItem wrong: %+v", m.SelectedItem())
	}
}

func TestNavigationClamps(t *testing.T) {
	m := New("t", items())
	m.sel = 0
	m, _ = m.Update(key(tea.KeyUp))
	if m.Selected() != 0 {
		t.Errorf("up past top moved: %d", m.Selected())
	}
	m, _ = m.Update(key(tea.KeyDown))
	m, _ = m.Update(runeKey('j')) // j also moves down
	if m.Selected() != 2 {
		t.Errorf("down nav wrong: %d, want 2", m.Selected())
	}
	m, _ = m.Update(runeKey('k')) // k moves up
	if m.Selected() != 1 {
		t.Errorf("k nav wrong: %d, want 1", m.Selected())
	}
	// Clamp at the bottom.
	for i := 0; i < 10; i++ {
		m, _ = m.Update(key(tea.KeyDown))
	}
	if m.Selected() != len(items())-1 {
		t.Errorf("down past bottom did not clamp: %d", m.Selected())
	}
}

func TestEnterChoosesSelected(t *testing.T) {
	var chosen Item
	m := New("t", items(), WithChoose(func(it Item) { chosen = it }))
	m, _ = m.Update(key(tea.KeyUp)) // 2 -> 1 (Fable)
	m.Update(key(tea.KeyEnter))
	if chosen.ID != "claude-fable-5" {
		t.Errorf("enter chose %+v, want Fable", chosen)
	}
}

func TestDigitJumpsAndChooses(t *testing.T) {
	var chosen Item
	n := 0
	m := New("t", items(), WithChoose(func(it Item) { chosen = it; n++ }))
	m, _ = m.Update(runeKey('4')) // row 4 = Haiku
	if chosen.ID != "claude-haiku-4-5" {
		t.Errorf("digit chose %+v, want Haiku", chosen)
	}
	if m.Selected() != 3 {
		t.Errorf("digit did not move the cursor: %d", m.Selected())
	}
	// An out-of-range digit is swallowed (no choose).
	m.Update(runeKey('9'))
	if n != 1 {
		t.Errorf("out-of-range digit chose a row (n=%d)", n)
	}
}

func TestEscCancels(t *testing.T) {
	cancelled := false
	chose := false
	m := New("t", items(), WithCancel(func() { cancelled = true }), WithChoose(func(Item) { chose = true }))
	m.Update(key(tea.KeyEscape))
	if !cancelled {
		t.Error("esc did not cancel")
	}
	if chose {
		t.Error("esc chose a row")
	}
}

func TestViewRendersRows(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	m := New("Select model", items())
	out := ansi.Strip(m.View(80))
	for _, want := range []string{"Select model", "1. Default", "2. Fable 5", "Opus 4.8", "✓", "› ", "choose"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q in:\n%s", want, out)
		}
	}
}

func TestViewWidthSafe(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	long := []Item{
		{Name: strings.Repeat("very-long-model-name-", 8), Desc: strings.Repeat("detail ", 20), Current: true},
		{Name: "short"},
	}
	m := New("Pick", long)
	for _, w := range []int{40, 60, 80, 120} {
		out := m.View(w)
		for i, l := range strings.Split(out, "\n") {
			if lw := lipgloss.Width(l); lw > w {
				t.Errorf("width %d: line %d overflows (%d cols): %q", w, i, lw, l)
			}
		}
	}
}

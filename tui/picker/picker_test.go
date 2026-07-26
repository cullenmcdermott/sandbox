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

func TestFilterIsOptIn(t *testing.T) {
	// Without WithFilter the default grammar must survive verbatim: letters
	// navigate and digits jump, and nothing accumulates a query.
	m := New("t", items())
	m, _ = m.Update(runeKey('o')) // 'o' is not in the grammar -> swallowed
	if m.Query() != "" {
		t.Errorf("non-filter picker accumulated a query: %q", m.Query())
	}
	if got := len(m.Filtered()); got != len(items()) {
		t.Errorf("non-filter picker filtered rows: %d, want %d", got, len(items()))
	}
	// SetQuery is inert without the option, so a host cannot half-enable it.
	m.SetQuery("opus")
	if m.Query() != "" {
		t.Errorf("SetQuery took effect without WithFilter: %q", m.Query())
	}
}

func TestFilterNarrowsRows(t *testing.T) {
	m := New("t", items(), WithFilter())
	for _, r := range "haiku" {
		m, _ = m.Update(runeKey(r))
	}
	if m.Query() != "haiku" {
		t.Fatalf("query = %q, want %q", m.Query(), "haiku")
	}
	vis := m.Filtered()
	if len(vis) != 1 || vis[0].ID != "claude-haiku-4-5" {
		t.Fatalf("filtered = %+v, want just Haiku", vis)
	}
	// Selection follows the visible set, not the original indexes.
	if m.SelectedItem().ID != "claude-haiku-4-5" {
		t.Errorf("SelectedItem = %+v, want Haiku", m.SelectedItem())
	}
	// Digits are query text in filter mode, not a jump: "4" narrows further
	// rather than choosing row 4.
	m, _ = m.Update(runeKey('-'))
	m, _ = m.Update(runeKey('4'))
	if m.Query() != "haiku-4" {
		t.Errorf("digit did not type into the query: %q", m.Query())
	}
}

func TestFilterMatchesIDAndName(t *testing.T) {
	m := New("t", items(), WithFilter())
	m.SetQuery("FABLE") // case-insensitive, matches Name "Fable 5"
	if vis := m.Filtered(); len(vis) != 1 || vis[0].ID != "claude-fable-5" {
		t.Errorf("name match failed: %+v", vis)
	}
	m.SetQuery("claude-opus") // matches ID only
	if vis := m.Filtered(); len(vis) != 1 || vis[0].ID != "claude-opus-4-8" {
		t.Errorf("id match failed: %+v", vis)
	}
	m.SetQuery("most capable") // Desc is deliberately NOT matched
	if vis := m.Filtered(); len(vis) != 0 {
		t.Errorf("desc should not match, got %+v", vis)
	}
}

func TestFilterEnterChoosesFromFilteredSet(t *testing.T) {
	var chosen Item
	m := New("t", items(), WithFilter(), WithChoose(func(it Item) { chosen = it }))
	m.SetQuery("claude") // 3 rows: fable, opus, haiku
	if got := len(m.Filtered()); got != 3 {
		t.Fatalf("filtered = %d rows, want 3", got)
	}
	// A new query resets the cursor to the top match.
	if m.Selected() != 0 {
		t.Fatalf("cursor did not reset to the top match: %d", m.Selected())
	}
	m, _ = m.Update(key(tea.KeyDown)) // -> opus (index 1 of the FILTERED set)
	m.Update(key(tea.KeyEnter))
	if chosen.ID != "claude-opus-4-8" {
		t.Errorf("enter chose %+v, want Opus", chosen)
	}
}

func TestFilterNavKeysTypeInsteadOfMoving(t *testing.T) {
	m := New("t", items(), WithFilter())
	m, _ = m.Update(runeKey('j'))
	m, _ = m.Update(runeKey('k'))
	if m.Query() != "jk" {
		t.Errorf("j/k did not type into the query: %q", m.Query())
	}
	// ctrl+n/ctrl+p replace them as navigation.
	m.SetQuery("")
	m, _ = m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if m.Selected() != 1 {
		t.Errorf("ctrl+n did not move down: %d", m.Selected())
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if m.Selected() != 0 {
		t.Errorf("ctrl+p did not move up: %d", m.Selected())
	}
}

func TestFilterBackspaceAndEscClearBeforeCancel(t *testing.T) {
	cancelled := 0
	m := New("t", items(), WithFilter(), WithCancel(func() { cancelled++ }))
	m.SetQuery("hai")
	m, _ = m.Update(key(tea.KeyBackspace))
	if m.Query() != "ha" {
		t.Errorf("backspace = %q, want %q", m.Query(), "ha")
	}
	// esc with a query clears it rather than closing the picker.
	m, _ = m.Update(key(tea.KeyEscape))
	if m.Query() != "" || cancelled != 0 {
		t.Errorf("esc with a query: query=%q cancelled=%d, want cleared and no cancel", m.Query(), cancelled)
	}
	// esc again, now empty, cancels.
	m.Update(key(tea.KeyEscape))
	if cancelled != 1 {
		t.Errorf("esc on an empty query did not cancel (n=%d)", cancelled)
	}
}

func TestFilterNoMatchesIsInert(t *testing.T) {
	chose := false
	m := New("t", items(), WithFilter(), WithChoose(func(Item) { chose = true }))
	m.SetQuery("zzzznope")
	if got := len(m.Filtered()); got != 0 {
		t.Fatalf("filtered = %d rows, want 0", got)
	}
	if m.SelectedItem() != (Item{}) {
		t.Errorf("SelectedItem on an empty set = %+v, want zero", m.SelectedItem())
	}
	m.Update(key(tea.KeyEnter))
	if chose {
		t.Error("enter chose a row with no matches")
	}
	if out := ansi.Strip(m.View(80)); !strings.Contains(out, "no matches") {
		t.Errorf("view does not say there are no matches:\n%s", out)
	}
}

func TestFilterViewShowsQuery(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	m := New("Pick a session", items(), WithFilter())
	if out := ansi.Strip(m.View(80)); !strings.Contains(out, "type to filter") {
		t.Errorf("empty-query view missing the hint:\n%s", out)
	}
	m.SetQuery("opus")
	out := ansi.Strip(m.View(80))
	if !strings.Contains(out, "/ opus") {
		t.Errorf("view missing the query line:\n%s", out)
	}
	// Row numbers are dropped in filter mode (digits type, they don't jump).
	if strings.Contains(out, "1. ") {
		t.Errorf("filter view still numbers rows (implies a digit jump):\n%s", out)
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

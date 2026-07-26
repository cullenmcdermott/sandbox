package picker

import (
	"fmt"
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

func TestFilterMatchesIDNameAndDesc(t *testing.T) {
	m := New("t", items(), WithFilter())
	m.SetQuery("FABLE") // case-insensitive, matches Name "Fable 5"
	if vis := m.Filtered(); len(vis) != 1 || vis[0].ID != "claude-fable-5" {
		t.Errorf("name match failed: %+v", vis)
	}
	m.SetQuery("claude-opus") // matches ID only — reach beyond the visible label
	if vis := m.Filtered(); len(vis) != 1 || vis[0].ID != "claude-opus-4-8" {
		t.Errorf("id match failed: %+v", vis)
	}
	// Desc IS matched: it is on screen, and for a list whose rows share a Name
	// (every session of one project) it holds the only distinguishing detail.
	// Excluding it made a query typed straight off the screen match nothing.
	m.SetQuery("most capable")
	if vis := m.Filtered(); len(vis) != 1 || vis[0].ID != "claude-fable-5" {
		t.Errorf("desc match failed: %+v", vis)
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
	// Item carries a slice (Cols) and so is not comparable; check the fields.
	if got := m.SelectedItem(); got.ID != "" || got.Name != "" || got.Desc != "" || got.Cols != nil {
		t.Errorf("SelectedItem on an empty set = %+v, want zero", got)
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

// --- height budget + row truncation ----------------------------------------

func manyItems(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{ID: fmt.Sprintf("id-%02d", i), Name: fmt.Sprintf("row-%02d", i)}
	}
	return out
}

// countRowLines counts rendered lines that ARE a manyItems row — matched at the
// line start (after the box edge, padding and the selection chevron) so the
// filter query line, which echoes the typed text, is not miscounted as a row. A
// wrapped row pushes its tail onto a second line without the label, so this
// counts rows, not lines — exactly what the window cap is about.
func countRowLines(out string) int {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		body := strings.TrimPrefix(strings.TrimSpace(strings.Trim(l, "│")), "› ")
		// The default grammar numbers its rows ("1. row-00"); filter mode does not.
		if _, rest, ok := strings.Cut(strings.TrimSpace(body), ". "); ok && strings.HasPrefix(rest, "row-") {
			n++
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(body), "row-") {
			n++
		}
	}
	return n
}

// A row longer than the box must be TRUNCATED, not wrapped: the enclosing box is
// width-constrained, so an unclamped row silently becomes two or three lines —
// which is what pushed a session picker's own title off the top of the screen.
// The invariant is that row height does not depend on row content.
func TestViewTruncatesEveryRowSoNoneWrap(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	short := []Item{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	long := []Item{
		{Name: strings.Repeat("long-", 30), Desc: strings.Repeat("detail ", 20)},
		// Unselected AND long: the case the old code left unclamped, since only
		// the selected row was truncated.
		{Name: strings.Repeat("also-long-", 20), Desc: strings.Repeat("more ", 30)},
		{Name: "c"},
	}
	for _, w := range []int{40, 80, 120} {
		wantLines := len(strings.Split(New("t", short).View(w), "\n"))
		gotLines := len(strings.Split(New("t", long).View(w), "\n"))
		if gotLines != wantLines {
			t.Errorf("width %d: long rows rendered %d lines, short rows %d — a row wrapped",
				w, gotLines, wantLines)
		}
	}
}

func TestMaxRowsWindowsTheListAndCountsWhatIsHidden(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	m := New("t", manyItems(40), WithMaxRows(5))
	out := ansi.Strip(m.View(80))
	if got := countRowLines(out); got != 5 {
		t.Errorf("drew %d rows, want the 5-row cap:\n%s", got, out)
	}
	// Selection is at the top, so the whole remainder is hidden below.
	for _, want := range []string{"row-00", "row-04", "↓ 35 more"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q in:\n%s", want, out)
		}
	}
	for _, dont := range []string{"row-05", "↑ "} {
		if strings.Contains(out, dont) {
			t.Errorf("view should not contain %q in:\n%s", dont, out)
		}
	}
}

func TestMaxRowsWindowFollowsTheSelection(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	m := New("t", manyItems(40), WithMaxRows(5))
	for i := 0; i < 39; i++ {
		m.MoveDown()
		out := ansi.Strip(m.View(80))
		if want := fmt.Sprintf("row-%02d", i+1); !strings.Contains(out, want) {
			t.Fatalf("selection %q scrolled out of the window:\n%s", want, out)
		}
		if got := countRowLines(out); got != 5 {
			t.Fatalf("at row %d: drew %d rows, want 5", i+1, got)
		}
	}
	// At the bottom the hidden count is all above, none below.
	out := ansi.Strip(m.View(80))
	if !strings.Contains(out, "↑ 35 more") || strings.Contains(out, "↓ ") {
		t.Errorf("bottom of list should report only rows hidden above:\n%s", out)
	}
}

// The cap applies to the FILTERED set, and a query that narrows below the cap
// drops the markers entirely.
func TestMaxRowsAppliesAfterFiltering(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	m := New("t", manyItems(40), WithFilter(), WithMaxRows(5))
	m.SetQuery("row-1") // row-10 … row-19: 10 matches, still over the cap
	out := ansi.Strip(m.View(80))
	if got := countRowLines(out); got != 5 {
		t.Errorf("drew %d rows, want 5:\n%s", got, out)
	}
	if !strings.Contains(out, "↓ 5 more") {
		t.Errorf("want 5 hidden below in:\n%s", out)
	}
	m.SetQuery("row-17") // one match: no window, no markers
	out = ansi.Strip(m.View(80))
	if got := countRowLines(out); got != 1 {
		t.Errorf("drew %d rows, want 1:\n%s", got, out)
	}
	if strings.Contains(out, "more") {
		t.Errorf("a fully-visible list must not claim hidden rows:\n%s", out)
	}
}

func TestMaxRowsZeroIsUnbounded(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	m := New("t", manyItems(40))
	if got := m.MaxRows(); got != 0 {
		t.Errorf("MaxRows default = %d, want 0", got)
	}
	if got := countRowLines(ansi.Strip(m.View(80))); got != 40 {
		t.Errorf("unbounded picker drew %d rows, want 40", got)
	}
	m.SetMaxRows(-3) // a host that computed a nonsense budget still gets a row
	if got := m.MaxRows(); got != 1 {
		t.Errorf("SetMaxRows(-3) = %d, want the 1-row floor", got)
	}
}

// --- aligned columns --------------------------------------------------------

func recordItems() []Item {
	return []Item{
		{ID: "a-1111", Name: "Review TODO list", Cols: []string{"sandbox", "1111", "now"}},
		{ID: "b-2222", Name: "x", Cols: []string{"demo-project", "2222", "18h"}},
		{ID: "c-3333", Name: "a much longer session title", Cols: []string{"homelab", "3333", "2d"}},
	}
}

// cellStarts reports the display column each cell of a rendered row begins at,
// which is the only thing "aligned" can mean here.
func cellStarts(line string, cells []string) []int {
	plain := ansi.Strip(line)
	out := make([]int, 0, len(cells))
	from := 0
	for _, c := range cells {
		i := strings.Index(plain[from:], c)
		if i < 0 {
			return nil
		}
		out = append(out, lipgloss.Width(plain[:from+i]))
		from += i + len(c)
	}
	return out
}

func rowLines(out string, names []string) []string {
	lines := []string{}
	for _, l := range strings.Split(out, "\n") {
		for _, n := range names {
			if strings.Contains(l, n) {
				lines = append(lines, l)
				break
			}
		}
	}
	return lines
}

func TestColumnsAlignAcrossRows(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	items := recordItems()
	m := New("Which session?", items, WithFilter(), WithMaxWidth(100))
	out := ansi.Strip(m.View(120))
	lines := rowLines(out, []string{"1111", "2222", "3333"})
	if len(lines) != 3 {
		t.Fatalf("want 3 row lines, got %d:\n%s", len(lines), out)
	}
	// Every row's repo/id/age must begin at the SAME display column, whatever
	// its name's length — that is what makes the fields scannable down the list.
	var want []int
	for i, l := range lines {
		got := cellStarts(l, items[i].Cols)
		if got == nil {
			t.Fatalf("row %d missing a cell: %q", i, l)
		}
		if i == 0 {
			want = got
			continue
		}
		for c := range want {
			if got[c] != want[c] {
				t.Errorf("row %d column %d starts at %d, row 0 has %d:\n%s", i, c, got[c], want[c], out)
			}
		}
	}
}

// A row with fewer cells than the widest row must leave the missing ones blank
// rather than shifting its later columns left.
func TestColumnsTolerateRaggedRows(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	items := []Item{
		{Name: "full", Cols: []string{"repo", "1111", "now", "title"}},
		{Name: "short", Cols: []string{"repo", "2222"}},
	}
	m := New("t", items, WithFilter(), WithMaxWidth(100))
	out := ansi.Strip(m.View(120))
	lines := rowLines(out, []string{"1111", "2222"})
	if len(lines) != 2 {
		t.Fatalf("want 2 rows, got %d:\n%s", len(lines), out)
	}
	full := cellStarts(lines[0], []string{"repo", "1111"})
	short := cellStarts(lines[1], []string{"repo", "2222"})
	for c := range full {
		if full[c] != short[c] {
			t.Errorf("column %d: full row at %d, short row at %d:\n%s", c, full[c], short[c], out)
		}
	}
}

// When the row does not fit, the NAME gives up width first — an id or an age
// truncated to "1…" is worthless, while a shortened title still reads. (Past the
// name's floor there is nothing left to give and the row is simply truncated
// like any other; that is below any width these columns are usable at.)
func TestTightBoxSqueezesTheNameNotTheCells(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })
	const boxW = 56 // holds the cells, but not the longest name at full length
	m := New("t", recordItems(), WithFilter(), WithMaxWidth(boxW))
	out := ansi.Strip(m.View(boxW + 8))
	for _, want := range []string{"sandbox", "1111", "now", "demo-project", "18h", "2d"} {
		if !strings.Contains(out, want) {
			t.Errorf("tight box dropped cell %q:\n%s", want, out)
		}
	}
	// The long name is the one that paid for it.
	if !strings.Contains(out, "…") {
		t.Errorf("nothing was truncated, so this is not the squeeze case:\n%s", out)
	}
	if strings.Contains(out, "a much longer session title") {
		t.Errorf("the long name survived intact, so the cells must have been cut:\n%s", out)
	}
	// And nothing wrapped: the box's own width is still respected.
	for i, l := range strings.Split(m.View(boxW+8), "\n") {
		if w := lipgloss.Width(l); w > boxW+8 {
			t.Errorf("line %d overflows (%d cols): %q", i, w, l)
		}
	}
}

func TestColumnsAreFilterable(t *testing.T) {
	m := New("t", recordItems(), WithFilter())
	m.SetQuery("demo-project") // a repo cell, visible on screen
	if vis := m.Filtered(); len(vis) != 1 || vis[0].ID != "b-2222" {
		t.Errorf("column filter failed: %+v", vis)
	}
	m.SetQuery("2d") // an age cell
	if vis := m.Filtered(); len(vis) != 1 || vis[0].ID != "c-3333" {
		t.Errorf("age filter failed: %+v", vis)
	}
}

func TestMaxWidthDefaultsAndFloors(t *testing.T) {
	m := New("t", items())
	if got := m.MaxWidth(); got != defaultMaxWidth {
		t.Errorf("MaxWidth default = %d, want %d", got, defaultMaxWidth)
	}
	m.SetMaxWidth(5) // below the floor: a box this narrow cannot hold a row
	if got := m.MaxWidth(); got != minBoxWidth {
		t.Errorf("SetMaxWidth(5) = %d, want the %d floor", got, minBoxWidth)
	}
	// A raised cap actually widens the box.
	wide := New("t", recordItems(), WithFilter(), WithMaxWidth(100))
	narrow := New("t", recordItems(), WithFilter())
	if lipgloss.Width(strings.Split(wide.View(200), "\n")[0]) <=
		lipgloss.Width(strings.Split(narrow.View(200), "\n")[0]) {
		t.Error("WithMaxWidth did not widen the box")
	}
}

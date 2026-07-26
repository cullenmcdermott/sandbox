// Package picker is a public, reusable selection overlay: a numbered list with
// ↑/↓ (and k/j) navigation, 1-9 jump-and-choose, enter to confirm, and esc to
// cancel — the model/backend/account picker vocabulary from the dashboard,
// generalized and freed of any app transport or lifecycle policy. It is a Charm
// Bubble Tea v2 component and imports nothing under internal/.
//
// The host supplies the rows and the choose/cancel callbacks; the picker owns
// only the selection state and rendering. Multi-stage flows (e.g. backend →
// account) are the host's business: drive one picker per stage.
//
// Long lists can opt into type-to-filter with WithFilter; see that option for
// why it is opt-in rather than always on.
package picker

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

const glyphChevron = "›"

// boxChrome is what the overlay's frame costs a line horizontally: two border
// columns plus one column of padding on each side. Rows and the query line are
// clamped to boxW-boxChrome, NOT boxW — clamping to the box's outer width still
// overflows the content area by these four columns, and lipgloss answers an
// overflowing line by wrapping it, which is how a one-line row silently becomes
// two and the list outgrows its height budget.
const boxChrome = 4

// Item is one selectable row. ID is the opaque value the host acts on; Name is
// the row label; Desc is an optional dim detail; Current marks the active choice
// (a dim-green ✓ and pre-selection at open).
type Item struct {
	ID      string
	Name    string
	Desc    string
	Current bool
}

// Option configures a Model.
type Option func(*Model)

// WithChoose registers the callback fired when a row is confirmed (enter or a
// row digit).
func WithChoose(fn func(Item)) Option { return func(m *Model) { m.onChoose = fn } }

// WithCancel registers the callback fired when the picker is dismissed (esc).
func WithCancel(fn func()) Option { return func(m *Model) { m.onCancel = fn } }

// WithFilter enables type-to-filter: printable keys build a query that narrows
// the rows to those whose ID, Name, or Desc contains it (case-insensitively),
// backspace and ctrl+u edit it, and esc clears a non-empty query before it
// cancels.
//
// It is opt-in because it necessarily takes over the key grammar: once letters
// type into a query, j/k can no longer navigate (↑/↓ and ctrl+n/ctrl+p do) and
// digits can no longer jump to a row (they are part of the query — you cannot
// type "claude-4-5" otherwise). A picker of four models is better served by the
// default vocabulary; a picker of forty sessions is not navigable without this.
func WithFilter() Option { return func(m *Model) { m.filter = true } }

// WithMaxRows caps how many rows View draws at once, scrolling a window of that
// size to keep the selection visible and reporting the rows it hid. Zero (the
// default) draws every row.
//
// A picker taller than its terminal is worse than useless — the box's own top,
// including the title and the filter query, scrolls off and the user is left
// staring at the middle of a list with no way to tell what is happening. Hosts
// that can size themselves should pass a budget derived from the terminal
// height; see (*Model).SetMaxRows to update it as the window resizes.
func WithMaxRows(n int) Option { return func(m *Model) { m.SetMaxRows(n) } }

// Model is a picker overlay. Build one with New; drive it with Update; render
// with View.
type Model struct {
	title    string
	items    []Item
	sel      int
	filter   bool
	query    string
	maxRows  int
	onChoose func(Item)
	onCancel func()
}

// New builds a picker titled title over items. The row marked Current (if any)
// is pre-selected.
func New(title string, items []Item, opts ...Option) *Model {
	m := &Model{title: title, items: items}
	for i := range items {
		if items[i].Current {
			m.sel = i
		}
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Items / SetItems read and replace the row set (clamping the selection).
func (m *Model) Items() []Item { return m.items }
func (m *Model) SetItems(items []Item) {
	m.items = items
	m.clampSel()
}

// Filtered returns the rows currently visible: every item when no query is
// active, else those matching it. Selected indexes into THIS slice, so a host
// reading the picker's state mid-filter reads the same rows the user sees.
func (m *Model) Filtered() []Item {
	if !m.filter || m.query == "" {
		return m.items
	}
	q := strings.ToLower(m.query)
	out := make([]Item, 0, len(m.items))
	for _, it := range m.items {
		if matches(it, q) {
			out = append(out, it)
		}
	}
	return out
}

// MaxRows / SetMaxRows read and replace the visible-row cap (0 = unbounded).
// Negative values clamp to 1: a host that computed a budget from a tiny
// terminal should still get a usable row, not an empty box.
func (m *Model) MaxRows() int { return m.maxRows }
func (m *Model) SetMaxRows(n int) {
	if n < 0 {
		n = 1
	}
	m.maxRows = n
}

// visibleWindow returns the [lo, hi) slice bounds of Filtered that View draws:
// everything when unbounded or short enough, else a maxRows-sized window
// centered on the selection and clamped to the ends. It is derived from the
// selection rather than kept as scroll state so that a filter keystroke — which
// can change both the row set and the selection at once — can never leave a
// stale offset pointing past the end.
func (m *Model) visibleWindow(n int) (int, int) {
	if m.maxRows <= 0 || n <= m.maxRows {
		return 0, n
	}
	lo := m.sel - m.maxRows/2
	if hi := n - m.maxRows; lo > hi {
		lo = hi
	}
	if lo < 0 {
		lo = 0
	}
	return lo, lo + m.maxRows
}

// Query reports the active filter text (always "" unless WithFilter was set).
func (m *Model) Query() string { return m.query }

// SetQuery replaces the filter text and moves the cursor to the top match —
// the selection cannot meaningfully survive a change to the set it indexes, and
// the first match is what the next keystroke is aiming at.
func (m *Model) SetQuery(q string) {
	if !m.filter {
		return
	}
	m.query = q
	m.sel = 0
}

// matches reports whether q (already lowercased) is a substring of the item's
// ID, Name, or Desc.
//
// Desc is included because the filter's contract is "narrow by what you can
// see": a row's distinguishing detail — a branch, an age, a short id — lives in
// Desc, and a query drawn from the visible label that matched nothing read as a
// broken filter. ID stays searchable even when a row does not display it, which
// is strictly extra reach and never surprises in the other direction.
func matches(it Item, q string) bool {
	return strings.Contains(strings.ToLower(it.ID), q) ||
		strings.Contains(strings.ToLower(it.Name), q) ||
		strings.Contains(strings.ToLower(it.Desc), q)
}

// Selected reports the cursor index (into Filtered).
func (m *Model) Selected() int { return m.sel }

// SelectedItem returns the currently highlighted item (zero Item when the
// visible set is empty — which a filter that matches nothing can produce).
func (m *Model) SelectedItem() Item {
	vis := m.Filtered()
	if m.sel < 0 || m.sel >= len(vis) {
		return Item{}
	}
	return vis[m.sel]
}

// MoveUp / MoveDown move the cursor, clamping at the ends.
func (m *Model) MoveUp() {
	if m.sel > 0 {
		m.sel--
	}
}
func (m *Model) MoveDown() {
	if m.sel < len(m.Filtered())-1 {
		m.sel++
	}
}

// clampSel keeps the cursor inside the visible set.
func (m *Model) clampSel() {
	if n := len(m.Filtered()); m.sel >= n {
		m.sel = max(0, n-1)
	}
}

// Update routes a key. In the default grammar: ↑/k and ↓/j navigate, a 1-9 digit
// jumps to and chooses a row, enter confirms the current row, and esc cancels.
// Under WithFilter the letter and digit keys type into the query instead — see
// updateFiltering. Non-grammar keys are swallowed (a picker is a full-capture
// overlay).
func (m *Model) Update(msg tea.Msg) (*Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.filter {
		m.updateFiltering(key)
		return m, nil
	}
	switch key.String() {
	case "esc":
		if m.onCancel != nil {
			m.onCancel()
		}
	case "up", "k":
		m.MoveUp()
	case "down", "j":
		m.MoveDown()
	case "enter":
		m.choose(m.sel)
	default:
		if s := key.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			if i := int(s[0] - '1'); i < len(m.items) {
				m.sel = i
				m.choose(i)
			}
		}
	}
	return m, nil
}

// updateFiltering is the WithFilter key grammar. Navigation is arrow keys plus
// ctrl+n/ctrl+p (readline muscle memory, since j/k now type); every printable
// key extends the query; esc clears a non-empty query before it cancels, so a
// mistyped filter costs one keystroke rather than the whole picker.
func (m *Model) updateFiltering(key tea.KeyPressMsg) {
	switch key.String() {
	case "esc":
		if m.query != "" {
			m.SetQuery("")
			return
		}
		if m.onCancel != nil {
			m.onCancel()
		}
	case "up", "ctrl+p":
		m.MoveUp()
	case "down", "ctrl+n":
		m.MoveDown()
	case "enter":
		m.choose(m.sel)
	case "backspace":
		if r := []rune(m.query); len(r) > 0 {
			m.SetQuery(string(r[:len(r)-1]))
		}
	case "ctrl+u":
		m.SetQuery("")
	default:
		if t := key.Text; t != "" && !hasControl(t) {
			m.SetQuery(m.query + t)
		}
	}
}

// hasControl reports whether s contains a control rune — the guard that keeps a
// key whose Text is set but unprintable from landing in the query.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// choose fires the callback for visible row i (indexes Filtered).
func (m *Model) choose(i int) {
	vis := m.Filtered()
	if i < 0 || i >= len(vis) {
		return
	}
	if m.onChoose != nil {
		m.onChoose(vis[i])
	}
}

// View renders the picker box at the given width: a titled, rounded overlay of
// numbered rows with the selected row highlighted, the current choice marked ✓,
// and a keybind hint footer. Under WithFilter it gains a query line and drops
// the row numbers (digits type into the query, so numbering would advertise a
// jump that no longer exists). Under WithMaxRows it draws a scrolling window of
// the rows instead of all of them, bracketed by the counts it hid.
//
// Every line is truncated to fit: the box wraps anything wider, and a wrapped
// row breaks both the layout and the height budget.
func (m *Model) View(width int) string {
	boxW := width - 8
	if boxW < 30 {
		boxW = 30
	} else if boxW > 60 {
		boxW = 60
	}
	title := m.title
	if title == "" {
		title = "Select"
	}
	lines := []string{lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(title), ""}
	if m.filter {
		lines = append(lines, m.queryLine(boxW-boxChrome), "")
	}
	vis := m.Filtered()
	if len(vis) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextMuted).Render("no matches"))
	}
	// Hidden-row counts bracket the window, so a scrolled list never reads as the
	// whole list — without them the user cannot tell there is more to reach.
	lo, hi := m.visibleWindow(len(vis))
	if lo > 0 {
		lines = append(lines, moreLine(lo, "↑"))
	}
	for i := lo; i < hi; i++ {
		lines = append(lines, m.line(i, vis[i], boxW-boxChrome))
	}
	if hi < len(vis) {
		lines = append(lines, moreLine(len(vis)-hi, "↓"))
	}
	lines = append(lines, "", m.hints())
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Malibu).
		Background(theme.Surface).
		Width(boxW).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// queryLine renders the filter prompt: the typed text plus a block cursor, or a
// dim hint while it is empty.
func (m *Model) queryLine(w int) string {
	prompt := lipgloss.NewStyle().Foreground(theme.Guac).Render("/ ")
	if m.query == "" {
		return clamp(prompt+lipgloss.NewStyle().Foreground(theme.TextMuted).Render("type to filter"), w)
	}
	return clamp(prompt+lipgloss.NewStyle().Foreground(theme.TextBright).Render(m.query)+
		lipgloss.NewStyle().Foreground(theme.TextDim).Render("▏"), w)
}

// moreLine renders a hidden-row count for one end of a scrolled window.
func moreLine(n int, arrow string) string {
	return "  " + lipgloss.NewStyle().Foreground(theme.TextMuted).
		Render(arrow+" "+strconv.Itoa(n)+" more")
}

// hints renders the keybind footer for the active grammar.
func (m *Model) hints() string {
	if m.filter {
		return kit.KbdRow(
			[2]string{"↑/↓", "select"},
			[2]string{"type", "filter"},
			[2]string{"enter", "choose"},
			[2]string{"esc", "clear/close"},
		)
	}
	return kit.KbdRow(
		[2]string{"↑/↓", "select"},
		[2]string{"1-9", "jump"},
		[2]string{"enter", "choose"},
		[2]string{"esc", "close"},
	)
}

// line renders one row: "› 1. <name>  <dim desc>" with the current choice
// suffixed by a dim-green ✓ and the selected row highlighted. The number is
// omitted in filter mode (see View).
func (m *Model) line(i int, r Item, w int) string {
	var num string
	if !m.filter {
		num = strconv.Itoa(i+1) + ". "
	}
	var suffix string
	if r.Current {
		suffix = " " + lipgloss.NewStyle().Foreground(theme.Guac).Faint(true).Render("✓")
	}
	var desc string
	if r.Desc != "" {
		desc = "  " + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(r.Desc)
	}
	if i == m.sel {
		body := lipgloss.NewStyle().Foreground(theme.Guac).Render(glyphChevron+" ") +
			lipgloss.NewStyle().Foreground(theme.TextDim).Render(num) +
			lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(r.Name) + suffix + desc
		return lipgloss.NewStyle().Background(theme.Raised2).Width(w).Render(clamp(body, w))
	}
	// Every row is truncated, not just the selected one: an over-long row is
	// WRAPPED by the enclosing box (which is Width-constrained, not overflow-
	// hidden), and one wrapped row silently costs the list two or three lines of
	// the height budget while looking like a rendering glitch.
	return clamp("  "+lipgloss.NewStyle().Foreground(theme.TextDim).Render(num)+
		lipgloss.NewStyle().Foreground(theme.Malibu).Render(r.Name)+suffix+desc, w)
}

// clamp truncates a styled line to w display columns (ANSI/grapheme-aware).
func clamp(s string, w int) string {
	if w < 1 {
		w = 1
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

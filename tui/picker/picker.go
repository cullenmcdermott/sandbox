// Package picker is a public, reusable selection overlay: a numbered list with
// ↑/↓ (and k/j) navigation, 1-9 jump-and-choose, enter to confirm, and esc to
// cancel — the model/backend/account picker vocabulary from the dashboard,
// generalized and freed of any app transport or lifecycle policy. It is a Charm
// Bubble Tea v2 component and imports nothing under internal/.
//
// The host supplies the rows and the choose/cancel callbacks; the picker owns
// only the selection state and rendering. Multi-stage flows (e.g. backend →
// account) are the host's business: drive one picker per stage.
package picker

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

const glyphChevron = "›"

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

// Model is a picker overlay. Build one with New; drive it with Update; render
// with View.
type Model struct {
	title    string
	items    []Item
	sel      int
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
	if m.sel >= len(items) {
		m.sel = max(0, len(items)-1)
	}
}

// Selected reports the cursor index.
func (m *Model) Selected() int { return m.sel }

// SelectedItem returns the currently highlighted item (zero Item when empty).
func (m *Model) SelectedItem() Item {
	if m.sel < 0 || m.sel >= len(m.items) {
		return Item{}
	}
	return m.items[m.sel]
}

// MoveUp / MoveDown move the cursor, clamping at the ends.
func (m *Model) MoveUp() {
	if m.sel > 0 {
		m.sel--
	}
}
func (m *Model) MoveDown() {
	if m.sel < len(m.items)-1 {
		m.sel++
	}
}

// Update routes a key: ↑/k and ↓/j navigate, a 1-9 digit jumps to and chooses a
// row, enter confirms the current row, and esc cancels. Non-grammar keys are
// swallowed (a picker is a full-capture overlay).
func (m *Model) Update(msg tea.Msg) (*Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
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

func (m *Model) choose(i int) {
	if i < 0 || i >= len(m.items) {
		return
	}
	if m.onChoose != nil {
		m.onChoose(m.items[i])
	}
}

// View renders the picker box at the given width: a titled, rounded overlay of
// numbered rows with the selected row highlighted, the current choice marked ✓,
// and a keybind hint footer.
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
	for i, r := range m.items {
		lines = append(lines, m.line(i, r, boxW-2))
	}
	lines = append(lines, "", kit.KbdRow(
		[2]string{"↑/↓", "select"},
		[2]string{"1-9", "jump"},
		[2]string{"enter", "choose"},
		[2]string{"esc", "close"},
	))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Malibu).
		Background(theme.Surface).
		Width(boxW).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// line renders one numbered row: "› 1. <name>  <dim desc>" with the current
// choice suffixed by a dim-green ✓ and the selected row highlighted.
func (m *Model) line(i int, r Item, w int) string {
	num := strconv.Itoa(i+1) + ". "
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
	return "  " + lipgloss.NewStyle().Foreground(theme.TextDim).Render(num) +
		lipgloss.NewStyle().Foreground(theme.Malibu).Render(r.Name) + suffix + desc
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

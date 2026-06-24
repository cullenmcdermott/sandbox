package main

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// modelChoice is one selectable Claude model in the picker.
type modelChoice struct {
	name string
	desc string
}

func modelChoices() []modelChoice {
	return []modelChoice{
		{"Opus 4.8", "claude-opus-4-8 · most capable"},
		{"Sonnet 4.6", "claude-sonnet-4-6 · balanced speed & depth"},
		{"Haiku 4.5", "claude-haiku-4-5 · fastest, lightest"},
	}
}

// pickerKey handles selection in the picker overlay.
func (m *model) pickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	choices := modelChoices()
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.pickerSel > 0 {
			m.pickerSel--
		}
	case "down", "j":
		if m.pickerSel < len(choices)-1 {
			m.pickerSel++
		}
	case "t":
		theme.Cycle()
	case "enter":
		m.startChat(choices[m.pickerSel].name)
	}
	return m, nil
}

// pickerView composites the floating picker over a muted backdrop, with a drop
// shadow — the z-ordered layer/compositor pattern from the dashboard, rebuilt
// from public packages only.
func (m *model) pickerView() string {
	backdrop := m.st.page.Width(m.width).Height(m.height).Render(
		lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top,
			lipgloss.NewStyle().Foreground(theme.TextDim).MarginTop(1).Render("choose a model")),
	)

	box := m.renderPicker()
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	bx, by := max((m.width-bw)/2, 0), max((m.height-bh)/2, 0)
	shadow := solidBlock(bw, bh, theme.Shadow)

	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(backdrop).X(0).Y(0).Z(0),
		lipgloss.NewLayer(shadow).X(bx+2).Y(by+1).Z(1),
		lipgloss.NewLayer(box).X(bx).Y(by).Z(2),
	))
	return canvas.Render()
}

// renderPicker draws the bordered picker box.
func (m *model) renderPicker() string {
	const boxW = 60
	const innerW = boxW - 2 // Padding(0,1)
	const labelW = 10
	descW := max(innerW-(2+labelW+1), 8)

	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("new claude session")
	lines := []string{kit.TitledRule(title, innerW, theme.Charple, theme.Dolly)}

	for i, c := range modelChoices() {
		mark := theme.MarkClaudeStyled()
		label := padRight(c.name, labelW)
		desc := truncate(c.desc, descW)
		if i == m.pickerSel {
			row := lipgloss.NewStyle().Foreground(theme.Guac).Render("❯") + mark + " " +
				lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(label) + " " +
				lipgloss.NewStyle().Foreground(theme.TextBody).Render(desc)
			lines = append(lines, lipgloss.NewStyle().Background(theme.Raised2).Width(innerW).Render(row))
		} else {
			row := " " + mark + " " + label + " " + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(desc)
			lines = append(lines, row)
		}
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", innerW)),
		kit.KbdRow([2]string{"↑/↓", "select"}, [2]string{"↵", "create"}, [2]string{"t", "theme"}, [2]string{"esc", "back"}),
	)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Charple).
		Background(theme.Surface).
		Width(boxW).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// solidBlock returns a w×h block filled with background color c — the picker's
// drop shadow.
func solidBlock(w, h int, c color.Color) string {
	if w < 1 || h < 1 {
		return ""
	}
	row := lipgloss.NewStyle().Background(c).Render(strings.Repeat(" ", w))
	rows := make([]string, h)
	for i := range rows {
		rows[i] = row
	}
	return strings.Join(rows, "\n")
}

func padRight(s string, w int) string {
	s = truncate(s, w)
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	r := []rune(s)
	if max == 1 {
		return "…"
	}
	for len(r) > 0 && lipgloss.Width(string(r))+1 > max {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

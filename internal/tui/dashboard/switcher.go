package dashboard

// switcher.go — the ⌃K fuzzy quick-switcher (slice 5d / Mockup C). It is a
// modal overlay over either the dashboard or the chat modal: type to fuzzy
// filter, ↑/↓ to select, enter to jump, esc to close. The switcher works on
// the live session list and emits an attachMsg when a session is chosen.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// switcherModel holds the state of the ⌃K quick-switcher.
type switcherModel struct {
	query string
	sel   int
	open  bool
}

// openSwitcher initializes the switcher with the current session list.
func (m *Model) openSwitcher() {
	m.switcher = switcherModel{open: true}
}

// closeSwitcher hides the switcher.
func (m *Model) closeSwitcher() {
	m.switcher = switcherModel{}
}

// switcherFiltered returns the visible sessions that match the current query.
func (m *Model) switcherFiltered() []Session {
	q := strings.ToLower(strings.TrimSpace(m.switcher.query))
	visible := m.visibleSessions()
	if q == "" {
		return visible
	}
	var out []Session
	for _, s := range visible {
		// Match on both the display title (rename/auto-title — what the list
		// shows) and the raw derived title, so a renamed session is findable
		// by either name.
		title := strings.ToLower(s.DisplayTitle() + " " + s.Title)
		repo := strings.ToLower(filepathBaseLocal(s.State.ProjectPath))
		backend := strings.ToLower(s.State.Backend)
		if strings.Contains(title, q) || strings.Contains(repo, q) || strings.Contains(backend, q) {
			out = append(out, s)
		}
	}
	return out
}

// renderSwitcher builds the switcher overlay string for the given width.
func (m *Model) renderSwitcher(w int) string {
	boxW := w - 20
	if boxW < 34 {
		boxW = 34
	}
	if boxW > 56 {
		boxW = 56
	}
	cursor := []string{"·", "•", "●", "•"}[m.spinnerFrame%4]
	q := m.switcher.query
	prompt := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("⌃K ") +
		lipgloss.NewStyle().Foreground(theme.TextBright).Render(q) +
		lipgloss.NewStyle().Foreground(theme.Charple).Render(cursor)
	lines := []string{prompt, lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", boxW))}

	matches := m.switcherFiltered()
	if len(matches) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.TextMuted).Render("no matches"))
	} else {
		sel := m.switcher.sel
		if sel < 0 {
			sel = 0
		}
		if sel >= len(matches) {
			sel = len(matches) - 1
		}
		for i, s := range matches {
			selected := i == sel
			var g string
			if s.DashStatus == StatusBusy {
				g = theme.SpinnerFrame(m.spinnerFrame)
			} else {
				gcol := glyphColor(s.DashStatus)
				g = lipgloss.NewStyle().Foreground(gcol).Render(s.DashStatus.Glyph())
			}
			if selected {
				row := lipgloss.NewStyle().Foreground(theme.Guac).Render(glyphChevron+" ") +
					g + " " +
					lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(padRight(truncate(s.DisplayTitle(), 16), 16)) +
					lipgloss.NewStyle().Foreground(theme.TextBody).Render(filepathBaseLocal(s.State.ProjectPath))
				lines = append(lines, lipgloss.NewStyle().Background(theme.Raised2).Width(boxW).Render(row))
			} else {
				row := "  " + g + " " + padRight(truncate(s.DisplayTitle(), 18), 18) +
					lipgloss.NewStyle().Foreground(theme.TextMuted).Render(filepathBaseLocal(s.State.ProjectPath))
				lines = append(lines, row)
			}
		}
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Charple).
		Background(theme.Surface).
		Width(boxW).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// switcherKey handles a key event while the switcher is open. It returns the
// follow-up command and whether the key was consumed.
func (m *Model) switcherKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	k := msg.String()
	switch k {
	case "esc", "ctrl+k":
		m.closeSwitcher()
		return nil, true
	case "up":
		if m.switcher.sel > 0 {
			m.switcher.sel--
		}
		return nil, true
	case "down":
		matches := m.switcherFiltered()
		if m.switcher.sel < len(matches)-1 {
			m.switcher.sel++
		}
		return nil, true
	case "enter":
		matches := m.switcherFiltered()
		if len(matches) == 0 {
			m.closeSwitcher()
			return nil, true
		}
		sel := m.switcher.sel
		if sel < 0 || sel >= len(matches) {
			sel = 0
		}
		s := matches[sel]
		m.closeSwitcher()
		return func() tea.Msg { return attachMsg{sess: s} }, true
	case "backspace":
		if len(m.switcher.query) > 0 {
			m.switcher.query = m.switcher.query[:len(m.switcher.query)-1]
			m.switcher.sel = 0
		}
		return nil, true
	}
	// Add printable runes to the query.
	key := msg.Key()
	if key.Code != 0 && key.Mod == 0 && key.Text != "" {
		m.switcher.query += key.Text
		m.switcher.sel = 0
		return nil, true
	}
	return nil, false
}

// glyphChevron is the selection indicator used in the switcher.
const glyphChevron = "›"

package dashboard

// backend_picker.go — the new-session backend picker (Mockup: `n` opens a small
// centered overlay to choose which agent backend a new session runs). It mirrors
// the ⌃K switcher's interaction model (↑/↓ select, enter confirm, esc cancel)
// but lives on the App, which owns the Creator. Selecting a backend dispatches
// createCmd(backend); esc returns to the dashboard without provisioning.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// backendChoice is one selectable backend in the picker.
type backendChoice struct {
	backend string // session.Backend* value passed to the Creator
	label   string // short display name
	desc    string // one-line description
}

// backendChoices are the backends a new session can run, in display order. The
// first is the default landing selection.
var backendChoices = []backendChoice{
	{session.BackendClaudeSDK, "claude", "Claude Agent SDK — the native transcript UI"},
	{session.BackendOpenCode, "opencode", "opencode serve — external opencode TUI"},
}

// backendPicker is the App-level new-session overlay. It is rendered over the
// dashboard while open; zero value (open == false) means hidden.
type backendPicker struct {
	open bool
	sel  int
}

// openBackendPicker shows the picker with the default backend selected.
func (a *App) openBackendPicker() {
	a.picker = backendPicker{open: true, sel: 0}
}

// closeBackendPicker hides the picker.
func (a *App) closeBackendPicker() {
	a.picker = backendPicker{}
}

// pickerKey handles a key event while the picker is open. It returns the
// follow-up command and whether the key was consumed. On enter it closes the
// picker and provisions a session with the chosen backend.
func (a *App) pickerKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		a.closeBackendPicker()
		return nil, true
	case "up", "k":
		if a.picker.sel > 0 {
			a.picker.sel--
		}
		return nil, true
	case "down", "j":
		if a.picker.sel < len(backendChoices)-1 {
			a.picker.sel++
		}
		return nil, true
	case "enter":
		sel := a.picker.sel
		if sel < 0 || sel >= len(backendChoices) {
			sel = 0
		}
		backend := backendChoices[sel].backend
		a.closeBackendPicker()
		a.connectingFor = &Session{Title: "new session"}
		a.connectErr = nil
		a.screen = ScreenConnecting
		return a.createCmd(backend), true
	}
	return nil, false
}

// pickerView composites the backend picker as a floating, centered overlay over
// the live dashboard. z-order: dashboard < shadow < picker.
func (a *App) pickerView() tea.View {
	bg := a.dashboard.View().Content
	w, h := a.width, a.height
	if w == 0 || h == 0 {
		v := tea.NewView(bg)
		v.AltScreen = true
		return v
	}

	box := a.renderBackendPicker()
	bw := lipgloss.Width(box)
	bh := lipgloss.Height(box)
	bx := (w - bw) / 2
	by := (h - bh) / 2
	if bx < 0 {
		bx = 0
	}
	if by < 0 {
		by = 0
	}
	shadow := solidBlock(bw, bh, theme.Shadow)

	layers := []*lipgloss.Layer{
		lipgloss.NewLayer(bg).X(0).Y(0).Z(0),
		lipgloss.NewLayer(shadow).X(bx + 2).Y(by + 1).Z(1),
		lipgloss.NewLayer(box).X(bx).Y(by).Z(2),
	}
	canvas := lipgloss.NewCanvas(w, h)
	canvas.Compose(lipgloss.NewCompositor(layers...))
	v := tea.NewView(canvas.Render())
	v.AltScreen = true
	return v
}

// renderBackendPicker builds the bordered picker box. Width is sized so the
// longest option description fits on one line without wrapping (T9). All inner
// content is laid out to innerW (boxW minus the 0×1 horizontal padding) so the
// title rule, rows, and separator align and don't overflow the border.
func (a *App) renderBackendPicker() string {
	const boxW = 64
	const innerW = boxW - 2 // account for Padding(0, 1)
	const labelW = 10
	// Chevron/indent (2) + label (labelW) + space (1) before the description.
	descW := innerW - (2 + labelW + 1)
	if descW < 8 {
		descW = 8
	}

	// Dialog title as a Charple→Dolly titled gradient rule (§B.2/§B.3).
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("new session")
	lines := []string{
		kit.TitledRule(title, innerW, theme.Charple, theme.Dolly),
	}

	sel := a.picker.sel
	if sel < 0 {
		sel = 0
	}
	if sel >= len(backendChoices) {
		sel = len(backendChoices) - 1
	}
	for i, c := range backendChoices {
		label := padRight(truncate(c.label, labelW), labelW)
		desc := truncate(c.desc, descW)
		if i == sel {
			row := lipgloss.NewStyle().Foreground(theme.Guac).Render(glyphChevron+" ") +
				lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(label) +
				" " + lipgloss.NewStyle().Foreground(theme.TextBody).Render(desc)
			lines = append(lines, lipgloss.NewStyle().Background(theme.Raised2).Width(innerW).Render(row))
		} else {
			row := "  " + label + " " + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(desc)
			lines = append(lines, row)
		}
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", innerW)),
		kit.KbdRow([2]string{"↑/↓", "select"}, [2]string{"↵", "create"}, [2]string{"esc", "cancel"}),
	)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Charple).
		Background(theme.Surface).
		Width(boxW).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

package dashboard

// backend_picker.go — the new-session backend picker (Mockup: `n` opens a small
// centered overlay to choose which agent backend a new session runs). It mirrors
// the ⌃K switcher's interaction model (↑/↓ select, enter confirm, esc cancel)
// but lives on the App, which owns the Creator. Selecting opencode dispatches
// createCmd immediately; selecting claude drills into the Anthropic account
// stage (account_picker.go) when stored accounts exist. esc from the backend
// stage returns to the dashboard without provisioning.

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
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

// pickerStage identifies which step of the new-session overlay is active. The
// overlay is a small state machine: backend → (for claude, when accounts exist)
// account → add-account type → label → login/console form. esc walks one step
// back; from the backend stage it closes to the dashboard. See pickerKey for
// the transitions.
type pickerStage int

const (
	// stageBackend is the initial claude/opencode chooser.
	stageBackend pickerStage = iota
	// stageAccount is the per-account chooser shown after "claude" when stored
	// accounts exist (rows: accounts, "cluster default", "＋ add account").
	stageAccount
	// stageAddType chooses how to add an account (subscription | console).
	stageAddType
	// stageLabelForm is the display-label entry for a new account (both kinds),
	// pre-filled with the type's default ("claude.ai" / "console"). Without it,
	// two same-kind accounts would render as indistinguishable picker rows —
	// inviting exactly the wrong-account pick (work vs personal billing/data)
	// the fail-closed design exists to prevent.
	stageLabelForm
	// stageConsoleForm is the masked API-key entry for a console account.
	stageConsoleForm
)

// backendPicker is the App-level new-session overlay. It is rendered over the
// dashboard while open; zero value (open == false) means hidden. Beyond the
// initial backend chooser it also drives the Anthropic account picker and the
// add-account sub-flow (stage), so a single overlay owns the whole new-session
// interaction. Secret bytes NEVER live here: the console form holds a masked
// textinput whose value is passed straight to the injected AccountStore and is
// never rendered.
type backendPicker struct {
	open  bool
	stage pickerStage
	sel   int

	// accounts is the metadata list loaded when entering stageAccount (no secret
	// bytes). Populated from the injected AccountStore.
	accounts []AccountInfo
	// listErr is a store List() failure surfaced in the account stage. When set,
	// the stage shows the error and does NOT offer a silent legacy create — the
	// user must go back and cannot pick an account (fail closed).
	listErr error
	// loginErr is the last add-account/login failure, shown inline in the account
	// stage after a failed sub-flow.
	loginErr error

	// addType is the addTypeChoices index picked in stageAddType (0 subscription,
	// 1 console). It drives the label prefill and which flow the label stage
	// hands off to; sel is reused per stage so the choice must be kept here.
	addType int
	// pendingLabel is the accepted display label from stageLabelForm, consumed by
	// the console-form submit (the subscription flow threads it directly).
	pendingLabel string

	// input is the active text field: the plain label entry in stageLabelForm,
	// and the masked API-key field in stageConsoleForm (EchoPassword — its value
	// is never rendered and never stored in the dashboard beyond the submit call
	// to AccountStore.AddConsoleKey).
	input textinput.Model
	// formErr is an inline validation error shown under the console form.
	formErr error
}

// openBackendPicker shows the picker with the default backend selected.
func (a *App) openBackendPicker() {
	a.picker = backendPicker{open: true, stage: stageBackend, sel: 0}
}

// closeBackendPicker hides the picker.
func (a *App) closeBackendPicker() {
	a.picker = backendPicker{}
}

// pickerKey handles a key event while the picker is open. It returns the
// follow-up command and whether the key was consumed. It dispatches to the
// active stage; the console form owns arbitrary text input, so it must be tried
// before the shared navigation keys.
func (a *App) pickerKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch a.picker.stage {
	case stageBackend:
		return a.pickerKeyBackend(msg)
	case stageAccount:
		return a.pickerKeyAccount(msg)
	case stageAddType:
		return a.pickerKeyAddType(msg)
	case stageLabelForm:
		return a.pickerKeyLabelForm(msg)
	case stageConsoleForm:
		return a.pickerKeyConsoleForm(msg)
	}
	return nil, false
}

// pickerKeyBackend handles the initial claude/opencode chooser. Selecting a
// non-claude backend provisions immediately. Selecting claude transitions to the
// account picker when stored accounts exist; with no store or zero accounts it
// keeps today's UX and creates on the shared Secret immediately.
func (a *App) pickerKeyBackend(msg tea.KeyPressMsg) (tea.Cmd, bool) {
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
		if backend == session.BackendClaudeSDK {
			return a.enterAccountStage(), true
		}
		return a.beginCreate(CreateParams{Backend: backend}), true
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

	box := a.renderPicker()
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

// renderPicker builds the bordered box for the active overlay stage. All stages
// share the same width/border chrome so the overlay reads as one dialog that
// swaps its body as the user drills in.
func (a *App) renderPicker() string {
	switch a.picker.stage {
	case stageAccount:
		return a.renderAccountPicker()
	case stageAddType:
		return a.renderAddTypePicker()
	case stageLabelForm:
		return a.renderLabelForm()
	case stageConsoleForm:
		return a.renderConsoleForm()
	default:
		return a.renderBackendPicker()
	}
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
		// Brand mark (one cell) ahead of the label so each backend is identifiable
		// by its glyph, not just its name. labelW already reserves room; the mark
		// replaces one space of the chevron/indent column.
		mark := BackendMark(c.backend)
		if mark == "" {
			mark = " "
		}
		label := padRight(truncate(c.label, labelW), labelW)
		desc := truncate(c.desc, descW)
		if i == sel {
			row := lipgloss.NewStyle().Foreground(theme.Guac).Render(glyphChevron) + mark + " " +
				lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(label) +
				" " + lipgloss.NewStyle().Foreground(theme.TextBody).Render(desc)
			lines = append(lines, lipgloss.NewStyle().Background(theme.Raised2).Width(innerW).Render(row))
		} else {
			row := " " + mark + " " + label + " " + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(desc)
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

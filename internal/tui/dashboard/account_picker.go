package dashboard

// account_picker.go — the Anthropic account step of the new-session overlay.
// After the user picks "claude" in the backend picker, this drives a second
// overlay stage where they choose which stored Anthropic account the session
// runs on (or the shared cluster Secret), plus an add-account sub-flow.
//
// The dashboard stays decoupled from Keychain / client/cred: it only ever
// sees account METADATA (AccountInfo) and calls injected AccountStore funcs to
// list/add accounts. Secret bytes never enter this package — the subscription
// login runs as a terminal-handover subprocess whose token is captured and
// stored entirely inside the injected store, and the console form's typed key
// is masked and passed straight through to the store, never rendered or
// retained here beyond the submit call.

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// AccountInfo is the dashboard-side view of one stored Anthropic account: pure
// metadata, never secret bytes. It mirrors the fields the picker renders.
type AccountInfo struct {
	// ID is the stable account identifier threaded to the Creator as
	// CreateParams.AnthropicAccountID. It is opaque to the dashboard.
	ID string
	// Label is the human-chosen display name.
	Label string
	// Type is the account kind for the glyph/tag: "subscription" or "console".
	Type string
	// Default marks the store's default account (shown with a marker).
	Default bool
}

// AccountStore is the dashboard's injected view of the credential store. It
// exposes ONLY metadata and login operations — never secret bytes — so the
// dashboard package never imports client/cred and Keychain access stays in the
// CLI layer. The concrete implementation is supplied via RunOptions.
type AccountStore interface {
	// ListAccounts returns the stored account metadata. A non-nil error must be
	// surfaced (fail closed): the picker never falls back to a silent shared-Secret
	// create when the list might be non-empty but unreadable.
	ListAccounts() ([]AccountInfo, error)

	// SubscriptionLogin prepares a claude.ai subscription login. It returns a
	// tea.ExecCommand that runs `claude setup-token` with stdin/stderr on the real
	// terminal and stdout captured internally, plus a finalize func to call after
	// the subprocess exits: finalize parses the captured token, stores it, and
	// returns the new account's metadata. The captured token never crosses back
	// into the dashboard — only the resulting metadata does. label may be "" (the
	// store picks a sensible default).
	SubscriptionLogin(label string) (tea.ExecCommand, func() (AccountInfo, error))

	// AddConsoleKey validates and stores a console API key, returning the new
	// account's metadata. The key is consumed by the store (validated + normalized
	// + persisted) and must never be echoed. label may be "".
	AddConsoleKey(label, key string) (AccountInfo, error)
}

// accountLoginDoneMsg reports the outcome of the subscription login terminal
// handover (tea.Exec). Exactly one of id/err is meaningful. It is handled by
// App.Update, which reloads the account list and re-selects the new account (or
// surfaces the error inline in the account stage).
type accountLoginDoneMsg struct {
	id  string
	err error
}

// addTypeChoice is one row of the add-account type chooser.
type addTypeChoice struct {
	label string
	desc  string
}

// addTypeChoices are the account kinds the add-account sub-flow can create, in
// display order. Index 0 is subscription (terminal handover), 1 is console (form).
var addTypeChoices = []addTypeChoice{
	{"claude.ai", "subscription login via `claude setup-token`"},
	{"console", "paste an Anthropic Console API key"},
}

// --------------------------------------------------------------------------
// Stage transitions
// --------------------------------------------------------------------------

// beginCreate closes the overlay and provisions a session with the given params,
// mirroring the original backend-picker enter behavior (connecting screen +
// createCmd). It is the single funnel every "create now" path routes through.
func (a *App) beginCreate(params CreateParams) tea.Cmd {
	a.closeBackendPicker()
	a.connectingFor = &Session{Title: "new session"}
	a.connectErr = nil
	a.screen = ScreenConnecting
	return a.createCmd(params)
}

// enterAccountStage is called when the user picks "claude". With no injected
// store it keeps today's UX and creates immediately on the shared Secret.
// Otherwise it ALWAYS opens the account picker (TODO §2d, DECIDED 2026-07-07):
// even with zero stored accounts the stage still shows, offering the "cluster
// default" row (identical to the old silent skip) and "＋ add account" so a
// first-time user discovers per-account login instead of being dropped onto the
// shared Secret invisibly. A store List() error is surfaced (fail closed).
//
// This stage is where the row set is decided; it is also the future home of the
// §6 reauth flow (a re-login entry for an existing account slots in here).
func (a *App) enterAccountStage() tea.Cmd {
	if a.accountStore == nil {
		return a.beginCreate(CreateParams{Backend: session.BackendClaudeSDK})
	}
	accounts, err := a.accountStore.ListAccounts()
	if err != nil {
		// Fail closed: show the error, offer only "esc → back". The user must NOT
		// be silently dropped onto the shared Secret when accounts might exist.
		a.picker.stage = stageAccount
		a.picker.accounts = nil
		a.picker.listErr = err
		a.picker.loginErr = nil
		a.picker.sel = 0
		return nil
	}
	// Zero accounts no longer skips: accountRowCount() is len(accounts)+2, so an
	// empty list still yields the two-row stage (cluster default + add account).
	a.picker.stage = stageAccount
	a.picker.accounts = accounts
	a.picker.listErr = nil
	a.picker.loginErr = nil
	a.picker.sel = 0
	return nil
}

// accountRowCount is the number of selectable rows in the account stage: one per
// account, then the "cluster default" row, then "＋ add account".
func (a *App) accountRowCount() int {
	return len(a.picker.accounts) + 2
}

// pickerKeyAccount handles the account chooser. esc returns to the backend
// picker (NOT the dashboard). Enter creates on the selected account, on the
// shared Secret (cluster default), or opens the add-account sub-flow.
func (a *App) pickerKeyAccount(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if a.picker.listErr != nil {
		// Only "esc → back" is available while a list error is shown (fail closed).
		if msg.String() == "esc" {
			a.picker.stage = stageBackend
			a.picker.listErr = nil
			a.picker.sel = 0
		}
		return nil, true
	}
	n := a.accountRowCount()
	switch msg.String() {
	case "esc":
		a.picker.stage = stageBackend
		a.picker.loginErr = nil
		a.picker.sel = 0
		return nil, true
	case "up", "k":
		if a.picker.sel > 0 {
			a.picker.sel--
		}
		return nil, true
	case "down", "j":
		if a.picker.sel < n-1 {
			a.picker.sel++
		}
		return nil, true
	case "enter":
		sel := a.picker.sel
		switch {
		case sel < len(a.picker.accounts):
			return a.beginCreate(CreateParams{
				Backend:            session.BackendClaudeSDK,
				AnthropicAccountID: a.picker.accounts[sel].ID,
			}), true
		case sel == len(a.picker.accounts):
			// Cluster default (shared Secret) — an explicit, visible legacy choice.
			return a.beginCreate(CreateParams{Backend: session.BackendClaudeSDK}), true
		default:
			// ＋ add account
			a.picker.stage = stageAddType
			a.picker.loginErr = nil
			a.picker.sel = 0
			return nil, true
		}
	}
	return nil, false
}

// pickerKeyAddType handles the account-kind chooser. subscription runs the
// terminal-handover login; console opens the masked key form. esc returns to the
// account picker.
func (a *App) pickerKeyAddType(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		a.picker.stage = stageAccount
		a.picker.sel = 0
		return nil, true
	case "up", "k":
		if a.picker.sel > 0 {
			a.picker.sel--
		}
		return nil, true
	case "down", "j":
		if a.picker.sel < len(addTypeChoices)-1 {
			a.picker.sel++
		}
		return nil, true
	case "enter":
		a.picker.addType = a.picker.sel
		a.picker.pendingLabel = "" // fresh flow: don't inherit a prior label
		return a.enterLabelForm(), true
	}
	return nil, false
}

// defaultAccountLabel is the label prefill for a new account of the given
// addTypeChoices index ("claude.ai" for subscription, "console" for console).
func defaultAccountLabel(addType int) string {
	if addType >= 0 && addType < len(addTypeChoices) {
		return addTypeChoices[addType].label
	}
	return ""
}

// enterLabelForm opens the display-label entry for a new account, prefilled
// with the pending label (when returning from a later step) or the type's
// default. A distinct label is what keeps two same-kind accounts tellable
// apart in the picker — the wrong-account pick this flow must prevent.
func (a *App) enterLabelForm() tea.Cmd {
	prefill := a.picker.pendingLabel
	if prefill == "" {
		prefill = defaultAccountLabel(a.picker.addType)
	}
	ti := textinput.New()
	ti.Placeholder = defaultAccountLabel(a.picker.addType)
	ti.CharLimit = 64
	ti.Prompt = "  label: "
	ti.SetValue(prefill)
	cmd := ti.Focus()
	a.picker.input = ti
	a.picker.formErr = nil
	a.picker.stage = stageLabelForm
	a.picker.sel = 0
	return cmd
}

// pickerKeyLabelForm edits the label field. enter accepts (an emptied field
// falls back to the type default) and hands off to the login step for the
// chosen kind; esc walks back to the type chooser.
func (a *App) pickerKeyLabelForm(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		a.picker.input = textinput.Model{}
		a.picker.stage = stageAddType
		a.picker.sel = a.picker.addType
		return nil, true
	case "enter":
		label := strings.TrimSpace(a.picker.input.Value())
		if label == "" {
			label = defaultAccountLabel(a.picker.addType)
		}
		a.picker.pendingLabel = label
		a.picker.input = textinput.Model{}
		if a.picker.addType == 0 {
			return a.startSubscriptionLogin(label), true
		}
		return a.enterConsoleForm(), true
	}
	ti, cmd := a.picker.input.Update(msg)
	a.picker.input = ti
	return cmd, true
}

// startSubscriptionLogin hands the terminal to `claude setup-token` via tea.Exec
// (the dashboard is suspended for the duration, exactly like the $EDITOR shell-
// out). The injected store owns the subprocess construction (stdout captured for
// the token) and the parse+store on return, so no token bytes touch this package.
func (a *App) startSubscriptionLogin(label string) tea.Cmd {
	if a.accountStore == nil {
		return nil
	}
	execCmd, finalize := a.accountStore.SubscriptionLogin(label)
	return tea.Exec(execCmd, func(runErr error) tea.Msg {
		if runErr != nil {
			return accountLoginDoneMsg{err: runErr}
		}
		info, err := finalize()
		if err != nil {
			return accountLoginDoneMsg{err: err}
		}
		return accountLoginDoneMsg{id: info.ID}
	})
}

// enterConsoleForm sets up the masked API-key field. EchoPassword ensures the
// typed key is never rendered (View shows '*' per rune).
func (a *App) enterConsoleForm() tea.Cmd {
	ti := textinput.New()
	ti.EchoMode = textinput.EchoPassword
	ti.Placeholder = "sk-ant-…"
	ti.CharLimit = 256
	ti.Prompt = "  key: "
	cmd := ti.Focus()
	a.picker.input = ti
	a.picker.formErr = nil
	a.picker.stage = stageConsoleForm
	a.picker.sel = 0
	return cmd
}

// pickerKeyConsoleForm edits the masked key field. enter validates+stores via
// the injected store (errors shown inline, key stays masked); esc walks back to
// the label form, dropping the typed key.
func (a *App) pickerKeyConsoleForm(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		a.picker.input = textinput.Model{} // drop the typed key
		a.picker.formErr = nil
		return a.enterLabelForm(), true // pendingLabel re-prefills the field
	case "enter":
		if a.accountStore == nil {
			return nil, true
		}
		key := a.picker.input.Value()
		info, err := a.accountStore.AddConsoleKey(a.picker.pendingLabel, key)
		if err != nil {
			// Inline validation error; keep the (masked) field so the user can fix it.
			a.picker.formErr = err
			return nil, true
		}
		a.picker.input = textinput.Model{} // drop the key once stored
		return a.finishAccountAdd(info.ID), true
	}
	// Any other key edits the masked field.
	ti, cmd := a.picker.input.Update(msg)
	a.picker.input = ti
	return cmd, true
}

// finishAccountAdd returns to the account stage after a successful add, reloading
// the list and selecting the new account. A reload error is surfaced (fail
// closed) rather than dropping the user onto a stale/empty list.
func (a *App) finishAccountAdd(id string) tea.Cmd {
	a.picker.stage = stageAccount
	a.picker.formErr = nil
	a.picker.loginErr = nil
	if a.accountStore == nil {
		return nil
	}
	accounts, err := a.accountStore.ListAccounts()
	if err != nil {
		a.picker.accounts = nil
		a.picker.listErr = err
		a.picker.sel = 0
		return nil
	}
	a.picker.accounts = accounts
	a.picker.listErr = nil
	a.picker.sel = accountIndex(accounts, id)
	return nil
}

// handleAccountLoginDone applies the outcome of the subscription terminal
// handover. On success it re-selects the new account; on failure it returns to
// the account stage with the error shown inline and the list reloaded.
func (a *App) handleAccountLoginDone(msg accountLoginDoneMsg) tea.Cmd {
	if !a.picker.open {
		return nil
	}
	if msg.err != nil {
		a.picker.stage = stageAccount
		a.picker.loginErr = msg.err
		a.picker.formErr = nil
		a.picker.sel = 0
		if a.accountStore != nil {
			if accounts, err := a.accountStore.ListAccounts(); err != nil {
				a.picker.accounts = nil
				a.picker.listErr = err
			} else {
				a.picker.accounts = accounts
				a.picker.listErr = nil
			}
		}
		return nil
	}
	return a.finishAccountAdd(msg.id)
}

// accountIndex returns the row index of id, or 0 if not found.
func accountIndex(accounts []AccountInfo, id string) int {
	for i, a := range accounts {
		if a.ID == id {
			return i
		}
	}
	return 0
}

// --------------------------------------------------------------------------
// Rendering
// --------------------------------------------------------------------------

const pickerBoxW = 64
const pickerInnerW = pickerBoxW - 2 // account for Padding(0, 1)

// pickerBox wraps stage body lines in the shared bordered chrome so every stage
// reads as one dialog.
func pickerBox(lines []string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Charple).
		Background(theme.Surface).
		Width(pickerBoxW).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// pickerRow renders one selectable row (chevron + optional mark + label + desc)
// with the backend-picker's selected/unselected styling.
func pickerRow(mark, label, desc string, selected bool, innerW, labelW int) string {
	descW := innerW - (2 + labelW + 1)
	if descW < 8 {
		descW = 8
	}
	if mark == "" {
		mark = " "
	}
	l := padRight(truncate(label, labelW), labelW)
	d := truncate(desc, descW)
	if selected {
		row := lipgloss.NewStyle().Foreground(theme.Guac).Render(glyphChevron) + mark + " " +
			lipgloss.NewStyle().Foreground(theme.TextBright).Bold(true).Render(l) +
			" " + lipgloss.NewStyle().Foreground(theme.TextBody).Render(d)
		return lipgloss.NewStyle().Background(theme.Raised2).Width(innerW).Render(row)
	}
	return " " + mark + " " + l + " " + lipgloss.NewStyle().Foreground(theme.TextMuted).Render(d)
}

// accountTypeTag returns a short muted tag for an account type, used as the
// per-account glyph column so the kind is identifiable at a glance.
func accountTypeTag(typ string) string {
	switch typ {
	case "subscription":
		return "◈"
	case "console":
		return "⚿"
	default:
		return "•"
	}
}

// renderAccountPicker builds the account chooser box: one row per stored account
// (label + type glyph + default marker), a "cluster default (shared Secret)" row,
// and a trailing "＋ add account" row. A list error replaces the rows with an
// error and a back hint (fail closed).
func (a *App) renderAccountPicker() string {
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("anthropic account")
	lines := []string{kit.TitledRule(title, pickerInnerW, theme.Charple, theme.Dolly)}

	if a.picker.listErr != nil {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(theme.Coral).Render(truncate("✗ "+a.picker.listErr.Error(), pickerInnerW)),
			"",
			lipgloss.NewStyle().Foreground(theme.TextMuted).Render("could not read stored accounts — pick again from the backend list"),
			lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", pickerInnerW)),
			kit.KbdRow([2]string{"esc", "back"}),
		)
		return pickerBox(lines)
	}

	const labelW = 18
	sel := a.picker.sel
	for i, acct := range a.picker.accounts {
		marker := ""
		if acct.Default {
			marker = "  (default)"
		}
		lines = append(lines, pickerRow(
			accountTypeTag(acct.Type),
			acct.Label+marker,
			acct.Type,
			i == sel,
			pickerInnerW, labelW,
		))
	}
	// Cluster default (shared Secret) — the visible legacy fallback.
	clusterIdx := len(a.picker.accounts)
	lines = append(lines, pickerRow(
		" ",
		"cluster default",
		"shared operator Secret (legacy)",
		sel == clusterIdx,
		pickerInnerW, labelW,
	))
	// ＋ add account
	addIdx := clusterIdx + 1
	lines = append(lines, pickerRow(
		"＋",
		"add account",
		"log in with a new Anthropic account",
		sel == addIdx,
		pickerInnerW, labelW,
	))

	if a.picker.loginErr != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.Coral).Render(truncate("✗ "+a.picker.loginErr.Error(), pickerInnerW)))
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", pickerInnerW)),
		kit.KbdRow([2]string{"↑/↓", "select"}, [2]string{"↵", "create"}, [2]string{"esc", "back"}),
	)
	return pickerBox(lines)
}

// renderAddTypePicker builds the add-account kind chooser.
func (a *App) renderAddTypePicker() string {
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("add anthropic account")
	lines := []string{kit.TitledRule(title, pickerInnerW, theme.Charple, theme.Dolly)}

	const labelW = 12
	sel := a.picker.sel
	for i, c := range addTypeChoices {
		lines = append(lines, pickerRow("", c.label, c.desc, i == sel, pickerInnerW, labelW))
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", pickerInnerW)),
		kit.KbdRow([2]string{"↑/↓", "select"}, [2]string{"↵", "choose"}, [2]string{"esc", "back"}),
	)
	return pickerBox(lines)
}

// renderLabelForm renders the plain (unmasked) display-label entry, prefilled
// with the type default or the previously accepted label.
func (a *App) renderLabelForm() string {
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("account label")
	lines := []string{
		kit.TitledRule(title, pickerInnerW, theme.Charple, theme.Dolly),
		lipgloss.NewStyle().Foreground(theme.TextMuted).Render("name this account (how it appears in the picker):"),
		a.picker.input.View(),
		lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", pickerInnerW)),
		kit.KbdRow([2]string{"↵", "next"}, [2]string{"esc", "back"}),
	}
	return pickerBox(lines)
}

// renderConsoleForm renders the masked API-key entry. The field is EchoPassword,
// so the typed key is never shown; only the mask and any validation error render.
func (a *App) renderConsoleForm() string {
	title := lipgloss.NewStyle().Foreground(theme.Malibu).Bold(true).Render("console api key")
	lines := []string{
		kit.TitledRule(title, pickerInnerW, theme.Charple, theme.Dolly),
		lipgloss.NewStyle().Foreground(theme.TextMuted).Render("paste your Anthropic Console API key (hidden):"),
		a.picker.input.View(),
	}
	if a.picker.formErr != nil {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.Coral).Render(truncate("✗ "+a.picker.formErr.Error(), pickerInnerW)))
	}
	lines = append(lines,
		lipgloss.NewStyle().Foreground(theme.BorderSubtle).Render(strings.Repeat("─", pickerInnerW)),
		kit.KbdRow([2]string{"↵", "save"}, [2]string{"esc", "back"}),
	)
	return pickerBox(lines)
}

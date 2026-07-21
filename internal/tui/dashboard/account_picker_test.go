package dashboard

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// noopExec is a do-nothing tea.ExecCommand for the fake store's SubscriptionLogin
// return (the login handover itself is exercised via accountLoginDoneMsg, so this
// command is never actually run in tests — it only needs to satisfy the type).
type noopExec struct{}

func (noopExec) Run() error          { return nil }
func (noopExec) SetStdin(io.Reader)  {}
func (noopExec) SetStdout(io.Writer) {}
func (noopExec) SetStderr(io.Writer) {}

// --------------------------------------------------------------------------
// Account-picker test fakes
// --------------------------------------------------------------------------

// fakeAccountStore is an in-memory AccountStore for headless picker tests. It
// records the console keys submitted so a test can assert the masked field's
// value reached the store, and lets tests inject a List error.
type fakeAccountStore struct {
	accounts    []AccountInfo
	listErr     error
	consoleErr  error
	addedKeys   []string // console keys passed to AddConsoleKey (the raw typed value)
	addedLabels []string // labels passed to AddConsoleKey
	subFinal    func() (AccountInfo, error)
	subExec     tea.ExecCommand
}

func (f *fakeAccountStore) ListAccounts() ([]AccountInfo, error) {
	return f.accounts, f.listErr
}

func (f *fakeAccountStore) AddConsoleKey(label, key string) (AccountInfo, error) {
	f.addedKeys = append(f.addedKeys, key)
	f.addedLabels = append(f.addedLabels, label)
	if f.consoleErr != nil {
		return AccountInfo{}, f.consoleErr
	}
	info := AccountInfo{ID: "acct-console-new", Label: "console", Type: "console"}
	f.accounts = append(f.accounts, info)
	return info, nil
}

func (f *fakeAccountStore) SubscriptionLogin(label string) (tea.ExecCommand, func() (AccountInfo, error)) {
	exe := f.subExec
	if exe == nil {
		exe = noopExec{}
	}
	fin := f.subFinal
	if fin == nil {
		fin = func() (AccountInfo, error) {
			info := AccountInfo{ID: "acct-sub-new", Label: "claude.ai", Type: "subscription"}
			f.accounts = append(f.accounts, info)
			return info, nil
		}
	}
	return exe, fin
}

// newAccountPickerApp builds a headless App wired to an account store and a
// Creator that reports the CreateParams it was called with over the returned
// channel. The window size is seeded so pickerView renders (View() is inspectable).
func newAccountPickerApp(t *testing.T, store AccountStore) (*App, chan CreateParams) {
	t.Helper()
	ch := make(chan CreateParams, 1)
	creator := func(_ context.Context, params CreateParams, _ func(ConnectStage, string)) (CreateResult, error) {
		ch <- params
		return CreateResult{
			State:  session.State{ID: "new1", Backend: params.Backend},
			Client: &fakeRunnerClient{},
		}, nil
	}
	app := NewApp(nil, nil, creator)
	app.accountStore = store
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return app, ch
}

// openBackendStage drives `n` → enter(cwd) through the leading directory stage
// (T10) so the app lands on the backend stage. It asserts both transitions.
func openBackendStage(t *testing.T, app *App) {
	t.Helper()
	app.Update(createSessionMsg{})
	if !app.picker.open || app.picker.stage != stageDir {
		t.Fatalf("createSessionMsg did not open the directory stage: open=%v stage=%v", app.picker.open, app.picker.stage)
	}
	app.Update(keyMsg("enter")) // accept cwd (the default row)
	if app.picker.stage != stageBackend {
		t.Fatalf("accepting cwd did not enter the backend stage: stage=%v", app.picker.stage)
	}
}

// openAccountStage drives `n` → enter(cwd) → enter(claude) so the app lands on
// the account stage. It asserts the transitions happened.
func openAccountStage(t *testing.T, app *App) {
	t.Helper()
	openBackendStage(t, app)
	app.Update(keyMsg("enter")) // claude is the default (sel 0)
	if app.picker.stage != stageAccount {
		t.Fatalf("selecting claude did not enter the account stage: stage=%v", app.picker.stage)
	}
}

func waitParams(t *testing.T, ch chan CreateParams) CreateParams {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("Creator was not called within 5 seconds")
		return CreateParams{}
	}
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

// TestAccountPickerZeroAccountsOpensStage: with no stored accounts, picking claude
// now ALWAYS enters the account stage (TODO §2d) instead of silently skipping. The
// stage shows exactly two rows — "host claude login" and "＋ add account" — and no
// per-account rows.
func TestAccountPickerZeroAccountsOpensStage(t *testing.T) {
	app, _ := newAccountPickerApp(t, &fakeAccountStore{accounts: nil})

	app.Update(createSessionMsg{})
	app.Update(keyMsg("enter")) // accept cwd (directory stage, T10)
	app.Update(keyMsg("enter")) // claude
	if !app.picker.open || app.picker.stage != stageAccount {
		t.Fatalf("zero accounts: overlay should stay on the account stage: open=%v stage=%v", app.picker.open, app.picker.stage)
	}
	if app.screen != ScreenDashboard {
		t.Errorf("zero accounts: screen = %v, want ScreenDashboard (no silent create)", app.screen)
	}
	if got := app.accountRowCount(); got != 2 {
		t.Fatalf("zero accounts: row count = %d, want 2 (host login + add account)", got)
	}
	if len(app.picker.accounts) != 0 {
		t.Errorf("zero accounts: account rows = %d, want 0", len(app.picker.accounts))
	}
	content := app.View().Content
	if !strings.Contains(content, "host claude login") {
		t.Error("zero accounts: the host claude login row is not rendered")
	}
	if !strings.Contains(content, "add account") {
		t.Error("zero accounts: the add account row is not rendered")
	}
}

// TestAccountPickerZeroAccountsHostLoginParity: with zero accounts, selecting the
// "host claude login" row (sel 0, the first of the two rows) must create with an
// empty account id — the CLI-side Creator resolves the host's Claude Code login
// (cred.SystemMaterial, Max mode).
func TestAccountPickerZeroAccountsHostLoginParity(t *testing.T) {
	app, ch := newAccountPickerApp(t, &fakeAccountStore{accounts: nil})

	app.Update(createSessionMsg{})
	app.Update(keyMsg("enter")) // accept cwd (directory stage, T10)
	app.Update(keyMsg("enter")) // claude → account stage
	if app.picker.stage != stageAccount || app.picker.sel != 0 {
		t.Fatalf("zero accounts: stage=%v sel=%d, want stageAccount sel=0 (host login)", app.picker.stage, app.picker.sel)
	}
	// Rows: [host-login(0), add-account(1)]. sel 0 is the host login.
	_, cmd := app.Update(keyMsg("enter"))
	if app.picker.open {
		t.Error("zero accounts: host login should have closed the picker and created")
	}
	if app.screen != ScreenConnecting {
		t.Errorf("zero accounts: screen = %v, want ScreenConnecting", app.screen)
	}
	if cmd == nil {
		t.Fatal("zero accounts: host login returned no create command")
	}
	p := waitParams(t, ch)
	if p.Backend != session.BackendClaudePane || p.AnthropicAccountID != "" {
		t.Errorf("zero accounts host login: params = %+v, want {claude-pane, \"\"}", p)
	}
}

// TestAccountPickerZeroAccountsAddAccountEntersAddFlow: with zero accounts, the
// second row ("＋ add account") enters the existing add-account type chooser — the
// same flow reused when accounts are present.
func TestAccountPickerZeroAccountsAddAccountEntersAddFlow(t *testing.T) {
	app, _ := newAccountPickerApp(t, &fakeAccountStore{accounts: nil})

	app.Update(createSessionMsg{})
	app.Update(keyMsg("enter")) // accept cwd (directory stage, T10)
	app.Update(keyMsg("enter")) // claude → account stage
	// Rows: [host-login(0), add-account(1)]. Move to add account.
	app.Update(keyMsg("down"))
	if app.picker.sel != 1 {
		t.Fatalf("zero accounts: sel=%d, want 1 (add account)", app.picker.sel)
	}
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageAddType {
		t.Fatalf("zero accounts: enter on add-account: stage=%v, want stageAddType", app.picker.stage)
	}
	if app.picker.sel != 0 {
		t.Errorf("zero accounts: add-type initial sel=%d, want 0", app.picker.sel)
	}
}

// TestAccountPickerNonEmptyRows: pins the non-empty stage layout. With N stored
// accounts the stage has N+2 rows (host login first, then each account — inert,
// rendered with the setup-token reason — then add account) and renders every
// account label plus the two framing rows.
func TestAccountPickerNonEmptyRows(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{
		{ID: "acct-aaaa", Label: "personal", Type: "subscription", Default: true},
		{ID: "acct-bbbb", Label: "work", Type: "console"},
	}}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	if got := app.accountRowCount(); got != 4 {
		t.Fatalf("non-empty row count = %d, want 4 (host login + 2 accounts + add account)", got)
	}
	content := app.View().Content
	for _, want := range []string{"host claude login", "personal", "work", "setup token", "add account"} {
		if !strings.Contains(content, want) {
			t.Errorf("non-empty stage missing row/marker %q", want)
		}
	}
}

// TestAccountPickerStoredAccountInert: enter on a stored account must NOT create
// a session — the store holds setup tokens interactive claude rejects (the "Not
// logged in" wall, live 2026-07-20). Instead the picker stays open and explains,
// steering to the host-login row.
func TestAccountPickerStoredAccountInert(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{
		{ID: "acct-aaaa", Label: "personal", Type: "subscription", Default: true},
		{ID: "acct-bbbb", Label: "work", Type: "console"},
	}}
	app, ch := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	// Rows: [host(0), personal(1), work(2), add(3)]. Move to the second account.
	app.Update(keyMsg("down"))
	app.Update(keyMsg("down"))
	if app.picker.sel != 2 {
		t.Fatalf("down did not move account selection: sel=%d", app.picker.sel)
	}
	app.Update(keyMsg("enter"))
	if !app.picker.open || app.picker.stage != stageAccount {
		t.Fatalf("enter on a stored account must keep the picker open: open=%v stage=%v", app.picker.open, app.picker.stage)
	}
	if app.screen != ScreenDashboard {
		t.Errorf("enter on a stored account: screen = %v, want ScreenDashboard (no create)", app.screen)
	}
	select {
	case p := <-ch:
		t.Fatalf("enter on a stored account created a session: %+v", p)
	case <-time.After(100 * time.Millisecond):
	}
	if app.picker.loginErr == nil || !strings.Contains(app.picker.loginErr.Error(), "setup token") {
		t.Fatalf("inert-account notice missing or wrong: %v", app.picker.loginErr)
	}
	if !strings.Contains(app.View().Content, "host claude login") {
		t.Error("the steering target (host claude login) is not visible")
	}
}

// TestAccountPickerHostLoginThreadsEmptyID: the leading "host claude login" row
// threads an empty account id (the CLI side resolves the host's Claude Code
// login), with stored accounts present.
func TestAccountPickerHostLoginThreadsEmptyID(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{
		{ID: "acct-aaaa", Label: "personal", Type: "subscription"},
	}}
	app, ch := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	// Rows: [host(0), acct(1), add-account(2)]. sel starts on the host row.
	if app.picker.sel != 0 {
		t.Fatalf("sel=%d, want 0 (host login is the default row)", app.picker.sel)
	}
	app.Update(keyMsg("enter"))
	p := waitParams(t, ch)
	if p.Backend != session.BackendClaudePane || p.AnthropicAccountID != "" {
		t.Errorf("host login: params = %+v, want {claude-pane, \"\"}", p)
	}
}

// TestAccountPickerEscReturnsToBackend: esc in the account stage walks back to
// the backend picker, not the dashboard.
func TestAccountPickerEscReturnsToBackend(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}}}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	app.Update(keyMsg("esc"))
	if !app.picker.open {
		t.Fatal("esc from account stage closed the overlay entirely")
	}
	if app.picker.stage != stageBackend {
		t.Errorf("esc from account stage: stage = %v, want stageBackend", app.picker.stage)
	}
}

// TestAccountPickerListErrorFailsClosed: a List() error when opening the picker
// is surfaced and does NOT silently create on the shared Secret.
func TestAccountPickerListErrorFailsClosed(t *testing.T) {
	store := &fakeAccountStore{listErr: errors.New("keychain locked")}
	app, ch := newAccountPickerApp(t, store)

	app.Update(createSessionMsg{})
	app.Update(keyMsg("enter")) // accept cwd (directory stage, T10)
	app.Update(keyMsg("enter")) // claude
	if !app.picker.open || app.picker.stage != stageAccount {
		t.Fatalf("list error: overlay should stay on the account stage: open=%v stage=%v", app.picker.open, app.picker.stage)
	}
	if app.picker.listErr == nil {
		t.Error("list error was not recorded for display")
	}
	if app.screen != ScreenDashboard {
		t.Errorf("list error: screen = %v, want ScreenDashboard (no silent create)", app.screen)
	}
	// enter must not create while the list error is shown (fail closed).
	app.Update(keyMsg("enter"))
	select {
	case p := <-ch:
		t.Fatalf("list error: enter created a session anyway: %+v", p)
	case <-time.After(100 * time.Millisecond):
	}
	// The error is visible in the overlay.
	if !strings.Contains(app.View().Content, "keychain locked") {
		t.Error("list error is not rendered in the overlay")
	}
}

// TestAddAccountTypeChoiceNavigation: entering ＋ add account shows the type
// chooser; ↑/↓ move within it; esc returns to the account picker.
func TestAddAccountTypeChoiceNavigation(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}}}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	// Rows: [host-login(0), acct(1), add-account(2)].
	app.Update(keyMsg("down"))
	app.Update(keyMsg("down"))
	if app.picker.sel != 2 {
		t.Fatalf("sel=%d, want 2 (add account)", app.picker.sel)
	}
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageAddType {
		t.Fatalf("enter on add-account: stage=%v, want stageAddType", app.picker.stage)
	}
	if app.picker.sel != 0 {
		t.Errorf("add-type initial sel=%d, want 0", app.picker.sel)
	}
	app.Update(keyMsg("down"))
	if app.picker.sel != 1 {
		t.Errorf("down in add-type: sel=%d, want 1 (console)", app.picker.sel)
	}
	app.Update(keyMsg("esc"))
	if app.picker.stage != stageAccount {
		t.Errorf("esc from add-type: stage=%v, want stageAccount", app.picker.stage)
	}
}

// TestConsoleFormMasksKeyAndSubmits: the typed console key is never rendered in
// View() (masked), and submitting passes the typed value to the injected store.
func TestConsoleFormMasksKeyAndSubmits(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}}}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	// Navigate to add account → console → label form → console form.
	app.Update(keyMsg("down"))
	app.Update(keyMsg("down")) // add-account row
	app.Update(keyMsg("enter"))
	app.Update(keyMsg("down"))  // console (sel 1)
	app.Update(keyMsg("enter")) // → stageLabelForm
	if app.picker.stage != stageLabelForm {
		t.Fatalf("did not reach the label form: stage=%v", app.picker.stage)
	}
	app.Update(keyMsg("enter")) // accept default label → stageConsoleForm
	if app.picker.stage != stageConsoleForm {
		t.Fatalf("did not reach the console form: stage=%v", app.picker.stage)
	}

	const secret = "ZZSECRETKEYZZ"
	for _, r := range secret {
		app.Update(keyMsg(string(r)))
	}
	// The raw key must NEVER appear in the rendered overlay (EchoPassword mask).
	if strings.Contains(app.View().Content, secret) {
		t.Fatal("the typed console key leaked into the rendered view (must be masked)")
	}

	// Submit: the store receives the exact typed key, and we return to the account
	// stage with the new account.
	app.Update(keyMsg("enter"))
	if len(store.addedKeys) != 1 || store.addedKeys[0] != secret {
		t.Fatalf("AddConsoleKey got %v, want one call with %q", store.addedKeys, secret)
	}
	if store.addedLabels[0] != "console" {
		t.Errorf("AddConsoleKey label = %q, want the accepted default %q", store.addedLabels[0], "console")
	}
	if app.picker.stage != stageAccount {
		t.Errorf("after console submit: stage=%v, want stageAccount", app.picker.stage)
	}
}

// REGRESSION (audit 2026-07-04, account_picker.go / app.go:788): a bracketed
// paste arrives as tea.PasteMsg, NOT tea.KeyPressMsg, so the picker inputs — the
// console-key field whose placeholder literally says "paste your Anthropic
// Console API key" and the label field — never received it. A pasted 100+ char
// key was silently dropped. App.Update must route PasteMsg into the active form
// input while the picker is open.
func TestConsoleFormAcceptsPaste(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}}}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	// Navigate to add account → console → label form → console form.
	app.Update(keyMsg("down"))
	app.Update(keyMsg("down")) // add-account row
	app.Update(keyMsg("enter"))
	app.Update(keyMsg("down"))  // console (sel 1)
	app.Update(keyMsg("enter")) // → stageLabelForm
	app.Update(keyMsg("enter")) // accept default label → stageConsoleForm
	if app.picker.stage != stageConsoleForm {
		t.Fatalf("did not reach the console form: stage=%v", app.picker.stage)
	}

	const pasted = "sk-ant-api03-PASTED-KEY-0123456789"
	app.Update(tea.PasteMsg{Content: pasted})

	// The paste reached the (masked) field, so submitting hands the exact bytes
	// to the store — proving the PasteMsg was not dropped.
	app.Update(keyMsg("enter"))
	if len(store.addedKeys) != 1 || store.addedKeys[0] != pasted {
		t.Fatalf("AddConsoleKey got %v, want one call with the pasted key %q", store.addedKeys, pasted)
	}
}

// TestLabelFormAcceptsPaste: the plain (unmasked) label field also receives a
// paste — same PasteMsg route, different stage.
func TestLabelFormAcceptsPaste(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}}}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	app.Update(keyMsg("down"))
	app.Update(keyMsg("down")) // add-account row
	app.Update(keyMsg("enter"))
	app.Update(keyMsg("down"))  // console (sel 1)
	app.Update(keyMsg("enter")) // → stageLabelForm
	if app.picker.stage != stageLabelForm {
		t.Fatalf("did not reach the label form: stage=%v", app.picker.stage)
	}

	// The label field is prefilled with the type default ("console"); pasting must
	// append into it, and the accepted label reaches the store on submit.
	app.picker.input.SetValue("")
	app.Update(tea.PasteMsg{Content: "work-laptop"})
	app.Update(keyMsg("enter")) // accept label → console form
	app.Update(keyMsg("k"))     // type a throwaway key
	app.Update(keyMsg("enter")) // submit console form
	if len(store.addedLabels) != 1 || store.addedLabels[0] != "work-laptop" {
		t.Fatalf("AddConsoleKey labels = %v, want one call with the pasted label %q", store.addedLabels, "work-laptop")
	}
}

// TestConsoleFormValidationErrorStaysInline: a store validation error keeps the
// form open with the error shown, and the key is still masked.
func TestConsoleFormValidationErrorStaysInline(t *testing.T) {
	store := &fakeAccountStore{
		accounts:   []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}},
		consoleErr: errors.New("invalid console key"),
	}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)
	app.Update(keyMsg("down"))
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter"))
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter")) // label form
	app.Update(keyMsg("enter")) // accept default label → console form
	for _, r := range "ZZBADZZ" {
		app.Update(keyMsg(string(r)))
	}
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageConsoleForm {
		t.Errorf("validation error should keep the console form open: stage=%v", app.picker.stage)
	}
	if app.picker.formErr == nil {
		t.Error("validation error was not recorded for inline display")
	}
	if strings.Contains(app.View().Content, "ZZBADZZ") {
		t.Error("the rejected key leaked into the view")
	}
}

// TestLabelFormCustomLabelThreadsToStore: a typed label replaces the prefill and
// reaches the store on console submit; esc from the label form returns to the
// type chooser, and esc from the console form returns to the label form with the
// accepted label re-prefilled.
func TestLabelFormCustomLabelThreadsToStore(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}}}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	// add account → console → label form.
	app.Update(keyMsg("down"))
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter"))
	app.Update(keyMsg("down"))
	app.Update(keyMsg("enter"))
	if app.picker.stage != stageLabelForm {
		t.Fatalf("did not reach the label form: stage=%v", app.picker.stage)
	}
	if got := app.picker.input.Value(); got != "console" {
		t.Errorf("label prefill = %q, want the type default %q", got, "console")
	}

	// esc walks back to the type chooser (console still selected), re-enter.
	app.Update(keyMsg("esc"))
	if app.picker.stage != stageAddType || app.picker.sel != 1 {
		t.Fatalf("esc from label form: stage=%v sel=%d, want stageAddType sel=1", app.picker.stage, app.picker.sel)
	}
	app.Update(keyMsg("enter"))

	// Replace the prefill with a custom label.
	for range "console" {
		app.Update(keyMsg("backspace"))
	}
	for _, r := range "work" {
		app.Update(keyMsg(string(r)))
	}
	app.Update(keyMsg("enter")) // → console form
	if app.picker.stage != stageConsoleForm {
		t.Fatalf("label accept did not reach the console form: stage=%v", app.picker.stage)
	}

	// esc from the console form returns to the label form with the label kept.
	app.Update(keyMsg("esc"))
	if app.picker.stage != stageLabelForm {
		t.Fatalf("esc from console form: stage=%v, want stageLabelForm", app.picker.stage)
	}
	if got := app.picker.input.Value(); got != "work" {
		t.Errorf("label re-prefill after esc = %q, want %q", got, "work")
	}
	app.Update(keyMsg("enter")) // back to console form

	for _, r := range "ZZKEYZZ" {
		app.Update(keyMsg(string(r)))
	}
	app.Update(keyMsg("enter"))
	if len(store.addedLabels) != 1 || store.addedLabels[0] != "work" {
		t.Errorf("AddConsoleKey labels = %v, want one call with %q", store.addedLabels, "work")
	}
}

// TestSubscriptionLoginReturnsAndSelects: the subscription login handover
// (accountLoginDoneMsg) returns to the account stage with the new account
// selected.
func TestSubscriptionLoginReturnsAndSelects(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}}}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	// Simulate the tea.Exec callback firing after a successful login: the store
	// already appended the new account, so re-selection resolves it.
	store.accounts = append(store.accounts, AccountInfo{ID: "acct-sub-new", Label: "claude.ai", Type: "subscription"})
	app.Update(accountLoginDoneMsg{id: "acct-sub-new"})
	if app.picker.stage != stageAccount {
		t.Fatalf("login done: stage=%v, want stageAccount", app.picker.stage)
	}
	// Row 0 is the host-login row, so account i sits at row i+1.
	if got := app.picker.accounts[app.picker.sel-1].ID; got != "acct-sub-new" {
		t.Errorf("login done: selected account = %q, want acct-sub-new", got)
	}
}

// TestSubscriptionLoginErrorSurfaces: a login error is shown in the account
// stage rather than crashing or creating.
func TestSubscriptionLoginErrorSurfaces(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}}}
	app, _ := newAccountPickerApp(t, store)
	openAccountStage(t, app)
	app.Update(accountLoginDoneMsg{err: errors.New("setup-token failed")})
	if app.picker.stage != stageAccount {
		t.Fatalf("login error: stage=%v, want stageAccount", app.picker.stage)
	}
	if app.picker.loginErr == nil {
		t.Error("login error was not recorded for display")
	}
	if !strings.Contains(app.View().Content, "setup-token failed") {
		t.Error("login error is not rendered in the overlay")
	}
}

// TestOpencodeSelectionUnchanged: picking opencode still creates immediately with
// no account step, regardless of stored accounts.
func TestOpencodeSelectionUnchanged(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{{ID: "acct-aaaa", Label: "personal", Type: "console"}}}
	app, ch := newAccountPickerApp(t, store)
	app.Update(createSessionMsg{})
	app.Update(keyMsg("enter")) // accept cwd (directory stage, T10)
	app.Update(keyMsg("down"))  // opencode (sel 1)
	app.Update(keyMsg("enter"))
	if app.picker.open {
		t.Error("opencode selection should create immediately (no account step)")
	}
	p := waitParams(t, ch)
	if p.Backend != session.BackendOpenCode || p.AnthropicAccountID != "" {
		t.Errorf("opencode params = %+v, want {opencode-server, \"\"}", p)
	}
}

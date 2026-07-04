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

// openAccountStage drives `n` → enter(claude) so the app lands on the account
// stage. It asserts the transition happened.
func openAccountStage(t *testing.T, app *App) {
	t.Helper()
	app.Update(createSessionMsg{})
	if !app.picker.open || app.picker.stage != stageBackend {
		t.Fatalf("createSessionMsg did not open the backend picker: open=%v stage=%v", app.picker.open, app.picker.stage)
	}
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

// TestAccountPickerZeroAccountsSkips: with no stored accounts, picking claude
// skips the account step entirely and creates on the shared Secret (empty id),
// preserving today's UX.
func TestAccountPickerZeroAccountsSkips(t *testing.T) {
	app, ch := newAccountPickerApp(t, &fakeAccountStore{accounts: nil})

	app.Update(createSessionMsg{})
	_, cmd := app.Update(keyMsg("enter")) // claude
	if app.picker.open {
		t.Error("zero accounts: picker should have closed and created immediately")
	}
	if app.screen != ScreenConnecting {
		t.Errorf("zero accounts: screen = %v, want ScreenConnecting", app.screen)
	}
	if cmd == nil {
		t.Fatal("zero accounts: enter returned no create command")
	}
	p := waitParams(t, ch)
	if p.Backend != session.BackendClaudeSDK || p.AnthropicAccountID != "" {
		t.Errorf("zero accounts: params = %+v, want {claude-sdk, \"\"}", p)
	}
}

// TestAccountPickerEnterThreadsAccountID: enter on a stored account threads its
// id to the Creator.
func TestAccountPickerEnterThreadsAccountID(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{
		{ID: "acct-aaaa", Label: "personal", Type: "subscription", Default: true},
		{ID: "acct-bbbb", Label: "work", Type: "console"},
	}}
	app, ch := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	// Move to the second account and confirm.
	app.Update(keyMsg("down"))
	if app.picker.sel != 1 {
		t.Fatalf("down did not move account selection: sel=%d", app.picker.sel)
	}
	app.Update(keyMsg("enter"))
	if app.picker.open {
		t.Error("enter on account did not close the picker")
	}
	if app.screen != ScreenConnecting {
		t.Errorf("enter on account: screen = %v, want ScreenConnecting", app.screen)
	}
	p := waitParams(t, ch)
	if p.Backend != session.BackendClaudeSDK || p.AnthropicAccountID != "acct-bbbb" {
		t.Errorf("params = %+v, want {claude-sdk, acct-bbbb}", p)
	}
}

// TestAccountPickerClusterDefaultThreadsEmptyID: the explicit "cluster default"
// row (after the account rows) threads an empty account id (legacy shared Secret).
func TestAccountPickerClusterDefaultThreadsEmptyID(t *testing.T) {
	store := &fakeAccountStore{accounts: []AccountInfo{
		{ID: "acct-aaaa", Label: "personal", Type: "subscription"},
	}}
	app, ch := newAccountPickerApp(t, store)
	openAccountStage(t, app)

	// Rows: [acct(0), cluster-default(1), add-account(2)]. Select cluster default.
	app.Update(keyMsg("down"))
	if app.picker.sel != 1 {
		t.Fatalf("sel=%d, want 1 (cluster default)", app.picker.sel)
	}
	app.Update(keyMsg("enter"))
	p := waitParams(t, ch)
	if p.Backend != session.BackendClaudeSDK || p.AnthropicAccountID != "" {
		t.Errorf("cluster default: params = %+v, want {claude-sdk, \"\"}", p)
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

	// Rows: [acct(0), cluster-default(1), add-account(2)].
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
	if got := app.picker.accounts[app.picker.sel].ID; got != "acct-sub-new" {
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
	app.Update(keyMsg("down")) // opencode (sel 1)
	app.Update(keyMsg("enter"))
	if app.picker.open {
		t.Error("opencode selection should create immediately (no account step)")
	}
	p := waitParams(t, ch)
	if p.Backend != session.BackendOpenCode || p.AnthropicAccountID != "" {
		t.Errorf("opencode params = %+v, want {opencode-server, \"\"}", p)
	}
}

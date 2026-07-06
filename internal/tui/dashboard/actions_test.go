package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// --------------------------------------------------------------------------
// Test fakes
// --------------------------------------------------------------------------

// fakeBackend records the session actions it receives so tests can assert that
// a keystroke dispatched the right cluster call. It satisfies dashboard.Backend.
type fakeBackend struct {
	suspended []session.ID
	resumed   []session.ID
	destroyed []session.ID
	actionErr error
	listErr   error // if set, List fails with it (drives the seed-failure path)
}

func (f *fakeBackend) List(context.Context) ([]session.State, error) { return nil, f.listErr }
func (f *fakeBackend) Watch(context.Context) (<-chan k8s.StateEvent, error) {
	return nil, errors.New("no watch in tests")
}
func (f *fakeBackend) Suspend(_ context.Context, ref session.Ref) error {
	f.suspended = append(f.suspended, ref.ID)
	return f.actionErr
}
func (f *fakeBackend) Resume(_ context.Context, ref session.Ref) error {
	f.resumed = append(f.resumed, ref.ID)
	return f.actionErr
}
func (f *fakeBackend) Destroy(_ context.Context, ref session.Ref) error {
	f.destroyed = append(f.destroyed, ref.ID)
	return f.actionErr
}

// fakeRunnerClient is a no-op RunnerClient that records StartTurn prompts and
// hands out an event channel that never emits. It satisfies dashboard.RunnerClient.
type fakeRunnerClient struct {
	events         chan session.Event
	startedPrompts []string
	startedModes   []string
	startedModels  []string
	startedEfforts []string
	startedAdvisor []bool
	resolved       []session.PermissionDecision
	interrupts     int               // count of InterruptTurn calls
	interruptRefs  []session.TurnRef // the TurnRef each InterruptTurn was called with
	execCommands   []string
	execResult     *session.ExecResult
	startErr       error // if set, StartTurn fails with it (still records the prompt)
	eventsErr      error // if set, Events fails to open the stream
	resolveErr     error // if set, ResolvePermission fails with it (still records the decision)
	passiveStreams int   // count of EventsPassive calls (RV6 background-stream path)
}

func (f *fakeRunnerClient) Health(context.Context) error { return nil }
func (f *fakeRunnerClient) StartTurn(_ context.Context, ref session.Ref, in session.TurnInput) (session.TurnRef, error) {
	f.startedPrompts = append(f.startedPrompts, in.Prompt)
	f.startedModes = append(f.startedModes, in.Mode)
	f.startedModels = append(f.startedModels, in.Model)
	f.startedEfforts = append(f.startedEfforts, in.Effort)
	f.startedAdvisor = append(f.startedAdvisor, in.Advisor)
	if f.startErr != nil {
		return session.TurnRef{}, f.startErr
	}
	return session.TurnRef{Session: ref.ID}, nil
}
func (f *fakeRunnerClient) InterruptTurn(_ context.Context, _ session.Ref, turn session.TurnRef) error {
	f.interrupts++
	f.interruptRefs = append(f.interruptRefs, turn)
	return nil
}
func (f *fakeRunnerClient) ResolvePermission(_ context.Context, _ session.Ref, d session.PermissionDecision) error {
	f.resolved = append(f.resolved, d)
	return f.resolveErr
}
func (f *fakeRunnerClient) Events(context.Context, session.Ref, uint64) (<-chan session.Event, error) {
	if f.eventsErr != nil {
		return nil, f.eventsErr
	}
	if f.events == nil {
		f.events = make(chan session.Event)
	}
	return f.events, nil
}
func (f *fakeRunnerClient) EventsPassive(ctx context.Context, ref session.Ref, after uint64) (<-chan session.Event, error) {
	f.passiveStreams++
	return f.Events(ctx, ref, after)
}
func (f *fakeRunnerClient) SessionState(context.Context, session.Ref) (session.State, error) {
	return session.State{}, nil
}
func (f *fakeRunnerClient) Exec(_ context.Context, _ session.Ref, command string) (session.ExecResult, error) {
	f.execCommands = append(f.execCommands, command)
	if f.execResult != nil {
		return *f.execResult, nil
	}
	return session.ExecResult{Stdout: "ok\n", ExitCode: 0}, nil
}

func (f *fakeRunnerClient) Idle(context.Context, session.Ref) (session.IdleStatus, error) {
	return session.IdleStatus{}, nil
}

// keyMsg builds a KeyPressMsg whose String() matches the dashboard keymap.
func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
}

// --------------------------------------------------------------------------
// Session action dispatch
// --------------------------------------------------------------------------

func TestSuspendActionDispatch(t *testing.T) {
	fb := &fakeBackend{}
	m := New(fb)
	m.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}}

	_, cmd := m.handleKey(keyMsg("x"))
	if cmd == nil {
		t.Fatal("x produced no command")
	}
	res, ok := cmd().(actionResultMsg)
	if !ok {
		t.Fatalf("want actionResultMsg, got %T", cmd())
	}
	if res.action != "suspend" || res.id != "s1" {
		t.Errorf("unexpected result: %+v", res)
	}
	if len(fb.suspended) != 1 || fb.suspended[0] != "s1" {
		t.Errorf("Suspend not dispatched: %v", fb.suspended)
	}
}

func TestResumeActionDispatch(t *testing.T) {
	fb := &fakeBackend{}
	m := New(fb)
	m.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusSuspended},
		sessionReadModel: sessionReadModel{DashStatus: StatusSuspended},
	}}

	_, cmd := m.handleKey(keyMsg("r"))
	if cmd == nil {
		t.Fatal("r produced no command")
	}
	if res, ok := cmd().(actionResultMsg); !ok || res.action != "resume" {
		t.Fatalf("want resume actionResultMsg, got %T %+v", cmd(), cmd())
	}
	if len(fb.resumed) != 1 || fb.resumed[0] != "s1" {
		t.Errorf("Resume not dispatched: %v", fb.resumed)
	}
}

func TestSuspendSkippedWhenAlreadySuspended(t *testing.T) {
	fb := &fakeBackend{}
	m := New(fb)
	m.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusSuspended},
		sessionReadModel: sessionReadModel{DashStatus: StatusSuspended},
	}}

	_, cmd := m.handleKey(keyMsg("x"))
	if cmd != nil {
		t.Error("x should be a no-op on an already-suspended session")
	}
	if len(fb.suspended) != 0 {
		t.Errorf("Suspend dispatched on suspended session: %v", fb.suspended)
	}
}

func TestNewSessionEmitsCreateMsg(t *testing.T) {
	m := New(&fakeBackend{})
	m.sessions = []Session{{
		State:            session.State{ID: "s1"},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}}

	_, cmd := m.handleKey(keyMsg("n"))
	if cmd == nil {
		t.Fatal("n produced no command")
	}
	if _, ok := cmd().(createSessionMsg); !ok {
		t.Fatalf("want createSessionMsg, got %T", cmd())
	}
}

// --------------------------------------------------------------------------
// Destroy confirm state machine
// --------------------------------------------------------------------------

func TestConfirmGatesDestroy(t *testing.T) {
	fb := &fakeBackend{}
	m := New(fb)
	m.sessions = []Session{{
		State:            session.State{ID: "s1"},
		Title:            "proj",
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}}

	// ! opens the confirm dialog but does NOT destroy yet.
	if _, cmd := m.handleKey(keyMsg("!")); cmd != nil {
		t.Error("! should not dispatch destroy immediately")
	}
	if m.confirm == nil {
		t.Fatal("! did not open the confirm dialog")
	}
	if len(fb.destroyed) != 0 {
		t.Fatalf("destroy fired before confirmation: %v", fb.destroyed)
	}

	// n cancels: confirm cleared, nothing destroyed.
	m.handleKey(keyMsg("n"))
	if m.confirm != nil {
		t.Error("n did not cancel the confirm dialog")
	}
	if len(fb.destroyed) != 0 {
		t.Fatalf("destroy fired after cancel: %v", fb.destroyed)
	}

	// Re-open and confirm with y: destroy dispatched.
	m.handleKey(keyMsg("!"))
	_, cmd := m.handleKey(keyMsg("y"))
	if m.confirm != nil {
		t.Error("y did not clear the confirm dialog")
	}
	if cmd == nil {
		t.Fatal("y did not return the destroy command")
	}
	if res, ok := cmd().(actionResultMsg); !ok || res.action != "destroy" {
		t.Fatalf("want destroy actionResultMsg, got %T %+v", cmd(), cmd())
	}
	if len(fb.destroyed) != 1 || fb.destroyed[0] != "s1" {
		t.Errorf("Destroy not dispatched: %v", fb.destroyed)
	}
}

func TestActionErrorSurfaced(t *testing.T) {
	m := New(&fakeBackend{})
	next, _ := m.Update(actionResultMsg{action: "suspend", id: "s1", err: errors.New("boom")})
	dm := next.(*Model)
	if dm.actionErr == nil {
		t.Fatal("actionErr not set on failed action")
	}
	// A subsequent success clears it.
	next, _ = dm.Update(actionResultMsg{action: "resume", id: "s1"})
	if next.(*Model).actionErr != nil {
		t.Error("actionErr not cleared on successful action")
	}
}

// --------------------------------------------------------------------------
// PendingAction optimism (U3)
// --------------------------------------------------------------------------

func TestSuspendSetsPendingAction(t *testing.T) {
	m := New(&fakeBackend{})
	m.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}}

	m.handleKey(keyMsg("x"))
	if m.sessions[0].PendingAction != "suspend" {
		t.Errorf("PendingAction = %q, want suspend", m.sessions[0].PendingAction)
	}
}

func TestResumeSetsPendingAction(t *testing.T) {
	m := New(&fakeBackend{})
	m.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusSuspended},
		sessionReadModel: sessionReadModel{DashStatus: StatusSuspended},
	}}

	m.handleKey(keyMsg("r"))
	if m.sessions[0].PendingAction != "resume" {
		t.Errorf("PendingAction = %q, want resume", m.sessions[0].PendingAction)
	}
}

func TestDestroyConfirmSetsPendingAction(t *testing.T) {
	m := New(&fakeBackend{})
	m.sessions = []Session{{
		State:            session.State{ID: "s1"},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}}

	m.handleKey(keyMsg("!"))
	m.handleKey(keyMsg("y"))
	if m.sessions[0].PendingAction != "destroy" {
		t.Errorf("PendingAction = %q, want destroy", m.sessions[0].PendingAction)
	}
}

func TestDestroyCancelClearsPendingAction(t *testing.T) {
	m := New(&fakeBackend{})
	m.sessions = []Session{{
		State:            session.State{ID: "s1"},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}}

	m.handleKey(keyMsg("!"))
	// Manually set PendingAction to simulate an earlier state (should be cleared on cancel)
	m.sessions[0].PendingAction = "should-clear"
	m.handleKey(keyMsg("n"))
	if m.sessions[0].PendingAction != "" {
		t.Errorf("PendingAction = %q, want empty after cancel", m.sessions[0].PendingAction)
	}
}

func TestActionResultMsgClearsPendingAction(t *testing.T) {
	m := New(&fakeBackend{})
	m.sessions = []Session{{
		State:            session.State{ID: "s1"},
		PendingAction:    "suspend",
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}}

	m.Update(actionResultMsg{action: "suspend", id: "s1"})
	if m.sessions[0].PendingAction != "" {
		t.Errorf("PendingAction = %q, want empty after actionResultMsg", m.sessions[0].PendingAction)
	}
}

// --------------------------------------------------------------------------
// App screen-switch: attach -> size -> detach
// --------------------------------------------------------------------------

func TestAppAttachSizeDetach(t *testing.T) {
	app := NewApp(nil, nil, nil)

	// Initial terminal size lands on the dashboard.
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	sess := Session{
		State:            session.State{ID: "s1", ProjectPath: "/x/proj", Backend: "claude-sdk"},
		Title:            "proj",
		sessionReadModel: sessionReadModel{DashStatus: StatusNeedsInput},
	}
	// attachReadyMsg drives the transition without a real connector.
	_, cmd := app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}})
	if app.screen != ScreenTranscript {
		t.Fatalf("screen = %v, want ScreenTranscript", app.screen)
	}
	if app.transcript == nil {
		t.Fatal("transcript model nil after attach")
	}
	// The attach must return a command (it batches Init with the size-seeding
	// WindowSizeMsg) — not nil.
	if cmd == nil {
		t.Error("attach returned no command (transcript would never paint or stream)")
	}

	// Sizing the App propagates to the transcript so it paints at real size.
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if app.transcript.width != 100 || app.transcript.height != 40 {
		t.Errorf("transcript not sized: %dx%d", app.transcript.width, app.transcript.height)
	}

	// esc detaches back to the dashboard, releasing the transcript.
	app.Update(keyMsg("esc"))
	if app.screen != ScreenDashboard {
		t.Errorf("esc did not detach: screen = %v", app.screen)
	}
	if app.transcript != nil {
		t.Error("transcript not released on detach")
	}
}

func TestAppAttachOpencodeExternalPane(t *testing.T) {
	app := NewApp(nil, nil, nil)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	sess := Session{
		State: session.State{ID: "oc1", ProjectPath: "/x/proj", Backend: session.BackendOpenCode},
		Title: "proj",
	}
	creds := &OpencodeCreds{Username: "opencode", Password: "secret", URL: "http://127.0.0.1:5000"}

	// An opencode-server attach must hand off to the external pane, NOT build a
	// Go transcript.
	_, cmd := app.Update(attachReadyMsg{sess: sess, client: &fakeRunnerClient{}, opencodeCreds: creds})
	if app.screen != ScreenExternal {
		t.Fatalf("screen = %v, want ScreenExternal", app.screen)
	}
	if app.external == nil {
		t.Fatal("external pane nil after opencode attach")
	}
	if app.transcript != nil {
		t.Error("transcript should not be built for an opencode-server session")
	}
	if cmd == nil {
		t.Error("attach returned no command (the external client would never launch)")
	}
	if app.external.creds.URL != creds.URL || app.external.creds.Password != creds.Password {
		t.Errorf("external pane got wrong creds: %+v", app.external.creds)
	}
	if app.external.w != 100 || app.external.h != 40 {
		t.Errorf("external pane not sized: %dx%d", app.external.w, app.external.h)
	}

	// The child exiting cleanly returns to the dashboard and releases the pane.
	app.Update(externalPaneFinishedMsg{})
	if app.screen != ScreenDashboard {
		t.Errorf("clean external exit: screen = %v, want ScreenDashboard", app.screen)
	}
	if app.external != nil {
		t.Error("external pane not released after exit")
	}
}

// TestAppExternalPaneEscIsForwardedNotDetached guards the triage fix:
// esc must NOT detach from the opencode external pane — the embedded opencode
// TUI uses esc to dismiss its own overlays/escape input mode, so the App must
// let it pass through to the child. Only ctrl+] / ctrl+4 detach.
func TestAppExternalPaneEscIsForwardedNotDetached(t *testing.T) {
	app := NewApp(nil, nil, nil)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	sess := Session{
		State: session.State{ID: "oc1", Backend: session.BackendOpenCode},
		Title: "proj",
	}
	app.Update(attachReadyMsg{
		sess:          sess,
		client:        &fakeRunnerClient{},
		opencodeCreds: &OpencodeCreds{Username: "opencode", Password: "secret", URL: "http://127.0.0.1:5000"},
	})
	if app.screen != ScreenExternal {
		t.Fatalf("screen = %v, want ScreenExternal", app.screen)
	}

	// esc must keep the user inside the opencode pane (forwarded to the child).
	app.Update(keyMsg("esc"))
	if app.screen != ScreenExternal {
		t.Errorf("esc detached the external pane (screen=%v) — opencode needs esc for its own overlays", app.screen)
	}
	if app.external == nil {
		t.Error("external pane was torn down on esc; expected to stay live for re-attach")
	}

	// ctrl+] is the explicit detach chord for the external pane.
	app.Update(keyMsg("ctrl+]"))
	if app.screen != ScreenDashboard {
		t.Errorf("ctrl+] did not detach the external pane: screen = %v, want ScreenDashboard", app.screen)
	}

	// Re-attaching minimizes a still-live pane instantly (no connector run);
	// esc must still pass through, not detach, after the restore.
	app.Update(attachMsg{sess: sess})
	if app.screen != ScreenExternal {
		t.Fatalf("restore: screen = %v, want ScreenExternal", app.screen)
	}
	app.Update(keyMsg("esc"))
	if app.screen != ScreenExternal {
		t.Errorf("esc detached the restored external pane (screen=%v); should forward to opencode", app.screen)
	}
}

func TestAppExternalPaneErrorSurfaces(t *testing.T) {
	app := NewApp(nil, nil, nil)
	app.screen = ScreenExternal
	app.external = NewExternalPane(Session{Title: "proj"}, OpencodeCreds{}, nil)

	app.Update(externalPaneFinishedMsg{err: errors.New("opencode attach: boom")})
	if app.screen != ScreenDashboard {
		t.Errorf("error exit: screen = %v, want ScreenDashboard", app.screen)
	}
	if app.connectErr == nil {
		t.Error("external pane error not surfaced on the App")
	}
	if app.dashboard.connectErr == nil {
		t.Error("external pane error not surfaced in the detail pane")
	}
}

func TestBackendPickerSelectsBackend(t *testing.T) {
	backendCh := make(chan string, 1)
	creator := func(ctx context.Context, params CreateParams, _ func(ConnectStage, string)) (CreateResult, error) {
		backendCh <- params.Backend
		return CreateResult{
			State:  session.State{ID: "new1", Backend: params.Backend},
			Client: &fakeRunnerClient{},
		}, nil
	}
	app := NewApp(nil, nil, creator)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	// `n` reaches the App as createSessionMsg → opens the picker over the dash.
	app.Update(createSessionMsg{})
	if !app.picker.open {
		t.Fatal("createSessionMsg did not open the backend picker")
	}
	if app.screen != ScreenDashboard {
		t.Errorf("picker should overlay the dashboard, screen = %v", app.screen)
	}

	// Move to the second choice (opencode-server) and confirm.
	app.Update(keyMsg("down"))
	if app.picker.sel != 1 {
		t.Fatalf("down did not move selection: sel = %d", app.picker.sel)
	}
	_, cmd := app.Update(keyMsg("enter"))
	if app.picker.open {
		t.Error("enter did not close the picker")
	}
	if app.screen != ScreenConnecting {
		t.Errorf("enter did not start connecting: screen = %v", app.screen)
	}
	if cmd == nil {
		t.Fatal("enter returned no create command")
	}
	// createCmd now starts a goroutine; wait for the creator to be called.
	select {
	case gotBackend := <-backendCh:
		if gotBackend != session.BackendOpenCode {
			t.Errorf("Creator got backend %q, want %q", gotBackend, session.BackendOpenCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Creator was not called within 5 seconds")
	}
}

func TestBackendPickerEscCancels(t *testing.T) {
	called := false
	creator := func(ctx context.Context, _ CreateParams, _ func(ConnectStage, string)) (CreateResult, error) {
		called = true
		return CreateResult{}, nil
	}
	app := NewApp(nil, nil, creator)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	app.Update(createSessionMsg{})
	if !app.picker.open {
		t.Fatal("picker not open")
	}
	app.Update(keyMsg("esc"))
	if app.picker.open {
		t.Error("esc did not close the picker")
	}
	if app.screen != ScreenDashboard {
		t.Errorf("esc left screen = %v, want ScreenDashboard", app.screen)
	}
	if called {
		t.Error("esc must not provision a session")
	}
}

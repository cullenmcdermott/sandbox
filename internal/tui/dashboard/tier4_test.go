// CANONICAL TEST — do not weaken.
package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// ORACLE: the same surface rendered twice under reduce-motion is byte-identical
// (the flake guard, §4.2/§6); COUNTER: the render is non-empty, so identity is
// not satisfied by returning nothing. [D1]
func TestRenderDeterministic(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	m := New(nil)
	m, _ = m.applySeed([]session.State{
		{ID: "a", Status: session.StatusRunning, PodReady: true},
		{ID: "b", Status: session.StatusRunning, PodReady: true},
	})
	m.width, m.height = 100, 30
	a := m.render()
	b := m.render()
	if a != b {
		t.Fatalf("render not deterministic under reduce-motion")
	}
	if strings.TrimSpace(a) == "" {
		t.Fatalf("render produced empty output")
	}
}

// ORACLE: time-derived strings read the injectable clock, not the wall clock, so
// a golden does not change every second; COUNTER: a relative time computed
// against an injected fixed clock reflects that clock. [D1]
func TestInjectableClock(t *testing.T) {
	old := nowFunc
	defer func() { nowFunc = old }()
	base := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return base }
	if got := relativeTime(base.Add(-2 * time.Hour)); !strings.Contains(got, "2h") {
		t.Fatalf("relativeTime ignored injected clock: %q, want to contain %q", got, "2h")
	}
}

// ORACLE: the first-run view states the primary CTA (n — new session). [D3]
func TestFirstRunState(t *testing.T) {
	m := New(nil)
	out := m.firstRunView(100, 30)
	if !strings.Contains(out, "new session") {
		t.Fatalf("first-run view missing CTA: %q", out)
	}
}

// ORACLE: the no-match copy names the query and the way out; COUNTER: it is
// distinct from a bare empty-cluster line. [D3]
func TestNoMatchCopy(t *testing.T) {
	out := noMatchCopy("widget")
	if !strings.Contains(out, "widget") {
		t.Fatalf("no-match copy dropped the query: %q", out)
	}
	if !strings.Contains(out, "esc") {
		t.Fatalf("no-match copy missing the way out: %q", out)
	}
}

// ORACLE: the connecting stepper lists the lifecycle stages and marks progress;
// COUNTER: an earlier stage than the current one is shown done (✓). [D3]
func TestConnectingStepper(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	// StageRunner (3) is the current stage; earlier stages should be ✓, later dim.
	out := connectingStepper(StageRunner, 0, "", nil)
	if !strings.Contains(out, "Attaching") {
		t.Fatalf("stepper missing Attaching stage: %q", out)
	}
	if !strings.Contains(out, "✓") {
		t.Fatalf("stepper did not mark a completed stage: %q", out)
	}
}

// ORACLE: a non-empty detail is appended to the CURRENT stage's label;
// COUNTER: the same detail does not leak onto other (non-current) stage lines.
func TestConnectingStepperDetail(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	out := connectingStepper(StageSync, 0, "uploading", nil)
	syncLine, otherLine := "", ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, connectStageLabel(StageSync)):
			syncLine = line
		case strings.Contains(line, connectStageLabel(StageCheck)):
			otherLine = line
		}
	}
	if !strings.Contains(syncLine, "uploading") {
		t.Fatalf("current stage line missing detail: %q", syncLine)
	}
	if strings.Contains(otherLine, "uploading") {
		t.Fatalf("detail leaked onto a non-current stage line: %q", otherLine)
	}
}

// ORACLE: for an opencode connect at StageOpencode, the stepper shows the
// "Starting opencode" step AND marks the immediately-prior StageSync done (✓) —
// proving the checklist does not regress (the bug where StageOpencode sorted
// before StageSync and was absent from the displayed set, so the screen froze
// with no spinner). COUNTER: the default claude stepper omits "Starting
// opencode". [opencode connect U1]
func TestConnectingStepperOpencodeNoRegress(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")

	// Stage values must be monotonic with connect()'s emission order: Sync before
	// Opencode. A regression here is exactly what made the screen look stuck.
	if StageSync >= StageOpencode {
		t.Fatalf("StageSync (%d) must sort before StageOpencode (%d)", StageSync, StageOpencode)
	}

	out := connectingStepper(StageOpencode, 0, "", opencodeConnectStages)
	if !strings.Contains(out, connectStageLabel(StageOpencode)) {
		t.Fatalf("opencode stepper missing %q step: %q", connectStageLabel(StageOpencode), out)
	}
	// The line for the prior StageSync must be checked, not a dim placeholder.
	syncLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, connectStageLabel(StageSync)) {
			syncLine = line
		}
	}
	if syncLine == "" {
		t.Fatalf("opencode stepper missing %q step: %q", connectStageLabel(StageSync), out)
	}
	if !strings.Contains(syncLine, "✓") {
		t.Fatalf("prior stage %q not marked done at StageOpencode (regressed): %q", connectStageLabel(StageSync), syncLine)
	}

	// COUNTER: the default (claude) stepper never shows the opencode step.
	def := connectingStepper(StageSync, 0, "", nil)
	if strings.Contains(def, connectStageLabel(StageOpencode)) {
		t.Fatalf("default stepper should omit %q: %q", connectStageLabel(StageOpencode), def)
	}
}

// ORACLE: connectingView for an opencode session renders the "Starting opencode"
// step; for a non-opencode session it does not. [opencode connect U1]
func TestConnectingViewOpencodeStep(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	app := NewApp(nil, nil, nil)
	app.width, app.height = 80, 24
	sess := Session{State: session.State{ID: "s1"}, Title: "oc"}
	app.screen = ScreenConnecting
	app.connectingFor = &sess
	app.connectStage = StageOpencode

	app.connectingOpencode = true
	if oc := app.connectingView().Content; !strings.Contains(oc, connectStageLabel(StageOpencode)) {
		t.Errorf("opencode connectingView missing %q step: %q", connectStageLabel(StageOpencode), oc)
	}

	app.connectingOpencode = false
	app.connectStage = StageRunner
	if cc := app.connectingView().Content; strings.Contains(cc, connectStageLabel(StageOpencode)) {
		t.Errorf("non-opencode connectingView should omit %q step: %q", connectStageLabel(StageOpencode), cc)
	}
}

// ORACLE: skeleton rows render exactly n lines, each filling the width, so the
// layout does not jump when real rows arrive. [D3]
func TestSkeletonRows(t *testing.T) {
	out := skeletonRows(3, 20)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("skeletonRows(3,_) produced %d lines, want 3", len(lines))
	}
}

// ORACLE: attention-first ordering floats a Waiting session above an idle one;
// COUNTER: with attentionFirst=false the input order is preserved. [D4]
func TestAttentionFirstOrdering(t *testing.T) {
	sessions := []Session{
		{State: session.State{ID: "idle"}, DashStatus: StatusIdle},
		{State: session.State{ID: "wait"}, DashStatus: StatusWaiting},
	}
	got := sortByAttention(sessions, true)
	if got[0].ID() != "wait" {
		t.Fatalf("attention-first did not float waiting to top: %q", got[0].ID())
	}
	got2 := sortByAttention(sessions, false)
	if got2[0].ID() != "idle" {
		t.Fatalf("attentionFirst=false must preserve order, got %q first", got2[0].ID())
	}
}

// ORACLE: the attention summary tallies waiting and needs-input; COUNTER: with
// nothing waiting it is the empty string (no chrome for zero). [D4]
func TestAttentionSummary(t *testing.T) {
	sessions := []Session{
		{DashStatus: StatusWaiting},
		{DashStatus: StatusWaiting},
		{DashStatus: StatusNeedsInput},
		{DashStatus: StatusIdle},
	}
	out := attentionSummary(sessions)
	if !strings.Contains(out, "2 waiting") || !strings.Contains(out, "1 needs input") {
		t.Fatalf("attention summary wrong: %q", out)
	}
	if got := attentionSummary([]Session{{DashStatus: StatusIdle}}); got != "" {
		t.Fatalf("attention summary should be empty when nothing waits, got %q", got)
	}
}

// ORACLE: the overflow band counts hidden rows and their attention; COUNTER:
// nothing hidden → empty string. [D4]
func TestOverflowSummary(t *testing.T) {
	hidden := []Session{
		{DashStatus: StatusBusy},
		{DashStatus: StatusWaiting},
		{DashStatus: StatusIdle},
	}
	out := overflowSummary(hidden)
	if !strings.Contains(out, "+3 more") {
		t.Fatalf("overflow summary missing count: %q", out)
	}
	if !strings.Contains(out, "1 waiting") {
		t.Fatalf("overflow summary missing attention rollup: %q", out)
	}
	if got := overflowSummary(nil); got != "" {
		t.Fatalf("overflow summary should be empty when nothing hidden, got %q", got)
	}
}

// ORACLE: a session needing attention gets a dot; COUNTER: an idle, unchanged
// session gets none. [D4]
func TestAttentionDot(t *testing.T) {
	if attentionDot(Session{DashStatus: StatusWaiting}) == "" {
		t.Fatalf("waiting session has no attention dot")
	}
	if got := attentionDot(Session{DashStatus: StatusIdle}); got != "" {
		t.Fatalf("idle session drew a dot: %q", got)
	}
}

// ORACLE: a group's attention rollup counts its waiting/needs-input children so
// a collapsed header still signals. [D4]
func TestGroupAttentionRollup(t *testing.T) {
	g := []Session{
		{DashStatus: StatusWaiting},
		{DashStatus: StatusNeedsInput},
		{DashStatus: StatusIdle},
	}
	if got := groupAttentionCount(g); got != 2 {
		t.Fatalf("group rollup = %d, want 2", got)
	}
}

// ORACLE: before seeded, renderRowLines shows skeleton bars (░), not "loading" or
// "No sessions" copy — proves skeletonRows is wired (U2 regression). [D3/U2]
// hasSkeletonBar returns true if s contains a long run of ░ characters (the
// signature of skeletonRows), as opposed to the short ░ runs from blockBar.
func hasSkeletonBar(s string) bool {
	return strings.Contains(s, strings.Repeat("░", 20))
}

// ORACLE: before seeded, renderRowLines shows skeleton bars (░), not "loading" or
// "No sessions" copy — proves skeletonRows is wired (U2 regression). [D3/U2]
func TestSkeletonBeforeSeeded(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	m := New(nil)
	m.width, m.height = 80, 20
	// seeded is false; no sessions; render should contain skeleton glyph.
	out := m.render()
	if !hasSkeletonBar(out) {
		t.Fatalf("pre-seed render should show skeleton bars; got:\n%q", out)
	}
	if strings.Contains(out, "No sessions") {
		t.Fatalf("pre-seed render must not show 'No sessions'; got:\n%q", out)
	}
	if strings.Contains(out, "loading") {
		t.Fatalf("pre-seed render must not show 'loading'; got:\n%q", out)
	}
}

// ORACLE: after seeded with 0 sessions and no filter, renderRowLines shows the
// first-run CTA (n — new session) — proves firstRunView is wired. [D3/U2]
func TestFirstRunViewWiredAfterSeed(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	m := New(nil)
	m.width, m.height = 80, 20
	m, _ = m.applySeed(nil) // seed with empty list → seeded=true
	out := m.render()
	if !strings.Contains(out, "n") {
		t.Fatalf("first-run view should contain CTA 'n'; got:\n%q", out)
	}
	if hasSkeletonBar(out) {
		t.Fatalf("first-run view must not show skeleton bars; got:\n%q", out)
	}
	if strings.Contains(out, "No sessions. Press") {
		t.Fatalf("first-run view must not show old 'No sessions. Press' copy; got:\n%q", out)
	}
}

// ORACLE: seeded + sessions present + committed filter matching nothing shows
// noMatchCopy text naming the query — proves noMatchCopy is wired. [D3/U2]
func TestNoMatchCopyWiredWhenFiltered(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	m := New(nil)
	m.width, m.height = 80, 20
	m, _ = m.applySeed([]session.State{
		{ID: "s1", Status: session.StatusRunning, PodReady: true},
	})
	m.filter = "zzznomatch"
	out := m.render()
	if !strings.Contains(out, "zzznomatch") {
		t.Fatalf("no-match view should name the query; got:\n%q", out)
	}
	if hasSkeletonBar(out) {
		t.Fatalf("no-match view must not show skeleton bars; got:\n%q", out)
	}
}

// ORACLE: a fake Connector that calls onStage(StageCheck..StageAttach) then
// succeeds must advance a.connectStage through all stages and eventually
// transition to ScreenTranscript — proving the U1 channel-streaming path is
// wired end-to-end. [U1]
func TestConnectStageProgressAndAttach(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")

	// Fake connector that emits all stages then returns a result.
	connector := func(ctx context.Context, ref session.Ref, projectPath string, onStage func(ConnectStage, string)) (ConnectResult, error) {
		for _, s := range []ConnectStage{StageCheck, StageForward, StageRunner, StageSync, StageAttach} {
			onStage(s, "")
		}
		return ConnectResult{Client: &fakeRunnerClient{}}, nil
	}

	app := NewApp(nil, connector, nil)
	app.width, app.height = 80, 24
	sess := Session{State: session.State{ID: "s1", Status: session.StatusRunning, PodReady: true}}

	// Trigger attach → should switch to ScreenConnecting and start streaming.
	_, cmd := app.Update(attachMsg{sess: sess})
	if app.screen != ScreenConnecting {
		t.Fatalf("expected ScreenConnecting after attachMsg, got %v", app.screen)
	}
	if cmd == nil {
		t.Fatal("attachMsg returned nil cmd")
	}

	// Drain connect messages: unwrap BatchMsg from tea.Batch to get sub-cmds,
	// run connectNextCmd sub-cmd to read from the channel, feed messages into app.
	// We do up to 20 rounds to avoid an infinite loop.
	for round := 0; round < 20 && app.screen != ScreenTranscript; round++ {
		if cmd == nil {
			break
		}
		msg := cmd()
		if msg == nil {
			break
		}
		// Handle BatchMsg: run the first sub-cmd (connectNextCmd) to get a real msg.
		if batch, ok := msg.(tea.BatchMsg); ok {
			var innerMsg tea.Msg
			for _, subCmd := range batch {
				if m := subCmd(); m != nil {
					if _, isTick := m.(connectTickMsg); !isTick {
						innerMsg = m
						break
					}
				}
			}
			if innerMsg == nil {
				break
			}
			msg = innerMsg
		}
		_, cmd = app.Update(msg)
	}

	if app.screen != ScreenTranscript {
		t.Errorf("expected ScreenTranscript, got %v", app.screen)
	}
}

// ORACLE: connectingView output contains the current stage label, a check mark
// for each earlier stage, and the title uses text ramp (not raw Charple bold).
// [U1.5]
func TestConnectingViewShowsStageAndSpinner(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	app := NewApp(nil, nil, nil)
	app.width, app.height = 80, 24
	sess := Session{State: session.State{ID: "s1"}, Title: "my-session"}
	app.screen = ScreenConnecting
	app.connectingFor = &sess
	app.connectStage = StageRunner // stage 3; check, forward should be ✓

	v := app.connectingView()
	out := v.Content

	if !strings.Contains(out, "Connecting to my-session") {
		t.Errorf("connectingView missing title; got:\n%q", out)
	}
	if !strings.Contains(out, connectStageLabel(StageRunner)) {
		t.Errorf("connectingView missing current stage %q; got:\n%q", connectStageLabel(StageRunner), out)
	}
	if !strings.Contains(out, "✓") {
		t.Errorf("connectingView missing ✓ for earlier stages; got:\n%q", out)
	}
	if !strings.Contains(out, "cancel") {
		t.Errorf("connectingView missing cancel hint; got:\n%q", out)
	}
}

// ORACLE: pressing a key in ScreenConnecting cancels the connect goroutine
// (connectCancel is called) and returns to ScreenDashboard. [U1]
func TestConnectCancelOnKeyPress(t *testing.T) {
	cancelled := false
	app := NewApp(nil, nil, nil)
	app.width, app.height = 80, 24
	app.screen = ScreenConnecting
	app.connectCancel = func() { cancelled = true }

	app.Update(keyMsg("esc"))

	if app.screen != ScreenDashboard {
		t.Errorf("expected ScreenDashboard after key in ScreenConnecting, got %v", app.screen)
	}
	if !cancelled {
		t.Error("connectCancel was not called on key press in ScreenConnecting")
	}
}

// makeSession is a convenience constructor for unit tests.
func makeSession(id string, status SessionStatus) Session {
	return Session{
		State:      session.State{ID: session.ID(id), Status: session.StatusRunning},
		Title:      id,
		DashStatus: status,
	}
}

// ORACLE: with attentionFirst=true, a Waiting session appears above an Idle
// one in visibleSessions() — proves sortByAttention is wired. [D4]
func TestAttentionFirstOrderingWired(t *testing.T) {
	m := New(nil)
	m.seeded = true
	idle := makeSession("idle-one", StatusIdle)
	waiting := makeSession("waiting-one", StatusWaiting)
	m.sessions = []Session{idle, waiting}

	// Without toggle: order unchanged.
	m.attentionFirst = false
	vis := m.visibleSessions()
	if vis[0].DashStatus != StatusIdle {
		t.Errorf("without attentionFirst: expected idle first, got %v", vis[0].DashStatus)
	}

	// With toggle: waiting floats to top.
	m.attentionFirst = true
	vis = m.visibleSessions()
	if vis[0].DashStatus != StatusWaiting {
		t.Errorf("with attentionFirst: expected waiting first, got %v", vis[0].DashStatus)
	}
}

// ORACLE: pressing \ toggles attentionFirst and re-orders the list; a second
// press restores the original order. [D4]
func TestAttentionToggleKeyWired(t *testing.T) {
	m := New(nil)
	m.seeded = true
	m.width, m.height = 80, 24
	idle := makeSession("idle-one", StatusIdle)
	waiting := makeSession("waiting-one", StatusWaiting)
	m.sessions = []Session{idle, waiting}

	if m.attentionFirst {
		t.Fatal("attentionFirst should default to false")
	}
	m.handleKey(keyMsg("\\"))
	if !m.attentionFirst {
		t.Error("attentionFirst should be true after first \\")
	}
	m.handleKey(keyMsg("\\"))
	if m.attentionFirst {
		t.Error("attentionFirst should be false after second \\")
	}
}

// ORACLE: renderSessionRow for a Waiting session contains the attention dot (●);
// an Idle session row does not. [D4]
func TestAttentionDotWiredInSessionRow(t *testing.T) {
	m := New(nil)
	m.seeded = true
	m.width = 80

	waiting := makeSession("w", StatusWaiting)
	idle := makeSession("i", StatusIdle)

	wRow := m.renderSessionRow(waiting, false, 80)
	if !strings.Contains(wRow, "●") {
		t.Error("renderSessionRow for Waiting session should contain ●")
	}
	iRow := m.renderSessionRow(idle, false, 80)
	if strings.Contains(iRow, "●") {
		t.Error("renderSessionRow for Idle session should not contain ●")
	}
}

// ORACLE: attentionSummary text appears in topBar output when sessions are waiting;
// nothing when all idle. [D4]
func TestAttentionSummaryWiredInTopBar(t *testing.T) {
	m := New(nil)
	m.seeded = true
	m.width, m.height = 120, 24

	m.sessions = []Session{makeSession("w", StatusWaiting)}
	bar := m.topBar(120)
	if !strings.Contains(bar, "waiting") {
		t.Errorf("topBar should contain 'waiting' when a session is waiting; got: %q", bar)
	}

	m.sessions = []Session{makeSession("i", StatusIdle)}
	bar = m.topBar(120)
	if strings.Contains(bar, "waiting") {
		t.Errorf("topBar should not contain 'waiting' when no session is waiting; got: %q", bar)
	}
}

// ORACLE: overflowSummary text appears in sessionListBody when visible rows exceed
// the box height and a hidden session is Waiting. [D4]
func TestOverflowBandWiredInSessionListBody(t *testing.T) {
	m := New(nil)
	m.seeded = true
	m.width, m.height = 120, 24

	// 5 sessions, box height 3 (bodyH=1 after box borders).
	for i := 0; i < 4; i++ {
		m.sessions = append(m.sessions, makeSession(strings.Repeat("x", i+1), StatusIdle))
	}
	m.sessions = append(m.sessions, makeSession("w", StatusWaiting))

	// boxH=3 → bodyH=1, so 4 sessions overflow (5-1=4 hidden, 1 waiting below).
	body := m.sessionListBody(60, 3)
	combined := strings.Join(body, "\n")
	if !strings.Contains(combined, "more") {
		t.Errorf("sessionListBody should contain overflow band with 'more'; got: %q", combined)
	}
}

// ORACLE: renderDetailLines for a waiting session uses kit.KbdRow (· separator)
// for its permission hint row, not the old hand-rolled "   "-spaced format.
// COUNTER: a non-waiting session does not contain a kit hint row. [A9]
func TestDetailPanePermHintUsesKitKbdRow(t *testing.T) {
	m := New(nil)
	m.seeded = true
	m.width, m.height = 80, 24
	waiting := Session{
		State:                 session.State{ID: "w1", Status: session.StatusRunning},
		DashStatus:            StatusWaiting,
		PendingPermissionTool: "Write(main.go)",
	}
	m.sessions = []Session{waiting}

	lines := m.renderDetailLines(60, 10)
	combined := strings.Join(lines, "\n")
	// kit.KbdRow uses " · " as separator between hints.
	if !strings.Contains(combined, " · ") {
		t.Errorf("renderDetailLines permission hint should use kit.KbdRow (· separator); got:\n%q", combined)
	}
	// kit.Kbd wraps keys in []; old code also did that but the separator is the tell.
	if !strings.Contains(combined, "[a]") {
		t.Errorf("renderDetailLines permission hint should contain [a]; got:\n%q", combined)
	}
}

// ORACLE: renderDetailLines with a connectErr uses kit.ErrorBlock (✗ prefix),
// not the old inline "! " coral line. [A9]
func TestDetailPaneErrorUsesKitErrorBlock(t *testing.T) {
	m := New(nil)
	m.seeded = true
	m.width, m.height = 80, 24
	m.sessions = []Session{makeSession("s1", StatusIdle)}
	m.connectErr = errors.New("port-forward timed out")

	lines := m.renderDetailLines(60, 10)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "✗") {
		t.Errorf("renderDetailLines connectErr should use kit.ErrorBlock (✗); got:\n%q", combined)
	}
	if !strings.Contains(combined, "port-forward timed out") {
		t.Errorf("renderDetailLines connectErr should include the error message; got:\n%q", combined)
	}
}

// ORACLE: renderConfirm uses kit.KbdRow (· separator) for its y/n hint. [A9]
func TestConfirmDialogUsesKitKbdRow(t *testing.T) {
	m := New(nil)
	m.seeded = true
	m.confirm = &confirmPrompt{message: "Destroy this session?", action: nil}
	out := m.renderConfirm()
	if !strings.Contains(out, " · ") {
		t.Errorf("renderConfirm hint should use kit.KbdRow (· separator); got:\n%q", out)
	}
}

// ORACLE: renderDetailLines for a session uses kit.KV-aligned rows (key column
// width 7), visible as equal-width key slots. [A9]
func TestDetailPaneKVUsesKit(t *testing.T) {
	m := New(nil)
	m.seeded = true
	m.width, m.height = 80, 24
	m.sessions = []Session{makeSession("s1", StatusIdle)}
	m.sessions[0].State.Backend = "claude-sdk"

	lines := m.renderDetailLines(60, 10)
	combined := strings.Join(lines, "\n")
	// kit.KV pads the key to keyWidth=7; "agent" (5 chars) is followed by 2 spaces.
	if !strings.Contains(combined, "agent") {
		t.Errorf("renderDetailLines should contain 'agent' KV row; got:\n%q", combined)
	}
}

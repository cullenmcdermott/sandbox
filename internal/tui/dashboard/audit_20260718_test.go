package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// V5: detaching an attached session must carry the transcript's derived read-model
// (DashStatus + the pending-permission descriptor) back onto the dashboard row
// before syncCursorFromTranscript advances the cursor past the events that produced
// it. Otherwise a permission requested while attached is invisible after detach —
// the reconnect resumes at after=lastSeq and the runner replays nothing for a
// blocked agent, so the row stays frozen at its attach-time state.
func TestParkCarriesPendingPermissionToDashboardRow(t *testing.T) {
	app := NewApp(nil, nil, nil)
	// The dashboard row is frozen Busy at attach time (its background stream was
	// cancelled by handleAttachReady) and carries no pending permission.
	app.dashboard.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
	}}

	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	// A permission is requested while attached: the transcript's shared read-model
	// derives StatusWaiting + the descriptor; the dashboard row saw none of it.
	m.handleEvent(mkEventSeq(131, session.EventPermissionRequested, session.PermissionPayload{
		PermissionID: "perm-1", Tool: "Bash", Input: json.RawMessage(`{"command":"rm -rf /tmp/x"}`),
	}))

	app.parkTranscript(m)

	got := app.dashboard.sessionByID("s1")
	if got.DashStatus != StatusWaiting {
		t.Fatalf("detach dropped the pending permission: DashStatus=%v, want Waiting", got.DashStatus)
	}
	if got.PendingPermissionID != "perm-1" || got.PendingPermissionTool != "Bash" {
		t.Fatalf("detach did not carry the pending-permission descriptor: id=%q tool=%q",
			got.PendingPermissionID, got.PendingPermissionTool)
	}
	// The cursor must also have advanced to the transcript position (§1a).
	if got.lastSeq != 131 {
		t.Fatalf("detach did not advance the resume cursor: lastSeq=%d, want 131", got.lastSeq)
	}
}

// V5 (completed-turn variant): a turn that completes while attached must leave the
// row NeedsInput after detach, not spinning Busy forever.
func TestParkCarriesCompletedTurnStatusToDashboardRow(t *testing.T) {
	app := NewApp(nil, nil, nil)
	app.dashboard.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
	}}

	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.turnActive = true
	m.handleEvent(mkEventSeq(50, session.EventTurnCompleted, nil))

	app.parkTranscript(m)

	got := app.dashboard.sessionByID("s1")
	if got.DashStatus != StatusNeedsInput {
		t.Fatalf("detach left the row %v after the turn completed during attach, want NeedsInput", got.DashStatus)
	}
}

// V5: a cluster-authoritative terminal status set by the watch mid-attach (suspend
// or fail) must not be overwritten by the transcript's now-stale runner-derived
// status on detach.
func TestParkPreservesClusterTerminalStatus(t *testing.T) {
	app := NewApp(nil, nil, nil)
	app.dashboard.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusSuspended},
		sessionReadModel: sessionReadModel{DashStatus: StatusSuspended},
	}}

	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.DashStatus = StatusBusy // stale: the transcript never saw the suspend

	app.parkTranscript(m)

	if got := app.dashboard.sessionByID("s1"); got.DashStatus != StatusSuspended {
		t.Fatalf("detach clobbered the cluster-authoritative Suspended with %v", got.DashStatus)
	}
}

// V22: /clear must nil the pinned todo pointer (which lives outside m.blocks) so a
// later todo.updated re-pins a fresh block instead of Bumping an orphan that no
// longer renders.
func TestSlashClearRepinsTodoChecklist(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()

	m.handleEvent(session.Event{Type: session.EventTodoUpdated, Payload: json.RawMessage(
		`{"todos":[{"content":"first","status":"in_progress","activeForm":"doing first"}]}`)})
	if m.todoBlock == nil {
		t.Fatal("precondition: first todo.updated must pin a block")
	}

	// /clear via the palette (the real user path).
	m.input.SetValue("/clear")
	if !m.paletteOpen() {
		t.Fatal("palette not open after SetValue('/clear')")
	}
	m.paletteKey(keyMsg("enter"))
	if m.todoBlock != nil {
		t.Fatal("/clear did not nil the orphaned todo block pointer")
	}

	// A later todo.updated must re-pin — the block must be back in m.blocks so it
	// renders again.
	m.handleEvent(session.Event{Type: session.EventTodoUpdated, Payload: json.RawMessage(
		`{"todos":[{"content":"second","status":"in_progress"}]}`)})
	if m.todoBlock == nil {
		t.Fatal("todo.updated after /clear did not re-pin the checklist")
	}
	var todoBlocks int
	for _, b := range m.blocks {
		if b.kind == blockTodos {
			todoBlocks++
		}
	}
	if todoBlocks != 1 {
		t.Fatalf("re-pinned todo block not in m.blocks: found %d blockTodos, want 1", todoBlocks)
	}
}

// V23(a): a REPLAYED historical turn.completed during cold re-attach catch-up must
// NOT auto-submit the queued prompt (the flush is gated on !m.replaying).
func TestReplayedTurnCompletedDoesNotFlushQueuedPrompt(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.events = make(chan session.Event) // foreground (owns the stream)
	m.replaying = true                  // catch-up in progress
	m.turnActive = true
	m.queuedPrompt = "do the thing"

	m.handleEvent(mkEventSeq(10, session.EventTurnCompleted, nil))

	if m.queuedPrompt != "do the thing" {
		t.Fatalf("a replayed turn.completed flushed the queued prompt (now %q) mid-catch-up", m.queuedPrompt)
	}
}

// V23(b): once the stream crosses the replay→live boundary with no turn active, a
// still-queued prompt IS released (the legitimate reconnect case).
func TestQueuedPromptFlushesAtLiveBoundary(t *testing.T) {
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.events = make(chan session.Event)
	m.replaying = true
	m.turnActive = false // caught up to idle
	m.queuedPrompt = "do the thing"

	cmd := m.handleEvent(session.Event{Type: session.EventStreamLive})

	if m.queuedPrompt != "" {
		t.Fatalf("live boundary did not release the queued prompt (still %q)", m.queuedPrompt)
	}
	if cmd == nil {
		t.Fatal("live-boundary flush produced no start-turn command")
	}
}

// V23(b'): a genuinely in-flight turn (turnActive survived catch-up) must NOT be
// 409ed by a boundary flush.
func TestQueuedPromptHeldAtLiveBoundaryWhileTurnActive(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.events = make(chan session.Event)
	m.replaying = true
	m.turnActive = true // a real turn is still running server-side
	m.queuedPrompt = "do the thing"

	m.handleEvent(session.Event{Type: session.EventStreamLive})

	if m.queuedPrompt != "do the thing" {
		t.Fatalf("boundary flush submitted into an in-flight turn (queued now %q)", m.queuedPrompt)
	}
}

// V23(c): a LIVE turn.completed still flushes the queued prompt immediately.
func TestLiveTurnCompletedFlushesQueuedPrompt(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.events = make(chan session.Event)
	m.replaying = false // live
	m.turnActive = true
	m.queuedPrompt = "do the thing"

	m.handleEvent(mkEventSeq(10, session.EventTurnCompleted, nil))

	if m.queuedPrompt != "" {
		t.Fatalf("a live turn.completed did not flush the queued prompt (still %q)", m.queuedPrompt)
	}
}

// V46: applySeed must not launch a background observer connect for the attached
// session — its transcript owns the live stream, so the connect would be torn down
// immediately by the liveSSEReadyMsg attachedID guard.
func TestApplySeedSkipsObserverForAttachedSession(t *testing.T) {
	m := New(nil)
	m.connector = failingConnector // non-nil so a launch would be attempted
	m.attachedID = "s1"

	_, cmds := m.applySeed([]session.State{{ID: "s1", Status: session.StatusRunning}})

	if len(cmds) != 0 {
		t.Fatalf("applySeed launched %d observer connects for the attached session, want 0", len(cmds))
	}
	if m.liveSSEConnecting["s1"] {
		t.Fatal("applySeed marked the attached session's observer in flight — the doomed connect started")
	}
}

// V47: when a catching-up session's pod is failed via the watch, catchingUp must be
// released so the terminal Failed attention toast can fire — cancelLiveSSE eats the
// closing stream's StreamEnded (its gen is gone), so the normal clearing path never
// runs.
func TestPodFailedReleasesCatchingUpAndNotifies(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusWaiting, PendingPermissionID: "p1"},
		catchingUp:       true,
	}}
	// A registered background stream that the failure will cancel.
	m.liveSSECancels["s1"] = func() {}
	m.liveSSEStreamGen["s1"] = 1

	m.applyPodEvent(k8s.StateEvent{State: session.State{ID: "s1", Status: session.StatusFailed}})

	got := m.sessionByID("s1")
	if got.catchingUp {
		t.Fatal("a watch-delivered Failed left catchingUp armed — the attention toast stays suppressed")
	}
	if got.DashStatus != StatusFailed {
		t.Fatalf("DashStatus=%v after Failed, want Failed", got.DashStatus)
	}
	if cmd := m.notifyIfBackgroundAttention(""); cmd == nil {
		t.Fatal("notify scan produced no toast for the Failed session (catchingUp skip still active)")
	}
}

// V47: a first-seen hydrated session denied a stream by the observer cap must not
// keep catchingUp armed — no stream means the EventStreamLive boundary that clears
// it can never arrive.
func TestApplySeedClearsCatchingUpWhenObserverDeclined(t *testing.T) {
	store := newFakeSnapshotStore()
	// A Busy (unprotected) session so the cap can decline it.
	store.snaps["cold"] = SessionSnapshot{LastSeq: 5, DashStatus: StatusBusy}
	m := New(nil).WithSnapshotStore(store)
	m.maxObserverStreams = 1
	m.connector = failingConnector
	// Occupy the single observer slot so "cold" is over the cap.
	m.liveSSECancels["hot"] = func() {}

	m, _ = m.applySeed([]session.State{
		{ID: "hot", Status: session.StatusRunning},
		{ID: "cold", Status: session.StatusRunning},
	})

	if got := m.sessionByID("cold"); got.catchingUp {
		t.Fatal("cap-declined hydrated session kept catchingUp armed — its attention toast stays suppressed forever")
	}
}

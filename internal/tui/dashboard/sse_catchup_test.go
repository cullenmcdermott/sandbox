package dashboard

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1a step 3: while a background stream replays its after=<seq> history, an
// attention state is APPLIED but must not toast (it may be a long-dead
// permission resolved later in the same history). Exactly one honest toast
// fires at the replay-complete boundary if the session is still in attention.
func TestCatchUpSuppressesReplayToastThenToastsOnLive(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{SessionFromState(session.State{ID: "bg", Status: session.StatusRunning})}
	id := session.ID("bg")
	// Simulate a background-stream install: the session is catching up.
	for i := range m.sessions {
		if m.sessions[i].ID() == id {
			m.sessions[i].catchingUp = true
		}
	}

	// Replay burst: an OLD-time permission.requested (10 min ago).
	oldT := time.Now().Add(-10 * time.Minute)
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{
		Type: session.EventPermissionRequested, Seq: 5, Time: oldT.Format(time.RFC3339Nano),
		Payload: mustJSON(session.PermissionPayload{PermissionID: "p1", Tool: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}),
	}})
	s := m.sessionByID(id)
	if s.DashStatus != StatusWaiting || s.PendingPermissionID != "p1" {
		t.Fatalf("replayed permission must still APPLY state: status=%v pending=%q", s.DashStatus, s.PendingPermissionID)
	}
	if !s.catchingUp {
		t.Fatal("an old-time replayed event must NOT clear catchingUp")
	}
	if cmd := m.notifyIfBackgroundAttention(m.attachedID); cmd != nil {
		t.Fatalf("no toast should fire during catch-up, got %#v", cmd())
	}

	// Replay-complete boundary.
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{Type: session.EventStreamLive}})
	if m.sessionByID(id).catchingUp {
		t.Fatal("EventStreamLive must clear catchingUp")
	}
	cmd := m.notifyIfBackgroundAttention(m.attachedID)
	if cmd == nil {
		t.Fatal("a still-waiting session should toast exactly once after the live boundary")
	}
	if _, ok := cmd().(toastMsg); !ok {
		t.Fatalf("expected toastMsg after boundary, got %#v", cmd())
	}
	if cmd2 := m.notifyIfBackgroundAttention(m.attachedID); cmd2 != nil {
		t.Fatal("must not re-toast the same still-waiting session on the next scan")
	}
}

// §1a review fix (Finding 2): the REAL background-stream install
// (liveSSEReadyMsg) must ARM catchingUp — otherwise the entire fresh-connect
// launch-storm suppression is unguarded (a mutation deleting the arming block
// ships green). All the other catch-up tests set the flag by hand; this one
// drives the actual Update path.
func TestLiveSSEReadyArmsCatchingUp(t *testing.T) {
	m := New(nil)
	id := session.ID("bg-install")
	m.sessions = []Session{SessionFromState(session.State{ID: id, Status: session.StatusRunning})}

	_, _ = m.Update(liveSSEReadyMsg{
		id:     id,
		ch:     make(chan session.Event),
		cancel: func() {},
		client: &fakeRunnerClient{},
	})

	if !m.sessionByID(id).catchingUp {
		t.Fatal("installing a background SSE stream must arm catchingUp (fresh-connect replay suppression)")
	}
}

// The headline §1a item-1 scenario: on relaunch, a permission that was
// REQUESTED and then RESOLVED later in the same replayed history must produce
// ZERO toasts — the notification-storm bug. State is applied (the session ends
// Busy, no pending permission), and nothing fires at the flip-to-live boundary.
func TestCatchUpNoToastForPermissionResolvedInReplay(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{SessionFromState(session.State{ID: "bg", Status: session.StatusRunning})}
	id := session.ID("bg")
	for i := range m.sessions {
		if m.sessions[i].ID() == id {
			m.sessions[i].catchingUp = true
		}
	}
	oldT := time.Now().Add(-10 * time.Minute)

	// Replay: permission.requested (→Waiting) then permission.resolved (→Busy),
	// both old-time so neither clears catchingUp.
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{
		Type: session.EventPermissionRequested, Seq: 5, Time: oldT.Format(time.RFC3339Nano),
		Payload: mustJSON(session.PermissionPayload{PermissionID: "p1", Tool: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}),
	}})
	if cmd := m.notifyIfBackgroundAttention(m.attachedID); cmd != nil {
		t.Fatalf("no toast during replay, got %#v", cmd())
	}
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{
		Type: session.EventPermissionResolved, Seq: 6, Time: oldT.Format(time.RFC3339Nano),
		Payload: mustJSON(session.PermissionPayload{PermissionID: "p1"}),
	}})

	// Flip to live: the session is now Busy (permission long resolved) — no toast.
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{Type: session.EventStreamLive}})
	s := m.sessionByID(id)
	if s.DashStatus != StatusBusy || s.PendingPermissionID != "" {
		t.Fatalf("replayed resolve should leave Busy/no-pending, got status=%v pending=%q", s.DashStatus, s.PendingPermissionID)
	}
	if cmd := m.notifyIfBackgroundAttention(m.attachedID); cmd != nil {
		t.Fatalf("a resolved-in-replay permission must NOT toast on relaunch, got %#v", cmd())
	}
}

// §1a review fix (Finding 4): the catch-up flag clears ONLY at EventStreamLive,
// never via a time-based freshness heuristic. On a SHORT (~2s) reconnect the
// last replayed events carry near-now timestamps; a freshness clear would fire
// mid-burst and leak a spurious toast + OS notification for an attention state
// resolved later in the same burst. A recent-timestamp requested→resolved replay
// must produce NO toast.
func TestCatchUpNoSpuriousToastOnShortReconnect(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{SessionFromState(session.State{ID: "bg", Status: session.StatusRunning})}
	id := session.ID("bg")
	for i := range m.sessions {
		if m.sessions[i].ID() == id {
			m.sessions[i].catchingUp = true
			m.sessions[i].lastSeq = 4
		}
	}
	// RECENT timestamps (the short-reconnect window) — these would trip an
	// ev.Time freshness clear, but only EventStreamLive may clear catchingUp.
	reqT := time.Now().Add(-1800 * time.Millisecond).Format(time.RFC3339Nano)
	resT := time.Now().Add(-1700 * time.Millisecond).Format(time.RFC3339Nano)

	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{
		Type: session.EventPermissionRequested, Seq: 5, Time: reqT,
		Payload: mustJSON(session.PermissionPayload{PermissionID: "p1", Tool: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}),
	}})
	if !m.sessionByID(id).catchingUp {
		t.Fatal("a recent-timestamp replayed event must NOT clear catchingUp (only EventStreamLive does)")
	}
	if cmd := m.notifyIfBackgroundAttention(m.attachedID); cmd != nil {
		t.Fatalf("no toast during catch-up on a short reconnect, got %#v", cmd())
	}
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{
		Type: session.EventPermissionResolved, Seq: 6, Time: resT,
		Payload: mustJSON(session.PermissionPayload{PermissionID: "p1"}),
	}})
	m.notifyIfBackgroundAttention(m.attachedID)
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{Type: session.EventStreamLive}})
	if cmd := m.notifyIfBackgroundAttention(m.attachedID); cmd != nil {
		t.Fatalf("a resolved-in-replay permission must NOT toast even on a short reconnect, got %#v", cmd())
	}
}

// §1a review fix (masking): a session already notified for permission P1 that,
// during the replay burst, RESOLVES P1 and then requests a DIFFERENT P2 must
// toast once for P2 at the flip — the leave-episode delete runs during catch-up
// (notify is scanned per event in the real Update flow) so the stale
// notifiedAttention entry can't mask the fresh episode.
func TestCatchUpFreshEpisodeAfterResolveInReplay(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{SessionFromState(session.State{ID: "bg", Status: session.StatusRunning})}
	id := session.ID("bg")
	for i := range m.sessions {
		if m.sessions[i].ID() == id {
			m.sessions[i].DashStatus = StatusWaiting
			m.sessions[i].PendingPermissionID = "p1"
			m.sessions[i].lastSeq = 100
			m.sessions[i].catchingUp = true
		}
	}
	m.notifiedAttention = map[session.ID]bool{id: true} // already toasted P1
	oldT := time.Now().Add(-10 * time.Minute).Format(time.RFC3339Nano)

	// Replay P1.resolved (→Busy) with a per-event notify scan (the real flow):
	// this ends P1's episode (deletes the stale notifiedAttention entry).
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{
		Type: session.EventPermissionResolved, Seq: 101, Time: oldT,
		Payload: mustJSON(session.PermissionPayload{PermissionID: "p1"}),
	}})
	m.notifyIfBackgroundAttention(m.attachedID)
	// Replay a NEW P2.requested (→Waiting): suppressed during catch-up.
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{
		Type: session.EventPermissionRequested, Seq: 102, Time: oldT,
		Payload: mustJSON(session.PermissionPayload{PermissionID: "p2", Tool: "Write", Input: json.RawMessage(`{"file_path":"/x"}`)}),
	}})
	if cmd := m.notifyIfBackgroundAttention(m.attachedID); cmd != nil {
		t.Fatalf("no toast during catch-up, got %#v", cmd())
	}
	// Flip to live: P2 is a fresh episode → exactly one toast.
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: session.Event{Type: session.EventStreamLive}})
	cmd := m.notifyIfBackgroundAttention(m.attachedID)
	if cmd == nil {
		t.Fatal("a new permission (P2) after P1 resolved in replay must toast at flip, not be masked")
	}
	if _, ok := cmd().(toastMsg); !ok {
		t.Fatalf("expected toastMsg for P2, got %#v", cmd())
	}
}

// §1a Fable-review fix: catchingUp armed at HYDRATE time has no
// EventStreamLive coming if the initial background connect FAILS (an
// unreachable-but-Running pod). The failure must release the suppression —
// and the handler's own notify scan must be able to toast the
// genuinely-pending hydrated attention state — instead of the flag sticking
// true and muting the session for the lifetime of the condition.
func TestConnectFailedReleasesCatchUpAndToasts(t *testing.T) {
	m := New(nil)
	id := session.ID("bg")
	s := SessionFromState(session.State{ID: id, Status: session.StatusRunning})
	s.catchingUp = true // hydrate-armed (applySeed/applyPodEvent snapshot path)
	s.DashStatus = StatusWaiting
	s.PendingPermissionID = "p1"
	s.PendingPermissionTool = "Bash"
	m.sessions = []Session{s}

	_, cmd := m.Update(liveSSEConnectFailedMsg{id: id, gen: 1})

	if m.sessionByID(id).catchingUp {
		t.Fatal("a failed initial connect must release catchingUp — no EventStreamLive is coming to clear it")
	}
	if cmd == nil {
		t.Fatal("the hydrated waiting session must toast once suppression is released")
	}
	if _, ok := cmd().(toastMsg); !ok {
		t.Fatalf("expected toastMsg after connect failure released suppression, got %#v", cmd())
	}
}

// Reconnect exhaustion (degradeUnreachable) is the second no-stream-coming
// terminal: it must release catch-up suppression so the degraded Failed state
// stays toastable like any other attention transition.
func TestDegradeUnreachableReleasesCatchUp(t *testing.T) {
	m := New(nil)
	id := session.ID("bg")
	s := SessionFromState(session.State{ID: id, Status: session.StatusRunning})
	s.catchingUp = true
	s.DashStatus = StatusBusy
	m.sessions = []Session{s}

	m.degradeUnreachable(id)

	got := m.sessionByID(id)
	if got.catchingUp {
		t.Fatal("degradeUnreachable must release catchingUp")
	}
	if got.DashStatus != StatusFailed {
		t.Fatalf("mid-turn degrade should be Failed, got %v", got.DashStatus)
	}
}

// StreamEnded with a not-running pod tears the stream down for good — the
// third no-stream-coming terminal. It must release the suppression alongside
// the permission clear so a later Failed derivation isn't muted.
func TestStreamEndedNotRunningReleasesCatchUp(t *testing.T) {
	m := New(nil)
	id := session.ID("bg")
	s := SessionFromState(session.State{ID: id, Status: session.StatusSuspended})
	s.catchingUp = true
	s.DashStatus = StatusWaiting
	m.sessions = []Session{s}

	m.handleRunnerEvent(RunnerEventMsg{ID: id, StreamEnded: true})

	if m.sessionByID(id).catchingUp {
		t.Fatal("StreamEnded on a not-running pod must release catchingUp")
	}
}

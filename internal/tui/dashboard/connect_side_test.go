package dashboard

import (
	"context"
	"errors"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1a connect-side (stream-registration pass). Multiple launch paths (seed,
// watch patch/insert, reconnect, detach-restore) could each fire a background
// connect for the same session because the launch guard only checked
// liveSSECancels — which is populated on ready, seconds after launch. The result
// was a leaked orphan stream whose eventual StreamEnded tore down the healthy
// stream (an unconditional cancelLiveSSE), plus double-applied events. The fix:
// (1) track in-flight connects and guard on them, (2) cancel a raced duplicate
// at ready, (3) tag every stream message with a generation so a stale/orphan
// message is ignored.

// hasLiveSSE counts an in-flight connect, not just a registered stream, so a
// launch guard suppresses a duplicate connect during the connectSem-throttled
// setup window (the whole bug: liveSSECancels is empty during that window).
func TestHasLiveSSECoversInFlightConnect(t *testing.T) {
	m := New(nil)
	if m.hasLiveSSE("x") {
		t.Fatal("no stream and not connecting → hasLiveSSE must be false")
	}
	m.liveSSEConnecting["x"] = true
	if !m.hasLiveSSE("x") {
		t.Error("in-flight connect must count as live (suppresses duplicate launch)")
	}
	delete(m.liveSSEConnecting, "x")
	m.liveSSECancels["x"] = func() {}
	if !m.hasLiveSSE("x") {
		t.Error("registered stream must count as live")
	}
}

// A watch pod event for a running session whose connect is already in flight must
// NOT launch a second connect — the pre-fix guard (liveSSECancels only) did, so
// seed + watch routinely double-launched.
func TestApplyPodEventSkipsConnectWhenInFlight(t *testing.T) {
	m := New(nil)
	m.connector = failingConnector // non-nil so the guard is actually reached
	m.sessions = []Session{{
		State:            session.State{ID: "s", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
	}}

	// Baseline: no in-flight connect → the pod event launches one.
	cmd := m.applyPodEvent(k8s.StateEvent{State: session.State{ID: "s", Status: session.StatusRunning}})
	if cmd == nil {
		t.Fatal("running session without a stream must launch a connect")
	}
	if !m.liveSSEConnecting["s"] {
		t.Fatal("launching a connect must mark the session in flight")
	}

	// Now a second pod event arrives while the first connect is still in flight:
	// the guard must skip it (no duplicate connect Cmd).
	cmd2 := m.applyPodEvent(k8s.StateEvent{State: session.State{ID: "s", Status: session.StatusRunning}})
	if cmd2 != nil {
		t.Error("a pod event must not launch a second connect while one is in flight")
	}
}

// At ready, if a stream is already registered for the session (a raced duplicate
// that beat the in-flight guard), the incoming stream is cancelled and the
// established one is preserved — the map overwrite that orphaned the live cancel
// func is gone.
func TestLiveSSEReadyCancelsRacedDuplicate(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{{
		State:            session.State{ID: "s", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
	}}
	// An established stream: gen 5, its own cancel func.
	establishedCancelled := false
	m.liveSSECancels["s"] = func() { establishedCancelled = true }
	m.liveSSEChannels["s"] = make(chan session.Event)
	m.liveSSEStreamGen["s"] = 5

	// A second connect becomes ready for the same session.
	incomingCancelled := false
	m.Update(liveSSEReadyMsg{
		id:     "s",
		ch:     make(chan session.Event),
		cancel: func() { incomingCancelled = true },
		client: &fakeRunnerClient{},
		gen:    6,
	})

	if !incomingCancelled {
		t.Error("the raced duplicate must be cancelled at ready")
	}
	if establishedCancelled {
		t.Error("the established stream must be preserved, not cancelled")
	}
	if m.liveSSEStreamGen["s"] != 5 {
		t.Errorf("established generation must survive, got %d want 5", m.liveSSEStreamGen["s"])
	}
}

// An orphan stream's StreamEnded (stale generation) must be ignored — it must NOT
// tear down the healthy stream via cancelLiveSSE, and must NOT schedule a
// reconnect. This is the core failure the pre-fix code hit: the orphan's close
// unconditionally cancelled whatever was registered.
func TestStaleStreamEndedDoesNotTearDownHealthyStream(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{{
		State:            session.State{ID: "s", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
	}}
	healthyCancelled := false
	m.liveSSECancels["s"] = func() { healthyCancelled = true }
	m.liveSSEChannels["s"] = make(chan session.Event)
	m.liveSSEStreamGen["s"] = 7 // the healthy stream's generation

	// A StreamEnded arrives tagged gen 3 (an orphan from a superseded connect).
	_, cmd := m.handleRunnerEvent(RunnerEventMsg{ID: "s", StreamEnded: true, gen: 3})

	if healthyCancelled {
		t.Error("a stale StreamEnded must not cancel the healthy stream")
	}
	if _, ok := m.liveSSECancels["s"]; !ok {
		t.Error("the healthy stream's registration must survive a stale StreamEnded")
	}
	if m.liveSSEStreamGen["s"] != 7 {
		t.Errorf("healthy generation must survive, got %d want 7", m.liveSSEStreamGen["s"])
	}
	if cmd != nil {
		t.Error("a stale StreamEnded must not schedule a reconnect")
	}

	// Sanity: a StreamEnded tagged with the REGISTERED generation DOES tear down.
	_, cmd = m.handleRunnerEvent(RunnerEventMsg{ID: "s", StreamEnded: true, gen: 7})
	if !healthyCancelled {
		t.Error("a matching-generation StreamEnded must tear down the stream")
	}
	if cmd == nil {
		t.Error("a matching-generation StreamEnded on a Running pod must schedule a reconnect")
	}
}

// A stale (superseded-generation) normal event is dropped rather than applied, so
// an orphan reader can't double-apply usage/status behind the healthy stream.
func TestStaleEventIsNotApplied(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{{
		State:            session.State{ID: "s", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusBusy, CtxLimit: 200_000},
	}}
	m.liveSSEChannels["s"] = make(chan session.Event)
	m.liveSSEStreamGen["s"] = 7

	usage := mkEvent(session.EventUsageUpdated, session.UsagePayload{InputTokens: 99_000})

	// Stale gen 3: dropped.
	m.handleRunnerEvent(RunnerEventMsg{ID: "s", Event: usage, gen: 3})
	if got := m.sessionByID("s").InputTokens; got != 0 {
		t.Errorf("stale-generation event must be dropped, but InputTokens=%d", got)
	}

	// Matching gen 7: applied.
	m.handleRunnerEvent(RunnerEventMsg{ID: "s", Event: usage, gen: 7})
	if got := m.sessionByID("s").InputTokens; got != 99_000 {
		t.Errorf("matching-generation event must apply, InputTokens=%d want 99000", got)
	}
}

// A failed initial connect clears the in-flight marker (via liveSSEConnectFailedMsg)
// so a later guard can retry — a nil return would strand liveSSEConnecting=true
// forever and permanently block the session's stream.
func TestConnectFailedClearsInFlightMarker(t *testing.T) {
	m := New(nil)
	m.liveSSEConnecting["s"] = true
	m.Update(liveSSEConnectFailedMsg{id: "s", gen: 4})
	if m.liveSSEConnecting["s"] {
		t.Error("liveSSEConnectFailedMsg must clear the in-flight marker so a retry can launch")
	}
}

// The load-bearing happy-path round trip: a real liveSSEReadyMsg must register
// the generation (liveSSEStreamGen[id]=gen), clear the in-flight marker, and open
// the stream — AND a subsequent event tagged with that SAME generation must be
// applied (not dropped by the stale-gen guard). This is the one path that proves
// the registered gen matches what liveSSENextCmd carries; if the registration
// stored the wrong value the session would go silently deaf, and every existing
// happy-path ready test uses gen=0 (which bypasses the guard) so none catches it.
func TestLiveSSEReadyRegistersGenerationRoundTrip(t *testing.T) {
	m := New(nil)
	id := session.ID("s")
	sess := transcriptSession()
	sess.State.ID = id
	sess.State.Status = session.StatusRunning
	sess.CtxLimit = 200_000
	m.sessions = []Session{sess}
	m.liveSSEConnecting[id] = true // as set by the connect that is now ready

	const gen = uint64(9)
	m.Update(liveSSEReadyMsg{
		id:     id,
		ch:     make(chan session.Event),
		cancel: func() {},
		client: &fakeRunnerClient{},
		gen:    gen,
	})

	if m.liveSSEStreamGen[id] != gen {
		t.Fatalf("ready must register the generation, got %d want %d", m.liveSSEStreamGen[id], gen)
	}
	if m.liveSSEConnecting[id] {
		t.Error("ready must clear the in-flight marker")
	}
	if _, ok := m.liveSSECancels[id]; !ok {
		t.Error("ready must register the stream cancel")
	}

	// An event carrying the registered generation must APPLY — proving the stored
	// gen matches the reader's gen (a mis-stored gen would drop every live event).
	m.handleRunnerEvent(RunnerEventMsg{
		ID:    id,
		Event: mkEvent(session.EventUsageUpdated, session.UsagePayload{InputTokens: 12_345}),
		gen:   gen,
	})
	if got := m.sessionByID(id).InputTokens; got != 12_345 {
		t.Errorf("event with the registered generation must apply, InputTokens=%d want 12345", got)
	}
}

// applySeed's launch guard must skip a session whose connect is already in flight
// (not just one with a registered stream) — the headline seed-races-watch
// double-launch the fix targets.
func TestApplySeedSkipsConnectWhenInFlight(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{
			State:            session.State{ID: "s", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
		}}
	m.liveSSEConnecting["s"] = true // a watch-driven connect is mid-setup
	called := false
	m.connector = func(_ context.Context, _ session.Ref, _ string, _ func(ConnectStage, string)) (ConnectResult, error) {
		called = true
		return ConnectResult{}, errors.New("should not be called")
	}

	_, cmds := m.applySeed([]session.State{{ID: "s", Status: session.StatusRunning}})
	if len(cmds) != 0 {
		t.Errorf("applySeed launched %d connects while one was in flight, want 0", len(cmds))
	}
	_ = called
}

// A failed reconnect attempt clears the in-flight marker AND schedules the next
// retry while the pod is Running and the budget isn't exhausted — the reconnect
// path's own marker-clear (asymmetric to the connect-failed one) is load-bearing:
// if it regressed, the reconnect guard could stall the retry loop.
func TestReconnectFailedClearsMarkerAndRetries(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{
			State:            session.State{ID: "s", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
		}}
	m.liveSSEConnecting["s"] = true // set by the reconnect connect now failing

	_, cmd := m.Update(liveSSEReconnectFailedMsg{id: "s", attempt: 0, gen: 3})
	if m.liveSSEConnecting["s"] {
		t.Error("liveSSEReconnectFailedMsg must clear the in-flight marker")
	}
	if cmd == nil {
		t.Error("a failed reconnect on a Running pod with budget left must schedule the next retry")
	}
}

// Regression guard (§1a connect-side review): the reconnect tick must PROCEED even
// when a connect is in flight (liveSSEConnecting set) but nothing is registered —
// otherwise a racing connect that then fails would strand a Running session with
// no stream and no pending retry. The generation token makes the possible
// duplicate connect safe, so the reconnect must not defer to the in-flight marker.
func TestReconnectProceedsDespiteInFlightConnect(t *testing.T) {
	m := New(nil)
	m.connector = failingConnector // non-nil so reconnectLiveSSECmd yields a Cmd
	m.sessions = []Session{
		{
			State:            session.State{ID: "s", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusBusy},
		}}
	m.liveSSEConnecting["s"] = true // a racing connect is in flight, nothing registered

	_, cmd := m.Update(liveSSEReconnectMsg{id: "s", attempt: 0})
	if cmd == nil {
		t.Error("reconnect must proceed despite an in-flight connect, or the retry loop is stranded")
	}
}

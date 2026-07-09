package dashboard

import (
	"errors"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1d connection-scaling cluster: the dashboard caps how many background
// observer forwards it keeps established at once and evicts the coldest when the
// cap is exceeded, so N warm sessions no longer pin N SPDY port-forwards through
// one kube-apiserver. Protected rows (attached / needs-attention) are never
// evicted; an evicted row reconnects on focus; a terminally-gone forward tears
// the observer down promptly.

// registerObserver simulates a background observer stream reaching ready for id,
// stamped active at t, driving the real liveSSEReadyMsg handler so the cap
// enforcement + LRU bookkeeping run exactly as in production.
func registerObserver(t *testing.T, m *Model, id session.ID, at time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return at }
	defer func() { nowFunc = old }()
	m.liveSSEConnecting[id] = true
	m.Update(liveSSEReadyMsg{
		id:     id,
		ch:     make(chan session.Event),
		cancel: func() {},
		client: &fakeRunnerClient{},
		gen:    m.nextLiveSSEGen(),
	})
}

// ORACLE: with the cap set to 3, establishing 5 observer streams leaves exactly
// 3 registered, and the two evicted are the two coldest (oldest recency).
func TestObserverCapEvictsColdest(t *testing.T) {
	m := New(nil)
	m.maxObserverStreams = 3
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := []string{"s0", "s1", "s2", "s3", "s4"}
	for i, id := range ids {
		m.sessions = append(m.sessions, runningSession(id))
		// s0 oldest … s4 newest.
		registerObserver(t, m, session.ID(id), base.Add(time.Duration(i)*time.Minute))
	}

	if got := len(m.liveSSECancels); got != 3 {
		t.Fatalf("established observer streams = %d, want cap 3", got)
	}
	// The two coldest (s0, s1) must be evicted; the three warmest survive.
	for _, gone := range []session.ID{"s0", "s1"} {
		if _, ok := m.liveSSECancels[gone]; ok {
			t.Errorf("coldest session %s must be evicted", gone)
		}
		if _, ok := m.observerActiveAt[gone]; ok {
			t.Errorf("evicted session %s must be pruned from the recency map", gone)
		}
	}
	for _, kept := range []session.ID{"s2", "s3", "s4"} {
		if _, ok := m.liveSSECancels[kept]; !ok {
			t.Errorf("warmest session %s must keep its stream", kept)
		}
	}
}

// The attached session and any session needing attention are never evicted, even
// when they are the coldest — the cap overshoots rather than blind an actionable
// row. Populates the observer maps directly because the attached session's own
// stream is owned by its transcript (the ready handler cancels a background one),
// so this exercises enforceObserverCap's protection policy in isolation.
func TestObserverCapNeverEvictsProtected(t *testing.T) {
	m := New(nil)
	m.maxObserverStreams = 2
	m.attachedID = "attached"
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	m.sessions = []Session{
		{State: session.State{ID: "attached", Status: session.StatusRunning}, sessionReadModel: sessionReadModel{DashStatus: StatusBusy}},
		{State: session.State{ID: "waiting", Status: session.StatusRunning}, sessionReadModel: sessionReadModel{DashStatus: StatusWaiting}},
		runningSession("warm1"),
		runningSession("warm2"),
	}
	// "attached" coldest, "waiting" next (both protected), then two warmer rows.
	for i, id := range []session.ID{"attached", "waiting", "warm1", "warm2"} {
		m.liveSSECancels[id] = func() {}
		m.liveSSEStreamGen[id] = uint64(i + 1)
		m.observerActiveAt[id] = base.Add(time.Duration(i) * time.Minute)
	}

	m.enforceObserverCap("") // keepID empty: nothing freshly registered

	// Both protected rows must survive despite being the coldest.
	for _, prot := range []session.ID{"attached", "waiting"} {
		if _, ok := m.liveSSECancels[prot]; !ok {
			t.Errorf("protected session %s must never be evicted", prot)
		}
	}
	// With 2 protected pinned above a cap of 2, the two unprotected warm rows are
	// evicted to hold the cap as tight as possible.
	for _, gone := range []session.ID{"warm1", "warm2"} {
		if _, ok := m.liveSSECancels[gone]; ok {
			t.Errorf("unprotected session %s should yield to protected rows at the cap", gone)
		}
	}
}

// COUNTER: below the cap nothing is evicted (the common case must be untouched).
func TestObserverCapNoEvictionBelowCap(t *testing.T) {
	m := New(nil)
	m.maxObserverStreams = 8
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		id := session.ID(string(rune('a' + i)))
		m.sessions = append(m.sessions, runningSession(string(id)))
		registerObserver(t, m, id, base.Add(time.Duration(i)*time.Minute))
	}
	if got := len(m.liveSSECancels); got != 4 {
		t.Fatalf("below the cap all 4 streams must stay, got %d", got)
	}
}

// admitObserver: at the cap, an unprotected session is denied a new observer
// connect (stays on its watch row); a protected one is always admitted.
func TestAdmitObserverAtCap(t *testing.T) {
	m := New(nil)
	m.maxObserverStreams = 2
	m.liveSSECancels["a"] = func() {}
	m.liveSSECancels["b"] = func() {} // established set now at the cap
	m.sessions = []Session{
		{State: session.State{ID: "cold", Status: session.StatusRunning}, sessionReadModel: sessionReadModel{DashStatus: StatusBusy}},
		{State: session.State{ID: "hot", Status: session.StatusRunning}, sessionReadModel: sessionReadModel{DashStatus: StatusWaiting}},
	}
	if m.admitObserver("cold") {
		t.Error("an unprotected session at the cap must be denied a new observer connect")
	}
	if !m.admitObserver("hot") {
		t.Error("a needs-attention session must be admitted even at the cap")
	}
}

// applySeed must not fan out more than the cap worth of observer connects on the
// launch burst (the O(sessions) cost §1d targets).
func TestApplySeedHonorsObserverCap(t *testing.T) {
	m := New(nil)
	m.maxObserverStreams = 3
	m.connector = failingConnector // non-nil so launches are attempted
	var states []session.State
	for i := 0; i < 7; i++ {
		states = append(states, session.State{ID: session.ID(string(rune('a' + i))), Status: session.StatusRunning})
	}
	_, cmds := m.applySeed(states)
	if len(cmds) != 3 {
		t.Fatalf("applySeed launched %d observer connects, want cap 3", len(cmds))
	}
	// Every launched connect was marked in flight, so the load count is honest.
	if len(m.liveSSEConnecting) != 3 {
		t.Fatalf("in-flight connects = %d, want 3", len(m.liveSSEConnecting))
	}
}

// Reconnect-on-focus: a Running session with no observer stream (evicted or never
// admitted) gets one launched when the cursor lands on it.
func TestFocusReconnectsEvictedObserver(t *testing.T) {
	m := New(nil)
	m.seeded = true
	m.maxObserverStreams = 4
	m.connector = failingConnector
	m.sessions = []Session{runningSession("cold")}
	m.cursor = 0

	// No stream yet.
	if m.hasLiveSSE("cold") {
		t.Fatal("precondition: cold session must have no stream")
	}
	cmd := m.focusObserverSelected()
	if cmd == nil {
		t.Fatal("focusing a streamless Running session must launch a reconnect")
	}
	if !m.liveSSEConnecting["cold"] {
		t.Error("the focus reconnect must mark the session in flight")
	}
	// Focusing again while the connect is in flight must not double-launch.
	if cmd2 := m.focusObserverSelected(); cmd2 != nil {
		t.Error("a second focus while a connect is in flight must not relaunch")
	}
}

// focusObserver refreshes recency so a focused-but-already-streamed session is
// no longer the coldest eviction victim (no relaunch, just a touch).
func TestFocusTouchesRecencyWithoutRelaunch(t *testing.T) {
	m := New(nil)
	m.maxObserverStreams = 8
	m.connector = failingConnector
	m.sessions = []Session{runningSession("s")}
	m.liveSSECancels["s"] = func() {}
	old := nowFunc
	touched := time.Date(2031, 5, 5, 5, 5, 5, 0, time.UTC)
	nowFunc = func() time.Time { return touched }
	defer func() { nowFunc = old }()

	if cmd := m.focusObserver("s"); cmd != nil {
		t.Error("focusing an already-streamed session must not launch a connect")
	}
	if got := m.observerActiveAt["s"]; !got.Equal(touched) {
		t.Errorf("focus must stamp recency = %v, got %v", touched, got)
	}
}

// §1d Done()-wiring: a reconnect that fails with session.ErrSessionGone (the
// port-forward's terminal NotFound stop surfaced through the connector) gives up
// the retry loop AT ONCE and tears the observer down — no backoff exhaustion.
func TestReconnectGivesUpAndTearsDownOnSessionGone(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{runningSession("s")}
	m.liveSSEConnecting["s"] = true
	m.observerActiveAt["s"] = nowFunc()
	m.retained["s"] = &TranscriptModel{} // warm model to prove teardown

	_, cmd := m.Update(liveSSEReconnectFailedMsg{
		id: "s", attempt: 0, gen: 1,
		err: session.ErrSessionGone,
	})
	if cmd != nil {
		t.Error("a session-gone reconnect failure must NOT schedule another retry")
	}
	if _, ok := m.retained["s"]; ok {
		t.Error("a terminal forward must drop the warm model")
	}
	if _, ok := m.observerActiveAt["s"]; ok {
		t.Error("a terminal forward must prune the recency entry")
	}
}

// COUNTER: a transient reconnect error (not session-gone) still retries with
// backoff while the pod is Running — the give-up path must be gone-specific.
func TestReconnectRetriesOnTransientErrorNotGone(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{runningSession("s")}
	m.liveSSEConnecting["s"] = true

	_, cmd := m.Update(liveSSEReconnectFailedMsg{
		id: "s", attempt: 0, gen: 1,
		err: errors.New("port-forward: connection refused"),
	})
	if cmd == nil {
		t.Error("a transient reconnect failure on a Running pod must schedule the next retry")
	}
}

// The attach gate lets a foreground connect preempt observer connects: while a
// foreground attach is in flight, an observer's wait() blocks; it proceeds once
// the attach finishes. The foreground path never blocks on enter().
func TestAttachGatePreemptsObservers(t *testing.T) {
	g := newAttachGate()

	// Idle: wait returns immediately.
	done := make(chan struct{})
	go func() { g.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("an idle gate must not block observers")
	}

	// Foreground connect starts — enter must not block.
	g.enter()

	blocked := make(chan struct{})
	go func() { g.wait(); close(blocked) }()
	select {
	case <-blocked:
		t.Fatal("an observer must yield while a foreground connect is in flight")
	case <-time.After(50 * time.Millisecond):
	}

	// Foreground connect finishes — the observer proceeds.
	g.exit()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("observers must resume once the foreground connect finishes")
	}
}

// Nested foreground connects (attach racing create) only reopen the gate once
// the last one exits, and a nil gate is a no-op (Models built directly in tests).
func TestAttachGateNestingAndNil(t *testing.T) {
	var nilGate *attachGate
	nilGate.wait() // must not panic or block

	g := newAttachGate()
	g.enter()
	g.enter()
	g.exit() // still one active → gate stays shut

	blocked := make(chan struct{})
	go func() { g.wait(); close(blocked) }()
	select {
	case <-blocked:
		t.Fatal("gate must stay shut while an inner foreground connect is still active")
	case <-time.After(50 * time.Millisecond):
	}
	g.exit() // last one out → reopen
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("gate must reopen once the last foreground connect exits")
	}
}

// --------------------------------------------------------------------------
// §1d H1/H2/H3 + C1 (2026-07-07 handoff review): the cap must actually evict a
// fleet of completed sessions, eviction must not destroy detached work, and
// every stream teardown must release its SPDY forward.
// --------------------------------------------------------------------------

// needsInputSession builds a Running session in the post-turn steady state:
// needs-input with all output already seen (lastSeq == seenSeq).
func needsInputSession(id string, lastSeq, seenSeq uint64) Session {
	s := Session{
		State:            session.State{ID: session.ID(id), Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusNeedsInput},
	}
	s.lastSeq = lastSeq
	s.seenSeq = seenSeq
	return s
}

// H1 ORACLE: needs-input is the steady state of every session that ever
// completed a turn, so a SEEN needs-input row must NOT be protected — otherwise
// a relaunch with a fleet of completed sessions admits everything and the cap
// is a no-op (the exact 953ef87 regression). UNSEEN output still protects.
func TestObserverCapNeedsInputProtectedOnlyWhileUnseen(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		needsInputSession("seen", 42, 42),   // caught up → evictable
		needsInputSession("unseen", 42, 40), // unseen output → protected
	}
	if m.observerProtected("seen") {
		t.Error("a needs-input session with no unseen output must be evictable (H1: the cap was a no-op)")
	}
	if !m.observerProtected("unseen") {
		t.Error("a needs-input session with unseen output must stay protected")
	}
}

// H1: the relaunch scenario end-to-end — a fleet of completed (needs-input,
// seen) sessions larger than the cap is evicted down to the cap, exactly like
// idle rows. Before the fix every one of these was "protected" and the cap
// admitted all of them.
func TestObserverCapEvictsSteadyStateNeedsInputFleet(t *testing.T) {
	m := New(nil)
	m.maxObserverStreams = 2
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		m.sessions = append(m.sessions, needsInputSession(id, 10, 10))
		registerObserver(t, m, session.ID(id), base.Add(time.Duration(i)*time.Minute))
	}
	if got := len(m.liveSSECancels); got != 2 {
		t.Fatalf("established streams = %d, want cap 2 (needs-input fleet must not bypass the cap)", got)
	}
}

// H2: a detached session with an armed /loop driver (or a queued prompt) on its
// warm model must never be the eviction victim — evicting it would silence work
// the user set in motion while the pod is healthy.
func TestObserverProtectsArmedAutopilotAndQueuedPrompt(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{runningSession("loop"), runningSession("queued"), runningSession("plain")}

	loop := &TranscriptModel{}
	loop.autopilot = autopilotState{kind: autopilotLoop, prompt: "go"}
	m.retained["loop"] = loop

	queued := &TranscriptModel{}
	queued.queuedPrompt = "next thing"
	m.retained["queued"] = queued

	m.retained["plain"] = &TranscriptModel{}

	if !m.observerProtected("loop") {
		t.Error("an armed /loop driver must protect its session's observer stream")
	}
	if !m.observerProtected("queued") {
		t.Error("a queued prompt must protect its session's observer stream")
	}
	if m.observerProtected("plain") {
		t.Error("an idle warm model must not protect its stream")
	}
}

// H2: eviction reclaims the transport, NOT the warm model — the retained
// transcript (and anything armed on it) survives, so re-focus is still an O(1)
// swap and a briefly-cold session loses no state.
func TestEvictObserverKeepsWarmModel(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{runningSession("s")}
	m.liveSSECancels["s"] = func() {}
	m.retained["s"] = &TranscriptModel{}

	m.evictObserver("s")

	if _, ok := m.liveSSECancels["s"]; ok {
		t.Error("eviction must tear down the stream")
	}
	if _, ok := m.retained["s"]; !ok {
		t.Error("eviction must NOT drop the retained warm model (H2)")
	}
}

// H3: an evicted-while-Busy row must fall back to its watch-derived baseline
// instead of spinning forever with no stream left to flip it.
func TestEvictObserverStampsBusyRowToWatchBaseline(t *testing.T) {
	m := New(nil)
	s := runningSession("busy")
	s.DashStatus = StatusBusy
	m.sessions = []Session{s}
	m.liveSSECancels["busy"] = func() {}

	m.evictObserver("busy")

	if got := m.sessions[0].DashStatus; got != StatusIdle {
		t.Errorf("evicted Busy row status = %v, want watch-derived StatusIdle", got)
	}
}

// C1 ORACLE: cancelLiveSSE (the single stream-teardown choke point — eviction,
// suspend, supersede all funnel through it) must invoke the registered transport
// close, or the SPDY forward + reconnect loop poll the API server forever.
func TestCancelLiveSSEClosesTransport(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{runningSession("s")}
	closed := 0
	m.liveSSEConnecting["s"] = true
	m.Update(liveSSEReadyMsg{
		id:     "s",
		ch:     make(chan session.Event),
		cancel: func() {},
		client: &fakeRunnerClient{},
		close:  func() { closed++ },
		gen:    m.nextLiveSSEGen(),
	})
	if closed != 0 {
		t.Fatal("registration must not close the transport")
	}
	m.cancelLiveSSE("s")
	if closed != 1 {
		t.Fatalf("cancelLiveSSE closed the transport %d times, want 1", closed)
	}
	if _, ok := m.liveSSECloses["s"]; ok {
		t.Error("the close func must be dropped after teardown")
	}
}

// C1: every ready-message discard path (raced duplicate, gone/suspended session,
// attached session) must release the incoming connection's transport — a
// discarded ready that only cancels the read ctx leaks the forward.
func TestDiscardedReadyMsgClosesTransport(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{runningSession("dup"), runningSession("attached")}
	m.attachedID = "attached"
	// An established stream already registered for "dup".
	m.liveSSECancels["dup"] = func() {}

	mk := func(id session.ID, closed *int) liveSSEReadyMsg {
		return liveSSEReadyMsg{
			id:     id,
			ch:     make(chan session.Event),
			cancel: func() {},
			client: &fakeRunnerClient{},
			close:  func() { *closed++ },
			gen:    m.nextLiveSSEGen(),
		}
	}

	var dupClosed, goneClosed, attachedClosed int
	m.Update(mk("dup", &dupClosed)) // raced duplicate
	if dupClosed != 1 {
		t.Errorf("raced-duplicate ready must close its transport, closed %d times", dupClosed)
	}
	m.Update(mk("vanished", &goneClosed)) // session no longer exists
	if goneClosed != 1 {
		t.Errorf("gone-session ready must close its transport, closed %d times", goneClosed)
	}
	m.Update(mk("attached", &attachedClosed)) // transcript owns the live stream
	if attachedClosed != 1 {
		t.Errorf("attached-session ready must close its transport, closed %d times", attachedClosed)
	}
}

// C1: the foreground paths release their transport too — detach funnels through
// parkTranscript (the single detach hook), and the external pane's real
// teardown (close(), never minimize) does the same for the opencode forwards.
func TestDetachAndPaneCloseReleaseTransport(t *testing.T) {
	app := NewApp(nil, nil, nil)
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	closed := 0
	m.transportClose = func() { closed++ }
	app.parkTranscript(m)
	if closed != 1 {
		t.Errorf("parkTranscript closed the transport %d times, want 1", closed)
	}
	if m.transportClose != nil {
		t.Error("parkTranscript must clear transportClose so a re-park can't double-close")
	}

	pane := &ExternalPane{}
	paneClosed := 0
	pane.transportClose = func() { paneClosed++ }
	pane.close()
	if paneClosed != 1 {
		t.Errorf("pane close() closed the transport %d times, want 1", paneClosed)
	}
}

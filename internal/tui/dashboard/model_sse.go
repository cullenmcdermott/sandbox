package dashboard

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// --------------------------------------------------------------------------
// Messages
// --------------------------------------------------------------------------

// PodEventMsg carries a single cluster-watch delta for one session. The
// dashboard's Update handler patches exactly that one session in the read-model.
type PodEventMsg struct {
	Event k8s.StateEvent
}

// seedMsg carries the initial session list from Backend.List.
type seedMsg []session.State

// RunnerEventMsg carries a single SSE event from a live runner connection for
// one session. The Update handler applies ApplyRunnerEvent to patch that
// session's DashStatus in the read-model.
type RunnerEventMsg struct {
	// ID is the session the event belongs to.
	ID session.ID
	// Event is the normalized runner event.
	Event session.Event
	// StreamEnded is true when the SSE channel was closed without an event
	// (connection lost). The handler degrades to cluster-derived status.
	StreamEnded bool
	// gen is the generation of the stream that produced this message (see
	// Model.liveSSEStreamGen). A message whose gen no longer matches the
	// registered stream is from a superseded/orphaned connect and is ignored.
	// Zero on test-synthesized / pre-generation messages, which always apply.
	gen uint64
}

// RunnerEventBatchMsg carries a BURST of SSE events drained from one passive
// stream in a single message (§4 E5). liveSSEBatchCmd blocks for the first event
// then non-blockingly drains up to eventBatchMax more, so a delta burst costs one
// Update+View instead of one per event. One channel is one generation, so gen
// tags the whole batch and the stale-stream guard checks it once. StreamEnded
// means the channel closed mid-drain: Events (read before the close) are applied
// first, then the stream-ended handling — no event is lost.
type RunnerEventBatchMsg struct {
	// ID is the session the events belong to.
	ID session.ID
	// Events are the drained events, in arrival order. May be empty when the
	// channel closed on the very first (blocking) read.
	Events []session.Event
	// StreamEnded is true when the channel closed (with or without drained
	// events). The handler applies Events, then degrades to cluster-derived status.
	StreamEnded bool
	// gen is the producing stream's generation (see RunnerEventMsg.gen).
	gen uint64
}

// syncStatusMsg carries one warm session's freshly-probed sync health. conflicts
// and hint are populated only when status == "conflicted" (§1d).
type syncStatusMsg struct {
	id        session.ID
	status    string
	conflicts []string
	hint      string
}

// idleStatusMsg carries one warm session's freshly-probed idle-since time.
type idleStatusMsg struct {
	id        session.ID
	idleSince time.Time
}

// syncPollTickMsg schedules the next round of warm-session sync/idle probes.
type syncPollTickMsg struct{}

// syncPollInterval is the cadence for probing warm sessions' sync + idle state.
const syncPollInterval = 4 * time.Second

// unfocusedSyncPollInterval throttles the Mutagen `sync list` probe for warm
// sessions the user is NOT looking at. The focused session(s) — the row selected
// in the list and/or the attached transcript — keep probing at syncPollInterval so
// their indicator stays fresh; every other warm session's sync-list fork backs off
// to this longer cadence. This drops the steady-state subprocess rate from one per
// warm session every 4s to one per warm session every 30s (an initial tick still
// sweeps every session once, so each gets a prompt first reading). See
// selectSyncProbeTargets.
const unfocusedSyncPollInterval = 30 * time.Second

func syncPollCmd() tea.Cmd {
	return tea.Tick(syncPollInterval, func(time.Time) tea.Msg { return syncPollTickMsg{} })
}

// selectSyncProbeTargets decides which warm sessions to fork a `mutagen sync list`
// probe for on a poll tick. Probing every warm session every tick is wasteful once
// the set grows, so the focused sessions (selected + attached) always probe while
// the rest back off to `backoff`. It stamps lastProbe for the chosen ids and prunes
// entries for sessions no longer warm so the map can't grow unbounded. A session
// absent from lastProbe (freshly warm, or after the dashboard's first tick) probes
// immediately, giving every session a prompt initial reading before it throttles.
func selectSyncProbeTargets(now time.Time, warm []session.ID, focused map[session.ID]bool, lastProbe map[session.ID]time.Time, backoff time.Duration) []session.ID {
	live := make(map[session.ID]bool, len(warm))
	var targets []session.ID
	for _, id := range warm {
		live[id] = true
		if focused[id] {
			targets = append(targets, id)
			lastProbe[id] = now
			continue
		}
		if last, ok := lastProbe[id]; !ok || now.Sub(last) >= backoff {
			targets = append(targets, id)
			lastProbe[id] = now
		}
	}
	for id := range lastProbe {
		if !live[id] {
			delete(lastProbe, id)
		}
	}
	return targets
}

// syncFocusSet is the set of sessions whose Mutagen sync indicator is currently on
// screen — the row selected in the list (detail pane) and the attached session
// (transcript header). These probe every tick so their status stays fresh; other
// warm sessions are throttled by selectSyncProbeTargets.
func (m *Model) syncFocusSet() map[session.ID]bool {
	focused := make(map[session.ID]bool, 2)
	if m.attachedID != "" {
		focused[m.attachedID] = true
	}
	if sel := m.selectedSession(); sel != nil {
		focused[sel.ID()] = true
	}
	return focused
}

// reconcileTickMsg schedules the next periodic full cluster re-list.
type reconcileTickMsg struct{}

// reconcileMsg carries a fresh full cluster snapshot (Backend.List) used to drop
// sessions the watch never told us were deleted. Distinct from seedMsg, which
// adds/patches; the reconcile only removes (the watch owns adds).
type reconcileMsg []session.State

// reconcileInterval is the cadence of the periodic full re-list that prunes
// phantom sessions the watch informer missed.
const reconcileInterval = 30 * time.Second

func reconcileTickCmd() tea.Cmd {
	return tea.Tick(reconcileInterval, func(time.Time) tea.Msg { return reconcileTickMsg{} })
}

// reconcileListCmd re-lists the cluster off the Update goroutine and delivers the
// result as a reconcileMsg. It must NOT touch m.sessions (that would race with
// Update); the snapshot is applied in the reconcileMsg handler.
func (m *Model) reconcileListCmd() tea.Cmd {
	backend := m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		states, err := backend.List(ctx)
		if err != nil {
			return nil // transient: skip this cycle, the next tick retries
		}
		return reconcileMsg(states)
	}
}

// probeSyncCmd probes one warm session's sync health off the Update goroutine.
func (m *Model) probeSyncCmd(id session.ID) tea.Cmd {
	prober := m.syncProber
	if prober == nil {
		return nil
	}
	return func() tea.Msg {
		h := prober(context.Background(), id)
		return syncStatusMsg{id: id, status: h.Status, conflicts: h.Conflicts, hint: h.Hint}
	}
}

// syncGCGrace is how long a sync must stay orphaned (pod endpoint down) AND its
// session stay absent from the cluster before the GC terminates it. It spans
// several reconcile cycles (reconcileInterval = 30s), so a fresh session still
// connecting (not yet listed), a brief pod restart, or a momentary cluster-list
// gap never triggers a reap.
const syncGCGrace = 90 * time.Second

// orphanGCMsg carries the current orphaned mutagen syncs for a GC pass.
type orphanGCMsg struct{ orphans []OrphanSync }

// gcListOrphansCmd lists this tool's down/orphaned mutagen syncs off the Update
// goroutine. nil when no reaper is configured (the GC is then a no-op). A list
// error is swallowed — the next reconcile retries — so a flaky mutagen daemon
// can't wedge the dashboard or, worse, look like "no orphans" and skip cleanup.
func (m *Model) gcListOrphansCmd() tea.Cmd {
	reaper := m.syncReaper
	if reaper == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		orphans, err := reaper.ListOrphans(ctx)
		if err != nil {
			return nil
		}
		return orphanGCMsg{orphans: orphans}
	}
}

// gcTerminateCmd terminates the given orphaned mutagen syncs off the Update
// goroutine. A failure is non-fatal: the syncs stay orphaned and the next GC pass
// retries (their grace entries were already cleared, so they re-accrue the grace).
func (m *Model) gcTerminateCmd(ids []string) tea.Cmd {
	reaper := m.syncReaper
	if reaper == nil || len(ids) == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = reaper.Terminate(ctx, ids)
		return nil
	}
}

// gcRunningSet is the set of session ids whose pod is (or is coming) up per the
// authoritative cluster snapshot — Running or Creating. The sync GC protects only
// these; everything else (Suspended, Failed, or absent/gone) has no live pod, so
// its orphaned syncs are eligible for reaping after the grace window.
func gcRunningSet(states []session.State) map[session.ID]bool {
	s := make(map[session.ID]bool, len(states))
	for _, st := range states {
		if st.Status == session.StatusRunning || st.Status == session.StatusCreating {
			s[st.ID] = true
		}
	}
	return s
}

// reapOrphans applies the GC grace policy to the current orphan syncs and returns
// a Cmd terminating those that are durably dead. An orphan is reaped only when (a)
// its session's pod is NOT up per the latest authoritative snapshot (gcRunning) —
// i.e. the session is gone, Suspended, or Failed, so the sync is thrashing a pod
// that isn't there — AND (b) it has stayed that way for at least syncGCGrace. So a
// running session's sync (even mid-blip) and a fresh session still scheduling are
// never touched, while an idle-reaped or destroyed session's syncs are cleaned up.
// A reaped sync is re-created idempotently by the next attach (and resumed if it
// was merely paused). Grace entries for recovered orphans (pod came back, or
// transport reconnected) are dropped so the map tracks only live candidates.
func (m *Model) reapOrphans(orphans []OrphanSync) tea.Cmd {
	if m.orphanSince == nil {
		m.orphanSince = make(map[string]time.Time)
	}
	now := nowFunc()
	seen := make(map[string]bool, len(orphans))
	var due []string
	for _, o := range orphans {
		seen[o.Identifier] = true
		// Protected: the session's pod is up (or scheduling) per the authoritative
		// snapshot → its sync should be/become connected; never reap it.
		if m.gcRunning[o.SessionID] {
			delete(m.orphanSince, o.Identifier)
			continue
		}
		// No live pod (gone/suspended/failed): start/continue the grace clock.
		since, ok := m.orphanSince[o.Identifier]
		if !ok {
			m.orphanSince[o.Identifier] = now
			continue
		}
		if now.Sub(since) >= syncGCGrace {
			due = append(due, o.Identifier)
			delete(m.orphanSince, o.Identifier)
		}
	}
	// Drop grace entries for syncs that recovered (no longer in the orphan list).
	for id := range m.orphanSince {
		if !seen[id] {
			delete(m.orphanSince, id)
		}
	}
	return m.gcTerminateCmd(due)
}

// probeIdleCmd probes one warm session's idle-since time off the Update
// goroutine, reusing the session's already-open background runner client.
func (m *Model) probeIdleCmd(id session.ID) tea.Cmd {
	client, ok := m.liveSSEClients[id]
	if !ok {
		return nil
	}
	ref := session.Ref{ID: id}
	return func() tea.Msg {
		// Bounded like approveCmd: a wedged port-forward must not pin this
		// goroutine (and its probe slot) forever.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st, err := client.Idle(ctx, ref)
		if err != nil {
			return idleStatusMsg{id: id} // zero idleSince = not counting
		}
		var since time.Time
		if st.IdleSince != "" {
			if parsed, perr := time.Parse(time.RFC3339, st.IdleSince); perr == nil {
				since = parsed
			}
		}
		return idleStatusMsg{id: id, idleSince: since}
	}
}

// liveSSEReadyMsg is returned by the SSE-start Cmd when the channel is open.
type liveSSEReadyMsg struct {
	id     session.ID
	ch     <-chan session.Event
	cancel context.CancelFunc
	client RunnerClient
	// close tears down the connect's transport (the SPDY forward + reconnect
	// loop — ConnectResult.Close). May be nil (tests). EVERY path that handles
	// this message must either register it (liveSSECloses) or invoke it alongside
	// cancel — a discarded ready message that only cancels leaks the forward
	// forever (§1d C1).
	close func()
	// gen is the generation token minted for this connect at launch (see
	// Model.liveSSEStreamGen). Stored as the registered generation on success.
	gen uint64
}

// discard releases everything a ready message carries when the handler refuses
// to register it (session gone/suspended, attached owns the stream, raced
// duplicate): the SSE read ctx AND the transport (§1d C1).
func (msg liveSSEReadyMsg) discard() {
	msg.cancel()
	if msg.close != nil {
		msg.close()
	}
}

// liveSSEReconnectMsg fires after a backoff delay to (re)attempt a background
// status stream for a session whose stream dropped while the cluster still
// believes the pod is Running. attempt is the 0-based try number. This is how a
// transient port-forward blip self-heals instead of sticking a false 'failed'
// glyph on a perfectly healthy backgrounded session (RV1).
type liveSSEReconnectMsg struct {
	id      session.ID
	attempt int
}

// liveSSEReconnectFailedMsg reports that a background reconnect attempt could not
// open the stream. The handler retries with backoff up to liveSSEMaxRetries, then
// degrades the session to its honest status (Failed if it was mid-turn — B13's
// "runner unreachable = failed" — else cluster-derived).
type liveSSEReconnectFailedMsg struct {
	id      session.ID
	attempt int
	// gen is the generation of the failed reconnect connect, so the handler can
	// clear the in-flight marker it set at launch.
	gen uint64
	// err is the connector error. A terminal session.ErrSessionGone (the Sandbox
	// was destroyed — surfaced by the port-forward's NotFound stop, c191c85) makes
	// the handler give up the retry loop AT ONCE and tear the observer down,
	// instead of burning the full backoff budget before degradeUnreachable (§1d
	// Done()-wiring). Any other (transient) error retries with backoff as before.
	err error
}

// liveSSEConnectFailedMsg reports that an initial background connect (not a
// reconnect) could not open its stream. The session keeps its cluster-derived
// status (graceful degradation, as before); the handler exists only to clear the
// in-flight marker so a later launch guard can retry. Carries the connect's
// generation for symmetry with the other stream messages.
type liveSSEConnectFailedMsg struct {
	id  session.ID
	gen uint64
}

// snapshotSaveInterval throttles non-transition snapshot writes (the usage
// stream fires often). Status transitions always persist immediately; usage-only
// updates coalesce to at most one disk write per interval per session, so a
// relaunch resumes within one interval of head without thrashing the index.
const snapshotSaveInterval = 3 * time.Second

// liveSSEMaxRetries bounds background-stream reconnect attempts before a session
// is declared unreachable. Small: a transient forward blip heals on attempt 0–1;
// a genuinely dead runner is surfaced as 'failed' within a few seconds.
const liveSSEMaxRetries = 3

// liveSSEReconnectDelay is the backoff before reconnect attempt n (2s, 4s, 8s…,
// capped) — long enough to ride out a port-forward re-establishment without
// hammering the connector (which resumes/port-forwards/health-checks each try).
func liveSSEReconnectDelay(attempt int) time.Duration {
	d := 2 * time.Second * (1 << attempt)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// liveSSEReconnectTick schedules the next reconnect attempt after a backoff.
func liveSSEReconnectTick(id session.ID, attempt int, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return liveSSEReconnectMsg{id: id, attempt: attempt}
	})
}

// approveResultMsg signals that a ResolvePermission call completed (or failed).
type approveResultMsg struct {
	id  session.ID
	err error
}

// seedFailedMsg signals that the initial cluster seed (backend.List) failed, so
// the dashboard can show an actionable failure state instead of skeleton bars.
type seedFailedMsg struct{ err error }

// backgroundConnector returns the connector used for background status streams
// and other non-attach connects: the lightweight observer connector when set,
// else the full connector.
func (m *Model) backgroundConnector() Connector {
	if m.observerConnector != nil {
		return m.observerConnector
	}
	return m.connector
}

// --------------------------------------------------------------------------
// Commands
// --------------------------------------------------------------------------

func (m *Model) seedCmd() tea.Cmd {
	backend := m.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		states, err := backend.List(ctx)
		if err != nil {
			// Surface the failure so the list can show an error + retry instead of
			// hanging on skeleton bars forever (a later seed/watch/retry clears it).
			return seedFailedMsg{err: err}
		}
		return seedMsg(states)
	}
}

func (m *Model) startWatchCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := m.backend.Watch(ctx)
		if err != nil {
			cancel()
			return nil
		}
		// The cancel is delivered via watchReadyMsg and stored in
		// handleWatchReady (on the Update goroutine) to avoid racing on
		// m.watchCancel from this background Cmd.
		return watchReadyMsg{ch: ch, cancel: cancel}
	}
}

// watchReadyMsg is returned by startWatchCmd with the open channel.
type watchReadyMsg struct {
	ch     <-chan k8s.StateEvent
	cancel context.CancelFunc
}

// watchNextCmd blocks on one read from the watch channel and returns a
// PodEventMsg, then the Update loop re-issues itself for the next event.
func watchNextCmd(ch <-chan k8s.StateEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil // channel closed — watch goroutine done
		}
		return PodEventMsg{Event: ev}
	}
}

// startLiveSSECmd opens an SSE stream for the given running session in a
// background Cmd. On success it returns liveSSEReadyMsg; on failure (runner
// unreachable) it degrades gracefully — the session keeps its cluster-derived
// status and the dashboard remains responsive.
//
// Each session is bounded to one concurrent SSE forward: we store the cancel
// function in liveSSECancels and cancel any prior stream before opening a new one.
func (m *Model) startLiveSSECmd(sess Session) tea.Cmd {
	connector := m.backgroundConnector()
	if connector == nil || sess.ID() == "" {
		return nil
	}
	// Cancel any existing stream for this session.
	m.cancelLiveSSE(sess.ID())

	id := sess.ID()
	// Mint this connect's generation and mark it in flight, both synchronously in
	// Update so a racing seed/watch guard sees it before it can launch a duplicate.
	gen := m.nextLiveSSEGen()
	m.liveSSEConnecting[id] = true
	ref := session.Ref{ID: id}
	projectPath := sess.State.ProjectPath
	// Resume from the last event we persisted for this session instead of 0, so
	// the runner replays only genuinely-new events rather than the full history
	// (the source of the launch-time notification flashing and usage count-up).
	afterSeq := sess.lastSeq
	sem := m.connectSem
	gate := m.attachGate

	return func() tea.Msg {
		// Yield to any in-flight foreground attach/create connect (§5 leftover):
		// during the launch burst the user's attach must win the kube-apiserver,
		// so an observer connect pauses here until no foreground connect is active.
		gate.wait()
		// Throttle the connect burst (FU2): hold a slot only for the expensive
		// setup, then release it so the long-lived stream below doesn't occupy the
		// cap.
		release := acquireConnectSlot(sem)
		ctx, cancel := context.WithCancel(context.Background())
		// Connect (includes resume-if-suspended + port-forward + health).
		res, err := connector(ctx, ref, projectPath, func(ConnectStage, string) {})
		if err != nil {
			release()
			cancel()
			// Graceful degradation: stream could not be opened; no crash. The
			// failed msg only clears the in-flight marker so a later guard retries.
			return liveSSEConnectFailedMsg{id: id, gen: gen}
		}
		ch, err := res.Client.EventsPassive(ctx, ref, afterSeq)
		release()
		if err != nil {
			cancel()
			// The connect succeeded, so a forward is up — release it (§1d C1);
			// cancel() alone stops only the establishment ctx, not the forward.
			if res.Close != nil {
				res.Close()
			}
			return liveSSEConnectFailedMsg{id: id, gen: gen}
		}
		// Deliver the ready message; the Update loop stores the cancel and
		// starts reading events.
		return liveSSEReadyMsg{id: id, ch: ch, cancel: cancel, client: res.Client, close: res.Close, gen: gen}
	}
}

// nextLiveSSEGen mints a fresh monotonic generation token for a background
// stream connect. Called synchronously in Update, so no locking is needed.
func (m *Model) nextLiveSSEGen() uint64 {
	m.liveSSEGenCounter++
	return m.liveSSEGenCounter
}

// hasLiveSSE reports whether a session already has a background stream — either
// registered (open channel) or a connect in flight. Launch guards use this so a
// slow connectSem-throttled connect can't be launched twice by seed + watch.
func (m *Model) hasLiveSSE(id session.ID) bool {
	if _, ok := m.liveSSECancels[id]; ok {
		return true
	}
	return m.liveSSEConnecting[id]
}

// maxConcurrentBackgroundConnects caps how many background observer connects run
// their setup phase at once (FU2). Small enough to keep the launch burst off the
// cluster, large enough that a handful of sessions still warm up promptly.
const maxConcurrentBackgroundConnects = 4

// acquireConnectSlot blocks until a background-connect slot is free and returns
// a one-shot release. A nil semaphore (tests that build a Model directly) is a
// no-op, so the throttle never deadlocks an unconfigured model.
func acquireConnectSlot(sem chan struct{}) func() {
	if sem == nil {
		return func() {}
	}
	sem <- struct{}{}
	var once sync.Once
	return func() { once.Do(func() { <-sem }) }
}

// reconnectLiveSSECmd is startLiveSSECmd's retry sibling: on success it delivers
// the same liveSSEReadyMsg (so the stream is stored and read like any other), but
// on failure it returns a liveSSEReconnectFailedMsg carrying the attempt number,
// so the Update loop can back off and retry — or eventually degrade — instead of
// silently giving up (which would leave a healthy-but-blipped session stuck busy).
func (m *Model) reconnectLiveSSECmd(sess Session, attempt int) tea.Cmd {
	connector := m.backgroundConnector()
	if connector == nil || sess.ID() == "" {
		return nil
	}
	m.cancelLiveSSE(sess.ID())

	id := sess.ID()
	gen := m.nextLiveSSEGen()
	m.liveSSEConnecting[id] = true
	ref := session.Ref{ID: id}
	projectPath := sess.State.ProjectPath
	// Resume from the last persisted event (see startLiveSSECmd): a reconnect
	// after a port-forward blip must not replay the whole stream either.
	afterSeq := sess.lastSeq
	sem := m.connectSem
	gate := m.attachGate

	return func() tea.Msg {
		gate.wait()
		release := acquireConnectSlot(sem)
		ctx, cancel := context.WithCancel(context.Background())
		res, err := connector(ctx, ref, projectPath, func(ConnectStage, string) {})
		if err != nil {
			release()
			cancel()
			return liveSSEReconnectFailedMsg{id: id, attempt: attempt, gen: gen, err: err}
		}
		ch, err := res.Client.EventsPassive(ctx, ref, afterSeq)
		release()
		if err != nil {
			cancel()
			if res.Close != nil {
				res.Close() // release the established forward (§1d C1)
			}
			return liveSSEReconnectFailedMsg{id: id, attempt: attempt, gen: gen, err: err}
		}
		return liveSSEReadyMsg{id: id, ch: ch, cancel: cancel, client: res.Client, close: res.Close, gen: gen}
	}
}

// degradeUnreachable applies the honest end-state for a session whose background
// stream is gone and could not be reconnected: Failed if it was mid-turn (B13:
// "runner unreachable = failed"), otherwise the cluster-derived baseline.
func (m *Model) degradeUnreachable(id session.ID) {
	for i := range m.sessions {
		if m.sessions[i].ID() != id {
			continue
		}
		switch m.sessions[i].DashStatus {
		case StatusBusy, StatusWaiting:
			if m.sessions[i].DashStatus != StatusFailed {
				m.sessions[i].statusChangedAt = time.Now()
			}
			m.sessions[i].DashStatus = StatusFailed
		default:
			m.sessions[i].DashStatus = DeriveStatus(m.sessions[i].State)
		}
		// Reconnects are exhausted — no EventStreamLive is coming, so release
		// the catch-up suppression; the degraded state (e.g. Failed) must be
		// able to toast like any other attention transition.
		m.sessions[i].catchingUp = false
		return
	}
}

// cancelLiveSSE stops the SSE stream for the given session if one is running.
func (m *Model) cancelLiveSSE(id session.ID) {
	if cancel, ok := m.liveSSECancels[id]; ok {
		cancel()
		delete(m.liveSSECancels, id)
	}
	// Tear down the transport too (§1d C1): the forward is rooted at
	// context.Background(), so cancelling the stream ctx above does NOT stop it —
	// without this, every evicted/suspended/superseded observer left an SPDY
	// forward + reconnect loop polling the API server forever.
	if closeFn, ok := m.liveSSECloses[id]; ok {
		if closeFn != nil {
			closeFn()
		}
		delete(m.liveSSECloses, id)
	}
	delete(m.liveSSEChannels, id)
	delete(m.liveSSEClients, id)
	// Forget the registered generation so any late message from the just-
	// cancelled stream (whose goroutine may still deliver a buffered event or the
	// channel-close StreamEnded) is treated as stale and ignored.
	delete(m.liveSSEStreamGen, id)
	// Drop the LRU recency entry here (the single stream-teardown choke point) so
	// observerActiveAt tracks only live observer streams and can't accumulate
	// entries for cancelled/suspended/evicted sessions (§1d).
	delete(m.observerActiveAt, id)
}

// liveSSENextCmd reads one event from the channel and re-issues itself.
// Returns a RunnerEventMsg (or StreamEnded=true on channel close). gen tags the
// message with the producing stream's generation so a stale reader (from a
// superseded connect) is ignored by handleRunnerEvent.
func liveSSENextCmd(id session.ID, ch <-chan session.Event, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return RunnerEventMsg{ID: id, StreamEnded: true, gen: gen}
		}
		return RunnerEventMsg{ID: id, Event: ev, gen: gen}
	}
}

// liveSSEBatchCmd is the batching passive-stream reader (§4 E5). It blocks for
// the first event, then non-blockingly drains any already-buffered events (up to
// eventBatchMax, shared with the foreground transcript's waitForEvent) into ONE
// RunnerEventBatchMsg — so a delta burst collapses to a single Update+View
// instead of one full render pipeline per event. This mirrors waitForEvent's
// coalescing for the dashboard's background observer streams.
//
// If the channel closes on the first read, StreamEnded rides on an empty batch.
// If it closes mid-drain, the events read so far ride along WITH StreamEnded so
// the handler applies them before degrading — no drained event is lost. gen tags
// the batch; a stale reader (superseded connect) is ignored by the guard.
func liveSSEBatchCmd(id session.ID, ch <-chan session.Event, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return RunnerEventBatchMsg{ID: id, StreamEnded: true, gen: gen}
		}
		events := []session.Event{ev}
		for len(events) < eventBatchMax {
			select {
			case ev, ok := <-ch:
				if !ok {
					return RunnerEventBatchMsg{ID: id, Events: events, StreamEnded: true, gen: gen}
				}
				events = append(events, ev)
			default:
				return RunnerEventBatchMsg{ID: id, Events: events, gen: gen}
			}
		}
		return RunnerEventBatchMsg{ID: id, Events: events, gen: gen}
	}
}

// approveCmd fires a ResolvePermission call for the selected session.
// It is non-blocking (runs in a Cmd); errors are ignored (fire-and-forget).
func (m *Model) approveCmd(sess Session, allow bool) tea.Cmd {
	if sess.PendingPermissionID == "" {
		return nil
	}
	id := sess.ID()
	ref := session.Ref{ID: id}
	decision := session.PermissionDecision{
		Session:    id,
		Permission: sess.PendingPermissionID,
		Allow:      allow,
		Scope:      "once",
	}

	// Prefer the live SSE client: its port-forward is already open, so
	// approve/deny doesn't pay for a fresh connect + health check.
	if client, ok := m.liveSSEClients[id]; ok {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err := client.ResolvePermission(ctx, ref, decision)
			return approveResultMsg{id: id, err: err}
		}
	}

	connector := m.backgroundConnector()
	if connector == nil {
		return nil
	}
	projectPath := sess.State.ProjectPath
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		res, err := connector(ctx, ref, projectPath, func(ConnectStage, string) {})
		if err != nil {
			return approveResultMsg{id: id, err: err}
		}
		// One-shot connection: release its forward once the call returns (§1d C1).
		if res.Close != nil {
			defer res.Close()
		}
		err = res.Client.ResolvePermission(ctx, ref, decision)
		return approveResultMsg{id: id, err: err}
	}
}

// --------------------------------------------------------------------------
// Update continuation for watchReadyMsg
// --------------------------------------------------------------------------

// handleWatchReady handles messages that need access to the watch channel reference.
// The root App routes watchReadyMsg here.
func (m *Model) handleWatchReady(msg watchReadyMsg) (tea.Model, tea.Cmd) {
	// Store the cancel so shutdown stops the informer.
	if m.watchCancel != nil {
		m.watchCancel() // cancel previous watch if any
	}
	m.watchCancel = msg.cancel
	// Hold the channel so PodEventMsg can re-arm the reader after each event, and
	// start reading the first event.
	m.watchCh = msg.ch
	return m, watchNextCmd(msg.ch)
}

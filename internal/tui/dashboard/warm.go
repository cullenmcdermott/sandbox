package dashboard

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// warmSoftLimit is the advisory ceiling on simultaneously warm sessions. It is
// NOT enforced in v1 — exceeding it only emits a log-warn (see maybeWarnWarm).
// It exists as the single tunable for adding LRU eviction later if N grows.
const warmSoftLimit = 12

// defaultMaxObserverStreams is the steady-state cap on concurrently-established
// background observer forwards (SPDY port-forward + SSE stream + reader goroutine
// + idle-probe timer per session). §1d: with no cap, N warm sessions pin N
// forwards through one kube-apiserver, and API-server port-forward pressure is
// the first thing to break (~30 sessions). The cap bounds that laptop-side cost;
// beyond it the coldest streams are evicted and their rows fall back to the
// watch-driven lifecycle status (see enforceObserverCap). Chosen below the ~30
// breakage and above warmSoftLimit so a normal working set never evicts.
// Override via RunOptions.MaxObserverStreams / WithMaxObserverStreams.
const defaultMaxObserverStreams = 16

// observerCap is the effective cap on established observer streams.
func (m *Model) observerCap() int {
	if m.maxObserverStreams > 0 {
		return m.maxObserverStreams
	}
	return defaultMaxObserverStreams
}

// observerProtected reports whether a session's observer stream must never be
// evicted: the attached/viewed session (its pane or feed rides the live stream)
// and rows the user must be able to act on. Waiting (pending permission) and
// Failed are always protected; NeedsInput only while it carries UNSEEN output
// (§1d H1) — needs-input is the steady state of every session that ever
// completed a turn, so protecting it unconditionally admitted the whole fleet
// past the cap and made eviction a no-op. Once the user has viewed the output
// (seenSeq caught up, incl. the hydrate-from-snapshot path, which marks history
// seen), the row is evictable; focus reconnects it on demand.
func (m *Model) observerProtected(id session.ID) bool {
	if id != "" && id == m.attachedID {
		return true
	}
	s := m.sessionByID(id)
	if s.ID() == "" {
		return false
	}
	switch s.DashStatus {
	case StatusWaiting, StatusFailed:
		return true
	case StatusNeedsInput:
		return s.lastSeq > s.seenSeq
	}
	return false
}

// touchObserver stamps a session's observer as active/visible at nowFunc(), the
// recency key the coldest-eviction policy reads. Called when a stream registers,
// on every live event applied for the session, and when the session is focused.
func (m *Model) touchObserver(id session.ID) {
	if id == "" {
		return
	}
	if m.observerActiveAt == nil {
		m.observerActiveAt = make(map[session.ID]time.Time)
	}
	m.observerActiveAt[id] = nowFunc()
}

// admitObserver decides whether to START a new background observer connect for
// id (the launch guards in applySeed/applyPodEvent consult it). A protected
// session (attached / needs-attention) is always admitted — enforceObserverCap
// then evicts a colder unprotected stream at ready to stay near the cap. An
// unprotected session is admitted only while there is head-room (established +
// in-flight connects < cap); at the cap it is left on its watch-derived row
// until it is focused or transitions into attention (reconnect-on-demand). This
// keeps the launch burst from establishing N>cap forwards only to evict most of
// them right after paying the connect cost.
func (m *Model) admitObserver(id session.ID) bool {
	if m.observerProtected(id) {
		return true
	}
	load := len(m.liveSSECancels) + len(m.liveSSEConnecting)
	return load < m.observerCap()
}

// coldestObserver returns the established observer stream with the oldest recency
// (least-recently active/visible), skipping keepID and any protected session. A
// stream with no recency entry sorts as maximally cold (zero time). Empty ID
// means nothing is evictable (only keepID and protected streams remain).
func (m *Model) coldestObserver(keepID session.ID) session.ID {
	var victim session.ID
	var oldest time.Time
	for id := range m.liveSSECancels {
		if id == keepID || m.observerProtected(id) {
			continue
		}
		t := m.observerActiveAt[id]
		if victim == "" || t.Before(oldest) {
			victim, oldest = id, t
		}
	}
	return victim
}

// evictObserver tears down a session's observer stream + forward to reclaim its
// cluster-side cost (SPDY forward + reconnect loop, SSE goroutine, idle-probe
// timer). The warm-set membership is deliberately KEPT: the cap exists for
// API-server port-forward pressure, not laptop RAM, and the row still resolves
// its status from the cluster watch. The row falls back to its watch-driven
// lifecycle status — the cluster
// watch keeps Running/Suspended/Failed/Gone fresh WITHOUT a forward; only
// runner-derived attention goes stale until a later focus/attention transition
// reconnects the stream on demand.
func (m *Model) evictObserver(id session.ID) {
	m.cancelLiveSSE(id) // closes the forward (C1) + prunes the recency entry
	for i := range m.sessions {
		if m.sessions[i].ID() == id {
			// No stream boundary (EventStreamLive) is coming, so release any armed
			// catch-up suppression rather than leave it stuck on the cold row.
			m.sessions[i].catchingUp = false
			// A runner-derived Busy has nothing left to flip it back (§1d H3: the
			// evicted row kept its spinner until focused) — stamp the watch-derived
			// baseline. Waiting is protected (never lands here) and NeedsInput stays
			// accurate without a stream, so Busy is the only stale-prone status.
			if m.sessions[i].DashStatus == StatusBusy {
				m.sessions[i].DashStatus = DeriveStatus(m.sessions[i].State)
				m.sessions[i].statusChangedAt = nowFunc()
			}
			break
		}
	}
	slog.Debug("evicted coldest observer stream to hold the cap",
		"session", id, "cap", m.observerCap(), "established", len(m.liveSSECancels))
}

// enforceObserverCap evicts the coldest unprotected observer streams until the
// established set is within the cap. keepID (the stream that just registered) is
// never chosen. When the protected set (attached + needs-attention) alone exceeds
// the cap the cap is deliberately overshot rather than blind an actionable
// session — a rare, bounded overshoot. Called at ready after a stream registers.
func (m *Model) enforceObserverCap(keepID session.ID) {
	for len(m.liveSSECancels) > m.observerCap() {
		victim := m.coldestObserver(keepID)
		if victim == "" {
			return
		}
		m.evictObserver(victim)
	}
}

// focusObserver keeps the focused session's observer stream warm and reconnects
// it on demand when it was evicted (or never admitted) while cold. Focus is the
// LRU "visible now" signal (touchObserver), so the freshly-focused session is
// never the eviction victim its own reconnect triggers at ready. Returns the
// connect Cmd, or nil when there is nothing to (re)establish.
func (m *Model) focusObserver(id session.ID) tea.Cmd {
	if id == "" {
		return nil
	}
	m.touchObserver(id)
	if m.connector == nil {
		return nil
	}
	s := m.sessionByID(id)
	if s.ID() == "" || s.State.Status != session.StatusRunning {
		return nil
	}
	// Already streamed (or the attached session, whose transcript owns the live
	// stream) → nothing to reconnect; the touch above is enough.
	if m.hasLiveSSE(id) || id == m.attachedID {
		return nil
	}
	// Matches the seed/watch launch path: opencode sessions also get an observer
	// stream (their Phase-4 metrics observer), just no retained Go transcript.
	return m.startLiveSSECmd(s)
}

// focusObserverSelected reconnects/keeps-warm the observer for the currently
// selected list row. The list-nav key handlers call it so moving the cursor onto
// a cold (evicted) session brings its live status back on demand.
func (m *Model) focusObserverSelected() tea.Cmd {
	if sel := m.selectedSession(); sel != nil {
		return m.focusObserver(sel.ID())
	}
	return nil
}

// attachGate lets a foreground attach/create connect take priority over the
// background observer connect burst (§5 leftover / §1d): while any foreground
// connect is in flight, observer connect goroutines pause at gate.wait() before
// acquiring a connectSem slot, so the kube-apiserver bandwidth goes to the attach
// the user is waiting on. The foreground path NEVER blocks on this gate — enter()
// only shuts it — so an attach can never be stuck behind a queue of observers.
type attachGate struct {
	mu     sync.Mutex
	active int
	open   chan struct{} // closed while no foreground connect is active
}

// newAttachGate returns a gate that starts open (idle: observers proceed).
func newAttachGate() *attachGate {
	ch := make(chan struct{})
	close(ch)
	return &attachGate{open: ch}
}

// enter marks a foreground connect as started, shutting the gate on the 0→1 edge.
// Never blocks.
func (g *attachGate) enter() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == 0 {
		g.open = make(chan struct{})
	}
	g.active++
}

// exit marks a foreground connect as finished, reopening the gate on the 1→0 edge.
func (g *attachGate) exit() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == 0 {
		return // defensive: unbalanced exit
	}
	g.active--
	if g.active == 0 {
		close(g.open)
	}
}

// wait blocks until no foreground connect is in flight. A nil gate (a Model built
// directly in a test) is a no-op so the observer path never deadlocks.
func (g *attachGate) wait() {
	if g == nil {
		return
	}
	g.mu.Lock()
	ch := g.open
	g.mu.Unlock()
	<-ch
}

// markWarm records that a session has a live background observer stream (warm),
// warning once when the set crosses the advisory soft limit. Idempotent.
func (m *Model) markWarm(id session.ID) {
	if id == "" {
		return
	}
	if m.warmSet == nil {
		m.warmSet = make(map[session.ID]struct{})
	}
	if _, ok := m.warmSet[id]; ok {
		return
	}
	m.warmSet[id] = struct{}{}
	if len(m.warmSet) > warmSoftLimit {
		slog.Warn("warm session set exceeds soft limit",
			"warm", len(m.warmSet), "softLimit", warmSoftLimit)
	}
}

// dropRetained removes a session from the warm set (warm→cold). Called when a
// pod suspends, is deleted, or its observer stream is exhausted.
func (m *Model) dropRetained(id session.ID) {
	delete(m.warmSet, id)
}

// warmCount is the number of warm sessions. Surfaced in the footer and logged;
// tracked, not enforced.
func (m *Model) warmCount() int { return len(m.warmSet) }

// idleRemaining returns how long until the reaper suspends a session that has
// been idle for `idleFor`, clamped to [0, timeout]. Zero means "due now".
func idleRemaining(timeout, idleFor time.Duration) time.Duration {
	if timeout <= 0 {
		return 0
	}
	rem := timeout - idleFor
	if rem < 0 {
		return 0
	}
	return rem
}

// roundDur is a compact, whole-unit duration string for the idle-soon hint
// ("45s", "12m") — precision the user doesn't need in a passive indicator.
func roundDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

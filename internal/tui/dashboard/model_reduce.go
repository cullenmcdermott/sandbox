package dashboard

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// saveSnapshot persists the session's live read-model through the snapshot store
// so a relaunch resumes from here. force (or a status transition) writes
// immediately; usage-only updates coalesce to snapshotSaveInterval to avoid
// thrashing the index on the high-frequency usage stream. No-op without a store.
func (m *Model) saveSnapshot(s *Session, force bool) {
	if m.snapStore == nil {
		return
	}
	if !force {
		if !s.lastSnapSave.IsZero() && time.Since(s.lastSnapSave) < snapshotSaveInterval {
			return
		}
	}
	s.lastSnapSave = time.Now()
	m.snapStore.SaveSnapshot(s.ID(), SessionSnapshot{
		LastSeq:               s.lastSeq,
		DashStatus:            s.DashStatus,
		PendingPermissionID:   s.PendingPermissionID,
		PendingPermissionTool: s.PendingPermissionTool,
		PendingPermissionArg:  s.PendingPermissionArg,
		Model:                 s.Model,
		InputTokens:           s.InputTokens,
		OutputTokens:          s.OutputTokens,
		CacheReadTokens:       s.CacheReadTokens,
		CacheWriteTokens:      s.CacheWriteTokens,
		TotalCostUSD:          s.TotalCostUSD,
		Branch:                s.Branch,
		Dirty:                 s.Dirty,
	})
}

// handleRunnerEvent applies a single SSE event to the relevant session in the
// read-model. If the stream ended, it degrades back to the cluster-derived
// status (idle/suspended/failed) without crashing.
//
// This single-event path is retained for tests that drive one RunnerEventMsg
// directly (and stays byte-for-byte identical to before). Production passive
// streams flow through handleRunnerEventBatch (§4 E5), which coalesces a delta
// burst into ONE Update+View; both share applyRunnerEvent / handleStreamEnded so
// the reduction semantics are identical — batching changes only the
// message/render granularity, never what each event does.
func (m *Model) handleRunnerEvent(msg RunnerEventMsg) (tea.Model, tea.Cmd) {
	// Stale-stream guard (§1a connect-side): a message tagged with a generation
	// that no longer matches this session's registered stream came from a
	// superseded or cancelled connect whose goroutine hadn't yet observed
	// cancellation. Ignore it — applying its event would double-apply, and acting
	// on its StreamEnded would tear down the healthy stream via cancelLiveSSE.
	// gen==0 is a test-synthesized / pre-generation message and always passes;
	// such messages set no liveSSEStreamGen entry either, so the ok check below is
	// skipped for them.
	if msg.gen != 0 {
		if cur, ok := m.liveSSEStreamGen[msg.ID]; !ok || cur != msg.gen {
			return m, nil
		}
	}
	if msg.StreamEnded {
		return m, m.handleStreamEnded(msg.ID)
	}
	m.applyRunnerEvent(msg.ID, msg.Event)
	// Re-issue the Cmd to read the next event from the stored channel. Carry the
	// same generation forward so the continuing reader stays tagged as this
	// stream (msg.gen matched the registered gen via the guard above).
	ch, ok := m.liveSSEChannels[msg.ID]
	if !ok {
		return m, nil // channel cancelled
	}
	return m, liveSSENextCmd(msg.ID, ch, msg.gen)
}

// handleRunnerEventBatch applies a burst of SSE events read from one passive
// stream in a SINGLE Update pass (§4 E5). The old per-event liveSSENextCmd cost
// one full Update+View pipeline PER event — 3-5 busy warm sessions ≈ 100-150
// render pipelines/sec, the multiplier that made every per-frame cost
// user-visible. liveSSEBatchCmd now drains a burst into one RunnerEventBatchMsg;
// we reduce every event here (identical per-event side effects) but render once.
//
// One channel is one generation, so the stale-stream guard gates the WHOLE batch
// with a single check. If the channel closed mid-drain (StreamEnded), the events
// read before the close are applied FIRST, then the stream-ended handling runs —
// no event read before the close is lost. The once-per-batch post-handling
// (maybeStartAnim, notifyIfBackgroundAttention) lives in the RunnerEventBatchMsg
// case in Update.
func (m *Model) handleRunnerEventBatch(msg RunnerEventBatchMsg) (tea.Model, tea.Cmd) {
	if msg.gen != 0 {
		if cur, ok := m.liveSSEStreamGen[msg.ID]; !ok || cur != msg.gen {
			return m, nil
		}
	}
	for _, ev := range msg.Events {
		m.applyRunnerEvent(msg.ID, ev)
	}
	if msg.StreamEnded {
		// Channel closed mid-drain: apply the drained events (above) THEN the
		// stream-ended degrade/reconnect — do not re-arm the reader.
		return m, m.handleStreamEnded(msg.ID)
	}
	// Re-arm the batch reader on the same channel + generation.
	if ch, ok := m.liveSSEChannels[msg.ID]; ok {
		return m, liveSSEBatchCmd(msg.ID, ch, msg.gen)
	}
	return m, nil
}

// handleStreamEnded degrades a session whose SSE stream closed: warm→cold drop,
// transient-blip reconnect on a still-Running pod, or authoritative degrade
// otherwise. Extracted so both the single- and batch-event paths (§4 E5) share
// identical stream-ended semantics. Returns a reconnect Cmd, or nil.
func (m *Model) handleStreamEnded(id session.ID) tea.Cmd {
	// warm→cold: a closed stream means the pod is no longer feeding us. Drop
	// the warm model unless the cluster still believes the pod is running (a
	// transient port-forward blip that the reconnect path below will retry).
	if s := m.sessionByID(id); s.State.Status != session.StatusRunning {
		m.dropRetained(id)
	}
	// Stream closed; clean up.
	m.cancelLiveSSE(id)
	delete(m.liveSSEChannels, id)
	var retryCmd tea.Cmd
	for i, s := range m.sessions {
		if s.ID() == id {
			// A stream drop is either a genuinely dead runner (B13: "runner
			// unreachable = failed") or just a transient port-forward blip on
			// a healthy pod (common with client-go SPDY forwards).
			if m.sessions[i].State.Status == session.StatusRunning {
				// Transient blip on a still-Running pod: PRESERVE the current
				// attention state — the Busy/Waiting glyph, a NeedsInput
				// awaiting-input state, and any pending permission — and retry
				// the background stream with backoff rather than flip to a scary
				// 'failed' glyph (RV1). Background streams had no reconnect path
				// at all before this. Preservation matters because the reconnect
				// replays after=lastSeq, which EXCLUDES the events that produced
				// this state (permission.requested, turn.completed→NeedsInput,
				// their Seq<=lastSeq) — so resetting to cluster-derived idle or
				// clearing the permission here would PERMANENTLY lose it (§1a
				// step 7; the old default branch reset NeedsInput→idle and the
				// Busy/Waiting branch wiped the permission → dead approve/deny).
				// If the state genuinely changed during the blip, the reconnect
				// replays the newer events at higher seqs and ApplyRunnerEvent
				// updates it; degradeUnreachable degrades only once the
				// reconnects are exhausted.
				retryCmd = liveSSEReconnectTick(id, 0, liveSSEReconnectDelay(0))
			} else {
				// Cluster says not-running (suspended/gone) — degrade to the
				// authoritative status and clear any now-unresolvable permission.
				// Release catch-up suppression too: the stream is gone for good,
				// so no EventStreamLive boundary will clear it, and a Failed
				// derived status must stay toastable.
				m.sessions[i].DashStatus = DeriveStatus(m.sessions[i].State)
				m.sessions[i].clearPendingPermission()
				m.sessions[i].catchingUp = false
			}
			// Persist the final state so a relaunch reflects it (and resumes
			// from the last seq we saw) rather than replaying from zero. A
			// preserved permission/attention state rides along in the snapshot
			// so it stays resolvable after a relaunch.
			m.saveSnapshot(&m.sessions[i], true)
			break
		}
	}
	return retryCmd
}

// applyRunnerEvent reduces a single non-StreamEnded SSE event into the read-model
// (status/usage/attention) for the list row and, while viewing, the activity
// feed. It runs every per-event side effect (seq dedup, touchObserver,
// ApplyRunnerEvent, snapshot throttling, title/session-id persistence, re-sort).
// The stale-generation guard is the CALLER's responsibility — one channel is one
// generation, so the batch path checks it once for the whole burst (§4 E5). This
// is the shared reduction body for handleRunnerEvent and handleRunnerEventBatch.
func (m *Model) applyRunnerEvent(id session.ID, event session.Event) {
	// Patch the session's status from this event.
	for i, s := range m.sessions {
		if s.ID() == id {
			// §1a step 3: clear the catch-up flag ONLY at the runner's
			// replay-complete boundary — EventStreamLive (Seq==0), which the runner
			// reliably emits as the LAST event of the after=<seq> replay burst
			// (runner/src/events.ts writes `: replay-complete`; client.go maps it).
			// A time-based "is this event fresh?" fallback was deliberately
			// REJECTED: on a short (~2s) reconnect the last replayed events carry
			// near-now timestamps and are indistinguishable from live ones, so a
			// freshness heuristic false-positives and leaks a spurious toast + OS
			// notification for an attention state resolved later in the same burst
			// (adversarial review, 2026-07-05).
			if m.sessions[i].catchingUp && event.Type == session.EventStreamLive {
				m.sessions[i].catchingUp = false
			}
			// Seq dedup (§1a step 2 — the list reducer's analog of the
			// transcript's lastSeq guard): an event at or below the resume cursor
			// is a REPLAY (a reconnect's after=lastSeq catch-up, or a duplicate
			// background stream). Do NOT re-drive read-model state
			// (DashStatus/statusChangedAt/usage/pending-permission), re-persist,
			// or re-arm attention for it — that is the relaunch-replay +
			// duplicate-stream bug class. Seq==0 events (EventStreamLive,
			// locally-synthesized markers) always pass through so downstream
			// boundary handling still sees them. The transcript self-dedups, so
			// the warm model is still fed below unconditionally.
			dup := event.Seq != 0 && event.Seq <= m.sessions[i].lastSeq
			var changed bool
			if !dup {
				// Live activity keeps this observer stream warm for the LRU cap
				// (§1d): a session mid-turn is the last one we want to evict, and a
				// session heading toward attention is busy first, so it stays warm
				// and never becomes the coldest victim before it goes Waiting.
				m.touchObserver(id)
				changed = ApplyRunnerEvent(&m.sessions[i], event)
				// Advance the resume cursor so a relaunch resumes from here
				// instead of replaying history.
				if event.Seq > m.sessions[i].lastSeq {
					m.sessions[i].lastSeq = event.Seq
				}
				// Keep the foreground session fully "seen" so it never accumulates
				// an unread badge for output the user is actively watching.
				if id == m.attachedID {
					m.sessions[i].seenSeq = m.sessions[i].lastSeq
				}
			}
			if !dup {
				m.saveSnapshot(&m.sessions[i], changed)
				// Persist a runner-generated auto title so it survives a re-seed
				// (the cluster state carries no local label). RenamedTitle still
				// wins at display time, so this is safe even for a renamed session.
				if event.Type == session.EventSessionTitle &&
					m.titleStore != nil && m.sessions[i].AutoTitle != "" {
					m.titleStore.SaveAutoTitle(id, m.sessions[i].AutoTitle)
				}
				// Persist the agent session id (session.started) so the CLI can make
				// the session resumable from the laptop on shutdown.
				if event.Type == session.EventSessionStarted &&
					m.titleStore != nil && m.sessions[i].AgentSessionID != "" {
					m.titleStore.SaveAgentSessionID(id, m.sessions[i].AgentSessionID)
				}
				if changed {
					m.sortSessions()
					m.clampCursor()
				}
			}
			break
		}
	}
}

// --------------------------------------------------------------------------
// State mutations
// --------------------------------------------------------------------------

func (m *Model) applySeed(states []session.State) (*Model, []tea.Cmd) {
	// Build a lookup of already-known sessions so we can preserve any
	// runner-derived status and avoid cancelling live SSE streams (B10:
	// seed/watch race). Seeds are concurrent with the watch; PodEventMsgs
	// may have already arrived and updated DashStatus/SSE before seedMsg.
	existingByID := make(map[session.ID]Session, len(m.sessions))
	for _, s := range m.sessions {
		existingByID[s.ID()] = s
	}

	m.sessions = make([]Session, 0, len(states))
	for _, st := range states {
		if st.Status == session.StatusGone {
			continue
		}
		s := SessionFromState(st)
		// Restore a persisted rename (T5): the seed comes from the cluster, which
		// doesn't carry the user's local label, so read it back from the store.
		if s.RenamedTitle == "" && m.titleStore != nil {
			s.RenamedTitle = m.titleStore.LoadTitle(s.ID())
		}
		// Restore the persisted runner-generated auto title (T6), same reasoning.
		if s.AutoTitle == "" && m.titleStore != nil {
			s.AutoTitle = m.titleStore.LoadAutoTitle(s.ID())
		}
		if prev, ok := existingByID[s.ID()]; ok {
			// Carry a rename forward across re-seeds even without a store (tests).
			if s.RenamedTitle == "" {
				s.RenamedTitle = prev.RenamedTitle
			}
			// Carry the auto title forward across re-seeds, too.
			if s.AutoTitle == "" {
				s.AutoTitle = prev.AutoTitle
			}
			// When the pod is suspended or failed, the cluster status is
			// authoritative: a stale runner-derived "busy/waiting" (and any
			// pending permission) can never be resolved here, so do not carry it
			// forward — otherwise a re-seed of a now-suspended session shows a
			// phantom "waiting" permission badge (C12). This mirrors applyPodEvent.
			clusterStatus := DeriveStatus(st)
			if clusterStatus == StatusSuspended || clusterStatus == StatusFailed {
				s.DashStatus = clusterStatus
				// s is fresh from SessionFromState, so PendingPermission* are
				// already empty here.
			} else {
				// Pod still running: preserve runner-derived fields so a late
				// seedMsg does not downgrade a session the watch already updated
				// to busy/waiting (B10).
				s.DashStatus = prev.DashStatus
				s.statusChangedAt = prev.statusChangedAt
				s.PendingPermissionID = prev.PendingPermissionID
				s.PendingPermissionTool = prev.PendingPermissionTool
				s.PendingPermissionArg = prev.PendingPermissionArg
				// Carry catch-up suppression forward (§1a fix 2): a seed arriving
				// mid-replay-burst must not silently disarm the flag and let a
				// replayed attention state toast. It still clears at the same
				// EventStreamLive boundary (or on degrade/teardown).
				s.catchingUp = prev.catchingUp
			}
			// Carry the SSE resume cursor + save throttle across re-seeds so a
			// later reconnect resumes from head, not 0 (which would replay).
			s.lastSeq = prev.lastSeq
			s.lastSnapSave = prev.lastSnapSave
			// Carry seenSeq too (§1a step 5): a cluster re-List must not reset it
			// to 0 while carrying lastSeq forward, or the row shows a phantom
			// lifetime-event-count unread badge after every re-seed.
			s.seenSeq = prev.seenSeq
			// The cluster List carries none of the SSE-accumulated live state, so
			// a re-seed would otherwise zero it (a phantom "just started" row):
			// carry usage/cost/model/branch/recent-tools forward like the cursor
			// above (§1b "re-seed drops usage tokens, Model, Branch, RecentTools").
			s.InputTokens, s.OutputTokens = prev.InputTokens, prev.OutputTokens
			s.CacheReadTokens, s.CacheWriteTokens = prev.CacheReadTokens, prev.CacheWriteTokens
			s.TotalCostUSD = prev.TotalCostUSD
			if prev.Model != "" {
				s.Model, s.CtxLimit = prev.Model, prev.CtxLimit
			}
			if prev.Branch != "" {
				s.Branch, s.Dirty = prev.Branch, prev.Dirty
			}
			if len(prev.RecentTools) > 0 {
				s.RecentTools = prev.RecentTools
			}
		} else if m.snapStore != nil {
			// First time we've seen this session this launch: hydrate the cached
			// snapshot so the row shows its real status/usage immediately and the
			// SSE stream resumes from the cached seq instead of replaying history.
			if snap, ok := m.snapStore.LoadSnapshot(s.ID()); ok {
				s.lastSeq = snap.LastSeq
				// Hydrated history is already-seen (§1a step 5) — set seenSeq here
				// too so a Suspended/Failed pod (which skips applySnapshot below)
				// still restores silently instead of showing a phantom unread badge.
				s.seenSeq = snap.LastSeq
				// Only trust the cached running-status while the cluster agrees the
				// pod is up; a suspended/failed pod's status is authoritative and a
				// stale "busy/waiting" can never resolve (mirrors the prev branch +
				// C12). We still keep lastSeq so any later stream resumes cleanly.
				if cs := DeriveStatus(st); cs != StatusSuspended && cs != StatusFailed {
					s.applySnapshot(snap)
					// Arm catch-up suppression from hydrate time (§1a fix 1): the
					// snapshot may restore a stale StatusWaiting + pending permission
					// seconds before the connectSem-throttled background connect is
					// ready, and a PodEventMsg in that window would otherwise toast it.
					// Released on every path where the stream's replay boundary can
					// no longer arrive: EventStreamLive itself, a failed initial
					// connect (liveSSEConnectFailedMsg), reconnect exhaustion
					// (degradeUnreachable), and the StreamEnded not-running branch —
					// so it can't stick on a session that never gets a stream.
					s.catchingUp = true
				}
			}
		}
		m.sessions = append(m.sessions, s)
	}
	m.sortSessions()
	m.clampCursor()
	m.seeded = true
	m.seedErr = nil // a successful seed proves the cluster is reachable

	// Start live SSE streams for running sessions that don't already have one —
	// neither registered (liveSSECancels) nor a connect in flight
	// (liveSSEConnecting). The in-flight check is what stops a seed running while
	// a watch-driven connect is mid-setup from launching a duplicate stream.
	var cmds []tea.Cmd
	if m.connector != nil {
		for i := range m.sessions {
			if m.sessions[i].State.Status == session.StatusRunning {
				id := m.sessions[i].ID()
				// Never launch for the attached session (§V46): its transcript owns
				// the live stream, so a background connect here would be immediately
				// torn down by the liveSSEReadyMsg attachedID guard — the same
				// start-then-cancel port-forward churn applyPodEvent skips.
				if id == m.attachedID {
					continue
				}
				// admitObserver holds the steady-state cap on the launch burst
				// (§1d): once at the cap, cold sessions stay on their watch-derived
				// row and reconnect on demand when focused/attended, instead of
				// establishing N>cap forwards.
				if !m.hasLiveSSE(id) && m.admitObserver(id) {
					cmds = append(cmds, m.startLiveSSECmd(m.sessions[i]))
				} else if m.sessions[i].catchingUp && !m.hasLiveSSE(id) {
					// Cap declined the launch: no stream starts, so the EventStreamLive
					// boundary that releases catch-up can never arrive — clear it now so
					// a restored attention state stays toastable (§V47).
					m.sessions[i].catchingUp = false
				}
			}
		}
	}
	return m, cmds
}

// mergeClusterState overlays the cluster-watch–derived fields onto an existing
// rich session state. The watch's sandboxToState carries lifecycle Status plus
// identity (id/sandbox/created/project/backend) but not the runner-derived
// descriptive fields (model/usage/last-activity), so a full replace would blank
// what the seed List or SSE stream populated. The watch is authoritative only
// for Status; the other fields are filled in only when the existing value is
// empty (so an identity learned later from the cluster isn't lost).
func mergeClusterState(existing, incoming session.State) session.State {
	merged := existing
	merged.Status = incoming.Status
	if merged.SandboxName == "" {
		merged.SandboxName = incoming.SandboxName
	}
	if merged.CreatedAt.IsZero() {
		merged.CreatedAt = incoming.CreatedAt
	}
	if merged.ProjectPath == "" {
		merged.ProjectPath = incoming.ProjectPath
	}
	if merged.WorkspacePath == "" {
		// [V11] The watch now recovers WorkspacePath (pod cwd / Mutagen alpha)
		// alongside ProjectPath, so carry it through: a session that first
		// appeared without it (seeded before the watch, or a legacy pod) gets
		// filled by a later watch event rather than staying blank.
		merged.WorkspacePath = incoming.WorkspacePath
	}
	if merged.Backend == "" {
		merged.Backend = incoming.Backend
	}
	return merged
}

// applyPodEvent patches the read-model for one cluster-watch event and returns
// any Cmd needed to start/stop a live SSE stream for the affected session.
func (m *Model) applyPodEvent(ev session.StateEvent) tea.Cmd {
	id := ev.State.ID
	if ev.Deleted || ev.State.Status == session.StatusGone {
		// Remove from the list and cancel its SSE stream.
		for i, s := range m.sessions {
			if s.ID() == id {
				m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
				break
			}
		}
		m.cancelLiveSSE(id)
		m.dropRetained(id)
		m.clampCursor()
		return nil
	}
	// Patch or insert.
	for i, s := range m.sessions {
		if s.ID() == id {
			// Preserve runner-derived status fields (PendingPermissionID, etc.)
			// and the descriptive fields the seed List populated — the watch
			// only carries Status + identity, so merge rather than replace.
			merged := mergeClusterState(m.sessions[i].State, ev.State)
			clusterStatus := DeriveStatus(merged)
			m.sessions[i].State = merged
			// Only reset to cluster-derived status if it's more restrictive
			// (suspended / failed) — don't overwrite busy/waiting/needs-input
			// with idle just because the pod is still "running".
			if clusterStatus == StatusSuspended || clusterStatus == StatusFailed {
				if m.sessions[i].DashStatus != clusterStatus {
					m.sessions[i].statusChangedAt = time.Now()
				}
				m.sessions[i].DashStatus = clusterStatus
				m.sessions[i].PendingPermissionID = ""
				m.sessions[i].PendingPermissionTool = ""
				m.sessions[i].PendingPermissionArg = ""
				// Release catch-up suppression (§V47): cancelLiveSSE below deletes the
				// stream gen, so the closing stream's StreamEnded fails the gen check
				// and handleStreamEnded's clearing path never runs — a still-armed
				// catchingUp would then make notifyIfBackgroundAttention skip this row
				// forever, permanently suppressing the terminal Failed attention toast.
				m.sessions[i].catchingUp = false
				m.cancelLiveSSE(id)
				m.dropRetained(id)
			} else if ev.State.Status == session.StatusRunning && m.sessions[i].DashStatus == StatusSuspended {
				// Pod just resumed: reset to idle and start SSE.
				m.sessions[i].DashStatus = StatusIdle
				m.sessions[i].statusChangedAt = time.Now()
			}
			m.sessions[i].Title = deriveTitle(merged)
			m.sortSessions()
			m.clampCursor()
			// Start a background SSE stream if the session is now Running and
			// lacks one — but never for the attached session: its transcript owns
			// the live stream, so a background connect here would be immediately
			// torn down by the liveSSEReadyMsg attachedID guard. Skip the
			// start-then-cancel connect/port-forward churn on every pod event.
			if ev.State.Status == session.StatusRunning && m.connector != nil &&
				id != m.attachedID {
				if !m.hasLiveSSE(id) && m.admitObserver(id) {
					return m.startLiveSSECmd(m.sessions[i])
				}
			}
			return nil
		}
	}
	// New session appeared — fade its glyph in.
	sess := SessionFromState(ev.State)
	sess.statusChangedAt = time.Now()
	// §1a step 6: hydrate persisted titles + snapshot BEFORE appending and
	// starting the SSE stream. The watch can beat the seed
	// (internal/k8s/watch.go documents "seed before watch"); without hydration
	// this insert path leaves lastSeq=0, so the stream started just below resumes
	// at after=0 and replays the ENTIRE history as if live (launch-time
	// notification flashes, usage counted from zero). Mirror applySeed's
	// first-seen hydration so an informer-first insert resumes from head.
	if m.titleStore != nil {
		if sess.RenamedTitle == "" {
			sess.RenamedTitle = m.titleStore.LoadTitle(sess.ID())
		}
		if sess.AutoTitle == "" {
			sess.AutoTitle = m.titleStore.LoadAutoTitle(sess.ID())
		}
	}
	if m.snapStore != nil {
		if snap, ok := m.snapStore.LoadSnapshot(sess.ID()); ok {
			sess.lastSeq = snap.LastSeq
			sess.seenSeq = snap.LastSeq
			// C12: only trust a cached running-status while the cluster agrees the
			// pod is up (a suspended/failed pod's status is authoritative).
			if cs := DeriveStatus(ev.State); cs != StatusSuspended && cs != StatusFailed {
				sess.applySnapshot(snap)
				// Arm catch-up suppression from hydrate time (§1a fix 1): mirror
				// applySeed so an informer-first insert that restores a stale
				// attention state can't toast before its background stream reaches
				// the EventStreamLive boundary. Same release paths as applySeed's
				// arm (connect-failed / degrade / not-running teardown).
				sess.catchingUp = true
			}
		}
	}
	m.sessions = append(m.sessions, sess)
	m.sortSessions()
	m.clampCursor()
	if ev.State.Status == session.StatusRunning && m.connector != nil &&
		!m.hasLiveSSE(sess.ID()) {
		if m.admitObserver(sess.ID()) {
			return m.startLiveSSECmd(sess)
		}
		// Cap declined the launch: no stream, so the EventStreamLive boundary that
		// releases catch-up can never arrive. Clear it on the stored row (sess was
		// copied into m.sessions above, then re-sorted) so a restored attention
		// state stays toastable — mirrors applySeed's launch loop (§V47).
		for i := range m.sessions {
			if m.sessions[i].ID() == sess.ID() {
				m.sessions[i].catchingUp = false
				break
			}
		}
	}
	return nil
}

// reconcile prunes sessions that have disappeared from the cluster but whose
// delete the watch informer never delivered (it can miss a delete that landed
// before its cache synced, leaving a phantom row forever — T5). It only REMOVES;
// the watch owns adds, so a session present in the cluster but missing locally is
// left for the watch to insert.
//
// To avoid racing with a just-created session the List snapshot predates (the
// watch adds it; this snapshot, taken earlier, wouldn't include it), a session is
// dropped only after it's been absent from two consecutive re-lists.
func (m *Model) reconcile(states []session.State) {
	present := make(map[session.ID]bool, len(states))
	for _, st := range states {
		present[st.ID] = true
	}
	if m.reconcileMisses == nil {
		m.reconcileMisses = make(map[session.ID]int)
	}

	dropped := false
	kept := m.sessions[:0]
	for _, s := range m.sessions {
		id := s.ID()
		if present[id] {
			delete(m.reconcileMisses, id)
			kept = append(kept, s)
			continue
		}
		// Absent from the cluster: one grace cycle before dropping.
		m.reconcileMisses[id]++
		if m.reconcileMisses[id] < 2 {
			kept = append(kept, s)
			continue
		}
		delete(m.reconcileMisses, id)
		m.cancelLiveSSE(id)
		m.dropRetained(id)
		dropped = true
	}
	m.sessions = kept

	// Prune miss counters for sessions no longer tracked (e.g. removed by the
	// watch between cycles) so the map can't grow unbounded.
	if len(m.reconcileMisses) > 0 {
		live := make(map[session.ID]bool, len(m.sessions))
		for _, s := range m.sessions {
			live[s.ID()] = true
		}
		for id := range m.reconcileMisses {
			if !live[id] {
				delete(m.reconcileMisses, id)
			}
		}
	}

	if dropped {
		m.sortSessions()
		m.clampCursor()
	}
}

func (m *Model) sortSessions() {
	SortSessions(m.sessions, m.sortKey, m.sortDir)
}

func (m *Model) clampCursor() {
	// Clamp against display rows (headers included in group view) — the cursor
	// indexes rows, not sessions, so clamping against visibleSessions would cut
	// off the tail of a grouped list.
	rows := m.visibleRows()
	if m.cursor >= len(rows) {
		m.cursor = max(0, len(rows)-1)
	}
}

// visibleSessions returns the filtered+sorted subset of sessions to display.
func (m *Model) visibleSessions() []Session {
	q := m.filter
	if m.filtering {
		q = m.filterBuf
	}
	return sortByAttention(FilterSessions(m.sessions, q), m.attentionFirst)
}

// selectedSession returns the currently highlighted session, or nil. It is the
// single accessor for "the session under the cursor" — sessionAt(m.cursor). The
// cursor indexes display rows (which include repo headers in group view), so
// indexing visibleSessions directly would describe/act on the wrong session
// whenever a header sits above the cursor.
func (m *Model) selectedSession() *Session {
	return m.sessionAt(m.cursor)
}

// sessionByID returns the Session with the given ID from the dashboard's session
// list, or a zero Session when not found. Used by App to restart background SSE
// after detach (B2).
func (m *Model) sessionByID(id session.ID) Session {
	for _, s := range m.sessions {
		if s.ID() == id {
			return s
		}
	}
	return Session{}
}

// syncCursorFromTranscript advances a session's resume cursor to the transcript's
// position (and marks it all seen) when the user DETACHES. While attached, the
// foreground transcript owns the live stream and the dashboard Session.lastSeq
// stays frozen; without this, the background stream restarted on detach resumes
// at the stale cursor and REPLAYS the just-watched events — advancing lastSeq but
// not seenSeq, which inflates the unread badge for content the user just viewed
// live (§1a adversarial review, 2026-07-05). Syncing the cursor also skips
// re-replaying those events entirely.
func (m *Model) syncCursorFromTranscript(id session.ID, trLastSeq uint64) {
	for i := range m.sessions {
		if m.sessions[i].ID() == id {
			if trLastSeq > m.sessions[i].lastSeq {
				m.sessions[i].lastSeq = trLastSeq
			}
			m.sessions[i].seenSeq = m.sessions[i].lastSeq // watched live during attach
			break
		}
	}
}

// parkReadModelFromTranscript copies the detaching transcript's derived read-model
// (DashStatus, the pending-permission descriptor, usage/model/branch — everything
// ApplyEvent derived while the transcript owned the live stream) back onto the
// dashboard Session, then force-saves the snapshot [V5]. Call it AFTER
// syncCursorFromTranscript so the forced snapshot rides the advanced cursor.
//
// While attached the background observer stream is cancelled (handleAttachReady),
// so the dashboard Session's read-model is FROZEN at attach time while the
// transcript's advances. syncCursorFromTranscript then moves the resume cursor
// past every event that produced that advanced state, and the reconnect replays
// only events > lastSeq — so the state must be CARRIED here, not re-derived, or a
// permission requested (or a turn that completed) during attach is lost forever:
// the row would stay Busy with an empty pending-permission descriptor, no
// attention float, no toast, while the agent sits blocked. This is the same
// carry-don't-re-derive invariant handleStreamEnded documents for the reconnect
// path. Both Session and TranscriptModel embed the identical sessionReadModel, so
// the copy is wholesale — except that a cluster-authoritative terminal status set
// by the watch mid-attach (suspend/fail) must survive: the transcript's
// runner-derived status is stale in that case (mirrors applySeed/applyPodEvent).
func (m *Model) parkReadModelFromTranscript(id session.ID, rm sessionReadModel) {
	for i := range m.sessions {
		if m.sessions[i].ID() == id {
			if s := m.sessions[i].DashStatus; s != StatusSuspended && s != StatusFailed {
				m.sessions[i].sessionReadModel = rm
			}
			m.saveSnapshot(&m.sessions[i], true)
			break
		}
	}
}

package dashboard

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// cacheableEvent reports whether an event belongs in the host-side transcript
// cache. Synthetic stream markers (EventStreamLive, seq 0) are excluded, and the
// high-volume incremental deltas are skipped — replay rebuilds final state from
// the started/completed events, so caching deltas would only bloat the file.
func cacheableEvent(ev session.Event) bool {
	if ev.Seq == 0 {
		return false
	}
	switch ev.Type {
	case session.EventMessageDelta, session.EventReasoningDelta, session.EventToolDelta:
		return false
	}
	return true
}

// maybeCache appends an event to the host-side cache exactly once, in seq order.
// Used by BOTH the foreground stream (tEventMsg) and the warm background feed
// (ingest): a session is fed by exactly one of those at a time (attach cancels
// the background stream, detach cancels the foreground stream), and the
// lastCachedSeq guard covers the brief handoff race. Best effort — a write
// failure never breaks the turn (the runner's events.db is authoritative).
func (m *TranscriptModel) maybeCache(ev session.Event) {
	if m.cache == nil || !cacheableEvent(ev) || ev.Seq <= m.lastCachedSeq {
		return
	}
	if err := m.cache.AppendEvent(m.ref.ID, ev); err == nil {
		m.lastCachedSeq = ev.Seq
	}
}

// finalizeStreaming commits any buffered streaming text as a final assistant
// block (covers turns that end without a message.completed) and returns that
// block's index, or -1 if nothing was committed.
func (m *TranscriptModel) finalizeStreaming() int {
	if m.streaming && strings.TrimSpace(m.assistantBuf.String()) != "" {
		m.streaming = false
		text := m.assistantBuf.String()
		m.assistantBuf.Reset()
		m.streamAI = nil
		m.appendBlock(blockAssistant, text)
		return len(m.blocks) - 1
	}
	m.streaming = false
	m.assistantBuf.Reset()
	m.streamAI = nil
	return -1
}

// --------------------------------------------------------------------------
// SSE stream + reconnect (mirrors tui.Model)
// --------------------------------------------------------------------------

// loadCachedTranscript rebuilds the transcript from the host-side cache on a cold
// open (Workstream C), advancing lastSeq to the cached head so the stream resumes
// from there. Guarded to a genuinely cold model (no blocks, no cursor): a warm
// model promoted to the foreground already holds its history, so re-loading would
// duplicate it. The replayed events feed the SAME handleEvent the live stream
// uses (so blocks/state stay identical), but are NOT re-written to the cache.
func (m *TranscriptModel) loadCachedTranscript() {
	if m.cache == nil || len(m.blocks) > 0 || m.lastSeq > 0 {
		return
	}
	events, err := m.cache.LoadEvents(m.ref.ID)
	if err != nil || len(events) == 0 {
		return
	}
	// Replay the cache synchronously (no UI shown yet); startEventStream then sets
	// the replay/loading state from the watermark for the remaining delta.
	//
	// Bulk mode: apply every event to m.blocks WITHOUT reconciling the list per
	// event — a naive replay calls syncBody→reconcileItems once per event, and
	// each reconcile re-fingerprints all prior items (hashing each block's full
	// text) and rebuilds the item set, making the cold load O(N^2). Suppress that,
	// then reconcile exactly once after the loop, so replay is O(N).
	m.bulkReplay = true
	for i := range events {
		_ = m.handleEvent(events[i])
		if events[i].Seq > m.lastCachedSeq {
			m.lastCachedSeq = events[i].Seq // already on disk; don't re-append
		}
	}
	m.bulkReplay = false
	m.syncBody()
}

func (m *TranscriptModel) startEventStream() tea.Cmd {
	// Tear down any prior stream first (e.g. on reconnect) so we don't leak its
	// context/connection (NEW-5).
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
	// Enter replay ONLY when there is history to catch up to (the dashboard's
	// cursor is ahead of what we've rendered). A fresh session (attachSeq 0) or a
	// warm reattach already caught up never shows "loading transcript…". The state
	// clears at the watermark (handleEvent) or the runner's replay-complete marker,
	// whichever comes first — so it self-clears even against an older runner.
	m.replaying = m.attachSeq > m.lastSeq
	m.replayedCount = 0
	ctx, cancel := context.WithCancel(context.Background())
	events, err := m.client.Events(ctx, m.ref, m.lastSeq)
	if err != nil {
		cancel()
		// The stream never opened (non-200 from /events, or the port-forward
		// died between the connect health-check and here). Route into the same
		// reconnect path a mid-stream drop uses — which shows "[connection lost
		// — reconnecting…]" and retries — instead of returning nil and leaving
		// a connected-looking but inert transcript that receives no events and
		// never recovers.
		return func() tea.Msg { return tStreamEndedMsg{} }
	}
	m.events = events
	m.streamCancel = cancel
	return m.waitForEvent()
}

// cancelStream tears down the transcript's live SSE stream. The App must call
// this before releasing the transcript on detach (NEW-5); otherwise the runner
// keeps a second SSE client open until GC / runner-side close, briefly defeating
// the "exactly one SSE client" intent (B2) and the idle-reaper accounting.
func (m *TranscriptModel) cancelStream() {
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
	m.events = nil
}

// eventBatchMax caps how many buffered events one waitForEvent collapses into a
// single batch, so a relentless stream still yields to the render/key loop
// between batches rather than spinning the drain forever.
const eventBatchMax = 512

// waitForEvent returns a Cmd that blocks for the next event, then
// non-blockingly drains any already-buffered events into one batch. Coalescing
// a burst of stream deltas into a single Update+View is what keeps a fast turn
// from re-rendering per delta and starving keystrokes (T1 lag). If the channel
// closes mid-drain the batch is delivered as-is; the next waitForEvent's
// blocking receive then surfaces tStreamEndedMsg.
//
// The channel is captured HERE, on the Update goroutine: the returned closure
// runs concurrently with later Updates that nil/replace m.events
// (cancelStream / startEventStream), so reading the field inside the closure
// would be a data race.
func (m *TranscriptModel) waitForEvent() tea.Cmd {
	ch := m.events
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return tStreamEndedMsg{}
		}
		batch := []session.Event{ev}
		for len(batch) < eventBatchMax {
			select {
			case ev, ok := <-ch:
				if !ok {
					return tEventBatchMsg(batch)
				}
				batch = append(batch, ev)
			default:
				return tEventBatchMsg(batch)
			}
		}
		return tEventBatchMsg(batch)
	}
}

// reconnectVerboseAttempts is how many failed reconnects emit a transcript line
// before the loop goes quiet (it keeps retrying at the capped backoff).
const reconnectVerboseAttempts = 3

// reconnectBackoff returns the delay before reconnect attempt n: 3s, 6s, 12s,
// 24s, then capped at 30s — so a transient blip heals fast while a dead session
// backs off instead of hammering the connector every 3s forever (RV29).
func reconnectBackoff(attempt int) time.Duration {
	d := 3 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= 30*time.Second {
			return 30 * time.Second
		}
	}
	return d
}

// reconnectAttemptTimeout bounds a single reconnect attempt. It must comfortably
// exceed a cold-pod resume (schedule + image pull + boot + 30s runner health),
// which can run past two minutes on a cold node, or a legitimate slow resume
// would be cut short and bounced into the backoff loop. Retries continue after
// it; the give-up is driven by error classification (ErrSessionGone), not this.
const reconnectAttemptTimeout = 180 * time.Second

// reconnectStageMsg carries one connect-stage update from the in-flight
// doReconnect into the Update loop. done=true signals the stage channel closed
// (the attempt finished) so the waiter stops.
type reconnectStageMsg struct {
	stage  ConnectStage
	detail string
	done   bool
}

// startReconnect opens a fresh stage channel and launches both the reconnect
// attempt and the stage drainer, so the header can show live resume progress.
func (m *TranscriptModel) startReconnect() tea.Cmd {
	m.reconnectStages = make(chan reconnectStageMsg, 8)
	m.reconnectStage = 0
	m.reconnectDetail = ""
	m.reconnectStageKnown = false
	return tea.Batch(m.doReconnect(), m.waitForReconnectStage())
}

// waitForReconnectStage returns a Cmd that drains one stage update from the
// current reconnect's channel, mirroring waitForEvent. It re-subscribes (via
// the Update handler) until doReconnect closes the channel. The channel is
// captured on the Update goroutine (see waitForEvent for why).
func (m *TranscriptModel) waitForReconnectStage() tea.Cmd {
	ch := m.reconnectStages
	return func() tea.Msg {
		if ch == nil {
			return reconnectStageMsg{done: true}
		}
		msg, ok := <-ch
		if !ok {
			return reconnectStageMsg{done: true}
		}
		return msg
	}
}

// doReconnect returns a Cmd running one reconnect attempt. m.reconnect and the
// stage channel are captured on the Update goroutine (see waitForEvent).
func (m *TranscriptModel) doReconnect() tea.Cmd {
	reconnect := m.reconnect
	ch := m.reconnectStages
	return func() tea.Msg {
		if reconnect == nil {
			return tReconnectFailedMsg{err: fmt.Errorf("no reconnect available")}
		}
		onStage := func(s ConnectStage, detail string) {
			if ch == nil {
				return
			}
			// Non-blocking: never stall the reconnect on a slow/absent UI drainer.
			select {
			case ch <- reconnectStageMsg{stage: s, detail: detail}:
			default:
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), reconnectAttemptTimeout)
		defer cancel()
		client, err := reconnect(ctx, onStage)
		if ch != nil {
			close(ch) // unblock the stage waiter; this attempt is done
		}
		if err != nil {
			return tReconnectFailedMsg{err: err}
		}
		return tReconnectedMsg{client: client}
	}
}

// startTurnCmd posts a new turn and surfaces a synchronous start failure; the
// turn itself streams back over SSE.
func startTurnCmd(client RunnerClient, ref session.Ref, prompt, mode, model, effort string, advisor bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := client.StartTurn(ctx, ref, session.TurnInput{Prompt: prompt, Mode: mode, Model: model, Effort: effort, Advisor: advisor}); err != nil {
			return turnErrMsg{err: err}
		}
		return nil
	}
}

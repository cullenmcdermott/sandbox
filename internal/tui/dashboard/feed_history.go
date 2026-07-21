package dashboard

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Feed history replay (§1h L3): the read-only activity feed opens against a
// session whose prior events live only in the runner's SQLite log — there is
// no host-side transcript store (the old EventCache had a reader but no
// production writer). On open, the App fetches history through a ONE-SHOT
// passive SSE replay from seq 0 and seeds the feed with it. The from-zero
// replay is deliberately scoped to this fetch: the dashboard read-model's
// long-lived stream keeps resuming from lastSeq (see startLiveSSECmd), so the
// launch-time notification flashing / usage double-count that after=lastSeq
// exists to prevent cannot return through this path.

// feedHistoryMaxEvents bounds how much replayed history the feed retains. The
// runner already streams the replay in bounded chunks (E2); this cap only
// keeps a very long session from bloating the feed model — the feed renders a
// tail, so the oldest events beyond the cap are dropped.
const feedHistoryMaxEvents = 2000

// feedHistoryReadTimeout bounds the replay read phase. If the runner has not
// delivered the replay-complete marker by then, the feed seeds whatever
// arrived — partial history beats an empty feed.
const feedHistoryReadTimeout = 15 * time.Second

// feedPendingLiveCap bounds the live-event buffer held while the history
// fetch is in flight (see App.feedPendingLive). On overflow the oldest are
// dropped; a successful replay re-covers them by seq anyway.
const feedPendingLiveCap = 1024

// feedHistoryMsg delivers the one-shot replay result to the App. gen guards
// against a stale fetch landing on a feed opened later.
type feedHistoryMsg struct {
	id     session.ID
	gen    uint64
	events []session.Event
	// complete reports that the replay-complete marker was seen; false means
	// the stream died or timed out mid-replay and events hold a prefix.
	complete bool
	err      error
}

// feedHistoryCmd fetches a session's event history via a one-shot passive SSE
// stream from seq 0, in a background Cmd. It mirrors startLiveSSECmd's connect
// discipline: attach-gate yield, connect-slot throttle for the expensive
// setup only, and the §1d C1 close-the-forward contract on every path.
func (a *App) feedHistoryCmd(sess Session, gen uint64) tea.Cmd {
	connector := a.dashboard.backgroundConnector()
	id := sess.ID()
	if connector == nil || id == "" {
		return func() tea.Msg { return feedHistoryMsg{id: id, gen: gen} }
	}
	ref := session.Ref{ID: id}
	projectPath := sess.State.ProjectPath
	sem := a.dashboard.connectSem
	gate := a.dashboard.attachGate
	return func() tea.Msg {
		gate.wait()
		release := acquireConnectSlot(sem)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		res, err := connector(ctx, ref, projectPath, func(ConnectStage, string) {})
		if err != nil {
			release()
			return feedHistoryMsg{id: id, gen: gen, err: err}
		}
		ch, err := res.Client.EventsPassive(ctx, ref, 0)
		release()
		if err != nil {
			if res.Close != nil {
				res.Close()
			}
			return feedHistoryMsg{id: id, gen: gen, err: err}
		}
		events, complete := collectFeedHistory(ch, time.After(feedHistoryReadTimeout), feedHistoryMaxEvents)
		cancel() // one-shot fetch: stop the stream before releasing the forward
		if res.Close != nil {
			res.Close()
		}
		return feedHistoryMsg{id: id, gen: gen, events: events, complete: complete}
	}
}

// collectFeedHistory drains a replay stream until the client-internal
// EventStreamLive marker (replay complete), channel close, or the deadline —
// whichever first — retaining at most maxEvents of the TAIL. Pure so the
// cap/termination behavior is unit-testable without a transport.
func collectFeedHistory(ch <-chan session.Event, deadline <-chan time.Time, maxEvents int) ([]session.Event, bool) {
	var events []session.Event
	trim := func() []session.Event {
		if len(events) > maxEvents {
			return append(events[:0], events[len(events)-maxEvents:]...)
		}
		return events
	}
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return trim(), false
			}
			if ev.Type == session.EventStreamLive {
				return trim(), true
			}
			events = append(events, ev)
			// Halve when doubled so a very long replay stays O(n) overall.
			if len(events) > 2*maxEvents {
				events = append(events[:0], events[len(events)-maxEvents:]...)
			}
		case <-deadline:
			return trim(), false
		}
	}
}

// feedDeliver routes one live tap event to the open feed. While the history
// fetch is in flight the event is buffered instead: the feed dedups by seq,
// so seeding AFTER a live ingest of higher seqs would drop the entire
// history — live events wait and re-apply after the seed (the seq overlap
// between the buffer and the replay tail dedups harmlessly).
func (a *App) feedDeliver(ev session.Event) {
	if a.feedAwaitingHistory {
		a.feedPendingLive = append(a.feedPendingLive, ev)
		if len(a.feedPendingLive) > feedPendingLiveCap {
			a.feedPendingLive = a.feedPendingLive[1:]
		}
		return
	}
	a.feed.ingest(ev)
}

// handleFeedHistory applies a one-shot replay result: seed the feed, then
// re-apply any live events buffered while the fetch ran. A result for a feed
// that is no longer the open one (closed, or reopened → new gen) is dropped.
func (a *App) handleFeedHistory(msg feedHistoryMsg) (tea.Model, tea.Cmd) {
	if a.feed == nil || msg.gen != a.feedHistoryGen || msg.id != a.feed.ref.ID {
		return a, nil
	}
	a.feedAwaitingHistory = false
	pending := a.feedPendingLive
	a.feedPendingLive = nil
	switch {
	case msg.err != nil:
		a.feed.notice("History unavailable — showing live activity only")
	default:
		a.feed.seed(msg.events)
		if !msg.complete {
			a.feed.notice("History replay incomplete")
		}
	}
	for _, ev := range pending {
		a.feed.ingest(ev)
	}
	return a, nil
}

// resetFeedHistory clears the fetch-in-flight state when the feed closes; the
// gen guard in handleFeedHistory makes the in-flight Cmd's eventual result a
// no-op (a reopen mints a new gen in handleViewFeed).
func (a *App) resetFeedHistory() {
	a.feedAwaitingHistory = false
	a.feedPendingLive = nil
}

package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// historyPrompt builds a turn.started event (the feed renders it as a user
// entry) for history/live crafting.
func historyPrompt(seq uint64, prompt string) session.Event {
	return feedEvent(seq, session.EventTurnStarted, session.TurnStartedPayload{Prompt: prompt})
}

// TestCollectFeedHistoryCompleteOnLiveMarker: events up to the client-internal
// EventStreamLive marker are returned with complete=true; the marker itself is
// not part of the history.
func TestCollectFeedHistoryCompleteOnLiveMarker(t *testing.T) {
	ch := make(chan session.Event, 4)
	ch <- historyPrompt(1, "a")
	ch <- historyPrompt(2, "b")
	ch <- session.Event{Type: session.EventStreamLive}
	events, complete := collectFeedHistory(ch, nil, 100)
	if !complete {
		t.Fatal("want complete=true at the stream-live marker")
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("events = %+v, want seqs 1,2", events)
	}
}

// TestCollectFeedHistoryPartialOnClose: a channel that closes before the live
// marker (stream died mid-replay) yields the partial prefix, complete=false.
func TestCollectFeedHistoryPartialOnClose(t *testing.T) {
	ch := make(chan session.Event, 2)
	ch <- historyPrompt(1, "a")
	close(ch)
	events, complete := collectFeedHistory(ch, nil, 100)
	if complete {
		t.Fatal("want complete=false on early close")
	}
	if len(events) != 1 || events[0].Seq != 1 {
		t.Fatalf("events = %+v, want the partial prefix seq 1", events)
	}
}

// TestCollectFeedHistoryDeadline: with nothing arriving, an expired deadline
// terminates the collect with what has arrived so far.
func TestCollectFeedHistoryDeadline(t *testing.T) {
	ch := make(chan session.Event) // never delivers
	deadline := make(chan time.Time, 1)
	deadline <- time.Time{}
	events, complete := collectFeedHistory(ch, deadline, 100)
	if complete || len(events) != 0 {
		t.Fatalf("events=%v complete=%v, want empty incomplete on deadline", events, complete)
	}
}

// TestCollectFeedHistoryKeepsTail: a replay longer than the cap retains the
// most recent maxEvents, ending at the true last event.
func TestCollectFeedHistoryKeepsTail(t *testing.T) {
	const total, cap = 50, 8
	ch := make(chan session.Event, total+1)
	for i := 1; i <= total; i++ {
		ch <- historyPrompt(uint64(i), fmt.Sprintf("p%d", i))
	}
	ch <- session.Event{Type: session.EventStreamLive}
	events, complete := collectFeedHistory(ch, nil, cap)
	if !complete {
		t.Fatal("want complete=true")
	}
	if len(events) != cap {
		t.Fatalf("len = %d, want the %d-event tail", len(events), cap)
	}
	if events[len(events)-1].Seq != total || events[0].Seq != total-cap+1 {
		t.Fatalf("tail spans %d..%d, want %d..%d", events[0].Seq, events[len(events)-1].Seq, total-cap+1, total)
	}
}

// TestFeedSeedThenLiveOverlapDedups pins the invariant handleFeedHistory
// relies on: live events that overlap the replay tail by seq are dropped by
// the feed reducer when applied AFTER the seed.
func TestFeedSeedThenLiveOverlapDedups(t *testing.T) {
	m := newFeedModel(session.Ref{ID: "s"}, "s", "claude")
	m.SetSize(80, 20)
	m.seed([]session.Event{historyPrompt(1, "one"), historyPrompt(2, "two")})
	m.ingest(historyPrompt(2, "two"))   // replay overlap — must dedup
	m.ingest(historyPrompt(3, "three")) // genuinely new
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3 (one/two/three, no dup of 'two')", len(m.items))
	}
}

// newFeedHistoryTestApp builds an App with an open feed awaiting history, the
// state handleViewFeed leaves behind (minus the async Cmds).
func newFeedHistoryTestApp(id session.ID) *App {
	a := &App{screen: ScreenFeed, dashboard: New(nil), width: 80, height: 24}
	a.feed = newFeedModel(session.Ref{ID: id}, "t", "claude")
	a.feed.SetSize(80, 24)
	a.feedHistoryGen = 1
	a.feedAwaitingHistory = true
	return a
}

// TestFeedHistoryBuffersLiveThenSeeds: live tap events arriving while the
// fetch is in flight are buffered (NOT ingested — that would seq-dedup-drop
// the later seed), then re-applied after the seed with the overlap deduped.
func TestFeedHistoryBuffersLiveThenSeeds(t *testing.T) {
	a := newFeedHistoryTestApp("s1")

	// Live events land mid-fetch: one overlapping the replay tail, one beyond it.
	a.tapFeed(RunnerEventMsg{ID: "s1", Event: historyPrompt(2, "two")})
	a.tapFeed(RunnerEventBatchMsg{ID: "s1", Events: []session.Event{historyPrompt(3, "three")}})
	if len(a.feed.items) != 0 {
		t.Fatalf("live events must buffer while history is in flight; feed has %d items", len(a.feed.items))
	}
	if len(a.feedPendingLive) != 2 {
		t.Fatalf("pending = %d, want 2", len(a.feedPendingLive))
	}

	a.handleFeedHistory(feedHistoryMsg{
		id: "s1", gen: 1, complete: true,
		events: []session.Event{historyPrompt(1, "one"), historyPrompt(2, "two")},
	})
	if a.feedAwaitingHistory || a.feedPendingLive != nil {
		t.Fatal("history arrival must clear the awaiting state and buffer")
	}
	if len(a.feed.items) != 3 {
		t.Fatalf("items = %d, want 3 (history one/two + live three, overlap deduped)", len(a.feed.items))
	}
}

// TestFeedHistoryStaleResultIgnored: a result for a closed/reopened feed
// (gen or id mismatch, or no feed at all) is dropped without touching state.
func TestFeedHistoryStaleResultIgnored(t *testing.T) {
	a := newFeedHistoryTestApp("s1")
	a.handleFeedHistory(feedHistoryMsg{id: "s1", gen: 99, events: []session.Event{historyPrompt(1, "x")}})
	if !a.feedAwaitingHistory || len(a.feed.items) != 0 {
		t.Fatal("stale-gen result must be ignored")
	}
	a.handleFeedHistory(feedHistoryMsg{id: "other", gen: 1, events: []session.Event{historyPrompt(1, "x")}})
	if !a.feedAwaitingHistory || len(a.feed.items) != 0 {
		t.Fatal("wrong-session result must be ignored")
	}
	a.feed = nil
	a.resetFeedHistory()
	// Must not panic with no feed open.
	a.handleFeedHistory(feedHistoryMsg{id: "s1", gen: 1})
}

// TestFeedHistoryErrorFlushesPendingWithNotice: on fetch failure the feed
// degrades to live-only — buffered events flush and a calm notice explains.
func TestFeedHistoryErrorFlushesPendingWithNotice(t *testing.T) {
	a := newFeedHistoryTestApp("s1")
	a.tapFeed(RunnerEventMsg{ID: "s1", Event: historyPrompt(5, "live")})
	a.handleFeedHistory(feedHistoryMsg{id: "s1", gen: 1, err: fmt.Errorf("connect failed")})
	if a.feedAwaitingHistory {
		t.Fatal("error must clear the awaiting state")
	}
	var haveNotice, haveLive bool
	for _, it := range a.feed.items {
		if it.kind == feedNotice && strings.Contains(it.text, "History unavailable") {
			haveNotice = true
		}
		if it.kind == feedUser && strings.Contains(it.text, "live") {
			haveLive = true
		}
	}
	if !haveNotice || !haveLive {
		t.Fatalf("want notice+flushed live entry; items=%+v", feedKinds(a.feed))
	}
}

// TestFeedHistoryIncompleteReplayNotice: a partial replay still seeds, with an
// incompleteness notice appended.
func TestFeedHistoryIncompleteReplayNotice(t *testing.T) {
	a := newFeedHistoryTestApp("s1")
	a.handleFeedHistory(feedHistoryMsg{
		id: "s1", gen: 1, complete: false,
		events: []session.Event{historyPrompt(1, "one")},
	})
	var haveNotice bool
	for _, it := range a.feed.items {
		if it.kind == feedNotice && strings.Contains(it.text, "incomplete") {
			haveNotice = true
		}
	}
	if len(a.feed.items) != 2 || !haveNotice {
		t.Fatalf("want seeded entry + incomplete notice; kinds=%v", feedKinds(a.feed))
	}
}

package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func jraw(s string) json.RawMessage { return json.RawMessage(s) }

// fakeEventCache is an in-memory dashboard.EventCache for the Workstream C tests:
// it returns preloaded events and records appends.
type fakeEventCache struct {
	loaded   []session.Event
	appended []session.Event
}

func (f *fakeEventCache) LoadEvents(id session.ID) ([]session.Event, error) { return f.loaded, nil }
func (f *fakeEventCache) AppendEvent(id session.ID, ev session.Event) error {
	f.appended = append(f.appended, ev)
	return nil
}

// C2: the stream.live boundary flips the transcript out of replay. While
// replaying, a replayed turn.started must NOT start the working spinner; once the
// boundary lands, a still-active turn resumes "working" (turnActive carried
// across). This is the fix for "replay feels like work" (#1).
func TestStreamLiveBoundaryGatesWorking(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.replaying = true

	m.handleEvent(session.Event{Seq: 1, Type: session.EventTurnStarted, Payload: jraw(`{"prompt":"hi"}`)})
	if !m.turnActive {
		t.Fatal("turn.started should set turnActive even during replay")
	}
	if cmd := m.maybeStartWorking(); cmd != nil {
		t.Fatal("the working loop must not start during replay")
	}

	// Boundary marker (no seq): flips replaying off without touching the cursor.
	m.handleEvent(session.Event{Type: session.EventStreamLive})
	if m.replaying {
		t.Fatal("stream.live must end replay")
	}
	if m.lastSeq != 1 {
		t.Fatalf("the marker must not advance lastSeq: got %d", m.lastSeq)
	}
	if cmd := m.maybeStartWorking(); cmd == nil {
		t.Fatal("a still-active turn must resume working after the boundary")
	}
}

// C2: a fresh session (attachSeq 0) never enters the loading state, and a cold
// attach with unseen history clears it at the watermark — even if the runner
// never sends the replay-complete marker (older runner / proxy strips comments).
func TestLoadingWatermark(t *testing.T) {
	fresh := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	fresh.width, fresh.height = 80, 200
	fresh.attachSeq = 0
	fresh.startEventStream()
	if fresh.replaying {
		t.Fatal("a fresh session (attachSeq 0) must not show loading transcript")
	}

	cold := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	cold.width, cold.height = 80, 200
	cold.attachSeq = 3
	cold.startEventStream()
	if !cold.replaying {
		t.Fatal("a cold attach with unseen history must show loading")
	}
	cold.handleEvent(session.Event{Seq: 1, Type: session.EventTurnStarted, Payload: jraw(`{"prompt":"q"}`)})
	if !cold.replaying {
		t.Fatal("still replaying before reaching the watermark")
	}
	cold.handleEvent(session.Event{Seq: 3, Type: session.EventTurnCompleted, Payload: jraw(`{}`)})
	if cold.replaying {
		t.Fatal("must clear loading at the watermark (lastSeq>=attachSeq) without a marker")
	}
}

// C2: the prompt-line indicator reads "loading transcript…" (not "working…")
// while replaying.
func TestLoadingStatusWhileReplaying(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.replaying = true
	m.replayedCount = 7
	out := stripANSI(m.loadingStatus())
	if !strings.Contains(out, "loading transcript") {
		t.Errorf("expected loading-transcript indicator, got %q", out)
	}
	if !strings.Contains(out, "7") {
		t.Errorf("expected the replayed count, got %q", out)
	}
}

// C1: a cold open rebuilds the transcript from the cache and advances lastSeq to
// the cached head so the stream resumes from there (after=lastSeq) instead of
// replaying from 0.
func TestColdAttachLoadsCache(t *testing.T) {
	cache := &fakeEventCache{loaded: []session.Event{
		{Seq: 1, Type: session.EventTurnStarted, Payload: jraw(`{"prompt":"q"}`)},
		{Seq: 2, Type: session.EventMessageCompleted, Payload: jraw(`{"role":"assistant","content":"cached-answer"}`)},
		{Seq: 3, Type: session.EventTurnCompleted, Payload: jraw(`{}`)},
	}}
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.cache = cache

	m.loadCachedTranscript()
	m.layout() // size the list so the body renders

	if m.lastSeq != 3 {
		t.Fatalf("lastSeq after cache load = %d, want 3 (resume from cached head)", m.lastSeq)
	}
	if !strings.Contains(stripANSI(m.body.Render()), "cached-answer") {
		t.Errorf("cache load did not rebuild the assistant block:\n%s", stripANSI(m.body.Render()))
	}

	// A warm/promoted model (already has blocks) must NOT re-load and duplicate.
	cache2 := &fakeEventCache{loaded: cache.loaded}
	m.cache = cache2
	before := m.lastSeq
	m.loadCachedTranscript() // guarded: len(blocks)>0 → no-op
	if m.lastSeq != before {
		t.Errorf("a populated transcript re-loaded the cache (lastSeq changed %d→%d)", before, m.lastSeq)
	}
}

// C1: streamed non-delta events are mirrored to the cache; high-volume deltas and
// the seq-less stream marker are not.
func TestStreamedEventsAreCached(t *testing.T) {
	cache := &fakeEventCache{}
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.cache = cache

	m.Update(tEventMsg(session.Event{Seq: 1, Type: session.EventMessageCompleted, Payload: jraw(`{"role":"assistant","content":"a"}`)}))
	m.Update(tEventMsg(session.Event{Seq: 2, Type: session.EventMessageDelta, Payload: jraw(`{"content":"x"}`)}))
	m.Update(tEventMsg(session.Event{Type: session.EventStreamLive}))

	if len(cache.appended) != 1 {
		t.Fatalf("cached %d events, want exactly 1 (completed only)", len(cache.appended))
	}
	if cache.appended[0].Type != session.EventMessageCompleted {
		t.Errorf("cached the wrong event: %s", cache.appended[0].Type)
	}
}

// C1 regression (review #2/#4): a session observed only in the background must
// still build the cache via ingest(), so a later cold attach rebuilds the full
// history with no hole. Before the fix, ingest advanced lastSeq without caching.
func TestWarmIngestPopulatesCacheNoHole(t *testing.T) {
	cache := &fakeEventCache{}
	warm := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	warm.width, warm.height = 80, 200
	warm.cache = cache
	warm.ingest(session.Event{Seq: 1, Type: session.EventTurnStarted, Payload: jraw(`{"prompt":"q"}`)})
	warm.ingest(session.Event{Seq: 2, Type: session.EventMessageCompleted, Payload: jraw(`{"role":"assistant","content":"bg-answer"}`)})
	warm.ingest(session.Event{Seq: 3, Type: session.EventTurnCompleted, Payload: jraw(`{}`)})

	if len(cache.appended) != 3 {
		t.Fatalf("warm ingest cached %d events, want 3 (no hole)", len(cache.appended))
	}

	// A later cold re-attach (fresh model, same cache) rebuilds that history.
	cold := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	cold.width, cold.height = 80, 200
	cold.cache = &fakeEventCache{loaded: cache.appended}
	cold.loadCachedTranscript()
	cold.layout()
	if cold.lastSeq != 3 {
		t.Fatalf("cold attach lastSeq=%d, want 3", cold.lastSeq)
	}
	if !strings.Contains(stripANSI(cold.body.Render()), "bg-answer") {
		t.Errorf("cold attach lost the background-observed turn:\n%s", stripANSI(cold.body.Render()))
	}
}

// C1: maybeCache is idempotent per seq — a duplicated event at the warm→foreground
// handoff is written once, so it can't double-render on the next cold replay.
func TestCacheDedupOnDuplicateSeq(t *testing.T) {
	cache := &fakeEventCache{}
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.cache = cache
	ev := session.Event{Seq: 5, Type: session.EventMessageCompleted, Payload: jraw(`{"role":"assistant","content":"x"}`)}
	m.maybeCache(ev)
	m.maybeCache(ev) // duplicate delivery (background buffered + foreground stream)
	if len(cache.appended) != 1 {
		t.Fatalf("duplicate seq cached %d times, want exactly 1", len(cache.appended))
	}
}

// cacheableEvent is the pure filter behind the above.
func TestCacheableEvent(t *testing.T) {
	cases := []struct {
		ev   session.Event
		want bool
	}{
		{session.Event{Seq: 1, Type: session.EventTurnStarted}, true},
		{session.Event{Seq: 2, Type: session.EventMessageCompleted}, true},
		{session.Event{Seq: 3, Type: session.EventToolCompleted}, true},
		{session.Event{Seq: 4, Type: session.EventMessageDelta}, false},
		{session.Event{Seq: 5, Type: session.EventReasoningDelta}, false},
		{session.Event{Seq: 6, Type: session.EventToolDelta}, false},
		{session.Event{Type: session.EventStreamLive}, false}, // seq 0 marker
	}
	for _, c := range cases {
		if got := cacheableEvent(c.ev); got != c.want {
			t.Errorf("cacheableEvent(%s seq=%d) = %v, want %v", c.ev.Type, c.ev.Seq, got, c.want)
		}
	}
}

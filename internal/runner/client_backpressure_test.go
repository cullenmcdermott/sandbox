package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestEventsSlowConsumerDoesNotForceReconnect is the regression for §1d: a slow
// (stalled) consumer must NOT be able to manufacture a forced disconnect+replay.
//
// Before the fix, the scanner sent decoded events straight to the 64-buffered
// `events` channel; a consumer that stopped draining filled that buffer, the
// scanner blocked mid-send and stopped calling Scan(), and the read watchdog —
// which is keyed on scanner reads — saw no reads for sseReadTimeout and
// force-closed a perfectly live stream. The consumer would then observe the
// channel close after ~65 events (the buffer plus the one in-flight send),
// never reaching the events the server kept sending.
//
// With socket reading decoupled from consumer draining, the scanner keeps
// reading (feeding an internal queue) while the consumer is stalled, so the
// watchdog stays fed by server activity and the stream stays open. We stall the
// consumer for several watchdog windows while the server streams a contiguous
// sequence, then drain and assert we can read well past the old buffer ceiling
// with no gap and no premature close.
func TestEventsSlowConsumerDoesNotForceReconnect(t *testing.T) {
	const window = 100 * time.Millisecond
	// The consumer withholds reads for this long — many watchdog windows and far
	// more than the 64-event channel buffer can absorb, so the pre-fix scanner
	// would have blocked mid-send and let the watchdog fire.
	const stall = 5 * window
	// How far into the (contiguous) sequence we insist on reading. Comfortably
	// above the 64-buffer ceiling, so a force-close during the stall (which caps
	// the reachable seq at ~65 in the old code) shows up as a premature close.
	const threshold = 300

	old := sseReadTimeout
	sseReadTimeout = window
	defer func() { sseReadTimeout = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/events") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// Stream a contiguous sequence until the client disconnects (Write starts
		// erroring) or we hit a generous cap.
		for seq := uint64(1); seq <= 5000; seq++ {
			ev := session.Event{Seq: seq, Type: session.EventTurnStarted, SessionID: "test"}
			data, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := c.Events(ctx, session.Ref{ID: "test"}, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	// Simulate a stalled TUI: hold the events channel undrained across several
	// watchdog windows while the server keeps sending.
	time.Sleep(stall)

	// Now drain. Every event must be contiguous (seq == want) and the channel
	// must stay open until we reach the threshold — a force-close manufactured by
	// our stall would instead close the channel early (well before `threshold`).
	want := uint64(1)
	reached := false
	for !reached {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("stream force-closed after a slow consumer: reached seq %d, wanted at least %d (§1d regression: a stalled consumer manufactured a reconnect)", want-1, threshold)
			}
			if ev.Seq != want {
				t.Fatalf("non-contiguous stream: got seq %d, want %d (a gap implies a dropped/forced reconnect)", ev.Seq, want)
			}
			if ev.Seq >= threshold {
				cancel() // proved the point; stop the server and unwind.
				reached = true
				break
			}
			want++
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out draining the stream at seq %d (wanted %d)", want, threshold)
		}
	}

	// Let the client goroutines observe the cancel and close `events`, so no
	// lingering goroutine races the sseReadTimeout restore under -race.
	for range events {
	}
}

// TestEventsSilentStreamStillForceCloses is the companion to the §1d fix: a
// genuinely dead upstream (server sends nothing and never closes — a half-open
// connection) must STILL be force-closed by the watchdog so the channel closes
// and the caller can reconnect. The §1d change must not weaken this: it only
// stops CONSUMER backpressure from tripping the watchdog, not real SERVER
// silence.
func TestEventsSilentStreamStillForceCloses(t *testing.T) {
	old := sseReadTimeout
	sseReadTimeout = 120 * time.Millisecond
	defer func() { sseReadTimeout = old }()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/events") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		ev := session.Event{Seq: 1, Type: session.EventSessionStarted, SessionID: "test"}
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		// Then go silent (no data, no close) — a half-open connection.
		<-release
	}))
	defer srv.Close()
	defer close(release)

	c := New(srv.URL, "token")
	events, err := c.Events(context.Background(), session.Ref{ID: "test"}, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	// The one event the server did send arrives.
	select {
	case ev, ok := <-events:
		if !ok || ev.Seq != 1 {
			t.Fatalf("expected first event seq=1, got %+v ok=%v", ev, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive the first event")
	}

	// The stream then goes silent; the watchdog must force-close → channel closes,
	// even though the consumer is draining promptly.
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected the channel to close after the watchdog fired on a silent stream, got another event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not force-close a genuinely silent stream (§1d must not weaken real-death detection)")
	}
}

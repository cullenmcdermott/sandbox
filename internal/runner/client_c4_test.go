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

// Regression for C4: a single SSE event larger than sseMaxLineBytes makes
// bufio.Scanner stop with ErrTooLong. Because the runner re-sends the same
// event on reconnect (after=lastSeq), this is both silent AND self-perpetuating
// — the old code just closed the channel, so the TUI looped reconnecting on the
// same oversized event with no explanation. The client must now surface a
// visible EventError before closing so the user learns the transcript is
// incomplete. We shrink sseMaxLineBytes so the test stays small.
func TestEventsSurfacesOversizedEvent(t *testing.T) {
	oldMax := sseMaxLineBytes
	sseMaxLineBytes = 128 * 1024
	defer func() { sseMaxLineBytes = oldMax }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/events") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// A normal, small event first.
		ev := session.Event{Seq: 1, Type: session.EventSessionStarted, SessionID: "test"}
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		// Then a single line that exceeds sseMaxLineBytes before any newline,
		// forcing bufio.ErrTooLong on the client.
		fmt.Fprintf(w, "data: %s\n\n", strings.Repeat("x", 200*1024))
		flusher.Flush()
	}))
	defer srv.Close()

	c := New(srv.URL, "token")
	events, err := c.Events(context.Background(), session.Ref{ID: "test"}, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	// First, the normal event.
	select {
	case ev, ok := <-events:
		if !ok || ev.Seq != 1 {
			t.Fatalf("expected first event seq=1, got %+v ok=%v", ev, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive the first event")
	}

	// Then a surfaced error event describing the oversized drop (NOT a silent close).
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("channel closed silently on oversized event (C4 regression): expected a surfaced error event first")
		}
		if ev.Type != session.EventError {
			t.Fatalf("expected an EventError for the oversized event, got type %q", ev.Type)
		}
		var p session.ErrorPayload
		if uerr := json.Unmarshal(ev.Payload, &p); uerr != nil {
			t.Fatalf("error payload did not decode: %v", uerr)
		}
		if !strings.Contains(p.Message, "exceeded") {
			t.Fatalf("error message should explain the oversize, got %q", p.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client never surfaced the oversized-event error (C4 regression)")
	}

	// Drain to close so the stream goroutine (and its watchdog) fully exit
	// before the test returns. Otherwise a lingering goroutine's read of a
	// package-level test var (sseReadTimeout/sseMaxLineBytes) races with the
	// next test's write of it under -race.
	for range events {
	}
}

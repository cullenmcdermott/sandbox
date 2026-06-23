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

// Regression for R6: on a half-open stream (server sends nothing and never
// closes), the read watchdog must force-close the body so the events channel
// closes — otherwise the reader blocks forever and reconnect never fires. We
// shorten sseReadTimeout so the test runs fast.
func TestEventsWatchdogClosesOnStall(t *testing.T) {
	old := sseReadTimeout
	sseReadTimeout = 150 * time.Millisecond
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
		// Then stall — simulate a half-open connection (no more data, no close)
		// until the test tears down.
		<-release
	}))
	defer srv.Close()
	defer close(release)

	c := New(srv.URL, "token")
	events, err := c.Events(context.Background(), session.Ref{ID: "test"}, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	// First event arrives.
	select {
	case ev, ok := <-events:
		if !ok || ev.Seq != 1 {
			t.Fatalf("expected first event seq=1, got %+v ok=%v", ev, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive the first event")
	}

	// The stream then stalls; the watchdog (150ms) must force-close → channel closes.
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected the channel to close after the watchdog fired, got another event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not force-close the stalled stream (R6 regression)")
	}
}

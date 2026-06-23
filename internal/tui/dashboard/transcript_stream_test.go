package dashboard

import (
	"context"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// ctxCapturingClient records the context passed to Events so a test can observe
// whether the live SSE stream was torn down (NEW-5).
type ctxCapturingClient struct {
	fakeRunnerClient
	ctx context.Context
}

func (c *ctxCapturingClient) Events(ctx context.Context, _ session.Ref, _ uint64) (<-chan session.Event, error) {
	c.ctx = ctx
	return make(chan session.Event), nil
}

// TestCancelStreamCancelsEventsContext: cancelStream must cancel the context
// handed to client.Events, so the runner-side SSE connection is torn down rather
// than left open until GC (NEW-5).
func TestCancelStreamCancelsEventsContext(t *testing.T) {
	c := &ctxCapturingClient{}
	m := NewTranscript(c, Session{State: session.State{ID: "s1"}}, nil)

	m.startEventStream()
	if c.ctx == nil {
		t.Fatal("startEventStream did not call client.Events")
	}
	if c.ctx.Err() != nil {
		t.Fatalf("stream context cancelled before detach: %v", c.ctx.Err())
	}

	m.cancelStream()
	if c.ctx.Err() == nil {
		t.Fatal("cancelStream did not cancel the Events context (NEW-5: SSE would leak on detach)")
	}
}

// TestParkTranscriptCancelsStream: the App's detach hook (parkTranscript, called
// at every detach path immediately before releasing the transcript) must cancel
// the transcript's live stream so at most one SSE client exists after detach.
func TestParkTranscriptCancelsStream(t *testing.T) {
	c := &ctxCapturingClient{}
	app := NewApp(nil, nil, nil)
	m := NewTranscript(c, Session{State: session.State{ID: "s1"}}, nil)
	m.startEventStream()
	if c.ctx == nil || c.ctx.Err() != nil {
		t.Fatal("precondition: stream should be live before detach")
	}

	app.parkTranscript(m)

	if c.ctx.Err() == nil {
		t.Fatal("parkTranscript did not cancel the transcript stream (NEW-5)")
	}
}

// TestStartEventStreamCancelsPriorStream: opening a new stream (e.g. on
// reconnect) must cancel the previous one so contexts/connections don't leak.
func TestStartEventStreamCancelsPriorStream(t *testing.T) {
	c := &ctxCapturingClient{}
	m := NewTranscript(c, Session{State: session.State{ID: "s1"}}, nil)

	m.startEventStream()
	first := c.ctx
	if first == nil {
		t.Fatal("first startEventStream did not open a stream")
	}

	m.startEventStream() // reconnect
	if first.Err() == nil {
		t.Fatal("startEventStream did not cancel the prior stream's context")
	}
}

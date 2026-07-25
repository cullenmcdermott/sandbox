package client

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/runner"
)

// A claude-pane session spawns its interactive child LAZILY on the first
// attach, so AttachPane — not StartTurn — is what starts an agent for this
// backend. The turn gate therefore does not cover it: a pane session whose
// first-ever sync never staged must be refused HERE, or claude boots into an
// empty directory (the failure that let a session come up blank).
func TestAttachPaneRefusedAfterFailedInitialSync(t *testing.T) {
	s := &Session{runner: runner.New("http://127.0.0.1:1", "tok")}
	task := &syncTask{done: make(chan struct{})}
	s.syncTask = task
	task.finish("", fmt.Errorf("%w: flush failed", ErrInitialSyncFailed))

	_, err := s.AttachPane(context.Background(), 80, 24)
	if !errors.Is(err, ErrInitialSyncFailed) {
		t.Fatalf("AttachPane err = %v, want ErrInitialSyncFailed", err)
	}
}

// The behavioral counter to the test above: the gate must refuse only on a
// KNOWN staging failure. A slow-but-healthy first upload is advisory, so the
// attach proceeds to the dial — which fails here only because the address is
// unroutable, NOT because the gate blocked it.
func TestAttachPaneProceedsAfterSyncWarning(t *testing.T) {
	s := &Session{runner: runner.New("http://127.0.0.1:1", "tok")}
	task := &syncTask{done: make(chan struct{})}
	s.syncTask = task
	task.finish("initial file sync still in progress (continuing in the background)", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.AttachPane(ctx, 80, 24)
	if errors.Is(err, ErrInitialSyncFailed) {
		t.Fatal("a sync WARNING must not refuse a pane attach")
	}
	if err == nil {
		t.Fatal("expected the dial to an unroutable address to fail")
	}
}

// The gate blocks while staging is still in flight: attaching mid-flush would
// spawn claude before the answer is known.
func TestAttachPaneWaitsForStagingToSettle(t *testing.T) {
	s := &Session{runner: runner.New("http://127.0.0.1:1", "tok")}
	task := &syncTask{done: make(chan struct{})}
	s.syncTask = task

	res := make(chan error, 1)
	go func() {
		_, err := s.AttachPane(context.Background(), 80, 24)
		res <- err
	}()

	select {
	case <-res:
		t.Fatal("AttachPane returned before staging settled")
	case <-time.After(50 * time.Millisecond):
	}

	task.finish("", fmt.Errorf("%w: flush failed", ErrInitialSyncFailed))
	select {
	case err := <-res:
		if !errors.Is(err, ErrInitialSyncFailed) {
			t.Fatalf("AttachPane err = %v, want ErrInitialSyncFailed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AttachPane did not release after staging settled")
	}
}

// A session that never connected fails the connection check BEFORE the gate —
// the nil-runner error must not be masked by a sync verdict.
func TestAttachPaneNotConnectedBeatsGate(t *testing.T) {
	s := &Session{}
	task := &syncTask{done: make(chan struct{})}
	s.syncTask = task
	task.finish("", fmt.Errorf("%w: flush failed", ErrInitialSyncFailed))

	if _, err := s.AttachPane(context.Background(), 80, 24); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("AttachPane err = %v, want ErrNotConnected", err)
	}
}

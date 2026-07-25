package client

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTurnRunner implements only StartTurn; the nil embedded interface makes
// any other method call panic, proving the gate never touches them.
type fakeTurnRunner struct {
	RunnerClient
	started atomic.Int32
}

func (f *fakeTurnRunner) StartTurn(ctx context.Context, ref Ref, in TurnInput) (TurnRef, error) {
	f.started.Add(1)
	return TurnRef{Session: ref.ID, Turn: "t1"}, nil
}

// Connect no longer blocks on the initial project flush (§5), so the runner
// handed to callers must gate StartTurn on the background staging work: a turn
// must not reach the agent before the workspace is staged.
func TestStartTurnGatedOnBackgroundSync(t *testing.T) {
	s := &Session{}
	task := &syncTask{done: make(chan struct{})}
	s.syncTask = task
	fr := &fakeTurnRunner{}
	g := &stagedRunner{RunnerClient: fr, s: s}

	res := make(chan error, 1)
	go func() {
		_, err := g.StartTurn(context.Background(), Ref{ID: "x"}, TurnInput{Prompt: "hi"})
		res <- err
	}()

	select {
	case <-res:
		t.Fatal("StartTurn completed before the background sync settled")
	case <-time.After(50 * time.Millisecond):
	}
	if n := fr.started.Load(); n != 0 {
		t.Fatalf("turn reached the runner before staging settled (started=%d)", n)
	}

	task.finish("", nil)
	select {
	case err := <-res:
		if err != nil {
			t.Fatalf("StartTurn after staging: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StartTurn did not release after the background task finished")
	}
	if n := fr.started.Load(); n != 1 {
		t.Fatalf("expected exactly one submitted turn, got %d", n)
	}
}

// A first-ever sync whose transport broke leaves the workspace EMPTY, so the
// turn gate must refuse rather than hand an agent an empty directory — the
// failure mode that let a session come up blank and burn tokens on nothing.
func TestStagedRunnerRefusesTurnAfterFailedInitialSync(t *testing.T) {
	s := &Session{}
	task := &syncTask{done: make(chan struct{})}
	s.syncTask = task
	fr := &fakeTurnRunner{}
	g := &stagedRunner{RunnerClient: fr, s: s}

	task.finish("", fmt.Errorf("%w: flush failed", ErrInitialSyncFailed))

	_, err := g.StartTurn(context.Background(), Ref{ID: "x"}, TurnInput{Prompt: "hi"})
	if !errors.Is(err, ErrInitialSyncFailed) {
		t.Fatalf("StartTurn err = %v, want ErrInitialSyncFailed", err)
	}
	if n := fr.started.Load(); n != 0 {
		t.Fatalf("turn reached the runner despite an unstaged workspace (started=%d)", n)
	}
}

// A slow-but-healthy first upload is an advisory, NOT a refusal: the warning
// surfaces and the turn still goes through.
func TestStagedRunnerAllowsTurnAfterSyncWarning(t *testing.T) {
	s := &Session{}
	task := &syncTask{done: make(chan struct{})}
	s.syncTask = task
	fr := &fakeTurnRunner{}
	g := &stagedRunner{RunnerClient: fr, s: s}

	task.finish("initial file sync still in progress (continuing in the background)", nil)

	if _, err := g.StartTurn(context.Background(), Ref{ID: "x"}, TurnInput{Prompt: "hi"}); err != nil {
		t.Fatalf("a sync WARNING must not block a turn: %v", err)
	}
	warn, err := s.AwaitSync(context.Background())
	if err != nil {
		t.Fatalf("AwaitSync err = %v, want nil", err)
	}
	if warn == "" {
		t.Fatal("AwaitSync dropped the advisory")
	}
}

// A caller cancelling its context while gated gets the context error and the
// turn is never submitted.
func TestStartTurnGateRespectsContextCancel(t *testing.T) {
	s := &Session{}
	s.syncTask = &syncTask{done: make(chan struct{})} // never finishes
	fr := &fakeTurnRunner{}
	g := &stagedRunner{RunnerClient: fr, s: s}

	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := g.StartTurn(ctx, Ref{ID: "x"}, TurnInput{Prompt: "hi"})
		res <- err
	}()
	cancel()

	select {
	case err := <-res:
		if err == nil {
			t.Fatal("expected a context error from a cancelled gated StartTurn")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gated StartTurn did not observe cancellation")
	}
	if n := fr.started.Load(); n != 0 {
		t.Fatalf("cancelled StartTurn must not submit a turn (started=%d)", n)
	}
}

// With no background work in flight (observer connect, or after it settled and
// was cleared) the gate is a no-op passthrough.
func TestStartTurnUngatedWithoutTask(t *testing.T) {
	s := &Session{}
	fr := &fakeTurnRunner{}
	g := &stagedRunner{RunnerClient: fr, s: s}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := g.StartTurn(ctx, Ref{ID: "x"}, TurnInput{Prompt: "hi"}); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if n := fr.started.Load(); n != 1 {
		t.Fatalf("expected passthrough submit, got %d", n)
	}
}

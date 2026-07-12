package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// fakePromptRunner implements only the RunnerClient methods startPromptTurn
// touches; the nil embedded interface makes any other call panic loudly (same
// pattern as client/staged_runner_test.go's fakeTurnRunner).
type fakePromptRunner struct {
	client.RunnerClient
	healthErr error
	startErr  error
	started   []client.TurnInput
	startedAt []session.Ref
}

func (f *fakePromptRunner) Health(context.Context) error { return f.healthErr }

func (f *fakePromptRunner) StartTurn(_ context.Context, ref client.Ref, in client.TurnInput) (client.TurnRef, error) {
	f.started = append(f.started, in)
	f.startedAt = append(f.startedAt, ref)
	if f.startErr != nil {
		return client.TurnRef{}, f.startErr
	}
	return client.TurnRef{Session: ref.ID, Turn: "turn-1"}, nil
}

// startPromptTurn is the headless first-turn seam behind
// `sandbox opencode "prompt"`: a healthy runner gets exactly one StartTurn
// carrying the prompt (no mode/model overrides — the session default model was
// provisioned at Create) and the TurnRef is returned to the caller.
func TestStartPromptTurnPostsThePrompt(t *testing.T) {
	f := &fakePromptRunner{}
	ref := session.Ref{ID: "opencode-abc123-deadbeef"}

	turn, err := startPromptTurn(context.Background(), f, ref, "fix the build")
	if err != nil {
		t.Fatalf("startPromptTurn: %v", err)
	}
	if turn.Turn != "turn-1" {
		t.Fatalf("turn ref = %+v, want Turn turn-1", turn)
	}
	if len(f.started) != 1 {
		t.Fatalf("StartTurn called %d times, want 1", len(f.started))
	}
	if f.started[0].Prompt != "fix the build" {
		t.Fatalf("prompt = %q, want the positional arg", f.started[0].Prompt)
	}
	if f.started[0].ApprovalPolicy != "" || f.started[0].Model != "" {
		t.Fatalf("unexpected mode/model overrides: %+v", f.started[0])
	}
	if f.startedAt[0] != ref {
		t.Fatalf("turn posted against %+v, want %+v", f.startedAt[0], ref)
	}
}

// A StartTurn failure must surface as a hard error (the whole point of the
// headless delivery is that the prompt is never silently dropped).
func TestStartPromptTurnPropagatesStartError(t *testing.T) {
	f := &fakePromptRunner{startErr: errors.New("boom")}

	_, err := startPromptTurn(context.Background(), f, session.Ref{ID: "x"}, "hi")
	if err == nil {
		t.Fatal("want error from failed StartTurn, got nil")
	}
	if !strings.Contains(err.Error(), "start turn") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %q should wrap the StartTurn failure", err)
	}
}

// A runner that never becomes healthy must fail the health gate before any turn
// is posted. A cancelled context short-circuits waitHealthy's retry loop so the
// test doesn't ride out the full 30s budget.
func TestStartPromptTurnFailsClosedOnUnhealthyRunner(t *testing.T) {
	f := &fakePromptRunner{healthErr: errors.New("connection refused")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := startPromptTurn(ctx, f, session.Ref{ID: "x"}, "hi")
	if err == nil {
		t.Fatal("want error from unhealthy runner, got nil")
	}
	if !strings.Contains(err.Error(), "runner health") {
		t.Fatalf("error %q should come from the health gate", err)
	}
	if len(f.started) != 0 {
		t.Fatalf("StartTurn must not fire on an unhealthy runner (called %d times)", len(f.started))
	}
}

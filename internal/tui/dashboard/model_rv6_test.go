package dashboard

import (
	"context"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// RV6: the dashboard opens a background SSE stream for every running session to
// drive list status. Those streams must attach as PASSIVE observers
// (EventsPassive), so the runner does not count them as attached clients — if it
// did, sseClientCount() would never reach 0 and the idle reaper could never
// suspend a session while the command center is open, nullifying auto-suspend.
func TestBackgroundStreamUsesPassiveAttach(t *testing.T) {
	fake := &fakeRunnerClient{}
	m := New(nil)
	m.connector = func(_ context.Context, _ session.Ref, _ string, _ func(ConnectStage, string)) (ConnectResult, error) {
		return ConnectResult{Client: fake}, nil
	}
	sess := Session{State: session.State{ID: "s1", Status: session.StatusRunning}}

	cmd := m.startLiveSSECmd(sess)
	if cmd == nil {
		t.Fatal("startLiveSSECmd returned nil")
	}
	msg := cmd()
	ready, ok := msg.(liveSSEReadyMsg)
	if !ok {
		t.Fatalf("expected liveSSEReadyMsg, got %T", msg)
	}
	ready.cancel() // don't leak the stream context

	if fake.passiveStreams != 1 {
		t.Errorf("background list stream used the active Events path (passiveStreams=%d, want 1) — would pin every session against the idle reaper (RV6)", fake.passiveStreams)
	}
}

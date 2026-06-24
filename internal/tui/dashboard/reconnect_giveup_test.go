package dashboard

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func noopReconnect(context.Context, func(ConnectStage, string)) (RunnerClient, error) {
	return &fakeRunnerClient{}, nil
}

// Fix D: a permanent "session gone" reconnect failure must stop the retry loop
// and show a terminal state, instead of "reconnecting…" forever.
func TestReconnectGivesUpOnSessionGone(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), noopReconnect)
	m.width, m.height = 80, 24
	m.reconnecting = true
	m.reconnectStartedAt = nowFunc()

	_, cmd := m.Update(tReconnectFailedMsg{err: fmt.Errorf("connect s1: %w", session.ErrSessionGone)})
	if cmd != nil {
		t.Error("must stop retrying when the session is gone (no retry tick)")
	}
	if !m.reconnectGaveUp || m.reconnecting {
		t.Fatalf("expected terminal give-up, got gaveUp=%v reconnecting=%v", m.reconnectGaveUp, m.reconnecting)
	}
	if h := stripANSI(m.renderHeader()); !strings.Contains(h, "session gone") {
		t.Errorf("header should show 'session gone', got %q", h)
	}
}

// COUNTER: a transient error keeps retrying and stays in the reconnecting state.
func TestReconnectKeepsRetryingOnTransientError(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), noopReconnect)
	m.width, m.height = 80, 24
	m.reconnecting = true
	m.reconnectStartedAt = nowFunc().Add(-90 * time.Second)

	_, cmd := m.Update(tReconnectFailedMsg{err: fmt.Errorf("port-forward: connection refused")})
	if cmd == nil {
		t.Error("a transient error must schedule a retry")
	}
	if m.reconnectGaveUp {
		t.Error("must not give up on a transient error")
	}
	// The header reads as live progress (elapsed shown), not a frozen label.
	h := stripANSI(m.renderHeader())
	if !strings.Contains(h, "reconnecting") || !strings.Contains(h, "1m") {
		t.Errorf("header should show reconnecting with elapsed time, got %q", h)
	}
}

// A successful reconnect clears the give-up/elapsed state.
func TestReconnectSuccessClearsGiveUp(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), noopReconnect)
	m.width, m.height = 80, 24
	m.reconnecting = true
	m.reconnectGaveUp = true
	m.reconnectStartedAt = nowFunc()

	m.Update(tReconnectedMsg{client: &fakeRunnerClient{}})
	if m.reconnectGaveUp || m.reconnecting || !m.reconnectStartedAt.IsZero() {
		t.Fatalf("reconnect success must clear all reconnect state: gaveUp=%v reconnecting=%v startedZero=%v",
			m.reconnectGaveUp, m.reconnecting, m.reconnectStartedAt.IsZero())
	}
}

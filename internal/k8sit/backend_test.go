//go:build integration

package k8sit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestBackendTurn drives a REAL one-shot turn through each RUNNER-TURN backend end
// to end (CreateSession → Start → PortForward → runner.StartTurn → SSE Events),
// table-driven over backendCases — the backend half of the parity matrix
// (docs/archive/testing-parity-plan.md). opencode goes through its turn adapter
// (runner/src/opencode-turn.ts) on the free big-pickle model ($0, no key). The
// supervise-only backends (claude-pane, codex) are SKIPPED: they don't drive turns
// through the runner (POST /turns 409s) — the CLI smoke covers their seam instead.
// A keyed runner-turn backend would assert a real reply only when its Secret is
// present, else degrade to plumbing-only (assertTurnStarts).
func TestBackendTurn(t *testing.T) {
	rc := localRestConfig(t) // context-isolation guard before any cluster mutation
	for _, bc := range backendCases {
		t.Run(bc.name, func(t *testing.T) {
			if !bc.drivesRunnerTurns {
				t.Skip("supervise-only backend: turns run in the pane/app-server, not through the runner (POST /turns 409s)") // gate-ok: no runner turn path for this backend
			}
			expectReply := bc.expectRealReply(t, rc)
			be, ref := createReadySession(t, bc.backend, bc.idTag)

			turnTimeout := envDuration("K8SIT_TURN_TIMEOUT", 180*time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), turnTimeout)
			defer cancel()

			events, tref := startTurnStream(t, ctx, be, ref,
				session.TurnInput{Prompt: turnPrompt, Model: bc.turnModel})
			if expectReply {
				assertTurnCompletes(t, ctx, events, tref.Turn)
				return
			}
			t.Logf("%s: no provider key -> plumbing-only", bc.name)
			assertTurnStarts(t, events, tref.Turn)
		})
	}
}

// startTurnStream waits for the runner to be healthy, starts a turn, and opens
// the event stream (after=0 replays from start so the turn's events are never
// missed). Shared by the per-backend turn tests.
func startTurnStream(t *testing.T, ctx context.Context, be *k8s.Backend, ref session.Ref, input session.TurnInput) (<-chan session.Event, session.TurnRef) {
	t.Helper()
	client, cleanup := runnerClientForRef(t, be, ref)
	t.Cleanup(cleanup)

	healthCtx, healthCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer healthCancel()
	if err := pollHealthy(healthCtx, client); err != nil {
		t.Fatalf("runner never became healthy: %v", err)
	}

	tref, err := client.StartTurn(ctx, ref, input)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	t.Logf("turn started: %s", tref.Turn)

	events, err := client.Events(ctx, ref, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	return events, tref
}

// assertTurnCompletes consumes the SSE stream until it sees both an assistant
// message.completed with non-empty content and turn.completed for ourTurn, or
// fails on turn.failed / ctx timeout / stream close.
func assertTurnCompletes(t *testing.T, ctx context.Context, events <-chan session.Event, ourTurn session.TurnID) {
	t.Helper()
	var sawAssistant, sawCompleted bool
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for turn completion (assistant=%v completed=%v): %v", sawAssistant, sawCompleted, ctx.Err())
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before turn.completed (assistant=%v completed=%v)", sawAssistant, sawCompleted)
			}
			if ev.TurnID != ourTurn {
				continue
			}
			switch ev.Type {
			case session.EventMessageCompleted:
				var m session.MessagePayload
				if err := json.Unmarshal(ev.Payload, &m); err != nil {
					t.Fatalf("decode message.completed payload: %v", err)
				}
				if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
					sawAssistant = true
				}
			case session.EventTurnCompleted:
				sawCompleted = true
			case session.EventTurnFailed:
				t.Fatalf("turn failed: %s", string(ev.Payload))
			}
			if sawAssistant && sawCompleted {
				return
			}
		}
	}
}

// assertTurnStarts is the plumbing-only assertion: prove the runner accepted
// and began our turn (turn.started) within a short window, then pass.
func assertTurnStarts(t *testing.T, events <-chan session.Event, ourTurn session.TurnID) {
	t.Helper()
	deadline := time.After(45 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("did not observe turn.started for %s within 45s", ourTurn)
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before turn.started for %s", ourTurn)
			}
			if ev.TurnID != ourTurn {
				continue
			}
			if ev.Type == session.EventTurnStarted {
				return
			}
		}
	}
}

package dashboard

import (
	"testing"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// golden_adverse_test.go — Phase E adverse-state frames for the shared transcript
// renderer (docs/archive/testing-parity-plan.md). The happy path is golden'd per-backend in
// golden_multiturn_test.go; this extends the golden set to the adverse states a turn
// can enter: a permission box, an ExitPlanMode plan card, an error block, and a
// mid-stream (un-completed) assistant message.
//
// These frames are backend-agnostic — they render from normalized events BEFORE any
// backend-labeled turn footer, so cross-backend parity is already proven by
// golden_multiturn_test.go + TestUXParityStatusLineMetrics. We snapshot each frame
// ONCE (no per-backend duplication) purely to lock the rendering. Regenerate with
// `-update`.

// adversePermissionBox renders the permission box for a Bash approval.
func TestGoldenAdversePermissionBox(t *testing.T) {
	withDeterministicRender(t, func() {
		m := newBackendTranscript(t, "claude-sdk", &fakeRunnerClient{})
		m.handleEvent(session.Event{Type: session.EventTurnStarted, TurnID: "t1"})
		m.handleEvent(mkEvent(session.EventPermissionRequested, session.PermissionPayload{
			PermissionID: "perm-1", Tool: "Bash", Input: jraw(`{"command":"rm -rf build/"}`),
		}))
		m.layout()
		golden.RequireEqual(t, []byte(m.permBox))
	})
}

// adversePlanCard renders the gold ExitPlanMode plan card.
func TestGoldenAdversePlanCard(t *testing.T) {
	withDeterministicRender(t, func() {
		m := newBackendTranscript(t, "claude-sdk", &fakeRunnerClient{})
		m.handleEvent(session.Event{Type: session.EventTurnStarted, TurnID: "t1"})
		m.handleEvent(mkEvent(session.EventPermissionRequested, session.PermissionPayload{
			PermissionID: "perm-2", Tool: "ExitPlanMode",
			Input: jraw(`{"plan":"1. Add the parser\n2. Wire the CLI flag\n3. Cover with tests"}`),
		}))
		m.layout()
		golden.RequireEqual(t, []byte(m.permBox))
	})
}

// adverseErrorBlock renders the body after a turn fails.
func TestGoldenAdverseErrorBlock(t *testing.T) {
	withDeterministicRender(t, func() {
		m := newBackendTranscript(t, "claude-sdk", &fakeRunnerClient{})
		m.handleEvent(session.Event{Type: session.EventTurnStarted, TurnID: "t1"})
		m.handleEvent(mkEvent(session.EventTurnFailed, map[string]string{"message": "provider rate limit exceeded"}))
		m.layout()
		golden.RequireEqual(t, []byte(m.bodyView()))
	})
}

// adverseStreaming renders the body mid-stream — message.started + deltas, no
// message.completed yet (the streaming-assistant frame).
func TestGoldenAdverseStreaming(t *testing.T) {
	withDeterministicRender(t, func() {
		m := newBackendTranscript(t, "claude-sdk", &fakeRunnerClient{})
		m.handleEvent(session.Event{Type: session.EventTurnStarted, TurnID: "t1"})
		m.handleEvent(mkEvent(session.EventMessageStarted, session.MessagePayload{Role: "assistant", Content: ""}))
		m.handleEvent(mkEvent(session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "Counting the Go ", Delta: true}))
		m.handleEvent(mkEvent(session.EventMessageDelta, session.MessagePayload{Role: "assistant", Content: "files now…", Delta: true}))
		m.layout()
		golden.RequireEqual(t, []byte(m.bodyView()))
	})
}

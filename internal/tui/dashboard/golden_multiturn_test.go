package dashboard

import (
	"testing"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// golden_multiturn_test.go — the FRONTEND half of the backend-parity matrix
// (docs/testing-parity-plan.md, Phases A/E). Because every backend now emits the
// SAME normalized events through the runner, the dashboard renders them through
// ONE transcript renderer — so frontend parity is a shared, parameterized golden:
// replay one scripted multi-turn conversation as EACH backend's transcript and
// snapshot it. A new backend (Codex) is onboarded by appending a row. Cost-free,
// deterministic, no cluster. Regenerate with `-update`.

// backendTranscriptCases is the frontend mirror of internal/k8sit's backendCases.
var backendTranscriptCases = []struct {
	name    string
	backend string
}{
	{name: "claude", backend: "claude-sdk"},
	{name: "opencode", backend: "opencode-server"},
}

// renderBackendTranscript builds a transcript labeled for `backend`, replays the
// fixture event stream through the real handleEvent → render path, and returns the
// rendered body. Shared frontend-conformance helper.
func renderBackendTranscript(t *testing.T, backend, fixture string) string {
	t.Helper()
	tm := NewTranscript(&fakeRunnerClient{}, Session{
		State: session.State{ID: "alpha", ProjectPath: "/work/alpha", Backend: backend},
		Title: "alpha",
	}, nil)
	tm.width, tm.height = 100, 30
	tm.layout()
	for _, ev := range loadEventStream(t, fixture) {
		tm.handleEvent(ev)
	}
	tm.layout()
	return tm.bodyView()
}

func TestGoldenTranscriptByBackend(t *testing.T) {
	for _, bc := range backendTranscriptCases {
		t.Run(bc.name, func(t *testing.T) {
			withDeterministicRender(t, func() {
				golden.RequireEqual(t, []byte(renderBackendTranscript(t, bc.backend, "transcript-multiturn.jsonl")))
			})
		})
	}
}

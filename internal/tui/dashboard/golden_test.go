package dashboard

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// golden_test.go — the D1 golden harness. It snapshots the key rendered surfaces
// at fixed sizes under reduce-motion with an injected clock, so the output is
// byte-stable and committed as testdata/*.golden. These baselines capture the
// CURRENT appearance of the card-shaped surfaces (permission box, plan card,
// confirm dialog) so the D2 kit migration can be proven to preserve them — any
// unintended visual change fails the snapshot. Regenerate with `-update`.
//
// Determinism inputs (the three sources of flakiness, all pinned):
//   - SANDBOX_REDUCE_MOTION=1  → transitions collapse to their end-state.
//   - nowFunc fixed            → relative/elapsed times don't move.
//   - theme.GradientCapable forced   → gradient-vs-solid fallback is not env-detected.

// goldenFixedNow is the injected wall clock for golden determinism.
var goldenFixedNow = time.Date(2030, 6, 21, 12, 0, 0, 0, time.UTC)

// withDeterministicRender pins the three flakiness sources for the duration of fn
// and restores them after.
func withDeterministicRender(t *testing.T, fn func()) {
	t.Helper()
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")

	oldNow := nowFunc
	oldGrad := theme.GradientCapable
	nowFunc = func() time.Time { return goldenFixedNow }
	theme.GradientCapable = true
	defer func() {
		nowFunc = oldNow
		theme.GradientCapable = oldGrad
	}()

	fn()
}

// goldenDashboard fixture: a seeded two-session dashboard at a fixed size.
func goldenDashboardModel() *Model {
	m := New(nil)
	m, _ = m.applySeed([]session.State{
		{ID: "alpha", ProjectPath: "/work/alpha", Backend: "claude-sdk", Status: session.StatusRunning, PodReady: true},
		{ID: "beta", ProjectPath: "/work/beta", Backend: "claude-sdk", Status: session.StatusRunning, PodReady: true},
	})
	m.width, m.height = 100, 30
	return m
}

func goldenTranscript() *TranscriptModel {
	tm := NewTranscript(&fakeRunnerClient{}, Session{
		State: session.State{ID: "alpha", ProjectPath: "/work/alpha", Backend: "claude-sdk"},
		Title: "alpha",
	}, nil)
	tm.width, tm.height = 100, 30
	return tm
}

func TestGoldenDashboard(t *testing.T) {
	withDeterministicRender(t, func() {
		m := goldenDashboardModel()
		golden.RequireEqual(t, []byte(m.render()))
	})
}

func TestGoldenPermissionBox(t *testing.T) {
	withDeterministicRender(t, func() {
		tm := goldenTranscript()
		tm.pending = &transcriptPermission{
			id:        "perm-1",
			tool:      "Edit",
			adds:      3,
			dels:      1,
			since:     goldenFixedNow.Add(-time.Hour), // appear transition fully elapsed
			diffLines: []string{"+ added line", "− removed line", "  context"},
		}
		tm.showDiff = true
		golden.RequireEqual(t, []byte(tm.buildPermissionBox(tm.width)))
	})
}

func TestGoldenPlanCard(t *testing.T) {
	withDeterministicRender(t, func() {
		tm := goldenTranscript()
		tm.pending = &transcriptPermission{
			id:     "plan-1",
			tool:   "ExitPlanMode",
			isPlan: true,
			plan:   "Step 1: do the thing.\nStep 2: verify it.\nStep 3: ship it.",
			since:  goldenFixedNow.Add(-time.Hour),
		}
		golden.RequireEqual(t, []byte(tm.renderPlanCard(tm.width)))
	})
}

func TestGoldenToolCard(t *testing.T) {
	withDeterministicRender(t, func() {
		tm := goldenTranscript()
		card := &toolCard{tool: "Bash", arg: "go test ./...", status: toolOK, summary: "exit 0"}
		golden.RequireEqual(t, []byte(tm.renderToolCard(card, tm.width)))
	})
}

func TestGoldenConfirmDialog(t *testing.T) {
	withDeterministicRender(t, func() {
		m := goldenDashboardModel()
		m.confirm = &confirmPrompt{message: "Destroy session alpha and its PVC?", id: "alpha"}
		golden.RequireEqual(t, []byte(m.renderConfirm()))
	})
}

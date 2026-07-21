package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// golden_test.go — the D1 golden harness. It snapshots the key rendered surfaces
// under an injected clock, so the output is byte-stable and committed as
// testdata/*.golden. These baselines capture the CURRENT appearance of the
// card-shaped surfaces (permission box, plan card, confirm dialog) so the D2 kit
// migration can be proven to preserve them — any unintended visual change fails
// the snapshot. Regenerate with `-update`.
//
// The harness pins three axes so a golden is a real cross-section, not just the
// settled end-state at one theme and one size (§10 visual-testing gaps):
//   - Motion: settled goldens set SANDBOX_REDUCE_MOTION=1 (transitions collapse
//     to their end-state); the mid-motion goldens (TestGoldenRowEnter /
//     TestGoldenStatusFlash) leave motion ON and read the clock at fixed offsets
//     so an in-flight fade/flash frame is pinned.
//   - Theme: TestGoldenDashboard / TestGoldenFeed fan out over every REGISTERED
//     theme (a subtest per theme → testdata/<Test>/<Theme>.golden).
//   - Size: the *Narrow goldens pin the degraded 60×20 layout.
//
// Determinism inputs (the three sources of flakiness, all pinned):
//   - SANDBOX_REDUCE_MOTION  → settled goldens collapse transitions; motion
//     goldens instead pin nowFunc at a fixed transition offset.
//   - nowFunc fixed            → relative/elapsed times don't move.
//   - theme.GradientCapable forced   → gradient-vs-solid fallback is not env-detected.

// goldenFixedNow is the injected wall clock for golden determinism.
var goldenFixedNow = time.Date(2030, 6, 21, 12, 0, 0, 0, time.UTC)

// withDeterministicRender pins the three flakiness sources for the duration of fn
// and restores them after. Motion is collapsed to its end-state (reduce-motion),
// so this captures the SETTLED appearance; use withMotionRender for in-flight
// transition frames.
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

// withMotionRender is like withDeterministicRender but leaves MOTION ON so an
// in-flight transition renders: it clears the reduce-motion inputs
// (anim.ReduceMotion reads both SANDBOX_REDUCE_MOTION=1 and NO_COLOR) and pins
// nowFunc at goldenFixedNow+offset, so a transition whose `since` is
// goldenFixedNow is read at exactly `offset` into its window. Everything is
// injected, so the frame is byte-deterministic.
func withMotionRender(t *testing.T, offset time.Duration, fn func()) {
	t.Helper()
	t.Setenv("SANDBOX_REDUCE_MOTION", "")
	t.Setenv("NO_COLOR", "")

	oldNow := nowFunc
	oldGrad := theme.GradientCapable
	nowFunc = func() time.Time { return goldenFixedNow.Add(offset) }
	theme.GradientCapable = true
	defer func() {
		nowFunc = oldNow
		theme.GradientCapable = oldGrad
	}()

	fn()
}

// useTheme applies the named registered theme for the test and restores the
// previously-active theme on cleanup, so a theme-parameterized golden never
// leaks its palette into a sibling test (the un-parameterized goldens assume the
// default theme).
func useTheme(t *testing.T, name string) {
	t.Helper()
	prev := theme.Active()
	th, ok := theme.ByName(name)
	if !ok {
		t.Fatalf("theme %q is not registered", name)
	}
	theme.ApplyTheme(th)
	t.Cleanup(func() {
		if p, ok := theme.ByName(prev); ok {
			theme.ApplyTheme(p)
		}
	})
}

// registeredThemeNames returns every registered theme's name, discovered through
// the public Cycle/Active API (the registry has no Names() accessor). Cycle wraps
// all the way around, so the active theme is left exactly as it was found.
func registeredThemeNames(t *testing.T) []string {
	t.Helper()
	start := theme.Active()
	names := []string{start}
	for {
		theme.Cycle()
		n := theme.Active()
		if strings.EqualFold(n, start) {
			break // wrapped back to the start; palette is restored
		}
		names = append(names, n)
	}
	return names
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

// goldenMotionModel is goldenDashboardModel with a live transition stamped onto
// the rows: rowEnter reads State.CreatedAt, statusFlash reads statusChangedAt.
// The SELECTED row (alpha, cursor 0) renders without motion by design, so beta —
// the non-selected row — is where the fade/flash shows. Stamping only the axis
// under test (enter XOR flash) keeps the two transitions isolated: a zero `since`
// leaves the other transition inert (rowEnter → 1, statusFlash → 0).
func goldenMotionModel(enter, flash bool) *Model {
	m := goldenDashboardModel()
	for i := range m.sessions {
		if enter {
			m.sessions[i].State.CreatedAt = goldenFixedNow
		}
		if flash {
			m.sessions[i].statusChangedAt = goldenFixedNow
		}
	}
	return m
}

func TestGoldenDashboard(t *testing.T) {
	for _, name := range registeredThemeNames(t) {
		t.Run(name, func(t *testing.T) {
			withDeterministicRender(t, func() {
				useTheme(t, name)
				m := goldenDashboardModel()
				golden.RequireEqual(t, []byte(m.render()))
			})
		})
	}
}

// TestGoldenDashboardNarrow pins the degraded 60×20 layout (the size axis): the
// list + detail split has to reflow into a cramped viewport. It renders under the
// default theme; a stable-but-ugly frame here is the point (it locks degradation).
func TestGoldenDashboardNarrow(t *testing.T) {
	withDeterministicRender(t, func() {
		m := goldenDashboardModel()
		m.width, m.height = 60, 20
		golden.RequireEqual(t, []byte(m.render()))
	})
}

// TestGoldenRowEnter pins the row fade-in (rowEnter, 180ms ease-out) at three
// points on its curve so a mid-transition frame — not just the settled end — is a
// committed baseline. beta (non-selected) fades its title foreground from TextDim
// toward TextBody; alpha (selected) is inert by design. Offsets: 0ms (fully dim
// start), 90ms (past the ease-out knee, mostly faded in), 200ms (past the 180ms
// window → settled, no override).
func TestGoldenRowEnter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset time.Duration
	}{
		{"start_0ms", 0},
		{"mid_90ms", 90 * time.Millisecond},
		{"settled_200ms", 200 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withMotionRender(t, tc.offset, func() {
				m := goldenMotionModel(true, false)
				golden.RequireEqual(t, []byte(m.render()))
			})
		})
	}
}

// TestGoldenStatusFlash pins the status-change background pulse (statusFlash,
// 300ms) at three points: 0ms (peak tint, strength 1 → Page blended 0.4 toward
// the status accent), 150ms (mid, faint), 350ms (past the 300ms window → settled,
// no tint). beta carries the flash; alpha (selected) is inert by design.
func TestGoldenStatusFlash(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset time.Duration
	}{
		{"peak_0ms", 0},
		{"mid_150ms", 150 * time.Millisecond},
		{"settled_350ms", 350 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withMotionRender(t, tc.offset, func() {
				m := goldenMotionModel(false, true)
				golden.RequireEqual(t, []byte(m.render()))
			})
		})
	}
}

func TestGoldenConfirmDialog(t *testing.T) {
	withDeterministicRender(t, func() {
		m := goldenDashboardModel()
		m.confirm = &confirmPrompt{message: "Destroy session alpha and its PVC?", id: "alpha"}
		golden.RequireEqual(t, []byte(m.renderConfirm()))
	})
}

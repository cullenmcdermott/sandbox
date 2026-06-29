package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// --------------------------------------------------------------------------
// Phase 2: elapsed timer on the connect/create splash
// --------------------------------------------------------------------------

// ORACLE: the connecting splash shows a live elapsed timer (≥1s) so a slow
// cold-pod resume reads as progress, not a freeze.
func TestConnectingViewShowsElapsed(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	old := nowFunc
	defer func() { nowFunc = old }()
	base := time.Unix(1700000000, 0)
	nowFunc = func() time.Time { return base }

	app := NewApp(nil, nil, nil)
	app.width, app.height = 80, 24
	sess := Session{State: session.State{ID: "s1"}, Title: "cold session"}
	app.screen = ScreenConnecting
	app.connectingFor = &sess
	app.connectStage = StageResume
	app.connectStartedAt = base.Add(-5 * time.Second)

	out := stripANSI(app.connectingView().Content)
	if !strings.Contains(out, "5s") {
		t.Errorf("connecting view should show elapsed '5s'; got:\n%s", out)
	}
}

// ORACLE: under one second elapsed, no timer suffix is shown (matches the
// reconnect-header gate, avoids a noisy "(0s)").
func TestConnectingViewNoTimerUnderOneSecond(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	old := nowFunc
	defer func() { nowFunc = old }()
	base := time.Unix(1700000000, 0)
	nowFunc = func() time.Time { return base }

	app := NewApp(nil, nil, nil)
	app.width, app.height = 80, 24
	sess := Session{State: session.State{ID: "s1"}, Title: "fresh"}
	app.screen = ScreenConnecting
	app.connectingFor = &sess
	app.connectStage = StageResume
	app.connectStartedAt = base.Add(-200 * time.Millisecond)

	out := stripANSI(app.connectingView().Content)
	if strings.Contains(out, "(0s)") {
		t.Errorf("connecting view should not show a sub-second timer; got:\n%s", out)
	}
}

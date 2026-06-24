package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Fix A + T4: while a session's pod resumes, the connecting screen must show the
// cached conversation dimmed as a backdrop with a centered "Reconnecting…"
// stepper modal floating over it from frame one — not a blank splash, and not a
// thin one-line banner.
func TestConnectingPreviewShowsCachedHistory(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	cache := &fakeEventCache{loaded: []session.Event{
		{Seq: 1, Type: session.EventTurnStarted, Payload: jraw(`{"prompt":"hello"}`)},
		{Seq: 2, Type: session.EventMessageCompleted, Payload: jraw(`{"role":"assistant","content":"CACHED-PREVIEW-LINE"}`)},
	}}
	app := NewApp(nil, nil, nil)
	app.width, app.height = 80, 24
	app.dashboard.WithEventCache(cache)

	sess := Session{State: session.State{ID: "s1"}, Title: "old chat"}
	app.connectingPreview = app.buildConnectingPreview(sess)
	if app.connectingPreview == nil {
		t.Fatal("preview should be built from the cache")
	}
	app.screen = ScreenConnecting
	app.connectingFor = &sess
	app.connectStage = StageResume

	out := stripANSI(app.connectingView().Content)
	if !strings.Contains(out, "CACHED-PREVIEW-LINE") {
		t.Errorf("connecting view did not show cached history backdrop:\n%s", out)
	}
	if !strings.Contains(out, connectStageLabel(StageResume)) {
		t.Errorf("connecting view did not show the %q stage in the stepper:\n%s", connectStageLabel(StageResume), out)
	}
	// The reconnect modal is titled "Reconnecting…", not "Connecting…".
	if !strings.Contains(out, "Reconnecting to old chat…") {
		t.Errorf("connecting view did not show the Reconnecting modal title:\n%s", out)
	}
}

// COUNTER: with no cache there is nothing to preview, so the centered "connecting"
// splash is shown instead (a brand-new/uncached session must not render an empty
// transcript frame).
func TestConnectingNoCacheKeepsSplash(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	app := NewApp(nil, nil, nil)
	app.width, app.height = 80, 24

	sess := Session{State: session.State{ID: "s2"}, Title: "fresh"}
	app.connectingPreview = app.buildConnectingPreview(sess) // no event cache → nil
	if app.connectingPreview != nil {
		t.Fatal("no cache → no preview")
	}
	app.screen = ScreenConnecting
	app.connectingFor = &sess

	out := stripANSI(app.connectingView().Content)
	if !strings.Contains(out, "Connecting to fresh…") {
		t.Errorf("expected the centered splash title, got:\n%s", out)
	}
}

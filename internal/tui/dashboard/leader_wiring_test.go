package dashboard

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// The external-pane leader chord (ctrl+] then a key) is wired into App.Update on
// ScreenExternal. These tests drive the decision paths without a live PTY: a
// bare ExternalPane (nil ptmx) satisfies the a.external != nil guard, and the
// detach/timeout/jump branches only flip screens — they never touch the child.
// (handleKey on a nil-ptmx pane is a safe no-op, so the disarm/forward paths are
// also PTY-free here; real key forwarding is exercised by
// TestAppExternalPaneEscIsForwardedNotDetached, which self-skips without a PTY.)

// externalApp builds an App parked on a live (bare) external pane for the given
// sessions, cursor at row 0.
func externalApp(sessions ...Session) *App {
	app := NewApp(nil, nil, nil)
	app.dashboard.seeded = true
	app.dashboard.sessions = sessions
	app.external = &ExternalPane{sess: Session{State: session.State{ID: "ext"}}}
	app.screen = ScreenExternal
	return app
}

// TestLeaderSingleTapDoesNotDetach: a lone ctrl+] arms the leader but must NOT
// detach immediately — the pane stays on ScreenExternal, now armed.
func TestLeaderSingleTapDoesNotDetach(t *testing.T) {
	app := externalApp()

	app.Update(keyMsg("ctrl+]"))

	if app.screen != ScreenExternal {
		t.Fatalf("single ctrl+] detached (screen=%v); it should only arm the leader", app.screen)
	}
	if !app.leaderArmed {
		t.Fatal("single ctrl+] did not arm the leader")
	}
}

// TestLeaderDoubleTapDetaches: ctrl+] ctrl+] detaches to the dashboard without
// tearing the pane down (child keeps running for an instant re-open).
func TestLeaderDoubleTapDetaches(t *testing.T) {
	app := externalApp()

	app.Update(keyMsg("ctrl+]"))
	app.Update(keyMsg("ctrl+]"))

	if app.screen != ScreenDashboard {
		t.Fatalf("ctrl+] ctrl+] did not detach: screen=%v, want ScreenDashboard", app.screen)
	}
	if app.external == nil {
		t.Fatal("double-tap detach tore the pane down; it must only minimize")
	}
	if app.leaderArmed {
		t.Fatal("leader still armed after detach")
	}
}

// TestLeaderTimeoutDetaches: an armed leader that lapses (leaderTimeoutMsg for
// the current generation) resolves to detach.
func TestLeaderTimeoutDetaches(t *testing.T) {
	app := externalApp()

	app.Update(keyMsg("ctrl+]"))
	gen := app.leaderGen
	app.Update(leaderTimeoutMsg{gen: gen})

	if app.screen != ScreenDashboard {
		t.Fatalf("leaderTimeout did not detach: screen=%v, want ScreenDashboard", app.screen)
	}
	if app.external == nil {
		t.Fatal("timeout detach tore the pane down; it must only minimize")
	}
}

// TestLeaderStaleTimeoutIgnored: a timeout tagged with an OLD generation (e.g.
// from a superseded arming) must be dropped, leaving the still-armed pane on the
// external screen.
func TestLeaderStaleTimeoutIgnored(t *testing.T) {
	app := externalApp()

	app.Update(keyMsg("ctrl+]"))
	gen := app.leaderGen
	app.Update(leaderTimeoutMsg{gen: gen - 1}) // stale: belongs to no live arming

	if app.screen != ScreenExternal {
		t.Fatalf("stale leaderTimeout detached (screen=%v); it should be ignored", app.screen)
	}
	if !app.leaderArmed {
		t.Fatal("stale leaderTimeout disarmed the live leader")
	}
}

// TestLeaderJumpAttachesTarget: with a session needing attention, ctrl+] g (and
// ctrl+] k) minimize the pane and emit an attachMsg for that session; the pane
// stays live (only minimized).
func TestLeaderJumpAttachesTarget(t *testing.T) {
	sessions := []Session{
		{
			State:            session.State{ID: "ext", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
		},
		{
			State:            session.State{ID: "failed", Status: session.StatusRunning},
			sessionReadModel: sessionReadModel{DashStatus: StatusFailed},
		},
	}
	for _, tc := range []struct{ name, key string }{
		{"next-g", "g"},
		{"prev-k", "k"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := externalApp(sessions...)
			app.dashboard.cursor = 0

			app.Update(keyMsg("ctrl+]"))
			_, cmd := app.Update(keyMsg(tc.key))

			if app.screen != ScreenDashboard {
				t.Fatalf("ctrl+] %s did not minimize the pane: screen=%v", tc.key, app.screen)
			}
			if app.external == nil {
				t.Fatal("jump tore the pane down; it must only minimize (child keeps running)")
			}
			if app.leaderArmed {
				t.Fatal("leader still armed after a jump")
			}
			// The returned cmd must yield an attachMsg for the attention session.
			var got *attachMsg
			for _, m := range collectLeafMsgs(cmd) {
				if am, ok := m.(attachMsg); ok {
					got = &am
				}
			}
			if got == nil {
				t.Fatal("jump produced no attachMsg")
			}
			if got.sess.ID() != "failed" {
				t.Fatalf("attachMsg targets %q, want the failed attention session", got.sess.ID())
			}
		})
	}
}

// TestLeaderJumpNoTargetStaysExternal: with nothing needing attention, ctrl+] g
// stays on the external screen and disarms — a following g must forward/ignore,
// not re-trigger a jump.
func TestLeaderJumpNoTargetStaysExternal(t *testing.T) {
	app := externalApp(Session{
		State:            session.State{ID: "ext", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	})
	app.dashboard.cursor = 0

	app.Update(keyMsg("ctrl+]"))
	app.Update(keyMsg("g"))

	if app.screen != ScreenExternal {
		t.Fatalf("ctrl+] g with no attention target left the external screen: screen=%v", app.screen)
	}
	if app.leaderArmed {
		t.Fatal("leader still armed after a no-target jump; it must disarm so the next g forwards, not jumps")
	}
}

// TestLeaderForwardDisarms: after arming, a non-leader key (that isn't g/k)
// disarms and takes the forward path, keeping the pane on the external screen.
func TestLeaderForwardDisarms(t *testing.T) {
	app := externalApp()

	app.Update(keyMsg("ctrl+]"))
	app.Update(keyMsg("a")) // arming ctrl+] swallowed; this 'a' forwards to the child

	if app.screen != ScreenExternal {
		t.Fatalf("forwarded key left the external screen: screen=%v", app.screen)
	}
	if app.leaderArmed {
		t.Fatal("leader still armed after forwarding a non-leader key")
	}
}

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// A codex session's external pane is the app-server websocket, which only
// exists locally if Connect asks for its port-forward — and only WORKS if the
// pod-side server is already up when the URL is handed out. These tests pin that
// contract: a full codex connect forwards runner+SSH+codex, waits for the
// app-server's /readyz under its own StageCodex phase, and hands back an
// External carrying the ws:// URL with no credentials; an unready app-server
// fails the connect instead; an observer connect forwards the runner port only
// and leaves External nil (what omni-proxy's captureExternalURL relies on to
// keep its previously captured URL); and a Backend that returns an incomplete
// Forwards is an error rather than a Connection with a silently missing pane.

// runnerHealthPort starts a runner-like /healthz on loopback and returns the
// port it listens on, so a fake forward handle can point Connect's concrete
// runner client at something that actually answers. It reports the CLI's own
// protocol version, keeping the handshake warning off these assertions.
func runnerHealthPort(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":          "ok",
			"protocolVersion": session.ProtocolVersion,
		})
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

// appServerPort stands in for the pod-side `codex app-server --listen ws://…`,
// which serves GET /readyz on the same port as its websocket listener. It
// records the paths it was asked for so a test can prove the connect probed
// readiness rather than assuming it.
func appServerPort(t *testing.T) (port int, paths func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// codexBackend wires a fake Backend for a warm, running codex session whose
// PortForward returns handles for exactly the named ports. The runner handle
// points at a live /healthz; the codex handle at a live app-server stand-in
// (nil paths when no codex port was requested).
//
// WorkspacePath names a worktree that does not exist, which puts Connect on the
// missing-worktree branch: it warns and skips file sync (the runner surface
// still comes up) instead of generating an SSH key and writing
// <stateDir>/ssh/config from its background goroutine. That isolates these
// tests on the codex branch — and avoids a goroutine nothing can join from
// outside (Close only cancels it, and closeHandles drops the syncTask AwaitSync
// would have waited on) racing t.TempDir's cleanup.
func codexBackend(t *testing.T, id string, ports ...PortName) (*fakeBackend, func() []string) {
	t.Helper()
	be := newFakeBackend()
	be.statusState = State{
		ID:            session.ID(id),
		Status:        session.StatusRunning,
		PodReady:      true,
		Backend:       session.BackendCodex,
		ProjectPath:   "/w",
		WorkspacePath: filepath.Join(t.TempDir(), "worktree-was-deleted"),
	}
	var codexPaths func() []string
	handles := Forwards{}
	for i, name := range ports {
		switch name {
		case PortRunner:
			handles[name] = newFakeForwardHandle(runnerHealthPort(t))
		case PortCodex:
			var p int
			p, codexPaths = appServerPort(t)
			handles[name] = newFakeForwardHandle(p)
		default:
			handles[name] = newFakeForwardHandle(30000 + i)
		}
	}
	be.handles = handles
	return be, codexPaths
}

// portNames returns the requested forward names, so a test asserts on the
// forward SET rather than on slice order.
func portNames(specs []session.PortSpec) map[PortName]bool {
	out := map[PortName]bool{}
	for _, s := range specs {
		out[s.Name] = true
	}
	return out
}

// A full codex connect must forward the app-server port alongside runner+SSH and
// publish its local address as External.URL. The empty Username/Password are the
// contract, not an oversight: the app-server has no auth and binds loopback in
// the pod, so the port-forward is the access control.
func TestConnectCodexFullForwardsCodexPortAndPublishesExternal(t *testing.T) {
	be, codexPaths := codexBackend(t, "codex-app-server-full", PortRunner, PortSSH, PortCodex)
	c, _, _ := fakeClient(t, be)

	var stages []Stage
	sess := c.Open("codex-app-server-full")
	defer sess.Close() // cancel the background sync/reaper goroutines before cleanup
	conn, err := sess.Connect(context.Background(), ConnectOptions{
		OnPhase: func(st Stage, _ string) { stages = append(stages, st) },
	})
	if err != nil {
		t.Fatalf("Connect = %v, want a live codex connection", err)
	}

	got := portNames(be.gotSpecs)
	for _, want := range []PortName{PortRunner, PortSSH, PortCodex} {
		if !got[want] {
			t.Errorf("full codex connect did not forward %q (forwarded %v)", want, got)
		}
	}
	if len(be.gotSpecs) != 3 {
		t.Errorf("full codex connect forwarded %d ports (%v), want exactly runner+ssh+codex", len(be.gotSpecs), got)
	}

	if conn.External == nil {
		t.Fatal("full codex connect left Connection.External nil; a downstream consumer " +
			"(omni-proxy captureExternalURL) has no app-server URL to attach to")
	}
	codexPort, _ := be.handles.LocalPort(PortCodex)
	if want := fmt.Sprintf("ws://127.0.0.1:%d", codexPort); conn.External.URL != want {
		t.Errorf("External.URL = %q, want %q", conn.External.URL, want)
	}
	if conn.External.Username != "" || conn.External.Password != "" {
		t.Errorf("External creds = %q/%q, want both empty: the codex app-server has no auth",
			conn.External.Username, conn.External.Password)
	}
	// The codex branch needs no Secret material of its own, so it must not charge
	// the connect the opencode password round trip ([R1d] fetches it only for
	// opencode sessions).
	if n := be.callCount("opencodepw"); n != 0 {
		t.Errorf("OpencodePassword called %d times on a codex connect, want 0", n)
	}

	// Readiness: the connect must have ASKED the app-server, over the same probe
	// the opencode branch uses, and must report the wait as its own phase so the
	// splash shows a live step instead of sitting on "Syncing files".
	if got := codexPaths(); len(got) == 0 || got[0] != "/readyz" {
		t.Errorf("codex connect probed %v, want a GET /readyz readiness check", got)
	}
	if !slices.Contains(stages, StageCodex) {
		t.Errorf("connect emitted stages %v, want StageCodex among them", stages)
	}
	// The wait must land AFTER sync and BEFORE attach, or the stepper regresses.
	ci, ai := slices.Index(stages, StageCodex), slices.Index(stages, StageAttach)
	if si := slices.Index(stages, StageSync); si > ci || ci > ai {
		t.Errorf("stage order %v: want Sync < Codex < Attach", stages)
	}
}

// The wait has to be load-bearing: if the app-server never answers, Connect must
// fail rather than hand back a URL the local TUI would race, and must tear the
// forwards down on the way out.
func TestConnectCodexUnreadyAppServerFailsTheConnect(t *testing.T) {
	be, _ := codexBackend(t, "codex-app-server-unready", PortRunner, PortSSH, PortCodex)
	// Point the codex forward at a port nothing listens on: bind, then close.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadPort := dead.Listener.Addr().(*net.TCPAddr).Port
	dead.Close()
	be.handles[PortCodex] = newFakeForwardHandle(deadPort)
	c, _, _ := fakeClient(t, be)

	// Bound the 30s readiness budget: the probe retries transport errors, so the
	// context is what ends this test.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	sess := c.Open("codex-app-server-unready")
	defer sess.Close()
	_, err := sess.Connect(ctx, ConnectOptions{})
	if err == nil {
		t.Fatal("Connect succeeded against an app-server that never answered")
	}
	if !strings.Contains(err.Error(), "codex app-server not ready") {
		t.Errorf("Connect error = %v, want it to name the codex readiness failure", err)
	}
	for name, h := range be.handles {
		if !h.(*fakeForwardHandle).closed {
			t.Errorf("%q forward left open after the failed readiness wait (SPDY leak)", name)
		}
	}
}

// An observer connect reads the runner event stream and nothing else, so it
// forwards the runner port only whatever the backend — and leaves External nil.
// omni-proxy depends on that nil: it keeps the URL captured by the last full
// connect rather than overwriting it with an empty one.
func TestConnectCodexObserverForwardsRunnerOnlyAndLeavesExternalNil(t *testing.T) {
	be, _ := codexBackend(t, "codex-app-server-obs", PortRunner)
	c, _, _ := fakeClient(t, be)

	sess := c.Open("codex-app-server-obs")
	defer sess.Close()
	conn, err := sess.Connect(context.Background(), ConnectOptions{Observer: true})
	if err != nil {
		t.Fatalf("observer Connect = %v, want success", err)
	}

	got := portNames(be.gotSpecs)
	if len(be.gotSpecs) != 1 || !got[PortRunner] {
		t.Errorf("observer codex connect forwarded %v, want the runner port only", got)
	}
	if conn.External != nil {
		t.Errorf("observer codex connect set External = %+v, want nil", conn.External)
	}
}

// A Backend that returns a Forwards without the codex handle must fail the
// connect rather than hand back a Connection whose pane silently doesn't exist —
// and must tear the forwards down on the way out, since they were already
// published by then.
func TestConnectCodexMissingForwardIsAnError(t *testing.T) {
	be, _ := codexBackend(t, "codex-app-server-nofwd", PortRunner, PortSSH)
	c, _, _ := fakeClient(t, be)

	_, err := c.Open("codex-app-server-nofwd").Connect(context.Background(), ConnectOptions{})
	if err == nil {
		t.Fatal("Connect succeeded with no codex forward, want an error")
	}
	if !strings.Contains(err.Error(), string(PortCodex)) {
		t.Errorf("Connect error = %v, want it to name the missing %q forward", err, PortCodex)
	}
	for name, h := range be.handles {
		if !h.(*fakeForwardHandle).closed {
			t.Errorf("%q forward left open after the failed codex connect (SPDY leak)", name)
		}
	}
}

package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// fakeHealthChecker is a test double for the healthChecker seam. It returns the
// scripted errors on successive Health calls (the last value repeats past the
// script) and counts the calls, mirroring reap_test.go's fakeIdleChecker.
type fakeHealthChecker struct {
	errs  []error
	calls int
}

func (f *fakeHealthChecker) Health(_ context.Context) error {
	i := f.calls
	f.calls++
	if i < len(f.errs) {
		return f.errs[i]
	}
	if len(f.errs) > 0 {
		return f.errs[len(f.errs)-1]
	}
	return nil
}

// TestWaitHealthyImmediate: a runner that answers OK on the first probe returns
// nil after exactly one Health call (no retry, no sleep).
func TestWaitHealthyImmediate(t *testing.T) {
	hc := &fakeHealthChecker{errs: []error{nil}}
	if err := waitHealthy(context.Background(), hc); err != nil {
		t.Fatalf("waitHealthy = %v, want nil", err)
	}
	if hc.calls != 1 {
		t.Errorf("Health called %d times, want 1", hc.calls)
	}
}

// TestWaitHealthyRetriesThenReady: a freshly resumed pod fails a couple of
// probes before /healthz answers; waitHealthyWithin must keep polling within the
// budget and then succeed.
func TestWaitHealthyRetriesThenReady(t *testing.T) {
	boot := errors.New("connection refused")
	hc := &fakeHealthChecker{errs: []error{boot, boot, nil}}
	err := waitHealthyWithin(context.Background(), hc, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("waitHealthyWithin = %v, want nil after the pod became ready", err)
	}
	if hc.calls != 3 {
		t.Errorf("Health called %d times, want 3 (two boots + ready)", hc.calls)
	}
}

// TestWaitHealthyDeadline: a runner that never answers surfaces the last probe
// error once the budget is exhausted, rather than blocking forever.
func TestWaitHealthyDeadline(t *testing.T) {
	sentinel := errors.New("still booting")
	hc := &fakeHealthChecker{errs: []error{sentinel}}
	err := waitHealthyWithin(context.Background(), hc, 5*time.Millisecond, time.Millisecond)
	if !errors.Is(err, sentinel) {
		t.Fatalf("waitHealthyWithin = %v, want the last probe error %v", err, sentinel)
	}
	if hc.calls == 0 {
		t.Error("Health was never called")
	}
}

// TestWaitHealthyContextCancelled: a cancelled context short-circuits the retry
// wait and returns ctx.Err() (not the probe error), so a caller that gives up
// isn't held for the full budget.
func TestWaitHealthyContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hc := &fakeHealthChecker{errs: []error{errors.New("unreachable")}}
	// Generous budget: only the cancelled context should end the loop.
	err := waitHealthyWithin(ctx, hc, time.Hour, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitHealthyWithin = %v, want context.Canceled", err)
	}
}

// --- Session.Connect pre-runner error paths (the [F3] residual) ---
//
// Connect builds a CONCRETE *runner.Client (runner.New) from the port-forward's
// local port and then blocks on waitHealthy against a real socket, so its
// runtime happy path can't be driven through the fake Backend seam without a
// runner-factory injection (a larger refactor, out of this batch's scope). The
// branches BEFORE runner.New — status/resume/pod-wait/port-forward/token — are
// all reachable through fakeBackend, and are pinned here.

// fakeForwardHandle is a no-op session.ForwardHandle so PortForward can return a
// usable handle (Connect reads handles[0].LocalPort()) without a live forward.
type fakeForwardHandle struct {
	port   int
	closed bool
	done   chan struct{}
}

func newFakeForwardHandle(port int) *fakeForwardHandle {
	return &fakeForwardHandle{port: port, done: make(chan struct{})}
}
func (h *fakeForwardHandle) LocalPort() int        { return h.port }
func (h *fakeForwardHandle) Close() error          { h.closed = true; return nil }
func (h *fakeForwardHandle) Done() <-chan struct{} { return h.done }

// TestConnectCloseRaceDoesNotResurrect pins the V9 fix: Close racing an in-flight
// Connect must NOT leave a live connection or an orphaned background goroutine.
// We stall Connect inside PortForward, call Close (which bumps the generation),
// then release the forward. Connect's generation guard at setHandlesGen must fire:
// it closes its own forwards and aborts instead of publishing a runner over a
// port-forward Close already tore down. Run under `go test -race`.
func TestConnectCloseRaceDoesNotResurrect(t *testing.T) {
	be := newFakeBackend()
	be.statusState = State{ID: "claude-sdk-x", Status: session.StatusRunning, ProjectPath: "/w"}
	handle := newFakeForwardHandle(12345)
	be.handles = []session.ForwardHandle{handle}

	entered := make(chan struct{})
	release := make(chan struct{})
	be.portForwardHook = func() {
		close(entered)
		<-release
	}
	c, _, _ := fakeClient(t, be)
	sess := c.Open("claude-sdk-x")

	errCh := make(chan error, 1)
	go func() {
		// Observer mode: read-only connect that still port-forwards and publishes
		// handles/runner, so it exercises the generation guard without the full
		// sync/opencode setup.
		_, err := sess.Connect(context.Background(), ConnectOptions{Observer: true})
		errCh <- err
	}()

	<-entered        // Connect is stalled inside PortForward.
	_ = sess.Close() // Supersede it (bumps the generation, tears down handles).
	close(release)   // Let PortForward return so Connect resumes and hits the guard.

	err := <-errCh
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("superseded Connect = %v, want an error wrapping ErrNotConnected", err)
	}
	// The connection must not be resurrected: no live runner, forwards closed.
	if sess.Runner() != nil {
		t.Error("Runner() is non-nil after Close raced Connect — resurrected connection")
	}
	if !handle.closed {
		t.Error("port-forward handle not closed after the superseded Connect (leak)")
	}
}

func TestConnectStatusError(t *testing.T) {
	be := newFakeBackend()
	be.statusErr = errors.New("api server down")
	c, _, _ := fakeClient(t, be)

	_, err := c.Open("claude-sdk-x").Connect(context.Background(), ConnectOptions{})
	if !errors.Is(err, be.statusErr) {
		t.Fatalf("Connect = %v, want the Status error to propagate", err)
	}
}

func TestConnectGone(t *testing.T) {
	be := newFakeBackend()
	be.statusState = State{ID: "claude-sdk-x", Status: session.StatusGone}
	c, _, _ := fakeClient(t, be)

	_, err := c.Open("claude-sdk-x").Connect(context.Background(), ConnectOptions{})
	if !errors.Is(err, session.ErrSessionGone) {
		t.Fatalf("Connect = %v, want ErrSessionGone", err)
	}
}

func TestConnectSuspendedObserverRefused(t *testing.T) {
	be := newFakeBackend()
	be.statusState = State{ID: "claude-sdk-x", Status: session.StatusSuspended}
	c, _, _ := fakeClient(t, be)

	_, err := c.Open("claude-sdk-x").Connect(context.Background(), ConnectOptions{Observer: true})
	if !errors.Is(err, ErrSessionSuspended) {
		t.Fatalf("Connect = %v, want ErrSessionSuspended for an observer on a suspended session", err)
	}
	// An observer must NOT resume (a cluster mutation): Resume was never called.
	if len(be.gotRefs["resume"]) != 0 {
		t.Error("observer Connect resumed a suspended session")
	}
}

func TestConnectResumeError(t *testing.T) {
	be := newFakeBackend()
	be.statusState = State{ID: "claude-sdk-x", Status: session.StatusSuspended}
	be.resumeErr = errors.New("resume boom")
	c, _, _ := fakeClient(t, be)

	_, err := c.Open("claude-sdk-x").Connect(context.Background(), ConnectOptions{})
	if err == nil || !errors.Is(err, be.resumeErr) {
		t.Fatalf("Connect = %v, want the Resume error", err)
	}
}

func TestConnectPortForwardError(t *testing.T) {
	be := newFakeBackend()
	be.statusState = State{ID: "claude-sdk-x", Status: session.StatusRunning, ProjectPath: "/w"}
	be.portErr = errors.New("forward boom")
	c, _, _ := fakeClient(t, be)

	_, err := c.Open("claude-sdk-x").Connect(context.Background(), ConnectOptions{})
	if err == nil || !errors.Is(err, be.portErr) {
		t.Fatalf("Connect = %v, want the PortForward error", err)
	}
}

// TestConnectTokenErrorTearsDownForward pins the post-forward failure teardown:
// when RunnerToken fails after the forward is up, Connect must close the handles
// (no leaked SPDY goroutines) and return the error before ever reaching the
// concrete runner client.
func TestConnectTokenErrorTearsDownForward(t *testing.T) {
	handle := newFakeForwardHandle(12345)
	be := newFakeBackend()
	be.statusState = State{ID: "claude-sdk-x", Status: session.StatusRunning, ProjectPath: "/w"}
	be.handles = []session.ForwardHandle{handle}
	be.tokenErr = errors.New("no token")
	c, _, _ := fakeClient(t, be)

	_, err := c.Open("claude-sdk-x").Connect(context.Background(), ConnectOptions{})
	if err == nil || !errors.Is(err, be.tokenErr) {
		t.Fatalf("Connect = %v, want the RunnerToken error", err)
	}
	if !handle.closed {
		t.Error("port-forward handle was not closed after the token failure (SPDY leak)")
	}
}

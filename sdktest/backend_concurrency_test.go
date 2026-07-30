package sdktest

// backend_concurrency_test.go — the pin for client.Backend's THREAD-SAFETY
// contract.
//
// [R1d] overlapped the session-Secret read with the port-forward to shorten the
// attach path. That is a latency win, but it silently changed what client.Backend
// demands of an implementation: a Backend that was correct when every method was
// called from one goroutine can become racy on upgrade, with nothing at the
// compile boundary to say so. client.Backend is public and externally
// implementable (docs/design-principles.md), so "must be safe for concurrent use"
// is a real part of its contract and belongs here, in the module that stands in
// for an outside consumer — not only in a doc comment.
//
// The pin is behavioral, not documentary: it proves two Backend methods are
// genuinely in flight at once.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/client"
)

// concurrentBackend answers like fakeBackend but instruments the two calls
// [R1d] overlaps. PortForward parks until RunnerToken has been ENTERED, so if
// the SDK ever serialized them again the park would time out instead of
// observing the overlap — the failure is a timeout, not a flake.
type concurrentBackend struct {
	tokenEntered chan struct{} // closed on first entry to RunnerToken
	overlapped   chan struct{} // closed by PortForward when it saw the overlap

	mu      sync.Mutex
	inother bool

	stop error
}

func newConcurrentBackend() *concurrentBackend {
	return &concurrentBackend{
		tokenEntered: make(chan struct{}),
		overlapped:   make(chan struct{}),
		stop:         errors.New("stop the connect here"),
	}
}

func (b *concurrentBackend) Namespace() string { return "fake-namespace" }

func (b *concurrentBackend) CreateSession(context.Context, client.Spec) (client.Ref, error) {
	return client.Ref{}, nil
}

func (b *concurrentBackend) Status(context.Context, client.Ref) (client.State, error) {
	return client.State{ID: "sess-conc", ProjectPath: "/w"}, nil
}

func (b *concurrentBackend) List(context.Context) ([]client.State, error) { return nil, nil }

func (b *concurrentBackend) Watch(context.Context) (<-chan client.StateEvent, error) {
	return nil, nil
}

func (b *concurrentBackend) Suspend(context.Context, client.Ref) error { return nil }

func (b *concurrentBackend) Resume(context.Context, client.Ref, client.ResumeOptions) error {
	return nil
}

func (b *concurrentBackend) Destroy(context.Context, client.Ref) error { return nil }

func (b *concurrentBackend) StartWithProgress(context.Context, client.Ref, func(string)) error {
	return nil
}

func (b *concurrentBackend) PortForward(_ context.Context, _ client.Ref, _ []client.PortSpec) (client.Forwards, error) {
	b.mu.Lock()
	b.inother = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.inother = false
		b.mu.Unlock()
	}()

	// Wait for the Secret read to enter while we are still here. A serialized
	// implementation never gets there, and this falls through on the timeout with
	// overlapped left open.
	select {
	case <-b.tokenEntered:
		close(b.overlapped)
	case <-time.After(3 * time.Second):
	}
	return nil, b.stop
}

func (b *concurrentBackend) RunnerToken(context.Context, client.Ref) (string, error) {
	b.mu.Lock()
	inForward := b.inother
	b.mu.Unlock()
	if inForward {
		// Only signal when PortForward is genuinely mid-call: that is the claim.
		close(b.tokenEntered)
	}
	return "tok", nil
}

func (b *concurrentBackend) OpencodePassword(context.Context, client.Ref) (string, error) {
	return "", nil
}

func (b *concurrentBackend) EnsureReaper(context.Context, client.Ref, client.ReaperOptions) error {
	return nil
}

var _ client.Backend = (*concurrentBackend)(nil)

// TestBackendIsCalledConcurrently is the contract pin: Connect overlaps
// independent Backend calls, so an external implementation must be safe for
// concurrent use. Run this package with -race and an implementation that guards
// nothing is caught here rather than in a consumer's production attach.
func TestBackendIsCalledConcurrently(t *testing.T) {
	be := newConcurrentBackend()
	c, err := client.New(client.WithBackend(be), client.WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	// Stops at the port-forward; everything under test happens before that.
	_, _ = c.Open("sess-conc").Connect(context.Background(), client.ConnectOptions{})

	select {
	case <-be.overlapped:
	default:
		t.Fatal("no two Backend methods were ever in flight at once. Either the " +
			"connect path stopped overlapping them (in which case this pin is stale " +
			"and the concurrency requirement can come back out of the Backend doc), " +
			"or the fake never reached the port-forward.")
	}
}

// The other half of the contract, stated where an external implementer will
// meet it: Connect may return while an overlapped call is STILL running (an
// early error return does not join it), so a Backend must tolerate a call that
// outlives the Connect that made it.
func TestBackendCallMayOutliveConnect(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	be := &slowTokenBackend{release: release, entered: make(chan struct{}, 1)}
	c, err := client.New(client.WithBackend(be), client.WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_, _ = c.Open("sess-slow").Connect(context.Background(), client.ConnectOptions{})
		close(done)
	}()

	select {
	case <-be.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("RunnerToken was never called")
	}
	select {
	case <-done:
		// Connect returned with RunnerToken still parked — exactly the documented
		// behaviour. An implementation that frees per-call state on Connect's
		// return would corrupt the in-flight call.
	case <-time.After(5 * time.Second):
		t.Fatal("Connect waited for an overlapped Backend call it had already " +
			"decided to fail past; the doc says it may return first")
	}
}

// slowTokenBackend parks in RunnerToken so a test can observe Connect returning
// while that call is still in flight.
type slowTokenBackend struct {
	concurrentBackend
	release chan struct{}
	entered chan struct{}
}

func (b *slowTokenBackend) PortForward(context.Context, client.Ref, []client.PortSpec) (client.Forwards, error) {
	return nil, errors.New("stop the connect here")
}

func (b *slowTokenBackend) RunnerToken(context.Context, client.Ref) (string, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return "tok", nil
}

var _ client.Backend = (*slowTokenBackend)(nil)

// --- compile pins for the advisory surface --------------------------------

// SyncAdvisory is the non-blocking read an attached UI polls for advisories the
// background sync phase discovers after Connect returns — including the ones
// that land after the sync task has already settled, which AwaitSync can never
// return because there is nothing left to block on. Pinned as a method
// expression so a retype or removal fails here first.
var (
	_ func(*client.Session) string                           = (*client.Session).SyncAdvisory
	_ func(*client.Session, context.Context) (string, error) = (*client.Session).AwaitSync
)

package k8s

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"
)

// tinyReconnectBackoff shrinks the reconnect backoff so the retry loop churns
// fast in tests, restoring the originals on cleanup.
func tinyReconnectBackoff(t *testing.T) {
	t.Helper()
	savedInit, savedMax := forwardReconnectBackoffInitial, forwardReconnectBackoffMax
	forwardReconnectBackoffInitial = 5 * time.Millisecond
	forwardReconnectBackoffMax = 5 * time.Millisecond
	t.Cleanup(func() {
		forwardReconnectBackoffInitial = savedInit
		forwardReconnectBackoffMax = savedMax
	})
}

func podWithSession(sid string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sid + "-pod",
			Namespace: "agent-sessions",
			Labels:    map[string]string{labelSessionID: sid},
		},
	}
}

// REGRESSION (§1d): when the session's Sandbox is destroyed from another shell,
// the pod that backed the forward is gone for good. On the next reconnect the
// pod re-resolve returns NotFound, and runForward must STOP retrying the vanished
// pod (rather than hammering it at the capped ≤10s cadence forever) and surface
// the terminal state to the handle owner by closing h.done with a NotFound error.
func TestRunForwardStopsWhenSessionGone(t *testing.T) {
	tinyReconnectBackoff(t)

	const sid = "sess-gone"
	// No Sandbox seeded and no pods: the pod re-resolve finds no live pod, then
	// the Sandbox Get returns NotFound -> a permanently-gone session.
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	// The forward always drops, forcing the loop into its reconnect path.
	var attempts int32
	b.forwardOnceFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, ready chan struct{}) error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("forward dropped")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &forwardHandle{localPort: 12345, done: make(chan struct{}), cancel: cancel}
	ready := make(chan struct{})
	go b.runForward(ctx, podWithSession(sid), 12345, 8080, h, ready)

	select {
	case <-h.Done():
		// Terminated, as required.
	case <-time.After(2 * time.Second):
		t.Fatal("runForward kept retrying a destroyed session; it must terminate on a NotFound re-resolve")
	}

	if !k8serrors.IsNotFound(h.err) {
		t.Fatalf("h.err = %v, want a NotFound terminal error", h.err)
	}
	// It should give up quickly, not spin thousands of times.
	if n := atomic.LoadInt32(&attempts); n > 5 {
		t.Fatalf("forwardOnce attempted %d times; a gone session should terminate after a couple of reconnects", n)
	}
}

// A transient reschedule gap (Sandbox still present, pod momentarily absent) must
// stay on the retry path: runForward keeps reconnecting rather than treating the
// missing pod as terminal. This is the counter to TestRunForwardStopsWhenSessionGone:
// only a genuinely-gone session (Sandbox NotFound) is terminal.
func TestRunForwardKeepsRetryingWhileSessionExists(t *testing.T) {
	tinyReconnectBackoff(t)

	const sid = "sess-rescheduling"
	// Sandbox exists but there is no live pod yet (reschedule gap): the pod
	// re-resolve returns a plain error, NOT NotFound, so the loop must keep going.
	agents := agentsfake.NewSimpleClientset()
	seedSandboxFor(t, agents, sid)
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	var attempts int32
	b.forwardOnceFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, ready chan struct{}) error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("forward dropped")
	}

	ctx, cancel := context.WithCancel(context.Background())
	h := &forwardHandle{localPort: 12346, done: make(chan struct{}), cancel: cancel}
	ready := make(chan struct{})
	go b.runForward(ctx, podWithSession(sid), 12346, 8080, h, ready)

	// Let it churn through several reconnect attempts.
	time.Sleep(200 * time.Millisecond)

	select {
	case <-h.Done():
		t.Fatal("runForward terminated on a transient reschedule gap; it must keep retrying while the Sandbox still exists")
	default:
	}
	if n := atomic.LoadInt32(&attempts); n < 3 {
		t.Fatalf("expected multiple retry attempts during a reschedule gap, got %d", n)
	}

	// Cancel (Close()) is the only intended teardown; the loop must then stop.
	cancel()
	select {
	case <-h.Done():
	case <-time.After(time.Second):
		t.Fatal("runForward did not stop after the handle was closed")
	}
	if !errors.Is(h.err, context.Canceled) {
		t.Fatalf("h.err = %v, want context.Canceled after Close()", h.err)
	}
}

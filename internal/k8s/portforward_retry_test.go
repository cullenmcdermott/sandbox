package k8s

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"

	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"
)

// notFoundErr builds a typed apierrors NotFound the way the client-go fake
// clientset does, so classifyForwardReconnect's k8serrors.IsNotFound check sees
// exactly what runForward sees in production.
func notFoundErr() error {
	return k8serrors.NewNotFound(schema.GroupResource{Group: "agents.x-k8s.io", Resource: "sandboxes"}, "sess")
}

// classifyForwardReconnect is the pure retry-decision the reconnect loop makes
// after re-resolving the pod. Pin every branch, including the edge cases that
// fall through to "retry stale".
func TestClassifyForwardReconnect(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1"}}

	cases := []struct {
		name string
		pod  *corev1.Pod
		err  error
		want forwardReconnectAction
	}{
		{
			name: "resolved live pod retargets the forward",
			pod:  pod,
			err:  nil,
			want: forwardUseNewPod,
		},
		{
			name: "typed NotFound is terminal (session gone)",
			pod:  nil,
			err:  notFoundErr(),
			want: forwardTerminal,
		},
		{
			name: "wrapped NotFound is still terminal",
			pod:  nil,
			err:  fmt.Errorf("re-resolve: %w", notFoundErr()),
			want: forwardTerminal,
		},
		{
			name: "plain error (reschedule gap / API blip) retries stale",
			pod:  nil,
			err:  errors.New("no live pod for session sess"),
			want: forwardRetryStale,
		},
		{
			name: "context cancellation is transient, not terminal",
			pod:  nil,
			err:  context.Canceled,
			want: forwardRetryStale,
		},
		{
			name: "nil error but nil pod is treated as transient",
			pod:  nil,
			err:  nil,
			want: forwardRetryStale,
		},
		{
			// A NotFound error that nonetheless came back with a pod: the terminal
			// signal wins only when there is no usable pod. The original switch put
			// the "nil err && non-nil pod" case first, so a non-nil pod with a
			// non-nil error never matches forwardUseNewPod and falls to NotFound.
			name: "NotFound wins over a stray non-nil pod",
			pod:  pod,
			err:  notFoundErr(),
			want: forwardTerminal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyForwardReconnect(tc.pod, tc.err); got != tc.want {
				t.Fatalf("classifyForwardReconnect(%v, %v) = %d, want %d", tc.pod, tc.err, got, tc.want)
			}
		})
	}
}

// nextForwardBackoff is the capped-exponential reconnect progression. Pin the
// doubling, the cap clamp, and idempotence at the cap.
func TestNextForwardBackoff(t *testing.T) {
	const max = 10 * time.Second

	cases := []struct {
		name    string
		current time.Duration
		max     time.Duration
		want    time.Duration
	}{
		{"initial doubles", 500 * time.Millisecond, max, 1 * time.Second},
		{"doubles again", 1 * time.Second, max, 2 * time.Second},
		{"doubles again", 2 * time.Second, max, 4 * time.Second},
		{"doubles again", 4 * time.Second, max, 8 * time.Second},
		{"doubling past the cap clamps to max", 8 * time.Second, max, max},
		{"at the cap stays at the cap", max, max, max},
		{"already over the cap clamps down", 30 * time.Second, max, max},
		{"exactly-half-of-cap doubles to the cap without clamping", 5 * time.Second, max, max},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextForwardBackoff(tc.current, tc.max); got != tc.want {
				t.Fatalf("nextForwardBackoff(%s, %s) = %s, want %s", tc.current, tc.max, got, tc.want)
			}
		})
	}
}

// The full backoff progression the loop produces, driven through the pure func,
// must match the documented 500ms→1s→2s→4s→8s→10s→10s… ceiling.
func TestForwardBackoffProgression(t *testing.T) {
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		10 * time.Second,
		10 * time.Second,
		10 * time.Second,
	}
	backoff := forwardReconnectBackoffInitial // 500ms in production
	for i, w := range want {
		backoff = nextForwardBackoff(backoff, forwardReconnectBackoffMax)
		if backoff != w {
			t.Fatalf("step %d: backoff = %s, want %s", i, backoff, w)
		}
	}
}

// ---- Done-signaling invariants (the C1 Close seam) ----

// Close() must cause Done() to fire: cancel() tears down the reconnect loop and
// the deferred close(h.done) runs. Exercises the real runForward.
func TestRunForwardCloseCausesDone(t *testing.T) {
	tinyReconnectBackoff(t)

	b := NewForClients(agentsfake.NewSimpleClientset(), fake.NewSimpleClientset(), "agent-sessions")
	// A forward that stays alive until the ctx is cancelled — the healthy steady
	// state, so the only exit is Close().
	b.forwardOnceFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, ready chan struct{}) error {
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	h := &forwardHandle{localPort: 12345, done: make(chan struct{}), cancel: cancel}
	ready := make(chan struct{})
	go b.runForward(ctx, podWithSession("sess"), 12345, 8080, h, ready)

	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("forward never became ready")
	}

	// Done must not fire before Close.
	select {
	case <-h.done:
		t.Fatal("h.done closed before Close()")
	case <-time.After(20 * time.Millisecond):
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-h.done:
	case <-time.After(time.Second):
		t.Fatal("Close() did not cause Done() to fire")
	}
	if !errors.Is(h.err, context.Canceled) {
		t.Fatalf("h.err = %v, want context.Canceled after Close()", h.err)
	}
}

// Close() must be idempotent: repeated and concurrent calls must not panic
// (the underlying context.CancelFunc is idempotent) and Done() must still close
// exactly once. Run under -race, this pins that only runForward closes h.done
// and that many concurrent Close()s race safely.
func TestRunForwardCloseIsIdempotentAndDoneClosesOnce(t *testing.T) {
	tinyReconnectBackoff(t)

	b := NewForClients(agentsfake.NewSimpleClientset(), fake.NewSimpleClientset(), "agent-sessions")
	b.forwardOnceFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, ready chan struct{}) error {
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	h := &forwardHandle{localPort: 12345, done: make(chan struct{}), cancel: cancel}
	ready := make(chan struct{})
	go b.runForward(ctx, podWithSession("sess"), 12345, 8080, h, ready)

	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("forward never became ready")
	}

	// Hammer Close() concurrently from many goroutines.
	const closers = 16
	var wg sync.WaitGroup
	wg.Add(closers)
	for i := 0; i < closers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := h.Close(); err != nil {
					t.Errorf("Close returned error: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	// Done must be closed (once). A double-close of h.done would have panicked in
	// runForward's deferred close; reaching here means it closed exactly once.
	select {
	case <-h.done:
	case <-time.After(time.Second):
		t.Fatal("Done() never fired after concurrent Close()")
	}
	// A closed channel keeps yielding; ensure a second receive doesn't block,
	// confirming the channel is closed rather than merely having one buffered send.
	select {
	case <-h.done:
	default:
		t.Fatal("h.done not closed (a send would not be repeatable)")
	}
}

// Concurrent Close() racing an independent error-driven teardown: the forward
// keeps dropping (error path repeatedly sets h.err) while a Close() lands. The
// single deferred close(h.done) must still fire exactly once with no data race
// on h.err between the loop goroutine and the reader — the loop owns h.err and
// the reader only reads it after Done(). Meaningful only under -race.
func TestRunForwardConcurrentErrorAndClose(t *testing.T) {
	tinyReconnectBackoff(t)

	// Sandbox present (so re-resolve is a transient gap, not terminal): the loop
	// churns the error path continuously until Close() cancels it.
	agents := agentsfake.NewSimpleClientset()
	seedSandboxFor(t, agents, "sess")
	b := NewForClients(agents, fake.NewSimpleClientset(), "agent-sessions")
	b.forwardOnceFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, ready chan struct{}) error {
		return errors.New("forward dropped")
	}

	ctx, cancel := context.WithCancel(context.Background())
	h := &forwardHandle{localPort: 12345, done: make(chan struct{}), cancel: cancel}
	ready := make(chan struct{})
	go b.runForward(ctx, podWithSession("sess"), 12345, 8080, h, ready)

	// Let the error path churn, then Close() concurrently from several goroutines.
	time.Sleep(30 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Close()
		}()
	}
	wg.Wait()

	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Done() never fired under concurrent error + Close()")
	}
	// After teardown, reading h.err is safe (loop goroutine has returned).
	if h.err == nil {
		t.Fatal("expected a non-nil terminal h.err")
	}
}

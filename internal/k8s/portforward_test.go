package k8s

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// restURLForPodPortForward must build a well-formed portforward URL even when
// the REST config Host ends in a trailing slash (as the Omni proxy emits), with
// no "//" in the path. Guards the documented Omni-proxy fix.
func TestRestURLForPodPortForwardTrailingSlashHost(t *testing.T) {
	for _, host := range []string{
		"https://api.example.com/",  // trailing slash (Omni proxy)
		"https://api.example.com",   // no trailing slash
		"https://api.example.com//", // doubled trailing slash
	} {
		core, err := kubernetes.NewForConfig(&rest.Config{Host: host})
		if err != nil {
			t.Fatalf("build clientset for host %q: %v", host, err)
		}
		b := &Backend{core: core}
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "agent-sessions"}}

		u := b.restURLForPodPortForward(pod)
		wantPath := "/api/v1/namespaces/agent-sessions/pods/pod-1/portforward"
		if u.Path != wantPath {
			t.Errorf("host %q: path = %q, want %q", host, u.Path, wantPath)
		}
		if strings.Contains(u.Path, "//") {
			t.Errorf("host %q: path %q contains a double slash", host, u.Path)
		}
		if u.Host != "api.example.com" {
			t.Errorf("host %q: url host = %q, want api.example.com", host, u.Host)
		}
	}
}

// REGRESSION (S4): forwardPort must wait on the ready channel (closed by
// client-go when the port-forward is actually listening) instead of a fixed
// 3s sleep. This makes establishment faster on fast clusters and fails cleanly
// on slow ones.
func TestForwardPortWaitsOnReady(t *testing.T) {
	b := NewForClients(agentsfake.NewSimpleClientset(), fake.NewSimpleClientset(), "test-ns")

	// Mock runForward: close ready immediately (fast cluster).
	b.runForwardFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, h *forwardHandle, ready chan struct{}) {
		defer close(h.done)
		close(ready)
	}

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1"}}
	ctx := context.Background()

	start := time.Now()
	h, err := b.forwardPort(ctx, pod, session.PortSpec{Local: 0, Remote: 8080})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("forwardPort: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handle")
	}
	// Should return almost immediately (<100ms), not wait 3s.
	if elapsed > 100*time.Millisecond {
		t.Fatalf("forwardPort took %s, expected <100ms when ready closes immediately", elapsed)
	}
	h.Close()
}

// When the forward goroutine fails before ready closes, forwardPort must
// surface the error from h.done.
func TestForwardPortReturnsErrorOnFailure(t *testing.T) {
	b := NewForClients(agentsfake.NewSimpleClientset(), fake.NewSimpleClientset(), "test-ns")

	wantErr := errors.New("spdy dial failed")
	b.runForwardFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, h *forwardHandle, ready chan struct{}) {
		defer close(h.done)
		h.err = wantErr
	}

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1"}}
	ctx := context.Background()

	_, err := b.forwardPort(ctx, pod, session.PortSpec{Local: 0, Remote: 8080})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}

// When the caller context is cancelled during establishment, forwardPort must
// return ctx.Err() and tear down the forward.
func TestForwardPortRespectsCallerCancel(t *testing.T) {
	b := NewForClients(agentsfake.NewSimpleClientset(), fake.NewSimpleClientset(), "test-ns")

	// Mock runForward: never close ready, so the only way out is caller cancel.
	b.runForwardFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, h *forwardHandle, ready chan struct{}) {
		defer close(h.done)
		<-ctx.Done() // block until cancelled
	}

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1"}}
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := b.forwardPort(ctx, pod, session.PortSpec{Local: 0, Remote: 8080})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// When ready never closes and the caller doesn't cancel, forwardPort must
// time out with a descriptive error.
func TestForwardPortTimesOutWhenReadyNeverCloses(t *testing.T) {
	b := NewForClients(agentsfake.NewSimpleClientset(), fake.NewSimpleClientset(), "test-ns")

	b.runForwardFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, h *forwardHandle, ready chan struct{}) {
		defer close(h.done)
		// Never close ready — simulate a stuck port-forward.
		<-ctx.Done()
	}

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1"}}
	ctx := context.Background()

	start := time.Now()
	_, err := b.forwardPort(ctx, pod, session.PortSpec{Local: 12345, Remote: 8080})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed < 4*time.Second || elapsed > 6*time.Second {
		t.Fatalf("timeout took %s, expected ~5s", elapsed)
	}
}

// REGRESSION: a transient FIRST-attempt failure that fails before client-go
// closes `ready` must NOT strand the caller. runForward retries, and the caller's
// `ready` must be signaled on the recovering attempt — not left waiting on a
// throwaway channel until the 5s establishment timeout (which would also tear
// down the just-recovered forward). Exercises the real runForward loop via the
// forwardOnceFn seam.
func TestForwardPortRecoversWhenFirstAttemptFailsBeforeReady(t *testing.T) {
	b := NewForClients(agentsfake.NewSimpleClientset(), fake.NewSimpleClientset(), "test-ns")

	// Tiny backoff so the retry is fast in the test.
	savedInit, savedMax := forwardReconnectBackoffInitial, forwardReconnectBackoffMax
	forwardReconnectBackoffInitial = 5 * time.Millisecond
	forwardReconnectBackoffMax = 5 * time.Millisecond
	t.Cleanup(func() {
		forwardReconnectBackoffInitial = savedInit
		forwardReconnectBackoffMax = savedMax
	})

	var attempts int32
	b.forwardOnceFn = func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, ready chan struct{}) error {
		if atomic.AddInt32(&attempts, 1) == 1 {
			// First attempt fails BEFORE listening — does NOT close ready.
			return errors.New("spdy dial failed")
		}
		// Recovering attempt: becomes ready, then stays alive until teardown.
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	}

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1"}}

	start := time.Now()
	// Fixed Local port avoids freePort()'s net.Listen so the test needs no real
	// socket (the mocked forwardOnceFn never binds).
	h, err := b.forwardPort(context.Background(), pod, session.PortSpec{Local: 12399, Remote: 8080})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("forwardPort should recover after a transient first-attempt failure, got: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handle after recovery")
	}
	// Must not have waited for the 5s establishment timeout.
	if elapsed > 2*time.Second {
		t.Fatalf("forwardPort took %s; recovery should be fast (regression: caller stranded on a throwaway ready channel)", elapsed)
	}
	if n := atomic.LoadInt32(&attempts); n < 2 {
		t.Fatalf("expected >=2 forwardOnce attempts (fail then succeed), got %d", n)
	}
	h.Close()
}

package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
)

// startupTestClient builds a Client backed by fake k8s clientsets and a temp
// state dir, returning the core fake so a test can assert on the reaper Job the
// background path creates.
func startupTestClient(t *testing.T) (*Client, *fake.Clientset) {
	t.Helper()
	core := fake.NewSimpleClientset()
	backend := k8s.NewForClients(agentsfake.NewSimpleClientset(), core, "agent-sessions")
	c, err := New(WithBackend(backend), WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, core
}

// The idle reaper was moved off the foreground connect path (§5) into
// startBackgroundSync, but it must STILL run reliably — the reaper is what caps
// runaway pod cost. With doSync=false (the no-file-sync path) the background task
// does nothing but ensure the reaper, so this proves the deferred reaper still
// fires and AwaitSync observes a clean completion.
func TestBackgroundSyncRunsDeferredReaper(t *testing.T) {
	ctx := context.Background()
	c, core := startupTestClient(t)
	s := c.Open("sess-reaper")

	s.startBackgroundSync(nil, syncpkg.Spec{}, false, false, "", "", time.Minute)

	warn, err := s.AwaitSync(ctx)
	if err != nil {
		t.Fatalf("AwaitSync: %v", err)
	}
	if warn != "" {
		t.Errorf("expected no warning on a clean reaper ensure, got %q", warn)
	}
	if _, err := core.BatchV1().Jobs(k8s.ReaperNamespace).Get(ctx, "reap-sess-reaper", metav1.GetOptions{}); err != nil {
		t.Fatalf("deferred reaper Job was not created: %v", err)
	}
}

// A reaper ensure that keeps failing must not vanish: the background task
// surfaces the failure as a non-empty advisory retrievable via AwaitSync (so a
// caller can tell the session won't auto-suspend), rather than silently dropping
// it.
func TestBackgroundSyncSurfacesReaperFailure(t *testing.T) {
	ctx := context.Background()
	c, core := startupTestClient(t)
	core.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated reaper create failure")
	})
	s := c.Open("sess-reaper-fail")

	s.startBackgroundSync(nil, syncpkg.Spec{}, false, false, "", "", time.Minute)

	warn, err := s.AwaitSync(ctx)
	if err != nil {
		t.Fatalf("AwaitSync: %v", err)
	}
	if warn == "" {
		t.Error("a persistently failing reaper ensure must surface an advisory, not vanish")
	}
}

// ensureReaperWithRetry recovers from a transient failure: it retries and
// eventually creates the reaper Job, returning no warning.
func TestEnsureReaperWithRetryRecovers(t *testing.T) {
	ctx := context.Background()
	c, core := startupTestClient(t)
	var attempts int
	core.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		attempts++
		if attempts == 1 {
			return true, nil, fmt.Errorf("transient reaper create failure")
		}
		return false, nil, nil // fall through to the real (fake) create
	})

	if warn := c.ensureReaperWithRetry(ctx, Ref{ID: "sess-retry"}, "", "", time.Minute); warn != "" {
		t.Fatalf("ensureReaperWithRetry should recover after a transient failure, got warning %q", warn)
	}
	if attempts < 2 {
		t.Errorf("expected a retry after the first failure, saw %d attempts", attempts)
	}
	if _, err := core.BatchV1().Jobs(k8s.ReaperNamespace).Get(ctx, "reap-sess-retry", metav1.GetOptions{}); err != nil {
		t.Fatalf("reaper Job not created after retry: %v", err)
	}
}

// AwaitSync returns immediately with no warning when there is no background work
// in flight (before the first Connect, or an Observer connect).
func TestAwaitSyncNoTask(t *testing.T) {
	c, _ := startupTestClient(t)
	s := c.Open("sess-none")
	warn, err := s.AwaitSync(context.Background())
	if err != nil || warn != "" {
		t.Errorf("AwaitSync with no task: got (%q, %v), want (\"\", nil)", warn, err)
	}
}

// AwaitSync honors caller cancellation while the background task is still in
// flight, rather than blocking forever.
func TestAwaitSyncRespectsContextCancel(t *testing.T) {
	c, _ := startupTestClient(t)
	s := c.Open("sess-cancel")
	// A never-finishing task stands in for slow background work.
	s.mu.Lock()
	s.syncTask = &syncTask{done: make(chan struct{})}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.AwaitSync(ctx); err == nil {
		t.Error("AwaitSync should return the context error when cancelled before the task finishes")
	}
}

// closeHandles cancels the background context and clears the task so a reconnect
// starts fresh; a task captured before Close still observes completion.
func TestCloseCancelsBackgroundSync(t *testing.T) {
	c, _ := startupTestClient(t)
	s := c.Open("sess-close")
	s.startBackgroundSync(nil, syncpkg.Spec{}, false, false, "", "", time.Minute)

	// The captured task must still complete even after Close (the goroutine
	// unblocks via the cancelled context).
	s.mu.Lock()
	task := s.syncTask
	s.mu.Unlock()

	_ = s.Close()

	select {
	case <-task.done:
	case <-time.After(5 * time.Second):
		t.Fatal("background task did not settle after Close")
	}
	// A fresh AwaitSync after Close sees no task.
	s.mu.Lock()
	cleared := s.syncTask == nil
	s.mu.Unlock()
	if !cleared {
		t.Error("closeHandles should clear the syncTask so a reconnect starts fresh")
	}
}

package k8s

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestCreateSessionPinsNamespace verifies C5: a session's resources are always
// created in the backend namespace even if spec.Namespace asks for another, so
// the b.namespace-scoped Destroy/Status/etc. can manage them (no orphans).
func TestCreateSessionPinsNamespace(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	spec := session.Spec{
		ID:          "test-ns",
		ProjectPath: "/tmp",
		Backend:     "claude-sdk",
		RunnerImage: "test:latest",
		Namespace:   "some-other-namespace",
	}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Resources must be in the backend namespace.
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "test-ns", metav1.GetOptions{}); err != nil {
		t.Errorf("Sandbox not in backend namespace: %v", err)
	}
	if _, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("test-ns"), metav1.GetOptions{}); err != nil {
		t.Errorf("Secret not in backend namespace: %v", err)
	}
	// And nothing must leak into the divergent spec.Namespace.
	if _, err := agents.AgentsV1alpha1().Sandboxes("some-other-namespace").Get(ctx, "test-ns", metav1.GetOptions{}); err == nil {
		t.Error("Sandbox leaked into spec.Namespace; it must be pinned to the backend namespace (C5)")
	}
}

// TestDestroyDoesNotOrphanSecretOnError verifies C5: if an earlier deletion
// fails (here the PVC), Destroy still deletes the per-session Secret (the runner
// bearer token) instead of orphaning it, and surfaces the error.
func TestDestroyDoesNotOrphanSecretOnError(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	spec := session.Spec{ID: "test-orphan", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Simulate a transient failure deleting the PVC.
	core.PrependReactor("delete", "persistentvolumeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated PVC delete failure")
	})

	if err := b.Destroy(ctx, session.Ref{ID: "test-orphan"}); err == nil {
		t.Fatal("Destroy should surface the PVC delete error")
	}

	// The per-session Secret must NOT be orphaned despite the PVC failure.
	if _, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("test-orphan"), metav1.GetOptions{}); err == nil {
		t.Error("per-session Secret (bearer token) was orphaned after a PVC delete error (C5 regression)")
	}
	// And the Sandbox should still have been deleted.
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "test-orphan", metav1.GetOptions{}); err == nil {
		t.Error("Sandbox was not deleted")
	}
}

// TestCreateSessionRollsBackSecretOnPVCFailure verifies the HIGH fix: if the
// PVC create fails after the per-session Secret (holding the runner bearer
// token) was already created, CreateSession must delete that Secret rather
// than leaving it orphaned — List/Status/Destroy only ever enumerate
// Sandboxes, so an orphaned Secret would otherwise be permanently invisible.
func TestCreateSessionRollsBackSecretOnPVCFailure(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	core.PrependReactor("create", "persistentvolumeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated PVC create failure")
	})

	spec := session.Spec{ID: "test-rollback-pvc", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
	if _, err := b.CreateSession(ctx, spec); err == nil {
		t.Fatal("CreateSession should surface the PVC create error")
	}

	if _, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("test-rollback-pvc"), metav1.GetOptions{}); err == nil {
		t.Error("Secret was orphaned after a PVC create failure (rollback did not run)")
	}
	if _, err := core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "test-rollback-pvc", metav1.GetOptions{}); err == nil {
		t.Error("PVC should not exist after a failed create")
	}
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "test-rollback-pvc", metav1.GetOptions{}); err == nil {
		t.Error("Sandbox should never have been created")
	}
}

// TestCreateSessionRollsBackSecretAndPVCOnSandboxFailure verifies the HIGH
// fix: if the Sandbox create fails after the Secret and PVC already
// succeeded, both must be rolled back — otherwise the orphaned Secret (live
// bearer token) and up-to-50Gi PVC are permanently invisible to
// status/destroy, which only look at Sandboxes.
func TestCreateSessionRollsBackSecretAndPVCOnSandboxFailure(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	agents.PrependReactor("create", "sandboxes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated Sandbox create failure")
	})

	spec := session.Spec{ID: "test-rollback-sandbox", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
	if _, err := b.CreateSession(ctx, spec); err == nil {
		t.Fatal("CreateSession should surface the Sandbox create error")
	}

	if _, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("test-rollback-sandbox"), metav1.GetOptions{}); err == nil {
		t.Error("Secret was orphaned after a Sandbox create failure (rollback did not run)")
	}
	if _, err := core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "test-rollback-sandbox", metav1.GetOptions{}); err == nil {
		t.Error("PVC was orphaned after a Sandbox create failure (rollback did not run)")
	}
}

// TestCreateSessionPreexistingPVCSurvivesRollback verifies C7: when the PVC
// pre-existed (a prior session's workspace) but the Secret was freshly created,
// a later Sandbox-create failure must NOT roll back — the old guard keyed to
// the Secret alone would have deleted the pre-existing PVC (workspace data) as
// collateral. The freshly created Secret may be orphaned; that is the cheaper
// failure.
func TestCreateSessionPreexistingPVCSurvivesRollback(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	// Seed a pre-existing PVC (prior session's workspace); no Secret.
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "test-c7", Namespace: "agent-sessions"}}
	if _, err := core.CoreV1().PersistentVolumeClaims("agent-sessions").Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed pvc: %v", err)
	}
	agents.PrependReactor("create", "sandboxes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated Sandbox create failure")
	})

	spec := session.Spec{ID: "test-c7", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
	if _, err := b.CreateSession(ctx, spec); err == nil {
		t.Fatal("CreateSession should surface the Sandbox create error")
	}
	if _, err := core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "test-c7", metav1.GetOptions{}); err != nil {
		t.Errorf("pre-existing PVC was deleted as rollback collateral (C7): %v", err)
	}
}

// TestCreateSessionRollbackErrorSurfacesBothFailures verifies that when the
// best-effort rollback itself fails, that failure is appended to (not
// swallowed by, not masking) the original create error, so callers/logs still
// see why CreateSession failed in the first place.
func TestCreateSessionRollbackErrorSurfacesBothFailures(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	agents.PrependReactor("create", "sandboxes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated Sandbox create failure")
	})
	core.PrependReactor("delete", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated Secret delete failure during rollback")
	})

	spec := session.Spec{ID: "test-rollback-error", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
	_, err := b.CreateSession(ctx, spec)
	if err == nil {
		t.Fatal("CreateSession should surface an error")
	}
	if !strings.Contains(err.Error(), "Sandbox create failure") {
		t.Errorf("original create error was masked: %v", err)
	}
	if !strings.Contains(err.Error(), "Secret delete failure during rollback") {
		t.Errorf("rollback failure was not surfaced: %v", err)
	}
}

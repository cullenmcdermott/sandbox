package k8s

import (
	"context"
	"fmt"
	"testing"

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

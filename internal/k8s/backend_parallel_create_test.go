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

// TestCreateSessionRollsBackPVCOnSecretFailure covers the §5 parallelization:
// the Secret and PVC are now created concurrently, so a Secret-create failure
// can race a PVC-create that already landed. The rollback defer must still clean
// up EVERYTHING created under this partial parallel failure — most importantly
// the up-to-50Gi PVC that List/Status/Destroy (which only enumerate Sandboxes)
// could never otherwise see. This is the mirror of the serial-era
// TestCreateSessionRollsBackSecretOnPVCFailure in backend_c5_test.go.
func TestCreateSessionRollsBackPVCOnSecretFailure(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	// The Secret create fails; the PVC create (the fake clientset applies it
	// regardless of the errgroup's context cancellation) may already have landed.
	core.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated Secret create failure")
	})

	spec := session.Spec{ID: "test-rollback-secret", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
	if _, err := b.CreateSession(ctx, spec); err == nil {
		t.Fatal("CreateSession should surface the Secret create error")
	}

	// Nothing may survive the failure: not the Secret, not a raced-through PVC,
	// not a Sandbox (which is never even attempted before both parallel creates
	// succeed).
	if _, err := core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "test-rollback-secret", metav1.GetOptions{}); err == nil {
		t.Error("PVC was orphaned after a Secret create failure (parallel-create rollback did not clean it up)")
	}
	if _, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("test-rollback-secret"), metav1.GetOptions{}); err == nil {
		t.Error("Secret should not exist after a failed create")
	}
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "test-rollback-secret", metav1.GetOptions{}); err == nil {
		t.Error("Sandbox should never have been created")
	}
}

// TestCreateSessionParallelCreateHappyPath is a guard that the concurrent
// Secret+PVC create followed by the serial Sandbox create still produces all
// three resources (the parallelization must not drop one on the success path).
func TestCreateSessionParallelCreateHappyPath(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	spec := session.Spec{ID: "test-parallel-ok", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("test-parallel-ok"), metav1.GetOptions{}); err != nil {
		t.Errorf("Secret missing after parallel create: %v", err)
	}
	if _, err := core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "test-parallel-ok", metav1.GetOptions{}); err != nil {
		t.Errorf("PVC missing after parallel create: %v", err)
	}
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "test-parallel-ok", metav1.GetOptions{}); err != nil {
		t.Errorf("Sandbox missing after parallel create: %v", err)
	}
}

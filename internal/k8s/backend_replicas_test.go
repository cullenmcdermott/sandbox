package k8s

import (
	"context"
	"errors"
	"testing"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func seedSandbox(t *testing.T, agents *agentsfake.Clientset, name string) {
	t.Helper()
	one := int32(1)
	sb := &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agent-sessions"},
		Spec:       agentv1alpha1.SandboxSpec{Replicas: &one},
	}
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Create(context.Background(), sb, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}
}

// Suspend goes through setReplicas, which must retry on a resourceVersion
// conflict (the reaper writes the same Sandbox) rather than failing.
func TestSetReplicasRetriesOnConflict(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")
	seedSandbox(t, agents, "sess-conflict")

	// First Update returns a Conflict; the retry (re-Get + re-Update) must succeed.
	var updates int
	gr := schema.GroupResource{Group: "agents.x-k8s.io", Resource: "sandboxes"}
	agents.PrependReactor("update", "sandboxes", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 1 {
			return true, nil, k8serrors.NewConflict(gr, "sess-conflict", nil)
		}
		return false, nil, nil // let the tracker apply subsequent updates
	})

	if err := b.Suspend(ctx, session.Ref{ID: "sess-conflict"}); err != nil {
		t.Fatalf("Suspend should retry past a conflict, got: %v", err)
	}
	if updates < 2 {
		t.Fatalf("expected at least 2 update attempts (1 conflict + 1 success), got %d", updates)
	}

	sb, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "sess-conflict", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Spec.Replicas == nil || *sb.Spec.Replicas != 0 {
		t.Fatalf("replicas not set to 0 after Suspend: %v", sb.Spec.Replicas)
	}
}

// A non-conflict error must not be retried and must surface.
func TestSetReplicasDoesNotRetryNonConflict(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")
	seedSandbox(t, agents, "sess-err")

	var updates int
	agents.PrependReactor("update", "sandboxes", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		return true, nil, k8serrors.NewInternalError(errors.New("boom"))
	})

	if err := b.Suspend(ctx, session.Ref{ID: "sess-err"}); err == nil {
		t.Fatal("expected Suspend to fail on a non-conflict error")
	}
	if updates != 1 {
		t.Fatalf("expected exactly 1 update attempt for a non-conflict error, got %d", updates)
	}
}

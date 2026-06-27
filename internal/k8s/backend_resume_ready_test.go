package k8s

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// mkReadyPod builds a Running+Ready pod labelled for the given sandbox, optionally
// terminating (DeletionTimestamp set).
func mkReadyPod(name, sandbox string, terminating bool) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "agent-sessions",
			Labels:    map[string]string{labelSessionID: sandbox},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	if terminating {
		ts := metav1.NewTime(time.Now())
		p.DeletionTimestamp = &ts
		p.Finalizers = []string{"sandbox.cullen.dev/test"} // keep the fake tracker from GCing it
	}
	return p
}

func seedSandboxFor(t *testing.T, agents *agentsfake.Clientset, name string) {
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

// On resume (replicas 0→1) the OLD pod lingers Running+Ready with a
// DeletionTimestamp while it terminates; it is momentarily the only pod, so
// getPodForSandbox falls back to it (R7). waitForPodReady must NOT report ready
// against that dying pod — otherwise resume returns ~10-15s before the genuinely-
// new pod is up and a turn started in that window is orphaned. It must keep
// polling until a non-terminating pod is Ready.
func TestWaitForPodReadyIgnoresTerminatingPod(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset(mkReadyPod("sess-resume-old", "sess-resume", true))
	seedSandboxFor(t, agents, "sess-resume")
	b := NewForClients(agents, core, "agent-sessions")

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := b.waitForPodReady(ctx, session.Ref{ID: "sess-resume"})
	if err == nil {
		t.Fatal("waitForPodReady reported ready against a terminating pod; it must keep waiting")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the wait to still be polling (context deadline), got: %v", err)
	}
}

// Once a genuinely-new, non-terminating Ready pod exists (alongside the still-
// terminating old one), waitForPodReady returns nil — it selects the new pod.
func TestWaitForPodReadyAcceptsNewNonTerminatingPod(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset(
		mkReadyPod("sess-resume2-old", "sess-resume2", true),  // terminating, must be ignored
		mkReadyPod("sess-resume2-new", "sess-resume2", false), // the real new pod
	)
	seedSandboxFor(t, agents, "sess-resume2")
	b := NewForClients(agents, core, "agent-sessions")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.waitForPodReady(ctx, session.Ref{ID: "sess-resume2"}); err != nil {
		t.Fatalf("waitForPodReady should accept the new non-terminating ready pod: %v", err)
	}
}

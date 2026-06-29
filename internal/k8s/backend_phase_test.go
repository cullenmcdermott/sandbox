package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Phase 2 (cold-start splash): podPhaseDetail classifies where a pod is in its
// startup lifecycle so the connect splash can show "scheduling" → "pulling image"
// → "starting" instead of a frozen terminal. Empty once Ready (the caller then
// advances past the start stage).
func TestPodPhaseDetail(t *testing.T) {
	pending := func(scheduled, running, ready bool) *corev1.Pod {
		phase := corev1.PodPending
		if running {
			phase = corev1.PodRunning
		}
		p := &corev1.Pod{Status: corev1.PodStatus{Phase: phase}}
		if scheduled {
			p.Status.Conditions = append(p.Status.Conditions, corev1.PodCondition{
				Type: corev1.PodScheduled, Status: corev1.ConditionTrue,
			})
		}
		if ready {
			p.Status.Conditions = append(p.Status.Conditions, corev1.PodCondition{
				Type: corev1.PodReady, Status: corev1.ConditionTrue,
			})
		}
		return p
	}

	tests := []struct {
		name string
		pod  *corev1.Pod
		want string
	}{
		{"no pod yet -> scheduling", nil, "scheduling"},
		{"pending, unscheduled -> scheduling", pending(false, false, false), "scheduling"},
		{"pending, scheduled -> pulling image", pending(true, false, false), "pulling image"},
		{
			"pending, scheduled via NodeName fallback (no condition) -> pulling image",
			&corev1.Pod{Spec: corev1.PodSpec{NodeName: "node-1"}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
			"pulling image",
		},
		{"running, not ready -> starting", pending(true, true, false), "starting"},
		{"running, ready -> empty (done)", pending(true, true, true), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := podPhaseDetail(tc.pod); got != tc.want {
				t.Errorf("podPhaseDetail = %q, want %q", got, tc.want)
			}
		})
	}
}

// StartWithProgress threads the pod phase out of the readiness wait so the
// connect splash animates. With a scheduled-but-never-ready pod the wait blocks
// until its context deadline, but the onPhase callback must still fire with the
// classified phase ("pulling image") so the splash shows progress rather than a
// frozen label. The callback also dedupes — the same phase is reported once.
func TestStartWithProgressReportsPhase(t *testing.T) {
	const sandbox = "sess-phase"
	agents := agentsfake.NewSimpleClientset()
	seedSandboxFor(t, agents, sandbox)
	// A scheduled-but-not-ready pod: kubelet is creating containers (pulling the
	// image). It never goes Ready, so the wait runs out its deadline.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandbox + "-pod",
			Namespace: "agent-sessions",
			Labels:    map[string]string{labelSessionID: sandbox},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
		Status: corev1.PodStatus{
			Phase:      corev1.PodPending,
			Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}},
		},
	}
	core := fake.NewSimpleClientset(pod)
	b := NewForClients(agents, core, "agent-sessions")

	var phases []string
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_ = b.StartWithProgress(ctx, session.Ref{ID: sandbox}, func(detail string) {
		phases = append(phases, detail)
	})

	if len(phases) == 0 {
		t.Fatal("onPhase never fired; the splash would show no progress")
	}
	for _, p := range phases {
		if p != "pulling image" {
			t.Errorf("unexpected phase %q (want only \"pulling image\" for a scheduled-pending pod)", p)
		}
	}
	if len(phases) != 1 {
		t.Errorf("expected the phase reported exactly once (deduped), got %d: %v", len(phases), phases)
	}
}

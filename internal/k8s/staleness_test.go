package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1d: both status-derivation paths trusted PodRunning + Ready conditions with
// no staleness cross-check. When a node dies the kubelet stops heart-beating and
// k8s takes minutes to mark the pod NotReady/Failed, so a crashed session read
// Running with a silently-stalled SSE stream. podStale is the pod-side gate; the
// following table pins each staleness signal the pod object exposes.
func TestPodStale(t *testing.T) {
	now := time.Now()
	fresh := metav1.NewTime(now.Add(-5 * time.Second))
	longAgo := metav1.NewTime(now.Add(-10 * time.Minute))

	terminating := func() *corev1.Pod {
		ts := metav1.NewTime(now.Add(-time.Minute))
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &ts},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		}
	}

	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "genuinely running+ready is not stale",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: longAgo}},
			}},
			want: false,
		},
		{
			name: "terminating (deletionTimestamp) is stale even while Ready=True",
			pod:  terminating(),
			want: true,
		},
		{
			name: "NodeLost pod status reason is stale even while phase Running",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Reason:     "NodeLost",
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: fresh}},
			}},
			want: true,
		},
		{
			name: "NodeLost on the Ready condition is stale",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse, Reason: "NodeLost", LastTransitionTime: fresh}},
			}},
			want: true,
		},
		{
			name: "Ready went Unknown long ago is stale",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionUnknown, LastTransitionTime: longAgo}},
			}},
			want: true,
		},
		{
			name: "Ready recently False (slow start) is not yet stale",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: fresh}},
			}},
			want: false,
		},
		{
			name: "still Pending (image pull) is not stale, even past threshold",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase:      corev1.PodPending,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: longAgo}},
			}},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := podStale(tc.pod, now); got != tc.want {
				t.Errorf("podStale = %v, want %v", got, tc.want)
			}
		})
	}
}

// §1d (watch path): sandboxToState holds only the Sandbox — no pod Get by design
// — so its staleness cross-check works off the Sandbox's own deletionTimestamp
// and Ready condition. sandboxStale pins those signals.
func TestSandboxStale(t *testing.T) {
	now := time.Now()
	longAgo := metav1.NewTime(now.Add(-10 * time.Minute))
	fresh := metav1.NewTime(now.Add(-5 * time.Second))

	sbWith := func(status metav1.ConditionStatus, lt metav1.Time, deleting bool) *agentv1alpha1.Sandbox {
		sb := &agentv1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "s"}}
		sb.Status.Conditions = []metav1.Condition{{
			Type:               string(agentv1alpha1.SandboxConditionReady),
			Status:             status,
			LastTransitionTime: lt,
		}}
		if deleting {
			ts := metav1.NewTime(now.Add(-time.Minute))
			sb.DeletionTimestamp = &ts
		}
		return sb
	}

	tests := []struct {
		name string
		sb   *agentv1alpha1.Sandbox
		want bool
	}{
		{"ready long ago is not stale", sbWith(metav1.ConditionTrue, longAgo, false), false},
		{"terminating is stale even while Ready=True", sbWith(metav1.ConditionTrue, longAgo, true), true},
		{"Ready Unknown long ago is stale", sbWith(metav1.ConditionUnknown, longAgo, false), true},
		{"Ready recently False is not yet stale", sbWith(metav1.ConditionFalse, fresh, false), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxStale(tc.sb, now); got != tc.want {
				t.Errorf("sandboxStale = %v, want %v", got, tc.want)
			}
		})
	}
}

// End-to-end through the Status (Get) path: a genuinely running pod maps to
// RUNNING, but a dead-node pod (NodeLost) that still reads Running/Ready must NOT
// be reported as a healthy RUNNING session — it degrades to UNKNOWN so the caller
// doesn't drive a stalled SSE stream.
func TestStatusCrossChecksPodStaleness(t *testing.T) {
	nodeLostPod := func(name, sandbox string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "agent-sessions",
				Labels:    map[string]string{labelSessionID: sandbox},
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Reason:     "NodeLost",
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		}
	}

	tests := []struct {
		name string
		pod  *corev1.Pod
		want session.Status
	}{
		{"healthy running pod", mkReadyPod("sess-ok-pod", "sess-ok", false), session.StatusRunning},
		{"dead-node pod reads Running but is UNKNOWN", nodeLostPod("sess-dead-pod", "sess-dead"), session.StatusUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := string(tc.pod.Labels[labelSessionID])
			agents := agentsfake.NewSimpleClientset()
			seedSandboxFor(t, agents, id)
			core := fake.NewSimpleClientset(tc.pod)
			b := NewForClients(agents, core, "agent-sessions")

			st, err := b.Status(context.Background(), session.Ref{ID: session.ID(id)})
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if st.Status != tc.want {
				t.Errorf("Status = %v, want %v", st.Status, tc.want)
			}
			if tc.want != session.StatusRunning && st.PodReady {
				t.Errorf("stale pod must not report PodReady=true")
			}
		})
	}
}

// End-to-end through the watch path: sandboxToState degrades a suspect Sandbox to
// UNKNOWN instead of a confident RUNNING/CREATING.
func TestSandboxToStateCrossChecksStaleness(t *testing.T) {
	now := time.Now()
	longAgo := metav1.NewTime(now.Add(-10 * time.Minute))
	one := int32(1)

	ready := &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "s-ready"},
		Spec:       agentv1alpha1.SandboxSpec{Replicas: &one},
	}
	ready.Status.Conditions = []metav1.Condition{{
		Type: string(agentv1alpha1.SandboxConditionReady), Status: metav1.ConditionTrue, LastTransitionTime: longAgo,
	}}

	staleReady := &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "s-stale"},
		Spec:       agentv1alpha1.SandboxSpec{Replicas: &one},
	}
	staleReady.Status.Conditions = []metav1.Condition{{
		Type: string(agentv1alpha1.SandboxConditionReady), Status: metav1.ConditionUnknown, LastTransitionTime: longAgo,
	}}

	terminatingReady := &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "s-term"},
		Spec:       agentv1alpha1.SandboxSpec{Replicas: &one},
	}
	ts := metav1.NewTime(now.Add(-time.Minute))
	terminatingReady.DeletionTimestamp = &ts
	terminatingReady.Status.Conditions = []metav1.Condition{{
		Type: string(agentv1alpha1.SandboxConditionReady), Status: metav1.ConditionTrue, LastTransitionTime: longAgo,
	}}

	tests := []struct {
		name string
		sb   *agentv1alpha1.Sandbox
		want session.Status
	}{
		{"ready sandbox is RUNNING", ready, session.StatusRunning},
		{"Ready Unknown long ago is UNKNOWN not CREATING", staleReady, session.StatusUnknown},
		{"terminating Ready=True is UNKNOWN not RUNNING", terminatingReady, session.StatusUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxToState(tc.sb).Status; got != tc.want {
				t.Errorf("sandboxToState = %v, want %v", got, tc.want)
			}
		})
	}
}

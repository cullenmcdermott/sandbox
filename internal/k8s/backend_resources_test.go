package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestBuildPodResourcesBestEffort: the zero-value Resources yields an empty
// ResourceRequirements — no Requests, no Limits — so the pod runs BestEffort.
// This is the default that lets a small single-node cluster pack several sessions
// without a per-pod CPU request capping the node.
func TestBuildPodResourcesBestEffort(t *testing.T) {
	got := buildPodResources(session.Resources{})
	if got.Requests != nil {
		t.Errorf("Requests = %v, want nil (BestEffort)", got.Requests)
	}
	if got.Limits != nil {
		t.Errorf("Limits = %v, want nil (BestEffort)", got.Limits)
	}
}

// TestBuildPodResourcesFull: every set field maps onto the matching request/limit
// with the parsed quantity.
func TestBuildPodResourcesFull(t *testing.T) {
	got := buildPodResources(session.Resources{
		CPURequest: "500m", MemoryRequest: "512Mi",
		CPULimit: "2", MemoryLimit: "4Gi",
	})
	for _, tc := range []struct {
		what string
		list corev1.ResourceList
		key  corev1.ResourceName
		want string
	}{
		{"cpu request", got.Requests, corev1.ResourceCPU, "500m"},
		{"memory request", got.Requests, corev1.ResourceMemory, "512Mi"},
		{"cpu limit", got.Limits, corev1.ResourceCPU, "2"},
		{"memory limit", got.Limits, corev1.ResourceMemory, "4Gi"},
	} {
		q, ok := tc.list[tc.key]
		if !ok {
			t.Errorf("%s missing", tc.what)
			continue
		}
		if q.Cmp(resource.MustParse(tc.want)) != 0 {
			t.Errorf("%s = %s, want %s", tc.what, q.String(), tc.want)
		}
	}
}

// TestBuildPodResourcesPartial: setting only requests leaves Limits unset (and
// vice versa) — a half-specified Resources never fabricates the other half.
func TestBuildPodResourcesPartial(t *testing.T) {
	got := buildPodResources(session.Resources{CPURequest: "250m"})
	if got.Limits != nil {
		t.Errorf("Limits = %v, want nil when only a request is set", got.Limits)
	}
	if q, ok := got.Requests[corev1.ResourceCPU]; !ok || q.Cmp(resource.MustParse("250m")) != 0 {
		t.Errorf("cpu request = %v (ok=%v), want 250m", got.Requests, ok)
	}
	if _, ok := got.Requests[corev1.ResourceMemory]; ok {
		t.Error("memory request must be absent when unset")
	}
}

// TestBuildSandboxDefaultBestEffort: a Spec with no Resources produces a runner
// container with empty ResourceRequirements — proving the old hardcoded
// requests/limits are gone.
func TestBuildSandboxDefaultBestEffort(t *testing.T) {
	sb := buildSandbox(session.Spec{ID: "claude-sdk-be", Backend: "claude-sdk", RunnerImage: "test:latest"})
	res := sb.Spec.PodTemplate.Spec.Containers[0].Resources
	if res.Requests != nil || res.Limits != nil {
		t.Fatalf("default pod must be BestEffort, got requests=%v limits=%v", res.Requests, res.Limits)
	}
}

// TestBuildSandboxResourcesThreaded: a Spec with Resources threads them onto the
// runner container.
func TestBuildSandboxResourcesThreaded(t *testing.T) {
	sb := buildSandbox(session.Spec{
		ID: "claude-sdk-sz", Backend: "claude-sdk", RunnerImage: "test:latest",
		Resources: session.Resources{CPURequest: "1", MemoryRequest: "1Gi", CPULimit: "4", MemoryLimit: "8Gi"},
	})
	res := sb.Spec.PodTemplate.Spec.Containers[0].Resources
	if q := res.Requests[corev1.ResourceCPU]; q.Cmp(resource.MustParse("1")) != 0 {
		t.Errorf("cpu request = %s, want 1", q.String())
	}
	if q := res.Limits[corev1.ResourceMemory]; q.Cmp(resource.MustParse("8Gi")) != 0 {
		t.Errorf("memory limit = %s, want 8Gi", q.String())
	}
}

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

// Digest pinning across suspend/resume (2026-07-01 review HIGH): a moving tag
// (:latest) + PullAlways means resume — which only flips replicas — would pull
// whatever the tag points at NOW and run it against the session's old
// events.db/session.json. The backend therefore records the kubelet-resolved
// digest of the image the pod actually ran (annotation, stamped at first
// ready) and Resume rewrites the pod template from it before scaling up.

const (
	testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testImage  = "ghcr.io/example/sandbox-runner:latest"
	testPinned = "ghcr.io/example/sandbox-runner@" + testDigest
)

// seedSandboxWithRunner creates a Sandbox whose pod template carries a runner
// container with the given image (mirroring what buildSandbox produces).
func seedSandboxWithRunner(t *testing.T, agents *agentsfake.Clientset, name, image string, annotations map[string]string) {
	t.Helper()
	one := int32(1)
	sb := &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agent-sessions", Annotations: annotations},
		Spec: agentv1alpha1.SandboxSpec{
			Replicas: &one,
			PodTemplate: agentv1alpha1.PodTemplate{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            runnerContainerName,
						Image:           image,
						ImagePullPolicy: resolveImagePullPolicy("", image),
					}},
				},
			},
		},
	}
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Create(context.Background(), sb, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}
}

// mkRunningPod builds a Running+Ready pod for the sandbox whose runner
// container reports the given kubelet-resolved imageID.
func mkRunningPod(name, sandbox, imageID string) *corev1.Pod {
	p := mkReadyPod(name, sandbox, false)
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:    runnerContainerName,
		ImageID: imageID,
		Ready:   true,
	}}
	return p
}

func getSandbox(t *testing.T, agents *agentsfake.Clientset, name string) *agentv1alpha1.Sandbox {
	t.Helper()
	sb, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	return sb
}

func TestStartPinsRunnerImageDigest(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset(mkRunningPod("sess-pin-pod", "sess-pin", testPinned))
	seedSandboxWithRunner(t, agents, "sess-pin", testImage, nil)
	b := NewForClients(agents, core, "agent-sessions")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.Start(ctx, session.Ref{ID: "sess-pin"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	sb := getSandbox(t, agents, "sess-pin")
	if got := sb.Annotations[annotationPinnedRunnerImage]; got != testPinned {
		t.Fatalf("pinned-image annotation = %q, want %q", got, testPinned)
	}
	// The template itself is untouched at start; only resume rewrites it.
	if got := sb.Spec.PodTemplate.Spec.Containers[0].Image; got != testImage {
		t.Fatalf("template image changed at start: %q", got)
	}
}

// docker-runtime imageIDs carry a docker-pullable:// prefix; the digest must
// still be extracted and joined with the SPEC image's repo.
func TestStartPinsThroughDockerPullablePrefix(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset(mkRunningPod("sess-dp-pod", "sess-dp", "docker-pullable://mirror.local/other-name@"+testDigest))
	seedSandboxWithRunner(t, agents, "sess-dp", testImage, nil)
	b := NewForClients(agents, core, "agent-sessions")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.Start(ctx, session.Ref{ID: "sess-dp"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := getSandbox(t, agents, "sess-dp").Annotations[annotationPinnedRunnerImage]; got != testPinned {
		t.Fatalf("pinned-image annotation = %q, want %q", got, testPinned)
	}
}

// A locally-loaded dev image can report an imageID with no registry digest;
// there is nothing safe to pin, and Start must not fail.
func TestStartSkipsPinWithoutDigest(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset(mkRunningPod("sess-nd-pod", "sess-nd", "sha256-only-local-id"))
	seedSandboxWithRunner(t, agents, "sess-nd", testImage, nil)
	b := NewForClients(agents, core, "agent-sessions")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.Start(ctx, session.Ref{ID: "sess-nd"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := getSandbox(t, agents, "sess-nd").Annotations[annotationPinnedRunnerImage]; got != "" {
		t.Fatalf("expected no pin annotation, got %q", got)
	}
}

func TestResumeRewritesImageFromPin(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset(mkRunningPod("sess-res-pod", "sess-res", testPinned))
	seedSandboxWithRunner(t, agents, "sess-res", testImage,
		map[string]string{annotationPinnedRunnerImage: testPinned})
	b := NewForClients(agents, core, "agent-sessions")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.Resume(ctx, session.Ref{ID: "sess-res"}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	sb := getSandbox(t, agents, "sess-res")
	c := sb.Spec.PodTemplate.Spec.Containers[0]
	if c.Image != testPinned {
		t.Fatalf("resumed template image = %q, want pinned %q", c.Image, testPinned)
	}
	// The auto-resolved PullAlways (moving tag) must relax to IfNotPresent for
	// the immutable digest ref, so resume survives an unreachable registry.
	if c.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("resumed pull policy = %q, want IfNotPresent", c.ImagePullPolicy)
	}
	if sb.Spec.Replicas == nil || *sb.Spec.Replicas != 1 {
		t.Fatalf("replicas not restored to 1")
	}
}

// Without a pin annotation (pod never became ready before suspend, or a local
// dev image), Resume behaves exactly as before: replicas only.
func TestResumeWithoutPinLeavesImage(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset(mkRunningPod("sess-np-pod", "sess-np", testPinned))
	seedSandboxWithRunner(t, agents, "sess-np", testImage, nil)
	b := NewForClients(agents, core, "agent-sessions")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.Resume(ctx, session.Ref{ID: "sess-np"}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	c := getSandbox(t, agents, "sess-np").Spec.PodTemplate.Spec.Containers[0]
	if c.Image != testImage {
		t.Fatalf("image rewritten without a pin: %q", c.Image)
	}
	if c.ImagePullPolicy != corev1.PullAlways {
		t.Fatalf("pull policy changed without a pin: %q", c.ImagePullPolicy)
	}
}

func TestImageRepo(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/x/runner:latest":           "ghcr.io/x/runner",
		"ghcr.io/x/runner@" + testDigest:    "ghcr.io/x/runner",
		"registry.local:5000/runner:v1":     "registry.local:5000/runner",
		"registry.local:5000/runner":        "registry.local:5000/runner",
		"runner":                            "runner",
		"runner:latest":                     "runner",
		"ghcr.io/x/runner:v1@" + testDigest: "ghcr.io/x/runner",
	}
	for in, want := range cases {
		if got := imageRepo(in); got != want {
			t.Errorf("imageRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImageIDDigest(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/x/runner@" + testDigest:                   testDigest,
		"docker-pullable://ghcr.io/x/runner@" + testDigest: testDigest,
		"sha256-local-only":                                "",
		"":                                                 "",
		"ghcr.io/x/runner@md5:nope":                        "",
	}
	for in, want := range cases {
		if got := imageIDDigest(in); got != want {
			t.Errorf("imageIDDigest(%q) = %q, want %q", in, got, want)
		}
	}
}

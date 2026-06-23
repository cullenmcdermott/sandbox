package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func newTestBackend(t *testing.T) *Backend {
	t.Helper()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	return NewForClients(agents, core, "agent-sessions")
}

func TestCreateSession(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	spec := session.Spec{
		ID:          "claude-sdk-test",
		ProjectPath: "/Users/cullen/git/homelab",
		Backend:     "claude-sdk",
		RunnerImage: "test:latest",
	}

	ref, err := b.CreateSession(ctx, spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ref.ID != "claude-sdk-test" {
		t.Errorf("id: got %q, want claude-sdk-test", ref.ID)
	}

	// Verify Sandbox was created
	sb, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "claude-sdk-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Spec.PodTemplate.Spec.Containers[0].Image != "test:latest" {
		t.Errorf("image: got %q, want test:latest", sb.Spec.PodTemplate.Spec.Containers[0].Image)
	}
	if sb.Spec.PodTemplate.Spec.AutomountServiceAccountToken == nil || *sb.Spec.PodTemplate.Spec.AutomountServiceAccountToken != false {
		t.Error("automountServiceAccountToken should be false")
	}
	if sb.Spec.PodTemplate.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy: got %q, want Never", sb.Spec.PodTemplate.Spec.RestartPolicy)
	}

	// Verify PVC was created
	pvc, err := b.core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "claude-sdk-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}
	// With no StorageClass in the Spec, the PVC must leave StorageClassName nil
	// so it falls back to the cluster's default StorageClass (an explicit "" would
	// request NO class and never bind). An environment-specific class is opt-in
	// via Spec.StorageClass (see TestCreateSessionExplicitStorageClass).
	if pvc.Spec.StorageClassName != nil {
		t.Errorf("storageClass: got %q, want nil (cluster default)", *pvc.Spec.StorageClassName)
	}
}

// TestCreateSessionProbes guards C9: the runner container must carry both a
// readiness and a liveness probe hitting GET /healthz on the runner HTTP port,
// so a crashed/hung runner is detected (readiness gates traffic; liveness lets
// the controller recreate a wedged pod) rather than being marked Ready forever.
func TestCreateSessionProbes(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	spec := session.Spec{ID: "claude-sdk-probe", Backend: "claude-sdk", RunnerImage: "test:latest"}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}

	sb, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "claude-sdk-probe", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	c := sb.Spec.PodTemplate.Spec.Containers[0]

	for _, tc := range []struct {
		name  string
		probe *corev1.Probe
	}{
		{"readiness", c.ReadinessProbe},
		{"liveness", c.LivenessProbe},
	} {
		if tc.probe == nil {
			t.Fatalf("%s probe is nil, want GET /healthz probe", tc.name)
		}
		h := tc.probe.HTTPGet
		if h == nil {
			t.Fatalf("%s probe is not an HTTPGet probe", tc.name)
		}
		if h.Path != "/healthz" {
			t.Errorf("%s probe path = %q, want /healthz", tc.name, h.Path)
		}
		// Target the named "http" port so it tracks the runner ContainerPort.
		if h.Port.StrVal != "http" {
			t.Errorf("%s probe port = %q, want \"http\"", tc.name, h.Port.StrVal)
		}
	}
}

// TestCreateSessionExplicitStorageClass verifies an explicit Spec.StorageClass
// is passed through to the PVC unchanged (the override path now that the default
// is the cluster default rather than a hardcoded rook class).
func TestCreateSessionExplicitStorageClass(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	spec := session.Spec{
		ID:           "claude-sdk-sc",
		Backend:      "claude-sdk",
		RunnerImage:  "test:latest",
		StorageClass: "rook-ceph-block",
	}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}
	pvc, err := b.core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "claude-sdk-sc", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "rook-ceph-block" {
		t.Errorf("storageClass: got %v, want rook-ceph-block", pvc.Spec.StorageClassName)
	}
}

// TestCreateSessionSinglePVC guards S1: the Sandbox spec must NOT carry a
// VolumeClaimTemplates entry (which would make the controller auto-provision a
// second, never-mounted PVC — 2× storage). The single per-session PVC is the
// standalone one, mounted via the explicit "session" Volume.
func TestCreateSessionSinglePVC(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	spec := session.Spec{ID: "claude-sdk-pvc", Backend: "claude-sdk", RunnerImage: "test:latest"}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}

	sb, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "claude-sdk-pvc", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if n := len(sb.Spec.VolumeClaimTemplates); n != 0 {
		t.Fatalf("VolumeClaimTemplates = %d, want 0 (a template auto-provisions a 2nd, never-mounted PVC — S1)", n)
	}

	// Storage must still be wired: the pod mounts the standalone PVC by name.
	pod := sb.Spec.PodTemplate.Spec
	var claim string
	for _, v := range pod.Volumes {
		if v.Name == "session" && v.PersistentVolumeClaim != nil {
			claim = v.PersistentVolumeClaim.ClaimName
		}
	}
	if claim != "claude-sdk-pvc" {
		t.Fatalf("session volume ClaimName = %q, want claude-sdk-pvc (standalone PVC)", claim)
	}
	var mounted bool
	for _, m := range pod.Containers[0].VolumeMounts {
		if m.Name == "session" && m.MountPath == "/session" {
			mounted = true
		}
	}
	if !mounted {
		t.Fatal("session volume not mounted at /session")
	}

	// Exactly one PVC object is created by the backend.
	pvcs, err := b.core.CoreV1().PersistentVolumeClaims("agent-sessions").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pvc: %v", err)
	}
	if len(pvcs.Items) != 1 {
		t.Fatalf("backend created %d PVCs, want 1", len(pvcs.Items))
	}
}

func TestCreateSessionSecretAndEnv(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	spec := session.Spec{
		ID:           "claude-sdk-env",
		ProjectPath:  "/Users/cullen/git/homelab",
		Backend:      "claude-sdk",
		RunnerImage:  "test:latest",
		SSHPublicKey: "ssh-ed25519 AAAATESTKEY user@host",
	}
	ref, err := b.CreateSession(ctx, spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Per-session Secret exists with a non-empty runner token and the SSH key.
	secret, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-env"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if len(secret.Data[secretKeyRunnerToken]) == 0 {
		t.Error("runner token should be set in secret")
	}
	if string(secret.Data[secretKeySSHAuthorizedKey]) != spec.SSHPublicKey {
		t.Errorf("ssh key: got %q, want %q", secret.Data[secretKeySSHAuthorizedKey], spec.SSHPublicKey)
	}

	// RunnerToken returns the same token (round-trips via the API).
	token, err := b.RunnerToken(ctx, ref)
	if err != nil {
		t.Fatalf("runner token: %v", err)
	}
	if token != string(secret.Data[secretKeyRunnerToken]) {
		t.Errorf("RunnerToken mismatch: got %q, want %q", token, secret.Data[secretKeyRunnerToken])
	}

	// Pod env carries the identity + project vars and references both secrets.
	sb, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "claude-sdk-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	env := sb.Spec.PodTemplate.Spec.Containers[0].Env
	if got := envValue(env, "SANDBOX_SESSION_ID"); got != "claude-sdk-env" {
		t.Errorf("SANDBOX_SESSION_ID: got %q, want claude-sdk-env", got)
	}
	if got := envValue(env, "SANDBOX_BACKEND"); got != "claude-sdk" {
		t.Errorf("SANDBOX_BACKEND: got %q, want claude-sdk", got)
	}
	if got := envValue(env, "PROJECT_PATH"); got != "/Users/cullen/git/homelab" {
		t.Errorf("PROJECT_PATH: got %q, want /Users/cullen/git/homelab", got)
	}
	rt := envVar(env, "RUNNER_TOKEN")
	if rt == nil || rt.ValueFrom == nil || rt.ValueFrom.SecretKeyRef == nil ||
		rt.ValueFrom.SecretKeyRef.Name != sessionSecretName("claude-sdk-env") {
		t.Error("RUNNER_TOKEN should reference the per-session secret")
	}
	ak := envVar(env, "CLAUDE_CODE_OAUTH_TOKEN")
	if ak == nil || ak.ValueFrom == nil || ak.ValueFrom.SecretKeyRef == nil ||
		ak.ValueFrom.SecretKeyRef.Name != anthropicSecretName ||
		ak.ValueFrom.SecretKeyRef.Optional == nil || !*ak.ValueFrom.SecretKeyRef.Optional {
		t.Error("CLAUDE_CODE_OAUTH_TOKEN should optionally reference the anthropic secret")
	}

	// The SSH public key is mounted from the per-session secret.
	var sshVol *corev1.Volume
	for i := range sb.Spec.PodTemplate.Spec.Volumes {
		if sb.Spec.PodTemplate.Spec.Volumes[i].Name == "ssh-key" {
			sshVol = &sb.Spec.PodTemplate.Spec.Volumes[i]
		}
	}
	if sshVol == nil || sshVol.Secret == nil || sshVol.Secret.SecretName != sessionSecretName("claude-sdk-env") {
		t.Error("ssh-key volume should project the per-session secret")
	}
	var mounted bool
	for _, vm := range sb.Spec.PodTemplate.Spec.Containers[0].VolumeMounts {
		if vm.Name == "ssh-key" && vm.MountPath == sshAuthorizedKeyMountPath {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("ssh-key should be mounted at %s", sshAuthorizedKeyMountPath)
	}
}

func envVar(env []corev1.EnvVar, name string) *corev1.EnvVar {
	for i := range env {
		if env[i].Name == name {
			return &env[i]
		}
	}
	return nil
}

func envValue(env []corev1.EnvVar, name string) string {
	if e := envVar(env, name); e != nil {
		return e.Value
	}
	return ""
}

func TestCreateSessionIdempotent(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	spec := session.Spec{
		ID:          "test-idempotent",
		ProjectPath: "/tmp",
		Backend:     "claude-sdk",
		RunnerImage: "test:latest",
	}

	// First create
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Second create should not fail (already exists)
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("second create: %v", err)
	}
}

func TestDestroy(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	spec := session.Spec{
		ID:          "test-destroy",
		ProjectPath: "/tmp",
		Backend:     "claude-sdk",
		RunnerImage: "test:latest",
	}
	ref, err := b.CreateSession(ctx, spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := b.Destroy(ctx, ref); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	// Verify Sandbox is gone
	_, err = b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "test-destroy", metav1.GetOptions{})
	if err == nil {
		t.Fatal("sandbox should be deleted")
	}

	// Verify PVC is gone
	_, err = b.core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "test-destroy", metav1.GetOptions{})
	if err == nil {
		t.Fatal("pvc should be deleted")
	}

	// Verify per-session Secret is gone
	_, err = b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("test-destroy"), metav1.GetOptions{})
	if err == nil {
		t.Fatal("secret should be deleted")
	}
}

func TestDestroyMissing(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	// Should not error on missing
	if err := b.Destroy(ctx, session.Ref{ID: "nonexistent"}); err != nil {
		t.Fatalf("destroy missing: %v", err)
	}
}

func TestStatusGone(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	st, err := b.Status(ctx, session.Ref{ID: "nonexistent"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Status != session.StatusGone {
		t.Errorf("status: got %s, want GONE", st.Status)
	}
}

func TestStatusSuspended(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	// Create a sandbox manually in suspended state (replicas=0)
	zero := int32(0)
	sb := &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-suspended",
			Namespace: "agent-sessions",
		},
		Spec: agentv1alpha1.SandboxSpec{
			Replicas: &zero,
		},
	}
	if _, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Create(ctx, sb, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	st, err := b.Status(ctx, session.Ref{ID: "test-suspended"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Status != session.StatusSuspended {
		t.Errorf("status: got %s, want SUSPENDED", st.Status)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	for _, id := range []string{"list-a", "list-b"} {
		spec := session.Spec{
			ID:          session.ID(id),
			ProjectPath: "/tmp",
			Backend:     "claude-sdk",
			RunnerImage: "test:latest",
		}
		if _, err := b.CreateSession(ctx, spec); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	states, err := b.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(states) != 2 {
		t.Errorf("got %d states, want 2", len(states))
	}
}

func TestSuspendResume(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	one := int32(1)
	sb := &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-suspend",
			Namespace: "agent-sessions",
		},
		Spec: agentv1alpha1.SandboxSpec{
			Replicas: &one,
		},
	}
	if _, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Create(ctx, sb, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Suspend
	if err := b.Suspend(ctx, session.Ref{ID: "test-suspend"}); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	sb2, _ := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "test-suspend", metav1.GetOptions{})
	if *sb2.Spec.Replicas != 0 {
		t.Errorf("after suspend: replicas=%d, want 0", *sb2.Spec.Replicas)
	}

	// Resume (set replicas back to 1, but don't wait for pod since no pod exists in fake)
	if err := b.setReplicas(ctx, session.Ref{ID: "test-suspend"}, 1); err != nil {
		t.Fatalf("setReplicas: %v", err)
	}
	sb3, _ := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "test-suspend", metav1.GetOptions{})
	if *sb3.Spec.Replicas != 1 {
		t.Errorf("after resume: replicas=%d, want 1", *sb3.Spec.Replicas)
	}
}

package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

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

// TestBuildEnvAnthropicAuth: the claude-sdk pod gets exactly one Anthropic
// credential env, selected by spec.AnthropicAuth. Default and "oauth" wire
// CLAUDE_CODE_OAUTH_TOKEN (from key api-key) and leave ANTHROPIC_API_KEY unset;
// "api-key" wires ANTHROPIC_API_KEY (from key console-api-key) and leaves
// CLAUDE_CODE_OAUTH_TOKEN unset. Never both — Claude Code would reject the OAuth
// token if a real x-api-key were also present.
func TestBuildEnvAnthropicAuth(t *testing.T) {
	cases := []struct {
		name       string
		auth       string
		wantEnv    string // env var that MUST be present
		wantKey    string // Secret key it must reference
		notWantEnv string // env var that MUST be absent
	}{
		{"default", "", "CLAUDE_CODE_OAUTH_TOKEN", anthropicSecretKey, "ANTHROPIC_API_KEY"},
		{"oauth", "oauth", "CLAUDE_CODE_OAUTH_TOKEN", anthropicSecretKey, "ANTHROPIC_API_KEY"},
		{"api-key", "api-key", "ANTHROPIC_API_KEY", anthropicAPISecretKey, "CLAUDE_CODE_OAUTH_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := buildEnv(session.Spec{Backend: "claude-sdk", AnthropicAuth: tc.auth}, "s")

			got := envVar(env, tc.wantEnv)
			if got == nil || got.ValueFrom == nil || got.ValueFrom.SecretKeyRef == nil {
				t.Fatalf("%s should reference a secret key", tc.wantEnv)
			}
			ref := got.ValueFrom.SecretKeyRef
			if ref.Name != anthropicSecretName {
				t.Errorf("%s secret name: got %q, want %q", tc.wantEnv, ref.Name, anthropicSecretName)
			}
			if ref.Key != tc.wantKey {
				t.Errorf("%s secret key: got %q, want %q", tc.wantEnv, ref.Key, tc.wantKey)
			}
			if ref.Optional == nil || !*ref.Optional {
				t.Errorf("%s should reference the secret optionally", tc.wantEnv)
			}
			if envVar(env, tc.notWantEnv) != nil {
				t.Errorf("%s must be absent when AnthropicAuth=%q (exactly one credential per pod)", tc.notWantEnv, tc.auth)
			}
		})
	}
}

// TestBuildEnvAnthropicAccount: an account-backed claude session references the
// PER-SESSION Secret (key anthropic-credential), NOT the shared
// anthropic-credentials Secret, and NOT Optional (CreateSession wrote the key,
// so a missing key must fail the pod loudly). The env var (credential type) is
// still chosen by AnthropicAuth. The no-account path is the legacy shared-Secret
// behavior, covered by TestBuildEnvAnthropicAuth.
func TestBuildEnvAnthropicAccount(t *testing.T) {
	cases := []struct {
		name       string
		auth       string
		wantEnv    string
		notWantEnv string
	}{
		{"account-oauth", "oauth", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
		{"account-default", "", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
		{"account-api-key", "api-key", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := session.Spec{
				Backend:             "claude-sdk",
				AnthropicAuth:       tc.auth,
				AnthropicAccountID:  "acct-work",
				AnthropicCredential: []byte("sk-ant-secret-should-never-inline"),
			}
			env := buildEnv(spec, "s")

			got := envVar(env, tc.wantEnv)
			if got == nil || got.ValueFrom == nil || got.ValueFrom.SecretKeyRef == nil {
				t.Fatalf("%s should reference a secret key", tc.wantEnv)
			}
			ref := got.ValueFrom.SecretKeyRef
			if ref.Name != sessionSecretName("s") {
				t.Errorf("%s secret name: got %q, want per-session %q", tc.wantEnv, ref.Name, sessionSecretName("s"))
			}
			if ref.Key != secretKeyAnthropicCredential {
				t.Errorf("%s secret key: got %q, want %q", tc.wantEnv, ref.Key, secretKeyAnthropicCredential)
			}
			if ref.Optional != nil && *ref.Optional {
				t.Errorf("%s must NOT be Optional on the account path (we wrote the key)", tc.wantEnv)
			}
			if got.Value != "" {
				t.Errorf("%s must be a SecretKeyRef, never an inline Value (got %q)", tc.wantEnv, got.Value)
			}
			if envVar(env, tc.notWantEnv) != nil {
				t.Errorf("%s must be absent (exactly one credential per pod)", tc.notWantEnv)
			}
		})
	}
}

// TestBuildEnvNoCredentialInline is the anti-regression guard (design-review
// requirement): with a credential set, the literal credential bytes must appear
// NOWHERE in the built env slice — the pod receives the secret only via
// SecretKeyRef, never as an inline Value that would land in the Sandbox object
// (and thus etcd, kubectl get, the local index if ever serialized).
func TestBuildEnvNoCredentialInline(t *testing.T) {
	const secretBytes = "sk-ant-oat-LITERAL-CREDENTIAL-BYTES"
	for _, auth := range []string{"oauth", "api-key"} {
		spec := session.Spec{
			Backend:             "claude-sdk",
			AnthropicAuth:       auth,
			AnthropicAccountID:  "acct-work",
			AnthropicCredential: []byte(secretBytes),
		}
		env := buildEnv(spec, "s")
		for _, e := range env {
			if strings.Contains(e.Value, secretBytes) {
				t.Fatalf("auth=%s: env %q inlines the credential bytes (must be SecretKeyRef only)", auth, e.Name)
			}
		}
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

// TestCreateSessionAnthropicCredential: when spec carries an account credential,
// the per-session Secret holds it under anthropic-credential and is labeled with
// the account id (for logout/rotation enumeration); the runner token and other
// keys are still present. The built Sandbox object must not inline the bytes.
func TestCreateSessionAnthropicCredential(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	const cred = "sk-ant-oat-ACCOUNT-CREDENTIAL"
	spec := session.Spec{
		ID:                  "claude-sdk-acct",
		ProjectPath:         "/tmp",
		Backend:             "claude-sdk",
		RunnerImage:         "test:latest",
		SSHPublicKey:        "ssh-ed25519 AAAAKEY user@host",
		AnthropicAuth:       "oauth",
		AnthropicAccountID:  "acct-work",
		AnthropicCredential: []byte(cred),
	}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}

	secret, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-acct"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret.Data[secretKeyAnthropicCredential]) != cred {
		t.Errorf("anthropic-credential: got %q, want %q", secret.Data[secretKeyAnthropicCredential], cred)
	}
	if secret.Labels[labelAnthropicAccount] != "acct-work" {
		t.Errorf("account label: got %q, want acct-work", secret.Labels[labelAnthropicAccount])
	}
	// Other keys still present.
	if len(secret.Data[secretKeyRunnerToken]) == 0 {
		t.Error("runner token should still be set alongside the credential")
	}
	if string(secret.Data[secretKeySSHAuthorizedKey]) != spec.SSHPublicKey {
		t.Error("ssh key should still be set alongside the credential")
	}

	// Anti-regression: the marshaled Sandbox object must not contain the literal
	// credential bytes anywhere (env is a SecretKeyRef into the per-session Secret).
	sb, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "claude-sdk-acct", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	raw, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal sandbox: %v", err)
	}
	if strings.Contains(string(raw), cred) {
		t.Fatal("credential bytes leaked into the Sandbox object (must be SecretKeyRef only)")
	}
	env := sb.Spec.PodTemplate.Spec.Containers[0].Env
	tok := envVar(env, "CLAUDE_CODE_OAUTH_TOKEN")
	if tok == nil || tok.ValueFrom == nil || tok.ValueFrom.SecretKeyRef == nil ||
		tok.ValueFrom.SecretKeyRef.Name != sessionSecretName("claude-sdk-acct") ||
		tok.ValueFrom.SecretKeyRef.Key != secretKeyAnthropicCredential {
		t.Error("CLAUDE_CODE_OAUTH_TOKEN should reference the per-session anthropic-credential key")
	}
}

// TestCreateSessionNoAnthropicCredential: without an account credential the
// per-session Secret carries neither the anthropic-credential key nor the
// account label (legacy shared-Secret path).
func TestCreateSessionNoAnthropicCredential(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	spec := session.Spec{ID: "claude-sdk-noacct", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}
	secret, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-noacct"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if _, ok := secret.Data[secretKeyAnthropicCredential]; ok {
		t.Error("anthropic-credential key must be absent without an account credential")
	}
	if _, ok := secret.Labels[labelAnthropicAccount]; ok {
		t.Error("account label must be absent without an account credential")
	}
}

// TestCreateSessionPatchesCredentialOnExists guards the AlreadyExists gap:
// re-creating a session id with a DIFFERENT account must patch the credential +
// label onto the existing Secret without clobbering runner-token,
// opencode-password or the ssh key.
func TestCreateSessionPatchesCredentialOnExists(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	first := session.Spec{
		ID:                  "claude-sdk-rot",
		ProjectPath:         "/tmp",
		Backend:             "claude-sdk",
		RunnerImage:         "test:latest",
		SSHPublicKey:        "ssh-ed25519 AAAAKEY user@host",
		AnthropicAccountID:  "acct-personal",
		AnthropicCredential: []byte("sk-ant-oat-PERSONAL"),
	}
	if _, err := b.CreateSession(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	before, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-rot"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	origToken := before.Data[secretKeyRunnerToken]
	origPw := before.Data[secretKeyOpencodePassword]
	origSSH := before.Data[secretKeySSHAuthorizedKey]

	// Re-create with the same id but a different account credential.
	second := first
	second.AnthropicAccountID = "acct-work"
	second.AnthropicCredential = []byte("sk-ant-oat-WORK")
	if _, err := b.CreateSession(ctx, second); err != nil {
		t.Fatalf("second create: %v", err)
	}

	after, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-rot"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret after: %v", err)
	}
	if string(after.Data[secretKeyAnthropicCredential]) != "sk-ant-oat-WORK" {
		t.Errorf("credential not patched: got %q, want sk-ant-oat-WORK", after.Data[secretKeyAnthropicCredential])
	}
	if after.Labels[labelAnthropicAccount] != "acct-work" {
		t.Errorf("account label not patched: got %q, want acct-work", after.Labels[labelAnthropicAccount])
	}
	// Other keys untouched (runner-token is generated once and reused; the patch
	// must not clobber it or the ssh/opencode keys).
	if string(after.Data[secretKeyRunnerToken]) != string(origToken) {
		t.Error("runner-token must be preserved across the credential patch")
	}
	if string(after.Data[secretKeyOpencodePassword]) != string(origPw) {
		t.Error("opencode-password must be preserved across the credential patch")
	}
	if string(after.Data[secretKeySSHAuthorizedKey]) != string(origSSH) {
		t.Error("ssh key must be preserved across the credential patch")
	}
}

// TestCreateSessionStripsCredentialOnAccountRemoval guards the stale-credential
// gap on the AlreadyExists path: re-creating a session id WITHOUT an account
// must strip the old anthropic-credential key and account label from the
// existing Secret — otherwise logout/rotation label enumeration would
// false-positive on a session that no longer uses the account, and a stale
// credential copy would linger. All other keys are preserved.
func TestCreateSessionStripsCredentialOnAccountRemoval(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	withAccount := session.Spec{
		ID:                  "claude-sdk-strip",
		ProjectPath:         "/tmp",
		Backend:             "claude-sdk",
		RunnerImage:         "test:latest",
		SSHPublicKey:        "ssh-ed25519 AAAAKEY user@host",
		AnthropicAccountID:  "acct-old",
		AnthropicCredential: []byte("sk-ant-oat-OLD"),
	}
	if _, err := b.CreateSession(ctx, withAccount); err != nil {
		t.Fatalf("first create: %v", err)
	}
	before, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-strip"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	origToken := before.Data[secretKeyRunnerToken]

	// Re-create the same id with no account: the shared-Secret fallback path.
	noAccount := withAccount
	noAccount.AnthropicAccountID = ""
	noAccount.AnthropicCredential = nil
	if _, err := b.CreateSession(ctx, noAccount); err != nil {
		t.Fatalf("second create: %v", err)
	}

	after, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-strip"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret after: %v", err)
	}
	if _, ok := after.Data[secretKeyAnthropicCredential]; ok {
		t.Error("anthropic-credential key must be stripped when re-created without an account")
	}
	if _, ok := after.Labels[labelAnthropicAccount]; ok {
		t.Error("account label must be stripped when re-created without an account")
	}
	if string(after.Data[secretKeyRunnerToken]) != string(origToken) {
		t.Error("runner-token must be preserved across the credential strip")
	}
	if string(after.Data[secretKeySSHAuthorizedKey]) != withAccount.SSHPublicKey {
		t.Error("ssh key must be preserved across the credential strip")
	}
}

// TestCreateSessionRecreateFailureDoesNotDestroy guards the destructive-rollback
// HIGH: when the per-session Secret already EXISTS (the session belongs to a
// prior CreateSession call) and the credential sync fails, CreateSession must
// return the error WITHOUT the rollback defer deleting the pre-existing
// Secret/PVC/Sandbox — the PVC holds the session's workspace data, and a failed
// re-create must never destroy it as collateral damage. (Fresh-create rollback
// staying intact is covered by TestCreateSessionRollsBackSecretOnPVCFailure /
// TestCreateSessionRollsBackSecretAndPVCOnSandboxFailure in backend_c5_test.go.)
func TestCreateSessionRecreateFailureDoesNotDestroy(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	first := session.Spec{
		ID:                  "claude-sdk-safe",
		ProjectPath:         "/tmp",
		Backend:             "claude-sdk",
		RunnerImage:         "test:latest",
		AnthropicAccountID:  "acct-a",
		AnthropicCredential: []byte("sk-ant-oat-A"),
	}
	if _, err := b.CreateSession(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Every Secret Update now fails non-retryably, so the re-create's credential
	// sync errors out after the Secret create has hit AlreadyExists.
	core.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(fmt.Errorf("simulated update failure"))
	})

	second := first
	second.AnthropicAccountID = "acct-b"
	second.AnthropicCredential = []byte("sk-ant-oat-B")
	if _, err := b.CreateSession(ctx, second); err == nil {
		t.Fatal("re-create with failing credential sync should return an error")
	}

	// No Delete may have been issued against any of the session's resources.
	for _, a := range core.Actions() {
		if a.GetVerb() == "delete" {
			t.Fatalf("re-create failure deleted a pre-existing %s — destructive rollback", a.GetResource().Resource)
		}
	}
	for _, a := range agents.Actions() {
		if a.GetVerb() == "delete" {
			t.Fatalf("re-create failure deleted a pre-existing %s — destructive rollback", a.GetResource().Resource)
		}
	}
	// And the resources are still intact with the ORIGINAL credential.
	secret, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-safe"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pre-existing secret gone after failed re-create: %v", err)
	}
	if string(secret.Data[secretKeyAnthropicCredential]) != "sk-ant-oat-A" {
		t.Errorf("pre-existing credential changed: got %q, want sk-ant-oat-A", secret.Data[secretKeyAnthropicCredential])
	}
	if _, err := core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "claude-sdk-safe", metav1.GetOptions{}); err != nil {
		t.Fatalf("pre-existing PVC gone after failed re-create: %v", err)
	}
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "claude-sdk-safe", metav1.GetOptions{}); err != nil {
		t.Fatalf("pre-existing Sandbox gone after failed re-create: %v", err)
	}
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

// TestListSurvivesPerItemGetFailure guards the reconcile-safety fix: a per-item
// Sandbox Get failure (API pressure, or the caller's list deadline truncating a
// sequence of Gets) must NOT drop a live Sandbox from the List snapshot. The
// dashboard reconcile treats absence-from-the-snapshot as deletion, so dropping
// a live session here would make it wrongly pruned. List builds states straight
// from the bulk-list objects, so a failing "get sandboxes" reactor can't shrink
// the result.
func TestListSurvivesPerItemGetFailure(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	for _, id := range []string{"list-a", "list-b"} {
		spec := session.Spec{ID: session.ID(id), ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest"}
		if _, err := b.CreateSession(ctx, spec); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// Every per-item Sandbox GET now fails, while the bulk LIST still succeeds.
	agents.PrependReactor("get", "sandboxes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(fmt.Errorf("simulated API pressure"))
	})

	states, err := b.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("got %d states, want 2 — a per-item Get failure must not drop live sessions from the snapshot", len(states))
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

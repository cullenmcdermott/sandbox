package k8s

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestIsReservedEnvName pins the denylist the client's env-injection gate
// consumes: the SANDBOX_ prefix and every runner/backend/credential var is
// reserved; an unrelated operator name is not.
func TestIsReservedEnvName(t *testing.T) {
	reserved := []string{
		"SANDBOX_SESSION_ID", "SANDBOX_ANYTHING", "SANDBOX_EXTRA_ENV_NAMES",
		"RUNNER_TOKEN", "PROJECT_PATH", "HOME", "PATH", "IS_SANDBOX",
		"TERMINATION_GRACE_SECONDS", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_DISABLE_AUTO_MEMORY",
		"XDG_DATA_HOME", "OPENCODE_CONFIG", "OPENCODE_PORT", "OPENCODE_SERVER_USERNAME",
		"OPENCODE_SERVER_PASSWORD", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENCODE_API_KEY",
		"OPENCODE_AUTH_JSON", "CLAUDE_CODE_OAUTH_TOKEN", "CODEX_AUTH_JSON", "CODEX_HOME",
		"CLAUDE_CREDENTIALS_JSON", "CLAUDE_OAUTH_ACCOUNT_JSON",
	}
	for _, name := range reserved {
		if !IsReservedEnvName(name) {
			t.Errorf("IsReservedEnvName(%q) = false, want true (reserved)", name)
		}
	}
	for _, name := range []string{"GITLAB_TOKEN", "TOOL_ENDPOINT", "MY_APP_URL", "SANDBOXY"} {
		if IsReservedEnvName(name) {
			t.Errorf("IsReservedEnvName(%q) = true, want false (operator-supplied)", name)
		}
	}
}

// TestBuildEnvExtraEnv: ExtraEnv lands as plain vars, ExtraSecretEnv as Optional
// SecretKeyRefs into the per-session Secret (key extra-secret-env-<NAME>), and both
// marker vars carry the SORTED comma-joined name lists. Applied on the common path
// so every backend sees them.
func TestBuildEnvExtraEnv(t *testing.T) {
	spec := session.Spec{
		Backend: "claude-sdk",
		// Deliberately out of sorted order to prove the markers sort.
		ExtraEnv:       map[string]string{"TOOL_ENDPOINT": "https://tool.internal", "APP_URL": "https://app"},
		ExtraSecretEnv: map[string][]byte{"GITLAB_TOKEN": []byte("glpat-secret"), "AWS_KEY": []byte("aws-secret")},
	}
	env := buildEnv(spec, "claude-sdk-x")

	// Plain ExtraEnv vars carry their literal value.
	if got := envValue(env, "TOOL_ENDPOINT"); got != "https://tool.internal" {
		t.Errorf("TOOL_ENDPOINT = %q, want https://tool.internal", got)
	}
	if got := envValue(env, "APP_URL"); got != "https://app" {
		t.Errorf("APP_URL = %q, want https://app", got)
	}
	if got := envValue(env, "SANDBOX_EXTRA_ENV_NAMES"); got != "APP_URL,TOOL_ENDPOINT" {
		t.Errorf("SANDBOX_EXTRA_ENV_NAMES = %q, want sorted APP_URL,TOOL_ENDPOINT", got)
	}

	// ExtraSecretEnv vars reference the per-session Secret, Optional so a dropped
	// var can't brick a resume, and never inline the value.
	for _, tc := range []struct{ name, key string }{
		{"GITLAB_TOKEN", "extra-secret-env-GITLAB_TOKEN"},
		{"AWS_KEY", "extra-secret-env-AWS_KEY"},
	} {
		e := envVar(env, tc.name)
		if e == nil || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			t.Fatalf("%s should be a SecretKeyRef", tc.name)
		}
		ref := e.ValueFrom.SecretKeyRef
		if ref.Name != sessionSecretName("claude-sdk-x") {
			t.Errorf("%s secret name = %q, want %q", tc.name, ref.Name, sessionSecretName("claude-sdk-x"))
		}
		if ref.Key != tc.key {
			t.Errorf("%s secret key = %q, want %q", tc.name, ref.Key, tc.key)
		}
		if ref.Optional == nil || !*ref.Optional {
			t.Errorf("%s SecretKeyRef must be Optional (a re-create that drops it must not brick the pod)", tc.name)
		}
		if e.Value != "" {
			t.Errorf("%s must not inline a value", tc.name)
		}
	}
	if got := envValue(env, "SANDBOX_EXTRA_SECRET_ENV_NAMES"); got != "AWS_KEY,GITLAB_TOKEN" {
		t.Errorf("SANDBOX_EXTRA_SECRET_ENV_NAMES = %q, want sorted AWS_KEY,GITLAB_TOKEN", got)
	}
}

// TestBuildEnvNoExtraEnvMarkers: with empty maps, neither marker var is emitted
// (so the runner's markers-absent path stays untouched).
func TestBuildEnvNoExtraEnvMarkers(t *testing.T) {
	env := buildEnv(session.Spec{Backend: "claude-sdk"}, "claude-sdk-y")
	if envVar(env, "SANDBOX_EXTRA_ENV_NAMES") != nil {
		t.Error("SANDBOX_EXTRA_ENV_NAMES must be absent when ExtraEnv is empty")
	}
	if envVar(env, "SANDBOX_EXTRA_SECRET_ENV_NAMES") != nil {
		t.Error("SANDBOX_EXTRA_SECRET_ENV_NAMES must be absent when ExtraSecretEnv is empty")
	}
}

// TestCreateSessionExtraSecretEnv: ExtraSecretEnv values land in the per-session
// Secret under extra-secret-env-<NAME> (no label), the runner-token/other keys are
// intact, and the marshaled Sandbox object never inlines the secret bytes.
func TestCreateSessionExtraSecretEnv(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	const tok = "glpat-EXTRA-SECRET-ENV-VALUE"
	spec := session.Spec{
		ID:             "claude-sdk-extra",
		ProjectPath:    "/tmp",
		Backend:        "claude-sdk",
		RunnerImage:    "test:latest",
		SSHPublicKey:   "ssh-ed25519 AAAAKEY user@host",
		ExtraSecretEnv: map[string][]byte{"GITLAB_TOKEN": []byte(tok)},
	}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}

	secret, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-extra"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret.Data["extra-secret-env-GITLAB_TOKEN"]) != tok {
		t.Errorf("extra-secret-env-GITLAB_TOKEN = %q, want %q", secret.Data["extra-secret-env-GITLAB_TOKEN"], tok)
	}
	if len(secret.Data[secretKeyRunnerToken]) == 0 {
		t.Error("runner token should still be set alongside the extra secret env")
	}
	// Not account-scoped — no label.
	if _, ok := secret.Labels[labelAnthropicAccount]; ok {
		t.Error("extra secret env must not add an account label")
	}

	// Anti-regression: the marshaled Sandbox must not contain the literal secret
	// bytes anywhere (the env is a SecretKeyRef into the per-session Secret).
	sb, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, "claude-sdk-extra", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	raw, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal sandbox: %v", err)
	}
	if strings.Contains(string(raw), tok) {
		t.Fatal("extra secret env bytes leaked into the Sandbox object (must be SecretKeyRef only)")
	}
}

// TestCreateSessionReconcilesExtraSecretEnv: a re-create patches a changed
// ExtraSecretEnv value and strips a key the new spec no longer carries. The
// Optional SecretKeyRefs mean the strip is safe with no shape guard.
func TestCreateSessionReconcilesExtraSecretEnv(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	first := session.Spec{
		ID:          "claude-sdk-recon",
		ProjectPath: "/tmp",
		Backend:     "claude-sdk",
		RunnerImage: "test:latest",
		ExtraSecretEnv: map[string][]byte{
			"GITLAB_TOKEN": []byte("glpat-OLD"),
			"DROP_ME":      []byte("stale-value"),
		},
	}
	if _, err := b.CreateSession(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Re-create: change GITLAB_TOKEN, drop DROP_ME entirely.
	second := first
	second.ExtraSecretEnv = map[string][]byte{"GITLAB_TOKEN": []byte("glpat-NEW")}
	if _, err := b.CreateSession(ctx, second); err != nil {
		t.Fatalf("re-create must succeed (Optional refs, no shape flip): %v", err)
	}

	secret, err := b.core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("claude-sdk-recon"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret.Data["extra-secret-env-GITLAB_TOKEN"]) != "glpat-NEW" {
		t.Errorf("changed value not patched: got %q, want glpat-NEW", secret.Data["extra-secret-env-GITLAB_TOKEN"])
	}
	if _, ok := secret.Data["extra-secret-env-DROP_ME"]; ok {
		t.Error("removed key must be stripped on re-create")
	}
}

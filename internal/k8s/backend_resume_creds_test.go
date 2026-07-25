package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Resume-time claude-pane credential refresh: a session suspended past its OAuth
// token's expiry must resume on the orchestrator's fresh host login rather than
// the (dead) copy written at Create. The refresh rewrites the per-session
// Secret's two claude-pane keys and MUST land before the replicas 0→1 flip — the
// pod resolves the Secret only at container start, so writing after the flip
// races the kubelet onto the expired copy.

// seedClaudePaneSecret creates a per-session Secret carrying the two claude-pane
// credential keys with the given bytes.
func seedClaudePaneSecret(t *testing.T, core *fake.Clientset, id, cred, account string) {
	t.Helper()
	if _, err := core.CoreV1().Secrets("agent-sessions").Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: sessionSecretName(id), Namespace: "agent-sessions"},
		Data: map[string][]byte{
			secretKeyClaudeCredentialsJSON:  []byte(cred),
			secretKeyClaudeOAuthAccountJSON: []byte(account),
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed claude-pane secret: %v", err)
	}
}

func getClaudePaneSecret(t *testing.T, core *fake.Clientset, id string) *corev1.Secret {
	t.Helper()
	sec, err := core.CoreV1().Secrets("agent-sessions").Get(context.Background(), sessionSecretName(id), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	return sec
}

func TestResumeRefreshesClaudePaneCredential(t *testing.T) {
	const id = "sess-cr"
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset(mkRunningPod("sess-cr-pod", id, testPinned))
	seedClaudePaneSecret(t, core, id, "old-cred", "old-acct")
	seedSandboxWithRunner(t, agents, id, testImage, nil)
	b := NewForClients(agents, core, "agent-sessions")

	// Ordering: by the time the Sandbox replicas flip lands, the Secret must
	// already carry the refreshed credential — writing after would race pod start.
	credAtScaleUp := "<never observed>"
	agents.PrependReactor("update", "sandboxes", func(k8stesting.Action) (bool, runtime.Object, error) {
		sec := getClaudePaneSecret(t, core, id)
		credAtScaleUp = string(sec.Data[secretKeyClaudeCredentialsJSON])
		return false, nil, nil // let the tracker apply the replicas update
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	opts := session.ResumeOptions{
		ClaudeCredentialsJSON:  []byte("new-cred"),
		ClaudeOAuthAccountJSON: []byte("new-acct"),
	}
	if err := b.Resume(ctx, session.Ref{ID: id}, opts); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	sec := getClaudePaneSecret(t, core, id)
	if got := string(sec.Data[secretKeyClaudeCredentialsJSON]); got != "new-cred" {
		t.Errorf("credential key = %q, want %q", got, "new-cred")
	}
	if got := string(sec.Data[secretKeyClaudeOAuthAccountJSON]); got != "new-acct" {
		t.Errorf("oauth-account key = %q, want %q", got, "new-acct")
	}
	if credAtScaleUp != "new-cred" {
		t.Errorf("credential not refreshed before replicas flip: observed %q at scale-up", credAtScaleUp)
	}
}

func TestResumeWithoutCredentialLeavesSecretAlone(t *testing.T) {
	const id = "sess-nocr"
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset(mkRunningPod("sess-nocr-pod", id, testPinned))
	seedClaudePaneSecret(t, core, id, "keep-cred", "keep-acct")
	seedSandboxWithRunner(t, agents, id, testImage, nil)
	b := NewForClients(agents, core, "agent-sessions")

	// Empty options must not touch the Secret at all — count any Secret update.
	var secretUpdates int
	core.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		secretUpdates++
		return false, nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.Resume(ctx, session.Ref{ID: id}, session.ResumeOptions{}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if secretUpdates != 0 {
		t.Errorf("empty ResumeOptions issued %d Secret update(s), want 0", secretUpdates)
	}
	sec := getClaudePaneSecret(t, core, id)
	if got := string(sec.Data[secretKeyClaudeCredentialsJSON]); got != "keep-cred" {
		t.Errorf("credential key mutated to %q, want %q", got, "keep-cred")
	}
	if got := string(sec.Data[secretKeyClaudeOAuthAccountJSON]); got != "keep-acct" {
		t.Errorf("oauth-account key mutated to %q, want %q", got, "keep-acct")
	}
}

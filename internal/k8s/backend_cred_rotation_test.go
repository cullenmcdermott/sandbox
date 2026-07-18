package k8s

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// seedSessionSecret creates a per-session runner Secret carrying the given
// anthropic credential bytes + account label (empty cred => no credential key).
func seedSessionSecret(t *testing.T, b *Backend, id, cred, accountID string) {
	t.Helper()
	data := map[string][]byte{}
	labels := map[string]string{}
	if cred != "" {
		data[secretKeyAnthropicCredential] = []byte(cred)
		labels[labelAnthropicAccount] = accountID
	}
	if _, err := b.core.CoreV1().Secrets(b.namespace).Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: sessionSecretName(id), Namespace: b.namespace, Labels: labels},
		Data:       data,
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed session secret: %v", err)
	}
}

// TestSyncSessionCredentialWarnsOnAccountSwap pins the [V33] rotation advisory:
// syncSessionCredential must WARN when it replaces an existing non-empty
// anthropic credential on a session Secret (a same-shape account swap) — the
// running pod resolved its credential from the Secret once at pod start, so it
// keeps authenticating as the OLD account until a restart. It must stay SILENT
// when the bytes are unchanged and when it is provisioning a credential onto a
// Secret that had none (first attach, not a rotation). The warning must never
// echo the raw credential bytes.
func TestSyncSessionCredentialWarnsOnAccountSwap(t *testing.T) {
	ctx := context.Background()
	const id = "claude-sdk-swap"
	const oldCred = "sk-ant-old-account-token"
	const newCred = "sk-ant-new-account-token"

	specWith := func(cred, account string) session.Spec {
		return session.Spec{
			ID:                  id,
			Namespace:           "agent-sessions",
			AnthropicCredential: []byte(cred),
			AnthropicAccountID:  account,
		}
	}

	t.Run("warns when the credential bytes change on an existing secret", func(t *testing.T) {
		b := newTestBackend(t)
		seedSessionSecret(t, b, id, oldCred, "acct-A")

		out := captureStderr(t, func() {
			if err := b.syncSessionCredential(ctx, specWith(newCred, "acct-B")); err != nil {
				t.Fatalf("syncSessionCredential: %v", err)
			}
		})
		if out == "" {
			t.Fatal("expected a rotation warning on an account swap, got none")
		}
		if !strings.Contains(out, id) {
			t.Errorf("warning missing the session id: %q", out)
		}
		if strings.Contains(out, oldCred) || strings.Contains(out, newCred) {
			t.Errorf("warning leaked raw credential bytes: %q", out)
		}
	})

	t.Run("silent when the credential is unchanged", func(t *testing.T) {
		b := newTestBackend(t)
		seedSessionSecret(t, b, id, oldCred, "acct-A")

		if out := captureStderr(t, func() {
			if err := b.syncSessionCredential(ctx, specWith(oldCred, "acct-A")); err != nil {
				t.Fatalf("syncSessionCredential: %v", err)
			}
		}); out != "" {
			t.Errorf("expected silence for an unchanged credential, got %q", out)
		}
	})

	t.Run("silent when provisioning onto a secret that had no credential", func(t *testing.T) {
		b := newTestBackend(t)
		seedSessionSecret(t, b, id, "", "") // Secret exists but carries no anthropic credential yet

		if out := captureStderr(t, func() {
			if err := b.syncSessionCredential(ctx, specWith(newCred, "acct-B")); err != nil {
				t.Fatalf("syncSessionCredential: %v", err)
			}
		}); out != "" {
			t.Errorf("expected silence for a first-time provisioning, got %q", out)
		}
	})
}

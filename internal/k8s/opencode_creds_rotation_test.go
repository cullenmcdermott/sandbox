package k8s

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns whatever
// fn wrote there. warnIfOpencodeCredsRotated prints its advisory to os.Stderr, so
// this is how a test asserts the warning fired (non-empty) or stayed silent
// (empty). Not for parallel use — it swaps a process global.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}

// seedOpencodeSecret creates the shared opencode-credentials Secret in the
// backend's namespace with the anthropic provider key set to keyBytes.
func seedOpencodeSecret(t *testing.T, b *Backend, keyBytes string) {
	t.Helper()
	if _, err := b.core.CoreV1().Secrets(b.namespace).Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: opencodeSecretName, Namespace: b.namespace},
		Data:       map[string][]byte{opencodeSecretKeyAnthropic: []byte(keyBytes)},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed opencode secret: %v", err)
	}
}

// stampedSandbox returns a Sandbox annotated as an opencode session whose
// create-time provider stamp is credsHash against the anthropic provider key.
func stampedSandbox(name, credsHash string) *agentv1alpha1.Sandbox {
	return &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "agent-sessions",
			Annotations: map[string]string{
				annotationOpencodeCredsHash: credsHash,
				annotationOpencodeProvider:  opencodeSecretKeyAnthropic,
			},
		},
	}
}

// TestWarnIfOpencodeCredsRotated pins the rotation advisory: it must FIRE when
// the live opencode-credentials Secret hashes differently than the pod's
// create-time stamp (the running pod is still authenticating with the old key,
// resolved once at pod start), and stay SILENT when the live key still matches
// the stamp. Silence is also required whenever there is nothing to compare — no
// stamp, or the Secret is unreadable — so a best-effort reconcile never spams.
func TestWarnIfOpencodeCredsRotated(t *testing.T) {
	ctx := context.Background()
	const liveKey = "sk-ant-live-key"

	t.Run("fires when the secret rotated under a running pod", func(t *testing.T) {
		b := newTestBackend(t)
		seedOpencodeSecret(t, b, liveKey)
		// Stamp records an OLD fingerprint that does not match the live key.
		sb := stampedSandbox("opencode-rotated", "deadbeef")

		out := captureStderr(t, func() { b.warnIfOpencodeCredsRotated(ctx, sb) })
		if out == "" {
			t.Fatal("expected a rotation warning, got none")
		}
		if !strings.Contains(out, "was rotated") || !strings.Contains(out, "opencode-rotated") {
			t.Errorf("warning text missing rotation/session detail: %q", out)
		}
		// The advisory must never echo the raw key bytes.
		if strings.Contains(out, liveKey) {
			t.Errorf("warning leaked the raw provider key: %q", out)
		}
	})

	t.Run("silent when the live key still matches the stamp", func(t *testing.T) {
		b := newTestBackend(t)
		seedOpencodeSecret(t, b, liveKey)
		// Stamp equals the live key's fingerprint => fresh, no warning.
		fresh := opencodeCredsHash(map[string][]byte{opencodeSecretKeyAnthropic: []byte(liveKey)}, "")
		sb := stampedSandbox("opencode-fresh", fresh)

		if out := captureStderr(t, func() { b.warnIfOpencodeCredsRotated(ctx, sb) }); out != "" {
			t.Errorf("expected silence for a fresh stamp, got %q", out)
		}
	})

	t.Run("silent when the sandbox carries no stamp", func(t *testing.T) {
		b := newTestBackend(t)
		seedOpencodeSecret(t, b, liveKey)
		sb := &agentv1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "unstamped", Namespace: "agent-sessions"}}

		if out := captureStderr(t, func() { b.warnIfOpencodeCredsRotated(ctx, sb) }); out != "" {
			t.Errorf("expected silence for an unstamped sandbox, got %q", out)
		}
	})

	t.Run("silent when the secret is unreadable", func(t *testing.T) {
		b := newTestBackend(t) // no opencode-credentials Secret seeded
		sb := stampedSandbox("opencode-nosecret", "deadbeef")

		if out := captureStderr(t, func() { b.warnIfOpencodeCredsRotated(ctx, sb) }); out != "" {
			t.Errorf("expected silence when the secret is absent, got %q", out)
		}
	})
}

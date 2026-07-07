//go:build integration

// Package k8sit holds the two-layer integration suite that drives the real
// CLI → agent-sandbox controller → Sandbox CRD → runner pod → HTTP+SSE turn
// loop against a disposable local KIND cluster ("sandbox-local").
//
// It is build-tagged `integration` so it stays out of `go test ./...` and
// `just check`; run it explicitly against the local dev env (see the justfile dev
// recipes). A hard context-isolation guard (localRestConfig) makes it impossible
// to point this suite at a non-local/remote cluster even if KUBECONFIG is
// mis-set: every test refuses to run unless the current kube context is
// "kind-sandbox-local" AND the API server host is loopback.
package k8sit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

const (
	// localContext is the only kube context this suite will ever talk to.
	localContext = "kind-sandbox-local"
	// localNamespace is where session pods (and the provider-key Secret) live.
	localNamespace = "agent-sessions"
	// runnerImage is the locally-built runner image loaded into the KIND node.
	runnerImage = "sandbox-runner:dev"
	// claudeCredSecret/claudeCredKey hold the optional Claude OAuth token
	// (CLAUDE_CODE_OAUTH_TOKEN). The claude-sdk turn test asserts a real reply
	// only when present; otherwise it degrades to plumbing-only. The opencode
	// backend needs no key — its default model (opencode/big-pickle) is free.
	claudeCredSecret = "anthropic-credentials"
	claudeCredKey    = "api-key"
	// turnPrompt is the canonical tiny prompt every backend's live turn sends.
	turnPrompt = "Reply with a short greeting."
)

// backendCase is the single source of truth for the integration conformance
// suite (Phase A of docs/archive/testing-parity-plan.md): every live test runs the same
// scenarios for each row, so a new backend (Codex) is onboarded by appending one.
type backendCase struct {
	name      string // subtest name
	backend   string // session.Backend* value
	idTag     string // short DNS-1123-safe session-id fragment
	turnModel string // model for the turn ("" = the backend's default; opencode's free big-pickle)
	needsKey  bool   // true if a REAL reply needs a provider secret (claude); opencode is free
}

var backendCases = []backendCase{
	{name: "opencode", backend: session.BackendOpenCode, idTag: "oc", turnModel: "", needsKey: false},
	{name: "claude", backend: session.BackendClaudeSDK, idTag: "cl", turnModel: "haiku", needsKey: true},
}

// expectRealReply reports whether this backend can complete a real reply in the
// current environment: opencode always (free model); claude only when its OAuth
// token Secret is present (else the test asserts plumbing-only).
func (bc backendCase) expectRealReply(t *testing.T, rc *rest.Config) bool {
	t.Helper()
	if !bc.needsKey {
		return true
	}
	return claudeKeyPresent(t, rc)
}

// localRestConfig is the context-isolation guard. It independently loads the
// kubeconfig the SAME way client-go does (honoring KUBECONFIG), then FAILS FAST
// unless BOTH:
//   - rawConfig.CurrentContext == "kind-sandbox-local", and
//   - rest.Config.Host is loopback (127.0.0.1 / ::1 / localhost).
//
// This guards against an accidental run against a remote production cluster
// even if KUBECONFIG is mis-set. It returns the validated
// *rest.Config so callers can build a clientset against the same target.
func localRestConfig(t *testing.T) *rest.Config {
	t.Helper()

	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, &clientcmd.ConfigOverrides{})

	raw, err := cc.RawConfig()
	if err != nil {
		t.Fatalf("context-isolation guard: load raw kubeconfig: %v", err)
	}
	if raw.CurrentContext != localContext {
		t.Fatalf("context-isolation guard: refusing to run — current context %q != %q (set KUBECONFIG to the local kubeconfig)",
			raw.CurrentContext, localContext)
	}

	rc, err := cc.ClientConfig()
	if err != nil {
		t.Fatalf("context-isolation guard: build rest.Config: %v", err)
	}
	if !isLoopbackHost(rc.Host) {
		t.Fatalf("context-isolation guard: refusing to run — API server host %q is not loopback (expected a KIND cluster)", rc.Host)
	}
	return rc
}

// isLoopbackHost reports whether the API server URL points at the local host.
func isLoopbackHost(host string) bool {
	return strings.Contains(host, "127.0.0.1") ||
		strings.Contains(host, "::1") ||
		strings.Contains(host, "localhost")
}

// claudeKeyPresent reports whether the anthropic-credentials Secret exists in
// agent-sessions with a non-empty, non-placeholder Claude OAuth token. A missing
// Secret is the normal "plumbing-only" case (the pod starts before the Secret
// exists), so NotFound returns false rather than failing the test. The opencode
// backend needs no equivalent — its default model is free.
func claudeKeyPresent(t *testing.T, rc *rest.Config) bool {
	t.Helper()
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		t.Fatalf("build clientset: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sec, err := cs.CoreV1().Secrets(localNamespace).Get(ctx, claudeCredSecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get secret %s/%s: %v", localNamespace, claudeCredSecret, err)
	}
	v := strings.TrimSpace(string(sec.Data[claudeCredKey]))
	// Treat the template's unedited placeholder as absent so a copy of
	// secret-template.yaml applied without real keys stays plumbing-only.
	return v != "" && !strings.Contains(v, "REPLACE_ME")
}

// createReadySession creates a fresh Sandbox for the given backend in the local
// dev env, waits for the pod to be Ready, and registers a Destroy cleanup. It
// runs the context-isolation guard first so no caller can skip it. Shared by the
// Backend-level tests and the CLI smoke test. idTag is a short DNS-1123-safe
// label fragment (e.g. "oc", "cl") so concurrent backends get distinct names.
func createReadySession(t *testing.T, backend string, idTag string) (*k8s.Backend, session.Ref) {
	t.Helper()
	localRestConfig(t) // guard: must pass before we touch the cluster

	be, err := k8s.New("")
	if err != nil {
		t.Fatalf("k8s.New: %v", err)
	}

	id := session.ID("k8sit-" + idTag + "-" + shortID(t))
	spec := session.Spec{
		ID:          id,
		ProjectPath: repoRoot(t),
		Backend:     backend,
		RunnerImage: runnerImage,
		// StorageClass "" => cluster default SC (KIND local-path binds).
	}

	createCtx, createCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer createCancel()
	ref, err := be.CreateSession(createCtx, spec)
	if err != nil {
		t.Fatalf("CreateSession %s: %v", id, err)
	}
	// Always tear the session + PVC down, even on failure. Use a fresh context
	// because the test's context may already be cancelled at cleanup time.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := be.Destroy(ctx, ref); err != nil {
			t.Logf("cleanup: destroy %s: %v", ref.ID, err)
		}
	})

	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer startCancel()
	if err := be.Start(startCtx, ref); err != nil {
		t.Fatalf("Start %s (pod did not become Ready): %v", ref.ID, err)
	}
	return be, ref
}

// runnerClientForRef port-forwards the runner HTTP port for ref and returns a
// connected client plus a cleanup func. Mirrors internal/cli.runnerClientFor
// but uses ForwardSpecsRunnerOnly (no SSH needed for a headless turn).
func runnerClientForRef(t *testing.T, backend *k8s.Backend, ref session.Ref) (*runner.Client, func()) {
	t.Helper()
	pfCtx, pfCancel := context.WithCancel(context.Background())
	handles, err := backend.PortForward(pfCtx, ref, k8s.ForwardSpecsRunnerOnly(0))
	if err != nil {
		pfCancel()
		t.Fatalf("PortForward %s: %v", ref.ID, err)
	}
	cleanup := func() {
		for _, h := range handles {
			h.Close()
		}
		pfCancel()
	}
	tokCtx, tokCancel := context.WithTimeout(pfCtx, 15*time.Second)
	defer tokCancel()
	token, err := backend.RunnerToken(tokCtx, ref)
	if err != nil {
		cleanup()
		t.Fatalf("RunnerToken %s: %v", ref.ID, err)
	}
	client := runner.New(httpLocalURL(handles[0].LocalPort()), token)
	return client, cleanup
}

// pollHealthy waits for the runner's /healthz to succeed, bounded by ctx.
// Mirrors internal/cli.waitHealthy's shape (short per-attempt timeouts).
func pollHealthy(ctx context.Context, client *runner.Client) error {
	for {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := client.Health(hctx)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// httpLocalURL builds the loopback runner URL for a forwarded local port.
func httpLocalURL(port int) string {
	return "http://127.0.0.1:" + itoa(port)
}

// itoa avoids pulling strconv into the hot path of a one-liner.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// envDuration reads a duration from env, falling back to def on empty/invalid.
func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// shortID returns a random 8-hex-char suffix for a DNS-1123-safe session name.
func shortID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("generate session id: %v", err)
	}
	return hex.EncodeToString(b)
}

// repoRoot walks up from the test's working directory to the module root
// (the directory containing go.mod). Used as the session ProjectPath and as
// the build dir for the CLI smoke test.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate repo root (go.mod) from %s", dir)
		}
		dir = parent
	}
}

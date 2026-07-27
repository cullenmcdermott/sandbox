package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestKubeExecRefresherNeedsRefresh pins the conservative marker table: each
// documented credExpiryMarkers signature must match (case-insensitively,
// against the full error chain), and ordinary non-credential cluster failures
// must not.
func TestKubeExecRefresherNeedsRefresh(t *testing.T) {
	r := &kubeExecRefresher{}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"getting credentials", fmt.Errorf("getting credentials: exit status 1"), true},
		{"exec plugin", errors.New("error retrieving credential: exec plugin: failed to parse output"), true},
		// client-go's own exec-plugin wrapper phrasing (plugin/pkg/client/auth/exec),
		// distinct from stdlib os/exec's "exec: \"tsh\": executable file not found".
		{"exec: executable not found", errors.New("exec: executable tsh not found"), true},
		{"unauthorized", errors.New(`sandboxes.agent-sandbox.dev is forbidden: Unauthorized`), true},
		{"server asked for credentials", errors.New("the server has asked for the client to provide credentials"), true},
		{"certificate has expired", errors.New("x509: certificate has expired or is not yet valid"), true},
		{"credentials expired", errors.New("credentials expired, please re-authenticate"), true},
		{"access token command", errors.New("error executing access token command \"gcloud ...\": exit status 1"), true},
		{"case-insensitive match", errors.New("CERTIFICATE HAS EXPIRED"), true},

		{"deadline exceeded", errors.New("context deadline exceeded"), false},
		{"sandbox not found", errors.New("sandbox not found"), false},
		{"connection refused", errors.New("dial tcp 127.0.0.1:6443: connect: connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.NeedsRefresh(tc.err); got != tc.want {
				t.Errorf("NeedsRefresh(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// certKubeconfig is a minimal, valid kubeconfig whose current-context's user
// has a plain token credential — no `exec:` plugin.
const certKubeconfig = `apiVersion: v1
kind: Config
current-context: test-context
clusters:
- name: test-cluster
  cluster:
    server: https://example.invalid:6443
contexts:
- name: test-context
  context:
    cluster: test-cluster
    user: test-user
users:
- name: test-user
  user:
    token: fake-token
`

// execKubeconfig is a minimal, valid kubeconfig whose current-context's user
// authenticates via an `exec:` credential plugin (e.g. `tsh kube credentials`).
const execKubeconfig = `apiVersion: v1
kind: Config
current-context: test-context
clusters:
- name: test-cluster
  cluster:
    server: https://example.invalid:6443
contexts:
- name: test-context
  context:
    cluster: test-cluster
    user: test-user
users:
- name: test-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: tsh
      args:
      - kube
      - credentials
      - --kube-cluster=demo
      env:
      - name: TELEPORT_HOME
        value: /tmp/teleport-home
`

func writeTempKubeconfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestNewKubeExecRefresherNoExecPluginIsNil pins the documented contract: a
// plain cert/token kubeconfig (no exec: plugin on the current context's user)
// can't be interactively refreshed, so newKubeExecRefresher must return nil
// rather than a refresher with nothing to hand the terminal to.
func TestNewKubeExecRefresherNoExecPluginIsNil(t *testing.T) {
	t.Setenv("KUBECONFIG", writeTempKubeconfig(t, certKubeconfig))

	got := newKubeExecRefresher()
	if got != nil {
		t.Fatalf("newKubeExecRefresher() = %#v, want nil (no exec plugin)", got)
	}
}

// TestNewKubeExecRefresherWithExecPlugin is the positive counterpart: a
// kubeconfig whose current context's user has an exec: plugin must yield a
// usable refresher built from that ExecConfig.
func TestNewKubeExecRefresherWithExecPlugin(t *testing.T) {
	t.Setenv("KUBECONFIG", writeTempKubeconfig(t, execKubeconfig))

	got := newKubeExecRefresher()
	if got == nil {
		t.Fatal("newKubeExecRefresher() = nil, want a refresher for the exec-plugin kubeconfig")
	}
	r, ok := got.(*kubeExecRefresher)
	if !ok {
		t.Fatalf("newKubeExecRefresher() returned %T, want *kubeExecRefresher", got)
	}
	if r.exec.Command != "tsh" {
		t.Errorf("exec.Command = %q, want %q", r.exec.Command, "tsh")
	}
}

// TestNewKubeExecRefresherNoKubeconfigIsNil pins the fail-safe end: no
// reachable kubeconfig at all must also yield nil, not a panic or error.
func TestNewKubeExecRefresherNoKubeconfigIsNil(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "no-such-kubeconfig.yaml"))

	if got := newKubeExecRefresher(); got != nil {
		t.Fatalf("newKubeExecRefresher() = %#v, want nil (no kubeconfig)", got)
	}
}

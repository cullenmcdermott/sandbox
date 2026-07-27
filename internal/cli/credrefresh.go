package cli

// credrefresh.go builds the dashboard's CredentialRefresher (TODO §8 P1-0a)
// from the active kubeconfig's `exec:` credential plugin — e.g. Teleport's
// `tsh kube credentials`. Interactive exec plugins authenticate fine at CLI
// startup (client-go runs them lazily, before tea.NewProgram claims the
// terminal), but the dashboard makes live cluster calls for its whole
// lifetime, and a plugin's cert TTL is easily shorter than a left-open
// session. When that expiry is detected mid-session, the dashboard hands the
// terminal to the same plugin via tea.Exec (internal/tui/dashboard/credrefresh.go)
// and retries.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// kubeExecRefresher implements dashboard.CredentialRefresher against the
// active kubeconfig's `exec:` credential plugin.
type kubeExecRefresher struct {
	exec *clientcmdapi.ExecConfig
}

// newKubeExecRefresher builds the dashboard's credential refresher from the
// active kubeconfig's `exec:` credential plugin. Returns nil when the current
// context's user has no exec plugin (a plain cert/token kubeconfig can't be
// interactively refreshed, so there is nothing to hand the terminal to).
func newKubeExecRefresher() dashboard.CredentialRefresher {
	// Same kubeconfig resolution the rest of the CLI uses (see
	// loadAmbientKubeconfig in doctor.go): default loading rules (honoring
	// $KUBECONFIG / ~/.kube/config) with no path/context override — the CLI
	// doesn't expose --kubeconfig/--context flags today, so there is nothing
	// more specific to honor.
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	raw, err := cc.RawConfig()
	if err != nil {
		return nil
	}
	ctxName := raw.CurrentContext
	if ctxName == "" {
		return nil
	}
	kubeCtx, ok := raw.Contexts[ctxName]
	if !ok || kubeCtx == nil {
		return nil
	}
	authInfo, ok := raw.AuthInfos[kubeCtx.AuthInfo]
	if !ok || authInfo == nil || authInfo.Exec == nil {
		return nil
	}
	return &kubeExecRefresher{exec: authInfo.Exec}
}

// credExpiryMarkers are case-insensitive substrings matched against the FULL
// error chain (err.Error()) to conservatively recognize an expired-credential
// failure worth an interactive exec-plugin re-run. A false positive here
// suspends the dashboard's alt-screen program to run a subprocess for an
// unrelated error, so each entry is a signature actually produced by
// client-go or an exec plugin on expiry/auth failure — not a generic guess.
var credExpiryMarkers = []string{
	// client-go's wrapping when the exec plugin itself returns an error (covers
	// most plugin-side failures, including tsh's own "certificate has expired").
	"getting credentials",
	// client-go's error when the exec plugin's ExecCredential response can't be
	// used (e.g. a stale/expired credential the plugin returned anyway).
	"exec plugin",
	// client-go's own exec-plugin wrapper (plugin/pkg/client/auth/exec) when the
	// plugin binary itself can't be found/started or exits non-zero — still
	// worth a re-run since a re-auth may (re)place it on PATH.
	"exec: executable",
	// the API server's 401 once the ambient credential the plugin produced has
	// actually expired against the cluster.
	"unauthorized",
	// the API server's more verbose 401 body for the same condition.
	"the server has asked for the client to provide credentials",
	// a certificate-based credential (client cert or the exec plugin's cached
	// one) past its NotAfter.
	"certificate has expired",
	// a generic phrasing some exec plugins (and client-go wrappers) use.
	"credentials expired",
	// gcloud's / other cloud CLIs' access-token exec plugin failure wrapping.
	"error executing access token command",
}

// NeedsRefresh reports whether err's full error chain matches one of
// credExpiryMarkers, case-insensitively. It returns false for a nil error.
func (r *kubeExecRefresher) NeedsRefresh(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range credExpiryMarkers {
		if strings.Contains(msg, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// Refresh builds the exec-plugin command from the kubeconfig's ExecConfig.
// Stdin and stderr are left unset so tea.Exec/kubeExecCmd wire them to the
// real terminal (the plugin's own interactive prompt); stdout is discarded so
// the plugin's ExecCredential JSON blob never paints the terminal during the
// handover. finalize always returns nil: there is nothing to parse here —
// client-go re-runs the plugin on the very next cluster request and picks up
// the freshly-refreshed ambient session (kubeconfig-cached token/cert file,
// keyring entry, etc.) that the plugin itself just wrote.
func (r *kubeExecRefresher) Refresh() (tea.ExecCommand, func() error) {
	// exec.Command defers PATH lookup to Start, so a missing plugin binary
	// surfaces as a Run error routed to credRefreshDoneMsg, not a panic.
	cmd := exec.Command(r.exec.Command, r.exec.Args...)
	env := os.Environ()
	for _, e := range r.exec.Env {
		env = append(env, fmt.Sprintf("%s=%s", e.Name, e.Value))
	}
	cmd.Env = env
	cmd.Stdout = io.Discard
	finalize := func() error { return nil }
	return &kubeExecCmd{cmd: cmd}, finalize
}

// kubeExecCmd adapts an *exec.Cmd to tea.ExecCommand. It exists only because
// bubbletea v2's own adapter (wrapExecCommand in exec.go) is unexported, so a
// plain *exec.Cmd — which has Stdin/Stdout/Stderr fields, not
// SetStdin/SetStdout/SetStderr methods — cannot satisfy the interface
// directly. Its Set* methods reproduce that unexported adapter's exact
// fill-only-if-unset semantics (confirmed by reading bubbletea's exec.go):
// SetStdout is called unconditionally by tea.Program.exec, but since Refresh
// above already sets cmd.Stdout to io.Discard, the nil-check here leaves it
// alone — this is what keeps the plugin's ExecCredential JSON off the
// terminal during the handover.
type kubeExecCmd struct{ cmd *exec.Cmd }

func (c *kubeExecCmd) Run() error { return c.cmd.Run() }

func (c *kubeExecCmd) SetStdin(r io.Reader) {
	if c.cmd.Stdin == nil {
		c.cmd.Stdin = r
	}
}

func (c *kubeExecCmd) SetStdout(w io.Writer) {
	if c.cmd.Stdout == nil {
		c.cmd.Stdout = w
	}
}

func (c *kubeExecCmd) SetStderr(w io.Writer) {
	if c.cmd.Stderr == nil {
		c.cmd.Stderr = w
	}
}

// compile-time: kubeExecRefresher satisfies the dashboard interface.
var _ dashboard.CredentialRefresher = (*kubeExecRefresher)(nil)

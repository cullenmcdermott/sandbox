package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/cred"
)

// newAuthCmd builds `sandbox auth`, which inspects the credentials the CLI uses
// to authenticate each supported agent (and the cluster connection) and manages
// the local multi-account Anthropic store. `status` is the read-side red/green
// readout; `login`/`list`/`logout`/`default` are the write side — a local
// Keychain-backed (file-fallback) store of Anthropic accounts that
// `sandbox claude --account` and the TUI account picker draw from.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect agent credentials and manage Anthropic accounts",
		Long: "Validate the auth configured for each supported agent (Claude / Codex /\n" +
			"OpenCode) and the cluster connection, with a red/green readout, and manage the\n" +
			"local multi-account Anthropic store (login / list / logout / default). Status\n" +
			"checks are cheap and offline; no secrets are ever printed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runAuthStatus(cmd) },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show configured auth per agent + cluster connectivity",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runAuthStatus(cmd) },
	})
	cmd.AddCommand(newAuthLoginCmd())
	cmd.AddCommand(newAuthListCmd())
	cmd.AddCommand(newAuthLogoutCmd())
	cmd.AddCommand(newAuthDefaultCmd())
	return cmd
}

func runAuthStatus(cmd *cobra.Command) error {
	home, _ := os.UserHomeDir()
	agents := cred.Report(cmd.Context(), cred.DefaultProviders(os.Getenv, home)...)
	renderAuthStatus(cmd.OutOrStdout(), probeCluster(cmd.Context()), agents)
	return nil
}

// clusterStatus is the cluster-connection readout (the one inherently-live check).
type clusterStatus struct {
	reachable bool
	host      string
	namespace string
	detail    string // error summary when unreachable
}

// probeCluster builds the backend from kubeconfig and does a short /healthz ping.
func probeCluster(ctx context.Context) clusterStatus {
	cs := clusterStatus{namespace: namespaceFlag}
	backend, err := newBackend()
	if err != nil {
		cs.detail = truncate(err.Error(), 140)
		return cs
	}
	cs.host = backend.Host()
	if cs.namespace == "" {
		cs.namespace = backend.Namespace()
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := backend.Ping(pingCtx); err != nil {
		cs.detail = truncate(err.Error(), 140)
		return cs
	}
	cs.reachable = true
	return cs
}

var (
	dotOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	dotWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	dotBad  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dimText = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func dot(l cred.Level) string {
	switch l {
	case cred.LevelOK:
		return dotOK.Render("●")
	case cred.LevelWarn:
		return dotWarn.Render("●")
	default:
		return dotBad.Render("●")
	}
}

// renderAuthStatus writes the red/green readout. Pure (no network) for testing.
func renderAuthStatus(w io.Writer, cs clusterStatus, agents []cred.Status) {
	fmt.Fprintf(w, "\n  auth status\n\n")

	lvl, state, detail := cred.LevelBad, "unreachable", cs.detail
	if cs.reachable {
		lvl, state = cred.LevelOK, "reachable"
		detail = "ns: " + cs.namespace
		if cs.host != "" {
			detail += "  ·  " + cs.host
		}
	}
	fmt.Fprintf(w, "  %s %-12s %-20s %s\n", dot(lvl), "kubernetes", state, dimText.Render(detail))

	for _, s := range agents {
		fmt.Fprintf(w, "  %s %-12s %-20s %s\n", dot(s.Level()), s.Name, string(s.Method), dimText.Render(s.Detail))
		for _, sub := range s.Sub {
			label := strings.TrimPrefix(sub.Name, s.Name+"/")
			fmt.Fprintf(w, "      %s %-12s %s\n", dot(sub.Level()), label, dimText.Render(sub.Detail))
		}
	}
	fmt.Fprintln(w)
}

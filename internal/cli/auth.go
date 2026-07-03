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

	"github.com/cullenmcdermott/sandbox/client/cred"
	"github.com/cullenmcdermott/sandbox/internal/authstatus"
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
	agents := authstatus.Report(cmd.Context(), authstatus.DefaultProviders(os.Getenv, home)...)

	// Stored Anthropic accounts are part of the readout (metadata only — the
	// status invariant that no secret bytes are ever read holds: List/Default
	// touch just the manifest). A store failure degrades to a warning line.
	var accounts []cred.Account
	var def string
	store, storeErr := newCredStore()
	if storeErr == nil {
		accounts, storeErr = store.List()
		def, _ = store.Default()
	}

	renderAuthStatus(cmd.OutOrStdout(), probeCluster(cmd.Context()), agents, accounts, def, storeErr)
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

func dot(l authstatus.Level) string {
	switch l {
	case authstatus.LevelOK:
		return dotOK.Render("●")
	case authstatus.LevelWarn:
		return dotWarn.Render("●")
	default:
		return dotBad.Render("●")
	}
}

// renderAuthStatus writes the red/green readout. Pure (no network) for testing.
// accounts/def/storeErr describe the local Anthropic account store (metadata
// only): a non-nil storeErr renders a warning line instead of the list.
func renderAuthStatus(w io.Writer, cs clusterStatus, agents []authstatus.Status, accounts []cred.Account, def string, storeErr error) {
	fmt.Fprintf(w, "\n  auth status\n\n")

	lvl, state, detail := authstatus.LevelBad, "unreachable", cs.detail
	if cs.reachable {
		lvl, state = authstatus.LevelOK, "reachable"
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

	fmt.Fprintf(w, "\n  anthropic accounts\n")
	switch {
	case storeErr != nil:
		fmt.Fprintf(w, "  %s %s\n", dot(authstatus.LevelWarn), dimText.Render(truncate("account store unreadable: "+storeErr.Error(), 140)))
	case len(accounts) == 0:
		fmt.Fprintf(w, "    %s\n", dimText.Render("none stored — add one with `sandbox auth login`"))
	default:
		for _, a := range accounts {
			marker := ""
			if a.ID == def {
				marker = "(default)"
			}
			fmt.Fprintf(w, "  %s %-18s %-14s %-8s %s\n", dot(authstatus.LevelOK), a.Label, string(a.Type), humanAge(a.CreatedAt), dimText.Render(marker))
		}
	}
	fmt.Fprintln(w)
}

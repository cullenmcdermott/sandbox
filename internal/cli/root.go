// Package cli implements the sandbox CLI's command tree: it creates and manages
// Kubernetes-backed remote agent sessions (Claude Agent SDK / OpenCode) and
// drives the local Bubble Tea dashboard that attaches to them.
//
// The CLI is a thin consumer of the public client package: session create /
// connect / turn / stream / sync all go through github.com/cullenmcdermott/sandbox/client,
// the same API an external Go program uses, so there is no parallel session engine.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// namespaceFlag is bound to the root --namespace persistent flag and threaded
// into every backend/client so all commands address the same namespace.
var namespaceFlag string

// Version is the CLI version, reported by `sandbox --version`. Override at build
// time with -ldflags "-X github.com/cullenmcdermott/sandbox/internal/cli.Version=v1.2.3".
var Version = "dev"

// NewRoot builds the remote-sandbox CLI command tree.
func NewRoot() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Run AI coding agents in remote Kubernetes sessions",
		Long: "sandbox runs AI coding agents (Claude Agent SDK / OpenCode) in remote\n" +
			"Kubernetes sessions with PVC persistence, file sync, and a local TUI.\n\n" +
			"Run `sandbox` with no arguments to open the command-center dashboard.",
		Example: "  sandbox                       # open the dashboard\n" +
			"  sandbox claude \"fix the build\"  # start a session and prompt it\n" +
			"  sandbox status                # list sessions\n" +
			"  sandbox attach <id>           # reconnect to a session",
		Version: Version,
		// SilenceErrors/SilenceUsage: main.go owns the single "sandbox: <err>"
		// print, so cobra must not also print the error or dump the full usage
		// blob after a runtime failure (a dropped port-forward is not a usage
		// error). Propagates to subcommands.
		SilenceErrors: true,
		SilenceUsage:  true,
		// PersistentPreRun configures structured debug logging once --debug (or
		// SANDBOX_DEBUG) is parsed, before any subcommand runs.
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			configureDebugLogging()
			// Keep client-go's port-forward error spew (klog → stderr) off the
			// terminal so it can't corrupt the dashboard's alt-screen.
			silenceKubernetesLogging()
		},
		// RunE is set so that bare `sandbox` (with no subcommand) launches the
		// command-center dashboard instead of printing help.
		RunE: func(cmd *cobra.Command, args []string) error {
			c, backend, err := newClientAndBackend()
			if err != nil {
				return err
			}
			// The connectors/creator/hooks bridge the public client package into
			// the dashboard.Connector/Creator types, so the dashboard imports
			// neither cli nor client.
			connector := newDashboardConnector(c, "")
			creator := newDashboardCreator(c, "", "")
			return afterTUI(func() error {
				return dashboard.Run(backend, connector, creator, dashboard.RunOptions{
					DestroyHook:       newLocalDestroyHook(c),
					PreDestroyHook:    newPreDestroySyncStop(c),
					TitleStore:        indexTitleStore{},
					SnapshotStore:     indexSnapshotStore{},
					EventCache:        indexEventCache{},
					ObserverConnector: newDashboardObserverConnector(c, ""),
					SyncProber:        dashboardSyncProber(),
					SyncReaper:        dashboardSyncReaper(),
					IdleTimeout:       defaultReaperIdleTimeout,
				})
			})
		},
	}
	cmd.PersistentFlags().StringVarP(&namespaceFlag, "namespace", "n", "", "Kubernetes namespace (default: agent-sessions)")
	cmd.PersistentFlags().BoolVar(&debugEnabled, "debug", false, "emit structured JSON-line debug logs to stderr (see docs/runner-api.md)")
	cmd.AddCommand(newClaudeRemoteCmd())
	cmd.AddCommand(newOpencodeCmd())
	cmd.AddCommand(newAttachCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newSyncCmd())
	cmd.AddCommand(newSuspendCmd())
	cmd.AddCommand(newResumeCmd())
	cmd.AddCommand(newCancelCmd())
	cmd.AddCommand(newTurnCmd())
	cmd.AddCommand(newDestroyCmd())
	cmd.AddCommand(newShellCmd())
	cmd.AddCommand(newRenameCmd())
	cmd.AddCommand(newReapCmd())
	cmd.AddCommand(newTraceCmd())
	cmd.AddCommand(newAuthCmd())
	return cmd
}

// Execute runs the CLI.
func Execute() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return NewRoot().ExecuteContext(ctx)
}

// newClient builds a public sandbox client scoped to the namespace from the root
// --namespace flag (empty means the default namespace). It is the single entry
// point the CLI uses to create/connect/manage sessions.
func newClient(opts ...client.Option) (*client.Client, error) {
	base := []client.Option{client.WithNamespace(namespaceFlag)}
	c, err := client.New(append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cluster: %w", err)
	}
	return c, nil
}

// newBackend creates a k8s Backend scoped to the root --namespace flag. Used by
// the in-cluster reaper and the read-only trace path that don't need the full
// client.
func newBackend() (*k8s.Backend, error) {
	b, err := k8s.New(namespaceFlag)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cluster: %w", err)
	}
	return b, nil
}

// newClientAndBackend builds a client plus the *k8s.Backend it wraps, sharing the
// single backend instance. The dashboard commands need the concrete *k8s.Backend
// for dashboard.Run/RunAttached (the dashboard takes the concrete type); the
// client drives everything else. Keeping the public client façade free of a raw
// Backend() accessor is why the CLI builds and shares the backend explicitly.
func newClientAndBackend(opts ...client.Option) (*client.Client, *k8s.Backend, error) {
	b, err := newBackend()
	if err != nil {
		return nil, nil, err
	}
	c, err := client.New(append([]client.Option{client.WithBackend(b)}, opts...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to cluster: %w", err)
	}
	return c, b, nil
}

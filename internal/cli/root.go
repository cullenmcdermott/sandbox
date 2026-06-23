// Package cli implements the sandbox CLI's command tree: it creates and manages
// Kubernetes-backed remote agent sessions (Claude Agent SDK / OpenCode) and
// drives the local Bubble Tea dashboard that attaches to them.
package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// namespaceFlag is bound to the root --namespace persistent flag and threaded
// into every backend so all commands address the same namespace consistently.
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
			backend, err := newBackend()
			if err != nil {
				return fmt.Errorf("failed to connect to cluster: %w", err)
			}
			// Build a connector adapter that bridges sessionConnector (which
			// lives here in internal/cli) into the dashboard.Connector type.
			// This avoids any import cycle: dashboard imports nothing from cli.
			connector := newDashboardConnector(backend, "")
			creator := newDashboardCreator(backend, "", "")
			return afterTUI(func() error {
				return dashboard.Run(backend, connector, creator, dashboard.RunOptions{
					DestroyHook:    newLocalDestroyHook(),
					PreDestroyHook: newPreDestroySyncStop(),
					TitleStore:     indexTitleStore{},
					SnapshotStore:  indexSnapshotStore{},
					SyncProber:     dashboardSyncProber(),
					IdleTimeout:    defaultReaperIdleTimeout,
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
	cmd.AddCommand(newDestroyCmd())
	cmd.AddCommand(newShellCmd())
	cmd.AddCommand(newRenameCmd())
	cmd.AddCommand(newReapCmd())
	cmd.AddCommand(newTraceCmd())
	return cmd
}

// Execute runs the CLI.
func Execute() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return NewRoot().ExecuteContext(ctx)
}

// newBackend creates a k8s Backend from the default kubeconfig, scoped to the
// namespace from the root --namespace flag (empty means the backend default).
func newBackend() (*k8s.Backend, error) {
	b, err := k8s.New(namespaceFlag)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cluster: %w", err)
	}
	return b, nil
}

// newSessionID generates a fresh, unique session ID for a new session. Each
// invocation gets its own pod and PVC: the ID combines the backend name, a
// short hash of the project path (so sessions are still grouped by project at a
// glance), and a random suffix that guarantees two sessions never collide —
// even from the same directory. Reconnecting to an existing session is done by
// explicit ID via `attach`, not by re-deriving from the path. The result is a
// valid Kubernetes DNS label.
func newSessionID(backend, projectPath string) (session.ID, error) {
	sum := sha256.Sum256([]byte(projectPath))
	pathHash := hex.EncodeToString(sum[:])[:6]

	rnd := make([]byte, 4)
	if _, err := rand.Read(rnd); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	suffix := hex.EncodeToString(rnd)

	return session.ID(sanitizeLabel(backend) + "-" + pathHash + "-" + suffix), nil
}

// sanitizeLabel lowercases and replaces any non-[a-z0-9-] rune with '-' so the
// value is safe to use in a Kubernetes resource name.
func sanitizeLabel(s string) string {
	b := make([]byte, 0, len(s))
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z':
			b = append(b, byte(c-'A'+'a'))
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-':
			b = append(b, byte(c))
		default:
			b = append(b, '-')
		}
	}
	return string(b)
}

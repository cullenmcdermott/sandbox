package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// turnStateClient is the subset of *runner.Client that the suspend/cancel
// commands need. The active turn id lives in the runner process, NOT in the
// Sandbox CRD that k8s.Backend.Status reads (NEW-9), so these commands must ask
// the runner directly. Defined as an interface so the logic is unit-testable
// with a fake (a real *runner.Client satisfies it).
type turnStateClient interface {
	SessionState(ctx context.Context, ref session.Ref) (session.State, error)
	InterruptTurn(ctx context.Context, ref session.Ref, turn session.TurnRef) error
}

// runnerClientFor port-forwards to the session's runner pod and returns a
// connected client plus a cleanup func that tears the forward down.
func runnerClientFor(ctx context.Context, backend *k8s.Backend, ref session.Ref) (*runner.Client, func(), error) {
	handles, err := backend.PortForward(ctx, ref, k8s.ForwardSpecs(0, 0))
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		for _, h := range handles {
			h.Close()
		}
	}
	token, err := backend.RunnerToken(ctx, ref)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("get runner token: %w", err)
	}
	client := runner.New(fmt.Sprintf("http://127.0.0.1:%d", handles[0].LocalPort()), token)
	return client, cleanup, nil
}

// cancelActiveTurn reads the live turn id from the runner and interrupts it,
// erroring if there is no active turn. Split out from newCancelCmd so it can be
// tested without a cluster (NEW-9).
func cancelActiveTurn(ctx context.Context, client turnStateClient, ref session.Ref) error {
	st, err := client.SessionState(ctx, ref)
	if err != nil {
		return err
	}
	if st.LastTurnID == "" {
		return fmt.Errorf("no active turn in session %s", ref.ID)
	}
	return client.InterruptTurn(ctx, ref, session.TurnRef{Session: ref.ID, Turn: st.LastTurnID})
}

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <session-id>",
		Short: "Reconnect to an existing remote session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			backend, err := newBackend()
			if err != nil {
				return err
			}
			ref := session.Ref{ID: session.ID(args[0])}

			// Resolve the project path for the sync/display before connecting.
			st, err := backend.Status(ctx, ref)
			if err != nil {
				return err
			}
			if st.Status == session.StatusGone {
				return fmt.Errorf("session %s does not exist", ref.ID)
			}

			// Launch the command center attached to this session. The dashboard
			// Connector handles resume-if-suspended, port-forward, health, sync,
			// and reaper, and doubles as the transcript's reconnect callback; the
			// list loads underneath so esc detaches to it.
			return afterTUI(func() error {
				return dashboard.RunAttached(
					backend,
					newDashboardConnector(backend, ""),
					newDashboardCreator(backend, "", ""),
					dashboard.SessionFromState(st),
					"",
					dashboard.RunOptions{DestroyHook: newLocalDestroyHook(), PreDestroyHook: newPreDestroySyncStop(), TitleStore: indexTitleStore{}, SnapshotStore: indexSnapshotStore{}, ObserverConnector: newDashboardObserverConnector(backend, ""), SyncProber: dashboardSyncProber(), IdleTimeout: defaultReaperIdleTimeout},
				)
			})
		},
	}
}

func newSuspendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "suspend <session-id>",
		Short: "Suspend a remote session (terminate pod, keep PVC)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			backend, err := newBackend()
			if err != nil {
				return err
			}
			ref := session.Ref{ID: session.ID(args[0])}
			// C6 / NEW-9: warn if a turn is in flight. The active turn id lives in
			// the runner (not the Sandbox CRD), so ask the runner directly. This is
			// best-effort — any failure (unreachable / already suspended) is
			// non-fatal; the 60s SIGTERM grace period is the real safeguard.
			if client, cleanup, err := runnerClientFor(ctx, backend, ref); err == nil {
				st, sErr := client.SessionState(ctx, ref)
				cleanup()
				if sErr == nil && st.LastTurnID != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: session %q has an active turn (%s); it will be interrupted by the SIGTERM flush\n", args[0], st.LastTurnID)
				}
			}
			if err := backend.Suspend(ctx, ref); err != nil {
				return err
			}
			// Pause sync: the pod (and its SSH port-forward) is gone while
			// suspended, so leaving sync running would thrash against a dead
			// transport. Best-effort — resume re-enables it.
			_ = syncManager().PauseAll(ctx, args[0])
			fmt.Fprintf(cmd.OutOrStdout(), "Suspended session %q (pod terminated, PVC kept).\n", args[0])
			return nil
		},
	}
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <session-id>",
		Short: "Resume a suspended remote session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := newBackend()
			if err != nil {
				return err
			}
			if err := backend.Resume(cmd.Context(), session.Ref{ID: session.ID(args[0])}); err != nil {
				return err
			}
			// Resume sync paused at suspend time (best-effort). The next attach
			// re-establishes the port-forward the sync sessions ride on.
			_ = syncManager().ResumeAll(cmd.Context(), args[0])
			fmt.Fprintf(cmd.OutOrStdout(), "Resumed session %q.\n", args[0])
			return nil
		},
	}
}

func newCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <session-id>",
		Short: "Cancel the active turn in a remote session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			backend, err := newBackend()
			if err != nil {
				return err
			}
			ref := session.Ref{ID: session.ID(args[0])}

			// NEW-9: the active turn id lives in the runner, not the Sandbox CRD,
			// so port-forward to the runner and read it from there. Reading it from
			// backend.Status (CRD-only) always saw "" → cancel could never run.
			// M9: bound the whole operation so a stalled port-forward/health check
			// can't hang the command indefinitely.
			opCtx, opCancel := context.WithTimeout(ctx, 15*time.Second)
			defer opCancel()
			client, cleanup, err := runnerClientFor(opCtx, backend, ref)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := cancelActiveTurn(opCtx, client, ref); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Interrupted the active turn in session %q.\n", args[0])
			return nil
		},
	}
}

// confirmDestroy implements the destroy command's C4 confirmation gate: it
// prompts on out, reads a single token from in, and reports whether the user
// explicitly approved (only "y"/"Y" proceeds; any other answer, or empty input
// / EOF, denies). Split out so the gate is unit-testable without a cluster.
func confirmDestroy(in io.Reader, out io.Writer, id string) bool {
	fmt.Fprintf(out, "This will permanently destroy session %q and its PVC. This cannot be undone.\nContinue? [y/N]: ", id)
	var answer string
	_, _ = fmt.Fscan(in, &answer)
	if answer != "y" && answer != "Y" {
		fmt.Fprintln(out, "Cancelled.")
		return false
	}
	return true
}

func newDestroyCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "destroy <session-id>",
		Short: "Destroy a remote session and its PVC (irreversible)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id := args[0]
			// C4: require confirmation unless --force is passed.
			if !force && !confirmDestroy(cmd.InOrStdin(), cmd.OutOrStdout(), id) {
				return nil
			}
			backend, err := newBackend()
			if err != nil {
				return err
			}
			if err := backend.Destroy(ctx, session.Ref{ID: session.ID(id)}); err != nil {
				return err
			}
			// Best-effort local cleanup: sync sessions, SSH alias, and key dir.
			// Only runs after the cluster teardown above succeeded.
			_ = syncManager().TerminateAll(ctx, id)
			if cfg, err := sshConfigManager(); err == nil {
				_ = cfg.Remove(id)
			}
			if dir, err := sessionKeyDir(id); err == nil {
				_ = os.RemoveAll(dir)
			}
			// Drop the local index entry so destroyed sessions don't linger in
			// `status --all` forever.
			if idx, ierr := newIndex(); ierr == nil {
				_ = idx.Delete(id)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Destroyed session %q and its PVC.\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	return cmd
}

func newStatusCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "List remote sessions and their status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			backend, err := newBackend()
			if err != nil {
				return err
			}
			sessions, err := backend.List(ctx)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			rows := 0
			printRow := func(id, status, pod, project, activity string) {
				if rows == 0 {
					fmt.Fprintf(out, "%-28s %-12s %-7s %-30s %s\n", "SESSION", "STATUS", "POD", "PROJECT", "LAST ACTIVITY")
				}
				rows++
				fmt.Fprintf(out, "%-28s %-12s %-7s %-30s %s\n", id, status, pod, project, activity)
			}

			seen := make(map[session.ID]bool, len(sessions))
			for _, st := range sessions {
				seen[st.ID] = true
				printRow(string(st.ID), string(st.Status), podLabel(st.PodReady), st.ProjectPath, fmtTime(st.LastActivity))
			}

			// With --all, surface sessions the local index knows about but that
			// are no longer present in the cluster (destroyed/expired).
			if all {
				if idx, ierr := newIndex(); ierr == nil {
					entries, _ := idx.List()
					for _, e := range entries {
						if seen[session.ID(e.SandboxSessionID)] {
							continue
						}
						printRow(e.SandboxSessionID, string(session.StatusGone), podLabel(false), e.ProjectPath, fmtTime(e.LastActivity))
					}
				}
			}

			if rows == 0 {
				fmt.Fprintln(out, "No remote sessions.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include sessions known locally but gone from the cluster")
	return cmd
}

// fmtTime renders a timestamp for the status table, blanking the zero value.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04")
}

// podLabel renders the pod-ready column as a readable word instead of a raw Go
// boolean (true/false) in the status table.
func podLabel(ready bool) string {
	if ready {
		return "ready"
	}
	return "-"
}

func newSyncCmd() *cobra.Command {
	var pause, resume, terminate bool
	cmd := &cobra.Command{
		Use:   "sync <session-id>",
		Short: "Manage Mutagen file sync for a remote session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id := args[0]
			mgr := syncManager()
			switch {
			case pause:
				return mgr.PauseAll(ctx, id)
			case resume:
				return mgr.ResumeAll(ctx, id)
			case terminate:
				if err := mgr.TerminateAll(ctx, id); err != nil {
					return err
				}
				if cfg, err := sshConfigManager(); err == nil {
					_ = cfg.Remove(id)
				}
				return nil
			default:
				out, err := mgr.Status(ctx, id)
				if err != nil {
					return err
				}
				fmt.Fprint(cmd.OutOrStdout(), string(out))
				return nil
			}
		},
	}
	cmd.Flags().BoolVar(&pause, "pause", false, "pause sync sessions")
	cmd.Flags().BoolVar(&resume, "resume", false, "resume sync sessions")
	cmd.Flags().BoolVar(&terminate, "terminate", false, "terminate sync sessions and remove the SSH alias")
	cmd.MarkFlagsMutuallyExclusive("pause", "resume", "terminate")
	return cmd
}

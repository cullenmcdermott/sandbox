package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui"
)

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

			// Ensure the session is running.
			st, err := backend.Status(ctx, ref)
			if err != nil {
				return err
			}
			if st.Status == session.StatusSuspended {
				if err := backend.Resume(ctx, ref); err != nil {
					return fmt.Errorf("resume session: %w", err)
				}
			} else if st.Status == session.StatusGone {
				return fmt.Errorf("session %s does not exist", ref.ID)
			}

			// Port-forward.
			handles, err := backend.PortForward(ctx, ref, k8s.ForwardSpecs(0, 0))
			if err != nil {
				return fmt.Errorf("port-forward: %w", err)
			}
			defer func() {
				for _, h := range handles {
					h.Close()
				}
			}()

			token, err := backend.RunnerToken(ctx, ref)
			if err != nil {
				return fmt.Errorf("get runner token: %w", err)
			}
			client := runner.New(fmt.Sprintf("http://127.0.0.1:%d", handles[0].LocalPort()), token)
			if err := client.Health(ctx); err != nil {
				return fmt.Errorf("runner health: %w", err)
			}

			// Re-establish file sync over the new SSH port-forward.
			if privPath, _, kerr := ensureSSHKey(string(ref.ID)); kerr == nil {
				if serr := startMutagen(ctx, string(ref.ID), st.ProjectPath, privPath, handles[1].LocalPort()); serr != nil {
					fmt.Fprintf(os.Stderr, "warning: file sync resume failed: %v\n", serr)
				}
			}

			// Attach TUI with replay (after=0 gets full history).
			model := tui.NewModel(client, ref, st.ProjectPath, "")
			return tui.Run(model)
		},
	}
}

func newSuspendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "suspend <session-id>",
		Short: "Suspend a remote session (terminate pod, keep PVC)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, err := newBackend()
			if err != nil {
				return err
			}
			return backend.Suspend(cmd.Context(), session.Ref{ID: session.ID(args[0])})
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
			return backend.Resume(cmd.Context(), session.Ref{ID: session.ID(args[0])})
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

			st, err := backend.Status(ctx, ref)
			if err != nil {
				return err
			}
			if st.LastTurnID == "" {
				return fmt.Errorf("no active turn in session %s", ref.ID)
			}

			handles, err := backend.PortForward(ctx, ref, k8s.ForwardSpecs(0, 0))
			if err != nil {
				return err
			}
			defer func() {
				for _, h := range handles {
					h.Close()
				}
			}()

			token, err := backend.RunnerToken(ctx, ref)
			if err != nil {
				return fmt.Errorf("get runner token: %w", err)
			}
			client := runner.New(fmt.Sprintf("http://127.0.0.1:%d", handles[0].LocalPort()), token)
			return client.InterruptTurn(ctx, ref, session.TurnRef{Session: ref.ID, Turn: st.LastTurnID})
		},
	}
}

func newDestroyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "destroy <session-id>",
		Short: "Destroy a remote session and its PVC (irreversible)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id := args[0]
			backend, err := newBackend()
			if err != nil {
				return err
			}
			if err := backend.Destroy(ctx, session.Ref{ID: session.ID(id)}); err != nil {
				return err
			}
			// Best-effort local cleanup: sync sessions, SSH alias, and key dir.
			_ = syncManager().TerminateAll(ctx, id)
			if cfg, err := sshConfigManager(); err == nil {
				_ = cfg.Remove(id)
			}
			if dir, err := sessionKeyDir(id); err == nil {
				_ = os.RemoveAll(dir)
			}
			return nil
		},
	}
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
			printRow := func(id, status string, podReady bool, project, activity string) {
				if rows == 0 {
					fmt.Fprintf(out, "%-20s %-12s %-8s %-30s %s\n", "SESSION", "STATUS", "POD", "PROJECT", "LAST ACTIVITY")
				}
				rows++
				fmt.Fprintf(out, "%-20s %-12s %-8t %-30s %s\n", id, status, podReady, project, activity)
			}

			seen := make(map[session.ID]bool, len(sessions))
			for _, st := range sessions {
				seen[st.ID] = true
				printRow(string(st.ID), string(st.Status), st.PodReady, st.ProjectPath, fmtTime(st.LastActivity))
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
						printRow(e.SandboxSessionID, string(session.StatusGone), false, e.ProjectPath, fmtTime(e.LastActivity))
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

package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/session"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// turnStateClient is the subset of *runner.Client that the suspend/cancel
// commands need. The active turn id lives in the runner process, NOT in the
// Sandbox CRD that k8s.Backend.Status reads, so these commands must ask the
// runner directly. Defined as an interface so the logic is unit-testable with a
// fake (a real *runner.Client satisfies it).
type turnStateClient interface {
	SessionState(ctx context.Context, ref session.Ref) (session.State, error)
	InterruptTurn(ctx context.Context, ref session.Ref, turn session.TurnRef) error
}

// cancelActiveTurn reads the live turn id from the runner and interrupts it,
// erroring if there is no active turn. Split out from newCancelCmd so it can be
// tested without a cluster.
func cancelActiveTurn(ctx context.Context, client turnStateClient, ref session.Ref) error {
	st, err := client.SessionState(ctx, ref)
	if err != nil {
		return err
	}
	if st.ActiveTurnID == "" {
		return fmt.Errorf("no active turn in session %s", ref.ID)
	}
	return client.InterruptTurn(ctx, ref, session.TurnRef{Session: ref.ID, Turn: st.ActiveTurnID})
}

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <session-id>",
		Short: "Reconnect to an existing remote session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, backend, err := newClientAndBackend()
			if err != nil {
				return err
			}
			id := session.ID(args[0])

			// Resolve the project path / display state before connecting.
			st, err := c.Status(ctx, id)
			if err != nil {
				return err
			}
			if st.Status == session.StatusGone {
				return fmt.Errorf("session %s does not exist", id)
			}

			// Best-effort orphan-sync GC at startup (SF1): clean up syncs left by
			// destroyed/reaped pods now rather than waiting for the first in-TUI
			// reconcile. Backgrounded so it never delays the attach.
			startupSyncGC()

			// Launch the command center attached to this session. The dashboard
			// Connector (driven by the client package) handles resume-if-suspended,
			// port-forward, health, sync, and reaper, and doubles as the
			// transcript's reconnect callback; the list loads underneath so esc
			// detaches to it.
			return afterTUI(func() error {
				return dashboard.RunAttached(
					backend,
					newDashboardConnector(c, ""),
					newDashboardCreator(c, "", ""),
					dashboard.SessionFromState(st),
					"",
					dashboard.RunOptions{DestroyHook: newLocalDestroyHook(c), PreDestroyHook: newPreDestroySyncStop(c), TitleStore: indexTitleStore{}, SnapshotStore: indexSnapshotStore{}, EventCache: newIndexEventCache(), DriverStore: indexDriverStore{}, ObserverConnector: newDashboardObserverConnector(c, ""), SyncProber: dashboardSyncProber(), SyncReaper: dashboardSyncReaper(), IdleTimeout: defaultReaperIdleTimeout, AccountStore: newDashboardAccountStore(), WorktreeOps: newWorktreeOps(c)},
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
			c, err := newClient()
			if err != nil {
				return err
			}
			ref := session.Ref{ID: session.ID(args[0])}
			// Warn if a turn is in flight. The active turn id lives in the runner
			// (not the Sandbox CRD), so ask the runner directly. Best-effort — any
			// failure (unreachable / already suspended) is non-fatal; the 60s
			// SIGTERM grace period is the real safeguard. Bounded like destroy's
			// probe (C9): the raw command ctx let a half-dead node stall the
			// suspend ~40s on port-forward + HTTP client timeouts.
			probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
			if rc, cleanup, err := c.DialRunner(probeCtx, ref); err == nil {
				st, sErr := rc.SessionState(probeCtx, ref)
				cleanup()
				if sErr == nil && st.ActiveTurnID != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: session %q has an active turn (%s); it will be interrupted by the SIGTERM flush\n", args[0], st.ActiveTurnID)
				}
			}
			probeCancel()
			// Suspend pauses file sync as part of the lifecycle (the pod's SSH
			// forward is gone while suspended).
			if err := c.Suspend(ctx, session.ID(args[0])); err != nil {
				return err
			}
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
			c, err := newClient()
			if err != nil {
				return err
			}
			// Resume re-enables file sync paused at suspend time; the next attach
			// re-establishes the port-forward the sync sessions ride on.
			if err := c.Resume(cmd.Context(), session.ID(args[0])); err != nil {
				return err
			}
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
			c, err := newClient()
			if err != nil {
				return err
			}
			ref := session.Ref{ID: session.ID(args[0])}

			// The active turn id lives in the runner, not the Sandbox CRD, so dial
			// the runner and read it from there. Bound the whole operation so a
			// stalled port-forward/health check can't hang the command.
			opCtx, opCancel := context.WithTimeout(ctx, 15*time.Second)
			defer opCancel()
			rc, cleanup, err := c.DialRunner(opCtx, ref)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := cancelActiveTurn(opCtx, rc, ref); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Interrupted the active turn in session %q.\n", args[0])
			return nil
		},
	}
}

// confirmDestroy implements the destroy command's confirmation gate: it prompts
// on out, reads one LINE from in, and reports whether the user explicitly
// approved. Only "y"/"yes" (case-insensitive) proceeds; any other answer — a
// bare Enter (empty line), or EOF — denies. Split out so the gate is
// unit-testable without a cluster.
//
// [V27] Reading a whole line (not a token via fmt.Fscan, which skips newlines
// and blocks until it sees a non-whitespace token) is what makes a bare Enter
// at an interactive terminal deny per the advertised [y/N] default instead of
// hanging forever.
func confirmDestroy(in io.Reader, out io.Writer, id string) bool {
	fmt.Fprintf(out, "This will permanently destroy session %q and its PVC. This cannot be undone.\nContinue? [y/N]: ", id)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		fmt.Fprintln(out, "Cancelled.")
		return false
	}
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
			c, err := newClient()
			if err != nil {
				return err
			}
			// Warn if a turn is in flight BEFORE the irreversible confirmation, so
			// the operator decides with full information — destroy can't be undone,
			// unlike suspend. Mirrors suspend's best-effort probe: the active turn
			// id lives in the runner (not the Sandbox CRD), so ask it directly. Any
			// failure (unreachable / already suspended) is non-fatal. Bound it
			// tightly so probing a dead/suspended pod can't stall the confirmation
			// prompt on a port-forward timeout — the common destroy target.
			ref := session.Ref{ID: session.ID(id)}
			probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
			if rc, cleanup, derr := c.DialRunner(probeCtx, ref); derr == nil {
				st, sErr := rc.SessionState(probeCtx, ref)
				cleanup()
				if sErr == nil && st.ActiveTurnID != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: session %q has an active turn (%s); destroying will terminate it\n", id, st.ActiveTurnID)
				}
			}
			probeCancel()
			// Require confirmation unless --force is passed.
			if !force && !confirmDestroy(cmd.InOrStdin(), cmd.OutOrStdout(), id) {
				return nil
			}
			// Destroy tears down the cluster resources, then the file sync and all
			// local state (SSH alias, key dir, index entry).
			if err := c.Destroy(ctx, session.ID(id)); err != nil {
				return err
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
			c, err := newClient()
			if err != nil {
				return err
			}
			sessions, err := c.List(ctx)
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

			// With --all, surface sessions the local index knows about but that are
			// no longer present in the cluster (destroyed/expired).
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
// boolean in the status table.
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
			id := string(session.ID(args[0]))
			// Deliberately NOT via newClient(): these are purely local mutagen
			// operations, and needing a loadable kubeconfig here would block
			// exactly the user who wants to clean up syncs for a cluster that is
			// already gone. Only `sync gc` needs cluster access.
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
				if cfg, err := localSSHConfig(); err == nil {
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
	cmd.AddCommand(newSyncGCCmd())
	return cmd
}

// clusterLister is the subset of *client.Client the GC needs: the authoritative
// live-session set for the current context. Kept as an interface so the orphan
// selection is unit-testable without a cluster.
type clusterLister interface {
	List(ctx context.Context) ([]session.State, error)
	// Namespace is the client's effective k8s namespace, used to scope the sync GC
	// to the current namespace ([V28] — a sync stamped with a DIFFERENT namespace
	// label belongs to a live session this namespace-scoped live set can't see).
	Namespace() string
}

// selectOrphanSyncs picks the orphaned syncs to terminate. It is conservative:
//   - considers only syncs whose transport is down (IsOrphanStatus) OR that are
//     Paused ([V35] — a paused sync of a session that no longer exists, e.g. a
//     `kubectl delete sandbox` on a suspended session, is otherwise immortal);
//   - skips a sync whose session is still live in THIS context's cluster
//     (running or suspended — it reconnects/resumes); this is what keeps a merely
//     suspended (still-existing) session's paused syncs protected;
//   - skips a sync a DIFFERENT kube context created (its sandbox-context label
//     is set and != currentCtx) — the MF3 cross-context over-reap fix: the live
//     set only covers the current context, so another context's session would
//     otherwise look "gone" and be wrongly reaped;
//   - skips a sync a DIFFERENT namespace created (its sandbox-namespace label is
//     set and != currentNs) — [V28], the same shape as MF3 but for namespaces:
//     the live set is namespace-scoped, so a same-context sync in another
//     namespace would otherwise look "gone".
//
// A legacy sync with no sandbox-context / sandbox-namespace label ("" — created
// before MF3 / [V28]) falls through to the live-set check: it is GC-able by
// whichever context/namespace is running (so it never becomes immortal),
// reproducing the pre-fix behavior. It is re-stamped with both labels the next
// time the connect path (re)creates it, closing the migration window. Returns the
// mutagen identifiers to terminate and a per-session orphan count for reporting.
func selectOrphanSyncs(syncs []syncpkg.SyncSession, live map[string]bool, currentCtx, currentNs string) (orphanIDs []string, bySession map[string]int) {
	bySession = map[string]int{}
	for _, s := range syncs {
		if !syncpkg.IsOrphanStatus(s.Status) && !syncpkg.IsPausedStatus(s.Status) {
			continue // actively syncing / connected → keep
		}
		if s.Context != "" && s.Context != currentCtx {
			continue // another kube context owns it — not ours to judge (MF3)
		}
		if s.Namespace != "" && s.Namespace != currentNs {
			continue // another namespace owns it — not ours to judge ([V28])
		}
		if live[s.SessionID] {
			continue // session still exists (running/suspended) → keep (reconnects/resumes)
		}
		orphanIDs = append(orphanIDs, s.Identifier)
		bySession[s.SessionID]++
	}
	return orphanIDs, bySession
}

// gcResult is the outcome of one orphan-selection pass.
type gcResult struct {
	orphanIDs []string
	bySession map[string]int
}

// syncGCCore runs one GC selection pass against the current context: list this
// tool's syncs, confirm the live set from the cluster, and select orphans via
// selectOrphanSyncs. It does NOT terminate — the caller decides (the command
// prints/terminates; the best-effort startup sweep terminates silently). It
// refuses to proceed if the cluster can't be listed: during an outage every pod
// is unreachable, so every sync would look orphaned and an empty live set would
// nuke them all.
func syncGCCore(ctx context.Context, mgr *syncpkg.Manager, c clusterLister) (gcResult, error) {
	syncs, err := mgr.List(ctx)
	if err != nil {
		return gcResult{}, fmt.Errorf("list syncs: %w", err)
	}
	states, err := c.List(ctx)
	if err != nil {
		return gcResult{}, fmt.Errorf("cannot list cluster sessions; refusing to run (an outage makes every sync look gone): %w", err)
	}
	live := make(map[string]bool, len(states))
	for _, st := range states {
		live[string(st.ID)] = true
	}
	orphanIDs, bySession := selectOrphanSyncs(syncs, live, mgr.CurrentContext(), c.Namespace())
	return gcResult{orphanIDs: orphanIDs, bySession: bySession}, nil
}

// startupSyncGC fires the best-effort orphan-sync sweep (SF1) in the background
// so it never delays the TUI opening, cleaning up syncs left by destroyed/
// idle-reaped/dev-reset pods at launch instead of waiting for the first in-TUI
// reconcile (~T+30s). Bounded so a slow or offline cluster can't leak a goroutine
// for the process lifetime.
func startupSyncGC() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		runBestEffortSyncGC(ctx)
	}()
}

// runBestEffortSyncGC runs a full GC pass (select + terminate) silently and
// swallows every error (SF1). It is fired at the start of the TUI-launching
// commands so orphaned syncs from destroyed/reaped/dev-reset pods are cleaned up
// without waiting T+30s for the first in-TUI reconcile. Best-effort by design:
// no cluster (offline), no mutagen, or a list failure just skips this pass — the
// in-TUI GC still runs later, and `sandbox sync gc` remains the explicit path.
func runBestEffortSyncGC(ctx context.Context) {
	c, err := newClient()
	if err != nil {
		return
	}
	mgr := syncManager()
	res, err := syncGCCore(ctx, mgr, c)
	if err != nil || len(res.orphanIDs) == 0 {
		return
	}
	_ = mgr.TerminateByIdentifier(ctx, res.orphanIDs...)
}

// newSyncGCCmd terminates orphaned Mutagen sync sessions: this tool's syncs whose
// pod endpoint is gone (the session was destroyed, idle-reaped, dev-reset, or
// kubectl-deleted) and that would otherwise retry the dead pod forever and pile
// up in the host Mutagen daemon. It is conservative by construction:
//   - scoped to this tool's syncs (the sandbox-session label) — never the lima
//     sandbox-vm-id syncs that share the host daemon;
//   - scoped to the CURRENT kube context (the sandbox-context label) — never a
//     sync a different context created (MF3);
//   - only syncs whose transport is down (IsOrphanStatus), so an actively-syncing
//     session is never touched;
//   - cross-referenced against the live cluster, so a running OR suspended
//     session's sync is kept (it reconnects / resumes); and
//   - it refuses to run if the cluster can't be listed — during an outage every
//     sync looks down, and we must not mistake that for "all sessions gone".
//
// A terminated sync is safe to lose: the connect path re-creates it idempotently.
func newSyncGCCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Terminate orphaned file-sync sessions whose pod is gone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			mgr := syncManager()
			// Authoritative live set. Refuse to proceed without it: during a cluster
			// outage every pod is unreachable, so every sync would look orphaned and
			// an empty live set would nuke them all.
			c, err := newClient()
			if err != nil {
				return fmt.Errorf("sync gc needs cluster access to confirm live sessions: %w", err)
			}
			res, err := syncGCCore(ctx, mgr, c)
			if err != nil {
				return fmt.Errorf("sync gc: %w", err)
			}
			out := cmd.OutOrStdout()
			if len(res.orphanIDs) == 0 {
				fmt.Fprintln(out, "sync gc: no orphaned sync sessions.")
				return nil
			}
			for sid, n := range res.bySession {
				fmt.Fprintf(out, "  %s — %d orphaned sync session(s)\n", sid, n)
			}
			if dryRun {
				fmt.Fprintf(out, "sync gc: %d orphaned sync session(s) across %d gone session(s) (dry-run — re-run without --dry-run to terminate).\n", len(res.orphanIDs), len(res.bySession))
				return nil
			}
			if err := mgr.TerminateByIdentifier(ctx, res.orphanIDs...); err != nil {
				return fmt.Errorf("sync gc: terminate orphans: %w", err)
			}
			fmt.Fprintf(out, "sync gc: terminated %d orphaned sync session(s) across %d gone session(s).\n", len(res.orphanIDs), len(res.bySession))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list orphaned syncs without terminating them")
	return cmd
}

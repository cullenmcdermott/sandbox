package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// errReaped signals the reaper loop is done: the session was suspended or is
// already gone/suspended. It ends the loop with a clean exit so the Job
// completes (and TTL-cleans itself).
var errReaped = errors.New("reaper finished")

// defaultReaperIdleTimeout is the reaper's default idle window before a session
// is suspended. The dashboard reads it to render the warm-session "suspends in"
// hint so the indicator matches the reaper's actual behavior. Sourced from the
// client package so the CLI, the library, and the reaper all agree.
const defaultReaperIdleTimeout = client.DefaultIdleTimeout

// newReapCmd is the hidden in-cluster reaper. It watches a single session and
// suspends it once it has been idle (turn-done AND detached) for --idle-timeout.
// Run as a per-session Job; see docs/session-lifecycle.md.
func newReapCmd() *cobra.Command {
	var (
		sessionID    string
		idleTimeout  time.Duration
		pollInterval time.Duration
	)
	cmd := &cobra.Command{
		Use:    "reap",
		Short:  "Watch a session and suspend it when idle (internal; runs in-cluster)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessionID == "" {
				return fmt.Errorf("--session is required")
			}
			backend, err := newBackend()
			if err != nil {
				return err
			}
			ref := session.Ref{ID: session.ID(sessionID)}
			return runReaper(cmd.Context(), backend, ref, idleTimeout, pollInterval)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id to watch")
	cmd.Flags().DurationVar(&idleTimeout, "idle-timeout", defaultReaperIdleTimeout, "idle duration before suspend")
	cmd.Flags().DurationVar(&pollInterval, "poll", 30*time.Second, "poll interval")
	return cmd
}

func runReaper(ctx context.Context, backend *k8s.Backend, ref session.Ref, idleTimeout, poll time.Duration) error {
	log := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[reaper %s] "+format+"\n", append([]any{ref.ID}, args...)...)
	}
	log("watching; idle-timeout=%s poll=%s", idleTimeout, poll)

	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		// A tick error other than errReaped is transient (pod restarting,
		// network blip): log and keep watching rather than dying.
		if err := reaperTick(ctx, backend, ref, idleTimeout, log); err != nil {
			if errors.Is(err, errReaped) {
				return nil
			}
			log("tick: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// reaperDecision is the reaper's pre-poll action for a given session status.
type reaperDecision int

const (
	reaperProceed reaperDecision = iota // pod is running: poll the runner for idle
	reaperWait                          // transient (creating): retry next tick
	reaperExit                          // terminal: stop the reaper cleanly
)

// decideReaper maps a session status to the reaper's action before it polls the
// runner. Gone, suspended, AND failed are all terminal and must exit so the
// reaper Job completes. In particular a StatusFailed pod runs with
// RestartPolicyNever and never recovers, so the previous `default: return nil`
// looped the reaper forever — keeping the Job (BackoffLimit 1<<20, no deadline)
// alive and burning cluster resources until the session was manually destroyed
// (RV16). Only CREATING (and any future transient state) should wait-and-retry.
func decideReaper(status session.Status) reaperDecision {
	switch status {
	case session.StatusGone, session.StatusSuspended, session.StatusFailed:
		return reaperExit
	case session.StatusRunning:
		return reaperProceed
	default:
		return reaperWait
	}
}

// idleChecker reads a session's idle state from the runner. *runner.Client
// satisfies it; tests use a fake.
type idleChecker interface {
	Idle(ctx context.Context, ref session.Ref) (session.IdleStatus, error)
}

// sessionSuspender suspends a session. *k8s.Backend satisfies it; tests use a
// fake.
type sessionSuspender interface {
	Suspend(ctx context.Context, ref session.Ref) error
}

func reaperTick(ctx context.Context, backend *k8s.Backend, ref session.Ref, idleTimeout time.Duration, log func(string, ...any)) error {
	st, err := backend.Status(ctx, ref)
	if err != nil {
		return err
	}
	switch decideReaper(st.Status) {
	case reaperExit:
		log("session %s is %s; exiting reaper", ref.ID, st.Status)
		return errReaped
	case reaperWait:
		return nil
	}
	// reaperProceed: the pod is running; poll the runner for idle below.
	if !st.PodReady {
		return nil
	}

	token, err := backend.RunnerToken(ctx, ref)
	if err != nil {
		return err
	}
	ip, err := backend.PodIP(ctx, ref)
	if err != nil {
		return err
	}
	client := runner.New(fmt.Sprintf("http://%s:%d", ip, k8s.RunnerPort()), token)

	return evaluateIdle(ctx, client, backend, ref, idleTimeout, time.Now(), log)
}

// evaluateIdle is the testable core of reaperTick once the pod is confirmed
// running and ready: it polls the runner for idle, and — if the session has
// been idle for at least idleTimeout — re-checks (M19 TOCTOU narrowing) and
// suspends. It is split from the k8s-coupled status/port-forward setup so it can
// be unit-tested with fakes (no cluster, no network). now is injected so the
// timeout comparison is deterministic in tests.
//
// Returns errReaped after a successful suspend (ends the reaper loop), nil when
// the session is still active / not yet past timeout (keep watching), and any
// runner/suspend error otherwise.
func evaluateIdle(ctx context.Context, idle idleChecker, suspend sessionSuspender, ref session.Ref, idleTimeout time.Duration, now time.Time, log func(string, ...any)) error {
	status, err := idle.Idle(ctx, ref)
	if err != nil {
		return err
	}
	if status.IdleSince == "" {
		return nil // active: a turn is running or a client is attached
	}
	since, err := time.Parse(time.RFC3339, status.IdleSince)
	if err != nil {
		return fmt.Errorf("parse idleSince %q: %w", status.IdleSince, err)
	}
	d := now.Sub(since)
	if d < idleTimeout {
		return nil
	}
	// M19: re-check idle immediately before suspending to narrow the TOCTOU
	// window — a turn (or client) may have arrived since the first poll above.
	// The residual gap before Suspend is inherent to a stateless reaper and is
	// covered by the runner's graceful shutdown.
	recheck, err := idle.Idle(ctx, ref)
	if err != nil {
		return err
	}
	if recheck.IdleSince == "" {
		log("became active during idle check; skipping suspend")
		return nil
	}
	log("idle for %s (>= %s); suspending", d.Round(time.Second), idleTimeout)
	if err := suspend.Suspend(ctx, ref); err != nil {
		return err
	}
	log("suspended")
	return errReaped
}

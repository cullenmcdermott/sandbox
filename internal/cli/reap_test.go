package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// RV16: the reaper must EXIT (not loop forever) on a terminal status. A
// StatusFailed pod runs with RestartPolicyNever and never recovers, so the old
// `default: return nil` kept the reaper Job alive indefinitely.
func TestDecideReaper(t *testing.T) {
	cases := []struct {
		status session.Status
		want   reaperDecision
	}{
		{session.StatusGone, reaperExit},
		{session.StatusSuspended, reaperExit},
		{session.StatusFailed, reaperExit}, // the RV16 fix
		{session.StatusRunning, reaperProceed},
		{session.StatusCreating, reaperWait},
		{session.Status("UNKNOWN_FUTURE"), reaperWait},
	}
	for _, c := range cases {
		if got := decideReaper(c.status); got != c.want {
			t.Errorf("decideReaper(%s) = %d, want %d", c.status, got, c.want)
		}
	}
}

// fakeIdleChecker is a test double for idleChecker. It returns scripted
// IdleStatus values on successive calls (so a test can model the M19 re-check
// returning a different state than the first poll) and counts the calls.
type fakeIdleChecker struct {
	results []session.IdleStatus
	err     error
	calls   int
}

func (f *fakeIdleChecker) Idle(_ context.Context, _ session.Ref) (session.IdleStatus, error) {
	if f.err != nil {
		return session.IdleStatus{}, f.err
	}
	i := f.calls
	f.calls++
	if i < len(f.results) {
		return f.results[i], nil
	}
	// Past the script, keep returning the last value (steady state).
	if len(f.results) > 0 {
		return f.results[len(f.results)-1], nil
	}
	return session.IdleStatus{}, nil
}

// fakeSuspender is a test double for sessionSuspender; it records whether
// Suspend was called.
type fakeSuspender struct {
	called bool
	err    error
}

func (f *fakeSuspender) Suspend(_ context.Context, _ session.Ref) error {
	f.called = true
	return f.err
}

func discardLog(string, ...any) {}

// TestEvaluateIdleNotYetIdle: the session became idle only a moment ago (well
// under the timeout), so the reaper neither suspends nor exits — it returns nil
// to keep watching.
func TestEvaluateIdleNotYetIdle(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	idle := &fakeIdleChecker{results: []session.IdleStatus{
		{IdleSince: now.Add(-2 * time.Minute).Format(time.RFC3339)},
	}}
	sus := &fakeSuspender{}

	err := evaluateIdle(context.Background(), idle, sus, session.Ref{ID: "s1"}, 15*time.Minute, now, discardLog)
	if err != nil {
		t.Fatalf("evaluateIdle returned %v, want nil (not yet idle)", err)
	}
	if sus.called {
		t.Error("Suspend was called for a session that is not yet past the idle timeout")
	}
	if idle.calls != 1 {
		t.Errorf("Idle polled %d times, want 1 (no re-check before timeout)", idle.calls)
	}
}

// TestEvaluateIdleActiveNoSuspend: an active session (empty IdleSince) is never
// suspended and the reaper keeps watching.
func TestEvaluateIdleActiveNoSuspend(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	idle := &fakeIdleChecker{results: []session.IdleStatus{{IdleSince: ""}}}
	sus := &fakeSuspender{}

	if err := evaluateIdle(context.Background(), idle, sus, session.Ref{ID: "s1"}, 15*time.Minute, now, discardLog); err != nil {
		t.Fatalf("evaluateIdle returned %v, want nil (active)", err)
	}
	if sus.called {
		t.Error("Suspend was called for an active session")
	}
}

// TestEvaluateIdlePastTimeoutSuspends: idle longer than the timeout, and the
// M19 re-check confirms still-idle, so the reaper suspends and returns errReaped
// to end the loop cleanly.
func TestEvaluateIdlePastTimeoutSuspends(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-30 * time.Minute).Format(time.RFC3339)
	idle := &fakeIdleChecker{results: []session.IdleStatus{
		{IdleSince: stale}, // first poll: idle past timeout
		{IdleSince: stale}, // M19 re-check: still idle
	}}
	sus := &fakeSuspender{}

	err := evaluateIdle(context.Background(), idle, sus, session.Ref{ID: "s1"}, 15*time.Minute, now, discardLog)
	if !errors.Is(err, errReaped) {
		t.Fatalf("evaluateIdle returned %v, want errReaped after suspend", err)
	}
	if !sus.called {
		t.Error("Suspend was not called for a session idle past the timeout")
	}
	if idle.calls != 2 {
		t.Errorf("Idle polled %d times, want 2 (poll + M19 re-check)", idle.calls)
	}
}

// TestEvaluateIdleM19Race: the first poll reports idle past the timeout, but by
// the time the reaper re-checks (M19) a turn/client has arrived (IdleSince
// cleared), so it must SKIP the suspend and keep watching.
func TestEvaluateIdleM19Race(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	idle := &fakeIdleChecker{results: []session.IdleStatus{
		{IdleSince: now.Add(-30 * time.Minute).Format(time.RFC3339)}, // first poll: idle
		{IdleSince: ""}, // M19 re-check: became active again
	}}
	sus := &fakeSuspender{}

	err := evaluateIdle(context.Background(), idle, sus, session.Ref{ID: "s1"}, 15*time.Minute, now, discardLog)
	if err != nil {
		t.Fatalf("evaluateIdle returned %v, want nil (raced active during re-check)", err)
	}
	if sus.called {
		t.Error("Suspend was called despite the session becoming active during the M19 re-check")
	}
	if idle.calls != 2 {
		t.Errorf("Idle polled %d times, want 2 (poll + M19 re-check)", idle.calls)
	}
}

// TestEvaluateIdlePropagatesError: a runner Idle error is surfaced, not
// swallowed into a suspend or a clean exit.
func TestEvaluateIdlePropagatesError(t *testing.T) {
	sentinel := errors.New("runner unreachable")
	idle := &fakeIdleChecker{err: sentinel}
	sus := &fakeSuspender{}

	err := evaluateIdle(context.Background(), idle, sus, session.Ref{ID: "s1"}, 15*time.Minute, time.Now(), discardLog)
	if !errors.Is(err, sentinel) {
		t.Fatalf("evaluateIdle returned %v, want the Idle error to propagate", err)
	}
	if sus.called {
		t.Error("Suspend must not be called when the idle poll failed")
	}
}

// TestEvaluateIdleMalformedIdleSince: a runner that reports an unparseable
// IdleSince surfaces a parse error (not a silent skip or a spurious suspend), so
// a runner bug can't quietly disable the reaper.
func TestEvaluateIdleMalformedIdleSince(t *testing.T) {
	idle := &fakeIdleChecker{results: []session.IdleStatus{{IdleSince: "not-a-timestamp"}}}
	sus := &fakeSuspender{}

	err := evaluateIdle(context.Background(), idle, sus, session.Ref{ID: "s1"}, 15*time.Minute, time.Now(), discardLog)
	if err == nil || !strings.Contains(err.Error(), "parse idleSince") {
		t.Fatalf("evaluateIdle returned %v, want a parse idleSince error", err)
	}
	if sus.called {
		t.Error("Suspend must not be called when IdleSince is unparseable")
	}
}

// TestEvaluateIdleRecheckError: an error on the M19 re-check (after the first
// poll already showed idle-past-timeout) propagates and blocks the suspend,
// rather than suspending on a stale first reading.
func TestEvaluateIdleRecheckError(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-30 * time.Minute).Format(time.RFC3339)
	sentinel := errors.New("runner blipped on recheck")
	idle := &recheckErrIdleChecker{first: session.IdleStatus{IdleSince: stale}, recheckErr: sentinel}
	sus := &fakeSuspender{}

	err := evaluateIdle(context.Background(), idle, sus, session.Ref{ID: "s1"}, 15*time.Minute, now, discardLog)
	if !errors.Is(err, sentinel) {
		t.Fatalf("evaluateIdle returned %v, want the re-check error to propagate", err)
	}
	if sus.called {
		t.Error("Suspend must not be called when the M19 re-check failed")
	}
}

// TestEvaluateIdleSuspendError: a Suspend failure (after confirmed idle) is
// surfaced so the reaper loop logs+retries rather than reporting a clean reap.
func TestEvaluateIdleSuspendError(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-30 * time.Minute).Format(time.RFC3339)
	idle := &fakeIdleChecker{results: []session.IdleStatus{{IdleSince: stale}, {IdleSince: stale}}}
	sus := &fakeSuspender{err: errors.New("suspend boom")}

	err := evaluateIdle(context.Background(), idle, sus, session.Ref{ID: "s1"}, 15*time.Minute, now, discardLog)
	if err == nil || errors.Is(err, errReaped) {
		t.Fatalf("evaluateIdle returned %v, want the Suspend error (not errReaped)", err)
	}
	if !sus.called {
		t.Error("Suspend should have been attempted")
	}
}

// recheckErrIdleChecker returns a scripted first result, then an error on the
// M19 re-check (the second Idle call) — a state fakeIdleChecker can't express,
// since its err applies to every call.
type recheckErrIdleChecker struct {
	first      session.IdleStatus
	recheckErr error
	calls      int
}

func (f *recheckErrIdleChecker) Idle(_ context.Context, _ session.Ref) (session.IdleStatus, error) {
	f.calls++
	if f.calls == 1 {
		return f.first, nil
	}
	return session.IdleStatus{}, f.recheckErr
}

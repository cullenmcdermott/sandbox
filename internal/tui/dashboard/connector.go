package dashboard

import (
	"context"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// RunnerClient is the subset of session.RunnerClient that the dashboard needs.
// It mirrors tui.RunnerClient exactly so that the concrete *runner.Client satisfies
// both interfaces without any adapter. The dashboard package declares its own copy
// here to avoid an import cycle: internal/cli imports internal/tui/dashboard, so
// dashboard must not import internal/cli or internal/tui.
type RunnerClient interface {
	Health(ctx context.Context) error
	StartTurn(ctx context.Context, ref session.Ref, input session.TurnInput) (session.TurnRef, error)
	InterruptTurn(ctx context.Context, ref session.Ref, turn session.TurnRef) error
	ResolvePermission(ctx context.Context, ref session.Ref, decision session.PermissionDecision) error
	Events(ctx context.Context, ref session.Ref, afterSeq uint64) (<-chan session.Event, error)
	// EventsPassive opens a status-observer stream that does NOT count as an
	// attached client for idle detection — used for background list streams so
	// the dashboard doesn't pin every running session against the reaper (RV6).
	EventsPassive(ctx context.Context, ref session.Ref, afterSeq uint64) (<-chan session.Event, error)
	SessionState(ctx context.Context, ref session.Ref) (session.State, error)
	Exec(ctx context.Context, ref session.Ref, command string) (session.ExecResult, error)
	// Idle reports whether the session is idle (and since when), used to render
	// the warm-session "suspends in ~X" hint.
	Idle(ctx context.Context, ref session.Ref) (session.IdleStatus, error)
}

// OpencodeCreds holds the local endpoint and HTTP basic-auth credentials for
// an opencode-server session. The dashboard passes these to the external pane
// so it can spawn `opencode attach` without knowing how they were obtained.
type OpencodeCreds struct {
	Username string
	Password string
	URL      string // e.g. http://127.0.0.1:4096
}

// ConnectResult is the successful outcome of a Connector call: a live client
// and the reconnect callback (same signature as the connector) that the
// transcript TUI uses when the SSE stream drops.
type ConnectResult struct {
	Client        RunnerClient
	Reconnect     func(ctx context.Context) (RunnerClient, error)
	Endpoint      string         // runner HTTP base URL (claude transcript / SSE)
	OpencodeCreds *OpencodeCreds // nil for claude-sdk sessions
	// Warning is a non-fatal advisory surfaced to the user (e.g. sync failed).
	// The dashboard renders it inline rather than dropping it to hidden stderr.
	Warning string
}

// Connector is a function that (re)establishes a live runner connection for
// the given session. The implementation lives in internal/cli and is passed
// into dashboard.Run so that the dashboard package never imports cli.
//
// The connector must:
//   - Resume the pod if it is suspended.
//   - Establish a port-forward.
//   - Wait until the runner is healthy.
//   - Return a fresh client and a reconnect callback.
//
// A failed connector returns a descriptive error; the dashboard renders it
// inline and stays on the dashboard screen — it does NOT crash.
//
// onStage(stage, detail) reports progress: stage is the coarse phase; detail is
// an optional live sub-status (e.g. "uploading" during the initial file sync),
// "" when there is none.
type Connector func(ctx context.Context, ref session.Ref, projectPath string, onStage func(ConnectStage, string)) (ConnectResult, error)

// SyncProber reports a coarse sync health for a session, decoupling the
// dashboard from internal/sync. Returns a short token: "synced"/"syncing"/
// "stalled"/"unknown".
type SyncProber func(ctx context.Context, id session.ID) string

// CreateResult is the successful outcome of a Creator call: the newly created
// session's observed State plus a live client and reconnect callback ready for
// the transcript to attach to.
type CreateResult struct {
	State         session.State
	Client        RunnerClient
	Reconnect     func(ctx context.Context) (RunnerClient, error)
	Endpoint      string         // runner HTTP base URL
	OpencodeCreds *OpencodeCreds // nil for claude-sdk sessions
	// Warning is a non-fatal advisory surfaced to the user (e.g. sync failed on
	// the freshly created session). Without this, the new-session path — the most
	// common path, and where the initial sync is most likely to hiccup — silently
	// dropped the warning the connector computed (RV23).
	Warning string
}

// Creator provisions a brand-new session for the dashboard's working directory
// and returns a live connection ready to attach. Like Connector, it is
// implemented in internal/cli and injected into dashboard.Run so the dashboard
// package never imports cli. It owns ID generation, SSH-key prep, Sandbox/PVC
// creation, pod start, port-forward, and health-check.
//
// A failed Creator returns a descriptive error; the dashboard renders it inline
// and stays on the dashboard screen — it does NOT crash.
type Creator func(ctx context.Context, backend string, onStage func(ConnectStage, string)) (CreateResult, error)

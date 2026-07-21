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

// ReconnectFunc re-establishes a live runner connection after the SSE stream
// drops (node drain, suspend/resume, transient port-forward loss). onStage
// reports coarse stage progress (Resume → port-forward → runner health) so the
// transcript header can show a live "Resuming pod…" readout instead of a flat
// "reconnecting…" during a slow cold-pod resume; pass a no-op to ignore it. It
// is a distinct callback per reconnect, NOT the connecting screen's onStage
// (whose channel is closed by reconnect time — reusing it would panic).
type ReconnectFunc func(ctx context.Context, onStage func(ConnectStage, string)) (RunnerClient, error)

// ConnectResult is the successful outcome of a Connector call: a live client
// and the reconnect callback that the transcript TUI uses when the SSE stream
// drops.
type ConnectResult struct {
	Client        RunnerClient
	Reconnect     ReconnectFunc
	Endpoint      string         // runner HTTP base URL (claude transcript / SSE)
	OpencodeCreds *OpencodeCreds // nil for claude-sdk sessions
	// PaneDial, when non-nil, establishes the session's remote pane transport
	// (the claude-pane WebSocket over the runner forward). Set only for
	// backends whose interactive TUI runs in the pod; the App routes the attach
	// to an ExternalPane over this transport instead of a Go transcript.
	PaneDial PaneDial
	// Warning is a non-fatal advisory surfaced to the user (e.g. sync failed).
	// The dashboard renders it inline rather than dropping it to hidden stderr.
	Warning string
	// Close, when non-nil, tears down the connection's transport — the SPDY
	// port-forward(s) and their reconnect loops (§1d C1). The context handed to
	// the Connector governs only establishment; an established forward lives
	// until Close is called. Every owner that discards a ConnectResult — stream
	// teardown, observer eviction, a superseded/raced connect, a failed
	// EventsPassive — MUST call it, or the forward polls the API server forever.
	Close func()
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

// SyncHealth is a session's sync-health reading, decoupling the dashboard from
// internal/sync. Status is the short token ("synced"/"syncing"/"stalled"/
// "conflicted"/"unknown"). Conflicts and Hint are populated only for the
// "conflicted" status: Conflicts holds a few already-formatted per-file detail
// lines and Hint a one-line resolution reminder — the dashboard renders both
// verbatim in the detail pane, staying ignorant of mutagen's conflict shape.
type SyncHealth struct {
	Status    string
	Conflicts []string
	Hint      string
}

// SyncProber reports a session's sync health, decoupling the dashboard from
// internal/sync. The zero SyncHealth (empty Status) is treated as no reading.
type SyncProber func(ctx context.Context, id session.ID) SyncHealth

// OrphanSync is a sandbox mutagen sync session whose pod endpoint is unreachable
// — a GC candidate. Identifier addresses the mutagen session; SessionID is the
// sandbox session it belongs to.
type OrphanSync struct {
	Identifier string
	SessionID  session.ID
}

// SyncReaper lists this tool's mutagen sync sessions whose pod endpoint is
// unreachable (orphan candidates) and terminates specific ones by identifier.
// Decoupled from internal/sync, like SyncProber.
//
// The dashboard's GC (piggybacked on the periodic cluster reconcile) terminates
// an orphan ONLY when its session's pod is NOT up per the latest authoritative
// snapshot — i.e. the session is gone, Suspended, or Failed (a Suspended session's
// sync thrashes because the in-cluster idle reaper can't pause the host daemon) —
// AND it has stayed that way past a grace window. So a Running session's sync
// (even mid-blip) and a fresh session still scheduling are never touched. A
// terminated sync is harmless to lose: the connect path re-creates it idempotently
// (and resumes it if it was merely paused) on the next attach.
type SyncReaper interface {
	ListOrphans(ctx context.Context) ([]OrphanSync, error)
	Terminate(ctx context.Context, identifiers []string) error
}

// CreateResult is the successful outcome of a Creator call: the newly created
// session's observed State plus a live client and reconnect callback ready for
// the transcript to attach to.
type CreateResult struct {
	State         session.State
	Client        RunnerClient
	Reconnect     ReconnectFunc
	Endpoint      string         // runner HTTP base URL
	OpencodeCreds *OpencodeCreds // nil for claude-sdk sessions
	// PaneDial mirrors ConnectResult.PaneDial for the create path.
	PaneDial PaneDial
	// Warning is a non-fatal advisory surfaced to the user (e.g. sync failed on
	// the freshly created session). Without this, the new-session path — the most
	// common path, and where the initial sync is most likely to hiccup — silently
	// dropped the warning the connector computed (RV23).
	Warning string
	// Close mirrors ConnectResult.Close for the create path (§1d C1).
	Close func()
}

// CreateParams carries the new-session choices the picker gathers before
// provisioning. It keeps the Creator decoupled from Keychain: only the account
// ID crosses the seam, never secret bytes. The CLI-side Creator resolves the ID
// to a credential (fail closed — see setAnthropicAccount).
type CreateParams struct {
	// Backend is the session.Backend* value the user picked.
	Backend string
	// AnthropicAccountID is the stored Anthropic account the claude session runs
	// on. Empty means the legacy/cluster-default path (shared operator Secret) —
	// either no accounts are stored or the user explicitly chose "cluster
	// default". Ignored for non-claude backends (opencode has no account step).
	AnthropicAccountID string
	// ProjectPath is the host project directory the new session mirrors, chosen
	// in the create overlay's directory picker (T10) — already ~-expanded and
	// canonicalized by the picker. Empty means the Creator's default: the
	// dashboard process's working directory (the pre-picker behavior, and what
	// the CLI commands always use). The CLI-side Creator re-validates a non-empty
	// path fail-closed (the directory may have vanished between pick and create).
	ProjectPath string
}

// Creator provisions a brand-new session for the picked project directory
// (params.ProjectPath, falling back to the dashboard's working directory) and
// returns a live connection ready to attach. Like Connector, it is
// implemented in internal/cli and injected into dashboard.Run so the dashboard
// package never imports cli. It owns ID generation, SSH-key prep, Sandbox/PVC
// creation, pod start, port-forward, and health-check.
//
// When params.AnthropicAccountID != "" the CLI-side Creator resolves the account
// to a per-session credential and FAILS CLOSED on any resolution/Keychain error
// (the error surfaces in the connect-error UI); it never silently falls back to
// the shared Secret. When empty, the legacy shared-Secret path is used unchanged.
//
// A failed Creator returns a descriptive error; the dashboard renders it inline
// and stays on the dashboard screen — it does NOT crash.
type Creator func(ctx context.Context, params CreateParams, onStage func(ConnectStage, string)) (CreateResult, error)

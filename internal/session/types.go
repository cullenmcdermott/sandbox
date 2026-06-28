// Package session defines the remote agent session model used by the sandbox
// CLI's Kubernetes backend.
//
// The types here are the normalized model that backends (Claude SDK, OpenCode,
// Codex) map their native protocols into. The local Bubble Tea TUI and the
// remote runner share these types over HTTP+SSE.
package session

import (
	"errors"
	"time"
)

// ErrSessionGone is returned by a connect/reconnect attempt when the session no
// longer exists in the cluster (its Sandbox CRD was deleted). It is a permanent,
// non-retryable condition: callers (e.g. the TUI reconnect loop) classify it with
// errors.Is to give up and show a terminal "session gone" state instead of
// retrying forever.
var ErrSessionGone = errors.New("session no longer exists")

// Backend identifiers selecting which agent backend the runner uses. These are
// the canonical values for Spec.Backend / State.Backend, shared across the CLI,
// the k8s backend, and the dashboard to avoid stringly-typed drift.
const (
	BackendClaudeSDK = "claude-sdk"
	BackendOpenCode  = "opencode-server"
)

// ID is a sandbox session identifier, e.g. "claude-sdk-7f3a".
type ID string

// TurnID identifies a single user-prompted turn within a session.
type TurnID string

// Ref addresses a remote session by ID.
type Ref struct {
	ID ID `json:"id"`
}

// Spec is the create-time specification for a remote session.
type Spec struct {
	// ID is the session name; also used as the Sandbox/PVC name. Must be a
	// valid Kubernetes DNS label (lowercase, [a-z0-9-], max 63).
	ID ID `json:"id"`

	// ProjectPath is the absolute local path of the project (e.g.
	// "/Users/cullen/git/homelab"). The runner mirrors this path inside the
	// pod so Claude project transcript keys stay compatible.
	ProjectPath string `json:"projectPath"`

	// Backend selects which agent backend the runner uses ("claude-sdk",
	// "opencode-server", "codex-app-server").
	Backend string `json:"backend"`

	// RunnerImage is the container image for the runner pod.
	RunnerImage string `json:"runnerImage"`

	// Model is an optional model id/alias (e.g. "opus", "sonnet",
	// "claude-opus-4-8") the runner passes to the Claude Agent SDK as the
	// session default. Empty means the account default. Per-turn overrides
	// (TurnInput.Model, the in-session /model command) take precedence.
	Model string `json:"model,omitempty"`

	// Namespace is the Kubernetes namespace for the Sandbox/PVC. Defaults to
	// "agent-sessions".
	Namespace string `json:"namespace"`

	// SSHPublicKey is the OpenSSH-format public key (e.g. "ssh-ed25519 AAAA...")
	// authorized for Mutagen's SSH transport into the pod. The matching private
	// key is held locally by the CLI. May be empty (SSH sync disabled).
	SSHPublicKey string `json:"-"`

	// StorageClass is the PVC storage class. Defaults to "rook-ceph-block".
	StorageClass string `json:"storageClass"`

	// StorageGiB is the PVC size. Defaults to 50.
	StorageGiB int `json:"storageGiB"`
}

// Status is the lifecycle state of a remote session.
type Status string

const (
	StatusUnknown   Status = "UNKNOWN"
	StatusCreating  Status = "CREATING"
	StatusRunning   Status = "RUNNING"
	StatusSuspended Status = "SUSPENDED"
	StatusFailed    Status = "FAILED"
	StatusGone      Status = "GONE"
)

// State is the observed state of a remote session, mirroring the runner's
// session.json plus Kubernetes pod/Sandbox state.
type State struct {
	ID          ID     `json:"id"`
	Backend     string `json:"backend"`
	ProjectPath string `json:"projectPath"`
	Model       string `json:"model,omitempty"`
	Status      Status `json:"status"`
	// ClaudeSession is populated from the runner's session.json (the upstream
	// Claude SDK session id) but is not yet read anywhere in the Go CLI; it is
	// carried for future resume/inspection features.
	ClaudeSession string    `json:"claudeSession,omitempty"`
	LastTurnID    TurnID    `json:"lastTurnId,omitempty"`
	LastActivity  time.Time `json:"lastActivity,omitempty"`
	CreatedAt     time.Time `json:"createdAt,omitempty"`
	PodName       string    `json:"podName,omitempty"`
	PodReady      bool      `json:"podReady,omitempty"`
	SandboxName   string    `json:"sandboxName,omitempty"`
	RunnerToken   string    `json:"-"`
}

// TurnInput is the user input that starts a turn.
type TurnInput struct {
	Prompt       string   `json:"prompt"`
	Resume       TurnID   `json:"resume,omitempty"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	// Mode is the SDK permission mode the turn runs in: one of
	// "default", "acceptEdits", "plan", "bypassPermissions". Empty means the
	// runner uses "acceptEdits" (preserves the pre-mode-switching behavior).
	Mode string `json:"mode,omitempty"`
	// Model overrides the model for this turn (the in-session /model switch):
	// an id/alias like "opus", "sonnet", "haiku", or a full id. Empty means the
	// runner falls back to its session default (Spec.Model / SANDBOX_MODEL) and
	// then the account default.
	Model string `json:"model,omitempty"`
	// Effort overrides the reasoning-effort level for this turn (the in-session
	// /effort switch): one of "low", "medium", "high", "xhigh", "max". Empty =>
	// the runner leaves options.effort unset (SDK adaptive-thinking default).
	// Supported on Fable 5 / Opus 4.6+ / Sonnet 4.6 only; silently ignored
	// elsewhere. NOTE: the wire value is the real SDK enum — the TUI displays
	// "max" as "ultracode".
	Effort string `json:"effort,omitempty"`
}

// TurnRef addresses a specific turn.
type TurnRef struct {
	Session ID     `json:"session"`
	Turn    TurnID `json:"turn"`
}

// PermissionDecision is a user's response to a permission request.
type PermissionDecision struct {
	Session     ID     `json:"session"`
	Permission  string `json:"permission"` // permission event ID
	Allow       bool   `json:"allow"`
	Scope       string `json:"scope"` // "once" | "session"
	EditedInput string `json:"editedInput,omitempty"`
}

// ExecResult is the outcome of a one-shot shell command run in the session
// cwd via the runner's /exec endpoint (slice 2 `!` passthrough). Output is
// bounded by the runner; ExitCode is the process exit code (124 on timeout).
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// PortSpec describes a port-forward request.
type PortSpec struct {
	Local  int `json:"local"`
	Remote int `json:"remote"`
}

// ForwardHandle represents an active port-forward.
type ForwardHandle interface {
	// LocalPort returns the local port the forward is listening on.
	LocalPort() int
	// Close stops the port-forward.
	Close() error
	// Done returns a channel closed when the forward terminates.
	Done() <-chan struct{}
}

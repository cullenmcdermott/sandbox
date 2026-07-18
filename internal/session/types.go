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
	// BackendCodex is the Codex backend: the runner supervises a `codex
	// app-server` child listening on a pod-loopback websocket and passively
	// observes it for metrics; turns are driven by the interactive codex client
	// over a port-forward, not through the runner. Phase 1 ships the supervisor +
	// observer + credential contract; the interactive pane lands in a later wave.
	BackendCodex = "codex-app-server"
)

// OpenCode provider identifiers selecting which SINGLE model-provider API key an
// opencode-server session is provisioned with (Spec.OpencodeProvider). The k8s
// backend injects only the selected provider's key from the shared
// opencode-credentials Secret; unselected providers are not mounted at all.
// Empty defaults to OpencodeProviderAnthropic.
const (
	OpencodeProviderAnthropic = "anthropic"
	OpencodeProviderOpenAI    = "openai"
	OpencodeProviderZen       = "opencode-zen"
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

	// ProjectPath is the absolute local path of the project's repo root (e.g.
	// "/Users/cullen/git/homelab"). It is the identity/display path: NewID
	// hashes it to group a repo's sessions, and it is what the local index and
	// UI show. It is NOT necessarily the path the pod runs in — that is
	// WorkspacePath (they differ once a per-session worktree exists).
	ProjectPath string `json:"projectPath"`

	// WorkspacePath is the absolute local path bind-mounted into the pod as the
	// agent's working directory and used as BOTH Mutagen sync endpoints. It is
	// the SDK cwd, so the pod's transcript directory and the local Mutagen alpha
	// path match (resumable transcripts). Empty means "same as ProjectPath":
	// there is no separate worktree, so the workspace IS the repo root. A
	// per-session git worktree (a later wave) points this at the worktree dir
	// while ProjectPath stays the repo root.
	WorkspacePath string `json:"workspacePath,omitempty"`

	// Backend selects which agent backend the runner uses (BackendClaudeSDK,
	// BackendOpenCode, BackendCodex).
	Backend string `json:"backend"`

	// RunnerImage is the container image for the runner pod.
	RunnerImage string `json:"runnerImage"`

	// ImagePullPolicy overrides the runner pod's imagePullPolicy. One of
	// "Always", "IfNotPresent", "Never". Empty selects a default: IfNotPresent
	// for a digest-pinned RunnerImage (immutable, safe to cache), else Always (so
	// a moving tag like :latest always reflects upstream). Set it explicitly for
	// side-loaded/offline images or a rate-limited/private registry.
	ImagePullPolicy string `json:"imagePullPolicy,omitempty"`

	// Model is an optional model id/alias (e.g. "opus", "sonnet",
	// "claude-opus-4-8") the runner passes to the Claude Agent SDK as the
	// session default. Empty means the account default. Per-turn overrides
	// (TurnInput.Model, the in-session /model command) take precedence.
	Model string `json:"model,omitempty"`

	// AnthropicAuth selects the Anthropic credential the claude-sdk runner pod
	// authenticates with. Exactly one credential env is populated per pod:
	//   ""/"oauth" — subscription OAuth token (CLAUDE_CODE_OAUTH_TOKEN, from the
	//                anthropic-credentials Secret key "api-key"); the default.
	//   "api-key"  — Console API key (ANTHROPIC_API_KEY, from the
	//                anthropic-credentials Secret key "console-api-key").
	// These spellings and Secret key names are a consumer contract — do not
	// rename them. Claude Code prefers x-api-key over OAuth and rejects the
	// OAuth token when ANTHROPIC_API_KEY is also set, so the selection is
	// explicit and never populates both. This field ALONE selects the env var —
	// including for account-backed sessions — so a caller setting
	// AnthropicAccountID MUST set it to match the account's type (see
	// AnthropicAccountID). Ignored by non-claude backends.
	AnthropicAuth string `json:"anthropicAuth,omitempty"`

	// AnthropicAccountID identifies the stored Anthropic account a claude-sdk
	// session is provisioned with (see client/cred). It is plain metadata:
	// serialized so status/the picker can show which account a session runs on
	// and so rotation/logout can enumerate affected sessions. A non-empty value
	// is ALSO the fail-closed branch signal in the k8s backend — when set, the
	// pod's credential comes from the per-session Secret (key
	// "anthropic-credential") rather than the shared anthropic-credentials
	// Secret; empty selects the shared-Secret fallback. The caller MUST also set
	// AnthropicAuth to the account's type ("oauth" for subscription accounts,
	// "api-key" for console) — env-var selection is driven solely by
	// AnthropicAuth, and the account's type is not visible at this layer, so the
	// correlation cannot be validated here; client/cred's
	// AuthForType(account.Type) is the canonical way to derive it. The id is
	// used as a Kubernetes label value (sandbox.cullen.dev/anthropic-account) on
	// that Secret, so it must be a valid label value / DNS-safe — the cred store
	// guarantees this. Ignored by non-claude backends.
	AnthropicAccountID string `json:"anthropicAccountId,omitempty"`

	// AnthropicCredential is the resolved secret bytes for AnthropicAccountID
	// (an OAuth token or Console API key), written into the per-session Secret
	// at CreateSession. It is NEVER serialized (json:"-") — like SSHPublicKey it
	// is create-time-only material that must not land in the local session index
	// or any wire payload; the pod receives it only as a SecretKeyRef env var,
	// never an inline value. The CLI/TUI resolves account → bytes (reads the
	// Keychain/file store); the client layer just carries and writes them.
	// Ignored by non-claude backends.
	AnthropicCredential []byte `json:"-"`

	// OpencodeProvider selects which SINGLE model-provider API key an
	// opencode-server session is provisioned with, injected fail-closed from the
	// shared opencode-credentials Secret. One of OpencodeProviderAnthropic
	// (default), OpencodeProviderOpenAI, OpencodeProviderZen; empty means
	// Anthropic. Only that provider's key is mounted — unselected providers are
	// not injected at all (a hardening change from the prior all-optional,
	// fail-open fan-out). Choosing a non-default provider is not yet wired through
	// the client CreateOptions surface (that generalization is a separate item);
	// until then opencode sessions always resolve to Anthropic. Ignored by
	// non-opencode backends.
	OpencodeProvider string `json:"opencodeProvider,omitempty"`

	// CodexAccountID identifies the stored ChatGPT account a codex-app-server
	// session is provisioned with. It is plain metadata: serialized so status/the
	// picker can show which account a session runs on and so rotation/logout can
	// enumerate affected sessions. A non-empty value is ALSO the fail-closed branch
	// signal in the k8s backend — when set, the pod's Codex credential comes from
	// the per-session Secret (key "codex-auth-json") rather than the shared
	// OPENAI_API_KEY fallback; empty selects the shared-Secret fallback. The id is
	// used as a Kubernetes label value (sandbox.cullen.dev/codex-account) on that
	// Secret, so it must be a valid label value / DNS-safe — the cred store
	// guarantees this. Ignored by non-codex backends.
	CodexAccountID string `json:"codexAccountId,omitempty"`

	// CodexAuthJSON is the FULL ChatGPT-OAuth auth.json document (NOT an API key)
	// for CodexAccountID, written into the per-session Secret at CreateSession. It
	// is NEVER serialized (json:"-") — like SSHPublicKey and AnthropicCredential it
	// is create-time-only material that must not land in the local session index or
	// any wire payload. The pod receives it as a SecretKeyRef env var
	// (CODEX_AUTH_JSON) and materializes it as a FILE at $CODEX_HOME/auth.json — it
	// is a file contract, not an env-var credential the process reads directly. The
	// CLI/TUI resolves account → bytes; the client layer just carries and writes
	// them. Ignored by non-codex backends.
	CodexAuthJSON []byte `json:"-"`

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

// Activity is the runner-reported turn activity of a session — whether a turn is
// currently running. It is a DISTINCT vocabulary from the lifecycle Status (D9):
// Status (CREATING/RUNNING/SUSPENDED/…) is owned by the k8s backend and describes
// the pod/Sandbox, while Activity (idle/busy/error) is owned by the runner and
// describes the agent turn loop. Keeping them on separate fields is the "one
// vocabulary per field" resolution — a runner-sourced State never overwrites the
// lifecycle Status with an "idle"/"busy" value.
type Activity string

const (
	ActivityIdle  Activity = "idle"
	ActivityBusy  Activity = "busy"
	ActivityError Activity = "error"
)

// ApprovalPolicy is the tool-approval policy a turn runs under — an owned enum so
// callers pick a named policy instead of a raw SDK string. The wire values are
// the Claude SDK permission-mode strings, kept for wire compatibility (both the
// runner's turn body and the persisted autopilot-spec overrides key off "mode").
// The runner maps the policy per-backend; a backend that cannot honor it (e.g.
// opencode-server, whose interactive client owns its own permission modal)
// ignores it rather than failing — documented at the runner's Agent seam, not
// silent.
type ApprovalPolicy string

const (
	// ApprovalDefault asks before each tool use (SDK "default").
	ApprovalDefault ApprovalPolicy = "default"
	// ApprovalAcceptEdits auto-accepts file edits (SDK "acceptEdits").
	ApprovalAcceptEdits ApprovalPolicy = "acceptEdits"
	// ApprovalPlan is read-only planning (SDK "plan").
	ApprovalPlan ApprovalPolicy = "plan"
	// ApprovalBypass skips per-tool permission prompts (SDK "bypassPermissions",
	// "yolo"). It is the runner default (since 2026-07-12); still hard-gated by
	// the SDK's allowDangerouslySkipPermissions + IS_SANDBOX check.
	ApprovalBypass ApprovalPolicy = "bypassPermissions"
)

// State is the observed state of a remote session, mirroring the runner's
// session.json plus Kubernetes pod/Sandbox state.
type State struct {
	ID          ID     `json:"id"`
	Backend     string `json:"backend"`
	ProjectPath string `json:"projectPath"`
	// WorkspacePath is the pod's bind-mount / SDK cwd, recovered from the pod's
	// PROJECT_PATH env. Empty means it equals ProjectPath (no worktree). See
	// Spec.WorkspacePath. Attach resolves the local Mutagen endpoint from this.
	WorkspacePath string `json:"workspacePath,omitempty"`
	Model         string `json:"model,omitempty"`
	// Status is the session's lifecycle state (CREATING/RUNNING/SUSPENDED/FAILED/
	// GONE/UNKNOWN). It is owned by the k8s backend (pod/Sandbox state); the runner
	// does NOT report it, so a runner-sourced State leaves it empty. This is the
	// single vocabulary for this field (D9) — the runner's turn-activity signal is
	// carried separately on Activity.
	Status Status `json:"status,omitempty"`
	// Activity is the runner-reported turn activity (idle/busy/error), populated
	// only by the runner client's SessionState from GET /status (the k8s backend
	// cannot know it and leaves it empty). Distinct from Status (D9).
	Activity Activity `json:"activity,omitempty"`
	// AgentSessionID is the backend's own resume id — the Claude SDK session UUID
	// for a claude-sdk session, or the opencode session id — reported by the
	// runner's session.json. One backend per session ⇒ one resume id. It is
	// carried for resume/inspection; the local index/history path persists it
	// separately (see internal/index.Entry.AgentSessionID).
	AgentSessionID string `json:"agentSession,omitempty"`
	// LastTurnID is the most recently started turn, which persists after the
	// turn finishes (the runner uses it to seed the next turn id). It does NOT
	// mean a turn is running — that is ActiveTurnID.
	LastTurnID TurnID `json:"lastTurnId,omitempty"`
	// ActiveTurnID is the currently running turn, empty when the session is
	// idle. Only populated from the runner's live registry (GET /status); the
	// k8s backend cannot know it.
	ActiveTurnID TurnID    `json:"activeTurnId,omitempty"`
	LastActivity time.Time `json:"lastActivity,omitempty"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
	PodName      string    `json:"podName,omitempty"`
	PodReady     bool      `json:"podReady,omitempty"`
	SandboxName  string    `json:"sandboxName,omitempty"`
	RunnerToken  string    `json:"-"`
	// Capabilities is the backend capability map reported by the runner on
	// GET /sessions/:id/status. It is populated only by the runner client's
	// SessionState (the k8s backend cannot know it and leaves it zero). The TUI
	// reads Capabilities.Autopilot to pick the autopilot code path (runner-owned
	// driver vs the local tea.Tick fallback — ADR §Q3 precedence).
	Capabilities Capabilities `json:"capabilities,omitempty"`
}

// Capabilities is the backend capability map from GET /status (mirrors the
// StatusResponse.capabilities object in runner/src/types.ts). Autopilot is true
// when this backend has a runner-side autopilot driver (the server-side
// /loop-/goal loop); today only claude-sdk reports true.
type Capabilities struct {
	Autopilot bool `json:"autopilot"`
}

// TurnInput is the user input that starts a turn.
type TurnInput struct {
	Prompt       string   `json:"prompt"`
	Resume       TurnID   `json:"resume,omitempty"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	// ApprovalPolicy is the tool-approval policy the turn runs under, an owned
	// enum (ApprovalDefault/ApprovalAcceptEdits/ApprovalPlan/ApprovalBypass) rather
	// than a raw SDK string. Empty means the runner applies its default
	// (ApprovalBypass since 2026-07-12 — the sandbox pod is the isolation
	// boundary; see docs/runner-api.md). The wire key stays "mode" and the wire
	// values are the SDK permission-mode strings, so this maps 1:1 for the
	// claude-sdk backend; the runner maps it per-backend and a backend that does
	// not honor it (opencode owns its own permission modal) ignores it.
	ApprovalPolicy ApprovalPolicy `json:"mode,omitempty"`
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
	// Advisor requests the SDK "advisor" tool for this turn (the in-session
	// /advisor toggle): a stronger model the executor may consult on hard calls.
	// The runner honors it once the pinned @anthropic-ai/claude-agent-sdk exposes
	// an advisor option (v0.3.181 does not — the field is a harmless no-op there,
	// so the wire/TUI plumbing is ready without breaking current turns).
	Advisor bool `json:"advisor,omitempty"`
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

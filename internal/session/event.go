package session

import "encoding/json"

// EventType is the normalized event type enum. Every event has a monotonic
// seq and is persisted in the runner's events.db before being sent to clients.
//
// The EventType consts and AllEventTypes live in eventtypes.gen.go, generated
// from schema/events.json by cmd/gen-eventschema (run `just gen`). The payload
// structs below stay hand-written so Go keeps its nuances (*int, omitempty) but
// are validated field-for-field against the schema by schema_test.go.
type EventType string

// EventStreamLive is a CLIENT-INTERNAL stream marker, NOT a persisted event: it
// is never written to events.db and is deliberately absent from AllEventTypes /
// schema/events.json (so the schema drift gate ignores it). The runner writes a
// `: replay-complete` SSE comment once it finishes replaying history to a freshly
// connected client; the RunnerClient surfaces that comment as an Event of this
// type so the TUI can flip out of its "loading transcript…" replay state into the
// live tail (Workstream C: replay/live boundary). It carries no payload.
const EventStreamLive EventType = "stream.live"

// Event is a single normalized event in the session event log. Payload is
// type-specific JSON; consumers decode it based on Type.
type Event struct {
	Seq       uint64          `json:"seq"`
	Time      string          `json:"time"` // RFC3339
	SessionID ID              `json:"sessionId"`
	TurnID    TurnID          `json:"turnId,omitempty"`
	Type      EventType       `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// MessagePayload is the payload for message.* events.
type MessagePayload struct {
	Role    string `json:"role"`            // "user" | "assistant"
	Content string `json:"content"`         // text content or delta
	Delta   bool   `json:"delta,omitempty"` // true for message.delta
}

// ToolPayload is the payload for tool.* events.
type ToolPayload struct {
	Tool            string          `json:"tool"`                  // "Bash", "Edit", etc.
	Input           json.RawMessage `json:"input,omitempty"`       // tool input
	Output          string          `json:"output,omitempty"`      // tool output (completed)
	PartialJSON     string          `json:"partialJson,omitempty"` // tool.delta: a chunk of the tool's input JSON as it streams (input_json_delta)
	ExitCode        *int            `json:"exitCode,omitempty"`    // Bash exit code
	Error           string          `json:"error,omitempty"`
	ToolUseID       string          `json:"toolUseId,omitempty"`       // this tool_use block's id
	ParentToolUseID string          `json:"parentToolUseId,omitempty"` // Task tool_use id that spawned a subagent's child tool (empty on main thread)
	AgentName       string          `json:"agentName,omitempty"`       // subagent_type for a Task tool (the dispatched agent name)
}

// PermissionPayload is the payload for permission.* events.
type PermissionPayload struct {
	PermissionID string          `json:"permissionId"`
	Tool         string          `json:"tool"`
	Input        json.RawMessage `json:"input"`
	Decision     string          `json:"decision,omitempty"` // "allow-once" | "allow-session" | "deny" | "" (pending)
}

// UsagePayload is the payload for usage.updated events.
type UsagePayload struct {
	InputTokens      int     `json:"inputTokens"`
	OutputTokens     int     `json:"outputTokens"`
	CacheReadTokens  int     `json:"cacheReadTokens"`
	CacheWriteTokens int     `json:"cacheWriteTokens"`
	TotalCostUSD     float64 `json:"totalCostUsd"`
}

// RateLimitPayload is the payload for rate_limit.updated events: the claude.ai
// plan usage windows (5-hour + weekly, plus the optional per-model weekly
// Opus/Sonnet caps) and their reset instants, sourced from the SDK's structured
// /usage data. Available is false for API-key/Bedrock/Vertex sessions (the TUI
// hides the windows then rather than fabricating values). Utilizations are
// 0-100; ResetsAt are RFC3339 and may be empty when the SDK reports null. The
// per-model *Util pointers are nil unless the plan has a separate cap for that
// model — a non-nil pointer to 0 means present at 0% (distinct from absent). See
// the runner's fetchAndEmitRateLimits in runner/src/claude.ts.
type RateLimitPayload struct {
	Available bool `json:"available"`
	// SubscriptionType is the claude.ai plan ('pro'/'max'/'team'/'enterprise'),
	// or empty for headless setup-token / API-key / 3P sessions. When Available
	// is false and this is empty, the status line shows "usage n/a (headless
	// auth)" rather than a bare blank row.
	SubscriptionType string  `json:"subscriptionType,omitempty"`
	FiveHourUtil     float64 `json:"fiveHourUtil"`
	FiveHourResetsAt string  `json:"fiveHourResetsAt,omitempty"`
	SevenDayUtil     float64 `json:"sevenDayUtil"`
	SevenDayResetsAt string  `json:"sevenDayResetsAt,omitempty"`
	// Per-model weekly windows (Max plans). Nil pointer => the plan has no
	// separate cap for that model; a non-nil pointer to 0 => present at 0%.
	SevenDayOpusUtil       *float64 `json:"sevenDayOpusUtil,omitempty"`
	SevenDayOpusResetsAt   string   `json:"sevenDayOpusResetsAt,omitempty"`
	SevenDaySonnetUtil     *float64 `json:"sevenDaySonnetUtil,omitempty"`
	SevenDaySonnetResetsAt string   `json:"sevenDaySonnetResetsAt,omitempty"`
}

// ErrorPayload is the payload for error events.
type ErrorPayload struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// ContextCompactedPayload is the payload for context.compacted events: emitted
// when the SDK compacts (summarizes) the conversation to fit the context window
// (the SDK's compact_boundary system message). The TUI resets the ctx% gauge to
// PostTokens (when reported) so it reflects the post-compaction size instead of
// the stale pre-compaction count, and drops a one-line "context compacted"
// marker so a long run's compaction is visible in scrollback.
type ContextCompactedPayload struct {
	Trigger    string `json:"trigger"`
	PreTokens  int    `json:"preTokens"`
	PostTokens int    `json:"postTokens,omitempty"`
}

// SessionStartedPayload is the payload for session.started events: the model,
// pod cwd, and applied permission mode reported by the SDK init message. The
// CLI uses model+cwd for the status line and the model id to look up the
// context-window limit (ctx%).
//
// Tools, PermissionMode, and ClaudeSessionID are emitted by the runner but not
// yet consumed by the Go CLI; they are kept on the wire for forward-compat
// (future status-line / resume features) and validated by schema_test.go.
type SessionStartedPayload struct {
	Model           string   `json:"model"`
	Cwd             string   `json:"cwd"`
	Tools           []string `json:"tools,omitempty"`
	PermissionMode  string   `json:"permissionMode,omitempty"`
	ClaudeSessionID string   `json:"claudeSessionId,omitempty"`
}

// SessionStatusPayload is the payload for session.status_changed events.
type SessionStatusPayload struct {
	Status string `json:"status"`           // "idle" | "busy" | "error"
	Reason string `json:"reason,omitempty"` // why the status changed (e.g. SessionEnd reason); M1
}

// WorkspaceStatusPayload is the payload for workspace.status events. The runner
// emits it at session start and after each turn completes so the status line can
// show the git branch + dirty marker. Skipped entirely when cwd is not a git
// repo (no event, no error).
type WorkspaceStatusPayload struct {
	Branch string `json:"branch"`
	Dirty  bool   `json:"dirty"`
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
}

// SessionTitlePayload is the payload for session.title events. The runner emits
// it once per session, after the first assistant turn completes, carrying a
// short conversation-derived summary used as the dashboard's auto title. Mirrors
// SessionTitlePayload in runner/src/types.ts.
type SessionTitlePayload struct {
	Title string `json:"title"`
}

// ModelInfo is one model from the SDK's supportedModels() list (models.available),
// used to populate the in-session /model palette dynamically instead of the
// hardcoded opus/sonnet/haiku aliases.
type ModelInfo struct {
	Value       string `json:"value"`       // model id for --model (e.g. claude-opus-4-8)
	DisplayName string `json:"displayName"` // human-readable name (e.g. "Opus 4.8")
	Description string `json:"description,omitempty"`
}

// ModelsAvailablePayload is the payload for models.available events: the list of
// models the account/SDK supports (Query.supportedModels()). Emitted once per
// session, fetched in-turn on the SDK init message (same open-control-channel
// window as rate_limit.updated). Absent until the first turn; the TUI falls back
// to the opus/sonnet/haiku aliases until it arrives.
type ModelsAvailablePayload struct {
	Models []ModelInfo `json:"models"`
}

// TodoItem is a single entry in a todo.updated list, mirroring the SDK
// TodoWrite tool's items.
type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`               // "pending" | "in_progress" | "completed"
	ActiveForm string `json:"activeForm,omitempty"` // present-tense form shown while in_progress
}

// TodoUpdatedPayload is the payload for todo.updated events: the agent's current
// task list, emitted by the runner whenever the SDK TodoWrite tool runs. Each
// event carries the full list (it replaces any prior list). The TUI renders it
// as a checklist.
type TodoUpdatedPayload struct {
	Todos []TodoItem `json:"todos"`
}

// TerminatingPayload is the payload for session.terminating events. The runner
// emits this when it receives SIGTERM (node drain, suspend, eviction) so the
// TUI can warn the user and the client can begin reconnecting once the pod is
// rescheduled or resumed.
type TerminatingPayload struct {
	Reason       string `json:"reason"`       // human-readable cause, e.g. "pod terminating (SIGTERM)"
	GraceSeconds int    `json:"graceSeconds"` // best-effort seconds before SIGKILL
	TurnsAborted int    `json:"turnsAborted"` // in-flight turns aborted during shutdown
}

// IdleStatus is the /sessions/:id/idle response: enough for an external reaper
// to decide whether a session has been idle (turn-done AND detached) long
// enough to suspend. IdleSince is the RFC3339 instant the session last became
// idle, or empty if it is currently active (a turn is running or a client is
// attached).
type IdleStatus struct {
	TurnActive      bool   `json:"turnActive"`
	AttachedClients int    `json:"attachedClients"`
	IdleSince       string `json:"idleSince,omitempty"`
}

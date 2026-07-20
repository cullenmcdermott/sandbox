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

// TurnStartedPayload is the payload for turn.started events: the prompt that
// drives this turn. Prompt is the user's message when the runner knows it (the
// claude-pane observer's UserPromptSubmit hook, the opencode headless first
// turn) and is empty when the runner only mirrors an externally-driven turn
// whose input the attached client owns. Backends that know the prompt also
// emit a message.started/completed role:"user" echo of it, so consumers render
// the user block off that shared path rather than off this payload.
type TurnStartedPayload struct {
	Prompt string `json:"prompt,omitempty"`
}

// TurnCompletedPayload is the payload for turn.completed events: the terminal
// result summary. Result is the complete final assistant message when the
// backend reports one (the claude-pane observer's Stop hook); the opencode and
// codex observers emit turn.completed with an empty payload (their result text
// arrives via message.completed instead).
type TurnCompletedPayload struct {
	Result string `json:"result,omitempty"` // final assistant result text; absent when not reported
}

// TurnFailedPayload is the payload for turn.failed events: a turn that ended in
// error. Message is always present (a human-readable reason; the runner also
// emits a parallel error event with the same text). Subtype and Errors are the
// richer Claude result-path detail and are absent on the opencode backend and
// the Claude mid-turn failure path, which emit Message only. The TUI renders
// Message.
type TurnFailedPayload struct {
	Message string   `json:"message"`
	Subtype string   `json:"subtype,omitempty"` // SDK result subtype (Claude result path only)
	Errors  []string `json:"errors,omitempty"`  // individual SDK error strings (Claude result path only)
}

// TurnInterruptedPayload is the payload for turn.interrupted events: an
// in-flight turn torn down before it completed. Reason names the cause (e.g.
// "client interrupt", "runner restart", "opencode observer stream ended").
type TurnInterruptedPayload struct {
	Reason string `json:"reason"`
}

// Citation is one source citation attached to an assistant message
// (message.completed only), flattened from the SDK's citation location shapes
// to what a footnote render needs (§2b gap 6).
type Citation struct {
	URL       string `json:"url,omitempty"`       // source URL (web citations)
	Title     string `json:"title,omitempty"`     // source / document title
	CitedText string `json:"citedText,omitempty"` // quoted snippet, mapper-capped
}

// MessagePayload is the payload for message.* events (reasoning.* events reuse
// this shape, carrying Content only). ParentToolUseID marks a subagent's
// stream: when set, the chunk belongs to the Task tool_use id named there, and
// consumers must route it to that subagent's presentation — never into the
// main streaming transcript, where it would interleave with (and corrupt) the
// main reply (§2b gap 1).
type MessagePayload struct {
	Role            string     `json:"role"`                      // "user" | "assistant"
	Content         string     `json:"content"`                   // text content or delta
	Delta           bool       `json:"delta,omitempty"`           // true for message.delta
	ParentToolUseID string     `json:"parentToolUseId,omitempty"` // Task tool_use id that spawned the emitting subagent (empty on the main thread)
	Citations       []Citation `json:"citations,omitempty"`       // sources cited by this text (message.completed only, §2b gap 6)
}

// ToolPayload is the payload for tool.* events.
type ToolPayload struct {
	Tool            string          `json:"tool"`               // "Bash", "Edit", etc.
	Input           json.RawMessage `json:"input,omitempty"`    // tool input
	Output          string          `json:"output,omitempty"`   // tool output (completed)
	ExitCode        *int            `json:"exitCode,omitempty"` // Bash exit code
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

// ContextCompactedPayload is the payload for context.compacted events: the
// conversation was compacted (summarized) to fit the context window. The TUI
// resets the ctx% gauge to PostTokens (when reported) so it reflects the
// post-compaction size instead of the stale pre-compaction count. No backend
// currently emits this (the claude SDK engine that did was removed by
// claude-pane-first); the vocabulary + consumer are retained because in-pane
// compaction is observable and an observer may re-emit it.
type ContextCompactedPayload struct {
	Trigger    string `json:"trigger"`
	PreTokens  int    `json:"preTokens"`
	PostTokens int    `json:"postTokens,omitempty"`
}

// SessionStartedPayload is the payload for session.started events: the model
// (and, when known, cwd + backend resume id) reported by a backend observer.
// The opencode and claude-pane observers re-emit it on every model CHANGE
// (deduped) so the Go read-model resolves a context-window limit for the ctx%
// indicator and the dashboard chip; Cwd may be empty for observer-mirrored
// sessions. AgentSessionID is the backend's resume id: the dashboard read
// model persists it to the local session index (SaveAgentSessionID) so the
// session stays resumable from the laptop; it is the SSE-payload analogue of
// State.AgentSessionID.
type SessionStartedPayload struct {
	Model          string `json:"model"`
	Cwd            string `json:"cwd"`
	AgentSessionID string `json:"agentSessionId,omitempty"`
}

// SessionStatusPayload is the payload for session.status_changed events.
type SessionStatusPayload struct {
	Status string `json:"status"`           // "idle" | "busy" | "error"
	Reason string `json:"reason,omitempty"` // why the status changed (e.g. SessionEnd reason); M1
}

// WorkspaceStatusPayload is the payload for workspace.status events: git
// branch + dirty/ahead/behind for the session row. No backend currently emits
// this (the claude SDK engine that did was removed by claude-pane-first); the
// vocabulary + consumer (the Branch/Dirty read-model) are retained because the
// pane statusline / backend observers can re-emit it.
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

package session

import "encoding/json"

// EventType is the normalized event type enum. Every event has a monotonic
// seq and is persisted in the runner's events.db before being sent to clients.
type EventType string

const (
	EventSessionStarted       EventType = "session.started"
	EventSessionStatusChanged EventType = "session.status_changed"
	EventTurnStarted          EventType = "turn.started"
	EventTurnCompleted        EventType = "turn.completed"
	EventTurnFailed           EventType = "turn.failed"
	EventTurnInterrupted      EventType = "turn.interrupted"
	EventMessageStarted       EventType = "message.started"
	EventMessageDelta         EventType = "message.delta"
	EventMessageCompleted     EventType = "message.completed"
	EventReasoningStarted     EventType = "reasoning.started"
	EventReasoningDelta       EventType = "reasoning.delta"
	EventReasoningCompleted   EventType = "reasoning.completed"
	EventToolStarted          EventType = "tool.started"
	EventToolDelta            EventType = "tool.delta"
	EventToolCompleted        EventType = "tool.completed"
	EventToolFailed           EventType = "tool.failed"
	EventPermissionRequested  EventType = "permission.requested"
	EventPermissionResolved   EventType = "permission.resolved"
	EventTodoUpdated          EventType = "todo.updated"
	EventDiffUpdated          EventType = "diff.updated"
	EventUsageUpdated         EventType = "usage.updated"
	EventSyncStatusChanged    EventType = "sync.status_changed"
	EventError                EventType = "error"
)

// Event is a single normalized event in the session event log. Payload is
// type-specific JSON; consumers decode it based on Type.
type Event struct {
	Seq       uint64           `json:"seq"`
	Time      string           `json:"time"` // RFC3339
	SessionID ID               `json:"session_id"`
	TurnID    TurnID           `json:"turn_id,omitempty"`
	Type      EventType        `json:"type"`
	Payload   json.RawMessage  `json:"payload"`
}

// MessagePayload is the payload for message.* events.
type MessagePayload struct {
	Role    string `json:"role"`              // "user" | "assistant"
	Content string `json:"content"`           // text content or delta
	Delta   bool   `json:"delta,omitempty"`   // true for message.delta
}

// ToolPayload is the payload for tool.* events.
type ToolPayload struct {
	Tool     string          `json:"tool"`              // "Bash", "Edit", etc.
	Input    json.RawMessage `json:"input,omitempty"`   // tool input
	Output   string          `json:"output,omitempty"`  // tool output (completed)
	ExitCode *int            `json:"exitCode,omitempty"` // Bash exit code
	Error    string          `json:"error,omitempty"`
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
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	CacheReadTokens   int `json:"cacheReadTokens"`
	CacheWriteTokens  int `json:"cacheWriteTokens"`
	TotalCostUSD      float64 `json:"totalCostUsd"`
}

// ErrorPayload is the payload for error events.
type ErrorPayload struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// SessionStatusPayload is the payload for session.status_changed events.
type SessionStatusPayload struct {
	Status string `json:"status"` // "idle" | "busy" | "error"
}

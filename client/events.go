package client

import "github.com/cullenmcdermott/sandbox/internal/session"

// EventType identifies a normalized event kind. Branch on Event.Type using the
// re-exported constants below; decode Event.Payload into the matching payload
// alias.
type EventType = session.EventType

// Event type constants — the full normalized event vocabulary (mirrors
// schema/events.json), plus the client-internal stream marker.
const (
	EventSessionStarted       = session.EventSessionStarted
	EventSessionStatusChanged = session.EventSessionStatusChanged
	EventSessionTerminating   = session.EventSessionTerminating
	EventTurnStarted          = session.EventTurnStarted
	EventTurnCompleted        = session.EventTurnCompleted
	EventTurnFailed           = session.EventTurnFailed
	EventTurnInterrupted      = session.EventTurnInterrupted
	EventMessageStarted       = session.EventMessageStarted
	EventMessageDelta         = session.EventMessageDelta
	EventMessageCompleted     = session.EventMessageCompleted
	EventReasoningStarted     = session.EventReasoningStarted
	EventReasoningDelta       = session.EventReasoningDelta
	EventReasoningCompleted   = session.EventReasoningCompleted
	EventToolStarted          = session.EventToolStarted
	EventToolDelta            = session.EventToolDelta
	EventToolProgress         = session.EventToolProgress
	EventToolCompleted        = session.EventToolCompleted
	EventToolFailed           = session.EventToolFailed
	EventPermissionRequested  = session.EventPermissionRequested
	EventPermissionResolved   = session.EventPermissionResolved
	EventTodoUpdated          = session.EventTodoUpdated
	EventUsageUpdated         = session.EventUsageUpdated
	EventContextCompacted     = session.EventContextCompacted
	EventRateLimitUpdated     = session.EventRateLimitUpdated
	EventWorkspaceStatus      = session.EventWorkspaceStatus
	EventSessionTitle         = session.EventSessionTitle
	EventModelsAvailable      = session.EventModelsAvailable
	EventAutopilotState       = session.EventAutopilotState
	EventError                = session.EventError

	// EventStreamLive is a client-internal marker (no seq, not persisted) emitted
	// once the replay backlog is drained and the stream has caught up to live. It
	// is NOT a runner event — treat it as a boundary signal, not data.
	EventStreamLive = session.EventStreamLive
)

// AllEventTypes is every persisted event type, in schema order (excludes the
// client-internal EventStreamLive marker).
var AllEventTypes = session.AllEventTypes

// Payload struct aliases. Decode Event.Payload into the one matching Event.Type
// (e.g. EventMessageCompleted → MessagePayload, EventToolCompleted → ToolPayload).
type (
	MessagePayload          = session.MessagePayload
	Citation                = session.Citation
	ToolPayload             = session.ToolPayload
	PermissionPayload       = session.PermissionPayload
	UsagePayload            = session.UsagePayload
	ContextCompactedPayload = session.ContextCompactedPayload
	RateLimitPayload        = session.RateLimitPayload
	ErrorPayload            = session.ErrorPayload
	SessionStartedPayload   = session.SessionStartedPayload
	SessionStatusPayload    = session.SessionStatusPayload
	WorkspaceStatusPayload  = session.WorkspaceStatusPayload
	SessionTitlePayload     = session.SessionTitlePayload
	ModelInfo               = session.ModelInfo
	ModelsAvailablePayload  = session.ModelsAvailablePayload
	TodoItem                = session.TodoItem
	TodoUpdatedPayload      = session.TodoUpdatedPayload
	TurnStartedPayload      = session.TurnStartedPayload
	TurnCompletedPayload    = session.TurnCompletedPayload
	TurnFailedPayload       = session.TurnFailedPayload
	TurnInterruptedPayload  = session.TurnInterruptedPayload
	TerminatingPayload      = session.TerminatingPayload
	AutopilotStatePayload   = session.AutopilotStatePayload
)

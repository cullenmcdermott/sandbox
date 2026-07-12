package dashboard

import (
	"encoding/json"

	"github.com/cullenmcdermott/sandbox/internal/models"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// sessionReadModel is the single derived read-model state that BOTH the dashboard
// list Session and the attached TranscriptModel keep from the runner SSE stream.
// It is EMBEDDED by both, so at runtime each instance holds its own copy (the
// list row and the transcript are fed the same events independently) — but there
// is exactly ONE reducer (ApplyEvent) and exactly ONE unmarshal per payload type
// in the codebase. Adding a §2b event that carries read-model state means editing
// ONE switch here, not two drifting ones (handleEvent + ApplyRunnerEvent).
//
// Presentation stays on the embedding structs: the transcript's blocks/streaming/
// permission UI and the dashboard's RecentTools ring / attention routing / auto
// title are NOT here — only status, model, usage/ctx%, git, and the pending
// tool-permission descriptor, i.e. the state both surfaces derive from the same
// payloads.
type sessionReadModel struct {
	// DashStatus is the six-state view-model status (see SessionStatus). Refined
	// from a cluster-derived baseline by the turn/permission/session-status events.
	DashStatus SessionStatus

	// Model is the SDK-resolved active model id; CtxLimit is its context-window
	// (cached via models.Limit so ctx% needs no per-render lookup). cwd is the
	// pod working dir and defaultModel the first resolved id (the account/session
	// default, restored by /model-default) — both read only by the transcript
	// status line, but derived here so session.started is unmarshalled once.
	Model        string
	CtxLimit     int
	cwd          string
	defaultModel string

	// AgentSessionID is the Claude Agent SDK session UUID from session.started,
	// persisted to the index so the session is resumable from the laptop. Read
	// only by the dashboard, derived here to keep session.started single-unmarshal.
	AgentSessionID string

	// Live token/cost accounting from usage.updated (and the context.compacted
	// baseline reset). The >0 guards mirror the runner: a partial usage event must
	// not clobber a known counter with a zero.
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	TotalCostUSD     float64

	// Git workspace state from workspace.status. Ahead/Behind are read only by the
	// transcript status line; Branch/Dirty also feed the dashboard row sub-line.
	Branch string
	Dirty  bool
	Ahead  int
	Behind int

	// Pending tool-permission descriptor (from permission.requested; cleared on
	// resolution or turn end). The transcript keeps its own richer UI model
	// (transcriptPermission with plan/diff); these plain fields drive the
	// dashboard row/detail "allow X?" prompt and ride along in the snapshot.
	PendingPermissionID   string
	PendingPermissionTool string
	PendingPermissionArg  string
}

// clearPendingPermission resets the pending-permission descriptor (on resolution
// or turn end).
func (rm *sessionReadModel) clearPendingPermission() {
	rm.PendingPermissionID = ""
	rm.PendingPermissionTool = ""
	rm.PendingPermissionArg = ""
}

// readModelResult reports what ApplyEvent did so the embedding reducers can layer
// their own presentation (transcript blocks/permission UI, dashboard attention)
// WITHOUT re-unmarshalling the payload. Zero value = nothing to layer.
type readModelResult struct {
	// compacted carries the parsed context.compacted payload (nil otherwise) so the
	// transcript can render its "context compacted · N→M tokens" scrollback marker.
	compacted *session.ContextCompactedPayload
	// permission carries the parsed permission.requested payload (nil otherwise) so
	// the transcript can build its rich plan/diff permission card from p.Tool/p.Input.
	permission *session.PermissionPayload
	// statusReason is the reason from a session.status "error" transition (empty
	// otherwise) so the transcript can surface it as an error block.
	statusReason string
}

// ApplyEvent is the single reducer for the shared read-model state. It unmarshals
// each shared payload exactly once and updates rm in place; every event type not
// listed is a no-op (safe for delta/tool/message/reasoning/etc. and for
// protocol-version skew). It returns a readModelResult so the caller can layer
// presentation without re-parsing.
//
// Status transitions follow the six-state reducer:
//
//	turn.started / session.status "busy" → busy
//	permission.requested                 → waiting  (captures the descriptor)
//	permission.resolved                  → busy     (turn still active; cleared)
//	turn.completed / .interrupted        → needs-input
//	turn.failed / session.status "error" → failed
//	session.status "idle"                → needs-input (unless already failed)
//
// The caller must set a cluster-derived baseline (idle/suspended/failed) before
// the first event; ApplyEvent only refines it.
func (rm *sessionReadModel) ApplyEvent(ev session.Event) readModelResult {
	var res readModelResult
	switch ev.Type {
	case session.EventSessionStarted:
		var p session.SessionStartedPayload
		_ = json.Unmarshal(ev.Payload, &p) // malformed → zero value → no-op
		if p.Model != "" {
			rm.Model = p.Model
			rm.CtxLimit = models.Limit(p.Model).ContextLimit
			if rm.defaultModel == "" {
				// First resolved id is the account/session default; remember it so
				// /model-default can restore the status line to it.
				rm.defaultModel = p.Model
			}
		}
		if p.Cwd != "" {
			rm.cwd = p.Cwd
		}
		if p.AgentSessionID != "" {
			rm.AgentSessionID = p.AgentSessionID
		}

	case session.EventWorkspaceStatus:
		var p session.WorkspaceStatusPayload
		_ = json.Unmarshal(ev.Payload, &p)
		// An empty branch (cwd not a git repo) leaves the slot blank rather than
		// clobbering a previously-known branch.
		if p.Branch != "" {
			rm.Branch = p.Branch
			rm.Dirty = p.Dirty
			rm.Ahead = p.Ahead
			rm.Behind = p.Behind
		}

	case session.EventUsageUpdated:
		var p session.UsagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		// D12: refresh the input/cache counters when the event carries ANY token of
		// them, not only when InputTokens>0. A provider can bill an entire turn as
		// cache-read with zero fresh input (plausible on opencode) — gating on
		// InputTokens alone left ctx% frozen for that case. Still skip an all-zero
		// usage.updated so an intermediate zero-token event can't clobber the counts.
		if p.InputTokens > 0 || p.CacheReadTokens > 0 || p.CacheWriteTokens > 0 {
			rm.InputTokens = p.InputTokens
			rm.CacheReadTokens = p.CacheReadTokens
			rm.CacheWriteTokens = p.CacheWriteTokens
		}
		if p.OutputTokens > 0 {
			rm.OutputTokens = p.OutputTokens
		}
		if p.TotalCostUSD > 0 {
			rm.TotalCostUSD = p.TotalCostUSD
		}

	case session.EventContextCompacted:
		var p session.ContextCompactedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		// Reset the ctx% baseline to the post-compaction size; PostTokens is
		// optional on the wire, so when absent (0) leave the counters untouched (a
		// usage.updated refreshes them on the next turn).
		if p.PostTokens > 0 {
			rm.InputTokens = p.PostTokens
			rm.CacheReadTokens = 0
			rm.CacheWriteTokens = 0
		}
		res.compacted = &p

	case session.EventSessionStatusChanged:
		var p session.SessionStatusPayload
		_ = json.Unmarshal(ev.Payload, &p)
		switch p.Status {
		case "busy":
			rm.DashStatus = StatusBusy
			rm.clearPendingPermission()
		case "idle":
			// idle after error must not mask the failure.
			if rm.DashStatus != StatusFailed {
				rm.DashStatus = StatusNeedsInput
				rm.clearPendingPermission()
			}
		case "error":
			rm.DashStatus = StatusFailed
			rm.clearPendingPermission()
			res.statusReason = p.Reason
		}

	case session.EventTurnStarted:
		rm.DashStatus = StatusBusy
		rm.clearPendingPermission()

	case session.EventPermissionRequested:
		var p session.PermissionPayload
		_ = json.Unmarshal(ev.Payload, &p)
		rm.DashStatus = StatusWaiting
		rm.PendingPermissionID = p.PermissionID
		rm.PendingPermissionTool = p.Tool
		rm.PendingPermissionArg = toolArg(p.Tool, p.Input)
		res.permission = &p

	case session.EventPermissionResolved:
		// Turn is still active; revert to busy (the turn continues).
		rm.DashStatus = StatusBusy
		rm.clearPendingPermission()

	case session.EventTurnCompleted, session.EventTurnInterrupted:
		rm.DashStatus = StatusNeedsInput
		rm.clearPendingPermission()

	case session.EventTurnFailed:
		rm.DashStatus = StatusFailed
		rm.clearPendingPermission()
	}
	return res
}

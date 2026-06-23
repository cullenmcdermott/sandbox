package dashboard

import (
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/models"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// ToolRef is one entry in a session's recent-tool ring (Phase 4): the real tool
// name plus its primary arg as extracted by toolArg (e.g. {"Edit",
// "internal/sync/mutagen.go"}). Newest entries are appended to the tail.
type ToolRef struct {
	Tool string
	Arg  string
}

// recentToolsCap bounds the per-session recent-tool ring. The detail pane shows
// the last ~3; the ring holds a little extra slack.
const recentToolsCap = 5

// SessionStatus is the six-state view-model status for the dashboard. It is
// derived from session.State.Status (cluster level) and, in Phase B, from
// live runner SSE events. It intentionally does NOT modify session.Status.
type SessionStatus int

const (
	// StatusIdle: pod up, no turn running, nothing pending.
	StatusIdle SessionStatus = iota
	// StatusBusy: a turn is actively running (spinner animates).
	StatusBusy
	// StatusWaiting: a tool permission is pending ("needs you").
	StatusWaiting
	// StatusNeedsInput: turn finished, awaiting next user prompt.
	StatusNeedsInput
	// StatusSuspended: pod scaled to zero (replicas=0), recoverable.
	StatusSuspended
	// StatusFailed: pod crashed or runner unreachable.
	StatusFailed
)

// String returns the human-readable label for the status.
func (s SessionStatus) String() string {
	switch s {
	case StatusBusy:
		return "busy"
	case StatusWaiting:
		return "waiting"
	case StatusNeedsInput:
		return "needs-input"
	case StatusIdle:
		return "idle"
	case StatusSuspended:
		return "suspended"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Glyph returns the single Unicode glyph that identifies this status. The
// glyph distinguishes states for colour-blind users and no-colour TTYs.
func (s SessionStatus) Glyph() string {
	switch s {
	case StatusBusy:
		return theme.GlyphBusy
	case StatusWaiting:
		return theme.GlyphWaiting
	case StatusNeedsInput:
		return theme.GlyphNeedsInput
	case StatusIdle:
		return theme.GlyphIdle
	case StatusSuspended:
		return theme.GlyphSuspended
	case StatusFailed:
		return theme.GlyphFailed
	default:
		return "?"
	}
}

// Session is the dashboard read-model for a single agent session. It wraps
// session.State with the derived SessionStatus and display-ready fields.
type Session struct {
	// State is the authoritative cluster state (Status, PodReady, etc.).
	State session.State

	// DashStatus is the six-state view-model status derived from State.Status
	// and (Phase B) live runner events.
	DashStatus SessionStatus

	// Title is the derived display name: the project path basename. It is the
	// final fallback in DisplayTitle, behind RenamedTitle and AutoTitle (the
	// runner-generated summary lives in AutoTitle, not here).
	Title string

	// Model is the active model id reported by the backend (e.g. "opus-4.8" or
	// "kimi-k2"). Surfaced in the list/detail and the external pane status row;
	// populated from State.Model (Seam C).
	Model string

	// RenamedTitle is a user-supplied human label. When non-empty it overrides
	// Title for display.
	RenamedTitle string

	// AutoTitle is the runner-generated conversation summary (T6). It overrides
	// the derived Title but yields to a user-supplied RenamedTitle. Populated
	// from the session.title SSE event and restored on seed from the index.
	AutoTitle string

	// ClaudeSessionID is the Claude Agent SDK session UUID from the session.started
	// event. Persisted to the index so the CLI can write a local history.jsonl
	// entry (making the session resumable from the laptop's `claude --resume`).
	ClaudeSessionID string

	// Archived marks a finished session (needs-input after a completed turn)
	// that the user has archived into a separate section.
	Archived bool

	// BusyFrame is the current spinner frame index for busy sessions.
	BusyFrame int

	// PendingAction is the in-progress suspend/resume/destroy action, set
	// optimistically when the user triggers it and cleared on actionResultMsg.
	// Non-empty means the row should show an animated "suspending…" label.
	PendingAction string

	// PendingPermissionID is the permissionId from the most recent
	// permission.requested event. It is cleared on permission.resolved or
	// when the turn ends. Non-empty only when DashStatus == StatusWaiting.
	PendingPermissionID string

	// PendingPermissionTool is the tool name from the pending permission event,
	// used for the detail pane "allow X?" prompt.
	PendingPermissionTool string

	// statusChangedAt is when DashStatus last changed, used to fade the row's
	// glyph in over a short window (the fresh-data charm touch). Zero means no
	// fade (e.g. initial seed).
	statusChangedAt time.Time

	// --- Live usage metrics (Phase 3) ------------------------------------
	// Updated from usage.updated SSE events. Drive the row/detail ctx% and the
	// detail cost line. CtxLimit is the model's context window (cached from
	// models.Limit on session.started / seed) so CtxPercent needs no lookup.
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	TotalCostUSD     float64
	CtxLimit         int

	// RecentTools is the main-thread tool ring (Phase 4), oldest→newest. Capped
	// at recentToolsCap; the detail pane renders the last few newest-first.
	RecentTools []ToolRef
}

// CtxPercent returns the rounded context-window utilization (0–100) for the
// session, or 0 when the limit is unknown. Per the redesign spec it counts
// input + cache-read + cache-write tokens against the model's context limit.
func (s Session) CtxPercent() int {
	if s.CtxLimit <= 0 {
		return 0
	}
	used := s.InputTokens + s.CacheReadTokens + s.CacheWriteTokens
	pct := used * 100 / s.CtxLimit
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return pct
}

// ShortID returns the first 4 hex chars of the session id — the row
// disambiguator (there is no git-branch field in the data model, P2).
func (s Session) ShortID() string {
	id := string(s.ID())
	if len(id) > 4 {
		return id[:4]
	}
	return id
}

// DisplayTitle returns the title to show for the session. Precedence: a
// user-supplied RenamedTitle always wins, then the runner-generated AutoTitle,
// then the derived Title (project basename).
func (s Session) DisplayTitle() string {
	if s.RenamedTitle != "" {
		return s.RenamedTitle
	}
	if s.AutoTitle != "" {
		return s.AutoTitle
	}
	return s.Title
}

// ID returns the session.ID shorthand.
func (s Session) ID() session.ID { return s.State.ID }

// ClientLabel maps a backend id to the short friendly client name shown in the
// UI ("claude" / "opencode"), falling back to the raw id for unknown backends.
func ClientLabel(backend string) string {
	switch backend {
	case session.BackendOpenCode:
		return "opencode"
	case session.BackendClaudeSDK:
		return "claude"
	default:
		return backend
	}
}

// AgentLabel is the "<model> · <client>" descriptor for the list row, collapsing
// to just the client when no model is known yet (e.g. before session.started).
func (s Session) AgentLabel() string {
	client := ClientLabel(s.State.Backend)
	if s.Model != "" {
		return s.Model + " · " + client
	}
	return client
}

// DeriveStatus maps a session.State to the six-state dashboard SessionStatus.
// It is the cluster-derived baseline; live runner events (ApplyRunnerEvent)
// refine a running session to busy/waiting/needs-input.
//
//	SUSPENDED → StatusSuspended
//	FAILED    → StatusFailed
//	RUNNING / CREATING → StatusIdle
//	GONE      → dropped by caller (not included in read-model)
//
// A starting pod (CREATING, or RUNNING before PodReady) maps to idle rather
// than a fake spinner: the cluster watch can't read pod readiness, so the
// honest baseline is idle until the live runner stream reports otherwise.
func DeriveStatus(st session.State) SessionStatus {
	switch st.Status {
	case session.StatusSuspended:
		return StatusSuspended
	case session.StatusFailed:
		return StatusFailed
	case session.StatusRunning, session.StatusCreating:
		return StatusIdle
	default:
		// StatusGone, StatusUnknown: caller drops these.
		return StatusIdle
	}
}

// deriveTitle produces the fallback display title from the session's project
// path basename. It is used when neither a user RenamedTitle nor a
// runner-generated AutoTitle is set (see Session.DisplayTitle).
func deriveTitle(st session.State) string {
	if st.ProjectPath != "" {
		return filepath.Base(st.ProjectPath)
	}
	return string(st.ID)
}

// SessionFromState converts a session.State into a dashboard Session.
func SessionFromState(st session.State) Session {
	s := Session{
		State:      st,
		DashStatus: DeriveStatus(st),
		Title:      deriveTitle(st),
		Model:      st.Model,
	}
	if st.Model != "" {
		s.CtxLimit = models.Limit(st.Model).ContextLimit
	}
	return s
}

// --------------------------------------------------------------------------
// Live-status reducer (Phase B)
// --------------------------------------------------------------------------

// ApplyRunnerEvent updates sess in-place based on a single SSE event from the
// runner. It implements the six-state reducer:
//
//	turn.started          → busy
//	permission.requested  → waiting  (captures PendingPermissionID)
//	permission.resolved   → busy     (turn still active; ID cleared)
//	turn.completed        → needs-input
//	turn.interrupted      → needs-input
//	turn.failed           → failed
//
// Any other event type is a no-op. The DashStatus must already be set to a
// cluster-derived baseline (idle/suspended/failed) before the first event
// arrives; this function only refines it.
//
// It returns true if the status changed (so the caller can re-sort if needed).
func ApplyRunnerEvent(sess *Session, ev session.Event) bool {
	prev := sess.DashStatus
	switch ev.Type {
	case session.EventSessionStarted:
		// Capture the model id for the list/detail + external pane status row.
		// Does not change the six-state status.
		var p session.SessionStartedPayload
		_ = json.Unmarshal(ev.Payload, &p) // malformed payload → zero value → no-op
		if p.Model != "" {
			sess.Model = p.Model
			// Cache the context-window limit so the row/detail ctx% needs no
			// per-render lookup (Phase 3).
			sess.CtxLimit = models.Limit(p.Model).ContextLimit
		}
		// Capture the Claude SDK session id so it can be persisted to the index
		// (drives local resume — history.jsonl entry + `claude --resume <id>`).
		if p.ClaudeSessionID != "" {
			sess.ClaudeSessionID = p.ClaudeSessionID
		}
		return false

	case session.EventSessionTitle:
		// Runner-generated auto title (T6). Updates the display label only; does
		// not change the six-state status. Empty titles are ignored so a failed
		// summarization can't blank the derived basename.
		var p session.SessionTitlePayload
		_ = json.Unmarshal(ev.Payload, &p) // malformed payload → zero value → no-op
		if p.Title != "" {
			sess.AutoTitle = p.Title
		}
		return false

	case session.EventTurnStarted:
		sess.DashStatus = StatusBusy
		// Clear any stale permission state from a previous turn.
		sess.PendingPermissionID = ""
		sess.PendingPermissionTool = ""

	case session.EventPermissionRequested:
		var p session.PermissionPayload
		_ = json.Unmarshal(ev.Payload, &p) // malformed payload → zero value → no-op
		sess.DashStatus = StatusWaiting
		sess.PendingPermissionID = p.PermissionID
		sess.PendingPermissionTool = p.Tool

	case session.EventPermissionResolved:
		// Turn is still active; revert to busy (the turn continues).
		sess.DashStatus = StatusBusy
		sess.PendingPermissionID = ""
		sess.PendingPermissionTool = ""

	case session.EventTurnCompleted:
		sess.DashStatus = StatusNeedsInput
		sess.PendingPermissionID = ""
		sess.PendingPermissionTool = ""

	case session.EventTurnInterrupted:
		sess.DashStatus = StatusNeedsInput
		sess.PendingPermissionID = ""
		sess.PendingPermissionTool = ""

	case session.EventTurnFailed:
		sess.DashStatus = StatusFailed
		sess.PendingPermissionID = ""
		sess.PendingPermissionTool = ""

	case session.EventUsageUpdated:
		// Live token/cost accounting (Phase 3). Does not change the six-state
		// status — it only refreshes the metrics the row/detail surface.
		var p session.UsagePayload
		_ = json.Unmarshal(ev.Payload, &p) // malformed payload → zero value → no-op
		sess.InputTokens = p.InputTokens
		sess.OutputTokens = p.OutputTokens
		sess.CacheReadTokens = p.CacheReadTokens
		sess.CacheWriteTokens = p.CacheWriteTokens
		sess.TotalCostUSD = p.TotalCostUSD
		return false

	case session.EventToolStarted:
		// Recent main-thread tool activity (Phase 4). Subagent-child tools
		// (ParentToolUseID set) are skipped so the ring reflects the main thread.
		var p session.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p) // malformed payload → zero value → no-op
		if p.ParentToolUseID != "" || p.Tool == "" {
			return false
		}
		sess.RecentTools = append(sess.RecentTools, ToolRef{Tool: p.Tool, Arg: toolArg(p.Tool, p.Input)})
		if len(sess.RecentTools) > recentToolsCap {
			sess.RecentTools = sess.RecentTools[len(sess.RecentTools)-recentToolsCap:]
		}
		return false

	default:
		// All other events (message deltas, tool events, etc.) do not change
		// the six-state status.
		return false
	}
	if sess.DashStatus != prev {
		sess.statusChangedAt = time.Now()
		return true
	}
	return false
}

// relativeTime returns a compact human-readable relative time string.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := nowFunc().Sub(t) // injectable clock so golden snapshots are stable (§4.2)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return formatInt(m) + "m ago"
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return formatInt(h) + "h ago"
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return formatInt(days) + "d ago"
	}
}

// formatInt converts an int to a string without importing strconv to keep
// this file's dependency surface small.
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if negative {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

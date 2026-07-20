package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Phase 4 (opencode metrics observer): the runner's passive observer emits the
// SAME normalized events for an interactive opencode turn that a claude turn
// emits, so the backend-agnostic ApplyRunnerEvent reducer lifts an opencode
// session out of permanent "idle" and surfaces its title/model/ctx%/cost/tools —
// with NO Go-side reducer change. This guards that parity end to end.
func TestPhase4_ObserverEventsDriveOpencodeListRow(t *testing.T) {
	sess := makeSession("opencode-xyz", StatusIdle)
	sess.State.Backend = session.BackendOpenCode

	// The observer's per-cycle sequence for an interactive opencode turn.
	ApplyRunnerEvent(&sess, mkEvent(session.EventTurnStarted, nil))
	if sess.DashStatus != StatusBusy {
		t.Fatalf("turn.started must lift opencode out of idle → busy, got %v", sess.DashStatus)
	}

	ApplyRunnerEvent(&sess, mkEvent(session.EventSessionStarted, session.SessionStartedPayload{Model: "opencode/big-pickle"}))
	if sess.Model != "opencode/big-pickle" {
		t.Errorf("session.started must set Model, got %q", sess.Model)
	}
	if sess.CtxLimit <= 0 {
		t.Errorf("session.started must cache a context limit for ctx%%, got %d", sess.CtxLimit)
	}

	ApplyRunnerEvent(&sess, mkEvent(session.EventUsageUpdated, session.UsagePayload{
		InputTokens: 40000, TotalCostUSD: 0.0123,
	}))
	if sess.TotalCostUSD != 0.0123 {
		t.Errorf("usage.updated must set cost, got %v", sess.TotalCostUSD)
	}
	if sess.CtxPercent() <= 0 {
		t.Errorf("ctx%% should be > 0 once tokens + limit are known, got %d", sess.CtxPercent())
	}

	ApplyRunnerEvent(&sess, mkEvent(session.EventSessionTitle, session.SessionTitlePayload{Title: "Fix the JSON parser"}))
	if sess.DisplayTitle() != "Fix the JSON parser" {
		t.Errorf("session.title must drive the live title, got %q", sess.DisplayTitle())
	}

	ApplyRunnerEvent(&sess, mkEvent(session.EventToolStarted, session.ToolPayload{Tool: "bash", Input: []byte(`{"cmd":"ls"}`)}))
	if len(sess.RecentTools) != 1 || sess.RecentTools[0].Tool != "bash" {
		t.Errorf("tool.started must populate RecentTools, got %+v", sess.RecentTools)
	}

	ApplyRunnerEvent(&sess, mkEvent(session.EventTurnCompleted, nil))
	if sess.DashStatus != StatusNeedsInput {
		t.Errorf("turn.completed must return opencode to needs-input, got %v", sess.DashStatus)
	}
}

func TestPhase4_StatusChangedDrivesOpencodeListRow(t *testing.T) {
	sess := makeSession("opencode-xyz", StatusIdle)
	sess.State.Backend = session.BackendOpenCode

	if !ApplyRunnerEvent(&sess, mkEvent(session.EventSessionStatusChanged, session.SessionStatusPayload{Status: "busy"})) {
		t.Fatal("session.status_changed busy should report a status change")
	}
	if sess.DashStatus != StatusBusy {
		t.Fatalf("busy status_changed must lift opencode out of idle, got %v", sess.DashStatus)
	}

	ApplyRunnerEvent(&sess, mkEvent(session.EventPermissionRequested, session.PermissionPayload{PermissionID: "p1", Tool: "Bash"}))
	if sess.PendingPermissionID == "" {
		t.Fatal("test setup failed: permission should be pending")
	}

	if !ApplyRunnerEvent(&sess, mkEvent(session.EventSessionStatusChanged, session.SessionStatusPayload{Status: "idle"})) {
		t.Fatal("session.status_changed idle should report a status change")
	}
	if sess.DashStatus != StatusNeedsInput {
		t.Fatalf("idle status_changed must return opencode to needs-input, got %v", sess.DashStatus)
	}
	if sess.PendingPermissionID != "" {
		t.Fatalf("idle status_changed must clear stale permission state, got %q", sess.PendingPermissionID)
	}
}

func TestPhase4_IdleStatusChangedDoesNotMaskFailedTurn(t *testing.T) {
	sess := makeSession("opencode-xyz", StatusBusy)
	sess.State.Backend = session.BackendOpenCode

	ApplyRunnerEvent(&sess, mkEvent(session.EventTurnFailed, nil))
	if sess.DashStatus != StatusFailed {
		t.Fatalf("turn.failed should mark failed, got %v", sess.DashStatus)
	}

	changed := ApplyRunnerEvent(&sess, mkEvent(session.EventSessionStatusChanged, session.SessionStatusPayload{Status: "idle"}))
	if changed {
		t.Fatal("idle status_changed after failure should not report a status change")
	}
	if sess.DashStatus != StatusFailed {
		t.Fatalf("idle status_changed must not mask failed turn, got %v", sess.DashStatus)
	}
}

// Phase 4 (in-pane statusline): the external opencode pane's status row reads the
// LIVE dashboard read-model (fed by the passive observer stream), so it surfaces
// DisplayTitle + status + ctx% + cost — at parity with the claude statusline —
// and strips the "provider/" prefix from the model id.
func TestPhase4_ExternalPaneLiveStatusRow(t *testing.T) {
	live := Session{
		State:            session.State{ID: "opencode-xyz", Backend: session.BackendOpenCode},
		AutoTitle:        "Refactor the lexer",
		sessionReadModel: sessionReadModel{DashStatus: StatusBusy, Model: "opencode/big-pickle", CtxLimit: 200000, InputTokens: 40000, TotalCostUSD: 0.0123},
	}
	// Static snapshot is stale on purpose; the live accessor must win.
	pane := NewExternalPane(Session{
		Title:            "proj",
		sessionReadModel: sessionReadModel{Model: "stale"},
	}, OpencodeCreds{}, func() Session { return live })
	pane.w, pane.h = 120, 30

	row := pane.statusRow()
	for _, want := range []string{"Refactor the lexer", "big-pickle", "ctx 20%", "$0.0123", "busy"} {
		if !strings.Contains(row, want) {
			t.Errorf("status row missing %q:\n%s", want, row)
		}
	}
	// The stale snapshot fields must NOT appear (live accessor wins).
	if strings.Contains(row, "proj") || strings.Contains(row, "stale") {
		t.Errorf("status row showed stale snapshot instead of live session:\n%s", row)
	}
	// The provider prefix is stripped for display.
	if strings.Contains(row, "opencode/big-pickle") {
		t.Errorf("model provider prefix should be stripped:\n%s", row)
	}
}

// Phase 4: a fresh pane with no live accessor falls back to its static snapshot
// and shows no empty/zero metrics (no turn observed yet).
func TestPhase4_ExternalPaneFallsBackToSnapshot(t *testing.T) {
	pane := NewExternalPane(Session{Title: "myproj"}, OpencodeCreds{}, nil)
	pane.w, pane.h = 120, 30
	row := pane.statusRow()
	if !strings.Contains(row, "myproj") {
		t.Errorf("fallback status row should show the snapshot title, got:\n%s", row)
	}
	if strings.Contains(row, "ctx ") || strings.Contains(row, "$") {
		t.Errorf("a pane with no observed turn should show no ctx%%/cost, got:\n%s", row)
	}
}

// Task 4.4 (claude-pane status row parity): a claude-pane pane renders
// title · claude · model · status · ctx% · cost from the same live read-model
// the opencode pane uses — the runner's hook/statusline observer feeds
// session.started (model→CtxLimit), usage.updated (tokens/cost), and the
// turn lifecycle (status) through the identical reducer.
func TestClaudePaneLiveStatusRowParity(t *testing.T) {
	live := Session{
		State:     session.State{ID: "claude-pane-xyz", Backend: session.BackendClaudePane},
		AutoTitle: "Fix the build",
	}
	// Feed the read-model through the real reducer with observer-shaped events
	// (what runner/src/claude-pane-observer.ts emits), not hand-set fields.
	ApplyRunnerEvent(&live, mkEvent(session.EventSessionStarted, session.SessionStartedPayload{Model: "claude-opus-4-8"}))
	ApplyRunnerEvent(&live, mkEvent(session.EventTurnStarted, nil))
	ApplyRunnerEvent(&live, mkEvent(session.EventUsageUpdated, session.UsagePayload{InputTokens: 10, CacheReadTokens: 30000, CacheWriteTokens: 10000, OutputTokens: 41, TotalCostUSD: 0.5}))

	pane := NewExternalPaneTransport(Session{Title: "stale"}, "claude", nil, func() Session { return live })
	pane.w, pane.h = 120, 30
	row := pane.statusRow()
	for _, want := range []string{"Fix the build", "claude", "claude-opus-4-8", "busy", "$0.5000"} {
		if !strings.Contains(row, want) {
			t.Errorf("claude pane status row missing %q:\n%s", want, row)
		}
	}
	// ctx% must be computed (CtxLimit resolved from the session.started model);
	// asserting the presence of a nonzero ctx segment rather than an exact
	// percentage keeps the test stable across model-limit table changes.
	if !strings.Contains(row, "ctx ") || strings.Contains(row, "ctx 0%") {
		t.Errorf("claude pane status row missing a live ctx%% segment:\n%s", row)
	}
	if live.CtxLimit == 0 {
		t.Error("session.started model did not resolve a context-window limit (models.Limit)")
	}
}

// Task 4.5 (claude-pane attention): the observer's permission lifecycle drives
// attention routing end-to-end. The claude-pane observer emits
// permission.requested (PermissionRequest hook) and clears it via
// permission.resolved on the next observed activity (PreToolUse/PostToolUse/
// Stop — the answer happened in-pane); the shared reducer must float the
// session (waiting → needsAttention) and clear it back to busy, with no
// claude-pane-specific code in the routing path.
func TestClaudePaneObserverAttentionLifecycle(t *testing.T) {
	sess := Session{State: session.State{ID: "claude-pane-att", Backend: session.BackendClaudePane}}

	// UserPromptSubmit → turn.started: busy, no attention.
	ApplyRunnerEvent(&sess, mkEvent(session.EventTurnStarted, nil))
	if needsAttention(sess) {
		t.Fatalf("busy session must not need attention (status %v)", sess.DashStatus)
	}

	// PermissionRequest hook → permission.requested (observer shape: pane-perm-N
	// id, tool + raw input): waiting + attention float + pending descriptor.
	ApplyRunnerEvent(&sess, mkEvent(session.EventPermissionRequested, session.PermissionPayload{
		PermissionID: "pane-perm-1", Tool: "Bash", Input: json.RawMessage(`{"command":"rm -rf build"}`),
	}))
	if sess.DashStatus != StatusWaiting || !needsAttention(sess) {
		t.Fatalf("permission.requested must float attention (status %v)", sess.DashStatus)
	}
	if sess.PendingPermissionTool != "Bash" || sess.PendingPermissionID != "pane-perm-1" {
		t.Fatalf("pending permission descriptor not captured: %q/%q", sess.PendingPermissionID, sess.PendingPermissionTool)
	}
	if dot := attentionDot(sess); dot == "" {
		t.Error("waiting session must render an attention dot")
	}

	// The user answers IN-PANE; the observer proves it via the next activity and
	// emits permission.resolved {decision: allow-once} → back to busy, cleared.
	ApplyRunnerEvent(&sess, mkEvent(session.EventPermissionResolved, session.PermissionPayload{
		PermissionID: "pane-perm-1", Tool: "Bash", Decision: "allow-once",
	}))
	if sess.DashStatus != StatusBusy || needsAttention(sess) {
		t.Fatalf("permission.resolved must clear attention back to busy (status %v)", sess.DashStatus)
	}
	if sess.PendingPermissionID != "" {
		t.Error("pending permission descriptor not cleared on resolution")
	}

	// Stop → turn.completed: needs-input (attention again, by design — the
	// reply is ready for the user), pending stays clear.
	ApplyRunnerEvent(&sess, mkEvent(session.EventTurnCompleted, nil))
	if sess.DashStatus != StatusNeedsInput || !needsAttention(sess) {
		t.Fatalf("turn.completed should be needs-input attention (status %v)", sess.DashStatus)
	}

	// And the list actually floats it: attention-first ordering puts the
	// waiting/needs-input session above a busy one.
	busy := Session{State: session.State{ID: "other"}, sessionReadModel: sessionReadModel{DashStatus: StatusBusy}}
	order := sortByAttention([]Session{busy, sess}, true)
	if order[0].ID() != sess.ID() {
		t.Errorf("attention-first ordering did not float the attention session: got %v first", order[0].ID())
	}
}

// Task 4.5 (abort path): a synthetic turn-abort (child crash mid-permission —
// the supervisor's turn.interrupted) must also clear a stale waiting state so
// a dead prompt can't hold the session in attention forever.
func TestClaudePaneObserverAttentionClearedByAbort(t *testing.T) {
	sess := Session{State: session.State{ID: "claude-pane-abort", Backend: session.BackendClaudePane}}
	ApplyRunnerEvent(&sess, mkEvent(session.EventTurnStarted, nil))
	ApplyRunnerEvent(&sess, mkEvent(session.EventPermissionRequested, session.PermissionPayload{
		PermissionID: "pane-perm-1", Tool: "Write",
	}))
	ApplyRunnerEvent(&sess, mkEvent(session.EventTurnInterrupted, nil))
	if sess.DashStatus == StatusWaiting {
		t.Fatal("turn.interrupted must clear the waiting state")
	}
	if sess.PendingPermissionID != "" {
		t.Error("pending permission descriptor survived the turn abort")
	}
}

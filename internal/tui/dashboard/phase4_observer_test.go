package dashboard

import (
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

// Phase 4 (in-pane statusline): the external opencode pane's status row reads the
// LIVE dashboard read-model (fed by the passive observer stream), so it surfaces
// DisplayTitle + status + ctx% + cost — at parity with the claude statusline —
// and strips the "provider/" prefix from the model id.
func TestPhase4_ExternalPaneLiveStatusRow(t *testing.T) {
	live := Session{
		State:        session.State{ID: "opencode-xyz", Backend: session.BackendOpenCode},
		DashStatus:   StatusBusy,
		AutoTitle:    "Refactor the lexer",
		Model:        "opencode/big-pickle",
		CtxLimit:     200000,
		InputTokens:  40000,
		TotalCostUSD: 0.0123,
	}
	// Static snapshot is stale on purpose; the live accessor must win.
	pane := NewExternalPane(Session{Title: "proj", Model: "stale"}, OpencodeCreds{}, func() Session { return live })
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

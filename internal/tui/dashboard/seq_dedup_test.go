package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1a step 2: the list reducer must dedup by seq like the transcript does — a
// re-fed event at or below the resume cursor (reconnect replay / duplicate
// stream) must not re-drive read-model state.
func TestHandleRunnerEventSeqDedup(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{SessionFromState(session.State{ID: "s1", Status: session.StatusRunning})}
	id := session.ID("s1")

	// Seq=5 turn.started → Busy, lastSeq=5.
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: mkEventSeq(5, session.EventTurnStarted, nil)})
	s := m.sessionByID(id)
	if s.DashStatus != StatusBusy || s.lastSeq != 5 {
		t.Fatalf("after seq5 turn.started: status=%v lastSeq=%d, want Busy/5", s.DashStatus, s.lastSeq)
	}
	flashAt := s.statusChangedAt

	// Re-feed Seq=3 permission.requested (BELOW the cursor) → must be a no-op:
	// status stays Busy, no pending permission, cursor unmoved, no flash restamp.
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: mkEventSeq(3, session.EventPermissionRequested,
		session.PermissionPayload{PermissionID: "p1", Tool: "Bash", Input: json.RawMessage(`{"command":"ls"}`)})})
	s = m.sessionByID(id)
	if s.DashStatus != StatusBusy {
		t.Fatalf("replayed seq3 changed status to %v (want still Busy)", s.DashStatus)
	}
	if s.PendingPermissionID != "" {
		t.Fatalf("replayed seq3 set PendingPermissionID=%q (want empty)", s.PendingPermissionID)
	}
	if s.lastSeq != 5 {
		t.Fatalf("replayed seq3 advanced lastSeq to %d (want 5)", s.lastSeq)
	}
	if !s.statusChangedAt.Equal(flashAt) {
		t.Fatal("replayed seq3 restamped statusChangedAt (would re-flash the row)")
	}

	// A genuinely-new Seq=6 turn.completed applies normally.
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: mkEventSeq(6, session.EventTurnCompleted, nil)})
	s = m.sessionByID(id)
	if s.lastSeq != 6 {
		t.Fatalf("seq6 turn.completed did not advance lastSeq: %d", s.lastSeq)
	}
	if s.DashStatus == StatusBusy {
		t.Fatalf("seq6 turn.completed did not apply (status still Busy)")
	}
}

// The duplicate-stream double-apply case: a tool.started re-delivered at the
// SAME seq (two background connects racing) must not append RecentTools twice.
func TestHandleRunnerEventDedupsSameSeqToolAppend(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{SessionFromState(session.State{ID: "s1", Status: session.StatusRunning})}
	id := session.ID("s1")

	tool := mkEventSeq(9, session.EventToolStarted, session.ToolPayload{Tool: "Bash", ToolUseID: "tu1", Input: json.RawMessage(`{"command":"ls"}`)})
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: tool})
	if n := len(m.sessionByID(id).RecentTools); n != 1 {
		t.Fatalf("after first tool.started, RecentTools len=%d, want 1", n)
	}
	// Re-deliver the exact same seq → deduped, no second append.
	m.handleRunnerEvent(RunnerEventMsg{ID: id, Event: tool})
	if n := len(m.sessionByID(id).RecentTools); n != 1 {
		t.Fatalf("duplicate same-seq tool.started double-appended RecentTools: len=%d, want 1", n)
	}
}

package dashboard

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/theme"
)

// --------------------------------------------------------------------------
// Status mapping tests
// --------------------------------------------------------------------------

func TestDeriveStatus(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		in   session.State
		want SessionStatus
	}{
		{
			name: "suspended maps to StatusSuspended",
			in:   session.State{Status: session.StatusSuspended},
			want: StatusSuspended,
		},
		{
			name: "failed maps to StatusFailed",
			in:   session.State{Status: session.StatusFailed},
			want: StatusFailed,
		},
		{
			name: "running+ready maps to StatusIdle",
			in:   session.State{Status: session.StatusRunning, PodReady: true, LastActivity: now},
			want: StatusIdle,
		},
		{
			name: "running+not-ready maps to StatusIdle (starting)",
			in:   session.State{Status: session.StatusRunning, PodReady: false},
			want: StatusIdle,
		},
		{
			name: "creating maps to StatusIdle",
			in:   session.State{Status: session.StatusCreating},
			want: StatusIdle,
		},
		{
			name: "unknown maps to StatusIdle",
			in:   session.State{Status: session.StatusUnknown},
			want: StatusIdle,
		},
		{
			name: "gone maps to StatusIdle (caller should drop gone sessions)",
			in:   session.State{Status: session.StatusGone},
			want: StatusIdle,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveStatus(tc.in)
			if got != tc.want {
				t.Errorf("DeriveStatus(%v) = %v, want %v", tc.in.Status, got, tc.want)
			}
		})
	}
}

func TestSessionStatusGlyphAndString(t *testing.T) {
	tests := []struct {
		status     SessionStatus
		wantGlyph  string
		wantString string
	}{
		{StatusBusy, "◐", "busy"},
		{StatusWaiting, "◆", "waiting"},
		{StatusNeedsInput, "❯", "needs-input"},
		{StatusIdle, "○", "idle"},
		{StatusSuspended, "◌", "suspended"},
		{StatusFailed, "✕", "failed"},
	}
	for _, tc := range tests {
		t.Run(tc.wantString, func(t *testing.T) {
			if got := tc.status.Glyph(); got != tc.wantGlyph {
				t.Errorf("Glyph() = %q, want %q", got, tc.wantGlyph)
			}
			if got := tc.status.String(); got != tc.wantString {
				t.Errorf("String() = %q, want %q", got, tc.wantString)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Sort ordering tests
// --------------------------------------------------------------------------

func TestSortByLastActive(t *testing.T) {
	now := time.Now()
	sessions := []Session{
		{State: session.State{ID: "b", LastActivity: now.Add(-10 * time.Minute)}, Title: "b"},
		{State: session.State{ID: "a", LastActivity: now}, Title: "a"},
		{State: session.State{ID: "c", LastActivity: now.Add(-5 * time.Minute)}, Title: "c"},
	}

	// Default: SortDesc puts newest first.
	SortSessions(sessions, SortByLastActive, SortDesc)
	ids := extractIDs(sessions)
	if ids[0] != "a" || ids[1] != "c" || ids[2] != "b" {
		t.Errorf("SortByLastActive desc: got %v, want [a c b]", ids)
	}

	// SortAsc puts oldest first.
	SortSessions(sessions, SortByLastActive, SortAsc)
	ids = extractIDs(sessions)
	if ids[0] != "b" || ids[1] != "c" || ids[2] != "a" {
		t.Errorf("SortByLastActive asc: got %v, want [b c a]", ids)
	}
}

func TestSortByTitle(t *testing.T) {
	sessions := []Session{
		{State: session.State{ID: "1"}, Title: "Zulu"},
		{State: session.State{ID: "2"}, Title: "alpha"},
		{State: session.State{ID: "3"}, Title: "Bravo"},
	}

	// Asc: A→Z (case-insensitive)
	SortSessions(sessions, SortByTitle, SortAsc)
	titles := extractTitles(sessions)
	if titles[0] != "alpha" || titles[1] != "Bravo" || titles[2] != "Zulu" {
		t.Errorf("SortByTitle asc: got %v, want [alpha Bravo Zulu]", titles)
	}

	// Desc: Z→A
	SortSessions(sessions, SortByTitle, SortDesc)
	titles = extractTitles(sessions)
	if titles[0] != "Zulu" || titles[1] != "Bravo" || titles[2] != "alpha" {
		t.Errorf("SortByTitle desc: got %v, want [Zulu Bravo alpha]", titles)
	}
}

func TestSortByStatus(t *testing.T) {
	sessions := []Session{
		{State: session.State{ID: "1"}, DashStatus: StatusSuspended},
		{State: session.State{ID: "2"}, DashStatus: StatusWaiting},
		{State: session.State{ID: "3"}, DashStatus: StatusBusy},
		{State: session.State{ID: "4"}, DashStatus: StatusFailed},
		{State: session.State{ID: "5"}, DashStatus: StatusIdle},
		{State: session.State{ID: "6"}, DashStatus: StatusNeedsInput},
	}

	// Asc: most urgent first.
	SortSessions(sessions, SortByStatus, SortAsc)
	// Expected order: waiting(0) busy(1) needs-input(2) failed(3) idle(4) suspended(5)
	wantStatuses := []SessionStatus{
		StatusWaiting, StatusBusy, StatusNeedsInput,
		StatusFailed, StatusIdle, StatusSuspended,
	}
	for i, want := range wantStatuses {
		if sessions[i].DashStatus != want {
			t.Errorf("SortByStatus asc[%d]: got %v, want %v", i, sessions[i].DashStatus, want)
		}
	}
}

// --------------------------------------------------------------------------
// Watch-patches-one-session reducer tests
// --------------------------------------------------------------------------

func TestApplyPodEventPatch(t *testing.T) {
	m := New(nil) // nil backend — driven manually
	m.sessions = []Session{
		{State: session.State{ID: "sess-a", Status: session.StatusRunning}, DashStatus: StatusIdle},
		{State: session.State{ID: "sess-b", Status: session.StatusRunning}, DashStatus: StatusIdle},
	}

	// Update sess-a to suspended.
	m.applyPodEvent(k8s.StateEvent{
		State: session.State{ID: "sess-a", Status: session.StatusSuspended},
	})

	var found bool
	for _, s := range m.sessions {
		if s.ID() == "sess-a" {
			found = true
			if s.DashStatus != StatusSuspended {
				t.Errorf("sess-a: got %v, want StatusSuspended", s.DashStatus)
			}
		}
	}
	if !found {
		t.Error("sess-a disappeared from sessions list after patch")
	}
	if len(m.sessions) != 2 {
		t.Errorf("session count: got %d, want 2", len(m.sessions))
	}
}

// TestToastTickLoopGuarded is the B11 regression: a second toast arriving while
// a tick loop is already running must not spawn a second concurrent loop.
func TestToastTickLoopGuarded(t *testing.T) {
	m := New(nil)
	_, cmd1 := m.Update(toastMsg{id: "s1", title: "a"})
	if !m.toastTickActive {
		t.Fatal("first toast should mark the tick loop active")
	}
	if cmd1 == nil {
		t.Fatal("first toast should start the tick loop")
	}
	_, cmd2 := m.Update(toastMsg{id: "s2", title: "b"})
	if cmd2 != nil {
		t.Error("second toast spawned a second tick loop (cmd should be nil while active)")
	}
}

// TestNotifyExcludesAttachedSession is the B12 regression: the attached session
// must never toast itself.
func TestNotifyExcludesAttachedSession(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{State: session.State{ID: "s1"}, DashStatus: StatusWaiting, Title: "s1"},
		{State: session.State{ID: "s2"}, DashStatus: StatusWaiting, Title: "s2"},
	}
	// Attached to s1 → the toast must be for s2, not s1.
	cmd := m.notifyIfBackgroundAttention("s1")
	if cmd == nil {
		t.Fatal("expected a toast for the non-attached waiting session")
	}
	tm, ok := cmd().(toastMsg)
	if !ok {
		t.Fatalf("expected toastMsg, got different message")
	}
	if tm.id != "s2" {
		t.Errorf("toasted the attached/wrong session: got %s want s2", tm.id)
	}
}

// TestAppAttachedSessionID is the B12 plumbing: the App reports the attached
// session id (the exclusion key) per screen.
func TestAppAttachedSessionID(t *testing.T) {
	a := NewApp(nil, nil, nil)
	if got := a.attachedSessionID(); got != "" {
		t.Errorf("dashboard screen should have no attached id, got %q", got)
	}
	a.transcript = NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	a.screen = ScreenTranscript
	if got := a.attachedSessionID(); got != a.transcript.ref.ID {
		t.Errorf("attached id: got %q want %q", got, a.transcript.ref.ID)
	}
}

// TestApplyPodEventPreservesRichFields is the B1 regression: a cluster-watch
// Modified event carries only lifecycle Status + identity (sandboxToState leaves
// project/model/backend/pod/last-active zero). Patching must merge that status
// onto the existing entry, not replace the whole State and blank the rich fields
// the seed List populated.
func TestApplyPodEventPreservesRichFields(t *testing.T) {
	m := New(nil)
	active := time.Now().Add(-5 * time.Minute)
	m.sessions = []Session{
		SessionFromState(session.State{
			ID:           "sess-a",
			Status:       session.StatusRunning,
			ProjectPath:  "/home/dev/my-project",
			Model:        "claude-opus-4-8",
			Backend:      "claude-sdk",
			PodName:      "sess-a-pod",
			LastActivity: active,
		}),
	}

	// A sparse watch delta (only ID + Status, as sandboxToState produces).
	m.applyPodEvent(k8s.StateEvent{
		State: session.State{ID: "sess-a", Status: session.StatusRunning},
	})

	s := m.sessions[0]
	if s.State.ProjectPath != "/home/dev/my-project" {
		t.Errorf("ProjectPath wiped: got %q", s.State.ProjectPath)
	}
	if s.State.Model != "claude-opus-4-8" {
		t.Errorf("Model wiped: got %q", s.State.Model)
	}
	if s.State.Backend != "claude-sdk" {
		t.Errorf("Backend wiped: got %q", s.State.Backend)
	}
	if s.State.PodName != "sess-a-pod" {
		t.Errorf("PodName wiped: got %q", s.State.PodName)
	}
	if !s.State.LastActivity.Equal(active) {
		t.Errorf("LastActivity wiped: got %v want %v", s.State.LastActivity, active)
	}
	if s.Title != "my-project" {
		t.Errorf("Title reverted to ID instead of project base: got %q", s.Title)
	}
}

// TestGroupToggleAndGGChord is the B5 regression: previously GroupToggle (bound
// to "g") was matched before the gg-chord logic, so ggPending never armed and
// "gg → top" was unreachable. A lone `g` must toggle group view; a quick `gg`
// must jump to the top while leaving the group-view state as it was.
func TestGroupToggleAndGGChord(t *testing.T) {
	newM := func() *Model {
		m := New(nil)
		m.sessions = []Session{
			SessionFromState(session.State{ID: "s1", Status: session.StatusRunning, ProjectPath: "/a"}),
			SessionFromState(session.State{ID: "s2", Status: session.StatusRunning, ProjectPath: "/b"}),
			SessionFromState(session.State{ID: "s3", Status: session.StatusRunning, ProjectPath: "/c"}),
		}
		return m
	}

	// Lone `g` toggles group view (and arms the chord).
	m := newM()
	if m.groupView.open {
		t.Fatal("group view should start closed")
	}
	m.handleKey(keyMsg("g"))
	if !m.groupView.open {
		t.Error("lone g did not open group view")
	}
	if !m.ggPending {
		t.Error("first g should arm the gg chord")
	}

	// `gg` jumps to top and reverts the transient toggle (group stays closed).
	m2 := newM()
	m2.cursor = 2
	m2.handleKey(keyMsg("g"))
	m2.handleKey(keyMsg("g"))
	if m2.cursor != 0 {
		t.Errorf("gg did not jump to top: cursor=%d want 0", m2.cursor)
	}
	if m2.groupView.open {
		t.Error("gg left group view toggled on; expected it reverted to closed")
	}
	if m2.ggPending {
		t.Error("ggPending should be cleared after gg")
	}
}

func TestApplyPodEventInsert(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{State: session.State{ID: "sess-a"}, DashStatus: StatusIdle},
	}

	// A new session appears.
	m.applyPodEvent(k8s.StateEvent{
		State: session.State{ID: "sess-new", Status: session.StatusRunning},
	})

	if len(m.sessions) != 2 {
		t.Errorf("session count after insert: got %d, want 2", len(m.sessions))
	}
	var found bool
	for _, s := range m.sessions {
		if s.ID() == "sess-new" {
			found = true
		}
	}
	if !found {
		t.Error("sess-new not found after insert")
	}
}

func TestApplyPodEventDelete(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{State: session.State{ID: "sess-a"}, DashStatus: StatusIdle},
		{State: session.State{ID: "sess-b"}, DashStatus: StatusIdle},
	}

	// sess-a is deleted.
	m.applyPodEvent(k8s.StateEvent{
		State:   session.State{ID: "sess-a", Status: session.StatusGone},
		Deleted: true,
	})

	if len(m.sessions) != 1 {
		t.Errorf("session count after delete: got %d, want 1", len(m.sessions))
	}
	if m.sessions[0].ID() != "sess-b" {
		t.Errorf("remaining session: got %v, want sess-b", m.sessions[0].ID())
	}
}

func TestApplyPodEventGoneStatus(t *testing.T) {
	// A StateEvent with Status=GONE (not Deleted flag) should also remove.
	m := New(nil)
	m.sessions = []Session{
		{State: session.State{ID: "sess-x"}, DashStatus: StatusIdle},
	}
	m.applyPodEvent(k8s.StateEvent{
		State:   session.State{ID: "sess-x", Status: session.StatusGone},
		Deleted: false,
	})
	// StatusGone should trigger the deletion path.
	if len(m.sessions) != 0 {
		t.Errorf("session count after GONE event: got %d, want 0", len(m.sessions))
	}
}

// --------------------------------------------------------------------------
// B10: Seed/watch race — late seedMsg must not clobber runner-derived status
// --------------------------------------------------------------------------

// ORACLE: If PodEventMsgs arrive before seedMsg (watch wins the race),
// applySeed must preserve the runner-derived DashStatus and SSE streams
// already established — not downgrade them to cluster-derived status.
func TestApplySeedPreservesRunnerDerivedStatus(t *testing.T) {
	m := New(nil)

	// Simulate watch arriving first: session sess-a is Running and the
	// dashboard has already set a runner-derived DashStatus of StatusBusy.
	m.sessions = []Session{
		{
			State:      session.State{ID: "sess-a", Status: session.StatusRunning},
			DashStatus: StatusBusy,
		},
	}

	// Now seedMsg arrives late (concurrent with watch).
	// The seed carries only the cluster-derived view (idle-ish).
	seed := []session.State{
		{
			ID:          "sess-a",
			Status:      session.StatusRunning,
			ProjectPath: "/home/user/proj",
		},
	}
	next, _ := m.applySeed(seed)

	if len(next.sessions) != 1 {
		t.Fatalf("session count after applySeed: got %d, want 1", len(next.sessions))
	}
	got := next.sessions[0].DashStatus
	if got != StatusBusy {
		t.Errorf("DashStatus after late seed: got %v, want StatusBusy (runner-derived must survive)", got)
	}
	// The seed should have filled in the rich descriptive fields.
	if next.sessions[0].State.ProjectPath != "/home/user/proj" {
		t.Errorf("ProjectPath not propagated by seed: got %q", next.sessions[0].State.ProjectPath)
	}
}

// ORACLE: applySeed does NOT open a new SSE stream when one is already live (B10).
func TestApplySeedSkipsSSEWhenAlreadyRunning(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{State: session.State{ID: "sess-b", Status: session.StatusRunning}, DashStatus: StatusBusy},
	}
	// Fake a live SSE cancel to mark the session as already streaming.
	started := false
	m.liveSSECancels["sess-b"] = func() {}
	// Inject a connector that records calls.
	m.connector = func(ctx context.Context, ref session.Ref, _ string, _ func(ConnectStage)) (ConnectResult, error) {
		started = true
		return ConnectResult{}, fmt.Errorf("should not be called")
	}

	seed := []session.State{
		{ID: "sess-b", Status: session.StatusRunning},
	}
	_, cmds := m.applySeed(seed)

	// No new SSE Cmd should have been produced.
	if len(cmds) != 0 {
		t.Errorf("applySeed produced %d SSE cmds when one was already running, want 0", len(cmds))
	}
	_ = started // connector must never be called
}

// --------------------------------------------------------------------------
// B13/RV1: Runner-unreachable mid-turn degrades to StatusFailed — but only after
// a bounded reconnect, so a transient port-forward blip self-heals.
// --------------------------------------------------------------------------

// ORACLE: When the background SSE stream ends while the session is StatusBusy or
// StatusWaiting on a still-Running pod, the session must NOT immediately show
// 'failed' (that would stick a false glyph on a healthy session after a transient
// forward blip). It preserves its status and schedules a reconnect; only once the
// reconnect budget is exhausted does it degrade to StatusFailed (B13: "runner
// unreachable = failed"). (RV1)
func TestRunnerUnreachableMidTurnRetriesThenFails(t *testing.T) {
	for _, startStatus := range []SessionStatus{StatusBusy, StatusWaiting} {
		m := New(nil)
		m.sessions = []Session{
			{
				State:      session.State{ID: "sess-c", Status: session.StatusRunning},
				DashStatus: startStatus,
			},
		}
		// Fake a live SSE cancel so the stream-ended logic doesn't no-op.
		m.liveSSECancels["sess-c"] = func() {}
		m.liveSSEChannels["sess-c"] = make(chan session.Event)

		// First drop: status preserved, a reconnect is scheduled (no false fail).
		next, cmd := m.handleRunnerEvent(RunnerEventMsg{ID: "sess-c", StreamEnded: true})
		dm := next.(*Model)
		if cmd == nil {
			t.Errorf("startStatus=%v: expected a reconnect cmd on first drop, got nil", startStatus)
		}
		if dm.sessions[0].DashStatus != startStatus {
			t.Errorf("startStatus=%v: status after first drop = %v, want preserved %v (not failed yet)",
				startStatus, dm.sessions[0].DashStatus, startStatus)
		}

		// Exhaust the reconnect budget (each attempt reports failure) → Failed.
		cur := dm
		for a := 0; a < liveSSEMaxRetries; a++ {
			nx, _ := cur.Update(liveSSEReconnectFailedMsg{id: "sess-c", attempt: a})
			cur = nx.(*Model)
		}
		if got := cur.sessions[0].DashStatus; got != StatusFailed {
			t.Errorf("startStatus=%v: after exhausting retries DashStatus=%v, want StatusFailed",
				startStatus, got)
		}
	}
}

// ORACLE: When the stream ends while the session is already idle/needs-input,
// the status degrades to cluster-derived baseline (not failed). [B13]
func TestRunnerStreamEndedWhenAtRestDegradesToIdle(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{
			State:      session.State{ID: "sess-d", Status: session.StatusRunning},
			DashStatus: StatusNeedsInput,
		},
	}
	m.liveSSECancels["sess-d"] = func() {}
	m.liveSSEChannels["sess-d"] = make(chan session.Event)

	next, _ := m.handleRunnerEvent(RunnerEventMsg{ID: "sess-d", StreamEnded: true})
	dm := next.(*Model)
	// For a Running pod at rest, cluster-derived = idle.
	if dm.sessions[0].DashStatus != StatusIdle {
		t.Errorf("DashStatus after stream-ended at rest = %v, want StatusIdle", dm.sessions[0].DashStatus)
	}
}

// --------------------------------------------------------------------------
// B14: Orphaned SSE cancelled when session deleted/suspended mid-connect
// --------------------------------------------------------------------------

// ORACLE: liveSSEReadyMsg must call msg.cancel() and drop the stream if the
// session was deleted (not in m.sessions) while the connector was in flight. [B14]
func TestLiveSSEReadyDroppedForDeletedSession(t *testing.T) {
	m := New(nil)
	// Session "gone-id" is NOT in m.sessions (deleted while connecting).

	cancelled := false
	ch := make(chan session.Event)
	msg := liveSSEReadyMsg{
		id:     "gone-id",
		ch:     ch,
		cancel: func() { cancelled = true },
	}

	next, cmd := m.Update(msg)
	dm := next.(*Model)

	if !cancelled {
		t.Error("liveSSEReadyMsg for deleted session must call cancel()")
	}
	if _, exists := dm.liveSSECancels["gone-id"]; exists {
		t.Error("liveSSECancels must not store entry for deleted session")
	}
	if cmd != nil {
		t.Error("liveSSEReadyMsg for deleted session must return nil cmd")
	}
}

// ORACLE: liveSSEReadyMsg must call msg.cancel() if session is suspended mid-connect. [B14]
func TestLiveSSEReadyDroppedForSuspendedSession(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{
			State:      session.State{ID: "susp-id", Status: session.StatusSuspended},
			DashStatus: StatusSuspended,
		},
	}

	cancelled := false
	msg := liveSSEReadyMsg{
		id:     "susp-id",
		ch:     make(chan session.Event),
		cancel: func() { cancelled = true },
	}

	m.Update(msg)
	if !cancelled {
		t.Error("liveSSEReadyMsg for suspended session must call cancel()")
	}
}

// ORACLE: liveSSEReadyMsg is accepted when session is still Running. [B14]
func TestLiveSSEReadyAcceptedForRunningSession(t *testing.T) {
	m := New(nil)
	m.sessions = []Session{
		{
			State:      session.State{ID: "run-id", Status: session.StatusRunning},
			DashStatus: StatusIdle,
		},
	}

	cancelled := false
	ch := make(chan session.Event)
	msg := liveSSEReadyMsg{
		id:     "run-id",
		ch:     ch,
		cancel: func() { cancelled = true },
	}

	next, cmd := m.Update(msg)
	dm := next.(*Model)

	if cancelled {
		t.Error("liveSSEReadyMsg for running session must NOT cancel")
	}
	if _, exists := dm.liveSSECancels["run-id"]; !exists {
		t.Error("liveSSECancels must have entry for running session")
	}
	if cmd == nil {
		t.Error("liveSSEReadyMsg for running session must return a cmd")
	}
}

// --------------------------------------------------------------------------
// Fuzzy filter tests
// --------------------------------------------------------------------------

func TestFilterSessions(t *testing.T) {
	sessions := []Session{
		{State: session.State{ID: "1", ProjectPath: "/Users/cullen/git/homelab", Backend: "claude-sdk"}, Title: "homelab"},
		{State: session.State{ID: "2", ProjectPath: "/Users/cullen/git/sandbox", Backend: "claude-sdk"}, Title: "sandbox"},
		{State: session.State{ID: "3", ProjectPath: "/Users/cullen/git/other", Backend: "opencode"}, Title: "other"},
	}

	// Empty query returns all.
	got := FilterSessions(sessions, "")
	if len(got) != 3 {
		t.Errorf("empty query: got %d sessions, want 3", len(got))
	}

	// Exact title match.
	got = FilterSessions(sessions, "sandbox")
	if len(got) != 1 || got[0].Title != "sandbox" {
		t.Errorf("title match 'sandbox': got %v", extractTitles(got))
	}

	// Backend match.
	got = FilterSessions(sessions, "opencode")
	if len(got) != 1 || got[0].State.Backend != "opencode" {
		t.Errorf("backend match 'opencode': got %v", got)
	}

	// No match.
	got = FilterSessions(sessions, "zzznomatch")
	if len(got) != 0 {
		t.Errorf("no match: got %d sessions, want 0", len(got))
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func extractIDs(sessions []Session) []string {
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = string(s.ID())
	}
	return ids
}

func extractTitles(sessions []Session) []string {
	titles := make([]string, len(sessions))
	for i, s := range sessions {
		titles[i] = s.Title
	}
	return titles
}

// TestPendingActionRendersSpinner verifies U3: when PendingAction is set, the
// row glyph is the animated spinner (same as StatusBusy) rather than a static
// status glyph.
func TestPendingActionRendersSpinner(t *testing.T) {
	m := New(&fakeBackend{})
	m.spinnerFrame = 3

	s := Session{State: session.State{ID: "s1"}, DashStatus: StatusIdle, PendingAction: "suspend"}
	row := m.renderSessionRow(s, false, 80)

	// The spinner frame at index 3 should appear in the row.
	want := theme.SpinnerFrame(3)
	if !strings.Contains(row, want) {
		t.Errorf("row should contain spinner frame %q, got:\n%s", want, row)
	}

	// Without PendingAction the same session shows the idle glyph (●).
	s.PendingAction = ""
	row2 := m.renderSessionRow(s, false, 80)
	if strings.Contains(row2, want) {
		t.Errorf("row without PendingAction should NOT contain spinner frame %q", want)
	}
}

package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// subline_test.go — Layout A sub-line: the second physical row line shows the
// colored status word followed by a dim "what/where/lifecycle" tail. These
// oracles pin the field set and the ShortID disambiguator fix (the trailing
// random suffix, not the backend-prefix head).

// ShortID must use the trailing random suffix of "<backend>-<hash>-<rnd>" ids,
// not the head — otherwise every claude session collapses to the useless "clau".
func TestShortIDUsesTrailingSuffix(t *testing.T) {
	cases := map[string]string{
		"claude-sdk-df80e6-75908cbf":      "7590", // real format → suffix, not "clau"
		"opencode-server-aa11bb-cc22dd33": "cc22",
		"a3f1c9de":                        "a3f1", // no hyphens → head
	}
	for id, want := range cases {
		got := Session{State: session.State{ID: session.ID(id)}}.ShortID()
		if got != want {
			t.Errorf("ShortID(%q) = %q, want %q", id, got, want)
		}
	}
}

// The sub-line must never lead with the backend-prefix head "clau".
func TestSublineNotConfusingClau(t *testing.T) {
	m := triageModel(t)
	s := Session{
		State:      session.State{ID: "claude-sdk-df80e6-75908cbf", Backend: session.BackendClaudeSDK},
		DashStatus: StatusIdle,
	}
	row := stripANSI(m.renderSessionRow(s, false, 80))
	if strings.Contains(row, "clau ") || strings.Contains(row, " clau") {
		t.Errorf("row sub-line must not show the confusing backend-prefix 'clau'; got:\n%s", row)
	}
	if !strings.Contains(row, "7590") {
		t.Errorf("row sub-line should show the trailing-suffix disambiguator '7590'; got:\n%s", row)
	}
}

// A workspace.status event populates the session's branch, and the branch (with a
// dirty marker) then surfaces on the row sub-line.
func TestWorkspaceStatusFeedsBranchOntoRow(t *testing.T) {
	m := triageModel(t)
	s := Session{
		State:      session.State{ID: "s1", Backend: session.BackendClaudeSDK, ProjectPath: "/work/proj"},
		DashStatus: StatusIdle,
	}
	ApplyRunnerEvent(&s, mkEvent(session.EventWorkspaceStatus,
		session.WorkspaceStatusPayload{Branch: "feat/x", Dirty: true}))
	if s.Branch != "feat/x" || !s.Dirty {
		t.Fatalf("WorkspaceStatus should set Branch/Dirty; got Branch=%q Dirty=%v", s.Branch, s.Dirty)
	}
	row := stripANSI(m.renderSessionRow(s, false, 80))
	if !strings.Contains(row, "feat/x*") {
		t.Errorf("row sub-line should show the dirty branch 'feat/x*'; got:\n%s", row)
	}
	if !strings.Contains(row, "/work/proj") {
		t.Errorf("row sub-line should show the project dir; got:\n%s", row)
	}
}

// The status word reflects the live six-state status (working/waiting/idle), and
// the waiting sub-state names the pending permission tool.
func TestSublineStatusWordAndPermSubstate(t *testing.T) {
	m := triageModel(t)
	waiting := Session{
		State:                 session.State{ID: "s1", Backend: session.BackendClaudeSDK},
		DashStatus:            StatusWaiting,
		PendingPermissionTool: "Bash",
	}
	row := stripANSI(m.renderSessionRow(waiting, false, 80))
	if !strings.Contains(row, "waiting") {
		t.Errorf("row should show status word 'waiting'; got:\n%s", row)
	}
	if !strings.Contains(row, "perm Bash") {
		t.Errorf("row should name the pending permission tool 'perm Bash'; got:\n%s", row)
	}

	working := Session{
		State:       session.State{ID: "s2", Backend: session.BackendClaudeSDK},
		DashStatus:  StatusBusy,
		RecentTools: []ToolRef{{Tool: "Edit", Arg: "main.go"}},
	}
	wrow := stripANSI(m.renderSessionRow(working, false, 80))
	if !strings.Contains(wrow, "working") {
		t.Errorf("busy row should show status word 'working'; got:\n%s", wrow)
	}
	if !strings.Contains(wrow, "edit main.go") {
		t.Errorf("busy row should show the live tool 'edit main.go'; got:\n%s", wrow)
	}
}

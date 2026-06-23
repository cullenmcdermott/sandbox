package session

import (
	"encoding/json"
	"testing"
)

func TestEventJSONRoundTrip(t *testing.T) {
	ev := Event{
		Seq:       1842,
		Time:      "2026-06-18T22:30:00Z",
		SessionID: "claude-sdk-7f3a",
		TurnID:    "turn-12",
		Type:      EventToolCompleted,
		Payload:   json.RawMessage(`{"tool":"Bash","output":"ok"}`),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Seq != ev.Seq {
		t.Errorf("seq: got %d, want %d", decoded.Seq, ev.Seq)
	}
	if decoded.Type != ev.Type {
		t.Errorf("type: got %s, want %s", decoded.Type, ev.Type)
	}
	if decoded.SessionID != ev.SessionID {
		t.Errorf("session_id: got %s, want %s", decoded.SessionID, ev.SessionID)
	}
}

func TestEventTypeStrings(t *testing.T) {
	cases := []struct {
		typ  EventType
		want string
	}{
		{EventSessionStarted, "session.started"},
		{EventTurnCompleted, "turn.completed"},
		{EventToolFailed, "tool.failed"},
		{EventPermissionRequested, "permission.requested"},
		{EventError, "error"},
	}
	for _, c := range cases {
		if string(c.typ) != c.want {
			t.Errorf("got %q, want %q", c.typ, c.want)
		}
	}
}

func TestToolPayloadRoundTrip(t *testing.T) {
	exitCode := 0
	p := ToolPayload{
		Tool:     "Bash",
		Output:   "hello world",
		ExitCode: &exitCode,
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ToolPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Tool != "Bash" {
		t.Errorf("tool: got %q, want Bash", decoded.Tool)
	}
	if decoded.ExitCode == nil || *decoded.ExitCode != 0 {
		t.Errorf("exitCode: got %v, want 0", decoded.ExitCode)
	}
}

func TestPermissionDecisionJSON(t *testing.T) {
	d := PermissionDecision{
		Session:    "claude-sdk-7f3a",
		Permission: "perm-abc",
		Allow:      true,
		Scope:      "once",
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded PermissionDecision
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !decoded.Allow || decoded.Scope != "once" {
		t.Errorf("got allow=%v scope=%q, want true/once", decoded.Allow, decoded.Scope)
	}
}

func TestSpecDefaults(t *testing.T) {
	// Spec doesn't have defaults built in; callers (k8s.Backend) fill them.
	// This test verifies the type is usable with zero values.
	s := Spec{ID: "test", ProjectPath: "/tmp", Backend: "claude-sdk"}
	if s.StorageGiB != 0 {
		t.Errorf("zero-value StorageGiB should be 0, got %d", s.StorageGiB)
	}
}

func TestStatusString(t *testing.T) {
	cases := []struct {
		status Status
		want   string
	}{
		{StatusRunning, "RUNNING"},
		{StatusSuspended, "SUSPENDED"},
		{StatusGone, "GONE"},
	}
	for _, c := range cases {
		if string(c.status) != c.want {
			t.Errorf("got %q, want %q", c.status, c.want)
		}
	}
}

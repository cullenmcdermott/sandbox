package session

import (
	"encoding/json"
	"strings"
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
		t.Errorf("sessionId: got %s, want %s", decoded.SessionID, ev.SessionID)
	}
	if decoded.TurnID != ev.TurnID {
		t.Errorf("turnId: got %s, want %s", decoded.TurnID, ev.TurnID)
	}

	// The wire format must be camelCase (matches the TS runner's output).
	if !strings.Contains(string(data), `"sessionId"`) {
		t.Errorf("marshal: missing camelCase sessionId key in %s", data)
	}
	if !strings.Contains(string(data), `"turnId"`) {
		t.Errorf("marshal: missing camelCase turnId key in %s", data)
	}
}

// REGRESSION (S2): the TS runner emits camelCase keys (sessionId/turnId); Go must
// decode them correctly. Prior tags used snake_case so these fields were always empty.
func TestEventUnmarshalFromRunnerJSON(t *testing.T) {
	// This is exactly the JSON shape the TS runner emits via SSE.
	input := `{"seq":7,"time":"2026-06-22T12:00:00Z","sessionId":"sess-abc","turnId":"turn-xyz","type":"message.delta","payload":{"content":"hi"}}`

	var ev Event
	if err := json.Unmarshal([]byte(input), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.SessionID != "sess-abc" {
		t.Errorf("SessionID: got %q, want sess-abc", ev.SessionID)
	}
	if ev.TurnID != "turn-xyz" {
		t.Errorf("TurnID: got %q, want turn-xyz", ev.TurnID)
	}
	if ev.Seq != 7 {
		t.Errorf("Seq: got %d, want 7", ev.Seq)
	}
}

// turnId is optional on the wire; omitting it must leave TurnID empty.
func TestEventUnmarshalOptionalTurnID(t *testing.T) {
	input := `{"seq":1,"time":"2026-06-22T12:00:00Z","sessionId":"sess-x","type":"session.started","payload":{}}`
	var ev Event
	if err := json.Unmarshal([]byte(input), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.SessionID != "sess-x" {
		t.Errorf("SessionID: got %q, want sess-x", ev.SessionID)
	}
	if ev.TurnID != "" {
		t.Errorf("TurnID: got %q, want empty", ev.TurnID)
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

func TestToolPayloadNestingRoundTrip(t *testing.T) {
	child := ToolPayload{Tool: "Grep", ToolUseID: "tu_2", ParentToolUseID: "tu_1", AgentName: ""}
	data, err := json.Marshal(child)
	if err != nil {
		t.Fatalf("marshal child: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"toolUseId":"tu_2"`) {
		t.Errorf("missing toolUseId in %s", s)
	}
	if !strings.Contains(s, `"parentToolUseId":"tu_1"`) {
		t.Errorf("missing parentToolUseId in %s", s)
	}
	if strings.Contains(s, "agentName") {
		t.Errorf("empty agentName should be omitted, got %s", s)
	}
	var decodedChild ToolPayload
	if err := json.Unmarshal(data, &decodedChild); err != nil {
		t.Fatalf("unmarshal child: %v", err)
	}
	if decodedChild.Tool != child.Tool || decodedChild.ToolUseID != child.ToolUseID ||
		decodedChild.ParentToolUseID != child.ParentToolUseID || decodedChild.AgentName != child.AgentName {
		t.Errorf("child round-trip: got %+v, want %+v", decodedChild, child)
	}

	task := ToolPayload{Tool: "Task", ToolUseID: "tu_1", AgentName: "general-purpose"}
	data, err = json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	s = string(data)
	if !strings.Contains(s, `"agentName":"general-purpose"`) {
		t.Errorf("missing agentName in %s", s)
	}
	if strings.Contains(s, "parentToolUseId") {
		t.Errorf("empty parentToolUseId should be omitted, got %s", s)
	}
	var decodedTask ToolPayload
	if err := json.Unmarshal(data, &decodedTask); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if decodedTask.Tool != task.Tool || decodedTask.ToolUseID != task.ToolUseID ||
		decodedTask.ParentToolUseID != task.ParentToolUseID || decodedTask.AgentName != task.AgentName {
		t.Errorf("task round-trip: got %+v, want %+v", decodedTask, task)
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

func TestTurnInputModeJSON(t *testing.T) {
	// ApprovalPolicy marshals to the wire "mode" key (kept for wire compat) and
	// round-trips.
	in := TurnInput{Prompt: "hi", ApprovalPolicy: ApprovalPlan}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(data); !strings.Contains(got, `"mode":"plan"`) {
		t.Errorf("expected mode key in %s", got)
	}
	var decoded TurnInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ApprovalPolicy != ApprovalPlan {
		t.Errorf("approval policy: got %q, want plan", decoded.ApprovalPolicy)
	}

	// Empty policy is omitted (default path => runner uses bypassPermissions).
	data, err = json.Marshal(TurnInput{Prompt: "hi"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "mode") {
		t.Errorf("empty mode should be omitted, got %s", data)
	}
}

func TestTurnInputEffortJSON(t *testing.T) {
	// Effort marshals to the "effort" key and round-trips. The wire value is the
	// real SDK enum ("max" for the TUI's "ultracode" label).
	in := TurnInput{Prompt: "hi", Effort: "max"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(data); !strings.Contains(got, `"effort":"max"`) {
		t.Errorf("expected effort key in %s", got)
	}
	var decoded TurnInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Effort != "max" {
		t.Errorf("effort: got %q, want max", decoded.Effort)
	}

	// Empty effort is omitted (default path => runner leaves options.effort unset).
	data, err = json.Marshal(TurnInput{Prompt: "hi"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "effort") {
		t.Errorf("empty effort should be omitted, got %s", data)
	}
}

func TestWorkspaceStatusPayloadRoundTrip(t *testing.T) {
	p := WorkspaceStatusPayload{Branch: "main", Dirty: true, Ahead: 2, Behind: 1}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"branch":"main"`, `"dirty":true`, `"ahead":2`, `"behind":1`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %s in %s", want, data)
		}
	}
	var decoded WorkspaceStatusPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded != p {
		t.Errorf("round-trip mismatch: got %+v, want %+v", decoded, p)
	}
}

// TestOpencodeProviderEntryKey pins the wire-vocabulary → auth.json entry-key
// mapping: empty and anthropic both map to "anthropic", openai is identity, and
// Zen maps to opencode's own key "opencode" (NOT the wire value "opencode-zen").
// An unknown provider passes through unchanged.
func TestOpencodeProviderEntryKey(t *testing.T) {
	cases := []struct {
		provider string
		want     string
	}{
		{"", "anthropic"},
		{OpencodeProviderAnthropic, "anthropic"},
		{OpencodeProviderOpenAI, "openai"},
		{OpencodeProviderZen, "opencode"},
		{"some-future-provider", "some-future-provider"},
	}
	for _, tc := range cases {
		if got := OpencodeProviderEntryKey(tc.provider); got != tc.want {
			t.Errorf("OpencodeProviderEntryKey(%q) = %q, want %q", tc.provider, got, tc.want)
		}
	}
}

// fwdTestHandle is a minimal ForwardHandle for exercising Forwards without a
// live port-forward.
type fwdTestHandle struct {
	port int
	done chan struct{}
}

func (h *fwdTestHandle) LocalPort() int        { return h.port }
func (h *fwdTestHandle) Close() error          { return nil }
func (h *fwdTestHandle) Done() <-chan struct{} { return h.done }

// TestForward pins the standard-endpoint spec builder: each name gets its
// standard remote port and an OS-assigned (0) local port, and an unknown name
// gets Remote 0 (which PortForward rejects).
func TestForward(t *testing.T) {
	specs := Forward(PortRunner, PortSSH)
	if len(specs) != 2 {
		t.Fatalf("Forward(runner, ssh) = %d specs, want 2", len(specs))
	}
	if specs[0].Name != PortRunner || specs[0].Remote != RunnerPort || specs[0].Local != 0 {
		t.Errorf("specs[0] = %+v, want {Name:runner Remote:%d Local:0}", specs[0], RunnerPort)
	}
	if specs[1].Name != PortSSH || specs[1].Remote != SSHPort || specs[1].Local != 0 {
		t.Errorf("specs[1] = %+v, want {Name:ssh Remote:%d Local:0}", specs[1], SSHPort)
	}

	unknown := Forward(PortName("bogus"))
	if len(unknown) != 1 || unknown[0].Remote != 0 {
		t.Errorf("Forward(unknown) = %+v, want Remote 0", unknown)
	}
}

// TestForwardsNameRouting pins the whole point of keying by name: a caller
// looks a handle up by PortName regardless of map iteration/insertion order,
// and a nil map (never-connected) is safe to query and Close.
func TestForwardsNameRouting(t *testing.T) {
	runnerH := &fwdTestHandle{port: 1111, done: make(chan struct{})}
	sshH := &fwdTestHandle{port: 2222, done: make(chan struct{})}
	fwds := Forwards{PortRunner: runnerH, PortSSH: sshH}

	if p, ok := fwds.LocalPort(PortRunner); !ok || p != 1111 {
		t.Errorf("LocalPort(runner) = (%d, %v), want (1111, true)", p, ok)
	}
	if p, ok := fwds.LocalPort(PortSSH); !ok || p != 2222 {
		t.Errorf("LocalPort(ssh) = (%d, %v), want (2222, true)", p, ok)
	}
	if _, ok := fwds.LocalPort(PortOpencode); ok {
		t.Error("LocalPort(opencode) on a runner+ssh set should be absent")
	}
	if h, ok := fwds.Get(PortRunner); !ok || h != runnerH {
		t.Errorf("Get(runner) = (%v, %v), want (runnerH, true)", h, ok)
	}

	fwds.Close()

	var nilFwds Forwards
	if _, ok := nilFwds.Get(PortRunner); ok {
		t.Error("Get on a nil Forwards must report absent, not panic")
	}
	if _, ok := nilFwds.LocalPort(PortRunner); ok {
		t.Error("LocalPort on a nil Forwards must report absent, not panic")
	}
	nilFwds.Close() // must not panic
}

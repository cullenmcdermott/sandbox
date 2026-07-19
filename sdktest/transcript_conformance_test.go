package sdktest

// transcript_conformance_test.go — the event-sourced conformance test. It proves
// an external app can build the polished Sandbox transcript by feeding PUBLIC
// client.Event values into tui/transcript, drive it through the real interactions
// (send via tui/composer, scroll, follow-mode escape+recovery, tool expansion,
// approval via the real callback, resize, theme swap, interruption, detach), and
// frame it with tui/chrome — naming no internal/ type. Six goldens pin the
// distinct session states (empty, connecting, streaming, reconnect, fatal-error,
// approval).

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/tui/chrome"
	"github.com/cullenmcdermott/sandbox/tui/composer"
	"github.com/cullenmcdermott/sandbox/tui/theme"
	"github.com/cullenmcdermott/sandbox/tui/transcript"
)

var updateGolden = flag.Bool("update", false, "update golden files")

// clk is a controllable clock so the working indicator / footer are deterministic.
type clk struct{ t time.Time }

func (c *clk) now() time.Time { return c.t }

func evt(seq uint64, typ client.EventType, payload any) client.Event {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			panic(err)
		}
		raw = b
	}
	return client.Event{Seq: seq, Type: typ, Payload: raw}
}

func widthSafeFrame(t *testing.T, frame string, w int) {
	t.Helper()
	for i, l := range strings.Split(frame, "\n") {
		if lw := lipgloss.Width(l); lw > w {
			t.Errorf("line %d overflows width %d (%d cols): %q", i, w, lw, l)
		}
	}
}

// TestTranscriptFromPublicEvents is the event-sourced importability conformance:
// build the transcript from client.Event values, drive every host interaction,
// and render across sizes — all from public packages.
func TestTranscriptFromPublicEvents(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	c := &clk{t: time.Unix(1_700_000_000, 0)}

	var approvedID, approvedScope string
	detached := false
	interrupted := false

	tr := transcript.New(
		transcript.WithNow(c.now),
		transcript.WithBackend("anthropic"),
		transcript.WithMarkdown(false),
		transcript.WithApprove(func(id, scope string) { approvedID, approvedScope = id, scope }),
		transcript.WithInterrupt(func() { interrupted = true }),
		transcript.WithDetach(func() { detached = true }),
	)
	t.Cleanup(tr.Close)

	// A composer wired to submit into the transcript — the real send path.
	comp := composer.New(
		composer.WithNow(c.now),
		composer.WithSubmit(func(s string) { tr.Submit(s) }),
	)
	comp.Focus()

	tr.SetSize(100, 30)

	// A full turn, event-sourced.
	ec := 0
	script := []client.Event{
		evt(1, client.EventSessionStarted, client.SessionStartedPayload{Model: "claude-opus-4-8"}),
		evt(2, client.EventModelsAvailable, client.ModelsAvailablePayload{Models: []client.ModelInfo{{Value: "claude-opus-4-8", DisplayName: "Opus 4.8"}}}),
		evt(3, client.EventTurnStarted, client.TurnStartedPayload{Prompt: "find and fix the flake"}),
		evt(4, client.EventMessageCompleted, client.MessagePayload{Role: "user", Content: "find and fix the flake"}),
		evt(5, client.EventReasoningCompleted, client.MessagePayload{Content: "A backoff race is the likely culprit."}),
		evt(6, client.EventToolStarted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Input: json.RawMessage(`{"command":"go test ./..."}`)}),
		evt(7, client.EventToolCompleted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Output: "ok\tpkg\t0.2s\nline2\nline3\nPASS", ExitCode: &ec}),
		evt(8, client.EventTodoUpdated, client.TodoUpdatedPayload{Todos: []client.TodoItem{
			{Content: "Reproduce", Status: "completed"},
			{Content: "Fix", ActiveForm: "Fixing", Status: "in_progress"},
		}}),
		evt(9, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "Fixed the backoff race; all green."}),
		evt(10, client.EventUsageUpdated, client.UsagePayload{InputTokens: 3100, OutputTokens: 820, TotalCostUSD: 0.04}),
		evt(11, client.EventTurnCompleted, client.TurnCompletedPayload{}),
	}
	for _, e := range script {
		tr.Apply(e)
	}

	// --- send via the composer (the real key path) ---
	comp.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	comp.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	comp, _ = comp.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansi.Strip(tr.Render()), "hi") {
		t.Error("composer Enter did not submit into the transcript")
	}

	// --- scroll + follow-mode escape and recovery ---
	tr.GotoBottom()
	if !tr.Following() {
		t.Error("GotoBottom did not enter follow mode")
	}
	tr.ScrollBy(-3)
	if tr.Following() {
		t.Error("scrolling up did not leave follow mode")
	}
	tr.GotoBottom()
	if !tr.Following() {
		t.Error("GotoBottom did not recover follow mode")
	}

	// --- arbitrary tool expansion (the Bash card has multi-line output) ---
	before := tr.Render()
	if !tr.ToggleExpand() {
		t.Error("no expandable block to toggle")
	}
	if tr.Render() == before {
		t.Error("tool expansion did not change the frame")
	}

	// --- approval via the real callback ---
	tr.Apply(evt(12, client.EventPermissionRequested, client.PermissionPayload{PermissionID: "perm-1", Tool: "Bash", Input: json.RawMessage(`{"command":"rm -rf x"}`)}))
	if tr.PendingPermission() == nil {
		t.Fatal("permission not surfaced")
	}
	tr.Approve("session")
	if approvedID != "perm-1" || approvedScope != "session" {
		t.Errorf("approve callback got (%q,%q), want (perm-1,session)", approvedID, approvedScope)
	}
	if tr.PendingPermission() != nil {
		t.Error("approve did not clear the pending permission")
	}

	// --- resize across the target sizes, all width-safe ---
	for _, s := range [][2]int{{80, 24}, {100, 30}, {140, 40}} {
		tr.SetSize(s[0], s[1])
		tr.GotoBottom()
		widthSafeFrame(t, tr.Render(), s[0])
	}

	// --- theme swap re-skins (real ANSI change, structure preserved) ---
	tr.SetSize(100, 30)
	dark := tr.Render()
	theme.ApplyForBackground(false)
	light := tr.Render()
	if dark == light {
		t.Error("theme swap did not re-skin the transcript")
	}
	if ansi.Strip(dark) != ansi.Strip(light) {
		t.Error("theme swap changed transcript structure (should be color-only)")
	}
	theme.ApplyForBackground(true)

	// --- interruption + graceful detach ---
	tr.Interrupt()
	if !interrupted {
		t.Error("Interrupt did not fire the callback")
	}
	tr.Detach()
	if !detached {
		t.Error("Detach did not fire the callback")
	}
}

// TestTranscriptGoldens pins the six distinct session states (transcript body
// framed by a chrome status line) so a visual regression is caught.
func TestTranscriptGoldens(t *testing.T) {
	theme.ApplyForBackground(true)
	t.Cleanup(func() { theme.ApplyForBackground(true) })

	const w, h = 100, 28

	// frame stacks an optional chrome status line above the transcript body.
	frame := func(tr *transcript.Model, status string) string {
		tr.SetSize(w, h)
		tr.GotoTop()
		body := tr.Render()
		if status != "" {
			return ansi.Strip(status + "\n" + body)
		}
		return ansi.Strip(body)
	}

	newTr := func() *transcript.Model {
		c := &clk{t: time.Unix(1_700_000_000, 0)}
		return transcript.New(transcript.WithNow(c.now), transcript.WithBackend("anthropic"), transcript.WithMarkdown(false))
	}

	// empty — a fresh, unconnected transcript renders nothing.
	empty := newTr()
	checkSDKGolden(t, "empty_100x28.txt", frame(empty, ""))

	// connecting — nothing streamed yet; the host shows a chrome working line.
	connecting := newTr()
	checkSDKGolden(t, "connecting_100x28.txt", frame(connecting, chrome.WorkingIndicator(chrome.Working{Verb: "Connecting"})))

	// streaming — mid-turn: user prompt, reasoning, a running tool, a partial
	// streaming assistant reply, plus a chrome working indicator.
	streaming := newTr()
	streaming.Apply(evt(1, client.EventTurnStarted, client.TurnStartedPayload{}))
	streaming.Apply(evt(2, client.EventMessageCompleted, client.MessagePayload{Role: "user", Content: "stream the fix"}))
	streaming.Apply(evt(3, client.EventReasoningCompleted, client.MessagePayload{Content: "Reading the reconnect path."}))
	streaming.Apply(evt(4, client.EventToolStarted, client.ToolPayload{Tool: "Bash", ToolUseID: "t1", Input: json.RawMessage(`{"command":"go test ./..."}`)}))
	streaming.Apply(evt(5, client.EventMessageStarted, client.MessagePayload{Role: "assistant"}))
	streaming.Apply(evt(6, client.EventMessageDelta, client.MessagePayload{Role: "assistant", Content: "Working on it — ", Delta: true}))
	checkSDKGolden(t, "streaming_100x28.txt", frame(streaming, chrome.WorkingIndicator(chrome.Working{Verb: "Writing", Elapsed: 12 * time.Second, OutputTokens: 120})))

	// reconnect — the pod is rescheduling; the reducer drops the ⚠ notice.
	reconnect := newTr()
	reconnect.Apply(evt(1, client.EventMessageCompleted, client.MessagePayload{Role: "assistant", Content: "partial reply so far"}))
	reconnect.Apply(evt(2, client.EventSessionTerminating, client.TerminatingPayload{Reason: "pod terminating (SIGTERM)"}))
	checkSDKGolden(t, "reconnect_100x28.txt", frame(reconnect, chrome.WorkingIndicator(chrome.Working{Verb: "Reconnecting"})))

	// fatal-error — the turn died; a loud error notice lands in scrollback.
	fatal := newTr()
	fatal.Apply(evt(1, client.EventTurnStarted, client.TurnStartedPayload{}))
	fatal.Apply(evt(2, client.EventMessageCompleted, client.MessagePayload{Role: "user", Content: "run the migration"}))
	fatal.Apply(evt(3, client.EventTurnFailed, client.TurnFailedPayload{Message: "runner exited (code 137) — OOMKilled"}))
	checkSDKGolden(t, "fatal_100x28.txt", frame(fatal, ""))

	// approval — a permission request is pending review.
	approval := newTr()
	approval.Apply(evt(1, client.EventToolStarted, client.ToolPayload{Tool: "Edit", ToolUseID: "e1", Input: json.RawMessage(`{"file_path":"x.go","old_string":"a := 1","new_string":"a := 2"}`)}))
	approval.Apply(evt(2, client.EventPermissionRequested, client.PermissionPayload{PermissionID: "p1", Tool: "Edit", Input: json.RawMessage(`{"file_path":"x.go","old_string":"a := 1","new_string":"a := 2"}`)}))
	checkSDKGolden(t, "approval_100x28.txt", frame(approval, ""))
}

func checkSDKGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", name, err)
	}
	if string(want) != got {
		t.Errorf("golden %s mismatch (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}

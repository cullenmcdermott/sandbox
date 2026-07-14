package dashboard

// §2c numbered-option permission panel + §2b gap 2 ("always allow" reachable):
// the panel's key grammar (↑/↓ selection, number keys, ↵ confirm, a/d
// accelerators) and the session-scope wire value it finally sends — the runner
// has implemented scope:'session' tool-name grants all along; the TUI
// hardcoded Scope:"once" and offered only a/d.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// permModel returns a transcript with a pending Bash permission whose grace
// gate has already elapsed (since is backdated past permissionGraceCap), so
// resolution keys act immediately.
func permModel(t *testing.T) (*TranscriptModel, *fakeRunnerClient) {
	t.Helper()
	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	m.handleEvent(session.Event{
		Type:    session.EventPermissionRequested,
		Payload: json.RawMessage(`{"permissionId":"p1","tool":"Bash","input":{"command":"rm -rf build/"}}`),
	})
	if m.pending == nil {
		t.Fatal("no pending permission after permission.requested")
	}
	m.pending.since = nowFunc().Add(-2 * permissionGraceCap)
	return m, fc
}

// drain runs the returned command so the fake client records the decision.
func drain(t *testing.T, m *TranscriptModel, key string) {
	t.Helper()
	_, cmd := m.handleKey(keyMsg(key))
	if cmd != nil {
		cmd()
	}
}

// ORACLE (§2b gap 2): option 2 resolves allow with Scope:"session" — the wire
// value the runner's grant store keys on — and the scrollback line names the
// grant's tool-level breadth.
func TestPermissionSessionScopeReachable(t *testing.T) {
	m, fc := permModel(t)
	drain(t, m, "2")
	if len(fc.resolved) != 1 {
		t.Fatalf("want 1 resolution, got %d", len(fc.resolved))
	}
	d := fc.resolved[0]
	if !d.Allow || d.Scope != "session" || d.Permission != "p1" {
		t.Errorf("decision = %+v, want allow/session/p1", d)
	}
	found := false
	for _, b := range m.blocks {
		if b.kind == blockInfo && strings.Contains(b.text, "Bash allowed for this session") {
			found = true
		}
	}
	if !found {
		t.Error("session grant not named in scrollback")
	}
}

// The a/d accelerators keep their historical meaning: allow-once / deny-once.
func TestPermissionAccelerators(t *testing.T) {
	m, fc := permModel(t)
	drain(t, m, "a")
	if d := fc.resolved[0]; !d.Allow || d.Scope != "once" {
		t.Errorf("a → %+v, want allow once", d)
	}

	m2, fc2 := permModel(t)
	drain(t, m2, "d")
	if d := fc2.resolved[0]; d.Allow {
		t.Errorf("d → %+v, want deny", d)
	}
}

// ↓↓ then ↵ resolves the third option (No); the selection renders with the
// chevron on the selected row.
func TestPermissionArrowNavigationAndConfirm(t *testing.T) {
	m, fc := permModel(t)
	drain(t, m, "down")
	drain(t, m, "down")
	if m.pending.sel != 2 {
		t.Fatalf("sel = %d after two downs, want 2", m.pending.sel)
	}
	box := m.buildPermissionBox(m.width)
	if !strings.Contains(stripANSI(box), "› 3. No") {
		t.Errorf("selection chevron not on option 3:\n%s", stripANSI(box))
	}
	drain(t, m, "enter")
	if d := fc.resolved[0]; d.Allow {
		t.Errorf("enter on option 3 → %+v, want deny", d)
	}
}

// The grace gate still guards every resolving key (number, enter, a/d): a
// keystroke inside the quiet window is swallowed, not applied.
func TestPermissionGraceGateCoversPanelKeys(t *testing.T) {
	// Freeze the clock so the whole key burst deterministically lands inside
	// the grace window even on a slow CI runner.
	old := nowFunc
	t.Cleanup(func() { nowFunc = old })
	base := time.Now()
	nowFunc = func() time.Time { return base }

	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	m.handleEvent(session.Event{
		Type:    session.EventPermissionRequested,
		Payload: json.RawMessage(`{"permissionId":"p1","tool":"Bash","input":{"command":"ls"}}`),
	})
	// pending.since is nowFunc() — inside the grace window.
	for _, k := range []string{"1", "2", "enter", "a", "d"} {
		drain(t, m, k)
	}
	if len(fc.resolved) != 0 {
		t.Errorf("grace-gated keys resolved anyway: %+v", fc.resolved)
	}
	if m.pending == nil {
		t.Error("pending cleared by a grace-gated key")
	}
}

// permPromptKey's pure grammar: clamped navigation, digit direct-select,
// out-of-range digits unhandled (fall through to scroll), accelerators.
func TestPermPromptKeyGrammar(t *testing.T) {
	cases := []struct {
		key         string
		sel         int
		wantSel     int
		wantResolve int
		wantHandled bool
	}{
		{"up", 0, 0, -1, true},   // clamp at top
		{"down", 2, 2, -1, true}, // clamp at bottom
		{"down", 0, 1, -1, true},
		{"enter", 1, 1, 1, true}, // confirm the selection
		{"1", 2, 0, 0, true},     // digit selects AND resolves
		{"3", 0, 2, 2, true},
		{"4", 0, 0, -1, false}, // out of range: unhandled
		{"a", 2, 2, 0, true},   // accelerator: allow once
		{"d", 0, 0, 2, true},   // accelerator: deny
		{"j", 1, 1, -1, false}, // j/k stay with transcript scroll
	}
	for _, tc := range cases {
		gotSel, gotResolve, gotHandled := permPromptKey(tc.key, tc.sel, 3)
		if gotSel != tc.wantSel || gotResolve != tc.wantResolve || gotHandled != tc.wantHandled {
			t.Errorf("permPromptKey(%q, sel=%d) = (%d, %d, %v), want (%d, %d, %v)",
				tc.key, tc.sel, gotSel, gotResolve, gotHandled, tc.wantSel, tc.wantResolve, tc.wantHandled)
		}
	}
}

// The grace gate follows the injectable clock (nowFunc), pinned here so the
// backdating trick above stays valid.
func TestPermissionGraceUsesInjectableClock(t *testing.T) {
	old := nowFunc
	t.Cleanup(func() { nowFunc = old })
	base := time.Now()
	nowFunc = func() time.Time { return base }

	fc := &fakeRunnerClient{}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	m.handleEvent(session.Event{
		Type:    session.EventPermissionRequested,
		Payload: json.RawMessage(`{"permissionId":"p1","tool":"Bash","input":{"command":"ls"}}`),
	})
	drain(t, m, "1")
	if len(fc.resolved) != 0 {
		t.Fatal("resolved inside the grace window")
	}
	nowFunc = func() time.Time { return base.Add(2 * permissionGraceCap) }
	drain(t, m, "1")
	if len(fc.resolved) != 1 || !fc.resolved[0].Allow || fc.resolved[0].Scope != "once" {
		t.Errorf("post-grace 1 → %+v, want allow once", fc.resolved)
	}
}

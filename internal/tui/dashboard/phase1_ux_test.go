package dashboard

import (
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// --------------------------------------------------------------------------
// Phase 1: permission approve/deny errors are surfaced, not swallowed
// --------------------------------------------------------------------------

// ORACLE: a failed ResolvePermission flows back as approveResultMsg{err}, and the
// Update handler records it in m.actionErr (the missing-case regression: the
// message used to fall through to a no-op and the error vanished).
func TestApproveResultErrorSurfaced(t *testing.T) {
	id := session.ID("s1")
	fc := &fakeRunnerClient{resolveErr: errors.New("resolve boom")}
	m := New(&fakeBackend{})
	m.liveSSEClients = map[session.ID]RunnerClient{id: fc}

	sess := Session{
		State:               session.State{ID: id, Status: session.StatusRunning},
		PendingPermissionID: "p1",
	}
	cmd := m.approveCmd(sess, true)
	if cmd == nil {
		t.Fatal("approveCmd returned no command")
	}
	msg := cmd()
	ar, ok := msg.(approveResultMsg)
	if !ok {
		t.Fatalf("approveCmd msg = %T, want approveResultMsg", msg)
	}
	if ar.err == nil {
		t.Fatal("approveResultMsg.err is nil; the resolve error was dropped")
	}

	m.Update(ar)
	if m.actionErr == nil {
		t.Fatal("m.actionErr not set after a failed approveResultMsg")
	}
	if !strings.Contains(m.actionErr.Error(), "resolve boom") {
		t.Errorf("m.actionErr = %q, want it to wrap the resolve error", m.actionErr)
	}
}

// ORACLE: a successful approveResultMsg clears a stale actionErr.
func TestApproveResultSuccessClearsError(t *testing.T) {
	m := New(&fakeBackend{})
	m.actionErr = errors.New("stale")
	m.Update(approveResultMsg{id: "s1", err: nil})
	if m.actionErr != nil {
		t.Errorf("m.actionErr = %v, want nil after a successful approve", m.actionErr)
	}
}

// --------------------------------------------------------------------------
// Phase 1: actionable failure state when the cluster is unreachable
// --------------------------------------------------------------------------

// ORACLE: seedCmd surfaces a List error as seedFailedMsg (it used to return nil,
// leaving the dashboard on skeleton bars forever).
func TestSeedCmdSurfacesListError(t *testing.T) {
	fb := &fakeBackend{listErr: errors.New("no route to host")}
	m := New(fb)
	msg := m.seedCmd()()
	sf, ok := msg.(seedFailedMsg)
	if !ok {
		t.Fatalf("seedCmd msg = %T, want seedFailedMsg", msg)
	}
	if sf.err == nil {
		t.Fatal("seedFailedMsg.err is nil")
	}
}

// ORACLE: after a seed failure, renderRowLines shows the error + retry hint, not
// skeleton bars; pressing r clears the error and re-issues the seed.
func TestSeedFailureShowsErrorAndRetry(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "1")
	m := New(&fakeBackend{})
	m.width, m.height = 80, 20

	m.Update(seedFailedMsg{err: errors.New("connection refused")})
	out := m.render()
	if hasSkeletonBar(out) {
		t.Fatalf("seed-failure render must not show skeleton bars; got:\n%q", out)
	}
	if !strings.Contains(out, "cluster") {
		t.Fatalf("seed-failure render should mention the cluster; got:\n%q", out)
	}
	if !strings.Contains(out, "retry") {
		t.Fatalf("seed-failure render should offer a retry; got:\n%q", out)
	}

	// r clears the error and re-issues seed + watch.
	_, cmd := m.handleKey(keyMsg("r"))
	if m.seedErr != nil {
		t.Error("r did not clear m.seedErr")
	}
	if cmd == nil {
		t.Error("r did not re-issue the seed command")
	}
}

// ORACLE: a successful seed clears a prior seedErr (self-heal on reconnect).
func TestSuccessfulSeedClearsSeedErr(t *testing.T) {
	m := New(&fakeBackend{})
	m.seedErr = errors.New("was down")
	m, _ = m.applySeed(nil)
	if m.seedErr != nil {
		t.Errorf("m.seedErr = %v, want nil after a successful seed", m.seedErr)
	}
}

// --------------------------------------------------------------------------
// Phase 1: esc dismisses a lingering connect/action error in the detail pane
// --------------------------------------------------------------------------

// ORACLE: esc clears connectErr/actionErr when no overlay is open.
func TestEscDismissesDetailErrors(t *testing.T) {
	m := New(nil)
	m.connectErr = errors.New("connect failed")
	m.actionErr = errors.New("suspend failed")
	m.handleKey(keyMsg("esc"))
	if m.connectErr != nil || m.actionErr != nil {
		t.Errorf("esc did not dismiss errors: connectErr=%v actionErr=%v", m.connectErr, m.actionErr)
	}
}

// --------------------------------------------------------------------------
// Phase 1: chat-side permission resolve errors are surfaced, not dropped
// --------------------------------------------------------------------------

// ORACLE: when ResolvePermission fails in the chat view, the Cmd returns a
// permResolveErrMsg and the Update handler appends a loud error block — instead
// of the old `_ =`-drop that left the optimistic "[permission approved]" block
// looking successful.
func TestChatPermissionResolveErrorSurfaced(t *testing.T) {
	fc := &fakeRunnerClient{resolveErr: errors.New("post timeout")}
	m := NewTranscript(fc, transcriptSession(), nil)
	m.width, m.height = 80, 24
	m.layout()
	m.pending = &transcriptPermission{id: "p1", tool: "Bash"}

	cmd := m.resolvePermission(true)
	if cmd == nil {
		t.Fatal("resolvePermission returned no command")
	}
	msg := cmd()
	pe, ok := msg.(permResolveErrMsg)
	if !ok {
		t.Fatalf("resolvePermission msg = %T, want permResolveErrMsg", msg)
	}

	m.Update(pe)
	if len(m.blocks) == 0 {
		t.Fatal("no blocks after permResolveErrMsg")
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockError {
		t.Errorf("last block kind = %v, want blockError", last.kind)
	}
	if !strings.Contains(last.text, "permission not delivered") {
		t.Errorf("last block = %q, want it to flag the undelivered permission", last.text)
	}
}

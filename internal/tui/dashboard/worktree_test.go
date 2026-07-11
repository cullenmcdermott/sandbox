package dashboard

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/tui/terminal"
)

// fakeWorktreeOps is a test double for the WorktreeOps seam. It records the
// arguments Convert was called with so tests can assert the deterministic
// prefill flowed through unchanged.
type fakeWorktreeOps struct {
	branch    string
	dirty     bool
	changed   []string
	statusErr error

	convertBranch string
	convertErr    error

	gotBranch    string
	gotMessage   string
	convertCalls int
}

func (f *fakeWorktreeOps) Status(_ context.Context, _ session.ID) (string, bool, []string, error) {
	if f.statusErr != nil {
		return "", false, nil, f.statusErr
	}
	return f.branch, f.dirty, f.changed, nil
}

func (f *fakeWorktreeOps) Convert(_ context.Context, _ session.ID, branchName, message string) (string, bool, error) {
	f.convertCalls++
	f.gotBranch, f.gotMessage = branchName, message
	if f.convertErr != nil {
		return "", false, f.convertErr
	}
	b := f.convertBranch
	if b == "" {
		b = branchName
	}
	return b, true, nil
}

// modelWithOneSession builds a dashboard Model with a single selectable session
// carrying the given auto title, plus the injected worktree seam.
func modelWithOneSession(ops WorktreeOps, autoTitle string) *Model {
	m := New(nil)
	m.caps = terminal.Caps{} // untargetable terminal: no desktop-notify cmd in the batch
	m.worktreeOps = ops
	m.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		AutoTitle:        autoTitle,
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
	}}
	return m
}

// TestBranchKeyOpensConvertModalWithPrefill drives `b` through the Status probe
// and asserts the modal opens with the deterministic title-derived prefill
// (branch = feat/<slug>, message = the title) and the git facts from Status.
func TestBranchKeyOpensConvertModalWithPrefill(t *testing.T) {
	ops := &fakeWorktreeOps{branch: "sandbox/s1", dirty: true, changed: []string{"a.go", "b.go"}}
	m := modelWithOneSession(ops, "Fix login flow")

	_, cmd := m.handleKey(keyMsg("b"))
	if cmd == nil {
		t.Fatal("b produced no command")
	}
	statusMsg, ok := cmd().(worktreeStatusMsg)
	if !ok {
		t.Fatalf("want worktreeStatusMsg, got %T", cmd())
	}
	m.Update(statusMsg)

	if m.convert == nil {
		t.Fatal("convert modal did not open")
	}
	if got := m.convert.branch.Value(); got != "feat/fix-login-flow" {
		t.Errorf("branch prefill = %q, want %q", got, "feat/fix-login-flow")
	}
	if got := m.convert.message.Value(); got != "Fix login flow" {
		t.Errorf("message prefill = %q, want %q", got, "Fix login flow")
	}
	if m.convert.curBranch != "sandbox/s1" {
		t.Errorf("curBranch = %q, want sandbox/s1", m.convert.curBranch)
	}
	if !m.convert.dirty || m.convert.changed != 2 {
		t.Errorf("facts: dirty=%v changed=%d, want dirty=true changed=2", m.convert.dirty, m.convert.changed)
	}
}

// TestBranchKeyNoWorktreeToast asserts that a session without a worktree shows a
// toast (via the toast plumbing) instead of the modal.
func TestBranchKeyNoWorktreeToast(t *testing.T) {
	ops := &fakeWorktreeOps{statusErr: ErrNoWorktree}
	m := modelWithOneSession(ops, "some title")

	_, cmd := m.handleKey(keyMsg("b"))
	if cmd == nil {
		t.Fatal("b produced no command")
	}
	statusMsg := cmd().(worktreeStatusMsg)
	_, toastCmd := m.Update(statusMsg)

	if m.convert != nil {
		t.Fatal("modal opened for a session with no worktree; want toast only")
	}
	if toastCmd == nil {
		t.Fatal("no toast command emitted for the no-worktree case")
	}
	toast, ok := toastCmd().(toastMsg)
	if !ok {
		t.Fatalf("want toastMsg, got %T", toastCmd())
	}
	if !strings.Contains(toast.note, "no git worktree") {
		t.Errorf("toast note = %q, want it to mention no git worktree", toast.note)
	}
}

// TestConvertSuccessToast drives the full accept path: open modal, press enter,
// resolve Convert, and assert the success toast plus that the approved strings
// reached the seam unchanged.
func TestConvertSuccessToast(t *testing.T) {
	ops := &fakeWorktreeOps{branch: "sandbox/s1", dirty: true, changed: []string{"a.go"}, convertBranch: "feat/fix-login-flow"}
	m := modelWithOneSession(ops, "Fix login flow")

	// Open the modal via the b-flow.
	_, cmd := m.handleKey(keyMsg("b"))
	m.Update(cmd().(worktreeStatusMsg))
	if m.convert == nil {
		t.Fatal("modal did not open")
	}

	// Accept.
	_, convCmd := m.handleKey(keyMsg("enter"))
	if convCmd == nil {
		t.Fatal("enter produced no convert command")
	}
	if !m.convert.busy {
		t.Error("modal should be busy while Convert is in flight")
	}
	result, ok := convCmd().(convertResultMsg)
	if !ok {
		t.Fatalf("want convertResultMsg, got %T", convCmd())
	}
	_, toastCmd := m.Update(result)

	if ops.convertCalls != 1 {
		t.Fatalf("Convert called %d times, want 1", ops.convertCalls)
	}
	if ops.gotBranch != "feat/fix-login-flow" || ops.gotMessage != "Fix login flow" {
		t.Errorf("Convert got (%q, %q), want (feat/fix-login-flow, Fix login flow)", ops.gotBranch, ops.gotMessage)
	}
	if m.convert != nil {
		t.Error("modal should close on a successful convert")
	}
	if toastCmd == nil {
		t.Fatal("no success toast emitted")
	}
	toast := toastCmd().(toastMsg)
	if !strings.Contains(toast.note, "feat/fix-login-flow") || !strings.Contains(toast.note, "ready to merge") {
		t.Errorf("toast note = %q, want branch ready-to-merge message", toast.note)
	}
}

// TestConvertBranchNameTakenKeepsModal asserts a taken branch name keeps the
// modal open with an inline error rather than closing it.
func TestConvertBranchNameTakenKeepsModal(t *testing.T) {
	ops := &fakeWorktreeOps{branch: "sandbox/s1", dirty: true, changed: []string{"a.go"}, convertErr: fmt.Errorf("%w: taken", ErrBranchNameTaken)}
	m := modelWithOneSession(ops, "Fix login flow")

	_, cmd := m.handleKey(keyMsg("b"))
	m.Update(cmd().(worktreeStatusMsg))

	_, convCmd := m.handleKey(keyMsg("enter"))
	result := convCmd().(convertResultMsg)
	m.Update(result)

	if m.convert == nil {
		t.Fatal("modal closed on a taken branch name; it should stay open for a retry")
	}
	if m.convert.busy {
		t.Error("modal should no longer be busy after the failed convert")
	}
	if !strings.Contains(m.convert.inlineErr, "already taken") {
		t.Errorf("inline error = %q, want a taken-name message", m.convert.inlineErr)
	}
}

// TestConvertPrefill covers the deterministic prefill rules including the
// no-title fallback.
func TestConvertPrefill(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		id          string
		current     string
		wantBranch  string
		wantMessage string
	}{
		{"slugified title", "Fix login flow", "s1", "sandbox/s1", "feat/fix-login-flow", "Fix login flow"},
		{"punctuation collapses", "Add OAuth2 + PKCE!", "s2", "sandbox/s2", "feat/add-oauth2-pkce", "Add OAuth2 + PKCE!"},
		{"no title falls back to current branch", "", "s3", "sandbox/s3", "sandbox/s3", "sandbox session s3"},
		{"empty current falls back to sandbox id", "", "s4", "", "sandbox/s4", "sandbox session s4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			branch, message := convertPrefill(c.title, c.id, c.current)
			if branch != c.wantBranch {
				t.Errorf("branch = %q, want %q", branch, c.wantBranch)
			}
			if message != c.wantMessage {
				t.Errorf("message = %q, want %q", message, c.wantMessage)
			}
		})
	}
}

// TestSlugify covers the slug helper edge cases.
func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Fix login flow": "fix-login-flow",
		"  trim  me  ":   "trim-me",
		"UPPER→lower":    "upper-lower",
		"!!!":            "",
		"already-a-slug": "already-a-slug",
		"multi   spaces": "multi-spaces",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

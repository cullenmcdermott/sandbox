package dashboard

import (
	"errors"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Regression for C2: TUI destroy must run the local-cleanup hook after a
// successful backend.Destroy (the prior test only checked the confirm gate +
// that Destroy dispatched, never that the hook fired — so the leak fix was
// unexercised).
func TestDestroyHookFiresOnSuccess(t *testing.T) {
	fb := &fakeBackend{}
	var got session.ID
	m := New(fb).WithDestroyHook(func(id session.ID) { got = id })

	cmd := m.destroyCmd(session.Ref{ID: "doomed"})
	if cmd == nil {
		t.Fatal("destroyCmd returned nil")
	}
	msg := cmd()
	res, ok := msg.(actionResultMsg)
	if !ok {
		t.Fatalf("expected actionResultMsg, got %T", msg)
	}
	if res.err != nil {
		t.Fatalf("destroy errored: %v", res.err)
	}
	if got != "doomed" {
		t.Fatalf("destroy hook not called with the session id; got %q", got)
	}
}

// The hook must NOT fire when backend.Destroy fails — local state stays until the
// cluster resource is actually gone.
func TestDestroyHookSkippedOnError(t *testing.T) {
	fb := &fakeBackend{actionErr: errors.New("destroy failed")}
	called := false
	m := New(fb).WithDestroyHook(func(session.ID) { called = true })

	if msg := m.destroyCmd(session.Ref{ID: "x"})(); msg.(actionResultMsg).err == nil {
		t.Fatal("expected a destroy error")
	}
	if called {
		t.Fatal("destroy hook must not fire when backend.Destroy errors")
	}
}

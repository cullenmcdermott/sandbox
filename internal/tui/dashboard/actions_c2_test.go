package dashboard

import (
	"errors"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// destroyCmd dispatches Backend.Destroy for the acted-upon session and surfaces
// the outcome as an actionResultMsg. The sync-teardown ordering and irreversible
// local cleanup that a destroy entails now live behind Backend.Destroy (the
// client SDK adapter, §1g), so the dashboard's contract is simply "dispatch the
// destroy and report what happened".
func TestDestroyDispatchesToBackend(t *testing.T) {
	fb := &fakeBackend{}
	m := New(fb)

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
	if len(fb.destroyed) != 1 || fb.destroyed[0] != "doomed" {
		t.Fatalf("Destroy not dispatched once for doomed: %v", fb.destroyed)
	}
}

// A backend Destroy failure propagates through the actionResultMsg so the detail
// pane can surface it.
func TestDestroyReportsBackendError(t *testing.T) {
	fb := &fakeBackend{actionErr: errors.New("destroy failed")}
	m := New(fb)

	if msg := m.destroyCmd(session.Ref{ID: "x"})(); msg.(actionResultMsg).err == nil {
		t.Fatal("expected a destroy error to propagate")
	}
}

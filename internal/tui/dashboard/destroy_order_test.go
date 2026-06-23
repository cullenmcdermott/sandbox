package dashboard

import (
	"errors"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestDestroyStopsSyncBeforeDestroy verifies the teardown ordering: the
// pre-destroy hook (sync stop) runs BEFORE backend.Destroy, and the
// post-destroy hook (irreversible local cleanup) runs only AFTER a successful
// Destroy. Each hook observes the fake backend's recorded state to pin its
// position relative to the Destroy call.
func TestDestroyStopsSyncBeforeDestroy(t *testing.T) {
	fb := &fakeBackend{}
	var preSawDestroy, postSawDestroy bool

	m := New(fb)
	m.WithPreDestroyHook(func(session.ID) { preSawDestroy = len(fb.destroyed) > 0 })
	m.WithDestroyHook(func(session.ID) { postSawDestroy = len(fb.destroyed) > 0 })

	cmd := m.destroyCmd(session.Ref{ID: "s1"})
	if cmd == nil {
		t.Fatal("destroyCmd returned nil")
	}
	cmd() // execute the command synchronously

	if preSawDestroy {
		t.Fatal("pre-destroy hook ran AFTER backend.Destroy — sync would race the dying pod")
	}
	if !postSawDestroy {
		t.Fatal("post-destroy local cleanup did not run after a successful Destroy")
	}
	if len(fb.destroyed) != 1 || fb.destroyed[0] != "s1" {
		t.Fatalf("Destroy not called once for s1: %v", fb.destroyed)
	}
}

// TestPreDestroyRunsEvenWhenDestroyFails confirms the pre-destroy sync stop is
// unconditional (it's recoverable), while the irreversible post-destroy cleanup
// is gated on success.
func TestPreDestroyRunsEvenWhenDestroyFails(t *testing.T) {
	fb := &fakeBackend{actionErr: errors.New("cluster unreachable")}
	preRan, postRan := false, false

	m := New(fb)
	m.WithPreDestroyHook(func(session.ID) { preRan = true })
	m.WithDestroyHook(func(session.ID) { postRan = true })

	m.destroyCmd(session.Ref{ID: "s1"})()

	if !preRan {
		t.Fatal("pre-destroy sync stop must run even when Destroy fails")
	}
	if postRan {
		t.Fatal("irreversible local cleanup must NOT run when Destroy fails")
	}
}

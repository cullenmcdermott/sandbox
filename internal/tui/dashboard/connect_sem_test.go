package dashboard

import (
	"testing"
	"time"
)

// FU2: the background-connect semaphore bounds how many observer connects run
// their setup at once. A third acquire against a cap-2 semaphore must block until
// a slot is released.
func TestAcquireConnectSlotCaps(t *testing.T) {
	sem := make(chan struct{}, 2)
	r1 := acquireConnectSlot(sem)
	_ = acquireConnectSlot(sem) // cap now full

	acquired := make(chan struct{})
	go func() {
		r3 := acquireConnectSlot(sem)
		close(acquired)
		r3()
	}()

	select {
	case <-acquired:
		t.Fatal("third acquire should block while the cap is full")
	case <-time.After(50 * time.Millisecond):
	}

	r1() // free a slot
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("third acquire should proceed after a release")
	}
}

// A nil semaphore (a Model built directly in a test) must be a no-op, and the
// release must be idempotent (it uses sync.Once, so a double release can't
// drain a slot it never took).
func TestAcquireConnectSlotNilAndIdempotent(t *testing.T) {
	release := acquireConnectSlot(nil)
	release()
	release() // must not panic or block

	sem := make(chan struct{}, 1)
	r := acquireConnectSlot(sem)
	r()
	r() // idempotent: must not block on the now-empty channel

	// The slot is free again, so a fresh acquire succeeds immediately.
	done := make(chan struct{})
	go func() { acquireConnectSlot(sem)(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idempotent release left the semaphore stuck full")
	}
}

// New() wires the cap so the throttle is active in production.
func TestNewModelHasConnectSemaphore(t *testing.T) {
	m := New(nil)
	if cap(m.connectSem) != maxConcurrentBackgroundConnects {
		t.Fatalf("connectSem cap = %d, want %d", cap(m.connectSem), maxConcurrentBackgroundConnects)
	}
}

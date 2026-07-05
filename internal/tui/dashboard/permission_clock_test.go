package dashboard

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// The permission grace gate anchors pending.since on nowFunc (not raw time.Now),
// so the anti-type-ahead behavior is governed by the same injectable clock
// permissionAnswerable() reads. Before this fix `since` used the real wall clock
// while `now` used the swapped nowFunc, so a test clock could not exercise the
// gate at all (now.Sub(since) was a garbage delta).
func TestPermissionGraceGateUsesInjectableClock(t *testing.T) {
	old := nowFunc
	defer func() { nowFunc = old }()
	base := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := base
	nowFunc = func() time.Time { return clock }

	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.handleEvent(session.Event{
		Type: session.EventPermissionRequested,
		Payload: mustJSON(session.PermissionPayload{
			PermissionID: "perm-1",
			Tool:         "Bash",
			Input:        json.RawMessage(`{"command":"ls"}`),
		}),
	})
	if m.pending == nil {
		t.Fatal("setup: permission was not registered")
	}
	if !m.pending.since.Equal(base) {
		t.Fatalf("pending.since = %v, want the injected clock base %v (still on time.Now?)", m.pending.since, base)
	}

	// At t0 the gate blocks the keystroke (type-ahead protection): no time has
	// elapsed on the injected clock.
	if m.permissionAnswerable(time.Time{}) {
		t.Fatal("permission should NOT be answerable at t0 (grace gate open)")
	}

	// Advancing the injected clock past the hard cap makes it answerable — the
	// gate is now fully controllable from the test clock.
	clock = base.Add(permissionGraceCap)
	if !m.permissionAnswerable(time.Time{}) {
		t.Fatal("permission should be answerable once the injected clock passes permissionGraceCap")
	}
}

func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// The per-row status-flash motion window is measured on nowFunc, so a test clock
// governs whether a row is still "in motion" — driving the self-scheduling tick
// loop deterministically.
func TestRowMotionActiveUsesInjectableClock(t *testing.T) {
	old := nowFunc
	defer func() { nowFunc = old }()
	base := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := base
	nowFunc = func() time.Time { return clock }

	m := New(nil)
	s := SessionFromState(session.State{ID: "s1", Status: session.StatusRunning})
	s.statusChangedAt = base
	m.sessions = []Session{s}

	clock = base.Add(statusFlashDur - time.Millisecond)
	if !m.rowMotionActive() {
		t.Fatal("row should be mid-flash within statusFlashDur of statusChangedAt")
	}
	clock = base.Add(statusFlashDur + time.Millisecond)
	if m.rowMotionActive() {
		t.Fatal("row flash should have ended once the injected clock passes statusFlashDur")
	}
}

// The cross-session toast auto-dismisses on nowFunc, so a test clock controls
// expiry without real sleeps.
func TestToastDismissUsesInjectableClock(t *testing.T) {
	old := nowFunc
	defer func() { nowFunc = old }()
	base := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := base
	nowFunc = func() time.Time { return clock }

	m := New(nil)
	m.toast = &notification{createdAt: base}

	// Before expiry, a tick keeps the toast.
	clock = base.Add(toastDismissAfter - time.Millisecond)
	next, _ := m.Update(toastTickMsg{})
	m = next.(*Model)
	if m.toast == nil {
		t.Fatal("toast dismissed before toastDismissAfter on the injected clock")
	}
	// Past expiry, the next tick clears it.
	clock = base.Add(toastDismissAfter + time.Millisecond)
	next, _ = m.Update(toastTickMsg{})
	m = next.(*Model)
	if m.toast != nil {
		t.Fatal("toast should auto-dismiss once the injected clock passes toastDismissAfter")
	}
}

// The transitions.go fade helpers read nowFunc, so a test clock past a fade
// window collapses the fade to its end state. Motion is forced ON so the
// past-window value is a clean discriminator (a real-clock regression, with the
// injected base years ahead of wall time, would NOT hit the window early-return
// and would compute the opposite end value).
func TestTransitionFadesUseInjectableClock(t *testing.T) {
	t.Setenv("SANDBOX_REDUCE_MOTION", "0")
	t.Setenv("NO_COLOR", "")
	old := nowFunc
	defer func() { nowFunc = old }()
	base := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := base
	nowFunc = func() time.Time { return clock }
	since := base

	clock = base.Add(rowEnterDur + time.Millisecond)
	if got := rowEnter(since); got != 1 {
		t.Fatalf("rowEnter past its window = %v, want 1 (fade complete on the injected clock)", got)
	}
	clock = base.Add(statusFlashDur + time.Millisecond)
	if got := statusFlash(since); got != 0 {
		t.Fatalf("statusFlash past its window = %v, want 0 (flash decayed on the injected clock)", got)
	}
}

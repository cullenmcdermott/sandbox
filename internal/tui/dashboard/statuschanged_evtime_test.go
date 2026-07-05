package dashboard

import (
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// §1a step 1: ApplyRunnerEvent must stamp statusChangedAt from the event's own
// time, not wall-clock, so a replayed/catch-up status transition doesn't
// re-trigger the row glyph flash on every relaunch.
func TestApplyRunnerEventStampsStatusChangedAtFromEventTime(t *testing.T) {
	// An OLD event time: the transition is real (Idle→Busy) but already past the
	// flash window, so no flash on catch-up.
	oldT := time.Now().Add(-10 * time.Minute).UTC()
	s := SessionFromState(session.State{ID: "s1", Status: session.StatusRunning})
	if s.DashStatus != StatusIdle {
		t.Fatalf("setup: fresh Running session should start Idle, got %v", s.DashStatus)
	}
	changed := ApplyRunnerEvent(&s, session.Event{
		Type: session.EventTurnStarted,
		Time: oldT.Format(time.RFC3339Nano),
	})
	if !changed || s.DashStatus != StatusBusy {
		t.Fatalf("turn.started should transition Idle→Busy (changed=%v status=%v)", changed, s.DashStatus)
	}
	if !s.statusChangedAt.Equal(oldT) {
		t.Fatalf("statusChangedAt = %v, want the event's own time %v", s.statusChangedAt, oldT)
	}

	// A LIVE event time (~now): statusChangedAt tracks it, so a genuinely-new
	// transition still flashes.
	s2 := SessionFromState(session.State{ID: "s2", Status: session.StatusRunning})
	now := time.Now().UTC()
	ApplyRunnerEvent(&s2, session.Event{Type: session.EventTurnStarted, Time: now.Format(time.RFC3339Nano)})
	if s2.statusChangedAt.Before(now.Add(-time.Second)) || s2.statusChangedAt.After(now.Add(time.Second)) {
		t.Fatalf("live-time statusChangedAt = %v, want ≈ %v", s2.statusChangedAt, now)
	}

	// An EMPTY/unparseable time falls back to wall-clock (~now).
	s3 := SessionFromState(session.State{ID: "s3", Status: session.StatusRunning})
	before := time.Now()
	ApplyRunnerEvent(&s3, session.Event{Type: session.EventTurnStarted, Time: ""})
	if s3.statusChangedAt.Before(before) || s3.statusChangedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("empty-time fallback statusChangedAt = %v, want ≈ now", s3.statusChangedAt)
	}
}

func TestEventTime(t *testing.T) {
	// ISO-8601 with milliseconds (what the runner emits) must parse.
	ms := "2026-07-05T12:00:00.123Z"
	if got, ok := eventTime(session.Event{Time: ms}); !ok || got.UTC().Format(time.RFC3339Nano) != "2026-07-05T12:00:00.123Z" {
		t.Fatalf("eventTime(%q) = %v,%v; want parsed ok", ms, got, ok)
	}
	// Plain RFC3339 (no fractional) must also parse.
	if _, ok := eventTime(session.Event{Time: "2026-07-05T12:00:00Z"}); !ok {
		t.Fatal("eventTime should accept RFC3339 without fractional seconds")
	}
	// Empty and garbage return ok=false.
	if _, ok := eventTime(session.Event{Time: ""}); ok {
		t.Fatal("eventTime(empty) should be ok=false")
	}
	if _, ok := eventTime(session.Event{Time: "not-a-time"}); ok {
		t.Fatal("eventTime(garbage) should be ok=false")
	}
}

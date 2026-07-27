package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// The runner has always emitted rate_limit.updated and session.RateLimitPayload
// has always been fully defined, but between the deletion of the transcript
// status line and this change NOTHING parsed the payload: the read-model had no
// case and ApplyRunnerEvent did not list the type, so plan usage was dropped on
// the floor.
func TestRateLimitUpdatedReachesTheReadModel(t *testing.T) {
	sess := makeSession("s1", StatusBusy)
	reset := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	ApplyRunnerEvent(&sess, mkEvent(session.EventRateLimitUpdated, session.RateLimitPayload{
		Available:        true,
		SubscriptionType: "max",
		FiveHourUtil:     42,
		FiveHourResetsAt: reset.Format(time.RFC3339),
		SevenDayUtil:     18,
	}))

	if !sess.RateLimitOK {
		t.Fatal("an available report must open the render gate")
	}
	if sess.FiveHourUtil != 42 || sess.SevenDayUtil != 18 {
		t.Errorf("utils = %v/%v, want 42/18", sess.FiveHourUtil, sess.SevenDayUtil)
	}
	if !sess.FiveHourResetsAt.Equal(reset) {
		t.Errorf("5h reset = %v, want %v", sess.FiveHourResetsAt, reset)
	}
	// Absent on the wire → zero time, which renders as a bare percentage.
	if !sess.SevenDayResetsAt.IsZero() {
		t.Errorf("weekly reset = %v, want zero for an omitted field", sess.SevenDayResetsAt)
	}
	if sess.RateLimitSubscription != "max" {
		t.Errorf("subscription = %q, want max", sess.RateLimitSubscription)
	}
}

// The guard the backlog item asked for: a session that never reported a window
// must show NOTHING, not a fabricated "5h 0%". Zero utilization and "never
// reported" are indistinguishable in the numbers, so the gate is a separate
// flag, not a value check.
func TestRateLimitRendersNothingUntilReported(t *testing.T) {
	sess := makeSession("s1", StatusBusy)
	if segs := sess.rateLimitSegs(time.Now()); len(segs) != 0 {
		t.Fatalf("un-reported session rendered %v, want nothing", segs)
	}

	// An UNAVAILABLE report (API-key / Bedrock / Vertex auth) also renders nothing.
	ApplyRunnerEvent(&sess, mkEvent(session.EventRateLimitUpdated, session.RateLimitPayload{
		Available: false,
	}))
	if segs := sess.rateLimitSegs(time.Now()); len(segs) != 0 {
		t.Fatalf("unavailable report rendered %v, want nothing", segs)
	}

	// A genuine 0% DOES render — that is a real fact about the plan.
	ApplyRunnerEvent(&sess, mkEvent(session.EventRateLimitUpdated, session.RateLimitPayload{
		Available: true, FiveHourUtil: 0, SevenDayUtil: 0,
	}))
	segs := sess.rateLimitSegs(time.Now())
	if len(segs) != 2 || segs[0] != "5h 0%" || segs[1] != "wk 0%" {
		t.Fatalf("segs = %v, want [5h 0%% wk 0%%]", segs)
	}
}

// Losing the plan (an available report followed by an unavailable one) must
// close the gate rather than freeze the last known windows on screen.
func TestRateLimitUnavailableClosesTheGate(t *testing.T) {
	sess := makeSession("s1", StatusBusy)
	ApplyRunnerEvent(&sess, mkEvent(session.EventRateLimitUpdated, session.RateLimitPayload{
		Available: true, FiveHourUtil: 90, SevenDayUtil: 90,
	}))
	if len(sess.rateLimitSegs(time.Now())) != 2 {
		t.Fatal("setup: windows should render after an available report")
	}
	ApplyRunnerEvent(&sess, mkEvent(session.EventRateLimitUpdated, session.RateLimitPayload{
		Available: false,
	}))
	if segs := sess.rateLimitSegs(time.Now()); len(segs) != 0 {
		t.Fatalf("stale windows survived an unavailable report: %v", segs)
	}
}

func TestRateLimitCountdownFormatting(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		reset time.Time
		want  string
	}{
		{"unknown reset prints no countdown", time.Time{}, "5h 50%"},
		{"minutes", now.Add(45 * time.Minute), "5h 50% ⟳46m"},
		{"hours", now.Add(3 * time.Hour), "5h 50% ⟳3h"},
		{"days", now.Add(50 * time.Hour), "5h 50% ⟳2d"},
		// A reset in the past means the next sample is imminent; "⟳-1h" would be
		// worse than nothing.
		{"elapsed reset prints no countdown", now.Add(-time.Hour), "5h 50%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rm := &sessionReadModel{
				RateLimitOK: true, FiveHourUtil: 50, FiveHourResetsAt: tt.reset,
			}
			got := rm.rateLimitSegs(now)[0]
			if got != tt.want {
				t.Errorf("seg = %q, want %q", got, tt.want)
			}
		})
	}
}

// A provider reporting an out-of-range utilization is their bug; it must not
// become a bug in our status bar.
func TestRateLimitClampsOutOfRangeUtilization(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		util float64
		want string
	}{
		{-5, "5h 0%"},
		{103, "5h 100%"},
		{49.6, "5h 50%"}, // rounds, not truncates
	} {
		rm := &sessionReadModel{RateLimitOK: true, FiveHourUtil: tt.util}
		if got := rm.rateLimitSegs(now)[0]; got != tt.want {
			t.Errorf("util %v → %q, want %q", tt.util, got, tt.want)
		}
	}
}

// A malformed reset instant must degrade to "no countdown", never to a parse
// error or a garbage date.
func TestRateLimitMalformedResetIsIgnored(t *testing.T) {
	sess := makeSession("s1", StatusBusy)
	ApplyRunnerEvent(&sess, mkEvent(session.EventRateLimitUpdated, session.RateLimitPayload{
		Available: true, FiveHourUtil: 10, FiveHourResetsAt: "not-a-timestamp",
	}))
	if !sess.FiveHourResetsAt.IsZero() {
		t.Errorf("malformed reset parsed to %v, want zero", sess.FiveHourResetsAt)
	}
	if got := sess.rateLimitSegs(time.Now())[0]; got != "5h 10%" {
		t.Errorf("seg = %q, want bare percentage", got)
	}
}

// End-to-end through the surface the user actually sees.
func TestPaneStatusRowShowsPlanWindows(t *testing.T) {
	sess := makeSession("s1", StatusBusy)
	sess.Model = "claude-opus-4-8"
	ApplyRunnerEvent(&sess, mkEvent(session.EventRateLimitUpdated, session.RateLimitPayload{
		Available: true, FiveHourUtil: 42, SevenDayUtil: 18,
	}))

	p := &ExternalPane{w: 200, label: "claude", sess: sess}
	row := p.statusRow()
	for _, want := range []string{"5h 42%", "wk 18%"} {
		if !strings.Contains(row, want) {
			t.Errorf("status row missing %q:\n%s", want, row)
		}
	}
}

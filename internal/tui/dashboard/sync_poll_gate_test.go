package dashboard

import (
	"sort"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func idSet(ids ...session.ID) map[session.ID]bool {
	m := make(map[session.ID]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func sortedIDs(ids []session.ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	sort.Strings(out)
	return out
}

// The first poll tick (empty lastProbe) sweeps every warm session once so each
// gets an initial reading; it also stamps lastProbe for all of them.
func TestSyncProbeTargetsFirstTickSweepsAll(t *testing.T) {
	now := time.Unix(1000, 0)
	warm := []session.ID{"a", "b", "c"}
	last := map[session.ID]time.Time{}

	got := selectSyncProbeTargets(now, warm, idSet("a"), last, 30*time.Second)

	if want := []string{"a", "b", "c"}; !eqStrings(sortedIDs(got), want) {
		t.Fatalf("first tick probed %v, want all %v", sortedIDs(got), want)
	}
	for _, id := range warm {
		if last[id] != now {
			t.Fatalf("lastProbe[%s] = %v, want %v", id, last[id], now)
		}
	}
}

// On a follow-up tick within the backoff window, only the focused session(s) are
// re-probed; the unfocused warm sessions are throttled.
func TestSyncProbeTargetsThrottlesUnfocused(t *testing.T) {
	base := time.Unix(1000, 0)
	warm := []session.ID{"a", "b", "c"}
	last := map[session.ID]time.Time{"a": base, "b": base, "c": base}

	// 4s later (well within a 30s backoff): only the focused "b" should probe.
	got := selectSyncProbeTargets(base.Add(4*time.Second), warm, idSet("b"), last, 30*time.Second)

	if want := []string{"b"}; !eqStrings(sortedIDs(got), want) {
		t.Fatalf("throttled tick probed %v, want only focused %v", sortedIDs(got), want)
	}
	// The focused session's clock advanced; the throttled ones did not.
	if last["b"] != base.Add(4*time.Second) {
		t.Fatalf("focused lastProbe not advanced: %v", last["b"])
	}
	if last["a"] != base || last["c"] != base {
		t.Fatalf("throttled sessions' clocks moved: a=%v c=%v", last["a"], last["c"])
	}
}

// Focus can cover more than one session at once (the selected list row AND the
// attached transcript); both stay fresh every tick.
func TestSyncProbeTargetsFocusSetProbesAll(t *testing.T) {
	base := time.Unix(1000, 0)
	warm := []session.ID{"a", "b", "c"}
	last := map[session.ID]time.Time{"a": base, "b": base, "c": base}

	got := selectSyncProbeTargets(base.Add(4*time.Second), warm, idSet("a", "c"), last, 30*time.Second)

	if want := []string{"a", "c"}; !eqStrings(sortedIDs(got), want) {
		t.Fatalf("probed %v, want focused set %v", sortedIDs(got), want)
	}
}

// Once the backoff elapses, an unfocused session is probed again (so its status
// still refreshes, just at a slower cadence).
func TestSyncProbeTargetsRefreshesAfterBackoff(t *testing.T) {
	base := time.Unix(1000, 0)
	warm := []session.ID{"a", "b"}
	last := map[session.ID]time.Time{"a": base, "b": base}

	// 30s later, backoff reached for the unfocused "a".
	got := selectSyncProbeTargets(base.Add(30*time.Second), warm, idSet("b"), last, 30*time.Second)

	if want := []string{"a", "b"}; !eqStrings(sortedIDs(got), want) {
		t.Fatalf("post-backoff tick probed %v, want %v", sortedIDs(got), want)
	}
}

// lastProbe is pruned of sessions that are no longer warm so it can't grow
// without bound across the dashboard's lifetime.
func TestSyncProbeTargetsPrunesDeadSessions(t *testing.T) {
	now := time.Unix(2000, 0)
	warm := []session.ID{"a"}
	last := map[session.ID]time.Time{"a": now.Add(-time.Minute), "gone": now.Add(-time.Hour)}

	_ = selectSyncProbeTargets(now, warm, idSet("a"), last, 30*time.Second)

	if _, ok := last["gone"]; ok {
		t.Fatalf("lastProbe kept a stale (no-longer-warm) session: %v", last)
	}
	if _, ok := last["a"]; !ok {
		t.Fatalf("lastProbe dropped a live warm session")
	}
}

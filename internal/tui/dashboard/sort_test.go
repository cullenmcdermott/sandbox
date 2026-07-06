package dashboard

import (
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// ids returns the session IDs in current slice order, for order assertions.
func ids(sessions []Session) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = string(s.ID())
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// REGRESSION (audit 2026-07-04, sort.go:116): the old descending comparator was
// `!less`, so for two equal-key rows BOTH `less(i,j)` and `less(j,i)` returned
// true. sort.SliceStable then treated each as strictly-before the other and
// swapped them on EVERY re-sort — the rows visibly ping-ponged (and the
// row-indexed cursor retargeted actions) each time SortSessions ran, which is
// once per cluster/runner event. A valid comparator must be idempotent:
// re-sorting an already-sorted slice must be a no-op in every key/dir.
func TestSortStableUnderRepeatedResort(t *testing.T) {
	// All three rows share the same title AND the same activity time, so the
	// primary key is a tie for every SortKey — this is exactly the case the old
	// `!less` mishandled. Only the ID tie-break gives a total order.
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	mk := func(id string) Session {
		return Session{
			Title:            "same",
			State:            session.State{ID: session.ID(id), LastActivity: base},
			sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
		}
	}

	for _, key := range []SortKey{SortByLastActive, SortByTitle, SortByStatus} {
		for _, dir := range []SortDir{SortAsc, SortDesc} {
			sessions := []Session{mk("c"), mk("a"), mk("b")}
			SortSessions(sessions, key, dir)
			first := ids(sessions)

			// Re-sort several times; the order must never change (idempotence).
			for i := 0; i < 5; i++ {
				SortSessions(sessions, key, dir)
				if got := ids(sessions); !eqStrings(got, first) {
					t.Fatalf("key=%v dir=%v: order changed on re-sort %d: %v -> %v (ping-pong)", key, dir, i, first, got)
				}
			}
			// The tie-break is by ID in a FIXED ascending direction regardless of dir.
			if want := []string{"a", "b", "c"}; !eqStrings(first, want) {
				t.Fatalf("key=%v dir=%v: equal-key tie-break = %v, want %v (ID ascending, dir-independent)", key, dir, first, want)
			}
		}
	}
}

// REGRESSION (sort.go:101): SortByTitle must order by the RENDERED title
// (DisplayTitle — RenamedTitle > AutoTitle > Title), not the raw derived Title,
// and it must flip correctly for descending.
func TestSortByTitleUsesDisplayTitle(t *testing.T) {
	// Raw Title order is identical ("proj"); DisplayTitle differs via the
	// rename/auto-title overrides, and that is what the row shows and sorts by.
	sessions := []Session{
		{Title: "proj", RenamedTitle: "zebra", State: session.State{ID: "s1"}},
		{Title: "proj", AutoTitle: "apple", State: session.State{ID: "s2"}},
		{Title: "proj", RenamedTitle: "mango", State: session.State{ID: "s3"}},
	}

	SortSessions(sessions, SortByTitle, SortAsc)
	if got, want := ids(sessions), []string{"s2", "s3", "s1"}; !eqStrings(got, want) {
		t.Fatalf("ascending by DisplayTitle = %v (%q,%q,%q), want %v (apple,mango,zebra)",
			got, sessions[0].DisplayTitle(), sessions[1].DisplayTitle(), sessions[2].DisplayTitle(), want)
	}

	SortSessions(sessions, SortByTitle, SortDesc)
	if got, want := ids(sessions), []string{"s1", "s3", "s2"}; !eqStrings(got, want) {
		t.Fatalf("descending by DisplayTitle = %v, want %v (zebra,mango,apple)", got, want)
	}
}

// SortByLastActive: ascending is oldest-first, descending is newest-first, with
// an ID tie-break for equal timestamps.
func TestSortByLastActiveDirection(t *testing.T) {
	t0 := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
	sessions := []Session{
		{State: session.State{ID: "old", LastActivity: t0}},
		{State: session.State{ID: "new", LastActivity: t0.Add(time.Hour)}},
		{State: session.State{ID: "mid", LastActivity: t0.Add(30 * time.Minute)}},
	}

	SortSessions(sessions, SortByLastActive, SortAsc)
	if got, want := ids(sessions), []string{"old", "mid", "new"}; !eqStrings(got, want) {
		t.Fatalf("ascending last-active = %v, want %v (oldest first)", got, want)
	}

	SortSessions(sessions, SortByLastActive, SortDesc)
	if got, want := ids(sessions), []string{"new", "mid", "old"}; !eqStrings(got, want) {
		t.Fatalf("descending last-active = %v, want %v (newest first)", got, want)
	}
}

package dashboard

import (
	"sort"
	"strings"
)

// SortKey is an enum for the sort dimension that cycles with the `s` key.
type SortKey int

const (
	SortByLastActive SortKey = iota // most-recently-active first (default)
	SortByTitle                     // alphabetical by display title
	SortByStatus                    // status severity order
	sortKeyCount                    // sentinel — must stay last
)

// String returns the human-readable label shown in the header.
func (k SortKey) String() string {
	switch k {
	case SortByLastActive:
		return "last-active"
	case SortByTitle:
		return "title"
	case SortByStatus:
		return "status"
	default:
		return "?"
	}
}

// Next returns the next sort key in the cycle.
func (k SortKey) Next() SortKey {
	return (k + 1) % sortKeyCount
}

// SortDir is the sort direction.
type SortDir int

const (
	SortAsc  SortDir = iota // ascending / oldest first / A→Z / lowest severity
	SortDesc                // descending / newest first / Z→A / highest severity
)

// Arrow returns the arrow glyph for the header.
func (d SortDir) Arrow() string {
	if d == SortAsc {
		return "↑"
	}
	return "↓"
}

// Flip toggles the direction.
func (d SortDir) Flip() SortDir {
	if d == SortAsc {
		return SortDesc
	}
	return SortAsc
}

// statusOrder maps a SessionStatus to a numeric severity (lower = less
// urgent). Used for SortByStatus.
func statusOrder(s SessionStatus) int {
	switch s {
	case StatusWaiting:
		return 0 // most urgent
	case StatusBusy:
		return 1
	case StatusNeedsInput:
		return 2
	case StatusFailed:
		return 3
	case StatusIdle:
		return 4
	case StatusSuspended:
		return 5
	default:
		return 6
	}
}

// SortSessions sorts a slice of Sessions in-place according to the given key
// and direction. It is stable (preserving relative order of equal elements)
// and pure (no external state).
func SortSessions(sessions []Session, key SortKey, dir SortDir) {
	sort.SliceStable(sessions, func(i, j int) bool {
		a, b := sessions[i], sessions[j]
		// Three-way primary comparison: <0 if a sorts before b, 0 if equal, >0 after.
		var c int
		switch key {
		case SortByLastActive:
			// Natural ascending order: earlier activity first. SortDesc flips this
			// below so the newest activity floats to the top.
			c = a.State.LastActivity.Compare(b.State.LastActivity)
		case SortByTitle:
			// Compare the rendered title (DisplayTitle), not the raw derived Title —
			// a rename / auto-title is what the row actually shows and sorts by.
			c = strings.Compare(strings.ToLower(a.DisplayTitle()), strings.ToLower(b.DisplayTitle()))
		case SortByStatus:
			c = statusOrder(a.DashStatus) - statusOrder(b.DashStatus)
		}
		// Flip the primary key for descending order (three-way cmp makes this exact:
		// equal keys stay 0, so they never falsely compare "less" in both directions).
		if dir == SortDesc {
			c = -c
		}
		if c != 0 {
			return c < 0
		}
		// Equal primary key: tie-break by ID in a FIXED (ascending) direction,
		// independent of dir. This makes the order total and stable — equal-key rows
		// keep a deterministic position and never ping-pong when SortSessions re-runs
		// on each cluster/runner event (the old `!less` returned true both ways for
		// equal keys, so sort.SliceStable swapped them on every re-sort).
		return string(a.ID()) < string(b.ID())
	})
}

// fuzzyMatch reports whether the needle is a fuzzy prefix-subsequence of
// haystack. It scores exact prefix matches highest, then subsequence matches.
// Returns (matched, score) where score > 0 means match. Higher score is better.
//
// This is a simple inline implementation that avoids pulling in an external
// package (sahilm/fuzzy) that isn't cached locally.
func fuzzyMatch(haystack, needle string) (bool, int) {
	if needle == "" {
		return true, 1
	}
	h := strings.ToLower(haystack)
	n := strings.ToLower(needle)

	// Exact substring match scores highest.
	if idx := strings.Index(h, n); idx == 0 {
		return true, 100
	}
	if strings.Contains(h, n) {
		return true, 50
	}

	// Subsequence match: each character of needle must appear in order.
	hi := 0
	for _, c := range n {
		found := false
		for ; hi < len(h); hi++ {
			if rune(h[hi]) == c {
				hi++
				found = true
				break
			}
		}
		if !found {
			return false, 0
		}
	}
	return true, 10
}

// FilterSessions returns the sessions that fuzzy-match the given query
// against Title + ProjectPath + Backend, preserving the input sort order.
// An empty query returns all sessions.
func FilterSessions(sessions []Session, query string) []Session {
	if query == "" {
		return sessions
	}
	out := make([]Session, 0, len(sessions))
	for _, s := range sessions {
		combined := s.Title + " " + s.State.ProjectPath + " " + s.State.Backend
		if ok, _ := fuzzyMatch(combined, query); ok {
			out = append(out, s)
		}
	}
	return out
}

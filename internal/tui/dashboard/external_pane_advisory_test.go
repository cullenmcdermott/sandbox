package dashboard

import (
	"strings"
	"sync"
	"testing"
)

// The advisory surface exists because for a while there was none. Connect and
// the background sync phase both computed warnings, ConnectResult carried one,
// attachReadyMsg carried one — and handleAttachReady read neither, because the
// transcript info block that used to render them went away with the chat TUI.
// A stalled file sync reached the user through no channel at all.
//
// These tests pin the pane end of that: whatever the connector reports has to
// come out on the one surface the user is actually looking at.

func advisoryPane(t *testing.T, seed string, live func() string) *ExternalPane {
	t.Helper()
	p := NewExternalPaneTransport(Session{}, "claude", nil, nil)
	p.w, p.h = 200, 40
	p.seedAdvisory = seed
	p.advisory = live
	return p
}

func TestStatusRowShowsConnectTimeAdvisory(t *testing.T) {
	p := advisoryPane(t, "file sync unavailable: no ssh key", nil)
	if row := p.statusRow(); !strings.Contains(row, "file sync unavailable") {
		t.Errorf("status row dropped the connect-time advisory: %q", row)
	}
}

// The live advisory is the one that carries [R5]/[R7]: the project sync failing
// in the background, and the reconnect flush classifying a stalled transport
// after the sync task has already settled. Neither is known at connect time, so
// a one-shot seed cannot express them.
func TestStatusRowShowsLateAdvisory(t *testing.T) {
	p := advisoryPane(t, "", func() string {
		return "file sync is stalled after reconnect (boom); edits may not be propagating"
	})
	if row := p.statusRow(); !strings.Contains(row, "stalled after reconnect") {
		t.Errorf("status row dropped the late advisory: %q", row)
	}
}

// A late advisory must be picked up by a pane already rendering — the whole
// point is that it arrives after the attach.
func TestStatusRowPicksUpAnAdvisoryThatArrivesLater(t *testing.T) {
	var mu sync.Mutex
	current := ""
	p := advisoryPane(t, "", func() string {
		mu.Lock()
		defer mu.Unlock()
		return current
	})

	if row := p.statusRow(); strings.Contains(row, "⚠") {
		t.Fatalf("clean session rendered a warning marker: %q", row)
	}
	mu.Lock()
	current = "file sync is stalled after reconnect"
	mu.Unlock()
	if row := p.statusRow(); !strings.Contains(row, "stalled after reconnect") {
		t.Errorf("a later advisory never reached the status row: %q", row)
	}
}

// SyncAdvisory joins everything the task accumulated, so it is a superset of the
// seed. Rendering both would print the seed twice.
func TestLiveAdvisorySupersedesTheSeed(t *testing.T) {
	p := advisoryPane(t, "file sync unavailable", func() string {
		return "file sync unavailable; file sync is stalled after reconnect"
	})
	row := p.statusRow()
	if n := strings.Count(row, "file sync unavailable"); n != 1 {
		t.Errorf("advisory rendered %d times, want 1: %q", n, row)
	}
}

// A clean connect must not spend a single column on an empty advisory.
func TestStatusRowHasNoAdvisorySegmentWhenClean(t *testing.T) {
	for name, p := range map[string]*ExternalPane{
		"no sources":    advisoryPane(t, "", nil),
		"empty live":    advisoryPane(t, "", func() string { return "" }),
		"live is nil":   advisoryPane(t, "", nil),
		"blank strings": advisoryPane(t, "", func() string { return "" }),
	} {
		if row := p.statusRow(); strings.Contains(row, "⚠") {
			t.Errorf("%s: rendered a warning marker with nothing to warn about: %q", name, row)
		}
	}
}

// The status row is ONE reserved line. A newline in an advisory would push the
// pane body up by a row and desync the emulator geometry from what is drawn.
func TestAdvisoryIsCollapsedToOneLine(t *testing.T) {
	p := advisoryPane(t, "file sync unavailable:\nmutagen: connection refused\n", nil)
	if got := p.paneAdvisory(); strings.ContainsAny(got, "\n\r") {
		t.Errorf("advisory kept a line break: %q", got)
	}
	if row := p.statusRow(); strings.Contains(row, "\n") {
		t.Errorf("status row spans more than one line: %q", row)
	}
}

// A long mutagen error must not crowd out the title and metrics entirely.
func TestAdvisoryIsCappedInWidth(t *testing.T) {
	long := strings.Repeat("mutagen refused the connection ", 20)
	p := advisoryPane(t, long, nil)
	p.sess = Session{}
	row := p.statusRow()
	if len(row) == 0 {
		t.Fatal("empty status row")
	}
	// The cap is on the advisory segment itself, before spread's own truncation.
	if got := p.paneAdvisory(); len(got) <= maxAdvisoryWidth {
		t.Fatalf("test needs an advisory longer than the cap, got %d", len(got))
	}
	if strings.Count(row, "mutagen refused the connection") > 3 {
		t.Errorf("advisory was not capped; it filled the bar: %q", row)
	}
}

// Truncation order is the property that matters on a narrow terminal: spread
// truncates the left segment from the right, so an advisory placed after the
// title would be the FIRST thing dropped. It goes first precisely so it is the
// last thing to go.
func TestAdvisorySurvivesANarrowTerminal(t *testing.T) {
	p := advisoryPane(t, "file sync stalled", nil)
	p.sess = Session{}
	p.w = 40
	if row := p.statusRow(); !strings.Contains(row, "sync stalled") {
		t.Errorf("advisory was truncated away on a narrow pane: %q", row)
	}
}

package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// detailWithSync renders the detail pane for one session carrying the given
// sync reading, joined into a single searchable string.
func detailWithSync(t *testing.T, status, detail string) string {
	t.Helper()
	m := New(nil)
	m.sessions = []Session{{
		State:            session.State{ID: "s1", Status: session.StatusRunning},
		sessionReadModel: sessionReadModel{DashStatus: StatusIdle},
		SyncStatus:       status,
		SyncDetail:       detail,
	}}
	return strings.Join(m.renderDetailLines(80, 24), "\n")
}

// "unknown" is the reading a user sees when their files have silently stopped
// moving, and it used to render as a bare word with no glyph and no reason —
// nothing to act on. The reason the prober now supplies must reach the pane.
func TestDetailPaneShowsSyncUnknownReason(t *testing.T) {
	out := detailWithSync(t, "unknown", "mutagen CLI not found")

	if !strings.Contains(out, "unknown") {
		t.Fatalf("detail pane dropped the sync status:\n%s", out)
	}
	if !strings.Contains(out, "mutagen CLI not found") {
		t.Errorf("detail pane dropped the sync reason:\n%s", out)
	}
	// Anchored to the sync row itself: a bare Contains(out, "?") would pass on a
	// stray question mark anywhere in the pane.
	if !strings.Contains(out, "? unknown") {
		t.Errorf("an unknown sync should carry a glyph like every other state:\n%s", out)
	}
}

// The counter: a healthy reading carries no reason, so the parenthetical must
// not appear for it — the detail is a diagnosis, not permanent chrome.
func TestDetailPaneSyncedHasNoReason(t *testing.T) {
	out := detailWithSync(t, "synced", "")

	if !strings.Contains(out, "synced") {
		t.Fatalf("detail pane dropped the sync status:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "synced") && strings.Contains(line, "(") {
			t.Errorf("healthy sync row should carry no parenthetical reason: %q", line)
		}
	}
}

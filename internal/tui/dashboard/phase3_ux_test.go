package dashboard

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Phase 3 (UX polish): a stalled file sync must be visible in the attached chat
// status line, not just the dashboard detail pane. The segment is gated on a
// non-empty status so the default (unprobed) status line stays byte-identical.
func TestStatusLineSyncSegment(t *testing.T) {
	m := &TranscriptModel{}

	// Empty (unprobed) status: no sync glyph at all.
	m.syncStatus = ""
	base := m.renderStatusLine()
	if strings.ContainsAny(stripANSI(base), "✓⟳⚠") {
		t.Fatalf("empty sync status must not render a glyph, got %q", stripANSI(base))
	}

	// Stalled: the warn glyph + label appear.
	m.syncStatus = "stalled"
	if out := stripANSI(m.renderStatusLine()); !strings.Contains(out, "⚠ stalled") {
		t.Fatalf("stalled sync must show the marker, got %q", out)
	}

	// A healthy status still adds a segment, so the row differs from the baseline.
	m.syncStatus = "synced"
	if got := stripANSI(m.renderStatusLine()); !strings.Contains(got, "✓ synced") {
		t.Fatalf("synced sync must show the marker, got %q", got)
	}
	if m.renderStatusLine() == base {
		t.Fatal("a non-empty sync status must add a sync segment")
	}
}

// The dashboard polls warm-session sync health; the foreground session is one of
// them. App must mirror that freshly-probed status into the attached transcript
// after each dashboard delegation so the status line can render it (no new probe).
func TestAppSyncStatusPropagatesToTranscript(t *testing.T) {
	a := &App{
		screen:     ScreenTranscript,
		dashboard:  New(nil),
		transcript: &TranscriptModel{ref: session.Ref{ID: "s1"}},
	}
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.sessions = []Session{
		{State: session.State{ID: "s1", Status: session.StatusRunning}},
	}

	// A freshly-probed sync status for the attached session must reach the
	// transcript model (the same path the dashboard uses to update its rows).
	a.Update(syncStatusMsg{id: "s1", status: "stalled"})
	if a.transcript.syncStatus != "stalled" {
		t.Fatalf("expected sync status to propagate to the transcript, got %q", a.transcript.syncStatus)
	}

	// A background (non-attached) session's status must NOT leak into the
	// foreground transcript.
	a.dashboard.sessions = append(a.dashboard.sessions,
		Session{State: session.State{ID: "s2", Status: session.StatusRunning}})
	a.Update(syncStatusMsg{id: "s2", status: "synced"})
	if a.transcript.syncStatus != "stalled" {
		t.Fatalf("a background session's sync status must not overwrite the foreground one, got %q", a.transcript.syncStatus)
	}
}

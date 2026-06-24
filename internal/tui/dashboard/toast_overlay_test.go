package dashboard

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestToastCompositesOverModalScreen is the T3 regression: a cross-session
// "needs you" toast must float over every screen, not only the bare dashboard.
// Before the fix renderToast was composited only inside renderZoned, so the
// toast was invisible behind the chat modal / connecting splash — exactly when a
// background session needs attention. withToast now overlays it at the App level.
func TestToastCompositesOverModalScreen(t *testing.T) {
	a := &App{screen: ScreenTranscript, dashboard: New(nil), width: 80, height: 24}
	a.dashboard.width, a.dashboard.height = 80, 24
	a.dashboard.toast = &notification{
		title:     "BgSess",
		note:      "claude · proj",
		status:    StatusWaiting,
		createdAt: time.Now(),
	}

	// A synthetic 24×80 frame standing in for the chat-modal screen content.
	var rows []string
	for i := 0; i < 24; i++ {
		rows = append(rows, strings.Repeat("x", 80))
	}
	base := tea.NewView(strings.Join(rows, "\n"))

	out := a.withToast(base).Content
	if !strings.Contains(out, "BgSess") {
		t.Fatal("toast title must appear when composited over a non-dashboard screen")
	}
	if !strings.Contains(out, "needs you") {
		t.Error("toast body must appear over the modal screen")
	}
}

// TestWithToastNoOpWithoutToast keeps the no-toast path byte-identical so the
// OSC-signal frame assertions (and every non-toast frame) are unaffected.
func TestWithToastNoOpWithoutToast(t *testing.T) {
	a := &App{screen: ScreenTranscript, dashboard: New(nil), width: 80, height: 24}
	base := tea.NewView("hello")
	if got := a.withToast(base).Content; got != "hello" {
		t.Fatalf("withToast with no toast changed content: %q", got)
	}
}

package dashboard

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Phase 3 item 3 (opencode wheel-scroll + clickable spots): the embedded opencode
// TUI enables mouse tracking itself, so once the host reports mouse events the
// pane must forward a WHEEL as an SGR mouse report — letting opencode's own
// scroll handle it — rather than the wheel being dropped or (with host mouse
// capture off) mapped to arrow keys that hijack opencode's prompt history.
func TestExternalPaneForwardsWheelAsSgr(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	p := &ExternalPane{ptmx: w, activeModes: map[ansi.DECMode]bool{
		ansi.ModeMouseNormal: true,
		ansi.ModeMouseExtSgr: true,
	}}
	p.handleMouse(tea.MouseWheelMsg{X: 5, Y: 10, Button: tea.MouseWheelUp})

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	// SGR wheel-up at (5,10): ESC[<64;6;11M — button 64 is the xterm wheel-up code,
	// coords are 1-based, trailing M is a press (wheel has no release).
	want := "\x1b[<64;6;11M"
	if got != want {
		t.Fatalf("wheel not forwarded as SGR wheel report:\n got=%q\nwant=%q", got, want)
	}
	// Distinct from a left-click (button 0) — proves the wheel is not collapsed into
	// a click, and definitely not arrow-key history navigation.
	if strings.HasPrefix(got, "\x1b[<0;") {
		t.Errorf("wheel encoded as a left-click, not a wheel button: %q", got)
	}
}

// Phase 3 item 3: View() must enable cell-motion mouse capture on ScreenExternal
// (it previously skipped it there). Without host mouse capture, the host terminal
// translates the wheel into arrow keys that fall through to opencode as Up/Down —
// the prompt-history hijack. Capturing here routes wheel/click to handleMouse,
// which re-encodes them as SGR mouse for opencode's native handling.
func TestExternalScreenEnablesMouseCapture(t *testing.T) {
	app := NewApp(nil, nil, nil)
	// Size the app + dashboard so View() renders (external pane is nil here; the
	// MouseMode decision in View() keys off a.screen, not the pane).
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	app.screen = ScreenExternal
	if got := app.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("ScreenExternal must enable MouseModeCellMotion (host reports wheel/click to the app); got %v", got)
	}

	// Counter: a non-external screen still enables capture too (unchanged) — so the
	// fix widened coverage rather than moving it.
	app.screen = ScreenDashboard
	if got := app.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("dashboard screen lost MouseModeCellMotion; got %v", got)
	}
}

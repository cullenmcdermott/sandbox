package dashboard

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// TestExternalPaneCloseReapsChild is the O1 regression guard: close() must reap
// the killed child (Wait) so it does not linger as a <defunct> zombie. close()
// calls Wait() synchronously, so once it returns the child is reaped and
// cmd.ProcessState is set (read here only after close() returns — race-free).
func TestExternalPaneCloseReapsChild(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		// Some sandboxes forbid PTY allocation ("operation not permitted").
		// That is an environment limitation, not a code failure — skip there;
		// CI and normal dev allocate a PTY and run this fully.
		if errors.Is(err, syscall.EPERM) || strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("PTY allocation not permitted in this environment: %v", err) // gate-ok: conditional on EPERM, a real env limit not a dodged test
		}
		t.Fatalf("pty.Start: %v", err)
	}
	p := &ExternalPane{cmd: cmd, ptmx: ptmx}

	p.close()

	if cmd.ProcessState == nil {
		t.Fatal("child not reaped after close(): ProcessState is nil (would linger as a <defunct> zombie until program exit)")
	}
	if cmd.ProcessState.ExitCode() == 0 {
		t.Errorf("killed child should report a non-zero/ signal exit, got exit code 0")
	}
	if !p.exited {
		t.Error("close() should mark the pane exited")
	}
}

func TestSafeWriteBoundary(t *testing.T) {
	flag := "\U0001F1FA\U0001F1F8" // 🇺🇸 — two regional indicators, one cluster
	cases := []struct {
		name string
		in   string
		want int // bytes safe to write now
	}{
		{"empty", "", 0},
		{"ascii", "abc", 3},
		{"ascii-ends-escape", "abc\x1b[H", len("abc\x1b[H")}, // escape never held
		{"trailing-nonascii-grapheme", "x\U0001F1FA", 1},     // hold the RI
		{"two-clusters-hold-last", "a" + flag, 1},            // 'a' safe, flag held
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := safeWriteBoundary([]byte(c.in)); got != c.want {
				t.Fatalf("safeWriteBoundary(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// REGRESSION (O7): a grapheme cluster split across two PTY reads must not render
// as two cells. Feeding the bytes in two chunks (split between the flag's two
// regional indicators) must produce the identical screen to a single write.
func TestExternalPaneFeedNoGraphemeSplit(t *testing.T) {
	// 🇺🇸 (U+1F1FA U+1F1F8, 4 bytes each) then 'X' to force the held cluster out.
	full := []byte("\U0001F1FA\U0001F1F8X")
	split := 4 // between the two regional indicators

	ref := vt.NewEmulator(10, 1)
	if _, err := ref.Write(full); err != nil {
		t.Fatalf("ref write: %v", err)
	}

	p := &ExternalPane{emu: vt.NewEmulator(10, 1)}
	p.feed(full[:split])
	p.feed(full[split:])

	if got, want := p.emu.Render(), ref.Render(); got != want {
		t.Fatalf("grapheme split across feed boundary:\n got=%q\nwant=%q", got, want)
	}
}

// REGRESSION (O7): a single multi-byte codepoint split mid-bytes across two
// reads must still render correctly (proven by parity with a single write).
func TestExternalPaneFeedSplitCodepoint(t *testing.T) {
	full := []byte("中X") // 中 (E4 B8 AD) then 'X'
	ref := vt.NewEmulator(10, 1)
	if _, err := ref.Write(full); err != nil {
		t.Fatalf("ref write: %v", err)
	}
	p := &ExternalPane{emu: vt.NewEmulator(10, 1)}
	p.feed(full[:2]) // E4 B8 — incomplete 中
	p.feed(full[2:]) // AD X — completes 中, then X
	if got, want := p.emu.Render(), ref.Render(); got != want {
		t.Fatalf("split codepoint:\n got=%q\nwant=%q", got, want)
	}
}

// EOF flush: a stream that ends mid-grapheme still writes the held bytes so they
// aren't silently dropped.
func TestExternalPaneFeedFlushesCarryOnEOF(t *testing.T) {
	full := []byte("A\U0001F1FA\U0001F1F8") // 'A' then a flag, ends non-ASCII
	ref := vt.NewEmulator(10, 1)
	if _, err := ref.Write(full); err != nil {
		t.Fatalf("ref write: %v", err)
	}

	p := &ExternalPane{emu: vt.NewEmulator(10, 1)}
	p.feed(full) // ends in a non-ASCII grapheme → flag is held in carry
	if len(p.carry) == 0 {
		t.Fatal("expected a held trailing grapheme")
	}
	// Simulate stream end (EOF flush path in apply).
	_, _ = p.emu.Write(p.carry)
	p.carry = nil

	if got, want := p.emu.Render(), ref.Render(); got != want {
		t.Fatalf("carry not flushed on EOF:\n got=%q\nwant=%q", got, want)
	}
}

// REGRESSION (O6): PasteMsg is forwarded to the child PTY wrapped in bracketed-
// paste sequences when the child has enabled the mode.
func TestExternalPaneHandlePasteWhenEnabled(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	p := &ExternalPane{ptmx: w, activeModes: map[ansi.DECMode]bool{ansi.ModeBracketedPaste: true}}
	p.handlePaste(tea.PasteMsg{Content: "hello world"})

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	want := ansi.BracketedPasteStart + "hello world" + ansi.BracketedPasteEnd
	if got != want {
		t.Fatalf("handlePaste when enabled:\n got=%q\nwant=%q", got, want)
	}
}

// PasteMsg is silently ignored when the child has not enabled bracketed paste.
func TestExternalPaneHandlePasteIgnoredWhenDisabled(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	p := &ExternalPane{ptmx: w, activeModes: make(map[ansi.DECMode]bool)}
	p.handlePaste(tea.PasteMsg{Content: "hello world"})

	buf := make([]byte, 256)
	r.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err = r.Read(buf)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected no write when disabled, got err=%v data=%q", err, string(buf))
	}
}

// REGRESSION (O6): MouseMsg is forwarded as xterm SGR mouse when the child has
// enabled mouse reporting + SGR encoding.
func TestExternalPaneHandleMouseSgrWhenEnabled(t *testing.T) {
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
	p.handleMouse(tea.MouseClickMsg{X: 5, Y: 10, Button: tea.MouseLeft})

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	// SGR left-click at (5,10): ESC[<0;6;11M  (1-based coords)
	want := "\x1b[<0;6;11M"
	if got != want {
		t.Fatalf("handleMouse SGR click:\n got=%q\nwant=%q", got, want)
	}
}

// MouseMsg is silently ignored when the child has not enabled mouse reporting.
func TestExternalPaneHandleMouseIgnoredWhenDisabled(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	p := &ExternalPane{ptmx: w, activeModes: make(map[ansi.DECMode]bool)}
	p.handleMouse(tea.MouseClickMsg{X: 5, Y: 10, Button: tea.MouseLeft})

	buf := make([]byte, 256)
	r.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err = r.Read(buf)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected no write when disabled, got err=%v data=%q", err, string(buf))
	}
}

// The emulator's EnableMode/DisableMode callbacks correctly update the pane's
// activeModes map (verified by driving mode sequences through the emulator).
func TestExternalPaneModeCallbacksTrackState(t *testing.T) {
	p := &ExternalPane{emu: vt.NewEmulator(10, 1), activeModes: make(map[ansi.DECMode]bool)}
	p.emu.SetCallbacks(vt.Callbacks{
		EnableMode: func(mode ansi.Mode) {
			if dec, ok := mode.(ansi.DECMode); ok {
				p.activeModes[dec] = true
			}
		},
		DisableMode: func(mode ansi.Mode) {
			if dec, ok := mode.(ansi.DECMode); ok {
				delete(p.activeModes, dec)
			}
		},
	})

	// Enable bracketed paste and mouse SGR.
	_, _ = p.emu.WriteString("\x1b[?2004h\x1b[?1006h\x1b[?1002h")
	if !p.activeModes[ansi.ModeBracketedPaste] {
		t.Fatal("expected ModeBracketedPaste enabled")
	}
	if !p.activeModes[ansi.ModeMouseExtSgr] {
		t.Fatal("expected ModeMouseExtSgr enabled")
	}
	if !p.activeModes[ansi.ModeMouseButtonEvent] {
		t.Fatal("expected ModeMouseButtonEvent enabled")
	}

	// Disable bracketed paste.
	_, _ = p.emu.WriteString("\x1b[?2004l")
	if p.activeModes[ansi.ModeBracketedPaste] {
		t.Fatal("expected ModeBracketedPaste disabled")
	}
	// Other modes should still be active.
	if !p.activeModes[ansi.ModeMouseExtSgr] {
		t.Fatal("expected ModeMouseExtSgr still enabled")
	}
}

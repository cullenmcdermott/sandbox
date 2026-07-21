package dashboard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// fakePaneTransport is a PaneTransport over any ReadWriteCloser (an os.Pipe end
// in these tests), recording resizes. It stands in for both real transports so
// input-forwarding behavior is tested at the seam, not against a PTY. The
// mutex covers resizes: on an Init'd pane the input-writer goroutine calls
// Resize while the test goroutine inspects.
type fakePaneTransport struct {
	io.ReadWriteCloser
	mu      sync.Mutex
	resizes [][2]int
}

func (f *fakePaneTransport) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, [2]int{cols, rows})
	return nil
}

func (f *fakePaneTransport) resizesSnapshot() [][2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]int(nil), f.resizes...)
}

// TestOpencodeAttachCmd pins the argv/env contract of the local opencode
// client spawn: server URL positional, -u user, --continue (history), and the
// password via env — NEVER argv, so it stays out of the host process list.
func TestOpencodeAttachCmd(t *testing.T) {
	creds := OpencodeCreds{Username: "opencode", Password: "secret", URL: "http://127.0.0.1:5000"}
	cmd := opencodeAttachCmd(creds)
	wantArgs := []string{"opencode", "attach", "http://127.0.0.1:5000", "-u", "opencode", "--continue"}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", cmd.Args, wantArgs)
	}
	for i, a := range wantArgs {
		if cmd.Args[i] != a {
			t.Fatalf("args[%d] = %q, want %q (full: %v)", i, cmd.Args[i], a, cmd.Args)
		}
	}
	var foundPass bool
	for _, e := range cmd.Env {
		if e == "OPENCODE_SERVER_PASSWORD=secret" {
			foundPass = true
		}
	}
	if !foundPass {
		t.Error("password not passed via OPENCODE_SERVER_PASSWORD env")
	}
	for _, a := range cmd.Args {
		if strings.Contains(a, "secret") {
			t.Errorf("password leaked into argv: %v", cmd.Args)
		}
	}
}

// TestExternalPaneCloseReapsChild is the O1 regression guard: close() must reap
// the killed child (Wait) so it does not linger as a <defunct> zombie. The
// child-process transport's Close waits synchronously, so once close() returns
// the child is reaped and cmd.ProcessState is set (read here only after close()
// returns — race-free).
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
	p := &ExternalPane{transport: &childProcTransport{name: "test child", ptmx: ptmx, cmd: cmd}}

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

// TestChildProcTransportReadReportsExit: a Read that fails because the child
// exited must reap it and surface the exit status as the stream-end error (the
// reason shown when a pane child dies on startup), and a subsequent Close must
// not double-Wait.
func TestChildProcTransportReadReportsExit(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 3")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		if errors.Is(err, syscall.EPERM) || strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("PTY allocation not permitted in this environment: %v", err) // gate-ok: conditional on EPERM, a real env limit not a dodged test
		}
		t.Fatalf("pty.Start: %v", err)
	}
	tr := &childProcTransport{name: "test child", ptmx: ptmx, cmd: cmd}

	// Drain until the master read fails (child exit → EIO).
	buf := make([]byte, 1024)
	var rerr error
	for {
		_, rerr = tr.Read(buf)
		if rerr != nil {
			break
		}
	}
	if rerr == nil || !strings.Contains(rerr.Error(), "test child exited") {
		t.Fatalf("Read after child exit = %v, want a 'test child exited' error", rerr)
	}
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 3 {
		t.Fatalf("child not reaped with its real status: %v", cmd.ProcessState)
	}
	// Close after the exit-path Wait must be safe (waitOnce).
	if err := tr.Close(); err != nil {
		t.Fatalf("Close after exit: %v", err)
	}
}

// TestExternalPaneInitDialFailure: a failed dial marks the pane exited with the
// dial error and emits the finished message so the App falls back to the
// dashboard (the opencode-not-installed / pane-attach-refused path).
func TestExternalPaneInitDialFailure(t *testing.T) {
	wantErr := errors.New("dial refused")
	p := NewExternalPaneTransport(Session{}, "claude", func(cols, rows int) (PaneTransport, error) {
		return nil, wantErr
	}, nil)
	cmd := p.Init()
	if !p.exited || !errors.Is(p.err, wantErr) {
		t.Fatalf("after failed dial: exited=%v err=%v, want exited with the dial error", p.exited, p.err)
	}
	msg := cmd()
	fin, ok := msg.(externalPaneFinishedMsg)
	if !ok || !errors.Is(fin.err, wantErr) {
		t.Fatalf("Init cmd = %#v, want externalPaneFinishedMsg carrying the dial error", msg)
	}
}

// TestExternalPaneTransportRoundTrip drives Init + the reader goroutine over a
// fake transport (the WS-shaped path): dialed at emulator geometry, transport
// output reaches the emulator via apply, keys write back to the transport, and
// the stream-end error surfaces on the pane.
func TestExternalPaneTransportRoundTrip(t *testing.T) {
	// fromPane receives what the pane writes; toPane feeds the pane's reader.
	toPaneR, toPaneW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	fromPaneR, fromPaneW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer fromPaneR.Close()
	defer toPaneW.Close()

	tr := &fakePaneTransport{ReadWriteCloser: struct {
		io.Reader
		io.Writer
		io.Closer
	}{toPaneR, fromPaneW, toPaneR}}

	var dialedCols, dialedRows int
	p := NewExternalPaneTransport(Session{}, "claude", func(cols, rows int) (PaneTransport, error) {
		dialedCols, dialedRows = cols, rows
		return tr, nil
	}, nil)
	p.w, p.h = 40, 10
	readCmd := p.Init()
	if p.err != nil {
		t.Fatalf("Init: %v", p.err)
	}
	if dialedCols != 40 || dialedRows != 9 {
		t.Fatalf("dialed at %dx%d, want 40x9 (width × height-1 status row)", dialedCols, dialedRows)
	}

	// Transport output → reader goroutine → chunk → apply → emulator.
	if _, err := toPaneW.WriteString("hello"); err != nil {
		t.Fatalf("feed transport: %v", err)
	}
	msg := readCmd()
	out, ok := msg.(ptyOutputMsg)
	if !ok || !out.chunk.ok {
		t.Fatalf("readCmd = %#v, want a live ptyOutputMsg", msg)
	}
	next, finished := p.apply(out.chunk)
	if finished || next == nil {
		t.Fatal("apply(live chunk) should continue the drain")
	}
	if !strings.Contains(p.emu.Render(), "hello") {
		t.Errorf("emulator missing transport output: %q", p.emu.Render())
	}

	// Key press → transport write.
	p.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	buf := make([]byte, 8)
	n, err := fromPaneR.Read(buf)
	if err != nil || string(buf[:n]) != "x" {
		t.Fatalf("transport received %q (%v), want %q", buf[:n], err, "x")
	}

	// resize → emulator + transport. The transport half rides the input-writer
	// queue (P4), so poll for its arrival.
	p.resize(50, 12)
	deadline := time.Now().Add(5 * time.Second)
	for {
		rs := tr.resizesSnapshot()
		if len(rs) > 0 && rs[len(rs)-1] == [2]int{50, 11} {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transport resizes = %v, want trailing 50x11", rs)
		}
		time.Sleep(time.Millisecond)
	}

	// Stream end with a reason → pane exited with that error.
	toPaneW.Close()
	msg = next()
	out, ok = msg.(ptyOutputMsg)
	if !ok {
		t.Fatalf("readCmd after close = %#v", msg)
	}
	if _, finished := p.apply(out.chunk); !finished {
		t.Fatal("apply(end chunk) should finish the pane")
	}
	if p.err != nil {
		t.Fatalf("clean EOF should not set an error, got %v", p.err)
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

	p := &ExternalPane{transport: &fakePaneTransport{ReadWriteCloser: w}, activeModes: map[ansi.DECMode]bool{ansi.ModeBracketedPaste: true}}
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

	p := &ExternalPane{transport: &fakePaneTransport{ReadWriteCloser: w}, activeModes: make(map[ansi.DECMode]bool)}
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

	p := &ExternalPane{transport: &fakePaneTransport{ReadWriteCloser: w}, activeModes: map[ansi.DECMode]bool{
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

	p := &ExternalPane{transport: &fakePaneTransport{ReadWriteCloser: w}, activeModes: make(map[ansi.DECMode]bool)}
	p.handleMouse(tea.MouseClickMsg{X: 5, Y: 10, Button: tea.MouseLeft})

	buf := make([]byte, 256)
	r.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err = r.Read(buf)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected no write when disabled, got err=%v data=%q", err, string(buf))
	}
}

// scrolledPane builds a direct-construction pane (no transport) whose 3-row
// emulator has been fed 5 lines, so l1/l2 live in the scrollback and l3-l5 on
// the live screen — the seed for the L7 wheel-scroll tests.
func scrolledPane(t *testing.T) *ExternalPane {
	t.Helper()
	p := &ExternalPane{emu: vt.NewEmulator(80, 3), activeModes: make(map[ansi.DECMode]bool), w: 80, h: 4}
	if _, err := p.emu.WriteString("l1\r\nl2\r\nl3\r\nl4\r\nl5"); err != nil {
		t.Fatalf("seed emulator: %v", err)
	}
	if got := p.emu.ScrollbackLen(); got != 2 {
		t.Fatalf("seed scrollback len = %d, want 2", got)
	}
	return p
}

// L7: with mouse tracking OFF (claude runs inline and never enables it), a
// wheel-up scrolls the pane's own view over the vt scrollback instead of being
// dropped: the offset moves (clamped to history), scrollback lines render
// above the top of the live screen, and the status row shows the return hint.
func TestExternalPaneWheelScrollsBackWhenTrackingOff(t *testing.T) {
	p := scrolledPane(t)

	p.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp})

	if p.scrollOffset != 2 {
		t.Fatalf("scrollOffset = %d, want 2 (one wheel step clamped to the 2-line history)", p.scrollOffset)
	}
	content := p.View().Content
	for _, want := range []string{"l1", "l2", "l3"} {
		if !strings.Contains(content, want) {
			t.Fatalf("scrolled view missing %q (scrollback tail + top of live screen):\n%s", want, content)
		}
	}
	if strings.Contains(content, "l5") {
		t.Fatalf("bottom live row must be pushed out of a view scrolled by 2:\n%s", content)
	}
	if !strings.Contains(content, "any key to return") {
		t.Fatalf("status row missing the scrolled-back indicator:\n%s", content)
	}
}

// L7: the scroll offset clamps at the scrollback length going up and at zero
// going down — wheel events can never point the view past the history.
func TestExternalPaneScrollOffsetClamps(t *testing.T) {
	p := scrolledPane(t)

	p.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	p.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if p.scrollOffset != 2 {
		t.Fatalf("scrollOffset after two wheel-ups = %d, want clamp at history length 2", p.scrollOffset)
	}
	p.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset after wheel-down = %d, want clamp at 0", p.scrollOffset)
	}
	p.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset after wheel-down at live = %d, want 0", p.scrollOffset)
	}
}

// L7: a key press snaps a scrolled-back view to live AND is still forwarded to
// the child — returning to live must never eat the keystroke.
func TestExternalPaneKeySnapsBackAndForwards(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	p := scrolledPane(t)
	p.transport = &fakePaneTransport{ReadWriteCloser: w}
	p.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if p.scrollOffset == 0 {
		t.Fatal("precondition: pane should be scrolled back")
	}

	p.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})

	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset after key = %d, want 0 (snap back to live)", p.scrollOffset)
	}
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil || string(buf[:n]) != "x" {
		t.Fatalf("child received %q (%v), want %q — snap-back must not swallow the key", buf[:n], err, "x")
	}
}

// L7: while the child has mouse tracking enabled, the wheel is forwarded to it
// (SGR-encoded) exactly as before — the child scrolls itself; the local
// scrollback view stays live.
func TestExternalPaneWheelForwardedWhenTrackingOn(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	p := scrolledPane(t)
	p.transport = &fakePaneTransport{ReadWriteCloser: w}
	p.activeModes[ansi.ModeMouseNormal] = true
	p.activeModes[ansi.ModeMouseExtSgr] = true

	p.handleMouse(tea.MouseWheelMsg{X: 5, Y: 10, Button: tea.MouseWheelUp})

	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset = %d, want 0 (tracking child owns the wheel)", p.scrollOffset)
	}
	buf := make([]byte, 32)
	n, _ := r.Read(buf)
	// SGR wheel-up at (5,10): ESC[<64;6;11M (1-based coords).
	if got, want := string(buf[:n]), "\x1b[<64;6;11M"; got != want {
		t.Fatalf("forwarded wheel = %q, want %q", got, want)
	}
}

// L7: new child output snaps a scrolled-back view to live before the bytes
// feed the emulator, so the freshly painted frame is what renders.
func TestExternalPaneOutputSnapsBack(t *testing.T) {
	p := scrolledPane(t)
	p.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if p.scrollOffset == 0 {
		t.Fatal("precondition: pane should be scrolled back")
	}

	cmd, finished := p.apply(ptyChunk{data: []byte("l6"), ok: true})
	if finished || cmd == nil {
		t.Fatal("apply(live chunk) should continue the drain")
	}
	if p.scrollOffset != 0 {
		t.Fatalf("scrollOffset after child output = %d, want 0 (snap back to live)", p.scrollOffset)
	}
	if !strings.Contains(p.View().Content, "l6") {
		t.Fatalf("view after output should be live and contain the new bytes:\n%s", p.View().Content)
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

// PERF (P1): a burst of chunks already buffered in p.out is consumed by a
// SINGLE apply call — one Update+View for the whole burst instead of one full
// render per 32 KB transport read. The coalesced bytes must go through the
// same grapheme-safe boundary handling as the per-chunk path, so a cluster
// split across two queued chunks still renders as one cell.
func TestExternalPaneApplyCoalescesBurst(t *testing.T) {
	flag := "\U0001F1FA\U0001F1F8" // 🇺🇸 — one cluster, split across two chunks below
	full := []byte("hello " + flag + " world")

	ref := vt.NewEmulator(40, 1)
	if _, err := ref.Write(full); err != nil {
		t.Fatalf("ref write: %v", err)
	}

	p := &ExternalPane{emu: vt.NewEmulator(40, 1), out: make(chan ptyChunk, 256)}
	// Queue the burst: the flag's two regional indicators land in different
	// chunks, so per-chunk feeding without coalescing+carry would split the
	// cluster into two cells.
	p.out <- ptyChunk{data: full[6:10], ok: true} // first RI of the flag
	p.out <- ptyChunk{data: full[10:], ok: true}  // second RI + " world"

	cmd, finished := p.apply(ptyChunk{data: full[:6], ok: true}) // "hello "
	if finished || cmd == nil {
		t.Fatal("apply(live burst) should continue the drain")
	}
	if len(p.out) != 0 {
		t.Fatalf("burst not coalesced: %d chunks still queued after one apply (each would cost a full Update+View)", len(p.out))
	}
	if got, want := p.emu.Render(), ref.Render(); got != want {
		t.Fatalf("coalesced burst render mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// PERF (P1): the batch drain is bounded so one Update can't absorb unbounded
// work — at most paneBatchMaxChunks chunks; leftovers stay queued for the next
// readCmd/apply cycle, in order.
func TestExternalPaneDrainBatchChunkBound(t *testing.T) {
	const queued = 300
	p := &ExternalPane{out: make(chan ptyChunk, queued)}
	for i := 0; i < queued; i++ {
		p.out <- ptyChunk{data: []byte{'a'}, ok: true}
	}

	data, end := p.drainBatch(ptyChunk{data: []byte{'a'}, ok: true})
	if end != nil {
		t.Fatalf("unexpected end-of-stream: %+v", end)
	}
	if len(data) != paneBatchMaxChunks {
		t.Fatalf("drained %d bytes, want exactly paneBatchMaxChunks=%d (first + %d drained)", len(data), paneBatchMaxChunks, paneBatchMaxChunks-1)
	}
	if left := len(p.out); left != queued-(paneBatchMaxChunks-1) {
		t.Fatalf("%d chunks left queued, want %d for the next cycle", left, queued-(paneBatchMaxChunks-1))
	}
}

// PERF (P1): the byte bound stops the drain once the batch reaches
// paneBatchMaxBytes (it may overshoot by at most one chunk — the bound is
// checked before each receive).
func TestExternalPaneDrainBatchByteBound(t *testing.T) {
	chunk := func(n int) ptyChunk { return ptyChunk{data: bytes.Repeat([]byte{'a'}, n), ok: true} }
	p := &ExternalPane{out: make(chan ptyChunk, 8)}
	p.out <- chunk(300 << 10)
	p.out <- chunk(300 << 10)
	p.out <- chunk(300 << 10) // would take the batch to 1.5 MiB — must stay queued

	data, end := p.drainBatch(chunk(600 << 10))
	if end != nil {
		t.Fatalf("unexpected end-of-stream: %+v", end)
	}
	if len(data) < paneBatchMaxBytes || len(data) > paneBatchMaxBytes+(300<<10) {
		t.Fatalf("batch = %d bytes, want ~paneBatchMaxBytes=%d (may overshoot by one chunk)", len(data), paneBatchMaxBytes)
	}
	if left := len(p.out); left != 1 {
		t.Fatalf("%d chunks left queued, want 1 (drain must stop at the byte bound)", left)
	}
}

// PERF (P1): an end-of-stream marker pulled mid-drain finishes the pane AND
// still feeds every byte received before it — no output is lost when the
// stream dies inside a burst.
func TestExternalPaneApplyBatchEndOfStream(t *testing.T) {
	wantErr := errors.New("child exited 1")
	p := &ExternalPane{emu: vt.NewEmulator(40, 1), out: make(chan ptyChunk, 4)}
	p.out <- ptyChunk{data: []byte("world"), ok: true}
	p.out <- ptyChunk{ok: false, err: wantErr}

	cmd, finished := p.apply(ptyChunk{data: []byte("hello "), ok: true})
	if !finished || cmd != nil {
		t.Fatal("apply should finish when the batch contains the end-of-stream marker")
	}
	if !p.exited || !errors.Is(p.err, wantErr) {
		t.Fatalf("exited=%v err=%v, want exited with the stream-end reason", p.exited, p.err)
	}
	if !strings.Contains(p.emu.Render(), "hello world") {
		t.Fatalf("bytes before the end marker were dropped: %q", p.emu.Render())
	}
}

// floodTransport produces output as fast as it is read — never blocking, never
// ending — until Close, after which Read fails. It drives the reader goroutine
// into the P5 scenario: output channel full, reader blocked mid-send.
type floodTransport struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func newFloodTransport() *floodTransport { return &floodTransport{closed: make(chan struct{})} }

func (f *floodTransport) Read(b []byte) (int, error) {
	select {
	case <-f.closed:
		return 0, errors.New("flood transport closed")
	default:
	}
	for i := range b {
		b[i] = 'x'
	}
	return len(b), nil
}

func (f *floodTransport) Write(b []byte) (int, error) { return len(b), nil }
func (f *floodTransport) Resize(cols, rows int) error { return nil }
func (f *floodTransport) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

// blockingWriteTransport blocks every Write until Close — the stalled-forward
// scenario, where conn.WriteMessage parks on a dead TCP window. Read also
// parks until Close so the reader goroutine stays quiet. writeStarted is
// closed when the first Write begins, i.e. the input-writer goroutine has
// consumed that entry and is now stuck inside the transport.
type blockingWriteTransport struct {
	closed       chan struct{}
	closeOnce    sync.Once
	writeStarted chan struct{}
	startOnce    sync.Once
}

func newBlockingWriteTransport() *blockingWriteTransport {
	return &blockingWriteTransport{closed: make(chan struct{}), writeStarted: make(chan struct{})}
}

func (b *blockingWriteTransport) Read(p []byte) (int, error) {
	<-b.closed
	return 0, errors.New("blocking transport closed")
}

func (b *blockingWriteTransport) Write(p []byte) (int, error) {
	b.startOnce.Do(func() { close(b.writeStarted) })
	<-b.closed
	return 0, errors.New("blocking transport closed")
}

func (b *blockingWriteTransport) Resize(cols, rows int) error { return nil }

func (b *blockingWriteTransport) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

// REGRESSION (P4): a transport whose Write has stalled must not freeze the
// Bubble Tea loop — keystrokes enqueue without blocking and ctrl+] detach
// still works. Before P4, handleKey called transport.Write synchronously on
// the UI goroutine, so the FIRST keystroke into a stalled forward froze the
// entire dashboard, detach included.
func TestAppPaneKeyAndDetachSurviveStalledTransport(t *testing.T) {
	tr := newBlockingWriteTransport()
	p := NewExternalPaneTransport(Session{}, "claude", func(cols, rows int) (PaneTransport, error) {
		return tr, nil
	}, nil)
	if cmd := p.Init(); cmd == nil || p.err != nil {
		t.Fatalf("Init failed: %v", p.err)
	}
	app := NewApp(nil, nil, nil)
	app.dashboard.seeded = true
	app.external = p
	app.screen = ScreenExternal

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.Update(keyMsg("x")) // the writer consumes this and parks inside Write
		<-tr.writeStarted
		app.Update(keyMsg("y"))      // queued behind the stalled write — must not block
		app.Update(keyMsg("ctrl+]")) // arm the leader
		app.Update(keyMsg("ctrl+]")) // resolve: detach
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("App.Update blocked on a stalled pane transport (P4): keystroke/detach froze the UI loop")
	}
	if app.screen != ScreenDashboard {
		t.Fatalf("ctrl+] ctrl+] did not detach over a stalled transport: screen=%v", app.screen)
	}

	// Teardown must be prompt too: close() signals done, and transport.Close()
	// unblocks the parked Write so the writer goroutine exits (no leak).
	closed := make(chan struct{})
	go func() { p.close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("pane close() blocked on a stalled transport")
	}
}

// orderRecordingTransport records Writes and Resizes in arrival order; Read
// parks until Close so the reader goroutine stays quiet.
type orderRecordingTransport struct {
	closed    chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	ops       []string
}

func newOrderRecordingTransport() *orderRecordingTransport {
	return &orderRecordingTransport{closed: make(chan struct{})}
}

func (o *orderRecordingTransport) Read(p []byte) (int, error) {
	<-o.closed
	return 0, errors.New("order transport closed")
}

func (o *orderRecordingTransport) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ops = append(o.ops, "w:"+string(p))
	return len(p), nil
}

func (o *orderRecordingTransport) Resize(cols, rows int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ops = append(o.ops, fmt.Sprintf("r:%dx%d", cols, rows))
	return nil
}

func (o *orderRecordingTransport) Close() error {
	o.closeOnce.Do(func() { close(o.closed) })
	return nil
}

func (o *orderRecordingTransport) opsSnapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.ops...)
}

// P4 ordering: keys and resize reach the transport in exactly the order the
// UI produced them — resize rides the same queue as keystrokes, so geometry
// can never overtake type-ahead (or vice versa) on the wire.
func TestExternalPaneInputOrderPreserved(t *testing.T) {
	tr := newOrderRecordingTransport()
	p := NewExternalPaneTransport(Session{}, "claude", func(cols, rows int) (PaneTransport, error) {
		return tr, nil
	}, nil)
	p.w, p.h = 40, 10
	if cmd := p.Init(); cmd == nil || p.err != nil {
		t.Fatalf("Init failed: %v", p.err)
	}
	defer p.close()

	p.handleKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	p.handleKey(tea.KeyPressMsg{Code: 'b', Text: "b"})
	p.resize(50, 12) // → 50x11 after the reserved status row
	p.handleKey(tea.KeyPressMsg{Code: 'c', Text: "c"})

	want := []string{"w:a", "w:b", "r:50x11", "w:c"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := tr.opsSnapshot()
		if len(got) >= len(want) {
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("transport op order = %v, want %v", got, want)
				}
			}
			if len(got) > len(want) {
				t.Fatalf("unexpected extra transport ops: %v", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("transport ops = %v, want %v (input writer never drained the queue)", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// P4 drop path: with the writer parked inside a stalled Write and the queue
// full, further input is DROPPED — recorded as a pane-level error on p.err,
// the surface externalPaneFinishedMsg reports to the dashboard — instead of
// blocking the UI goroutine. Filling the queue itself records nothing.
func TestExternalPaneFullQueueDropsAndRecordsError(t *testing.T) {
	tr := newBlockingWriteTransport()
	p := NewExternalPaneTransport(Session{}, "claude", func(cols, rows int) (PaneTransport, error) {
		return tr, nil
	}, nil)
	if cmd := p.Init(); cmd == nil || p.err != nil {
		t.Fatalf("Init failed: %v", p.err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// First key: consumed by the writer, which parks inside Write.
		p.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
		<-tr.writeStarted
		// The queue is now empty and the writer is stuck, so exactly
		// paneInputQueueCap more entries fit without dropping.
		for i := 0; i < paneInputQueueCap; i++ {
			p.handleKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
		}
		if p.err != nil {
			t.Errorf("filling the queue must not record a drop: %v", p.err)
		}
		// One more overflows: dropped and recorded, still without blocking.
		p.handleKey(tea.KeyPressMsg{Code: 'b', Text: "b"})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("input send blocked on a full queue (P4): it must drop, not block")
	}
	if p.err == nil || !strings.Contains(p.err.Error(), "dropped") {
		t.Fatalf("full-queue drop not recorded: err = %v", p.err)
	}
	p.close()
}

// REGRESSION (P5): close() while the output channel is full must unblock the
// reader goroutine's pending send so it exits, instead of retaining it (plus up
// to ~8 MB of buffered chunks) forever. The reader closes p.out on exit, so an
// eventually-closed channel IS the exit signal; without the done channel this
// test times out with the reader parked on `p.out <-`.
func TestExternalPaneReaderExitsOnCloseWithFullChannel(t *testing.T) {
	tr := newFloodTransport()
	p := NewExternalPaneTransport(Session{}, "claude", func(cols, rows int) (PaneTransport, error) {
		return tr, nil
	}, nil)
	if cmd := p.Init(); cmd == nil || p.err != nil {
		t.Fatalf("Init failed: %v", p.err)
	}

	// Let the flood fill the channel to capacity; nothing consumes it (as after
	// a pane replacement, where handlePtyOutput drops stale messages without
	// re-issuing readCmd), so the reader ends up blocked mid-send.
	deadline := time.Now().Add(5 * time.Second)
	for len(p.out) < cap(p.out) {
		if time.Now().After(deadline) {
			t.Fatalf("output channel never filled: %d/%d", len(p.out), cap(p.out))
		}
		time.Sleep(time.Millisecond)
	}

	p.close()
	p.close() // double-close must be safe (replacement path + finished path)

	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-p.out:
			if !ok {
				return // reader exited and closed p.out — no leak
			}
		case <-timeout:
			t.Fatal("reader goroutine did not exit after close(): p.out never closed (P5 leak)")
		}
	}
}

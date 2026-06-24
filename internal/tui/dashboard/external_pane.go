package dashboard

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
	"github.com/rivo/uniseg"
)

// ExternalPane embeds the real `opencode attach` client as a Tier-2 PTY pane:
// the runner supervises `opencode serve` in the pod, and a local `opencode`
// process — reached over a localhost port-forward — is the interactive TUI. We
// do NOT hand the whole terminal to it (that would lose the reserved status row
// and an instant minimize); instead the child runs in an OS PTY whose output is
// fed into a VT emulator that we render as a full Bubble Tea frame each tick,
// reserving the last row for a status line.
//
// Data flow:
//
//	opencode attach (OS PTY) ──stdout──▶ reader goroutine ──chan──▶ emulator.Write
//	                                                                     │ Render()
//	user keys ──KeyPressMsg──▶ encodeKey ──▶ PTY master (stdin)          ▼
//	                                                             View (rows + status)
//
// The reader goroutine drains the PTY into a buffered channel unconditionally,
// so the child never blocks on a full PTY buffer even while the pane is
// minimized (screen back on the dashboard) — which is what makes re-opening the
// pane instant rather than a reconnect.
type ExternalPane struct {
	sess  Session
	creds OpencodeCreds

	emu  *vt.Emulator
	ptmx *os.File
	cmd  *exec.Cmd
	out  chan ptyChunk

	// carry holds a trailing non-ASCII grapheme withheld from the emulator until
	// the next chunk, so a cluster split across PTY reads isn't rendered as two
	// cells (O7). At most one grapheme cluster (a few bytes).
	carry []byte

	// activeModes tracks DEC modes the child has enabled via the emulator's
	// callbacks, so we know whether to forward PasteMsg (bracketed paste) and
	// MouseMsg (mouse reporting) to the child PTY.
	activeModes map[ansi.DECMode]bool

	w, h   int
	exited bool
	err    error
}

// minSize keeps the emulator non-degenerate before the first WindowSizeMsg.
const extDefaultW, extDefaultH = 80, 24

// ptyChunk is one read from the child PTY; ok=false marks EOF (child exited).
type ptyChunk struct {
	data []byte
	ok   bool
}

// ptyOutputMsg carries a PTY read back into the Bubble Tea loop. It is handled
// by the App at the top level (not gated on the active screen) so the emulator
// stays current — and the reader keeps draining — even while minimized.
type ptyOutputMsg struct {
	pane  *ExternalPane
	chunk ptyChunk
}

func NewExternalPane(sess Session, creds OpencodeCreds) *ExternalPane {
	return &ExternalPane{sess: sess, creds: creds, w: extDefaultW, h: extDefaultH, activeModes: make(map[ansi.DECMode]bool)}
}

// emuSize is the emulator/PTY size: full width, height minus the reserved
// status row. Guarded to stay positive.
func (p *ExternalPane) emuSize() (cols, rows int) {
	cols, rows = p.w, p.h-1
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

// Init spawns `opencode attach` in a PTY and starts draining it. It returns the
// first read Cmd; on spawn failure it returns a finished message carrying the
// error so the App can fall back to the dashboard.
func (p *ExternalPane) Init() tea.Cmd {
	// Pre-flight: the local `opencode` client must be installed (and version-
	// matched to the pod's `opencode serve`). Without it the spawn would fail
	// with a bare ENOENT; surface an actionable message instead.
	if _, err := exec.LookPath("opencode"); err != nil {
		p.exited = true
		p.err = fmt.Errorf("opencode CLI not found on PATH — install it locally (Nix) to attach to opencode sessions")
		return func() tea.Msg { return externalPaneFinishedMsg{err: p.err} }
	}

	cols, rows := p.emuSize()
	p.emu = vt.NewEmulator(cols, rows)
	// Track DEC modes set by the child so we know whether to forward PasteMsg
	// and MouseMsg (O6).
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

	// Auth: the server URL is positional; basic-auth user via -u and the
	// password via OPENCODE_SERVER_PASSWORD in the env (never argv, so it stays
	// out of the host process list).
	cmd := exec.Command("opencode", "attach", p.creds.URL, "-u", p.creds.Username)
	cmd.Env = append(os.Environ(), "OPENCODE_SERVER_PASSWORD="+p.creds.Password, "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		p.exited, p.err = true, fmt.Errorf("opencode attach: %w", err)
		return func() tea.Msg { return externalPaneFinishedMsg{err: p.err} }
	}
	p.cmd = cmd
	p.ptmx = ptmx
	p.out = make(chan ptyChunk, 256)

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				p.out <- ptyChunk{data: cp, ok: true}
			}
			if rerr != nil {
				p.out <- ptyChunk{ok: false}
				close(p.out)
				return
			}
		}
	}()

	// Drain the emulator's reply buffer back to the child PTY. The vt emulator
	// answers terminal capability queries (DA, DSR/cursor-position, DECRQM, OSC
	// 10/11 color) by writing to an internal io.Pipe exposed only via Read();
	// opencode's opentui renderer measures cell/Unicode width on startup with OSC
	// 66 + CSI 6n and BLOCKS until it gets those cursor-position reports. Without
	// this pump the replies never reach the child, so it never paints (blank pane)
	// — and because the reply pipe is unbuffered, the emu.Write() in apply() (on
	// the Bubble Tea main loop) would itself block on the first query, freezing
	// the whole dashboard. close() closes the emulator's reply pipe so this
	// goroutine's blocked emu.Read returns EOF and it exits.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := p.emu.Read(buf)
			if n > 0 {
				_, _ = ptmx.Write(buf[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()

	return p.readCmd()
}

// readCmd blocks on the next PTY chunk and wraps it as a ptyOutputMsg. The App
// re-issues it (via apply) after each chunk so the drain continues.
func (p *ExternalPane) readCmd() tea.Cmd {
	out := p.out
	return func() tea.Msg {
		chunk, ok := <-out
		if !ok {
			return ptyOutputMsg{pane: p, chunk: ptyChunk{ok: false}}
		}
		return ptyOutputMsg{pane: p, chunk: chunk}
	}
}

// apply feeds a PTY chunk into the emulator and returns the next read Cmd, or a
// finished message when the child has exited. Returns (cmd, finished).
func (p *ExternalPane) apply(chunk ptyChunk) (tea.Cmd, bool) {
	if !chunk.ok {
		p.exited = true
		// Flush any held trailing grapheme now that the stream has ended.
		if p.emu != nil && len(p.carry) > 0 {
			_, _ = p.emu.Write(p.carry)
			p.carry = nil
		}
		// Capture a non-zero exit so a child that dies on startup (e.g. attach
		// hitting a not-yet-ready server, or an auth/version mismatch) reports a
		// reason instead of silently bouncing back to the dashboard.
		if werr := p.cmd.Wait(); werr != nil {
			p.err = fmt.Errorf("opencode attach exited: %w", werr)
		}
		return nil, true
	}
	if p.emu != nil {
		p.feed(chunk.data)
	}
	return p.readCmd(), false
}

// feed writes PTY bytes to the emulator without splitting a grapheme cluster
// across a write boundary (O7): the embedded vt emulator flushes its pending
// grapheme at the end of every Write, so a cluster straddling two PTY reads
// would render as two cells. feed prepends any previously-held tail, writes up
// to the last safe boundary, and keeps the trailing (possibly-extendable)
// grapheme for the next chunk.
func (p *ExternalPane) feed(data []byte) {
	buf := data
	if len(p.carry) > 0 {
		buf = append(p.carry, data...)
		p.carry = nil
	}
	cut := safeWriteBoundary(buf)
	if cut > 0 {
		_, _ = p.emu.Write(buf[:cut])
	}
	if cut < len(buf) {
		p.carry = append([]byte(nil), buf[cut:]...)
	}
}

// safeWriteBoundary returns the length of the prefix of b that can be written to
// the emulator without splitting a grapheme cluster. A buffer ending in ASCII
// (which includes every control/escape sequence) is always safe, so escapes are
// never delayed; only a trailing non-ASCII grapheme — which a following combining
// mark or ZWJ continuation could extend — is held back.
func safeWriteBoundary(b []byte) int {
	if len(b) == 0 || b[len(b)-1] < 0x80 {
		return len(b)
	}
	state := -1
	pos := 0
	for pos < len(b) {
		cluster, rest, _, newState := uniseg.FirstGraphemeCluster(b[pos:], state)
		if len(rest) == 0 {
			return pos // start of the final cluster — hold it back
		}
		pos += len(cluster)
		state = newState
	}
	return pos
}

// handleKey encodes a key press to terminal input bytes and writes them to the
// PTY master. The universal escape is intercepted by the App before reaching
// here, so every key delivered to the pane is meant for the child.
func (p *ExternalPane) handleKey(msg tea.KeyPressMsg) {
	if p.ptmx == nil {
		return
	}
	if b := encodeKey(msg); len(b) > 0 {
		_, _ = p.ptmx.Write(b)
	}
}

// handlePaste forwards pasted text to the child PTY wrapped in bracketed-paste
// sequences when the child has enabled the mode (O6).
func (p *ExternalPane) handlePaste(msg tea.PasteMsg) {
	if p.ptmx == nil || !p.activeModes[ansi.ModeBracketedPaste] {
		return
	}
	_, _ = p.ptmx.WriteString(ansi.BracketedPasteStart)
	_, _ = p.ptmx.WriteString(msg.Content)
	_, _ = p.ptmx.WriteString(ansi.BracketedPasteEnd)
}

// mouseEnabled returns true if the child has enabled any mouse tracking mode.
func (p *ExternalPane) mouseEnabled() bool {
	for _, m := range []ansi.DECMode{
		ansi.ModeMouseX10,
		ansi.ModeMouseNormal,
		ansi.ModeMouseHighlight,
		ansi.ModeMouseButtonEvent,
		ansi.ModeMouseAnyEvent,
	} {
		if p.activeModes[m] {
			return true
		}
	}
	return false
}

// handleMouse forwards a mouse event to the child PTY encoded as xterm SGR
// mouse when the child has enabled mouse reporting (O6).
func (p *ExternalPane) handleMouse(msg tea.MouseMsg) {
	if p.ptmx == nil || !p.mouseEnabled() {
		return
	}

	m := msg.Mouse()
	isMotion := false
	isRelease := false
	switch msg.(type) {
	case tea.MouseMotionMsg:
		isMotion = true
	case tea.MouseReleaseMsg:
		isRelease = true
	}

	b := ansi.EncodeMouseButton(m.Button, isMotion,
		m.Mod.Contains(tea.ModShift),
		m.Mod.Contains(tea.ModAlt),
		m.Mod.Contains(tea.ModCtrl))

	if p.activeModes[ansi.ModeMouseExtSgr] {
		_, _ = p.ptmx.WriteString(ansi.MouseSgr(b, m.X, m.Y, isRelease))
	} else {
		_, _ = p.ptmx.WriteString(ansi.MouseX10(b, m.X, m.Y))
	}
}

// resize updates the pane size and propagates it to the emulator and the PTY
// (which sends SIGWINCH to the child so it repaints at the new size).
func (p *ExternalPane) resize(w, h int) {
	p.w, p.h = w, h
	cols, rows := p.emuSize()
	if p.emu != nil {
		p.emu.Resize(cols, rows)
	}
	if p.ptmx != nil {
		_ = pty.Setsize(p.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	}
}

// close kills the child, reaps it, and releases the PTY. Called when the pane is
// torn down for real (child exited, or replaced by a different session) — NOT on
// minimize.
func (p *ExternalPane) close() {
	// Close the master first so the reader goroutine's blocked ptmx.Read returns
	// an error and the goroutine exits.
	if p.ptmx != nil {
		_ = p.ptmx.Close()
		p.ptmx = nil
	}
	// Stop the reply-pump goroutine by closing the emulator's reply pipe (its
	// InputPipe writer) directly rather than calling emu.Close(). emu.Read (pump
	// goroutine) and emu.Close (this main-loop call) both touch the emulator's
	// internal `closed` bool with no synchronization — a data race. Closing the
	// pipe writer makes the pump's blocked emu.Read return EOF without writing
	// `closed`, so that field stays write-free and the race is gone by
	// construction. (vt.SafeEmulator does not help: its Read is unlocked and it
	// doesn't override Close.) Fall back to emu.Close() only if the input pipe
	// isn't the expected *io.PipeWriter, so the goroutine can't leak.
	if p.emu != nil {
		if pw, ok := p.emu.InputPipe().(*io.PipeWriter); ok {
			_ = pw.CloseWithError(io.EOF)
		} else {
			_ = p.emu.Close()
		}
	}
	if p.cmd != nil && p.cmd.Process != nil && !p.exited {
		_ = p.cmd.Process.Kill()
		// Reap the killed child (O1). Without Wait() it lingers as a <defunct>
		// zombie until program exit: a replaced pane never reaches apply()'s EOF
		// Wait() because app.go's stale-pane guard drops its ptyOutputMsg. SIGKILL
		// is uncatchable so Wait() returns promptly — same call the EOF path makes.
		_ = p.cmd.Wait()
		p.exited = true
	}
}

// View renders the emulator screen (h-1 rows) plus the reserved status row.
func (p *ExternalPane) View() tea.View {
	body := ""
	if p.emu != nil {
		body = p.emu.Render()
	}
	status := p.statusRow()
	v := tea.NewView(body + "\n" + status)
	v.AltScreen = true
	return v
}

// statusRow is the reserved last line: session · client · model · escape hint.
func (p *ExternalPane) statusRow() string {
	model := p.sess.Model
	if model == "" {
		model = "opencode"
	}
	left := lipgloss.NewStyle().Foreground(theme.Charple).Bold(true).Render(p.sess.Title) +
		lipgloss.NewStyle().Foreground(theme.TextMuted).Render(" · opencode · "+model)
	right := kit.Kbd("^]", "dash")

	w := p.w
	if w < 1 {
		w = extDefaultW
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	bar := left + lipgloss.NewStyle().Render(spaces(gap)) + right
	return lipgloss.NewStyle().Width(w).Background(theme.Surface).Render(bar)
}

func spaces(n int) string {
	if n < 0 {
		n = 0
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

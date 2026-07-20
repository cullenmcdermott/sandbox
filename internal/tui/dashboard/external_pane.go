package dashboard

import (
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/cullenmcdermott/sandbox/tui/kit"
	"github.com/cullenmcdermott/sandbox/tui/theme"
	"github.com/rivo/uniseg"
)

// ExternalPane embeds a real external agent TUI as a Tier-2 pane: the byte
// stream of a terminal client whose engine lives elsewhere. For opencode that
// client is a local `opencode attach` child in an OS PTY (the runner
// supervises `opencode serve` in the pod); for claude-pane it is the
// interactive claude child running IN the pod under a runner-owned PTY,
// streamed over the pane WebSocket. Either way we do NOT hand the whole
// terminal to it (that would lose the reserved status row and an instant
// minimize); the transport's output is fed into a VT emulator that we render
// as a full Bubble Tea frame each tick, reserving the last row for a status
// line.
//
// Data flow:
//
//	PaneTransport ──Read──▶ reader goroutine ──chan──▶ emulator.Write
//	                                                        │ Render()
//	user keys ──KeyPressMsg──▶ encodeKey ──▶ transport.Write ▼
//	                                                View (rows + status)
//
// The reader goroutine drains the transport into a buffered channel
// unconditionally, so the remote end never blocks on a full buffer even while
// the pane is minimized (screen back on the dashboard) — which is what makes
// re-opening the pane instant rather than a reconnect.
type ExternalPane struct {
	sess Session
	// label names the embedded client in the status row ("opencode", "claude").
	label string
	// dial establishes the transport once, in Init, at the emulator geometry.
	dial PaneDial

	// liveSession, when set, returns the current dashboard read-model Session for
	// this pane's id — refreshed by the background passive SSE stream that the
	// runner's observer feeds (Phase 4). The status row reads it so live
	// title/status/ctx%/cost track the in-pane turn, instead of the static snapshot
	// captured at construction. nil in tests/standalone → falls back to p.sess.
	liveSession func() Session

	// transportClose tears down the attach connection's forwards (the HTTP/SSH/
	// opencode SPDY forwards — ConnectResult.Close, §1d C1). The pane's byte
	// stream rides those forwards (opencode's local child dials through one; the
	// claude WS is one), so this must run only when the pane is torn down for
	// real (close()), never on minimize. nil in tests.
	transportClose func()

	emu       *vt.Emulator
	transport PaneTransport
	out       chan ptyChunk

	// carry holds a trailing non-ASCII grapheme withheld from the emulator until
	// the next chunk, so a cluster split across transport reads isn't rendered as
	// two cells (O7). At most one grapheme cluster (a few bytes).
	carry []byte

	// activeModes tracks DEC modes the child has enabled via the emulator's
	// callbacks, so we know whether to forward PasteMsg (bracketed paste) and
	// MouseMsg (mouse reporting) to the child.
	activeModes map[ansi.DECMode]bool

	w, h   int
	exited bool
	err    error
}

// minSize keeps the emulator non-degenerate before the first WindowSizeMsg.
const extDefaultW, extDefaultH = 80, 24

// ptyChunk is one read from the pane transport; ok=false marks end of stream.
type ptyChunk struct {
	data []byte
	ok   bool
	// err is the stream-end reason when ok is false: nil for a clean EOF (a
	// deliberate quit / detach), else the transport's description (child exit
	// status, pane preemption, network drop).
	err error
}

// ptyOutputMsg carries a transport read back into the Bubble Tea loop. It is
// handled by the App at the top level (not gated on the active screen) so the
// emulator stays current — and the reader keeps draining — even while minimized.
type ptyOutputMsg struct {
	pane  *ExternalPane
	chunk ptyChunk
}

// NewExternalPane builds the opencode pane: a local `opencode attach` child in
// an OS PTY, dialed with the connector's creds.
func NewExternalPane(sess Session, creds OpencodeCreds, liveSession func() Session) *ExternalPane {
	return NewExternalPaneTransport(sess, "opencode", dialOpencodePane(creds), liveSession)
}

// NewExternalPaneTransport builds a pane over an arbitrary transport dialer —
// the seam the claude-pane WebSocket stream plugs into (ConnectResult.PaneDial)
// and a future codex pane will reuse. label names the client in the status row.
func NewExternalPaneTransport(sess Session, label string, dial PaneDial, liveSession func() Session) *ExternalPane {
	return &ExternalPane{sess: sess, label: label, dial: dial, liveSession: liveSession, w: extDefaultW, h: extDefaultH, activeModes: make(map[ansi.DECMode]bool)}
}

// session returns the live read-model Session when a liveSession accessor is set
// (the attached dashboard's, fed by the passive observer stream), else the static
// snapshot captured at construction.
func (p *ExternalPane) session() Session {
	if p.liveSession != nil {
		return p.liveSession()
	}
	return p.sess
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

// Init dials the transport and starts draining it. It returns the first read
// Cmd; on dial failure it returns a finished message carrying the error so the
// App can fall back to the dashboard.
func (p *ExternalPane) Init() tea.Cmd {
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

	tr, err := p.dial(cols, rows)
	if err != nil {
		p.exited, p.err = true, err
		return func() tea.Msg { return externalPaneFinishedMsg{err: p.err} }
	}
	p.transport = tr
	p.out = make(chan ptyChunk, 256)

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := tr.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				p.out <- ptyChunk{data: cp, ok: true}
			}
			if rerr != nil {
				// io.EOF is the clean end of stream; anything else is the reason
				// it died (child exit status, pane preemption, network drop).
				var reason error
				if !errors.Is(rerr, io.EOF) {
					reason = rerr
				}
				p.out <- ptyChunk{ok: false, err: reason}
				close(p.out)
				return
			}
		}
	}()

	// Drain the emulator's reply buffer back to the transport. The vt emulator
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
				_, _ = tr.Write(buf[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()

	return p.readCmd()
}

// readCmd blocks on the next transport chunk and wraps it as a ptyOutputMsg.
// The App re-issues it (via apply) after each chunk so the drain continues.
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

// apply feeds a transport chunk into the emulator and returns the next read
// Cmd, or a finished message when the stream has ended. Returns (cmd, finished).
func (p *ExternalPane) apply(chunk ptyChunk) (tea.Cmd, bool) {
	if !chunk.ok {
		p.exited = true
		// Flush any held trailing grapheme now that the stream has ended.
		if p.emu != nil && len(p.carry) > 0 {
			_, _ = p.emu.Write(p.carry)
			p.carry = nil
		}
		// The transport's stream-end reason (child exit status, preemption) —
		// nil for a clean EOF, so a deliberate quit doesn't report an error.
		if chunk.err != nil {
			p.err = chunk.err
		}
		return nil, true
	}
	if p.emu != nil {
		p.feed(chunk.data)
	}
	return p.readCmd(), false
}

// feed writes transport bytes to the emulator without splitting a grapheme
// cluster across a write boundary (O7): the embedded vt emulator flushes its
// pending grapheme at the end of every Write, so a cluster straddling two reads
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
// transport. The universal escape is intercepted by the App before reaching
// here, so every key delivered to the pane is meant for the child.
func (p *ExternalPane) handleKey(msg tea.KeyPressMsg) {
	if p.transport == nil {
		return
	}
	if b := encodeKey(msg); len(b) > 0 {
		_, _ = p.transport.Write(b)
	}
}

// handlePaste forwards pasted text to the child wrapped in bracketed-paste
// sequences when the child has enabled the mode (O6). One Write so the paste
// travels as a single unit (one frame on a network transport).
func (p *ExternalPane) handlePaste(msg tea.PasteMsg) {
	if p.transport == nil || !p.activeModes[ansi.ModeBracketedPaste] {
		return
	}
	_, _ = p.transport.Write([]byte(ansi.BracketedPasteStart + msg.Content + ansi.BracketedPasteEnd))
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

// handleMouse forwards a mouse event to the child encoded as xterm SGR mouse
// when the child has enabled mouse reporting (O6).
func (p *ExternalPane) handleMouse(msg tea.MouseMsg) {
	if p.transport == nil || !p.mouseEnabled() {
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
		_, _ = p.transport.Write([]byte(ansi.MouseSgr(b, m.X, m.Y, isRelease)))
	} else {
		_, _ = p.transport.Write([]byte(ansi.MouseX10(b, m.X, m.Y)))
	}
}

// resize updates the pane size and propagates it to the emulator and the
// transport (a PTY SIGWINCH locally, a resize control frame over the wire).
func (p *ExternalPane) resize(w, h int) {
	p.w, p.h = w, h
	cols, rows := p.emuSize()
	if p.emu != nil {
		p.emu.Resize(cols, rows)
	}
	if p.transport != nil {
		_ = p.transport.Resize(cols, rows)
	}
}

// close tears the pane down for real (stream ended, or replaced by a different
// session) — NOT on minimize.
func (p *ExternalPane) close() {
	// Close the transport first so the reader goroutine's blocked Read returns
	// and the goroutine exits; the child-process transport also kills + reaps
	// its child here (O1), and the WS transport performs the closing handshake
	// (a clean detach — the remote child keeps running).
	if p.transport != nil {
		_ = p.transport.Close()
		p.transport = nil
		p.exited = true
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
	// Release the attach connection's forwards last, after the stream that used
	// them is down (§1d C1).
	if p.transportClose != nil {
		p.transportClose()
		p.transportClose = nil
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

// statusRow is the reserved last line: title · client · model · live status ·
// ctx% · cost, with a detach hint on the right. The live status/ctx%/cost come
// from the runner's passive observer stream (opencode observer / claude-pane
// hook+statusline observer) via the dashboard read-model, so every external
// pane reaches metric parity regardless of engine. (^] / ctrl+] only — esc is
// forwarded to the child so its own overlays can use it.)
func (p *ExternalPane) statusRow() string {
	s := p.session()
	model := s.Model
	// Display the bare model id, dropping the "provider/" prefix the observer emits
	// (e.g. "opencode/big-pickle" → "big-pickle").
	if i := strings.LastIndex(model, "/"); i >= 0 && i+1 < len(model) {
		model = model[i+1:]
	}
	if model == "" {
		model = p.label
	}
	muted := lipgloss.NewStyle().Foreground(theme.TextMuted)
	left := lipgloss.NewStyle().Foreground(theme.Charple).Bold(true).Render(s.DisplayTitle()) +
		muted.Render(" · "+p.label+" · "+model)

	// Live metrics, surfaced only once the observer has reported them (a fresh
	// pane with no turn yet shows just title/model, no empty "idle · ctx 0%").
	segs := []string{s.DashStatus.Glyph() + " " + s.DashStatus.String()}
	if pct := s.CtxPercent(); pct > 0 {
		segs = append(segs, fmt.Sprintf("ctx %d%%", pct))
	}
	if s.TotalCostUSD > 0 {
		segs = append(segs, fmt.Sprintf("$%.4f", s.TotalCostUSD))
	}
	left += muted.Render(" · " + strings.Join(segs, " · "))
	right := kit.Kbd("^]", "dash")

	w := p.w
	if w < 1 {
		w = extDefaultW
	}
	// spread truncates the (long) title-and-model left segment so a wide
	// DisplayTitle can't wrap the status bar to 2-3 lines; the ^]/dash key on the
	// right always stays visible.
	bar := spread(left, right, w)
	return lipgloss.NewStyle().Width(w).Background(theme.Surface).Render(bar)
}

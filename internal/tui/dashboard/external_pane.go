package dashboard

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

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
//	user keys ──KeyPressMsg──▶ encodeKey ──chan──▶ input    ▼
//	                             writer ──▶ transport.Write View (rows + status)
//
// The reader goroutine drains the transport into a buffered channel
// unconditionally, so the remote end never blocks on a full buffer even while
// the pane is minimized (screen back on the dashboard) — which is what makes
// re-opening the pane instant rather than a reconnect.
//
// The input-writer goroutine owns the UI-side transport writes (P4):
// handleKey/handlePaste/handleMouse/resize enqueue non-blockingly instead of
// calling transport.Write on the Bubble Tea goroutine, where a stalled
// forward (a blocked WebSocket write) would freeze the whole dashboard —
// including ctrl+] detach.
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

	// in feeds the input-writer goroutine (P4), the sole UI-side transport
	// writer: keys, paste, mouse, and resize control frames ride this queue in
	// the order the UI produced them. nil on a pane that never dialed
	// (direct-construction tests), where send/resize fall back to synchronous
	// transport calls. The emulator reply pump does NOT ride it: capability
	// replies must never be dropped (the child blocks awaiting them), and that
	// goroutine is already off the UI loop — transports serialize concurrent
	// writers (PaneStream.writeMu / OS PTY write atomicity).
	in chan paneInput

	// done is closed (exactly once, via closeOnce) in close() so blocked pane
	// goroutines exit: the reader's pending channel send unblocks (P5) —
	// transport.Close() unblocks a blocked Read, but not a send stuck on a full
	// p.out; without this signal a replaced pane could retain the goroutine
	// plus up to ~8 MB of buffered chunks for the process lifetime — and the
	// input writer's select observes it and returns (P4), abandoning anything
	// still queued for the torn-down transport.
	done      chan struct{}
	closeOnce sync.Once

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

// Chunk-coalescing bounds (P1, mirroring the SSE E5 batch drain): apply
// non-blockingly drains chunks already buffered in p.out and feeds the
// concatenation to the emulator as ONE Write, so a transport burst costs one
// Update+View instead of a full render per 32 KB read. The bounds cap the work
// (and allocation) a single Update can absorb; anything beyond them is picked
// up by the next readCmd/apply cycle.
const (
	paneBatchMaxChunks = 256
	paneBatchMaxBytes  = 1 << 20 // 1 MiB
)

// paneInputQueueCap bounds the input-writer queue (P4). 64 entries is far more
// input than a user produces in the time a healthy transport takes to drain
// one write; a full queue therefore means the transport has stalled, and the
// right move is to drop (recording a pane error) rather than block the UI.
const paneInputQueueCap = 64

// paneInput is one queued transport write: input bytes (key/paste/mouse
// encodings) or, when size is non-nil, a resize control frame. Resize rides
// the same queue as keystrokes so geometry and input reach the transport in
// the order the UI produced them (see resize).
type paneInput struct {
	data []byte
	size *paneSize
}

// paneSize is a queued resize in emulator geometry (cols × rows).
type paneSize struct{ cols, rows int }

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
	p.in = make(chan paneInput, paneInputQueueCap)
	p.done = make(chan struct{})

	// Input writer (P4): the sole consumer of p.in and the only UI-side
	// transport writer. Write/Resize errors are deliberately ignored, exactly
	// as the old synchronous handleKey path ignored them: the reader goroutine
	// is the authority on stream death and surfaces the reason. On close() the
	// done channel wakes the idle select; a Write blocked mid-stall is
	// unblocked by close()'s transport.Close(), after which the select observes
	// done and exits — so the goroutine can neither leak nor write to a nil
	// transport (it holds tr, not p.transport).
	go func() {
		for {
			select {
			case <-p.done:
				return
			case in := <-p.in:
				if in.size != nil {
					_ = tr.Resize(in.size.cols, in.size.rows)
					continue
				}
				_, _ = tr.Write(in.data)
			}
		}
	}()

	go func() {
		// The reader is the only sender on p.out, so closing it on exit is
		// always safe — and unblocks any in-flight readCmd, whose synthesized
		// end-of-stream message the App drops as stale after a replacement.
		defer close(p.out)
		buf := make([]byte, 32*1024)
		for {
			n, rerr := tr.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				select {
				case p.out <- ptyChunk{data: cp, ok: true}:
				case <-p.done:
					// close() ran: nobody will drain p.out again, so bail out
					// instead of blocking on the send forever (P5).
					return
				}
			}
			if rerr != nil {
				// io.EOF is the clean end of stream; anything else is the reason
				// it died (child exit status, pane preemption, network drop).
				var reason error
				if !errors.Is(rerr, io.EOF) {
					reason = rerr
				}
				select {
				case p.out <- ptyChunk{ok: false, err: reason}:
				case <-p.done:
				}
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
//
// After the first (already-received) chunk it non-blockingly drains any
// further chunks buffered in p.out — bounded by paneBatchMaxChunks /
// paneBatchMaxBytes — and feeds the concatenation to the emulator as ONE
// Write (P1): a burst costs one Update+View instead of one full render per
// 32 KB transport read. The coalesced bytes go through the same feed /
// safeWriteBoundary path as a single chunk, so grapheme-safe boundary
// handling (O7) is unchanged. The reader goroutine is untouched: it keeps
// draining the transport unconditionally so the remote never blocks, even
// while the pane is minimized.
func (p *ExternalPane) apply(chunk ptyChunk) (tea.Cmd, bool) {
	data, end := p.drainBatch(chunk)
	if len(data) > 0 && p.emu != nil {
		p.feed(data)
	}
	if end == nil {
		return p.readCmd(), false
	}
	p.exited = true
	// Flush any held trailing grapheme now that the stream has ended.
	if p.emu != nil && len(p.carry) > 0 {
		_, _ = p.emu.Write(p.carry)
		p.carry = nil
	}
	// The transport's stream-end reason (child exit status, preemption) —
	// nil for a clean EOF, so a deliberate quit doesn't report an error.
	if end.err != nil {
		p.err = end.err
	}
	return nil, true
}

// drainBatch coalesces the first (blocking-received) chunk with any successors
// already buffered in p.out, without blocking and in arrival order. It returns
// the concatenated data and, when the stream ended during the batch, the
// end-of-stream chunk (data still carries everything received before the end,
// so no output is lost). p.out may be nil on a pane that never dialed (tests);
// the select's default arm makes that a plain single-chunk apply.
func (p *ExternalPane) drainBatch(first ptyChunk) (data []byte, end *ptyChunk) {
	if !first.ok {
		return nil, &first
	}
	data = first.data
	for chunks := 1; chunks < paneBatchMaxChunks && len(data) < paneBatchMaxBytes; chunks++ {
		select {
		case c, ok := <-p.out:
			if !ok {
				// Channel closed mid-drain: the reader exited. Its end-of-stream
				// marker, if any, was consumed before the close, so this reads as
				// a clean EOF.
				return data, &ptyChunk{ok: false}
			}
			if !c.ok {
				return data, &c
			}
			data = append(data, c.data...)
		default:
			return data, nil
		}
	}
	return data, nil
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

// send hands input bytes to the input-writer goroutine without blocking the
// Bubble Tea loop (P4). On a full queue — only possible once the transport has
// stalled long enough to back up paneInputQueueCap writes — the input is
// DROPPED and a pane-level error recorded (it rides p.err, the same surface as
// every other pane stream error: externalPaneFinishedMsg carries it to the
// dashboard's inline connectErr when the pane ends) rather than blocking the
// UI, which would freeze the whole dashboard including ctrl+] detach. A pane
// constructed without Init (tests) has no writer goroutine; it writes
// synchronously, preserving the old seam. No-op after close() (transport nil).
func (p *ExternalPane) send(b []byte) {
	if p.transport == nil {
		return
	}
	if p.in == nil {
		_, _ = p.transport.Write(b)
		return
	}
	select {
	case p.in <- paneInput{data: b}:
	default:
		p.recordInputDrop("input")
	}
}

// recordInputDrop notes that a queued transport write was dropped because the
// input queue is full (stalled transport). The first drop reason sticks; a
// later real stream-end reason from apply overwrites it (more diagnostic).
// Runs only on the Bubble Tea goroutine, like every other p.err writer.
func (p *ExternalPane) recordInputDrop(kind string) {
	if p.err == nil {
		p.err = fmt.Errorf("pane %s dropped: transport stalled (%d writes queued)", kind, paneInputQueueCap)
	}
}

// handleKey encodes a key press to terminal input bytes and queues them for
// the transport. The universal escape is intercepted by the App before
// reaching here, so every key delivered to the pane is meant for the child.
func (p *ExternalPane) handleKey(msg tea.KeyPressMsg) {
	if b := encodeKey(msg); len(b) > 0 {
		p.send(b)
	}
}

// handlePaste forwards pasted text to the child wrapped in bracketed-paste
// sequences when the child has enabled the mode (O6). One send so the paste
// travels as a single unit (one frame on a network transport).
func (p *ExternalPane) handlePaste(msg tea.PasteMsg) {
	if !p.activeModes[ansi.ModeBracketedPaste] {
		return
	}
	p.send([]byte(ansi.BracketedPasteStart + msg.Content + ansi.BracketedPasteEnd))
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
	if !p.mouseEnabled() {
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
		p.send([]byte(ansi.MouseSgr(b, m.X, m.Y, isRelease)))
	} else {
		p.send([]byte(ansi.MouseX10(b, m.X, m.Y)))
	}
}

// resize updates the pane size and propagates it to the emulator and the
// transport (a PTY SIGWINCH locally, a resize control frame over the wire).
//
// The transport half rides the input queue rather than calling Resize
// directly: PaneStream.Resize shares the connection's write path (writeMu)
// with data writes, so a direct call here would both reintroduce the
// UI-goroutine block this queue exists to remove (P4) and race ahead of
// queued keystrokes — geometry overtaking type-ahead reorders what the child
// sees. Queued, resize and input reach the transport in UI order. A dropped
// resize (stalled transport) loses that SIGWINCH; the next WindowSizeMsg
// re-sends the then-current geometry.
func (p *ExternalPane) resize(w, h int) {
	p.w, p.h = w, h
	cols, rows := p.emuSize()
	if p.emu != nil {
		p.emu.Resize(cols, rows)
	}
	if p.transport == nil {
		return
	}
	if p.in == nil {
		// No writer goroutine (direct-construction tests): synchronous, as before.
		_ = p.transport.Resize(cols, rows)
		return
	}
	select {
	case p.in <- paneInput{size: &paneSize{cols: cols, rows: rows}}:
	default:
		p.recordInputDrop("resize")
	}
}

// close tears the pane down for real (stream ended, or replaced by a different
// session) — NOT on minimize.
func (p *ExternalPane) close() {
	// Signal the reader and input-writer goroutines first (P5/P4):
	// transport.Close() below unblocks a Read or Write in flight, but not the
	// reader's send blocked on a full p.out — after a pane replacement nobody
	// drains that channel again, so without this signal the goroutine (plus up
	// to ~8 MB of buffered chunks) would be retained for the process lifetime.
	// The input writer abandons anything still queued on p.in and exits via
	// its done select. closeOnce makes the double-close (replacement path and
	// finished path can both run) safe; done is nil on a pane that never dialed.
	p.closeOnce.Do(func() {
		if p.done != nil {
			close(p.done)
		}
	})
	// Close the transport so the reader goroutine's blocked Read returns
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

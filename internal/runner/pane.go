package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Pane attach sentinel errors, mapped from the runner's application close codes
// (runner/src/claude-pane.ts CLOSE_REPLACED / CLOSE_CHILD_EXITED). Callers
// branch with errors.Is: preemption means another pane took over (stop quietly);
// a child exit means the interactive claude process ended (the next attach
// respawns it via --resume).
var (
	ErrPanePreempted   = errors.New("runner pane: preempted by a newer pane attach")
	ErrPaneChildExited = errors.New("runner pane: interactive child exited")
)

// WebSocket close codes the runner uses on the pane socket
// (runner/src/claude-pane.ts).
const (
	paneCloseReplaced    = 4001
	paneCloseChildExited = 4002
)

// paneCloseGrace bounds how long Close waits to flush the closing handshake
// frame before tearing the connection down regardless.
const paneCloseGrace = time.Second

// PaneStream is a live `GET /sessions/:id/pane` WebSocket carrying a claude-pane
// session's interactive PTY (docs/runner-api.md): Read yields raw terminal
// output (the runner replays its scrollback ring first, then streams live),
// Write sends keyboard/paste input verbatim, and Resize sends the JSON control
// frame that SIGWINCHes the remote PTY.
//
// Read must be called from a single goroutine (the standard reader-loop shape);
// Write/Resize/Close are mutually safe and may run concurrently with Read.
type PaneStream struct {
	conn *websocket.Conn

	// writeMu serializes data writes and control (resize) writes — the
	// underlying websocket connection supports only one concurrent writer.
	writeMu sync.Mutex

	// frame is the remainder of the binary frame Read is currently draining.
	frame io.Reader

	// done is closed exactly once (via closeOnce) on the Close path; the
	// SANDBOX_TRACE RTT pinger goroutine exits on it (pane_rtt.go).
	done      chan struct{}
	closeOnce sync.Once

	// traceID is the connect-flow correlation id (Client.SetTraceID) stamped
	// into the RTT summary line; "" prints as "-".
	traceID string
	// rtt holds the RTT probe's samples; nil unless SANDBOX_TRACE armed the
	// probe at attach time (pane_rtt.go).
	rtt *rttRing
	// pingerDone is closed by the pinger goroutine as it exits (nil when the
	// probe is off), letting tests assert no goroutine outlives Close.
	pingerDone chan struct{}
}

// paneDialer is the pane transport's dialer. It differs from
// websocket.DefaultDialer in two ways, both [R2]:
//
//   - EnableCompression negotiates permessage-deflate. The pane carries ANSI,
//     which compresses roughly 10-20x, and a reattach replays up to 256 KiB of
//     scrollback as a single frame — the dominant cost of time-to-readable-pane
//     on a high-latency link. (gorilla implements no-context-takeover only, so
//     each frame deflates independently; the big replay frame is the win.)
//   - 32 KiB read/write buffers instead of gorilla's 4 KiB, so that replay
//     frame is not chopped into dozens of syscalls.
var paneDialer = &websocket.Dialer{
	Proxy:             websocket.DefaultDialer.Proxy,
	HandshakeTimeout:  websocket.DefaultDialer.HandshakeTimeout,
	EnableCompression: true,
	ReadBufferSize:    32 * 1024,
	WriteBufferSize:   32 * 1024,
}

// AttachPane dials the session's pane WebSocket on the runner base URL (the
// same port-forward every other runner call rides — no extra forward spec) and
// returns the live PTY stream. A positive cols/rows sends the initial resize
// control frame immediately after attach so the remote PTY adopts the client
// geometry before (or as) the first live output arrives; pass 0,0 to skip it.
//
// AttachPane is intentionally NOT part of session.RunnerClient: only the
// claude-pane backend serves the endpoint (every other backend answers 409),
// so it stays off the interface the TUI/dashboard fakes implement — callers
// reach it through the concrete *runner.Client, like Idle.
func (c *Client) AttachPane(ctx context.Context, ref session.Ref, cols, rows int) (*PaneStream, error) {
	// http://host → ws://host, https://host → wss://host.
	wsBase := "ws" + strings.TrimPrefix(c.baseURL, "http")
	u := wsBase + fmt.Sprintf("/sessions/%s/pane", ref.ID)
	// [R4] Carry the geometry on the handshake URL as well as in the initial
	// resize frame below. The runner spawns the child lazily inside attach(),
	// which runs before it can read any frame, so query params are the only way
	// the first-ever spawn gets a real size instead of 80x24 + a reflow one
	// round trip later. The resize frame stays: it is what a runner that ignores
	// the params (older image) still honours, and what covers a later resize.
	if cols > 0 && rows > 0 {
		u += fmt.Sprintf("?cols=%d&rows=%d", cols, rows)
	}
	hdr := http.Header{}
	if c.token != "" {
		hdr.Set("Authorization", "Bearer "+c.token)
	}
	// [T4] Carry the connect-flow correlation id into the pane attach, the same
	// header every other runner request already sets. Without it a connect id
	// from client/trace.go dead-ended at the HTTP routes: the pane — the primary
	// backend's whole interaction — could not be followed from it. Our dialer can
	// set handshake headers, so no query-param fallback is needed here (the
	// runner accepts one for clients that cannot).
	if c.traceID != "" {
		hdr.Set("X-Sandbox-Trace-Id", c.traceID)
	}
	conn, resp, err := paneDialer.DialContext(ctx, u, hdr)
	if err != nil {
		// A pre-upgrade rejection (401/404/409) arrives as ErrBadHandshake with
		// the plain HTTP response attached; fold its status into the error the
		// same way every other runner call does.
		if resp != nil {
			defer resp.Body.Close()
			return nil, statusError(resp, "runner pane attach")
		}
		return nil, fmt.Errorf("runner pane attach: %w", err)
	}
	// [R2] Compression is negotiated for BOTH directions, but only one of them
	// benefits. Server→client is scrollback replay and ANSI — the reason
	// EnableCompression is on at all. Client→server is keystrokes: gorilla has no
	// size threshold (unlike the runner's `threshold: 512`), so with write
	// compression left on it deflates every 1-3 byte frame, spending CPU on both
	// ends to make the frame BIGGER, on the one direction where latency is felt
	// keystroke by keystroke. Turning writes off keeps inbound inflate intact —
	// the negotiation is per-direction.
	conn.EnableWriteCompression(false)
	ps := &PaneStream{conn: conn, done: make(chan struct{}), traceID: c.traceID}
	if cols > 0 && rows > 0 {
		if rerr := ps.Resize(cols, rows); rerr != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("runner pane attach: initial resize: %w", rerr)
		}
	}
	if paneTraceEnabled() {
		ps.startRTTProbe(panePingInterval)
	}
	return ps, nil
}

// Read returns the next raw PTY output bytes. Frame boundaries are not
// preserved (this is a byte stream, matching io.Reader semantics). On the
// runner's application close codes it returns ErrPanePreempted /
// ErrPaneChildExited; a normal close maps to io.EOF.
func (p *PaneStream) Read(b []byte) (int, error) {
	for {
		if p.frame != nil {
			n, err := p.frame.Read(b)
			if errors.Is(err, io.EOF) {
				p.frame = nil
				if n > 0 {
					return n, nil
				}
				continue
			}
			return n, err
		}
		mt, r, err := p.conn.NextReader()
		if err != nil {
			return 0, mapPaneReadErr(err)
		}
		if mt != websocket.BinaryMessage {
			// The server sends no text frames today; drain and skip any that
			// appear so control chatter can never corrupt the PTY byte stream.
			_, _ = io.Copy(io.Discard, r)
			continue
		}
		p.frame = r
	}
}

// Write sends keyboard/paste input to the remote PTY as one binary frame.
func (p *PaneStream) Write(b []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if err := p.conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Resize sends the `{"type":"resize","cols":N,"rows":N}` control frame; the
// runner resizes the PTY (SIGWINCH) so the child reflows.
func (p *PaneStream) Resize(cols, rows int) error {
	msg, err := json.Marshal(struct {
		Type string `json:"type"`
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
	}{Type: "resize", Cols: cols, Rows: rows})
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return p.conn.WriteMessage(websocket.TextMessage, msg)
}

// Close detaches: it sends a best-effort closing handshake (so the runner sees
// a clean detach, leaving the child running) and tears the connection down.
// Safe to call concurrently with a blocked Read, which then returns an error.
func (p *PaneStream) Close() error {
	p.closeOnce.Do(func() {
		if p.done != nil {
			close(p.done) // stop the RTT pinger (if armed) before teardown
		}
		if p.rtt != nil {
			p.emitRTTSummary()
		}
	})
	// WriteControl is safe concurrently with other writes and readers.
	_ = p.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "detached"),
		time.Now().Add(paneCloseGrace))
	return p.conn.Close()
}

// mapPaneReadErr maps a websocket read error onto the pane sentinel errors:
// the runner's application close codes become ErrPanePreempted /
// ErrPaneChildExited, a clean close becomes io.EOF, and anything else (network
// drop, forced teardown) passes through for the caller to surface.
func mapPaneReadErr(err error) error {
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case paneCloseReplaced:
			return ErrPanePreempted
		case paneCloseChildExited:
			return ErrPaneChildExited
		case websocket.CloseNormalClosure, websocket.CloseGoingAway:
			return io.EOF
		}
	}
	return err
}

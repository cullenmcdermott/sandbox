// Package runner is the Go HTTP client for the sandbox-claude-runner API.
// It implements session.RunnerClient over a port-forward to the runner pod.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// sseReadTimeout is the half-open-connection watchdog window for the SSE
// stream (R6): if no line arrives within it, the body is force-closed to unblock
// the scanner. A package var so tests can shorten it.
var sseReadTimeout = 90 * time.Second

// sseMaxLineBytes bounds a single SSE line (one event). Events can legitimately
// be large (big diffs, whole-file Read results), so the ceiling is generous.
// Exceeding it makes bufio.Scanner stop with bufio.ErrTooLong; because the
// runner re-sends the same event on reconnect (after=lastSeq), an oversized
// event would otherwise drive an invisible reconnect loop — so C4 surfaces it
// as a visible error event instead of silently truncating. A package var so
// tests can shrink it.
var sseMaxLineBytes = 16 * 1024 * 1024

// Client implements session.RunnerClient over HTTP.
type Client struct {
	baseURL string // e.g. "http://127.0.0.1:8787"
	token   string // bearer token
	traceID string // optional connect-flow correlation id (see SetTraceID)
	http    *http.Client

	// remoteProtocolVersion caches the last protocolVersion Health() observed
	// on GET /healthz (0 until the first successful Health call, or if the
	// runner predates the field entirely). Accessed via atomics because Health
	// can be polled from a goroutine (e.g. waitHealthy) while ProtocolVersion is
	// read from the caller. See ProtocolVersion and session.ProtocolVersion.
	remoteProtocolVersion int32
}

// New creates a runner client targeting the given base URL with the given
// bearer token. baseURL should be the local end of a port-forward.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetTraceID attaches a connect-flow correlation id (client/trace.go tracer) to
// every subsequent request as the X-Sandbox-Trace-Id header. The runner bridges
// it to the assigned turn id on POST /turns (runner/src/trace.ts traceTurnLink)
// so merged CLI+pod logs pivot between the CLI's connect spans and the runner's
// turn spans by either id; other routes currently ignore it. Empty (the
// default, and what a disabled tracer yields) sends no header. Set once right
// after New, before the client is shared across goroutines — it is not
// synchronized for later mutation.
func (c *Client) SetTraceID(id string) { c.traceID = id }

// healthResponse mirrors the runner's GET /healthz body (runner/src/server.ts
// healthzBody). protocolVersion is absent on a pre-handshake runner image, in
// which case it decodes to the zero value (0) — treated as "unknown/old" by
// ProtocolVersion's callers.
type healthResponse struct {
	Status          string `json:"status"`
	ProtocolVersion int    `json:"protocolVersion"`
}

// Health checks /healthz and records the runner's reported protocol version
// (see ProtocolVersion) for mismatch detection by the caller. A decode
// failure of the (already 200-OK) body does not fail Health itself — the
// runner is up, it just didn't (or couldn't) report a version.
func (c *Client) Health(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/healthz", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError(resp, "runner health")
	}
	var h healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err == nil {
		atomic.StoreInt32(&c.remoteProtocolVersion, int32(h.ProtocolVersion))
	}
	return nil
}

// ProtocolVersion returns the runner's protocolVersion as last reported by a
// successful Health call, or 0 if Health has not succeeded yet or the runner
// predates the protocolVersion field (an old runner image — OSS users build
// and push their own, so CLI/runner skew is the steady state, not an edge
// case). Compare against session.ProtocolVersion to detect skew; callers warn
// rather than refuse (see client/session.go Connect and
// internal/cli/connect.go waitHealthy).
func (c *Client) ProtocolVersion() int {
	return int(atomic.LoadInt32(&c.remoteProtocolVersion))
}

// ProtocolMismatchWarning returns a human-readable advisory if remoteVersion
// (as reported by a runner's Health/ProtocolVersion) differs from this CLI's
// session.ProtocolVersion, or "" if they match. Centralized here (rather than
// duplicated per caller) so client.Session.Connect and the headless
// internal/cli commands (turn, trace — via waitHealthy) report identical
// wording. Deliberately advisory, not fatal: OSS users build and push their
// own runner images, so a skewed pair is the steady state, not an edge case.
// The wording must stay ACTIONABLE — it names both versions, the concrete
// failure mode (a renamed wire field decodes as empty against the wrong
// counterpart, e.g. the §8 v1→v2 status/claudeSession renames), and the fix
// (update the image + restart the pod, or re-create the session); pinned by
// client/session_protocol_version_test.go.
func ProtocolMismatchWarning(remoteVersion int) string {
	if remoteVersion == session.ProtocolVersion {
		return ""
	}
	if remoteVersion == 0 {
		return fmt.Sprintf("runner did not report a protocol version (old runner image, pre-dates the handshake); this CLI expects protocol v%d and renamed wire fields may silently decode as empty — update the runner image, then suspend/resume the session to restart its pod, or destroy and re-create the session", session.ProtocolVersion)
	}
	return fmt.Sprintf("CLI/runner protocol version mismatch: runner speaks v%d, this CLI expects v%d — renamed wire fields may silently decode as empty; update the runner image, then suspend/resume the session to restart its pod, or destroy and re-create the session", remoteVersion, session.ProtocolVersion)
}

// StartTurn POSTs a new turn to the runner.
func (c *Client) StartTurn(ctx context.Context, ref session.Ref, input session.TurnInput) (session.TurnRef, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return session.TurnRef{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/sessions/%s/turns", ref.ID), body)
	if err != nil {
		return session.TurnRef{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return session.TurnRef{}, statusError(resp, "runner start turn")
	}
	var result struct {
		TurnID session.TurnID `json:"turnId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return session.TurnRef{}, fmt.Errorf("runner start turn: decode: %w", err)
	}
	return session.TurnRef{Session: ref.ID, Turn: result.TurnID}, nil
}

// InterruptTurn cancels an active turn.
func (c *Client) InterruptTurn(ctx context.Context, ref session.Ref, turn session.TurnRef) error {
	u := fmt.Sprintf("/sessions/%s/turns/%s/interrupt", ref.ID, turn.Turn)
	resp, err := c.do(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return statusError(resp, "runner interrupt turn")
	}
	return nil
}

// ResolvePermission sends a permission decision.
func (c *Client) ResolvePermission(ctx context.Context, ref session.Ref, decision session.PermissionDecision) error {
	body, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("/sessions/%s/permissions/%s", ref.ID, decision.Permission)
	resp, err := c.do(ctx, http.MethodPost, u, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return statusError(resp, "runner resolve permission")
	}
	return nil
}

// SessionState fetches the runner's session.json state.
func (c *Client) SessionState(ctx context.Context, ref session.Ref) (session.State, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/sessions/%s/status", ref.ID), nil)
	if err != nil {
		return session.State{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return session.State{}, statusError(resp, "runner session state")
	}
	var st session.State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return session.State{}, fmt.Errorf("runner session state: decode: %w", err)
	}
	return st, nil
}

// Exec runs a one-shot shell command in the session cwd via POST
// /sessions/:id/exec and returns the captured (bounded) output.
func (c *Client) Exec(ctx context.Context, ref session.Ref, command string) (session.ExecResult, error) {
	body, err := json.Marshal(struct {
		Command string `json:"command"`
	}{Command: command})
	if err != nil {
		return session.ExecResult{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/sessions/%s/exec", ref.ID), body)
	if err != nil {
		return session.ExecResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return session.ExecResult{}, statusError(resp, "runner exec")
	}
	var result session.ExecResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return session.ExecResult{}, fmt.Errorf("runner exec: decode: %w", err)
	}
	return result, nil
}

// Idle fetches the runner's idle state (turn-done AND detached) for the reaper.
//
// Idle is intentionally NOT part of session.RunnerClient or
// dashboard.RunnerClient: it is consumed only by the idle reaper
// (internal/cli/reap.go) through the concrete *runner.Client type, so it stays
// off the interfaces the TUI/dashboard depend on.
func (c *Client) Idle(ctx context.Context, ref session.Ref) (session.IdleStatus, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/sessions/%s/idle", ref.ID), nil)
	if err != nil {
		return session.IdleStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return session.IdleStatus{}, statusError(resp, "runner idle")
	}
	var st session.IdleStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return session.IdleStatus{}, fmt.Errorf("runner idle: decode: %w", err)
	}
	return st, nil
}

// ErrAutopilotUnsupported is returned by ArmAutopilot/DisarmAutopilot when the
// backend has no runner-side autopilot driver (the runner answers 409). The TUI
// treats it as the signal to fall back to its local tea.Tick driver.
var ErrAutopilotUnsupported = errors.New("runner: backend has no autopilot driver")

// ErrAutopilotNotArmed is returned by DisarmAutopilot when there is no spec to
// disarm (the runner answers 404). Idempotent callers can treat it as success.
var ErrAutopilotNotArmed = errors.New("runner: no autopilot spec to disarm")

// ArmAutopilot arms (or replaces) the runner-owned autopilot driver via
// PUT /sessions/:id/autopilot (the server-side /loop-/goal loop; see
// docs/archive/server-side-loop-adr.md). It returns the runner's /status body reflecting
// the now-armed driver. A backend without a runner driver answers 409, mapped to
// ErrAutopilotUnsupported so the caller can fall back to its local driver; a
// malformed request answers 400, folded into the returned error by statusError.
func (c *Client) ArmAutopilot(ctx context.Context, ref session.Ref, req session.AutopilotRequest) (session.State, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return session.State{}, err
	}
	resp, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/sessions/%s/autopilot", ref.ID), body)
	if err != nil {
		return session.State{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusConflict {
			return session.State{}, fmt.Errorf("%w (%v)", ErrAutopilotUnsupported, statusError(resp, "runner arm autopilot"))
		}
		return session.State{}, statusError(resp, "runner arm autopilot")
	}
	var st session.State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return session.State{}, fmt.Errorf("runner arm autopilot: decode: %w", err)
	}
	return st, nil
}

// DisarmAutopilot disarms the runner-owned driver via
// DELETE /sessions/:id/autopilot: the runner sets the persisted spec to
// state:"stopped" (reason "user") and bumps gen. It returns the runner's /status
// body. A never-armed driver answers 404, mapped to ErrAutopilotNotArmed so an
// idempotent caller can treat it as already-disarmed; a backend without a runner
// driver answers 409, mapped to ErrAutopilotUnsupported.
func (c *Client) DisarmAutopilot(ctx context.Context, ref session.Ref) (session.State, error) {
	resp, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/sessions/%s/autopilot", ref.ID), nil)
	if err != nil {
		return session.State{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusNotFound:
			return session.State{}, fmt.Errorf("%w (%v)", ErrAutopilotNotArmed, statusError(resp, "runner disarm autopilot"))
		case http.StatusConflict:
			return session.State{}, fmt.Errorf("%w (%v)", ErrAutopilotUnsupported, statusError(resp, "runner disarm autopilot"))
		}
		return session.State{}, statusError(resp, "runner disarm autopilot")
	}
	var st session.State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return session.State{}, fmt.Errorf("runner disarm autopilot: decode: %w", err)
	}
	return st, nil
}

// Events opens an SSE stream of events after the given sequence number for a
// real (active) client — one that keeps the session "attached" for idle
// detection. The channel closes when the stream ends or ctx is cancelled.
func (c *Client) Events(ctx context.Context, ref session.Ref, afterSeq uint64) (<-chan session.Event, error) {
	return c.events(ctx, ref, afterSeq, false)
}

// EventsPassive opens an SSE stream as a passive status observer: it receives
// the same events but does NOT count as an attached client on the runner, so it
// cannot keep the idle reaper from suspending the session. Used by the dashboard
// for background list-status streams (RV6).
func (c *Client) EventsPassive(ctx context.Context, ref session.Ref, afterSeq uint64) (<-chan session.Event, error) {
	return c.events(ctx, ref, afterSeq, true)
}

func (c *Client) events(ctx context.Context, ref session.Ref, afterSeq uint64, passive bool) (<-chan session.Event, error) {
	u := fmt.Sprintf("/sessions/%s/events?after=%d", ref.ID, afterSeq)
	if passive {
		u += "&passive=1"
	}

	// Capture the tunable package vars once, synchronously in the caller's
	// goroutine, so the stream and watchdog goroutines read these locals and
	// never the globals. Tests mutate the globals (and restore them in a defer);
	// reading locals keeps a lingering goroutine from a previous Events() call
	// from racing that restore under -race.
	readTimeout := sseReadTimeout
	maxLine := sseMaxLineBytes

	// Use a client with no timeout for SSE streaming.
	streamClient := &http.Client{Timeout: 0}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+u, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("runner events: connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		err := statusError(resp, "runner events")
		resp.Body.Close()
		return nil, err
	}

	events := make(chan session.Event, 64)

	// §1d: decouple socket reading from consumer draining. A stalled consumer
	// must NOT be able to manufacture a reconnect. Previously the scanner sent
	// decoded events straight to `events`; when the TUI stalled, that buffered
	// channel filled, the scanner blocked mid-send, and it therefore stopped
	// calling Scan() — starving the read watchdog below, which then force-closed
	// a perfectly live stream (reconnect+replay) purely from consumer
	// backpressure. Now a dedicated scanner goroutine reads the socket as fast as
	// the server sends and hands events to a forwarder over `decoded`; the
	// forwarder is the ONLY thing that ever blocks on the (possibly slow)
	// consumer. That keeps the watchdog's liveness a measure of SERVER activity
	// (bytes read off the socket), not consumer consumption.
	decoded := make(chan session.Event)

	// Forwarder: drain `decoded` into the consumer's `events` channel through an
	// internal FIFO queue that grows as needed, so the scanner never blocks on a
	// slow consumer. The queue is bounded in practice by the consumer eventually
	// draining; a consumer that never drains at all is a distinct failure mode a
	// reconnect wouldn't fix either — and we drop no events, preserving the
	// after=<seq> contiguity the replay path relies on.
	go func() {
		defer close(events)
		var queue []session.Event
		in := decoded // nil'd once the scanner closes `decoded`, to stop selecting it
		for in != nil || len(queue) > 0 {
			if len(queue) == 0 {
				// Nothing to deliver yet — only wait for more input (or cancel).
				select {
				case ev, ok := <-in:
					if !ok {
						in = nil
						continue
					}
					queue = append(queue, ev)
				case <-ctx.Done():
					return
				}
				continue
			}
			// Race delivering the head against accepting more, so a slow
			// consumer parks on `events <- queue[0]` while the scanner keeps
			// feeding `decoded` into the queue unabated.
			select {
			case ev, ok := <-in:
				if !ok {
					in = nil
					continue
				}
				queue = append(queue, ev)
			case events <- queue[0]:
				queue = queue[1:]
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer resp.Body.Close()
		defer close(decoded)

		// R6/§1d: Half-open connection watchdog. scanner.Scan() blocks forever on
		// a half-open TCP connection (pod hard-loss, no FIN/RST). The server sends
		// ': heartbeat\n\n' every 30s (R5); if the scanner reads nothing for
		// sseReadTimeout the connection is dead and we close the body to unblock
		// the scanner. Liveness (lastRead) is keyed on the scanner's socket reads,
		// i.e. server activity — with the forwarder above absorbing consumer
		// backpressure, the scanner keeps reading regardless of how slowly the
		// consumer drains, so a slow consumer can no longer starve this signal.
		// (Package var so tests can shorten it.)
		lastRead := make(chan struct{}, 1)
		// M11: close lastRead exactly once on any exit path so the watchdog
		// always unblocks. Replaces two hand-placed closes that were correct but
		// double-close-prone under refactoring.
		defer close(lastRead)
		go func() {
			timer := time.NewTimer(readTimeout)
			defer timer.Stop()
			for {
				select {
				case <-timer.C:
					// No data in sseReadTimeout — force-close the body to unblock Scan().
					resp.Body.Close()
					return
				case _, ok := <-lastRead:
					if !ok {
						return // scanner goroutine exited
					}
					// Received data; reset the timer.
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(readTimeout)
				case <-ctx.Done():
					return
				}
			}
		}()

		// This scanner deliberately relies on the runner's single-line-per-event
		// SSE framing: each event is exactly one `data: <json>\n\n` frame. We
		// parse one `data: ` line into one event and do NOT accumulate
		// continuation `data:` lines, so multi-line `data:` frames (valid per the
		// SSE spec but never emitted by our runner) are NOT handled. If the runner
		// ever splits an event across multiple data: lines, this loop must change.
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
		for scanner.Scan() {
			// Signal the watchdog that we received data (any SSE line).
			select {
			case lastRead <- struct{}{}:
			default:
			}
			// scanner.Bytes() returns a slice into the scanner's internal buffer that
			// is only valid until the next Scan; we fully consume it in this iteration
			// (json.Unmarshal copies into ev.Payload's json.RawMessage, and the
			// prefix checks don't retain it), so no copy is needed here (§4 E8) — the
			// old scanner.Text() + []byte(data) allocated the line twice per event.
			line := scanner.Bytes()
			// Replay/live boundary (Workstream C): the runner writes this comment
			// once it has replayed all history to us. Surface it as a client-internal
			// stream.live marker so the TUI flips out of "loading transcript…" into
			// the live tail. It is not a persisted event (no seq), so it bypasses the
			// data: decode path below.
			if bytes.HasPrefix(line, []byte(": replay-complete")) {
				select {
				case decoded <- session.Event{Type: session.EventStreamLive, SessionID: ref.ID}:
				case <-ctx.Done():
					return
				}
				continue
			}
			data, ok := bytes.CutPrefix(line, []byte("data: "))
			if !ok {
				continue
			}
			var ev session.Event
			if err := json.Unmarshal(data, &ev); err != nil {
				continue // skip malformed events
			}
			select {
			case decoded <- ev:
			case <-ctx.Done():
				return
			}
		}
		// C4: don't let a stream error vanish. A clean EOF and an intentional
		// ctx-cancel both report no error (ctx.Err() set), and a transient
		// network drop closes the channel → the TUI reconnects with replay and
		// shows "[connection lost]". The one case that is both silent AND
		// self-perpetuating is an event larger than sseMaxLineBytes: reconnect
		// re-fetches the same oversized event forever. Surface that as a visible
		// error event so the user knows the transcript is incomplete.
		if err := scanner.Err(); err != nil && ctx.Err() == nil && errors.Is(err, bufio.ErrTooLong) {
			payload, _ := json.Marshal(session.ErrorPayload{
				Message: fmt.Sprintf("event stream: a single event exceeded the %d-byte limit and was dropped; the transcript may be incomplete", maxLine),
			})
			select {
			case decoded <- session.Event{Type: session.EventError, SessionID: ref.ID, Payload: payload}:
			case <-ctx.Done():
			}
		}
	}()

	return events, nil
}

// errBodyLimit bounds how much of a non-2xx response body we read when building
// an error message. Runner errors are small JSON ({"error":"..."}); the cap
// guards against a misbehaving/proxy response streaming megabytes into our error
// string.
const errBodyLimit = 8 * 1024

// statusError builds an error for a non-2xx response. It reads a bounded prefix
// of resp.Body, tries to decode the runner's {"error":string} envelope, and
// folds the server's message into the returned error so callers (and bug
// reports) see *why* the request failed instead of an opaque "status 409". The
// prefix is "<op>: status <code>", e.g. statusError(resp, "runner start turn").
// resp.Body is NOT closed here — the caller still owns it.
func statusError(resp *http.Response, op string) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
	msg := serverErrorMessage(data)
	if msg == "" {
		return fmt.Errorf("%s: status %d", op, resp.StatusCode)
	}
	return fmt.Errorf("%s: status %d: %s", op, resp.StatusCode, msg)
}

// serverErrorMessage extracts a human-readable message from a runner error
// body. It prefers the {"error":string} envelope; failing that (non-JSON body,
// or JSON without an "error" string) it falls back to the trimmed raw body so a
// plain-text 502 from an intermediary is still surfaced. Returns "" when the
// body is empty/whitespace.
func serverErrorMessage(data []byte) string {
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &env); err == nil && env.Error != "" {
		return env.Error
	}
	return strings.TrimSpace(string(data))
}

// do is the internal HTTP request helper that adds the bearer token.
func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.traceID != "" {
		req.Header.Set("X-Sandbox-Trace-Id", c.traceID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// Ensure the Client satisfies the interface.
var _ session.RunnerClient = (*Client)(nil)

// *Client must also satisfy the wider dashboard.RunnerClient surface (a
// structural superset of session.RunnerClient that adds EventsPassive for RV6
// status-observer streams). That compile-time assertion lives in internal/cli
// (which already imports both packages) so the TUI dependency tree is not pulled
// into this lightweight HTTP client.

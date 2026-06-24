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
	http    *http.Client
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

// Health checks /healthz.
func (c *Client) Health(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/healthz", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError(resp, "runner health")
	}
	return nil
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
	go func() {
		defer resp.Body.Close()
		defer close(events)

		// R6: Half-open connection watchdog. scanner.Scan() blocks forever on a
		// half-open TCP connection (pod hard-loss, no FIN/RST). The server now
		// sends ': heartbeat\n\n' every 30s (R5); if we receive nothing for
		// sseReadTimeout the connection is dead and we close the body to unblock
		// the scanner. (Package var so tests can shorten it.)
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
					// No data in 90s — force-close the body to unblock Scan().
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
			line := scanner.Text()
			// Replay/live boundary (Workstream C): the runner writes this comment
			// once it has replayed all history to us. Surface it as a client-internal
			// stream.live marker so the TUI flips out of "loading transcript…" into
			// the live tail. It is not a persisted event (no seq), so it bypasses the
			// data: decode path below.
			if strings.HasPrefix(line, ": replay-complete") {
				select {
				case events <- session.Event{Type: session.EventStreamLive, SessionID: ref.ID}:
				case <-ctx.Done():
					return
				}
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			var ev session.Event
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue // skip malformed events
			}
			select {
			case events <- ev:
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
			case events <- session.Event{Type: session.EventError, SessionID: ref.ID, Payload: payload}:
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

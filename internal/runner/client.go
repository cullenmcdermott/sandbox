// Package runner is the Go HTTP client for the sandbox-claude-runner API.
// It implements session.RunerClient over a port-forward to the runner pod.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Client implements session.RunnerClient over HTTP.
type Client struct {
	baseURL string    // e.g. "http://127.0.0.1:8787"
	token   string    // bearer token
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
		return fmt.Errorf("runner health: status %d", resp.StatusCode)
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
		return session.TurnRef{}, fmt.Errorf("runner start turn: status %d", resp.StatusCode)
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
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("runner interrupt turn: status %d", resp.StatusCode)
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
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("runner resolve permission: status %d", resp.StatusCode)
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
		return session.State{}, fmt.Errorf("runner session state: status %d", resp.StatusCode)
	}
	var st session.State
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return session.State{}, fmt.Errorf("runner session state: decode: %w", err)
	}
	return st, nil
}

// Events opens an SSE stream of events after the given sequence number.
// The channel closes when the stream ends or ctx is cancelled.
func (c *Client) Events(ctx context.Context, ref session.Ref, afterSeq uint64) (<-chan session.Event, error) {
	u := fmt.Sprintf("/sessions/%s/events?after=%d", ref.ID, afterSeq)

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
		resp.Body.Close()
		return nil, fmt.Errorf("runner events: status %d", resp.StatusCode)
	}

	events := make(chan session.Event, 64)
	go func() {
		defer resp.Body.Close()
		defer close(events)

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
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
	}()

	return events, nil
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

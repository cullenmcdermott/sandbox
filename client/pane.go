package client

import (
	"context"
	"io"

	"github.com/cullenmcdermott/sandbox/internal/runner"
)

// PaneStream is the interactive PTY byte stream of a claude-pane session
// (docs/runner-api.md `GET /sessions/:id/pane`): Read yields raw terminal
// output (scrollback replay first, then live), Write sends keyboard/paste
// input verbatim, and Resize propagates the client terminal geometry to the
// remote PTY (SIGWINCH). Closing the stream detaches — the remote interactive
// child keeps running, and a later attach replays the retained scrollback.
//
// Read must be driven from a single goroutine; Write/Resize/Close may run
// concurrently with it.
type PaneStream interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

// Pane attach sentinel errors for errors.Is: a Read fails with
// ErrPanePreempted when a newer pane attach took the session over (at most one
// pane drives the PTY), and with ErrPaneChildExited when the interactive
// claude process ended — the next AttachPane respawns it, resuming the same
// conversation.
var (
	ErrPanePreempted   = runner.ErrPanePreempted
	ErrPaneChildExited = runner.ErrPaneChildExited
)

// AttachPane attaches to a claude-pane session's interactive PTY over the
// existing runner port-forward (no extra forward spec). Requires a prior
// successful Connect. A positive cols/rows sends the initial resize control
// frame with the attach so the remote PTY adopts the client geometry
// immediately; pass 0,0 to keep the runner's current geometry.
//
// Only the claude-pane backend serves the endpoint; every other backend
// rejects the attach with a 409, surfaced as an error here.
//
// The attach is gated on the session's background first-sync staging, the same
// way the turn gate (stagedRunner) gates StartTurn: the runner spawns the
// interactive child LAZILY on the first attach, so this call — not a turn — is
// what starts an agent in the workspace for a pane session. A first-ever sync
// whose transport broke leaves that workspace EMPTY, so the attach is refused
// with ErrInitialSyncFailed rather than booting claude into nothing. A
// slow-but-healthy upload is only an advisory and does not block the attach.
//
// The gate observes ctx: since the wait can run as long as the session's
// background sync phase, a caller whose ctx budget is sized for the dial alone
// should AwaitSync under its own budget first (see internal/cli mapPaneDial) —
// this gate then returns immediately.
func (s *Session) AttachPane(ctx context.Context, cols, rows int) (PaneStream, error) {
	s.mu.Lock()
	rc := s.runner
	s.mu.Unlock()
	if rc == nil {
		return nil, ErrNotConnected
	}
	if _, err := s.AwaitSync(ctx); err != nil {
		return nil, err
	}
	return rc.AttachPane(ctx, s.ref, cols, rows)
}

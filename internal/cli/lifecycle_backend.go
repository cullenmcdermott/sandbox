package cli

import (
	"context"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// clientLifecycleBackend adapts the client SDK's session-lifecycle methods to
// the dashboard.Backend interface (§1g). List/Watch are promoted from the
// embedded concrete *k8s.Backend (the read-model seed + cluster watch);
// Suspend/Resume/Destroy route through *client.Client so a TUI keystroke gets
// the SAME behavior as the `suspend`/`resume`/`destroy` subcommands. Calling the
// raw backend directly would skip sync pause/resume and — for destroy — skip
// both the sync teardown-before-delete ordering and the per-session worktree WIP
// capture, silently losing uncommitted work.
type clientLifecycleBackend struct {
	*k8s.Backend
	c *client.Client
}

// newClientLifecycleBackend wraps the shared *k8s.Backend (for List/Watch) with
// the client that drives the lifecycle actions.
func newClientLifecycleBackend(c *client.Client, b *k8s.Backend) *clientLifecycleBackend {
	return &clientLifecycleBackend{Backend: b, c: c}
}

// Suspend routes through the client so file sync is paused alongside the pod.
func (b *clientLifecycleBackend) Suspend(ctx context.Context, ref session.Ref) error {
	return b.c.Suspend(ctx, ref.ID)
}

// Resume routes through the client so file sync is resumed alongside the pod.
func (b *clientLifecycleBackend) Resume(ctx context.Context, ref session.Ref) error {
	return b.c.Resume(ctx, ref.ID)
}

// Destroy routes through the client so sync is stopped BEFORE the cluster delete
// (avoiding mutagen-over-SSH EOF races), the per-session worktree's WIP is
// captured, and local state is removed — none of which the raw backend does.
func (b *clientLifecycleBackend) Destroy(ctx context.Context, ref session.Ref) error {
	return b.c.Destroy(ctx, ref.ID)
}

// Compile-time assertion that the adapter satisfies the dashboard's Backend
// contract (the interface the dashboard seeds, watches, and acts through).
var _ dashboard.Backend = (*clientLifecycleBackend)(nil)

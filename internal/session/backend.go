package session

import "context"

// Backend manages the lifecycle of remote agent sessions. It is the
// session-oriented equivalent of the existing lima VM Backend, designed for
// Kubernetes-based sessions where the pod/PVC is the source of truth.
//
// The interface intentionally separates lifecycle (Create/Start/Suspend/...)
// from transport (PortForward/RunnerClient). CLI commands compose these:
// `sandbox claude` calls Create + Start + PortForward, then opens the TUI
// which uses RunnerClient for turns and events.
type Backend interface {
	// CreateSession creates the Sandbox + PVC in Kubernetes but does not
	// wait for the pod to be ready. Use Start to wait for readiness.
	CreateSession(ctx context.Context, spec Spec) (Ref, error)

	// Start waits for the session pod to be running and ready. If the
	// session is suspended, this is equivalent to Resume.
	Start(ctx context.Context, ref Ref) error

	// Suspend sets operatingMode to Suspended. The pod is terminated but
	// the PVC and session.json survive.
	Suspend(ctx context.Context, ref Ref) error

	// Resume sets operatingMode back to Running and waits for the pod.
	Resume(ctx context.Context, ref Ref) error

	// Destroy deletes the Sandbox and PVC. Irreversible.
	Destroy(ctx context.Context, ref Ref) error

	// Status returns the current observed state of the session.
	Status(ctx context.Context, ref Ref) (State, error)

	// PortForward establishes a kubectl port-forward to the runner pod's
	// HTTP and SSH ports. Returns a handle for each.
	PortForward(ctx context.Context, ref Ref, ports []PortSpec) ([]ForwardHandle, error)

	// List returns all known sessions in the namespace.
	List(ctx context.Context) ([]State, error)
}

// RunnerClient is the HTTP client for the runner pod's API. It is separate
// from Backend so the TUI can use it directly over an established
// port-forward without re-deriving the session.
type RunnerClient interface {
	// Health checks /healthz.
	Health(ctx context.Context) error

	// StartTurn POSTs a new turn to the runner.
	StartTurn(ctx context.Context, ref Ref, input TurnInput) (TurnRef, error)

	// InterruptTurn cancels an active turn.
	InterruptTurn(ctx context.Context, ref Ref, turn TurnRef) error

	// ResolvePermission sends a permission decision.
	ResolvePermission(ctx context.Context, ref Ref, decision PermissionDecision) error

	// Events opens an SSE stream of events after the given sequence number.
	// The channel closes when the stream ends or ctx is cancelled.
	Events(ctx context.Context, ref Ref, afterSeq uint64) (<-chan Event, error)

	// SessionState fetches the runner's session.json state.
	SessionState(ctx context.Context, ref Ref) (State, error)

	// Exec runs a one-shot shell command in the session cwd and returns its
	// captured (bounded) output. No persisted cd/env between calls.
	Exec(ctx context.Context, ref Ref, command string) (ExecResult, error)
}

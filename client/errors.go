package client

import (
	"errors"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Typed errors callers can branch on with errors.Is. They are the public,
// stable error vocabulary for the package — the idiomatic Go alternative to a
// subprocess JSON error-code envelope.
var (
	// ErrSessionGone reports that a session no longer exists in the cluster (its
	// Sandbox CRD was deleted). It is permanent and non-retryable. Returned by
	// Connect against a gone session; re-exported from the session model so
	// errors.Is works across the seam.
	ErrSessionGone = session.ErrSessionGone

	// ErrNoActiveTurn is returned by Session.CancelTurn when there is no turn in
	// flight to interrupt.
	ErrNoActiveTurn = errors.New("sandbox: no active turn")

	// ErrSessionSuspended is returned by an Observer Connect against a suspended
	// session: observer connects are read-only and never resume (the pod is gone,
	// so there is nothing to observe). Resume the session or use a full Connect.
	ErrSessionSuspended = errors.New("sandbox: session is suspended")

	// ErrProjectPathRequired is returned by Create when CreateOptions.ProjectPath
	// is empty. The project path is the absolute workspace path mirrored into the
	// pod; the library does not assume a current working directory.
	ErrProjectPathRequired = errors.New("sandbox: CreateOptions.ProjectPath is required")

	// ErrNotConnected is returned by the Session turn/stream convenience methods
	// when called before a successful Connect.
	ErrNotConnected = errors.New("sandbox: session not connected (call Connect first)")

	// ErrInvalidImagePullPolicy is returned when an image pull-policy override
	// (CreateOptions.ImagePullPolicy, WithReaperImagePullPolicy, or
	// ConnectOptions.ReaperImagePullPolicy) is a non-empty value other than the
	// exact spellings "Always", "IfNotPresent", or "Never".
	ErrInvalidImagePullPolicy = errors.New("sandbox: invalid image pull policy")

	// ErrInvalidAnthropicAuth is returned by Create when CreateOptions.AnthropicAuth
	// is a non-empty value other than the exact spellings "oauth" or "api-key".
	// A typo like "apikey" errors here rather than silently falling through to the
	// default OAuth path.
	ErrInvalidAnthropicAuth = errors.New("sandbox: invalid anthropic auth")
)

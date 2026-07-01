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

	// ErrProjectPathRequired is returned by Create when CreateOptions.ProjectPath
	// is empty. The project path is the absolute workspace path mirrored into the
	// pod; the library does not assume a current working directory.
	ErrProjectPathRequired = errors.New("sandbox: CreateOptions.ProjectPath is required")

	// ErrNotConnected is returned by the Session turn/stream convenience methods
	// when called before a successful Connect.
	ErrNotConnected = errors.New("sandbox: session not connected (call Connect first)")

	// ErrInvalidImagePullPolicy is returned by Create when CreateOptions.ImagePullPolicy
	// is a non-empty value other than the exact spellings "Always", "IfNotPresent",
	// or "Never".
	ErrInvalidImagePullPolicy = errors.New("sandbox: invalid image pull policy")
)

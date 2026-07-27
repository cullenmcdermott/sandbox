package client

import (
	"context"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// offlineBackend keeps Client structurally valid while making accidental
// cluster use fail predictably instead of panicking on a nil backend.
type offlineBackend struct{ namespace string }

func (b offlineBackend) Namespace() string {
	if b.namespace == "" {
		return "agent-sessions"
	}
	return b.namespace
}

func (offlineBackend) CreateSession(context.Context, Spec) (Ref, error) { return Ref{}, ErrOffline }
func (offlineBackend) Status(context.Context, Ref) (State, error)       { return State{}, ErrOffline }
func (offlineBackend) List(context.Context) ([]State, error)            { return nil, ErrOffline }
func (offlineBackend) Watch(context.Context) (<-chan StateEvent, error) { return nil, ErrOffline }
func (offlineBackend) Suspend(context.Context, Ref) error               { return ErrOffline }
func (offlineBackend) Resume(context.Context, Ref, ResumeOptions) error { return ErrOffline }
func (offlineBackend) Destroy(context.Context, Ref) error               { return ErrOffline }
func (offlineBackend) StartWithProgress(context.Context, Ref, func(string)) error {
	return ErrOffline
}
func (offlineBackend) PortForward(context.Context, Ref, []session.PortSpec) ([]session.ForwardHandle, error) {
	return nil, ErrOffline
}
func (offlineBackend) RunnerToken(context.Context, Ref) (string, error) {
	return "", ErrOffline
}
func (offlineBackend) OpencodePassword(context.Context, Ref) (string, error) {
	return "", ErrOffline
}
func (offlineBackend) EnsureReaper(context.Context, Ref, k8s.ReaperOptions) error {
	return ErrOffline
}

var _ Backend = offlineBackend{}

package cli

// offline.go — client construction for the commands that never touch the
// cluster.
//
// `sandbox worktree path` and shell completion resolve sessions from the LOCAL
// index only (client.ResolveSessions reads ~/.local/share/sandbox/…, nothing
// else). Both document that as load-bearing: `cd $(sandbox worktree path …)`
// must work on a plane, and a TAB press must never wait on an apiserver.
//
// They were nonetheless built with newClient(), which calls client.New →
// k8s.New → kubeconfig resolution, and that fails with "failed to connect to
// cluster" when no KUBECONFIG is set — before a single line of the index is
// read. Outside the flox shell (which exports one) `worktree path` was simply
// broken, and completion failed the same way but SILENTLY: a completion
// function that errors is a completion that offers nothing, so TAB just went
// dead with no clue why.
//
// The contract was right; only the construction was wrong. newOfflineClient
// builds the same *client.Client with a backend that resolves no kubeconfig and
// refuses every cluster call, so the index-only paths cannot depend on cluster
// reachability by accident — and a future edit that reaches for a cluster
// operation from one of them fails loudly and immediately, rather than quietly
// reintroducing the dependency.

import (
	"context"
	"errors"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// errOffline is what every cluster operation on an offlineBackend returns. It
// names the cause rather than the symptom: reaching this error means an
// index-only command grew a cluster dependency, which is a bug in the caller,
// not a misconfiguration on the user's machine.
var errOffline = errors.New("this command resolves sessions from the local index only and cannot reach the cluster")

// newOfflineClient builds a client for the index-only commands. It never
// resolves a kubeconfig, so it cannot fail for want of one — the only error it
// can return is a state-directory failure, which would break local resolution
// anyway.
func newOfflineClient() (*client.Client, error) {
	return client.New(
		client.WithNamespace(namespaceFlag),
		client.WithBackend(offlineBackend{}),
	)
}

// offlineBackend satisfies client.Backend without a cluster connection. Every
// method is a refusal; none is reachable from ResolveSessions, which touches
// only the local index.
type offlineBackend struct{}

var _ client.Backend = offlineBackend{}

func (offlineBackend) Namespace() string { return namespaceFlag }

func (offlineBackend) CreateSession(context.Context, client.Spec) (client.Ref, error) {
	return client.Ref{}, errOffline
}

func (offlineBackend) Status(context.Context, client.Ref) (client.State, error) {
	return client.State{}, errOffline
}

func (offlineBackend) List(context.Context) ([]client.State, error) { return nil, errOffline }

func (offlineBackend) Watch(context.Context) (<-chan client.StateEvent, error) {
	return nil, errOffline
}

func (offlineBackend) Suspend(context.Context, client.Ref) error { return errOffline }

func (offlineBackend) Resume(context.Context, client.Ref, client.ResumeOptions) error {
	return errOffline
}

func (offlineBackend) Destroy(context.Context, client.Ref) error { return errOffline }

func (offlineBackend) StartWithProgress(context.Context, client.Ref, func(string)) error {
	return errOffline
}

func (offlineBackend) PortForward(context.Context, client.Ref, []session.PortSpec) ([]session.ForwardHandle, error) {
	return nil, errOffline
}

func (offlineBackend) RunnerToken(context.Context, client.Ref) (string, error) {
	return "", errOffline
}

func (offlineBackend) OpencodePassword(context.Context, client.Ref) (string, error) {
	return "", errOffline
}

func (offlineBackend) EnsureReaper(context.Context, client.Ref, k8s.ReaperOptions) error {
	return errOffline
}

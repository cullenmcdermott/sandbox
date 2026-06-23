package dashboard

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Backend is the subset of *k8s.Backend the dashboard needs: seeding the
// read-model, watching the cluster, and the suspend/resume/destroy session
// actions. It is declared as an interface (rather than depending on the
// concrete *k8s.Backend) so unit tests can inject a fake and assert that a
// keystroke dispatches the right cluster call. *k8s.Backend satisfies it.
type Backend interface {
	List(ctx context.Context) ([]session.State, error)
	Watch(ctx context.Context) (<-chan k8s.StateEvent, error)
	Suspend(ctx context.Context, ref session.Ref) error
	Resume(ctx context.Context, ref session.Ref) error
	Destroy(ctx context.Context, ref session.Ref) error
}

// --------------------------------------------------------------------------
// Action messages
// --------------------------------------------------------------------------

// createSessionMsg is emitted by the dashboard when the user presses `n`. The
// root App handles it (it owns the Creator) by switching to the connecting
// screen and provisioning a new session, then attaching to it.
type createSessionMsg struct{}

// actionResultMsg reports the outcome of a suspend/resume/destroy action. The
// cluster watch is the source of truth for the resulting status change; this
// message exists only to surface failures in the detail pane.
type actionResultMsg struct {
	action string
	id     session.ID
	err    error
}

// --------------------------------------------------------------------------
// Confirmation dialog
// --------------------------------------------------------------------------

// confirmPrompt is an active destructive-action confirmation. While it is set
// the dashboard renders a centered y/n dialog and routes keys to it: y runs
// action, n/esc cancels. Only destroy (`!`) gates behind a confirm — suspend
// and resume are recoverable and dispatch immediately.
type confirmPrompt struct {
	message string
	action  tea.Cmd
	id      session.ID // session being acted upon (for PendingAction)
}

// --------------------------------------------------------------------------
// Action commands
// --------------------------------------------------------------------------

// suspendCmd scales the session's pod to zero (replicas=0), preserving the PVC.
func (m *Model) suspendCmd(ref session.Ref) tea.Cmd {
	backend := m.backend
	if backend == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := backend.Suspend(ctx, ref)
		return actionResultMsg{action: "suspend", id: ref.ID, err: err}
	}
}

// resumeCmd scales the session's pod back to one and waits for it to be ready.
func (m *Model) resumeCmd(ref session.Ref) tea.Cmd {
	backend := m.backend
	if backend == nil {
		return nil
	}
	return func() tea.Msg {
		// Resume waits for the pod to become ready, which can take a while on
		// a cold node (RWO volume reattach), so the timeout is generous.
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		err := backend.Resume(ctx, ref)
		return actionResultMsg{action: "resume", id: ref.ID, err: err}
	}
}

// destroyCmd deletes the session's Sandbox and PVC. Irreversible; gated behind
// a confirm dialog at the call site. On success the destroyHook is called for
// local cleanup (C2: sync teardown, SSH alias removal, key deletion).
func (m *Model) destroyCmd(ref session.Ref) tea.Cmd {
	backend := m.backend
	if backend == nil {
		return nil
	}
	hook := m.destroyHook
	preHook := m.preDestroyHook
	return func() tea.Msg {
		// Stop file sync before tearing the pod down so the mutagen-over-SSH
		// stream doesn't race the pod's disappearance into "connection
		// closed"/EOF errors. Runs regardless of Destroy's outcome — it's
		// recoverable (a re-attach re-establishes sync).
		if preHook != nil {
			preHook(ref.ID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err := backend.Destroy(ctx, ref)
		if err == nil && hook != nil {
			// Best-effort irreversible local cleanup (C2): run outside the context
			// (which may have been cancelled by the 60s timeout in a slow cluster).
			hook(ref.ID)
		}
		return actionResultMsg{action: "destroy", id: ref.ID, err: err}
	}
}

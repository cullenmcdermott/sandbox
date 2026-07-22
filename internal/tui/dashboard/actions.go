package dashboard

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Backend is the subset of *k8s.Backend the dashboard needs: seeding the
// read-model, watching the cluster, and the suspend/resume/destroy session
// actions. It is declared as an interface (rather than depending on the
// concrete *k8s.Backend) so unit tests can inject a fake and assert that a
// keystroke dispatches the right cluster call. *k8s.Backend satisfies it.
type Backend interface {
	List(ctx context.Context) ([]session.State, error)
	Watch(ctx context.Context) (<-chan session.StateEvent, error)
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

// sessionTitle returns the display title of the session with the given id, or
// the raw id when it is not tracked (e.g. it already left the list). Used to
// label async action tasks in the statusline (§C1).
func (m *Model) sessionTitle(id session.ID) string {
	for i := range m.sessions {
		if m.sessions[i].ID() == id {
			return m.sessions[i].DisplayTitle()
		}
	}
	return string(id)
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
// a confirm dialog at the call site. Destroy routes through the client SDK
// adapter (internal/cli), which stops file sync BEFORE the cluster delete — so
// the mutagen-over-SSH stream is torn down while the pod is still up rather than
// racing its disappearance into "connection closed"/EOF errors — then captures
// the per-session worktree's WIP and removes local state. The dashboard only
// observes the outcome; the ordering guarantees live behind Backend.Destroy.
func (m *Model) destroyCmd(ref session.Ref) tea.Cmd {
	backend := m.backend
	if backend == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err := backend.Destroy(ctx, ref)
		return actionResultMsg{action: "destroy", id: ref.ID, err: err}
	}
}

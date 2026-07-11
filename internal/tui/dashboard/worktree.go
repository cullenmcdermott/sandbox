package dashboard

// worktree.go implements the TUI half of convert-to-branch (design
// docs/worktree-lifecycle-design.md §4.6, resolution 8): the `b` keymap gathers
// deterministic git facts through the WorktreeOps seam, opens an editable modal
// whose branch/message are DETERMINISTICALLY prefilled from the session title
// (already LLM-generated — there is no propose turn), and runs the approved
// strings through client.Session.ConvertToBranch off the Update goroutine. The
// dashboard never imports client: the seam and a small set of package-local
// sentinels keep the git/LLM plumbing on the CLI side.

import (
	"context"
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Convert-flow sentinels. The CLI adapter maps the client package's
// ErrNoWorktree / ErrBranchNameTaken / ErrWorktreeDirty onto these so the
// dashboard can branch on the failure kind (toast vs keep-modal-with-inline-
// error) without importing client.
var (
	// ErrNoWorktree signals the selected session has no per-session worktree
	// (non-git fallback / WorktreeOff), so `b` shows a toast rather than a modal.
	ErrNoWorktree = errors.New("dashboard: session has no worktree")
	// ErrBranchNameTaken signals the approved branch name already exists; the
	// modal stays open with an inline error so the user picks another.
	ErrBranchNameTaken = errors.New("dashboard: branch name already taken")
	// ErrWorktreeDirty signals a dirty worktree was submitted with an empty
	// commit message; the modal stays open with an inline error.
	ErrWorktreeDirty = errors.New("dashboard: commit message required")
)

// WorktreeOps is the dashboard's injected view of the per-session worktree git
// surface. It is intentionally narrow — exactly the two operations the
// convert-to-branch flow needs — and carries no LLM/proposal step (resolution
// 8): the deterministic prefill is derived from the session title in the
// dashboard, and Convert takes the already-approved strings. The concrete
// implementation (internal/cli) wraps client.Session.WorktreeStatus /
// ConvertToBranch; unit tests inject a fake.
type WorktreeOps interface {
	// Status returns the deterministic git facts for the session's worktree:
	// the current branch (sandbox/<id> until converted), whether it is dirty,
	// and the changed-file list. A session with no worktree returns an
	// ErrNoWorktree-shaped error.
	Status(ctx context.Context, id session.ID) (branch string, dirty bool, changed []string, err error)
	// Convert runs the deterministic git side of convert-to-branch with the
	// already-approved branch name and commit message, returning the final
	// branch and whether a commit was created. A taken name returns an
	// ErrBranchNameTaken-shaped error; a dirty worktree with an empty message
	// returns an ErrWorktreeDirty-shaped error.
	Convert(ctx context.Context, id session.ID, branchName, message string) (finalBranch string, committed bool, err error)
}

// WithWorktreeOps injects the convert-to-branch git surface. nil disables the
// `b` keymap (unit-test / library default — no worktree flow).
func (m *Model) WithWorktreeOps(ops WorktreeOps) *Model {
	m.worktreeOps = ops
	return m
}

// worktreeStatusMsg reports the result of the pre-modal Status probe fired by
// `b`. On success the convert modal opens with prefilled fields; on an
// ErrNoWorktree-shaped error a toast is shown instead.
type worktreeStatusMsg struct {
	id      session.ID
	branch  string
	dirty   bool
	changed int
	err     error
}

// convertResultMsg reports the outcome of a ConvertToBranch run.
type convertResultMsg struct {
	id     session.ID
	branch string
	err    error
}

// convertModal is the editable convert-to-branch prompt (design §4.6 step 3): a
// human-confirmation gate over the deterministic git rename. It shows the git
// facts from Status plus editable branch / commit-message fields.
type convertModal struct {
	id        session.ID
	branch    textinput.Model // editable target branch, prefilled from the title
	message   textinput.Model // editable commit message, prefilled from the title
	focus     int             // 0 = branch, 1 = message
	curBranch string          // the current (sandbox/<id>) branch, for the facts line
	dirty     bool
	changed   int
	inlineErr string // set on a keep-modal error (taken name, dirty/empty message)
	busy      bool   // a Convert is in flight
}

// worktreeStatusCmd probes the selected session's worktree facts off the Update
// goroutine so `b` never blocks on git.
func (m *Model) worktreeStatusCmd(id session.ID) tea.Cmd {
	ops := m.worktreeOps
	if ops == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		branch, dirty, changed, err := ops.Status(ctx, id)
		return worktreeStatusMsg{id: id, branch: branch, dirty: dirty, changed: len(changed), err: err}
	}
}

// openConvertModal handles the Status result: a no-worktree (or otherwise
// failed) probe shows a toast; a success opens the editable modal with the
// deterministic title-derived prefill.
func (m *Model) openConvertModal(msg worktreeStatusMsg) tea.Cmd {
	sess := m.sessionByID(msg.id)
	title := sess.DisplayTitle()
	if msg.err != nil {
		note := "no git worktree for this session"
		if !errors.Is(msg.err, ErrNoWorktree) {
			note = msg.err.Error()
		}
		return worktreeToastCmd(msg.id, title, note, StatusFailed)
	}
	branch, message := convertPrefill(title, string(msg.id), msg.branch)
	m.convert = newConvertModal(msg.id, branch, message, msg.branch, msg.dirty, msg.changed)
	return m.convert.branch.Focus()
}

// newConvertModal builds the modal with the two prefilled inputs, focus on the
// branch field.
func newConvertModal(id session.ID, branch, message, curBranch string, dirty bool, changed int) *convertModal {
	b := textinput.New()
	b.Prompt = "  branch:  "
	b.CharLimit = 120
	b.SetValue(branch)
	msgIn := textinput.New()
	msgIn.Prompt = "  message: "
	msgIn.CharLimit = 200
	msgIn.SetValue(message)
	return &convertModal{
		id:        id,
		branch:    b,
		message:   msgIn,
		curBranch: curBranch,
		dirty:     dirty,
		changed:   changed,
	}
}

// handleConvertKey routes keys while the convert modal is open: esc cancels,
// enter submits, tab/↑/↓ move focus, everything else edits the focused field.
func (m *Model) handleConvertKey(msg tea.KeyPressMsg) tea.Cmd {
	cm := m.convert
	switch msg.String() {
	case "esc":
		m.convert = nil
		return nil
	case "enter":
		return m.submitConvert()
	case "tab", "shift+tab", "up", "down":
		return cm.toggleFocus()
	}
	var cmd tea.Cmd
	if cm.focus == 0 {
		cm.branch, cmd = cm.branch.Update(msg)
	} else {
		cm.message, cmd = cm.message.Update(msg)
	}
	return cmd
}

// toggleFocus flips the active field, clearing any inline error the edit is
// about to address.
func (cm *convertModal) toggleFocus() tea.Cmd {
	cm.inlineErr = ""
	if cm.focus == 0 {
		cm.focus = 1
		cm.branch.Blur()
		return cm.message.Focus()
	}
	cm.focus = 0
	cm.message.Blur()
	return cm.branch.Focus()
}

// submitConvert validates the branch locally, then runs Convert off the Update
// goroutine. An empty branch is refused inline (never blocks Update).
func (m *Model) submitConvert() tea.Cmd {
	cm := m.convert
	if cm == nil || cm.busy {
		return nil
	}
	branch := strings.TrimSpace(cm.branch.Value())
	message := strings.TrimSpace(cm.message.Value())
	if branch == "" {
		cm.inlineErr = "branch name is required"
		return nil
	}
	cm.inlineErr = ""
	cm.busy = true
	return m.convertCmd(cm.id, branch, message)
}

// convertCmd runs the deterministic ConvertToBranch git op in a goroutine.
func (m *Model) convertCmd(id session.ID, branch, message string) tea.Cmd {
	ops := m.worktreeOps
	if ops == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		finalBranch, _, err := ops.Convert(ctx, id, branch, message)
		return convertResultMsg{id: id, branch: finalBranch, err: err}
	}
}

// handleConvertResult renders the Convert outcome: success closes the modal and
// toasts "branch <name> ready to merge"; a taken name or a dirty/empty-message
// error keeps the modal open with an inline message; any other error keeps the
// modal with the raw error so the work is never silently lost.
func (m *Model) handleConvertResult(msg convertResultMsg) tea.Cmd {
	if m.convert == nil {
		return nil
	}
	m.convert.busy = false
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, ErrBranchNameTaken):
			m.convert.inlineErr = "branch name already taken — pick another"
		case errors.Is(msg.err, ErrWorktreeDirty):
			m.convert.inlineErr = "a commit message is required to convert a dirty worktree"
		default:
			m.convert.inlineErr = msg.err.Error()
		}
		return nil // keep the modal open so the user can retry
	}
	id := m.convert.id
	title := m.sessionByID(id).DisplayTitle()
	m.convert = nil
	return worktreeToastCmd(id, title, "branch "+msg.branch+" ready to merge", StatusIdle)
}

// worktreeToastCmd surfaces a convert outcome through the existing cross-session
// toast plumbing (the toastMsg handler sets m.toast + starts the tick loop).
func worktreeToastCmd(id session.ID, title, note string, status SessionStatus) tea.Cmd {
	if title == "" {
		title = string(id)
	}
	return func() tea.Msg {
		return toastMsg{id: id, title: title, note: note, status: status}
	}
}

// convertPrefill derives the deterministic branch name and commit message from
// the session title (resolution 8 — titles are already LLM-generated, so no
// propose turn runs). branch = "feat/<slugified-title>", falling back to the
// current sandbox/<id> branch when the title yields no slug; message = the
// title, or "sandbox session <id>" when there is none.
func convertPrefill(title, id, currentBranch string) (branch, message string) {
	if slug := slugify(title); slug != "" {
		branch = "feat/" + slug
	} else {
		branch = currentBranch
		if branch == "" {
			branch = "sandbox/" + id
		}
	}
	if strings.TrimSpace(title) != "" {
		message = strings.TrimSpace(title)
	} else {
		message = "sandbox session " + id
	}
	return branch, message
}

// slugify lowercases the input and collapses every run of non-alphanumeric
// characters into a single hyphen, trimming leading/trailing hyphens — e.g.
// "Fix login flow!" → "fix-login-flow". Returns "" when nothing survives.
func slugify(s string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

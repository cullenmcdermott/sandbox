// Package sync manages Mutagen file sync sessions for remote agent sessions.
//
// Unlike the existing lima-based mutagen.Manager (which syncs to Lima VMs via
// SSH), this module syncs to Kubernetes runner pods via SSH over
// kubectl port-forward. The sync sessions are:
//
//  1. Project: local repo <-> remote <host project path>, two-way-safe (the pod
//     bind-mounts the workspace at the real host path, so both endpoints match)
//  2. Config inputs: ~/.claude/skills etc -> remote /session/state/claude/, one-way
//  3. Transcripts: remote /session/state/claude/projects -> local ~/.claude/projects, one-way
package sync

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
)

// Runner is the interface for Mutagen CLI invocations. It matches the
// existing mutagen.Runner from system-config so the same implementation
// can be shared.
type Runner interface {
	Output(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error)
}

// Manager manages Mutagen sync sessions for a remote session.
type Manager struct {
	r Runner
}

// New creates a sync Manager.
func New(r Runner) *Manager {
	return &Manager{r: r}
}

// Spec describes the sync sessions to create for a remote session.
type Spec struct {
	SessionID    string // sandbox session ID
	ProjectPath  string // local absolute path of the project
	RemotePath   string // remote workspace path; the real host project path bind-mounted into the pod (e.g. /Users/cullen/git/homelab)
	HomeDir      string // local home dir (e.g. /Users/cullen)
	SSHHost      string // mutagen SSH host alias resolved via the per-session ssh config block (e.g. sandbox-<id>), not a host:port
	RemoteClaude string // remote CLAUDE_CONFIG_DIR (e.g. /session/state/claude)
}

// ConfigInputsSubs is the set of ~/.claude subdirectories synced one-way
// from host to remote (config inputs, no credentials).
var ConfigInputsSubs = []struct {
	Local  string
	Remote string
}{
	{"skills", "skills"},
	{"agents", "agents"},
	{"commands", "commands"},
	{"hooks", "hooks"},
}

// TranscriptSubs is the set of ~/.claude subdirectories synced one-way
// from remote to host (transcripts of agent activity).
var TranscriptSubs = []string{"projects", "todos", "tasks"}

// CreateAll creates all three sync session groups for a remote session.
// It is idempotent: existing sessions are not recreated.
func (m *Manager) CreateAll(ctx context.Context, spec Spec) error {
	label := sessionLabel(spec.SessionID)

	// 1. Project sync (two-way-safe). Skip rather than hand Mutagen an empty
	// alpha URL — a missing ProjectPath means the caller couldn't resolve the
	// local repo path (e.g. attaching to a session whose State lacks it), and a
	// blank path would otherwise produce "unable to parse alpha URL: empty URL".
	if spec.ProjectPath == "" {
		return fmt.Errorf("sync: project path is empty for session %s; skipping project sync", spec.SessionID)
	}
	projectName := "sandbox-" + spec.SessionID + "-project"
	// Ignore patterns prevent secrets, credentials, and large build trees from
	// being pushed to the pod (C1). These are defense-in-depth defaults; users
	// can add more via mutagen.yml in the project root.
	args := []string{
		"sync", "create",
		"--name=" + projectName,
		"--label", label,
		// two-way-safe is data-preserving (M27): when only one side changed a
		// path, the change propagates; when BOTH sides changed the same path
		// (a conflict — e.g. the operator and the agent edit one file at once),
		// Mutagen does NOT pick a winner or clobber either copy. It halts
		// propagation for that path and surfaces it as a conflict, visible via
		// `mutagen sync list <name>` (and `mutagen sync monitor`). The operator
		// resolves it by removing the unwanted side's version, after which sync
		// resumes. (Contrast two-way-resolved, which would auto-pick the local
		// side and silently overwrite the pod's edit.)
		"--mode=two-way-safe",
		"--ignore-vcs",
		// Secrets and credentials — never sync to the pod.
		"--ignore=.env",
		"--ignore=.env.*",
		"--ignore=*.pem",
		"--ignore=*.key",
		"--ignore=*.p12",
		"--ignore=*.pfx",
		// Large build/dependency trees that belong on each side independently.
		"--ignore=node_modules",
		"--ignore=vendor",
		"--ignore=.venv",
		"--ignore=venv",
		"--ignore=__pycache__",
		"--ignore=.cache",
		"--ignore=dist",
		"--ignore=build",
		"--ignore=target",
		spec.ProjectPath, spec.SSHHost + ":" + spec.RemotePath,
	}
	if _, err := m.r.Output(ctx, nil, args...); err != nil {
		// C8: use a more specific substring to avoid swallowing unrelated failures.
		// Mutagen emits "session already exists" for idempotent re-creates.
		if !isMutagenAlreadyExists(err) {
			return fmt.Errorf("sync: create project session: %w", err)
		}
	}

	// 2. Config inputs (one-way host -> remote)
	for _, sub := range ConfigInputsSubs {
		name := "sandbox-" + spec.SessionID + "-config-" + sub.Local
		localPath := path.Join(spec.HomeDir, ".claude", sub.Local)
		remotePath := path.Join(spec.RemoteClaude, sub.Remote)
		args := []string{
			"sync", "create",
			"--name=" + name,
			"--label", label,
			"--mode=one-way-safe",
			localPath, spec.SSHHost + ":" + remotePath,
		}
		if _, err := m.r.Output(ctx, nil, args...); err != nil {
			if !isMutagenAlreadyExists(err) {
				return fmt.Errorf("sync: create config-%s session: %w", sub.Local, err)
			}
		}
	}

	// 3. Transcripts (one-way remote -> host)
	for _, sub := range TranscriptSubs {
		name := "sandbox-" + spec.SessionID + "-transcripts-" + sub
		localPath := path.Join(spec.HomeDir, ".claude", sub)
		remotePath := path.Join(spec.RemoteClaude, sub)
		args := []string{
			"sync", "create",
			"--name=" + name,
			"--label", label,
			"--mode=one-way-safe",
			spec.SSHHost + ":" + remotePath, localPath,
		}
		if _, err := m.r.Output(ctx, nil, args...); err != nil {
			if !isMutagenAlreadyExists(err) {
				return fmt.Errorf("sync: create transcripts-%s session: %w", sub, err)
			}
		}
	}

	return nil
}

// Status returns mutagen's listing output for a session's sync sessions.
func (m *Manager) Status(ctx context.Context, sessionID string) ([]byte, error) {
	return m.r.Output(ctx, nil, "sync", "list", "--label-selector="+sessionLabel(sessionID))
}

// FlushAll forces a synchronization cycle for the session's sync sessions and
// blocks until it completes. `mutagen sync create` returns as soon as the
// session is registered — before the SSH transport is proven and before any
// files have staged — so the real failure modes (auth rejected, agent install
// failed, pod unreachable) and the initial project upload happen asynchronously
// and are invisible to the caller. Flushing surfaces those failures (RV20) and
// lets the first sync settle before the user starts working (RV21). Callers
// should bound this with a timeout: a broken transport errors fast, while a
// large-but-healthy first sync may legitimately take a while.
func (m *Manager) FlushAll(ctx context.Context, sessionID string) error {
	_, err := m.r.Output(ctx, nil, "sync", "flush", "--label-selector="+sessionLabel(sessionID))
	if err != nil && isMutagenNotFound(err) {
		return nil
	}
	return err
}

// PauseAll pauses all sync sessions for a session. A "not found" error (no
// sessions matched the selector, e.g. a session that was never synced) is
// treated as success, matching FlushAll/TerminateAll.
func (m *Manager) PauseAll(ctx context.Context, sessionID string) error {
	label := sessionLabel(sessionID)
	_, err := m.r.Output(ctx, nil, "sync", "pause", "--label-selector="+label)
	if err != nil && isMutagenNotFound(err) {
		return nil
	}
	return err
}

// ResumeAll resumes all sync sessions for a session. A "not found" error (no
// sessions matched the selector) is treated as success, matching
// FlushAll/TerminateAll.
func (m *Manager) ResumeAll(ctx context.Context, sessionID string) error {
	label := sessionLabel(sessionID)
	_, err := m.r.Output(ctx, nil, "sync", "resume", "--label-selector="+label)
	if err != nil && isMutagenNotFound(err) {
		return nil
	}
	return err
}

// TerminateAll terminates all sync sessions for a session.
func (m *Manager) TerminateAll(ctx context.Context, sessionID string) error {
	label := sessionLabel(sessionID)
	_, err := m.r.Output(ctx, nil, "sync", "terminate", "--label-selector="+label)
	// C8: use specific mutagen "not found" message rather than bare "not found"
	// which could match an unrelated error.
	if err != nil && isMutagenNotFound(err) {
		return nil
	}
	return err
}

// isMutagenAlreadyExists reports whether the mutagen error indicates that a
// sync session with the given name already exists (idempotent create). Mutagen
// writes "session already exists" to stderr on duplicate create.
func isMutagenAlreadyExists(err error) bool {
	return strings.Contains(err.Error(), "session already exists")
}

// isMutagenNotFound reports whether a mutagen error means no sessions matched
// the label selector — normal when terminating a session that was never synced.
func isMutagenNotFound(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no sessions found") ||
		strings.Contains(msg, "session not found") ||
		strings.Contains(msg, "unable to locate requested sessions")
}

func sessionLabel(sessionID string) string {
	return "sandbox-session=" + sessionID
}

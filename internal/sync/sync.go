// Package sync manages Mutagen file sync sessions for remote agent sessions.
//
// Unlike the existing lima-based mutagen.Manager (which syncs to Lima VMs via
// SSH), this module syncs to Kubernetes runner pods via SSH over
// kubectl port-forward. The sync sessions are:
//
//  1. Project: local repo <-> remote /session/workspace/<path>, two-way-safe
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
	SessionID     string // sandbox session ID
	ProjectPath   string // local absolute path of the project
	RemotePath    string // remote workspace path (e.g. /session/workspace/Users/cullen/git/homelab)
	HomeDir       string // local home dir (e.g. /Users/cullen)
	SSHHost       string // mutagen SSH host alias (e.g. 127.0.0.1:<port>)
	RemoteClaude  string // remote CLAUDE_CONFIG_DIR (e.g. /session/state/claude)
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

	// 1. Project sync (two-way-safe)
	projectName := "sandbox-" + spec.SessionID + "-project"
	args := []string{
		"sync", "create",
		"--name=" + projectName,
		"--label", label,
		"--mode=two-way-safe",
		"--ignore-vcs",
		spec.ProjectPath, spec.SSHHost + ":" + spec.RemotePath,
	}
	if _, err := m.r.Output(ctx, nil, args...); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
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
			if !strings.Contains(err.Error(), "already exists") {
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
			if !strings.Contains(err.Error(), "already exists") {
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

// PauseAll pauses all sync sessions for a session.
func (m *Manager) PauseAll(ctx context.Context, sessionID string) error {
	label := sessionLabel(sessionID)
	_, err := m.r.Output(ctx, nil, "sync", "pause", "--label-selector="+label)
	return err
}

// ResumeAll resumes all sync sessions for a session.
func (m *Manager) ResumeAll(ctx context.Context, sessionID string) error {
	label := sessionLabel(sessionID)
	_, err := m.r.Output(ctx, nil, "sync", "resume", "--label-selector="+label)
	return err
}

// TerminateAll terminates all sync sessions for a session.
func (m *Manager) TerminateAll(ctx context.Context, sessionID string) error {
	label := sessionLabel(sessionID)
	_, err := m.r.Output(ctx, nil, "sync", "terminate", "--label-selector="+label)
	if err != nil && strings.Contains(err.Error(), "not found") {
		return nil
	}
	return err
}

func sessionLabel(sessionID string) string {
	return "sandbox-session=" + sessionID
}

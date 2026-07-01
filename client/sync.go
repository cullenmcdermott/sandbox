package client

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
)

// syncManager returns a Mutagen sync Manager backed by the mutagen CLI.
func (c *Client) syncManager() *syncpkg.Manager {
	return syncpkg.New(syncpkg.NewExecRunner(""))
}

// StopSync terminates all Mutagen sync sessions for a session. Best-effort.
// Exposed so the TUI can stop sync before a cluster destroy (clean teardown
// rather than racing the pod's disappearance into EOF errors).
func (c *Client) StopSync(ctx context.Context, id ID) {
	_ = c.syncManager().TerminateAll(ctx, string(id))
}

// SyncPause pauses a session's Mutagen sync sessions.
func (c *Client) SyncPause(ctx context.Context, id ID) error {
	return c.syncManager().PauseAll(ctx, string(id))
}

// SyncResume resumes a session's Mutagen sync sessions.
func (c *Client) SyncResume(ctx context.Context, id ID) error {
	return c.syncManager().ResumeAll(ctx, string(id))
}

// SyncTerminate terminates a session's Mutagen sync sessions and removes its SSH
// alias.
func (c *Client) SyncTerminate(ctx context.Context, id ID) error {
	if err := c.syncManager().TerminateAll(ctx, string(id)); err != nil {
		return err
	}
	if cfg, err := c.sshConfig(); err == nil {
		_ = cfg.Remove(string(id))
	}
	return nil
}

// SyncStatus returns the raw Mutagen status output for a session's sync sessions.
func (c *Client) SyncStatus(ctx context.Context, id ID) ([]byte, error) {
	return c.syncManager().Status(ctx, string(id))
}

// RemoveLocalState removes a session's local artifacts: SSH alias, per-session
// key directory, and index entry. Run after a confirmed cluster-side destroy.
func (c *Client) RemoveLocalState(id ID) {
	sid := string(id)
	if cfg, err := c.sshConfig(); err == nil {
		_ = cfg.Remove(sid)
	}
	if dir, err := c.sessionKeyDir(sid); err == nil {
		_ = os.RemoveAll(dir)
	}
	_ = c.index.Delete(sid)
}

// sessionKeyDir returns the local directory holding a session's SSH key. It
// validates that the resolved path is still under the state root to prevent
// path-traversal via a crafted session id.
func (c *Client) sessionKeyDir(id string) (string, error) {
	root := c.stateDir
	dir := filepath.Join(root, id)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, dirAbs)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "" {
		return "", fmt.Errorf("session id %q escapes session root: unsafe path %q", id, dirAbs)
	}
	return dir, nil
}

// ensureSSHKey returns the local private key path and the authorized (public)
// key for a session, generating and persisting a new ed25519 keypair on first
// use. The same key is reused across reconnects so it keeps matching the public
// key stored in the pod's per-session Secret.
func (c *Client) ensureSSHKey(id string) (privPath, authorizedKey string, err error) {
	dir, err := c.sessionKeyDir(id)
	if err != nil {
		return "", "", err
	}
	privPath = filepath.Join(dir, "id_ed25519")
	pubPath := privPath + ".pub"

	if priv, rerr := os.ReadFile(privPath); rerr == nil {
		if pub, perr := os.ReadFile(pubPath); perr == nil {
			return privPath, strings.TrimSpace(string(pub)), nil
		}
		// .pub missing — derive it from the private key.
		if signer, serr := ssh.ParsePrivateKey(priv); serr == nil {
			auth := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
			return privPath, auth, nil
		}
	}

	priv, auth, err := syncpkg.GenerateKeyPair("sandbox-" + id)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(privPath, priv, 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(pubPath, []byte(auth+"\n"), 0o644); err != nil {
		return "", "", err
	}
	return privPath, auth, nil
}

// sshConfig returns the per-session SSH alias manager, writing into the state
// dir's ssh/config (included from ~/.ssh/config).
func (c *Client) sshConfig() (*syncpkg.SSHConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	include := filepath.Join(filepath.Dir(c.stateDir), "ssh", "config")
	userCfg := filepath.Join(home, ".ssh", "config")
	return syncpkg.NewSSHConfig(include, userCfg), nil
}

// startMutagen writes the SSH alias for the current port-forward and (re)creates
// the session's Mutagen sync sessions. Idempotent across reconnects; reports
// whether the load-bearing project sync was freshly created (this session's
// first-ever sync) so the caller can skip a blocking initial flush on reconnect.
func (c *Client) startMutagen(ctx context.Context, id, projectPath, privPath string, sshLocalPort int) (created bool, err error) {
	cfg, err := c.sshConfig()
	if err != nil {
		return false, err
	}
	if err := cfg.Upsert(id, sshLocalPort, privPath); err != nil {
		return false, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	mgr := c.syncManager()
	// Resume any syncs paused by a prior suspend BEFORE CreateAll: CreateAll is
	// idempotent and reports "already exists" without un-pausing, so without this
	// a plain re-attach would leave files frozen with no error. Best-effort.
	_ = mgr.ResumeAll(ctx, id)
	return mgr.CreateAll(ctx, syncpkg.Spec{
		SessionID: id,
		// The pod bind-mounts the workspace at the real host project path, so both
		// sync endpoints use the same absolute path (keeps the SDK cwd
		// host-matching for resumable transcripts).
		ProjectPath:  projectPath,
		RemotePath:   projectPath,
		HomeDir:      home,
		SSHHost:      syncpkg.Alias(id),
		RemoteClaude: remoteClaudeDir,
	})
}

// ensureReaper starts (or confirms) the per-session idle reaper. A failure is
// non-fatal — the session works without auto-suspend — so it only warns.
func (c *Client) ensureReaper(ctx context.Context, ref Ref, image string, idleTimeout time.Duration) {
	opts := k8s.ReaperOptions{Image: image, IdleTimeout: idleTimeout}
	// Test/override hooks: shorten the idle window and poll for end-to-end
	// validation without waiting the default. Unset => EnsureReaper defaults.
	if v := os.Getenv("SANDBOX_REAPER_IDLE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			opts.IdleTimeout = d
		}
	}
	if v := os.Getenv("SANDBOX_REAPER_POLL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			opts.PollInterval = d
		}
	}
	if err := c.backend.EnsureReaper(ctx, ref, opts); err != nil {
		fmt.Fprintf(os.Stderr, "warning: idle reaper for %s not started: %v\n", ref.ID, err)
	}
}

// waitHealthy polls the runner /healthz until it responds OK or ctx is done. A
// freshly resumed pod (or new port-forward) may need a moment.
func waitHealthy(ctx context.Context, client *runner.Client) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := client.Health(hctx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// waitOpencodeReady blocks until an HTTP request to url elicits any HTTP response
// (confirming `opencode serve` is listening in the pod), or the budget/context
// expires. ANY status code counts as ready — only transport errors are retried —
// because a client-go SPDY forward accepts the local connection the instant its
// listener binds, so a bare TCP dial false-passes before the pod port answers.
func waitOpencodeReady(ctx context.Context, url string) error {
	deadline := time.Now().Add(30 * time.Second)
	client := &http.Client{Timeout: 3 * time.Second}
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

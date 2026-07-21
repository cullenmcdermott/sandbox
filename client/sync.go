package client

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
)

// syncManager returns a Mutagen sync Manager. It is backed by the mutagen CLI in
// production; a test may inject an alternative runner via Client.syncRunner to
// observe or stub the Mutagen calls the orchestration paths make.
func (c *Client) syncManager() *syncpkg.Manager {
	r := c.syncRunner
	if r == nil {
		r = syncpkg.NewExecRunner("")
	}
	m := syncpkg.New(r)
	// [V28] Stamp the client's effective namespace onto new syncs so the GC can
	// scope to it (a same-context sync in another namespace belongs to a live
	// session the namespace-scoped live set can't see).
	m.SetNamespace(c.backend.Namespace())
	return m
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
	// [V12] Validate the id up front via sessionKeyDir: an empty, ".", or
	// traversing id must delete NOTHING. A "" would otherwise resolve the per-
	// session key dir AND the index dir to the state root itself and RemoveAll the
	// entire tree (every session's keys, the index, the ssh include, worktrees).
	// A rejected id simply has no per-session state to remove, so bail out.
	dir, err := c.sessionKeyDir(sid)
	if err != nil {
		return
	}
	if cfg, err := c.sshConfig(); err == nil {
		_ = cfg.Remove(sid)
	}
	_ = os.RemoveAll(dir)
	_ = c.index.Delete(sid)
}

// sessionIDRe constrains a session id to the same [a-z0-9-]+ charset
// internal/sync's validateSessionID enforces before ssh-config interpolation.
// [V12] It is the first guard in sessionKeyDir: an empty or "." id would resolve
// filepath.Rel(root, root) to "." — which the traversal check below alone lets
// through — and RemoveLocalState would then os.RemoveAll the entire state root.
var sessionIDRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// sessionKeyDir returns the local directory holding a session's SSH key. It
// validates the id charset AND that the resolved path is still strictly under
// the state root, so a crafted (or empty / ".") session id can neither traverse
// out of the root nor resolve to the root itself.
func (c *Client) sessionKeyDir(id string) (string, error) {
	if !sessionIDRe.MatchString(id) {
		return "", fmt.Errorf("invalid session id %q: must match [a-z0-9-]+", id)
	}
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
	if err != nil || strings.HasPrefix(rel, "..") || rel == "" || rel == "." {
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

// SSHConfigPath returns the path of the per-session SSH alias include file for a
// given state dir: <stateDir>/ssh/config — INSIDE the state dir alongside the
// session index and worktrees, so a WithStateDir consumer keeps every sandbox
// artifact under one root. Exported so local-only CLI helpers (which have no
// connected Client but the same state dir) compute the identical path instead of
// re-deriving it and drifting ([V13] — the CLI's `sandbox sync --terminate` once
// pointed at the pre-migration sibling location and silently no-op'd removals).
func SSHConfigPath(stateDir string) string {
	return filepath.Join(stateDir, "ssh", "config")
}

// sshConfig returns the per-session SSH alias manager. The include file lives at
// <stateDir>/ssh/config — INSIDE the state dir alongside the session index and
// (future) worktrees, so a WithStateDir consumer keeps every sandbox artifact
// under one root — and is Include'd from ~/.ssh/config. A best-effort one-time
// migration relocates a pre-existing include dir from its old sibling location.
func (c *Client) sshConfig() (*syncpkg.SSHConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	include := SSHConfigPath(c.stateDir)
	userCfg := filepath.Join(home, ".ssh", "config")
	c.migrateSSHDir(include, userCfg)
	return syncpkg.NewSSHConfig(include, userCfg), nil
}

// worktreesRoot returns the local root under which per-session git worktrees
// live: <stateDir>/worktrees, a sibling of the per-session index/key dirs so a
// WithStateDir consumer keeps everything under one root and reaping has one
// place to enumerate. createWorktree (worktree.go) creates the per-session
// worktree at <worktreesRoot>/<id>; ReapWorktrees enumerates the same root.
func (c *Client) worktreesRoot() string {
	return filepath.Join(c.stateDir, "worktrees")
}

// migrateSSHDir performs the one-time relocation of the per-session ssh include
// dir from its old home — a SIBLING of the state dir (dir(stateDir)/ssh) — to its
// new home INSIDE the state dir (stateDir/ssh), and rewrites the Include line in
// the user's ~/.ssh/config to point at the new path. Best-effort and idempotent:
// it acts only when the old dir exists and the new one does not, so a fresh
// install or an already-migrated one is a no-op. Every error is swallowed —
// sshConfig's next Upsert re-creates the dir and re-adds the Include regardless.
func (c *Client) migrateSSHDir(newInclude, userCfg string) {
	newDir := filepath.Dir(newInclude)                       // <stateDir>/ssh
	oldDir := filepath.Join(filepath.Dir(c.stateDir), "ssh") // sibling of the state dir
	if oldDir == newDir {
		return
	}
	if _, err := os.Stat(newDir); err == nil {
		return // new location already present: fresh or already migrated
	}
	if _, err := os.Stat(oldDir); err != nil {
		return // nothing legacy to migrate
	}
	if err := os.MkdirAll(filepath.Dir(newDir), 0o700); err != nil {
		return
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return
	}
	rewriteSSHInclude(userCfg, filepath.Join(oldDir, "config"), newInclude)
}

// rewriteSSHInclude replaces the "Include <oldConfig>" line in the user's ssh
// config with the quoted new form (matching the C5 quoted-Include written by
// SSHConfig.ensureInclude), collapsing any duplicate. Best-effort: a missing or
// unwritable config is left untouched (the next Upsert re-adds the Include).
func rewriteSSHInclude(userCfg, oldConfig, newInclude string) {
	data, err := os.ReadFile(userCfg)
	if err != nil {
		return
	}
	quotedOld := fmt.Sprintf("Include %q", oldConfig)
	legacyOld := "Include " + oldConfig
	quotedNew := fmt.Sprintf("Include %q", newInclude)
	var out []string
	changed, seenNew := false, false
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(strings.TrimRight(line, "\r")) {
		case quotedOld, legacyOld:
			changed = true
			if !seenNew {
				out = append(out, quotedNew)
				seenNew = true
			}
			continue
		case quotedNew:
			if seenNew {
				changed = true
				continue // drop the duplicate
			}
			seenNew = true
		}
		out = append(out, line)
	}
	if changed {
		_ = os.WriteFile(userCfg, []byte(strings.Join(out, "\n")), 0o600)
	}
}

// startProjectSync writes the SSH alias for the current port-forward and creates
// ONLY the load-bearing project sync — the one the agent needs staged before it
// can work on the repo. It also resumes any syncs a prior suspend paused. The 7
// non-load-bearing config/transcript syncs are created off the foreground by the
// caller (see Session.Connect / CreateInputs) so the visible prompt is not gated
// on them (§5). Idempotent across reconnects; reports whether the project sync
// was freshly created (this session's first-ever sync) so the caller can skip a
// blocking initial flush on reconnect. The built Spec is returned so the caller
// can hand it to the background CreateInputs without rebuilding it.
func (c *Client) startProjectSync(ctx context.Context, id, workspacePath, privPath string, sshLocalPort int) (created bool, spec syncpkg.Spec, err error) {
	cfg, err := c.sshConfig()
	if err != nil {
		return false, syncpkg.Spec{}, err
	}
	if err := cfg.Upsert(id, sshLocalPort, privPath); err != nil {
		return false, syncpkg.Spec{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, syncpkg.Spec{}, err
	}
	spec = syncpkg.Spec{
		SessionID: id,
		// The pod bind-mounts the workspace at this real host path, so both sync
		// endpoints use the same absolute path (keeps the SDK cwd host-matching
		// for resumable transcripts). This is the WORKSPACE path (the worktree dir
		// once one exists), not the repo-root ProjectPath.
		ProjectPath:  workspacePath,
		RemotePath:   workspacePath,
		HomeDir:      home,
		SSHHost:      syncpkg.Alias(id),
		RemoteClaude: remoteClaudeDir,
	}
	mgr := c.syncManager()
	// Resume any syncs paused by a prior suspend BEFORE creating: CreateProject/
	// CreateInputs are idempotent and report "already exists" without un-pausing,
	// so without this a plain re-attach would leave files frozen with no error.
	// Best-effort.
	_ = mgr.ResumeAll(ctx, id)
	created, err = mgr.CreateProject(ctx, spec)
	return created, spec, err
}

// ensureReaper starts (or confirms) the per-session idle reaper. A failure is
// non-fatal — the session works without auto-suspend — so it returns a warning
// string for the caller to surface (Connection.Warning), empty on success.
// Writing to stderr here would be invisible to library callers and corrupt an
// active TUI. (The idle window is resolved by Connect; see the precedence note
// there.)
func (c *Client) ensureReaper(ctx context.Context, ref Ref, image, pullPolicy string, idleTimeout time.Duration) string {
	opts := k8s.ReaperOptions{Image: image, ImagePullPolicy: pullPolicy, IdleTimeout: idleTimeout}
	// Test hook: shorten the reaper poll for end-to-end validation without
	// waiting the default. There is no programmatic knob for it. Unset =>
	// EnsureReaper default.
	if v := os.Getenv("SANDBOX_REAPER_POLL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			opts.PollInterval = d
		}
	}
	if err := c.backend.EnsureReaper(ctx, ref, opts); err != nil {
		return fmt.Sprintf("idle reaper not started (session won't auto-suspend): %v", err)
	}
	return ""
}

// ensureReaperWithRetry runs ensureReaper with a few bounded retries. The reaper
// is what caps runaway pod cost, so when it is ensured off the foreground connect
// path (§5) a single transient API-server blip must not silently leave a session
// with no auto-suspend. Retries stop early if ctx is cancelled (session closed).
// Returns the last warning (empty on success) for the caller to surface.
func (c *Client) ensureReaperWithRetry(ctx context.Context, ref Ref, image, pullPolicy string, idleTimeout time.Duration) string {
	const attempts = 3
	backoff := 500 * time.Millisecond
	var warn string
	for i := 0; i < attempts; i++ {
		if warn = c.ensureReaper(ctx, ref, image, pullPolicy, idleTimeout); warn == "" {
			return ""
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return warn
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return warn
}

// healthChecker is the runner health seam waitHealthy depends on. *runner.Client
// satisfies it; tests use a fake (mirrors reap.go's idleChecker split).
type healthChecker interface {
	Health(ctx context.Context) error
}

// waitHealthy polls the runner /healthz until it responds OK or ctx is done. A
// freshly resumed pod (or new port-forward) may need a moment.
func waitHealthy(ctx context.Context, client healthChecker) error {
	return waitHealthyWithin(ctx, client, 30*time.Second, time.Second)
}

// waitHealthyWithin is the testable core of waitHealthy: it polls client.Health
// every interval until it returns nil, the budget elapses (returning the last
// error), or ctx is cancelled. budget/interval are injected so the
// deadline-exhaustion branch is exercisable without a real 30s wait. Each poll
// caps at a 3s per-attempt timeout, matching production.
func waitHealthyWithin(ctx context.Context, client healthChecker, budget, interval time.Duration) error {
	deadline := time.Now().Add(budget)
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
		case <-time.After(interval):
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

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/cullenmcdermott/sandbox/internal/index"
	"github.com/cullenmcdermott/sandbox/internal/session"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

const (
	// remoteClaudeDir mirrors the runner's CLAUDE_CONFIG_DIR.
	remoteClaudeDir = "/session/state/claude"
)

// newIndex returns the local session index at the default path.
func newIndex() (*index.Index, error) {
	return index.NewDefault()
}

// sessionKeyDir returns the local directory holding a session's SSH key.
// It validates that the resolved path is still under the index root to
// prevent path-traversal via a crafted session id (C5).
func sessionKeyDir(id string) (string, error) {
	root, err := index.DefaultRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, id)
	// Guard: filepath.Join collapses ".." components; ensure the result is
	// still a child of root.
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
func ensureSSHKey(id string) (privPath, authorizedKey string, err error) {
	dir, err := sessionKeyDir(id)
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

// sshConfigManager returns the per-session SSH alias manager.
func sshConfigManager() (*syncpkg.SSHConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root, err := index.DefaultRoot()
	if err != nil {
		return nil, err
	}
	// ~/.local/share/sandbox/ssh/config, included from ~/.ssh/config.
	include := filepath.Join(filepath.Dir(root), "ssh", "config")
	userCfg := filepath.Join(home, ".ssh", "config")
	return syncpkg.NewSSHConfig(include, userCfg), nil
}

// syncManager returns a Mutagen sync Manager backed by the mutagen CLI.
func syncManager() *syncpkg.Manager {
	return syncpkg.New(syncpkg.NewExecRunner(""))
}

// dashboardSyncProber builds the dashboard's per-session sync-health probe,
// backed by the Mutagen sync manager. It maps a SyncState to its short token
// and degrades to "unknown" on any error so the indicator never blocks the UI.
func dashboardSyncProber() dashboard.SyncProber {
	return func(ctx context.Context, id session.ID) string {
		st, err := syncManager().StatusSummary(ctx, string(id))
		if err != nil {
			return "unknown"
		}
		return st.String()
	}
}

// dashboardSyncReaper builds the dashboard's orphaned-sync GC, backed by the
// Mutagen sync manager. ListOrphans reports this tool's syncs whose pod endpoint
// is down (sync.IsOrphanStatus); the dashboard decides which are durably dead
// (session gone + past the grace) before terminating them by identifier.
func dashboardSyncReaper() dashboard.SyncReaper {
	return reaperAdapter{}
}

type reaperAdapter struct{}

func (reaperAdapter) ListOrphans(ctx context.Context) ([]dashboard.OrphanSync, error) {
	sessions, err := syncManager().List(ctx)
	if err != nil {
		return nil, err
	}
	var orphans []dashboard.OrphanSync
	for _, s := range sessions {
		if syncpkg.IsOrphanStatus(s.Status) {
			orphans = append(orphans, dashboard.OrphanSync{
				Identifier: s.Identifier,
				SessionID:  session.ID(s.SessionID),
			})
		}
	}
	return orphans, nil
}

func (reaperAdapter) Terminate(ctx context.Context, identifiers []string) error {
	return syncManager().TerminateByIdentifier(ctx, identifiers...)
}

// startMutagen writes the SSH alias for the current port-forward and (re)creates
// the session's Mutagen sync sessions. It is idempotent across reconnects, and
// reports whether the load-bearing project sync was freshly created (created=true,
// i.e. this session's first-ever sync) so the caller can skip a blocking initial
// flush on reconnect.
func startMutagen(ctx context.Context, id, projectPath, privPath string, sshLocalPort int) (created bool, err error) {
	cfg, err := sshConfigManager()
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
	// Resume any syncs paused by a prior `sandbox suspend` BEFORE CreateAll. CreateAll
	// is idempotent — for an already-existing (paused) sync it reports "already exists"
	// and returns created=false WITHOUT un-pausing it — so without this, the most
	// natural resume (just re-attaching, not running `sandbox resume`) would leave the
	// session's files frozen with no error shown. Idempotent + best-effort: a no-op on
	// non-paused sessions, "not found" treated as success.
	_ = syncManager().ResumeAll(ctx, id)
	return syncManager().CreateAll(ctx, syncpkg.Spec{
		SessionID: id,
		// The pod bind-mounts the workspace at the real host project path (see
		// k8s runnerVolumeMounts), so both sync endpoints use the same absolute
		// path. This keeps the SDK cwd host-matching for resumable transcripts.
		ProjectPath:  projectPath,
		RemotePath:   projectPath,
		HomeDir:      home,
		SSHHost:      syncpkg.Alias(id),
		RemoteClaude: remoteClaudeDir,
	})
}

// indexTitleStore implements dashboard.TitleStore on top of the local session
// index, so a session rename in the TUI persists across restart/reattach (T5).
type indexTitleStore struct{}

// LoadTitle returns the persisted user-chosen title for a session, or "".
func (indexTitleStore) LoadTitle(id session.ID) string {
	idx, err := newIndex()
	if err != nil {
		return ""
	}
	entry, err := idx.Load(string(id))
	if err != nil {
		return ""
	}
	return entry.RenamedTitle
}

// SaveTitle persists the user-chosen title, preserving the rest of the entry.
func (indexTitleStore) SaveTitle(id session.ID, title string) {
	idx, err := newIndex()
	if err != nil {
		return
	}
	entry, err := idx.Load(string(id))
	if err != nil {
		// No entry yet (e.g. session created outside this host): start a minimal
		// one so the rename isn't dropped.
		entry = index.Entry{SandboxSessionID: string(id), SandboxName: string(id)}
	}
	entry.RenamedTitle = title
	_ = idx.Save(string(id), entry)
}

// SaveClaudeSessionID persists the Claude SDK session UUID for a session,
// preserving the rest of the entry. This is what later lets the CLI write a
// local ~/.claude/history.jsonl entry so the session resumes from the laptop.
func (indexTitleStore) SaveClaudeSessionID(id session.ID, claudeID string) {
	if claudeID == "" {
		return
	}
	idx, err := newIndex()
	if err != nil {
		return
	}
	entry, err := idx.Load(string(id))
	if err != nil {
		entry = index.Entry{SandboxSessionID: string(id), SandboxName: string(id)}
	}
	if entry.ClaudeSessionID == claudeID {
		return // already recorded; avoid a redundant write
	}
	entry.ClaudeSessionID = claudeID
	_ = idx.Save(string(id), entry)
}

// LoadAutoTitle returns the persisted runner-generated auto title, or "".
func (indexTitleStore) LoadAutoTitle(id session.ID) string {
	idx, err := newIndex()
	if err != nil {
		return ""
	}
	entry, err := idx.Load(string(id))
	if err != nil {
		return ""
	}
	return entry.AutoTitle
}

// SaveAutoTitle persists the runner-generated auto title, preserving the rest of
// the entry (notably any user-chosen RenamedTitle).
func (indexTitleStore) SaveAutoTitle(id session.ID, title string) {
	idx, err := newIndex()
	if err != nil {
		return
	}
	entry, err := idx.Load(string(id))
	if err != nil {
		entry = index.Entry{SandboxSessionID: string(id), SandboxName: string(id)}
	}
	entry.AutoTitle = title
	_ = idx.Save(string(id), entry)
}

// indexSnapshotStore implements dashboard.SnapshotStore on top of the local
// session index, so the dashboard's per-session live read-model survives a
// restart — letting a relaunch render real status/usage immediately and resume
// the SSE stream from the cached seq instead of replaying the full history.
type indexSnapshotStore struct{}

// LoadSnapshot returns the cached snapshot for a session, or ok=false when none
// has been persisted yet (or the entry can't be read).
func (indexSnapshotStore) LoadSnapshot(id session.ID) (dashboard.SessionSnapshot, bool) {
	idx, err := newIndex()
	if err != nil {
		return dashboard.SessionSnapshot{}, false
	}
	entry, err := idx.Load(string(id))
	if err != nil || entry.Snapshot == nil {
		return dashboard.SessionSnapshot{}, false
	}
	snap := entry.Snapshot
	status, _ := dashboard.ParseStatus(snap.DashStatus) // unknown → StatusIdle
	return dashboard.SessionSnapshot{
		LastSeq:               entry.LastEventSeq,
		DashStatus:            status,
		PendingPermissionID:   snap.PendingPermissionID,
		PendingPermissionTool: snap.PendingPermissionTool,
		Model:                 snap.Model,
		InputTokens:           snap.InputTokens,
		OutputTokens:          snap.OutputTokens,
		CacheReadTokens:       snap.CacheReadTokens,
		CacheWriteTokens:      snap.CacheWriteTokens,
		TotalCostUSD:          snap.TotalCostUSD,
	}, true
}

// SaveSnapshot persists the snapshot, preserving the rest of the entry. The
// resume cursor lives on Entry.LastEventSeq; the display-derived fields live on
// Entry.Snapshot.
func (indexSnapshotStore) SaveSnapshot(id session.ID, snap dashboard.SessionSnapshot) {
	idx, err := newIndex()
	if err != nil {
		return
	}
	entry, err := idx.Load(string(id))
	if err != nil {
		entry = index.Entry{SandboxSessionID: string(id), SandboxName: string(id)}
	}
	entry.LastEventSeq = snap.LastSeq
	entry.LastActivity = time.Now()
	entry.Snapshot = &index.Snapshot{
		DashStatus:            snap.DashStatus.String(),
		PendingPermissionID:   snap.PendingPermissionID,
		PendingPermissionTool: snap.PendingPermissionTool,
		Model:                 snap.Model,
		InputTokens:           snap.InputTokens,
		OutputTokens:          snap.OutputTokens,
		CacheReadTokens:       snap.CacheReadTokens,
		CacheWriteTokens:      snap.CacheWriteTokens,
		TotalCostUSD:          snap.TotalCostUSD,
	}
	_ = idx.Save(string(id), entry)
}

// indexEventCache implements dashboard.EventCache on top of the local index's
// per-session events.ndjson (Workstream C): the foreground transcript loads it on
// a cold open to rebuild history instantly and appends each non-delta event it
// streams. Best effort — a cache miss/failure just falls back to a full runner
// replay, which is still correct (the cache is a discardable local mirror).
type indexEventCache struct{}

func (indexEventCache) LoadEvents(id session.ID) ([]session.Event, error) {
	idx, err := newIndex()
	if err != nil {
		return nil, err
	}
	return idx.LoadCachedEvents(string(id))
}

func (indexEventCache) AppendEvent(id session.ID, ev session.Event) error {
	idx, err := newIndex()
	if err != nil {
		return err
	}
	return idx.AppendCachedEvent(string(id), ev)
}

// newPreDestroySyncStop returns a callback that stops file sync for a session.
// The TUI runs it BEFORE the cluster-side destroy so the mutagen-over-SSH stream
// is torn down cleanly rather than racing the pod's disappearance into
// "connection closed"/EOF errors. It is recoverable — a re-attach re-creates the
// sync sessions — so unlike newLocalDestroyHook it runs regardless of whether
// the destroy then succeeds.
func newPreDestroySyncStop() func(id session.ID) {
	return func(id session.ID) {
		_ = syncManager().TerminateAll(context.Background(), string(id))
	}
}

// newLocalDestroyHook returns a callback that performs the irreversible local
// cleanup the CLI `destroy` command does: remove the SSH alias, delete the
// per-session key directory (C2 fix for TUI destroy), and drop the local index
// entry so the session doesn't linger in `status --all`. Sync teardown is NOT
// here — it runs earlier via newPreDestroySyncStop. The TUI invokes this only
// after the cluster-side destroy is confirmed.
func newLocalDestroyHook() func(id session.ID) {
	return func(id session.ID) {
		sid := string(id)
		if cfg, err := sshConfigManager(); err == nil {
			_ = cfg.Remove(sid)
		}
		if dir, err := sessionKeyDir(sid); err == nil {
			_ = os.RemoveAll(dir)
		}
		if idx, err := newIndex(); err == nil {
			_ = idx.Delete(sid)
		}
	}
}

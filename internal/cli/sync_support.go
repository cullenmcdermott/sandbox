package cli

import (
	"context"
	"time"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/index"
	"github.com/cullenmcdermott/sandbox/internal/session"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// This file holds the CLI/TUI glue that wraps internal/index and internal/sync
// for the dashboard (title/snapshot/event stores, sync health probe + orphan GC,
// destroy hooks). The session create/connect/sync/lifecycle orchestration lives
// in the public client package; these adapters are TUI-specific read/write
// surfaces the dashboard needs and an external library consumer does not.

// newIndex returns the local session index at the default path.
func newIndex() (*index.Index, error) {
	return index.NewDefault()
}

// syncManager returns a Mutagen sync Manager backed by the mutagen CLI, for the
// dashboard's read-only health probe and the orphan GC sweep.
func syncManager() *syncpkg.Manager {
	return syncpkg.New(syncpkg.NewExecRunner(""))
}

// dashboardSyncProber builds the dashboard's per-session sync-health probe,
// backed by the Mutagen sync manager. It maps a SyncState to its short token and
// degrades to "unknown" on any error so the indicator never blocks the UI.
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
// Mutagen sync manager.
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

// indexTitleStore implements dashboard.TitleStore on top of the local session
// index, so a session rename in the TUI persists across restart/reattach.
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
// preserving the rest of the entry. This is what later lets the CLI write a local
// ~/.claude/history.jsonl entry so the session resumes from the laptop.
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
// per-session events.ndjson: the foreground transcript loads it on a cold open to
// rebuild history instantly and appends each non-delta event it streams. Best
// effort — a cache miss/failure just falls back to a full runner replay.
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
// sync sessions — so unlike the local destroy hook it runs regardless of whether
// the destroy then succeeds.
func newPreDestroySyncStop(c *client.Client) func(id session.ID) {
	return func(id session.ID) {
		c.StopSync(context.Background(), id)
	}
}

// newLocalDestroyHook returns a callback that performs the irreversible local
// cleanup the CLI `destroy` command does: remove the SSH alias, delete the
// per-session key directory, and drop the local index entry so the session
// doesn't linger in `status --all`. Sync teardown is NOT here — it runs earlier
// via newPreDestroySyncStop. The TUI invokes this only after the cluster-side
// destroy is confirmed.
func newLocalDestroyHook(c *client.Client) func(id session.ID) {
	return func(id session.ID) {
		c.RemoveLocalState(id)
	}
}

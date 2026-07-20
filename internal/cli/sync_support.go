package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
// dashboard's read-only health probe, the orphan GC sweep, and the local-only
// `sandbox sync` operations.
func syncManager() *syncpkg.Manager {
	return syncpkg.New(syncpkg.NewExecRunner(""))
}

// localSSHConfig returns the per-session SSH alias manager at the default state
// root — the same include file the client package writes (the "ssh" dir INSIDE
// the state root, Include'd from ~/.ssh/config) — without needing a
// cluster-connected client. Used by `sandbox sync --terminate`, which must work
// when the kubeconfig is gone. It computes the path via client.SSHConfigPath so
// it can never drift from where the client actually writes it ([V13] — this once
// pointed at the pre-migration sibling dir and silently failed to remove aliases).
func localSSHConfig() (*syncpkg.SSHConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root, err := index.DefaultRoot()
	if err != nil {
		return nil, err
	}
	include := client.SSHConfigPath(root)
	return syncpkg.NewSSHConfig(include, filepath.Join(home, ".ssh", "config")), nil
}

// syncHealMinInterval bounds how often a single session's stalled sync is
// self-healed (MF5). The dashboard probes each warm session's health on a timer,
// so without a debounce a persistent stall would fire ResumeAll+FlushAll on every
// probe. One heal attempt per interval is enough to un-stick a transient stall
// while the SSE stream stays healthy.
const syncHealMinInterval = 30 * time.Second

// syncProber is the stateful backing for the dashboard's sync-health probe. It
// maps a SyncState to the dashboard's decoupled SyncHealth and, on a heal-eligible
// reading (a transport stall or a paused-while-running sync — never a safety
// halt; see healEligible), fires a debounced background self-heal (MF5) — the
// layer that owns the prober, not the dashboard, drives the recovery.
type syncProber struct {
	mu         sync.Mutex
	lastHealAt map[session.ID]time.Time
}

// probe reads a session's sync health, shapes the conflict detail, and triggers
// the self-heal when the sync has stalled. It degrades to "unknown" on any error
// so the indicator never blocks the UI.
func (p *syncProber) probe(ctx context.Context, id session.ID) dashboard.SyncHealth {
	// conflictFileCap bounds how many conflicting files the detail pane lists
	// before collapsing the rest into a "+N more" line, so a mass conflict can't
	// flood the pane.
	const conflictFileCap = 5
	st, summary, err := syncManager().StatusDetail(ctx, string(id))
	if err != nil {
		return dashboard.SyncHealth{Status: "unknown"}
	}
	if healEligible(st) {
		p.maybeHeal(id)
	}
	h := dashboard.SyncHealth{Status: st.String()}
	if st == syncpkg.SyncConflicted && summary.Total > 0 {
		for i, cf := range summary.Files {
			if i >= conflictFileCap {
				h.Conflicts = append(h.Conflicts, fmt.Sprintf("+%d more", summary.Total-conflictFileCap))
				break
			}
			h.Conflicts = append(h.Conflicts, cf.Describe())
		}
		h.Hint = syncpkg.ConflictResolutionHint
	}
	return h
}

// healEligible reports whether a sync state should trigger the debounced MF5
// self-heal (ResumeAll+FlushAll). A plain transport stall and a paused-while-
// running sync are both safely resumable ([V14]), but a SyncSafetyHalted state is
// deliberately EXCLUDED ([V2]): resuming a mutagen safety halt (root emptied/
// deleted/type change) is its documented CONFIRM-and-propagate action for a mass
// deletion, so it must wait for explicit user review, never an auto-heal. Healthy
// / unknown / conflicted states need no transport heal. The prober only probes
// running sessions, so healing a paused sync is safe (a deliberate `sync --pause`
// self-resumes; a suspended session is not probed).
func healEligible(st syncpkg.SyncState) bool {
	return st == syncpkg.SyncStalled || st == syncpkg.SyncPaused
}

// maybeHeal fires a background Reconcile (ResumeAll+FlushAll) for a stalled
// session, at most once per syncHealMinInterval (MF5). It runs off the probe
// goroutine on its own bounded context so a slow flush never blocks the health
// read, and swallows errors — a failed heal just leaves the stall visible for the
// next probe to retry. It heals a stalled-but-existing sync while SSE is healthy,
// the gap where nothing else re-runs sync setup; a genuinely terminated sync is
// still recreated by the connect path on the next reconnect.
func (p *syncProber) maybeHeal(id session.ID) {
	p.mu.Lock()
	now := time.Now()
	if last, ok := p.lastHealAt[id]; ok && now.Sub(last) < syncHealMinInterval {
		p.mu.Unlock()
		return
	}
	p.lastHealAt[id] = now
	p.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = syncManager().Reconcile(ctx, string(id))
	}()
}

// dashboardSyncProber builds the dashboard's per-session sync-health probe,
// backed by the Mutagen sync manager. It maps a SyncState to its short token and
// degrades to "unknown" on any error so the indicator never blocks the UI. For a
// conflicted sync it also formats a capped per-file detail list + a resolution
// hint (§1d), and for a stalled sync it fires a debounced self-heal (MF5) — the
// internal/sync parsing/reconcile lives in the sync package; this adapter only
// shapes the typed summary into the dashboard's decoupled SyncHealth.
func dashboardSyncProber() dashboard.SyncProber {
	p := &syncProber{lastHealAt: make(map[session.ID]time.Time)}
	return p.probe
}

// dashboardSyncReaper builds the dashboard's orphaned-sync GC, backed by the
// Mutagen sync manager.
func dashboardSyncReaper() dashboard.SyncReaper {
	return reaperAdapter{}
}

type reaperAdapter struct{}

func (reaperAdapter) ListOrphans(ctx context.Context) ([]dashboard.OrphanSync, error) {
	mgr := syncManager()
	sessions, err := mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	// Scope to the current kube context (MF3) AND namespace ([V28]): a sync a
	// DIFFERENT context or namespace created carries that owner's sandbox-context /
	// sandbox-namespace label. The dashboard's GC confirms a candidate against the
	// current cluster's live set before terminating, but that live set is scoped to
	// the current context+namespace and can't see another owner's sessions — so an
	// out-of-scope sync would look orphaned and be wrongly reaped. Legacy syncs (no
	// label, "") fall through as before so they never become immortal.
	currentCtx := mgr.CurrentContext()
	currentNs := effectiveNamespace()
	var orphans []dashboard.OrphanSync
	for _, s := range sessions {
		if !syncpkg.IsOrphanStatus(s.Status) {
			continue
		}
		if s.Context != "" && s.Context != currentCtx {
			continue // another kube context owns it — not ours to reap
		}
		if s.Namespace != "" && s.Namespace != currentNs {
			continue // another namespace owns it — not ours to reap ([V28])
		}
		orphans = append(orphans, dashboard.OrphanSync{
			Identifier: s.Identifier,
			SessionID:  session.ID(s.SessionID),
		})
	}
	return orphans, nil
}

// effectiveNamespace resolves the k8s namespace the CLI operates in for GC
// scoping ([V28]) — the --namespace flag, or the default the client/backend
// applies when it is unset. It must match what the client stamps into the
// sandbox-namespace sync label (c.backend.Namespace()) so a same-namespace sync
// is never skipped as foreign. k8s namespaces are DNS-1123 labels, already valid
// mutagen label values, so no sanitization is needed here.
func effectiveNamespace() string {
	if namespaceFlag != "" {
		return namespaceFlag
	}
	return defaultCLINamespace
}

// defaultCLINamespace mirrors internal/k8s's defaultNamespace: the namespace the
// backend applies when --namespace is unset. Kept as a local const rather than an
// import so the GC's namespace comparison ([V28]) stays a pure-local computation.
const defaultCLINamespace = "agent-sessions"

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

// SaveTitle persists the user-chosen title. [V7] It writes a PARTIAL entry (only
// the identity fields Save needs plus the field it owns) and lets Save's locked
// load-merge fill the rest, so a concurrent snapshot/driver write can't have its
// newer LastEventSeq/Snapshot clobbered by a full entry this adapter loaded
// earlier outside the lock.
func (indexTitleStore) SaveTitle(id session.ID, title string) {
	idx, err := newIndex()
	if err != nil {
		return
	}
	_ = idx.Save(string(id), index.Entry{
		SandboxSessionID: string(id),
		SandboxName:      string(id),
		RenamedTitle:     title,
	})
}

// SaveAgentSessionID persists the backend's resume id (the Claude SDK session
// UUID today) for a session, preserving the rest of the entry. This is what later
// lets the CLI write a local ~/.claude/history.jsonl entry so the session resumes
// from the laptop.
func (indexTitleStore) SaveAgentSessionID(id session.ID, agentID string) {
	if agentID == "" {
		return
	}
	idx, err := newIndex()
	if err != nil {
		return
	}
	// [V7] Locked read-modify-write: this adapter both reads (dedup + the
	// ProjectPath the audit line needs) and writes, so it must run inside the
	// per-path lock rather than loading a full entry, mutating, and re-Saving it
	// (which would clobber a concurrent snapshot writer's newer LastEventSeq).
	var (
		changed     bool
		projectPath string
	)
	if uerr := idx.Update(string(id), func(e *index.Entry) {
		if e.AgentSessionID == agentID {
			return // already recorded; no change
		}
		e.AgentSessionID = agentID
		changed = true
		projectPath = e.ProjectPath
	}); uerr != nil || !changed {
		return
	}
	// Record the provenance to the append-only audit log (§1d). Done here, at the
	// single point a new mapping is learned, so the log grows once per session —
	// not on every event — and outlives the index entry a later destroy removes.
	appendTranscriptAudit(string(id), agentID, projectPath)
}

// transcriptAuditRecord is one line of the append-only transcript audit log
// (§1d). The pod syncs each session's Claude transcript into the UNSCOPED
// ~/.claude/projects tree, where it becomes locally `claude --resume`-able with
// no built-in link back to the sandbox session that produced it. This record
// ties the resumable Claude SDK session id to its sandbox session (and project),
// so that provenance is auditable even after `destroy` deletes the index entry.
type transcriptAuditRecord struct {
	Time            time.Time `json:"time"`
	SandboxSession  string    `json:"sandboxSession"`
	ClaudeSessionID string    `json:"claudeSessionId"`
	ProjectPath     string    `json:"projectPath,omitempty"`
}

// appendTranscriptAudit appends one mapping line to
// <remote-sessions>/transcript-audit.jsonl. Best-effort and side-effect-free on
// failure: an audit line is nice-to-have provenance, never load-bearing, so any
// error (no home dir, unwritable log) is swallowed. It does NOT change what the
// transcript sync copies — it only records the mapping the sync leaves implicit.
func appendTranscriptAudit(sandboxID, claudeID, projectPath string) {
	if claudeID == "" {
		return
	}
	root, err := index.DefaultRoot()
	if err != nil {
		return
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return
	}
	line, err := json.Marshal(transcriptAuditRecord{
		Time:            time.Now(),
		SandboxSession:  sandboxID,
		ClaudeSessionID: claudeID,
		ProjectPath:     projectPath,
	})
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(root, "transcript-audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
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
	// [V7] Partial entry + Save's locked merge — see SaveTitle.
	_ = idx.Save(string(id), index.Entry{
		SandboxSessionID: string(id),
		SandboxName:      string(id),
		AutoTitle:        title,
	})
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
	// [V7] Snapshot/LastEventSeq must OVERWRITE — a non-zero-deferred merge can't
	// express "advance the cursor" (a stale concurrent title Save loaded outside
	// the lock would otherwise reintroduce an older LastEventSeq/Snapshot). So this
	// uses Index.Update, a locked read-modify-write, rather than a partial Save.
	_ = idx.Update(string(id), func(e *index.Entry) {
		if e.SandboxSessionID == "" {
			e.SandboxSessionID = string(id)
		}
		if e.SandboxName == "" {
			e.SandboxName = string(id)
		}
		e.LastEventSeq = snap.LastSeq
		e.LastActivity = time.Now()
		e.Snapshot = &index.Snapshot{
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
	})
}

// indexEventCache implements dashboard.EventCache on top of the local index's
// per-session events.ndjson: the foreground transcript loads it on a cold open to
// rebuild history instantly and appends each non-delta event it streams. Best
// effort — a cache miss/failure just falls back to a full runner replay.
//
// It caches one open append handle per session (opened lazily on first append)
// rather than reopening the file per cached event, which the perf review flagged as
// ~5 syscalls on every event from both the foreground stream and every warm feed
// (§4 E10). Handles stay open for the process lifetime (the dashboard tracks only a
// handful of sessions); the OS reclaims them on exit.
type indexEventCache struct {
	mu      sync.Mutex
	writers map[session.ID]*index.CacheWriter
}

func newIndexEventCache() *indexEventCache {
	return &indexEventCache{writers: make(map[session.ID]*index.CacheWriter)}
}

func (c *indexEventCache) LoadEvents(id session.ID) ([]session.Event, error) {
	idx, err := newIndex()
	if err != nil {
		return nil, err
	}
	return idx.LoadCachedEvents(string(id))
}

func (c *indexEventCache) AppendEvent(id session.ID, ev session.Event) error {
	w, err := c.writer(id)
	if err != nil {
		return err
	}
	return w.Append(ev)
}

// writer returns the session's persistent append handle, opening (and caching) it
// on first use so later appends skip the per-event open/close (§4 E10).
func (c *indexEventCache) writer(id session.ID) (*index.CacheWriter, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if w := c.writers[id]; w != nil {
		return w, nil
	}
	idx, err := newIndex()
	if err != nil {
		return nil, err
	}
	w, err := idx.OpenCacheWriter(string(id))
	if err != nil {
		return nil, err
	}
	c.writers[id] = w
	return w, nil
}

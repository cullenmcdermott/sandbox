// Package index manages the local session index at
// ~/.local/share/sandbox/remote-sessions/<session-id>/. This is a local
// mirror/cache of remote session state. The remote PVC is authoritative
// while the session exists.
package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// saveLocks serializes the load-merge-write cycle in Save per index-entry path,
// closing the lost-update window where two concurrent Saves (e.g. a re-seed and
// a rename) could read the same on-disk entry and have the second write clobber
// the first. Keyed by the absolute session.json path so distinct entries never
// contend. Process-wide; cross-process races are out of scope (the local index
// is single-host).
var saveLocks sync.Map // map[string]*sync.Mutex

func lockForPath(path string) *sync.Mutex {
	m, _ := saveLocks.LoadOrStore(path, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// Index manages local session index files.
type Index struct {
	root string // ~/.local/share/sandbox/remote-sessions
}

// New creates an Index rooted at the given directory (typically
// ~/.local/share/sandbox/remote-sessions).
func New(root string) *Index {
	return &Index{root: root}
}

// DefaultRoot returns the default root path for the session index.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "sandbox", "remote-sessions"), nil
}

// NewDefault creates an Index at the default path.
func NewDefault() (*Index, error) {
	root, err := DefaultRoot()
	if err != nil {
		return nil, err
	}
	return New(root), nil
}

// Entry is the local index entry for a remote session.
type Entry struct {
	SandboxSessionID string `json:"sandboxSessionId"`
	Backend          string `json:"backend"`
	ProjectPath      string `json:"projectPath"`
	Model            string `json:"model,omitempty"`
	// RenamedTitle is the user-chosen display label for the session (T5). Empty
	// means fall back to the derived title (project basename / auto-title).
	RenamedTitle string `json:"renamedTitle,omitempty"`
	// AutoTitle is the runner-generated conversation summary (T6). Used as the
	// display title when RenamedTitle is empty; the derived basename is the
	// final fallback.
	AutoTitle string `json:"autoTitle,omitempty"`
	// AgentSessionID is the backend's own resume id reported by the runner
	// (session.started event) — the Claude Agent SDK session UUID for a claude-sdk
	// session (one backend per session ⇒ one resume id, §8 De-Claude rename). It is
	// the id `claude --resume <id>` expects, and the key used to write a local
	// ~/.claude/history.jsonl entry so a k8s session shows up in the interactive
	// resume picker on the laptop. Load migrates the pre-§8 on-disk key
	// "claudeSessionId" into this field; Save then rewrites it as "agentSessionId".
	AgentSessionID string `json:"agentSessionId,omitempty"`
	Namespace      string `json:"namespace"`
	SandboxName    string `json:"sandboxName"`
	RunnerToken    string `json:"-"` // stored separately, not in JSON
	// WorktreePath, WorktreeBranch, and RepoRoot record the session's per-session
	// git worktree (empty for a non-git / WorktreeOff session). They let teardown
	// and reaping reason about the worktree without re-running git discovery:
	// WorktreePath is the local worktree dir (also the Mutagen alpha / pod cwd),
	// WorktreeBranch is its auto-branch (sandbox/<id>) that preserves committed
	// work after the dir is removed, and RepoRoot is the parent repo's toplevel
	// (`git -C RepoRoot worktree remove/prune` targets it).
	WorktreePath    string    `json:"worktreePath,omitempty"`
	WorktreeBranch  string    `json:"worktreeBranch,omitempty"`
	RepoRoot        string    `json:"repoRoot,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	LastActivity    time.Time `json:"lastActivity"`
	LastEventSeq    uint64    `json:"lastEventSeq"`
	MutagenSessions []string  `json:"mutagenSessions,omitempty"`
	ForwardHTTPPort int       `json:"forwardHttpPort,omitempty"`
	ForwardSSHPort  int       `json:"forwardSshPort,omitempty"`
	// Snapshot is the cached live-display state of the session as of
	// LastEventSeq. The dashboard seeds its list from it on launch (so rows show
	// real status/usage immediately) and resumes the runner SSE stream from
	// LastEventSeq instead of replaying the whole event history — which is what
	// made the TUI flash notifications and count usage up from zero on every
	// launch. Nil until the dashboard has observed at least one live event.
	Snapshot *Snapshot `json:"snapshot,omitempty"`
}

// Snapshot is the cached dashboard read-model for a session, captured at
// Entry.LastEventSeq. It carries only display-derived fields (the cluster State
// and titles live on the Entry itself), so it can be regenerated from the event
// stream at any time and is safe to discard.
type Snapshot struct {
	// DashStatus is the six-state dashboard status label (Session.DashStatus's
	// String()): "busy", "waiting", "needs-input", "idle", "suspended", "failed".
	DashStatus            string  `json:"dashStatus,omitempty"`
	PendingPermissionID   string  `json:"pendingPermissionId,omitempty"`
	PendingPermissionTool string  `json:"pendingPermissionTool,omitempty"`
	Model                 string  `json:"model,omitempty"`
	InputTokens           int     `json:"inputTokens,omitempty"`
	OutputTokens          int     `json:"outputTokens,omitempty"`
	CacheReadTokens       int     `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens      int     `json:"cacheWriteTokens,omitempty"`
	TotalCostUSD          float64 `json:"totalCostUsd,omitempty"`
}

// validateID checks that id does not path-traverse outside root (C5).
func validateID(root, id string) error {
	joined := filepath.Join(root, id)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	joinedAbs, err := filepath.Abs(joined)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, joinedAbs)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("index: id %q escapes root", id)
	}
	return nil
}

// Save writes the session index entry to disk.
//
// To avoid lost updates, Save performs a locked load-merge-write: it reads any
// existing on-disk entry and fills each zero-valued field of entry from it
// before writing. This means a caller that constructs a partial Entry (e.g. a
// session re-seed that only sets the cluster-known fields) will not clobber
// locally-owned fields that another path persisted earlier — notably
// RenamedTitle and AutoTitle, which the cluster State does not carry. Callers
// that intend to set a field non-empty always win; only zero values defer to
// the existing entry. The whole cycle is guarded by a per-path lock so two
// concurrent Saves serialize rather than racing on the read.
func (i *Index) Save(id string, entry Entry) error {
	if err := validateID(i.root, id); err != nil {
		return err
	}
	dir := filepath.Join(i.root, id)
	path := filepath.Join(dir, "session.json")

	lock := lockForPath(path)
	lock.Lock()
	defer lock.Unlock()

	// Load-merge: fill zero-valued incoming fields from the existing on-disk
	// entry so a partial Save preserves previously-persisted values.
	if prev, err := i.Load(id); err == nil {
		entry = mergeEntry(prev, entry)
	}

	return writeEntryFile(dir, path, entry)
}

// Update applies fn to the session's index entry as a locked read-modify-write:
// it loads the current on-disk entry (or a minimal one seeded with the id when
// none exists), passes a pointer to fn to mutate in place, and writes the result
// — all inside the same per-path lock Save uses. [V7] It is for callers that must
// OVERWRITE a field the zero-defer merge would otherwise preserve from a stale
// in-memory copy — notably Snapshot / LastEventSeq, which a snapshot writer must
// be able to advance without a concurrent title/driver Save reintroducing an
// older value it loaded outside the lock. Save's partial-entry merge is the right
// tool when a caller only OWNS a subset of fields; Update is the right tool when
// a caller must set a field to a value that a merge cannot express (e.g. an
// unconditional overwrite that also depends on the current on-disk value).
func (i *Index) Update(id string, fn func(*Entry)) error {
	if err := validateID(i.root, id); err != nil {
		return err
	}
	dir := filepath.Join(i.root, id)
	path := filepath.Join(dir, "session.json")

	lock := lockForPath(path)
	lock.Lock()
	defer lock.Unlock()

	entry, err := i.Load(id)
	if err != nil {
		entry = Entry{SandboxSessionID: id, SandboxName: id}
	}
	fn(&entry)
	return writeEntryFile(dir, path, entry)
}

// writeEntryFile marshals entry and writes it to path via a temp+rename atomic
// write (C3), creating dir as needed. Callers hold the per-path lock. Shared by
// Save and Update so both use the identical crash-safe write.
func writeEntryFile(dir, path string, entry Entry) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("index: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	// C3: atomic write via temp+rename so a crash mid-write can't corrupt
	// the index entry (plain WriteFile truncates before writing).
	tmp, err := os.CreateTemp(dir, ".session-json-tmp-*")
	if err != nil {
		return fmt.Errorf("index: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("index: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("index: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("index: close temp: %w", err)
	}
	return os.Rename(tmpName, path)
}

// mergeEntry returns next with each zero-valued field filled from prev. A caller
// that sets a field non-empty always wins; only zero values defer to the
// existing on-disk entry. RunnerToken is not persisted (json:"-") so it is left
// untouched — Save never reads it from disk and callers carry it out-of-band.
func mergeEntry(prev, next Entry) Entry {
	if next.SandboxSessionID == "" {
		next.SandboxSessionID = prev.SandboxSessionID
	}
	if next.Backend == "" {
		next.Backend = prev.Backend
	}
	if next.ProjectPath == "" {
		next.ProjectPath = prev.ProjectPath
	}
	if next.Model == "" {
		next.Model = prev.Model
	}
	if next.RenamedTitle == "" {
		next.RenamedTitle = prev.RenamedTitle
	}
	if next.AutoTitle == "" {
		next.AutoTitle = prev.AutoTitle
	}
	if next.Namespace == "" {
		next.Namespace = prev.Namespace
	}
	if next.SandboxName == "" {
		next.SandboxName = prev.SandboxName
	}
	if next.AgentSessionID == "" {
		next.AgentSessionID = prev.AgentSessionID
	}
	if next.WorktreePath == "" {
		next.WorktreePath = prev.WorktreePath
	}
	if next.WorktreeBranch == "" {
		next.WorktreeBranch = prev.WorktreeBranch
	}
	if next.RepoRoot == "" {
		next.RepoRoot = prev.RepoRoot
	}
	if next.CreatedAt.IsZero() {
		next.CreatedAt = prev.CreatedAt
	}
	if next.LastActivity.IsZero() {
		next.LastActivity = prev.LastActivity
	}
	if next.LastEventSeq == 0 {
		next.LastEventSeq = prev.LastEventSeq
	}
	if next.MutagenSessions == nil {
		next.MutagenSessions = prev.MutagenSessions
	}
	if next.ForwardHTTPPort == 0 {
		next.ForwardHTTPPort = prev.ForwardHTTPPort
	}
	if next.ForwardSSHPort == 0 {
		next.ForwardSSHPort = prev.ForwardSSHPort
	}
	if next.Snapshot == nil {
		next.Snapshot = prev.Snapshot
	}
	return next
}

// Load reads a session index entry.
func (i *Index) Load(id string) (Entry, error) {
	// [V30] Enforce the same C5 traversal guard the write side (Save/Delete)
	// applies, so a `../`-laden id can't read a file outside the index root. A
	// rejected id surfaces as an error, exactly like a missing entry — callers
	// already handle Load's error return.
	if err := validateID(i.root, id); err != nil {
		return Entry{}, err
	}
	path := filepath.Join(i.root, id, "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("index: load %s: %w", id, err)
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("index: decode %s: %w", id, err)
	}
	// Backward-compat migration (§8 De-Claude): entries written before the
	// AgentSessionID rename persisted the resume id under "claudeSessionId". Accept
	// the old key when the new one is absent so existing sessions stay resumable;
	// the next Save rewrites it as "agentSessionId".
	if entry.AgentSessionID == "" {
		var legacy struct {
			ClaudeSessionID string `json:"claudeSessionId"`
		}
		if json.Unmarshal(data, &legacy) == nil && legacy.ClaudeSessionID != "" {
			entry.AgentSessionID = legacy.ClaudeSessionID
		}
	}
	return entry, nil
}

// Delete removes a session index entry.
func (i *Index) Delete(id string) error {
	if err := validateID(i.root, id); err != nil {
		return err
	}
	dir := filepath.Join(i.root, id)
	return os.RemoveAll(dir)
}

// RecentProjects returns the distinct ProjectPaths of indexed sessions,
// most-recently-active first (LastActivity, falling back to CreatedAt for
// entries that never recorded activity), capped at limit (limit <= 0 means
// uncapped). It backs the recents rows of the dashboard create overlay's
// directory picker (T10). Best-effort: a listing error yields nil — the picker
// still offers cwd + free-text entry.
func (i *Index) RecentProjects(limit int) []string {
	entries, err := i.List()
	if err != nil {
		return nil
	}
	activity := func(e Entry) time.Time {
		if !e.LastActivity.IsZero() {
			return e.LastActivity
		}
		return e.CreatedAt
	}
	sort.SliceStable(entries, func(a, b int) bool {
		return activity(entries[a]).After(activity(entries[b]))
	})
	seen := make(map[string]bool, len(entries))
	var out []string
	for _, e := range entries {
		p := e.ProjectPath
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// List returns all local session index entries.
func (i *Index) List() ([]Entry, error) {
	entries, err := os.ReadDir(i.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("index: list %s: %w", i.root, err)
	}
	var result []Entry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// The state dir also holds non-session sibling dirs (ssh/, worktrees/);
		// a dir with no session.json is simply not a session, so skip it silently
		// rather than logging it as corrupt. Session ids are never "ssh"/"worktrees"
		// (they are <backend>-<hash>-<rand>), so this cannot hide a real entry.
		if _, serr := os.Stat(filepath.Join(i.root, e.Name(), "session.json")); errors.Is(serr, os.ErrNotExist) {
			continue
		}
		entry, err := i.Load(e.Name())
		if err != nil {
			// Don't fail the whole listing over one unreadable/corrupt entry,
			// but don't swallow it silently either: a corrupt session.json is a
			// real condition the operator should see. Skip it and log to stderr
			// with the id and error so it's observable.
			log.Printf("index: skipping corrupt session entry %q: %v", e.Name(), err)
			continue
		}
		result = append(result, entry)
	}
	return result, nil
}

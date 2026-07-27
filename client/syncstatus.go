package client

import (
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
)

// Public type aliases for the typed sync-health model. These are identical to
// the internal/sync types (a Go alias is type identity, not a copy), so a
// caller and the CLI/TUI share the exact same structs with no conversion.
type (
	// SyncState is a reduced view of a session's Mutagen file-sync health.
	SyncState = syncpkg.SyncState
	// SyncConflict is one per-path sync conflict: Path is the workspace-relative
	// path, Local/Remote record which endpoint(s) changed it (mutagen's own
	// vocabulary is alpha/beta — the local workspace and the pod, respectively).
	SyncConflict = syncpkg.Conflict
	// SyncStatus is a session's typed file-sync health, as returned by
	// (*Client).SyncStatus.
	SyncStatus = syncpkg.Status
)

const (
	// SyncUnknown means no sync session exists for this session, or its state
	// could not be determined. See SyncStatus.Detail for the reason.
	SyncUnknown = syncpkg.SyncUnknown
	// SyncSynced means the sync is watching for changes with no conflicts.
	SyncSynced = syncpkg.SyncSynced
	// SyncSyncing means the sync is actively scanning, staging, or otherwise
	// transitioning — in flight, not yet settled.
	SyncSyncing = syncpkg.SyncSyncing
	// SyncPaused means the sync is paused: a suspended session, an explicit
	// pause, or a best-effort resume that failed to un-pause. Self-healable —
	// resuming the session (or SyncResume) clears it.
	SyncPaused = syncpkg.SyncPaused
	// SyncStalled means the sync's transport is down (a dropped connection, an
	// unreachable pod). Self-healable — it typically clears on its own once the
	// pod is reachable again, or via SyncResume.
	SyncStalled = syncpkg.SyncStalled
	// SyncSafetyHalted means mutagen halted the sync on a safety condition (its
	// root emptied, was deleted, or changed type) — mutagen's guard against
	// silently propagating a mass deletion. This is NOT self-healable: resuming
	// a safety halt is mutagen's documented CONFIRM-and-propagate action, so it
	// must never be automated. It needs a human to inspect both sides and decide.
	SyncSafetyHalted = syncpkg.SyncSafetyHalted
	// SyncConflicted means one or more files were changed on both sides between
	// syncs and mutagen refuses to pick a winner. NOT self-healable — see
	// SyncConflictHint for the one-line resolution and SyncStatus.Conflicts for
	// the per-file detail.
	SyncConflicted = syncpkg.SyncConflicted

	// SyncConflictHint is the one-line resolution reminder for a SyncConflicted
	// sync: delete the unwanted copy on one side, and sync resumes automatically.
	SyncConflictHint = syncpkg.ConflictResolutionHint
)

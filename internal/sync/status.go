package sync

import (
	"context"
	"encoding/json"
	"strings"
)

// SyncState is a reduced view of a session's Mutagen sync health for the TUI.
type SyncState int

const (
	SyncUnknown SyncState = iota // no sessions / could not determine
	SyncSynced                   // watching for changes, no conflicts
	SyncSyncing                  // scanning / staging / transitioning
	SyncStalled                  // halted, errored, or has conflicts
)

func (s SyncState) String() string {
	switch s {
	case SyncSynced:
		return "synced"
	case SyncSyncing:
		return "syncing"
	case SyncStalled:
		return "stalled"
	default:
		return "unknown"
	}
}

// mutagenSession is the subset of `mutagen sync list --template '{{json .}}'`
// output we care about.
type mutagenSession struct {
	Status    string `json:"status"`
	Conflicts []any  `json:"conflicts"`
}

// StatusSummary returns a reduced SyncState for the given session's Mutagen
// sessions. The worst state across the session's syncs wins (stalled >
// syncing > synced) so a single stuck endpoint surfaces.
func (m *Manager) StatusSummary(ctx context.Context, sessionID string) (SyncState, error) {
	out, err := m.r.Output(ctx, nil,
		"sync", "list",
		"--label-selector="+sessionLabel(sessionID),
		"--template", "{{json .}}",
	)
	if err != nil {
		return SyncUnknown, err
	}
	return parseSyncState(out), nil
}

func parseSyncState(out []byte) SyncState {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return SyncUnknown
	}
	var sessions []mutagenSession
	if err := json.Unmarshal([]byte(trimmed), &sessions); err != nil {
		// Older mutagen prints a single object, not an array.
		var one mutagenSession
		if json.Unmarshal([]byte(trimmed), &one) == nil {
			sessions = []mutagenSession{one}
		}
	}
	if len(sessions) == 0 {
		return SyncUnknown
	}
	worst := SyncSynced
	for _, s := range sessions {
		st := classify(s)
		if st > worst { // ordering: Stalled(3) > Syncing(2) > Synced(1)
			worst = st
		}
	}
	return worst
}

func classify(s mutagenSession) SyncState {
	if len(s.Conflicts) > 0 {
		return SyncStalled
	}
	status := strings.ToLower(s.Status)
	switch {
	case strings.Contains(status, "halted"),
		strings.Contains(status, "error"),
		strings.Contains(status, "problem"):
		return SyncStalled
	case strings.Contains(status, "watching"):
		return SyncSynced
	default:
		// scanning / staging / reconciling / transitioning / waiting → in-flight
		return SyncSyncing
	}
}

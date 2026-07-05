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
	SyncStalled                  // halted or errored (transport-level; may self-heal)
	// SyncConflicted: file conflicts need user resolution. Distinct from (and
	// ranked above) SyncStalled — a transport stall can clear on reconnect, but a
	// conflict is stuck until resolved, so it wins the worst-of reducer and gets
	// its own glyph instead of masquerading as a transport error.
	SyncConflicted
)

func (s SyncState) String() string {
	switch s {
	case SyncSynced:
		return "synced"
	case SyncSyncing:
		return "syncing"
	case SyncStalled:
		return "stalled"
	case SyncConflicted:
		return "conflicted"
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
// sessions. The worst state across the session's syncs wins (conflicted >
// stalled > syncing > synced) so a single stuck endpoint surfaces.
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
		if st > worst { // ordering: Conflicted(4) > Stalled(3) > Syncing(2) > Synced(1)
			worst = st
		}
	}
	return worst
}

// StagingPhase returns a short, human progress word for an in-flight sync
// ("connecting", "scanning", "uploading", "applying") or "" when the session is
// idle/watching/unknown. It gives the connect screen a live hint during the
// initial blocking flush instead of a frozen "Syncing files". Robust by design:
// it substring-matches the coarse status string, so it survives mutagen status
// wording changes across versions.
func (m *Manager) StagingPhase(ctx context.Context, sessionID string) string {
	out, err := m.r.Output(ctx, nil,
		"sync", "list",
		"--label-selector="+sessionLabel(sessionID),
		"--template", "{{json .}}",
	)
	if err != nil {
		return ""
	}
	return parseStagingPhase(out)
}

func parseStagingPhase(out []byte) string {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var sessions []mutagenSession
	if err := json.Unmarshal([]byte(trimmed), &sessions); err != nil {
		var one mutagenSession
		if json.Unmarshal([]byte(trimmed), &one) == nil {
			sessions = []mutagenSession{one}
		}
	}
	best := ""
	for _, s := range sessions {
		if p := stagingPhase(s.Status); stagingRank(p) > stagingRank(best) {
			best = p
		}
	}
	return best
}

// stagingPhase maps a coarse mutagen status string to a progress word.
func stagingPhase(status string) string {
	st := strings.ToLower(status)
	switch {
	case strings.Contains(st, "stag"):
		return "uploading"
	case strings.Contains(st, "transition"), strings.Contains(st, "apply"), strings.Contains(st, "reconcil"):
		return "applying"
	case strings.Contains(st, "scan"):
		return "scanning"
	case strings.Contains(st, "connect"), strings.Contains(st, "wait"):
		return "connecting"
	default:
		// watching / idle / unknown → no useful in-flight detail.
		return ""
	}
}

// stagingRank orders phases so the most-active one wins across a session's syncs.
func stagingRank(p string) int {
	switch p {
	case "uploading":
		return 4
	case "applying":
		return 3
	case "scanning":
		return 2
	case "connecting":
		return 1
	default:
		return 0
	}
}

func classify(s mutagenSession) SyncState {
	if len(s.Conflicts) > 0 {
		return SyncConflicted
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

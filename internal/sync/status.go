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
	Status    string            `json:"status"`
	Conflicts []mutagenConflict `json:"conflicts"`
}

// mutagenConflict mirrors one per-path sync conflict from mutagen's conflicts[]
// JSON: the change(s) each endpoint made to a path both sides touched. Mutagen
// names the endpoints alpha (the local workspace) and beta (the pod). Only the
// paths are decoded — the full before/after Entry trees are ignored.
//
// Field names are best-effort against mutagen's shape (camelCase alphaChanges /
// betaChanges, each a list of {path,...}); parsing is deliberately tolerant, so
// an unrecognized/older shape decodes to empty change lists. Such a conflict
// still counts toward SyncConflicted (classify only checks len>0); we simply
// can't name its path and conflictsFrom emits a generic entry (§1d).
type mutagenConflict struct {
	AlphaChanges []mutagenChange `json:"alphaChanges"`
	BetaChanges  []mutagenChange `json:"betaChanges"`
}

// mutagenChange is one endpoint's change to a path within a conflict. Only the
// path is used for the per-file summary.
type mutagenChange struct {
	Path string `json:"path"`
}

// Conflict is one per-path sync conflict, resolved from mutagen's conflicts[]
// JSON into a typed summary. Path is the workspace-relative path in conflict;
// Alpha/Beta record which endpoint(s) changed it (alpha = local workspace, beta
// = pod). Both true means each side edited the same path — the classic
// two-way-safe standoff mutagen halts on.
type Conflict struct {
	Path  string
	Alpha bool
	Beta  bool
}

// Describe renders a Conflict as a short human line for the sync detail pane.
func (c Conflict) Describe() string {
	switch {
	case c.Alpha && c.Beta:
		return c.Path + " (both sides changed it)"
	case c.Alpha:
		return c.Path + " (changed locally)"
	case c.Beta:
		return c.Path + " (changed on the pod)"
	default:
		return c.Path
	}
}

// ConflictSummary is the typed detail behind a SyncConflicted status: the
// per-file conflicts (deduped by path, source-order preserved) and their count.
type ConflictSummary struct {
	Files []Conflict
	Total int
}

// ConflictResolutionHint is the one-line reminder shown alongside a conflicted
// sync. two-way-safe halts a conflicted path (it never picks a winner), so it
// stays stuck until a human removes the unwanted copy on ONE side — after which
// sync resumes on its own.
const ConflictResolutionHint = "resolve: delete the unwanted copy on one side (local or pod); sync then resumes automatically"

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

// decodeSessions parses `mutagen sync list --template '{{json .}}'` output into
// the session subset we care about, tolerating both the array form and the older
// single-object form. Returns nil for empty/null/garbage output.
func decodeSessions(out []byte) []mutagenSession {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var sessions []mutagenSession
	if err := json.Unmarshal([]byte(trimmed), &sessions); err != nil {
		// Older mutagen prints a single object, not an array.
		var one mutagenSession
		if json.Unmarshal([]byte(trimmed), &one) == nil {
			sessions = []mutagenSession{one}
		}
	}
	return sessions
}

func parseSyncState(out []byte) SyncState {
	return worstState(decodeSessions(out))
}

// worstState reduces a session set to the worst SyncState across its syncs so a
// single stuck endpoint surfaces (Conflicted > Stalled > Syncing > Synced).
func worstState(sessions []mutagenSession) SyncState {
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

// StatusDetail returns a session's worst SyncState together with a typed
// per-file ConflictSummary (empty unless the state is SyncConflicted). It is the
// detail-carrying counterpart of StatusSummary, used by the dashboard to show
// which files conflicted and a resolution hint. One `mutagen sync list` exec.
func (m *Manager) StatusDetail(ctx context.Context, sessionID string) (SyncState, ConflictSummary, error) {
	out, err := m.r.Output(ctx, nil,
		"sync", "list",
		"--label-selector="+sessionLabel(sessionID),
		"--template", "{{json .}}",
	)
	if err != nil {
		return SyncUnknown, ConflictSummary{}, err
	}
	sessions := decodeSessions(out)
	files := conflictsFrom(sessions)
	return worstState(sessions), ConflictSummary{Files: files, Total: len(files)}, nil
}

// conflictsFrom flattens every session's conflicts[] into a deduped, source-order
// list of per-file Conflicts. A path that both endpoints changed merges into one
// entry with Alpha && Beta. A conflict whose shape we couldn't parse (no paths)
// still yields a generic entry so the count stays honest (§1d defensive parsing).
func conflictsFrom(sessions []mutagenSession) []Conflict {
	byPath := map[string]int{} // path -> index into out
	var out []Conflict
	add := func(path string, alpha, beta bool) {
		if path == "" {
			path = "(path unavailable)"
		}
		if i, ok := byPath[path]; ok {
			out[i].Alpha = out[i].Alpha || alpha
			out[i].Beta = out[i].Beta || beta
			return
		}
		byPath[path] = len(out)
		out = append(out, Conflict{Path: path, Alpha: alpha, Beta: beta})
	}
	for _, s := range sessions {
		for _, cf := range s.Conflicts {
			named := false
			for _, ch := range cf.AlphaChanges {
				if ch.Path != "" {
					add(ch.Path, true, false)
					named = true
				}
			}
			for _, ch := range cf.BetaChanges {
				if ch.Path != "" {
					add(ch.Path, false, true)
					named = true
				}
			}
			if !named {
				add("", false, false)
			}
		}
	}
	return out
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
	sessions := decodeSessions(out)
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

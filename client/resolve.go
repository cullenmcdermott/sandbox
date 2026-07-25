package client

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// resolve.go turns what a human types into a session.
//
// Every session-taking command has, until now, treated its argument as a
// literal session id — the full `claude-pane-df80e6-3e1d6e81` string, typed or
// pasted. That is the one form nobody remembers. People know their sessions by
// what they are working on ("the auth refactor"), by where they are standing
// (the shell is already inside the worktree), or by the branch they want to
// merge. Resolution belongs in the SDK rather than in each cobra command
// because it is the same question every caller asks, and because an external
// consumer building its own front-end needs the identical answer.
//
// It resolves OFFLINE, against the local session index only — no apiserver
// round-trip. Two reasons, both load-bearing: shell completion runs this on
// every TAB and must return in milliseconds, and a laptop with no cluster
// reachable should still be able to say where a session's worktree is. The
// consequence is that a match is a match against what this machine last
// recorded, which may include a session the cluster has since reaped. Callers
// that need liveness join the result against List.

// MatchKind names the way a query matched a session. It doubles as the
// specificity ranking used to order candidates: a query that lands an exact id
// beats one that merely appears in a title, and ResolveSession only reports
// ambiguity among candidates of the SAME kind (see its doc).
type MatchKind string

// The match kinds, most specific first. resolveRank encodes the order.
const (
	// MatchAny is the kind reported for every session when the query is empty:
	// "no filter, here is everything this machine knows about". Completion uses
	// it to list candidates before the user has typed anything.
	MatchAny MatchKind = "any"

	// MatchID is an exact, whole session id. It short-circuits: an id that is
	// also a prefix of some longer id is still unambiguously that session.
	MatchID MatchKind = "id"

	// MatchWorktreePath means the query is the session's worktree directory, or
	// a path inside it — which is how a shell standing in the worktree resolves
	// itself with no argument at all.
	MatchWorktreePath MatchKind = "worktree-path"

	// MatchBranch is an exact match on the session's branch: the auto-branch
	// sandbox/<id>, or whatever ConvertToBranch renamed it to. Typing the branch
	// you are about to merge is a natural way to name the session that produced it.
	MatchBranch MatchKind = "branch"

	// MatchIDPrefix is a leading substring of the id. The random suffix makes
	// even a few characters selective in practice.
	MatchIDPrefix MatchKind = "id-prefix"

	// MatchTitle is a case-insensitive substring of the display title (the user's
	// rename, else the runner's auto-title, else the project basename).
	MatchTitle MatchKind = "title"

	// MatchProjectPath means the query is the session's project root, or inside
	// it. Deliberately the weakest kind: every session on a repo shares it, so it
	// identifies a session only when there is exactly one.
	MatchProjectPath MatchKind = "project-path"
)

// resolveRank orders the kinds by specificity (lower is more specific). Kinds
// absent from the map sort last.
var resolveRank = map[MatchKind]int{
	MatchID:           0,
	MatchWorktreePath: 1,
	MatchBranch:       2,
	MatchIDPrefix:     3,
	MatchTitle:        4,
	MatchProjectPath:  5,
	MatchAny:          6,
}

// SessionMatch is one candidate answer to a resolution query, carrying enough
// context for a caller to render a disambiguation list without a second lookup.
// The fields come from the local index, so they describe the session as of this
// machine's last write — see the package note on offline resolution.
type SessionMatch struct {
	// ID is the session id: the value every other SDK call takes.
	ID ID

	// Title is the display title, resolved through the same fallback chain the
	// dashboard uses: the user's rename, else the runner's auto-title, else the
	// project directory's basename. Never empty in practice, but a hand-corrupted
	// index entry could leave it so — render defensively.
	Title string

	// Backend is the session's agent backend ("claude-pane", "opencode-server",
	// "codex-app-server"), for callers that disambiguate by it.
	Backend string

	// ProjectPath is the repo root the session belongs to. WorktreePath is its
	// per-session git worktree and Branch that worktree's branch; both are empty
	// for a non-git or WorktreeOff session, where the workspace IS ProjectPath.
	ProjectPath  string
	WorktreePath string
	Branch       string

	// LastActivity is the last activity this machine recorded, falling back to
	// the creation time when the session never reported any. It is the tiebreak
	// within a match kind — most recently touched first, which is nearly always
	// the one meant.
	LastActivity time.Time

	// MatchedBy records which rule matched, both to explain a choice to the user
	// and because ResolveSession's ambiguity rule is kind-scoped.
	MatchedBy MatchKind
}

// ErrNoSessionMatch reports that a query matched no session on this machine.
// Because resolution is offline, this means "nothing in the local index" — a
// session created on another machine, or one whose index entry was removed, is
// invisible here even if it is alive in the cluster.
var ErrNoSessionMatch = errors.New("sandbox: no session matches")

// ErrAmbiguousSession is the sentinel behind every AmbiguousSessionError, so a
// caller can branch with errors.Is without depending on the concrete type.
var ErrAmbiguousSession = errors.New("sandbox: session query is ambiguous")

// AmbiguousSessionError reports that a query matched several sessions equally
// well. It carries the candidates so a CLI can print them (or a TUI can offer a
// picker) instead of making the user re-run a command to find out what it was
// choosing between.
type AmbiguousSessionError struct {
	Query      string
	Candidates []SessionMatch
}

func (e *AmbiguousSessionError) Error() string {
	ids := make([]string, 0, len(e.Candidates))
	for _, c := range e.Candidates {
		ids = append(ids, string(c.ID))
	}
	return fmt.Sprintf("sandbox: %q matches %d sessions (%s)", e.Query, len(e.Candidates), strings.Join(ids, ", "))
}

// Unwrap makes errors.Is(err, ErrAmbiguousSession) true.
func (e *AmbiguousSessionError) Unwrap() error { return ErrAmbiguousSession }

// ResolveSessions returns every locally-known session matching query, ranked
// most-specific-first and, within a rank, most-recently-active first.
//
// An empty query returns all sessions (kind MatchAny) — the listing shell
// completion offers before anything is typed. A query that exactly equals a
// session id short-circuits to that one session alone.
//
// Otherwise the query is tried against each session as: a path (worktree first,
// then project root — a query is treated as a path only when it looks like one,
// and matches the directory itself or anything under it), an exact branch name,
// an id prefix, and a case-insensitive title substring. A session appears at
// most once, under its strongest matching kind.
//
// It never returns an error for "nothing matched" — that is an empty slice, and
// the distinction matters to completion, which must stay silent rather than
// error. Use ResolveSession when exactly one session is required.
func (c *Client) ResolveSessions(ctx context.Context, query string) ([]SessionMatch, error) {
	// Resolution is pure local I/O, but honoring cancellation keeps the method
	// well-behaved inside a caller's timeout and leaves room for a future
	// liveness join without a signature change.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := c.index.List()
	if err != nil {
		return nil, fmt.Errorf("sandbox: read local session index: %w", err)
	}

	q := strings.TrimSpace(query)
	matches := make([]SessionMatch, 0, len(entries))
	for _, e := range entries {
		kind, ok := matchEntry(e, q)
		if !ok {
			continue
		}
		m := sessionMatch(e, kind)
		// An exact id is the end of the search: returning the one session the
		// user unambiguously named, even when that id is also the prefix of
		// another, is the only defensible reading of a full id.
		if kind == MatchID {
			return []SessionMatch{m}, nil
		}
		matches = append(matches, m)
	}
	sortMatches(matches)
	return matches, nil
}

// ResolveSession resolves query to exactly one session.
//
// It returns ErrNoSessionMatch when nothing matched, and an
// *AmbiguousSessionError (wrapping ErrAmbiguousSession, carrying the
// candidates) when several matched equally well.
//
// "Equally well" is scoped to the match kind: if one session matches by id
// prefix and three others merely share the project path, the id-prefix match
// wins outright rather than dragging the weaker candidates into an ambiguity
// error. Ambiguity within a kind is real ambiguity — two sessions whose titles
// both contain "auth" are two answers to the same question, and the caller must
// choose.
func (c *Client) ResolveSession(ctx context.Context, query string) (SessionMatch, error) {
	matches, err := c.ResolveSessions(ctx, query)
	if err != nil {
		return SessionMatch{}, err
	}
	if len(matches) == 0 {
		return SessionMatch{}, fmt.Errorf("%w: %q", ErrNoSessionMatch, query)
	}
	// Sorted most-specific-first, so the ambiguity set is the leading run that
	// shares the winner's kind.
	best := matches[0].MatchedBy
	tied := 1
	for _, m := range matches[1:] {
		if m.MatchedBy != best {
			break
		}
		tied++
	}
	if tied > 1 {
		return SessionMatch{}, &AmbiguousSessionError{
			Query:      query,
			Candidates: append([]SessionMatch(nil), matches[:tied]...),
		}
	}
	return matches[0], nil
}

// matchEntry reports the strongest kind by which q matches e, or false when it
// does not match at all. An empty q matches everything (MatchAny).
func matchEntry(e index.Entry, q string) (MatchKind, bool) {
	if q == "" {
		return MatchAny, true
	}
	id := e.SandboxSessionID
	switch {
	case q == id:
		return MatchID, true
	case looksLikePath(q) && pathWithin(q, e.WorktreePath):
		return MatchWorktreePath, true
	case e.WorktreeBranch != "" && q == e.WorktreeBranch:
		return MatchBranch, true
	case id != "" && strings.HasPrefix(id, q):
		return MatchIDPrefix, true
	case strings.Contains(strings.ToLower(entryTitle(e)), strings.ToLower(q)):
		return MatchTitle, true
	case looksLikePath(q) && pathWithin(q, e.ProjectPath):
		return MatchProjectPath, true
	}
	return "", false
}

// sessionMatch projects an index entry into the public match shape.
func sessionMatch(e index.Entry, kind MatchKind) SessionMatch {
	return SessionMatch{
		ID:           ID(e.SandboxSessionID),
		Title:        entryTitle(e),
		Backend:      e.Backend,
		ProjectPath:  e.ProjectPath,
		WorktreePath: e.WorktreePath,
		Branch:       e.WorktreeBranch,
		LastActivity: entryActivity(e),
		MatchedBy:    kind,
	}
}

// entryTitle resolves the display title: the user's rename, else the runner's
// auto-title, else the project directory's basename. Same chain the dashboard
// and the resume picker use — a session must not be called one thing on screen
// and another on the command line.
func entryTitle(e index.Entry) string {
	if e.RenamedTitle != "" {
		return e.RenamedTitle
	}
	if e.AutoTitle != "" {
		return e.AutoTitle
	}
	if e.ProjectPath != "" {
		return filepath.Base(e.ProjectPath)
	}
	return ""
}

// entryActivity is the recency key: last recorded activity, falling back to
// creation for a session that never reported any.
func entryActivity(e index.Entry) time.Time {
	if !e.LastActivity.IsZero() {
		return e.LastActivity
	}
	return e.CreatedAt
}

// looksLikePath reports whether a query should be tried as a filesystem path.
// Requiring a separator (or a "." leader) keeps a bare word like "auth" from
// being resolved against the filesystem, where it would match any session whose
// project happens to sit in a directory of that name.
func looksLikePath(q string) bool {
	return strings.ContainsRune(q, filepath.Separator) || q == "." || strings.HasPrefix(q, "."+string(filepath.Separator))
}

// pathWithin reports whether query names dir or something inside it. Both sides
// are made absolute and cleaned first, so "." resolves against the caller's cwd
// (which is the point — a shell standing in a worktree resolves itself) and a
// trailing slash or ".." segment does not defeat the comparison.
//
// It is deliberately lexical: no symlink resolution, no stat. A session's
// worktree may have been deleted, and answering "which session is this path"
// must not depend on the path still existing.
func pathWithin(query, dir string) bool {
	if dir == "" {
		return false
	}
	qAbs, err := filepath.Abs(query)
	if err != nil {
		return false
	}
	dAbs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dAbs, qAbs)
	if err != nil {
		return false
	}
	// rel == "." is the directory itself; anything that starts with ".." has
	// escaped it.
	return rel == "." || !strings.HasPrefix(rel, "..")
}

// sortMatches orders candidates most-specific-first, then most-recently-active,
// with the id as a final tiebreak so the order is total and stable across runs
// (a completion list that reshuffles between identical invocations is a bug).
func sortMatches(m []SessionMatch) {
	sort.SliceStable(m, func(a, b int) bool {
		ra, rb := resolveRank[m[a].MatchedBy], resolveRank[m[b].MatchedBy]
		if ra != rb {
			return ra < rb
		}
		if !m[a].LastActivity.Equal(m[b].LastActivity) {
			return m[a].LastActivity.After(m[b].LastActivity)
		}
		return m[a].ID < m[b].ID
	})
}

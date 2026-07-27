package client

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// resolve_test.go covers ResolveSessions/ResolveSession: the ranking between
// match kinds, the exact-id short-circuit, the kind-scoped ambiguity rule, and
// the guards that keep a bare word from being resolved against the filesystem.
//
// Everything here runs against a real index under a temp state dir — resolution
// is defined as "what this machine recorded", so faking the store would test a
// different function than the one that ships.

// resolveClient builds an offline Client rooted at a temp state dir.
func resolveClient(t *testing.T) *Client {
	t.Helper()
	c, err := Offline(WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Offline: %v", err)
	}
	return c
}

func TestOfflineDoesNotLoadKubeconfigAndClusterMethodsFailTyped(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))
	c, err := Offline(WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Offline loaded kubeconfig: %v", err)
	}
	if _, err := c.ResolveSessions(context.Background(), ""); err != nil {
		t.Fatalf("offline ResolveSessions: %v", err)
	}
	if _, err := c.List(context.Background()); !errors.Is(err, ErrOffline) {
		t.Fatalf("offline List error = %v, want ErrOffline", err)
	}
}

// seed writes one index entry, failing the test if the write does.
func seed(t *testing.T, c *Client, e index.Entry) {
	t.Helper()
	if err := c.index.Save(e.SandboxSessionID, e); err != nil {
		t.Fatalf("seed %s: %v", e.SandboxSessionID, err)
	}
}

// ago is a readable way to space entries apart in time for recency ordering.
func ago(d time.Duration) time.Time { return time.Now().Add(-d) }

func TestResolveSessionsEmptyQueryListsEverythingNewestFirst(t *testing.T) {
	c := resolveClient(t)
	seed(t, c, index.Entry{SandboxSessionID: "claude-pane-old", ProjectPath: "/repo/a", LastActivity: ago(2 * time.Hour)})
	seed(t, c, index.Entry{SandboxSessionID: "claude-pane-new", ProjectPath: "/repo/b", LastActivity: ago(time.Minute)})

	got, err := c.ResolveSessions(context.Background(), "")
	if err != nil {
		t.Fatalf("ResolveSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2: %+v", len(got), got)
	}
	if got[0].ID != "claude-pane-new" {
		t.Errorf("newest session should sort first, got %q", got[0].ID)
	}
	if got[0].MatchedBy != MatchAny {
		t.Errorf("MatchedBy = %q, want %q", got[0].MatchedBy, MatchAny)
	}
}

// A full id is never ambiguous — not even against a longer id it prefixes. The
// short-circuit is what makes "paste the id" always do the obvious thing.
func TestResolveSessionExactIDBeatsALongerIDItPrefixes(t *testing.T) {
	c := resolveClient(t)
	seed(t, c, index.Entry{SandboxSessionID: "claude-pane-abc", ProjectPath: "/repo/a", LastActivity: ago(time.Hour)})
	seed(t, c, index.Entry{SandboxSessionID: "claude-pane-abc-extended", ProjectPath: "/repo/b", LastActivity: ago(time.Minute)})

	got, err := c.ResolveSession(context.Background(), "claude-pane-abc")
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if got.ID != "claude-pane-abc" {
		t.Errorf("ID = %q, want the exactly-named session", got.ID)
	}
	if got.MatchedBy != MatchID {
		t.Errorf("MatchedBy = %q, want %q", got.MatchedBy, MatchID)
	}
}

// The shell standing inside a worktree resolves itself: both the worktree dir
// and any path under it name that session.
func TestResolveSessionByWorktreePathIncludingSubdirectory(t *testing.T) {
	c := resolveClient(t)
	wt := filepath.Join(t.TempDir(), "worktrees", "claude-pane-wt")
	seed(t, c, index.Entry{
		SandboxSessionID: "claude-pane-wt",
		ProjectPath:      "/repo/a",
		WorktreePath:     wt,
		WorktreeBranch:   "sandbox/claude-pane-wt",
		LastActivity:     ago(time.Minute),
	})

	for _, q := range []string{wt, filepath.Join(wt, "internal", "cli")} {
		got, err := c.ResolveSession(context.Background(), q)
		if err != nil {
			t.Fatalf("ResolveSession(%q): %v", q, err)
		}
		if got.ID != "claude-pane-wt" {
			t.Errorf("ResolveSession(%q) = %q, want claude-pane-wt", q, got.ID)
		}
		if got.MatchedBy != MatchWorktreePath {
			t.Errorf("ResolveSession(%q) MatchedBy = %q, want %q", q, got.MatchedBy, MatchWorktreePath)
		}
	}
}

// A path OUTSIDE the worktree must not match it — the lexical containment check
// is the whole guard against a sibling directory resolving to the wrong session.
func TestResolveSessionsSiblingPathDoesNotMatchWorktree(t *testing.T) {
	c := resolveClient(t)
	root := t.TempDir()
	seed(t, c, index.Entry{
		SandboxSessionID: "claude-pane-wt",
		ProjectPath:      "/repo/a",
		WorktreePath:     filepath.Join(root, "session-one"),
		LastActivity:     ago(time.Minute),
	})

	got, err := c.ResolveSessions(context.Background(), filepath.Join(root, "session-two"))
	if err != nil {
		t.Fatalf("ResolveSessions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a sibling directory matched %d session(s): %+v", len(got), got)
	}
}

func TestResolveSessionByBranchAndIDPrefix(t *testing.T) {
	c := resolveClient(t)
	seed(t, c, index.Entry{
		SandboxSessionID: "claude-pane-branchy",
		ProjectPath:      "/repo/a",
		WorktreeBranch:   "feat/ctx-percent",
		LastActivity:     ago(time.Minute),
	})

	byBranch, err := c.ResolveSession(context.Background(), "feat/ctx-percent")
	if err != nil {
		t.Fatalf("by branch: %v", err)
	}
	if byBranch.MatchedBy != MatchBranch || byBranch.ID != "claude-pane-branchy" {
		t.Errorf("by branch = %q/%q, want claude-pane-branchy/%q", byBranch.ID, byBranch.MatchedBy, MatchBranch)
	}

	byPrefix, err := c.ResolveSession(context.Background(), "claude-pane-bra")
	if err != nil {
		t.Fatalf("by prefix: %v", err)
	}
	if byPrefix.MatchedBy != MatchIDPrefix || byPrefix.ID != "claude-pane-branchy" {
		t.Errorf("by prefix = %q/%q, want claude-pane-branchy/%q", byPrefix.ID, byPrefix.MatchedBy, MatchIDPrefix)
	}
}

// The title chain must agree with what the dashboard shows: rename wins, then
// the runner's auto-title, then the project basename. A session called one thing
// on screen and another on the command line is unresolvable in practice.
func TestResolveSessionsTitleFallbackChainAndCaseInsensitiveMatch(t *testing.T) {
	c := resolveClient(t)
	seed(t, c, index.Entry{SandboxSessionID: "s-renamed", ProjectPath: "/repo/alpha", RenamedTitle: "Auth Refactor", AutoTitle: "ignored", LastActivity: ago(time.Minute)})
	seed(t, c, index.Entry{SandboxSessionID: "s-auto", ProjectPath: "/repo/beta", AutoTitle: "Sync prober fixes", LastActivity: ago(time.Minute)})
	seed(t, c, index.Entry{SandboxSessionID: "s-basename", ProjectPath: "/repo/gamma", LastActivity: ago(time.Minute)})

	for _, tc := range []struct{ query, wantID, wantTitle string }{
		{"auth refactor", "s-renamed", "Auth Refactor"}, // case-insensitive, rename wins over auto
		{"prober", "s-auto", "Sync prober fixes"},       // auto-title used when no rename
		{"gamma", "s-basename", "gamma"},                // project basename is the last resort
	} {
		got, err := c.ResolveSession(context.Background(), tc.query)
		if err != nil {
			t.Fatalf("ResolveSession(%q): %v", tc.query, err)
		}
		if got.ID != ID(tc.wantID) {
			t.Errorf("ResolveSession(%q) = %q, want %q", tc.query, got.ID, tc.wantID)
		}
		if got.Title != tc.wantTitle {
			t.Errorf("ResolveSession(%q).Title = %q, want %q", tc.query, got.Title, tc.wantTitle)
		}
		if got.MatchedBy != MatchTitle {
			t.Errorf("ResolveSession(%q).MatchedBy = %q, want %q", tc.query, got.MatchedBy, MatchTitle)
		}
	}
}

// A bare word must never be tried as a path: "alpha" is a directory name in the
// project path below, and resolving it that way would silently match a session
// the user did not name.
func TestResolveSessionsBareWordIsNotTreatedAsAPath(t *testing.T) {
	c := resolveClient(t)
	seed(t, c, index.Entry{SandboxSessionID: "s-1", ProjectPath: "/repo/alpha", RenamedTitle: "unrelated", LastActivity: ago(time.Minute)})

	got, err := c.ResolveSessions(context.Background(), "repo")
	if err != nil {
		t.Fatalf("ResolveSessions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("bare word %q matched %d session(s) as a path: %+v", "repo", len(got), got)
	}
}

// Ambiguity is scoped to the match KIND: one id-prefix hit wins outright even
// though two other sessions match the same query by the weaker title rule. The
// alternative — a three-way ambiguity error — would make id prefixes useless the
// moment any session mentioned the same word.
func TestResolveSessionStrongerKindWinsWithoutAmbiguity(t *testing.T) {
	c := resolveClient(t)
	seed(t, c, index.Entry{SandboxSessionID: "zebra-one", ProjectPath: "/repo/a", RenamedTitle: "unrelated", LastActivity: ago(3 * time.Minute)})
	seed(t, c, index.Entry{SandboxSessionID: "s-two", ProjectPath: "/repo/b", RenamedTitle: "zebra migration", LastActivity: ago(time.Minute)})
	seed(t, c, index.Entry{SandboxSessionID: "s-three", ProjectPath: "/repo/c", RenamedTitle: "more zebra work", LastActivity: ago(2 * time.Minute)})

	// Note the id-prefix session is the LEAST recently active: kind outranks
	// recency, so this fails if the sort ever degrades to recency-first.
	got, err := c.ResolveSession(context.Background(), "zebra")
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if got.ID != "zebra-one" {
		t.Errorf("ID = %q, want zebra-one", got.ID)
	}
	if got.MatchedBy != MatchIDPrefix {
		t.Errorf("MatchedBy = %q, want %q", got.MatchedBy, MatchIDPrefix)
	}

	// All three are still offered as candidates for completion — winning
	// outright is a ResolveSession rule, not a filter on ResolveSessions.
	all, err := c.ResolveSessions(context.Background(), "zebra")
	if err != nil {
		t.Fatalf("ResolveSessions: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("got %d candidates, want all 3: %v", len(all), ids(all))
	}
}

// Ambiguity WITHIN a kind is real ambiguity: the caller must choose, and gets
// the candidates to choose from.
func TestResolveSessionAmbiguousWithinAKindCarriesCandidates(t *testing.T) {
	c := resolveClient(t)
	repo := t.TempDir()
	seed(t, c, index.Entry{SandboxSessionID: "s-one", ProjectPath: repo, LastActivity: ago(time.Minute)})
	seed(t, c, index.Entry{SandboxSessionID: "s-two", ProjectPath: repo, LastActivity: ago(2 * time.Minute)})

	_, err := c.ResolveSession(context.Background(), repo)
	if !errors.Is(err, ErrAmbiguousSession) {
		t.Fatalf("err = %v, want it to wrap ErrAmbiguousSession", err)
	}
	var amb *AmbiguousSessionError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want an *AmbiguousSessionError", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(amb.Candidates), amb.Candidates)
	}
	// Candidates carry enough to render a disambiguation list without a second lookup.
	if amb.Candidates[0].ID != "s-one" {
		t.Errorf("candidates should be recency-ordered, got %q first", amb.Candidates[0].ID)
	}
	if amb.Query != repo {
		t.Errorf("Query = %q, want %q", amb.Query, repo)
	}
}

func TestResolveSessionNoMatch(t *testing.T) {
	c := resolveClient(t)
	seed(t, c, index.Entry{SandboxSessionID: "s-1", ProjectPath: "/repo/a", LastActivity: ago(time.Minute)})

	if _, err := c.ResolveSession(context.Background(), "nothing-like-this"); !errors.Is(err, ErrNoSessionMatch) {
		t.Errorf("err = %v, want ErrNoSessionMatch", err)
	}
}

// ResolveSessions reports "no match" as an empty slice, never an error —
// completion must stay silent rather than print a failure on every TAB.
func TestResolveSessionsNoMatchIsNotAnError(t *testing.T) {
	c := resolveClient(t)
	seed(t, c, index.Entry{SandboxSessionID: "s-1", ProjectPath: "/repo/a", LastActivity: ago(time.Minute)})

	got, err := c.ResolveSessions(context.Background(), "nothing-like-this")
	if err != nil {
		t.Fatalf("ResolveSessions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d matches, want none", len(got))
	}
}

// Equal-recency candidates must come back in a stable, total order — a
// completion list that reshuffles between identical invocations is a bug.
func TestResolveSessionsOrderIsTotalWhenActivityTies(t *testing.T) {
	c := resolveClient(t)
	same := ago(time.Hour)
	seed(t, c, index.Entry{SandboxSessionID: "b-session", ProjectPath: "/repo/a", LastActivity: same})
	seed(t, c, index.Entry{SandboxSessionID: "a-session", ProjectPath: "/repo/a", LastActivity: same})

	for i := 0; i < 5; i++ {
		got, err := c.ResolveSessions(context.Background(), "")
		if err != nil {
			t.Fatalf("ResolveSessions: %v", err)
		}
		if len(got) != 2 || got[0].ID != "a-session" {
			t.Fatalf("run %d: order = %v, want a-session first", i, ids(got))
		}
	}
}

// A cancelled context is honored before any index I/O.
func TestResolveSessionsHonorsContextCancellation(t *testing.T) {
	c := resolveClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ResolveSessions(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// A session with no activity timestamp falls back to its creation time rather
// than sorting as the zero time (which would bury a brand-new session).
func TestResolveSessionsFallsBackToCreatedAtForRecency(t *testing.T) {
	c := resolveClient(t)
	seed(t, c, index.Entry{SandboxSessionID: "s-active", ProjectPath: "/repo/a", LastActivity: ago(time.Hour)})
	seed(t, c, index.Entry{SandboxSessionID: "s-fresh", ProjectPath: "/repo/b", CreatedAt: ago(time.Minute)})

	got, err := c.ResolveSessions(context.Background(), "")
	if err != nil {
		t.Fatalf("ResolveSessions: %v", err)
	}
	if len(got) != 2 || got[0].ID != "s-fresh" {
		t.Errorf("order = %v, want the just-created session first", ids(got))
	}
}

func ids(m []SessionMatch) []ID {
	out := make([]ID, 0, len(m))
	for _, x := range m {
		out = append(out, x.ID)
	}
	return out
}

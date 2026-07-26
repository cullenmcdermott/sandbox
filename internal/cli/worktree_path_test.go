package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/client"
)

// fakeResolver is a test double for sessionResolver: canned matches (or an
// error), recording the query it was asked.
type fakeResolver struct {
	matches  []client.SessionMatch
	err      error
	gotQuery string
}

func (f *fakeResolver) ResolveSessions(_ context.Context, query string) ([]client.SessionMatch, error) {
	f.gotQuery = query
	return f.matches, f.err
}

func match(id, title, wt, branch string, kind client.MatchKind) client.SessionMatch {
	return client.SessionMatch{
		ID:           client.ID(id),
		Title:        title,
		Backend:      "claude-pane",
		ProjectPath:  "/repo",
		WorktreePath: wt,
		Branch:       branch,
		LastActivity: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		MatchedBy:    kind,
	}
}

// noChooser stands in for "there is no terminal": the branch that must produce
// a listing error rather than hanging.
func noChooser([]client.SessionMatch) (client.SessionMatch, error) {
	return client.SessionMatch{}, errNoTTY
}

// TestWorktreePathPrintsOnlyThePath is the `cd $(...)` contract: stdout carries
// the path and nothing else — no label, no trailing commentary.
func TestWorktreePathPrintsOnlyThePath(t *testing.T) {
	r := &fakeResolver{matches: []client.SessionMatch{
		match("sess-1", "auth refactor", "/wt/sess-1", "sandbox/sess-1", client.MatchIDPrefix),
	}}
	var out bytes.Buffer
	if err := runWorktreePath(context.Background(), r, &out, "sess", false, noChooser); err != nil {
		t.Fatalf("runWorktreePath: %v", err)
	}
	if got := out.String(); got != "/wt/sess-1\n" {
		t.Errorf("stdout = %q, want exactly the path", got)
	}
	if r.gotQuery != "sess" {
		t.Errorf("resolver got query %q, want %q", r.gotQuery, "sess")
	}
}

// TestWorktreePathNoMatchErrors pins that zero matches is an error (a non-zero
// exit), so `cd $(sandbox worktree path typo)` cannot cd to nowhere.
func TestWorktreePathNoMatchErrors(t *testing.T) {
	var out bytes.Buffer
	err := runWorktreePath(context.Background(), &fakeResolver{}, &out, "nope", false, noChooser)
	if err == nil {
		t.Fatal("expected an error for zero matches")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error does not name the query: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote to stdout on failure: %q", out.String())
	}
}

// TestWorktreePathAmbiguousWithoutTTYLists is the no-terminal branch: name the
// candidates instead of prompting where nobody can answer.
func TestWorktreePathAmbiguousWithoutTTYLists(t *testing.T) {
	r := &fakeResolver{matches: []client.SessionMatch{
		match("sess-1", "auth one", "/wt/sess-1", "feat/a", client.MatchTitle),
		match("sess-2", "auth two", "/wt/sess-2", "feat/b", client.MatchTitle),
	}}
	var out bytes.Buffer
	err := runWorktreePath(context.Background(), r, &out, "auth", false, noChooser)
	if err == nil {
		t.Fatal("expected an ambiguity error with no tty")
	}
	for _, want := range []string{"sess-1", "sess-2", "feat/a", "feat/b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error missing %q: %v", want, err)
		}
	}
	if out.Len() != 0 {
		t.Errorf("wrote a path despite ambiguity: %q", out.String())
	}
}

// TestWorktreePathAmbiguityIsKindScoped: a strong match plus weaker ones is not
// ambiguous. Prompting there would make the common case (an id prefix on a repo
// with several sessions) needlessly interactive.
func TestWorktreePathAmbiguityIsKindScoped(t *testing.T) {
	r := &fakeResolver{matches: []client.SessionMatch{
		match("sess-1", "the one", "/wt/sess-1", "feat/a", client.MatchIDPrefix),
		match("sess-2", "weaker", "/wt/sess-2", "feat/b", client.MatchProjectPath),
		match("sess-3", "weaker", "/wt/sess-3", "feat/c", client.MatchProjectPath),
	}}
	var out bytes.Buffer
	if err := runWorktreePath(context.Background(), r, &out, "sess-1", false, noChooser); err != nil {
		t.Fatalf("kind-scoped resolution errored: %v", err)
	}
	if got := out.String(); got != "/wt/sess-1\n" {
		t.Errorf("stdout = %q, want the strongest match's path", got)
	}
}

// TestWorktreePathPickerChoiceWins: when a chooser IS available, its pick is
// what lands on stdout.
func TestWorktreePathPickerChoiceWins(t *testing.T) {
	matches := []client.SessionMatch{
		match("sess-1", "auth one", "/wt/sess-1", "feat/a", client.MatchTitle),
		match("sess-2", "auth two", "/wt/sess-2", "feat/b", client.MatchTitle),
	}
	var offered []client.SessionMatch
	choose := func(m []client.SessionMatch) (client.SessionMatch, error) {
		offered = m
		return m[1], nil
	}
	var out bytes.Buffer
	if err := runWorktreePath(context.Background(), &fakeResolver{matches: matches}, &out, "auth", false, choose); err != nil {
		t.Fatalf("runWorktreePath: %v", err)
	}
	if got := out.String(); got != "/wt/sess-2\n" {
		t.Errorf("stdout = %q, want the picked session's path", got)
	}
	if len(offered) != 2 {
		t.Errorf("chooser offered %d candidates, want 2", len(offered))
	}
}

// TestWorktreePathPickerCancelWritesNothing: dismissing the picker must not
// leave a path on stdout for `cd` to consume.
func TestWorktreePathPickerCancelWritesNothing(t *testing.T) {
	matches := []client.SessionMatch{
		match("sess-1", "auth one", "/wt/sess-1", "feat/a", client.MatchTitle),
		match("sess-2", "auth two", "/wt/sess-2", "feat/b", client.MatchTitle),
	}
	cancel := func([]client.SessionMatch) (client.SessionMatch, error) {
		return client.SessionMatch{}, errPickCancelled
	}
	var out bytes.Buffer
	err := runWorktreePath(context.Background(), &fakeResolver{matches: matches}, &out, "auth", false, cancel)
	if !errors.Is(err, errPickCancelled) {
		t.Errorf("err = %v, want the cancel sentinel", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote a path after cancel: %q", out.String())
	}
}

// TestWorktreePathNoWorktreeSaysSo pins the settled decision: a session without
// a worktree reports that fact and does NOT silently fall back to the repo root
// (which would `cd` you into the main checkout thinking it was isolated).
func TestWorktreePathNoWorktreeSaysSo(t *testing.T) {
	r := &fakeResolver{matches: []client.SessionMatch{
		match("sess-1", "plain", "", "", client.MatchID),
	}}
	var out bytes.Buffer
	err := runWorktreePath(context.Background(), r, &out, "sess-1", false, noChooser)
	if err == nil {
		t.Fatal("expected an error for a session with no worktree")
	}
	if !strings.Contains(err.Error(), "no per-session worktree") {
		t.Errorf("error does not explain the absence: %v", err)
	}
	if strings.Contains(out.String(), "/repo") {
		t.Errorf("fell back to the project path: %q", out.String())
	}
}

// TestWorktreePathJSONIsStructuredAndNeverInteractive: --json emits an array
// (even for one match) and does not consult the chooser.
func TestWorktreePathJSONIsStructuredAndNeverInteractive(t *testing.T) {
	matches := []client.SessionMatch{
		match("sess-1", "auth one", "/wt/sess-1", "feat/a", client.MatchTitle),
		match("sess-2", "auth two", "/wt/sess-2", "feat/b", client.MatchTitle),
	}
	chooserCalled := false
	choose := func(m []client.SessionMatch) (client.SessionMatch, error) {
		chooserCalled = true
		return m[0], nil
	}
	var out bytes.Buffer
	if err := runWorktreePath(context.Background(), &fakeResolver{matches: matches}, &out, "auth", true, choose); err != nil {
		t.Fatalf("runWorktreePath --json: %v", err)
	}
	if chooserCalled {
		t.Error("--json prompted; it must never be interactive")
	}
	var recs []worktreePathJSON
	if err := json.Unmarshal(out.Bytes(), &recs); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].ID != "sess-1" || recs[0].WorktreePath != "/wt/sess-1" || recs[0].MatchedBy != "title" {
		t.Errorf("record 0 = %+v", recs[0])
	}
}

// TestWorktreePathJSONSingleMatchIsStillAnArray: the shape must not change with
// the match count, so a script never branches on it.
func TestWorktreePathJSONSingleMatchIsStillAnArray(t *testing.T) {
	r := &fakeResolver{matches: []client.SessionMatch{
		match("sess-1", "only", "/wt/sess-1", "feat/a", client.MatchID),
	}}
	var out bytes.Buffer
	if err := runWorktreePath(context.Background(), r, &out, "sess-1", true, noChooser); err != nil {
		t.Fatalf("runWorktreePath --json: %v", err)
	}
	var recs []worktreePathJSON
	if err := json.Unmarshal(out.Bytes(), &recs); err != nil {
		t.Fatalf("single match is not a JSON array: %v\n%s", err, out.String())
	}
	if len(recs) != 1 {
		t.Errorf("got %d records, want 1", len(recs))
	}
}

// TestWorktreePathEmptyQueryOffersEverything: no argument means "all sessions",
// which is what makes a bare `sandbox worktree path` a picker over the fleet.
func TestWorktreePathEmptyQueryOffersEverything(t *testing.T) {
	matches := []client.SessionMatch{
		match("sess-1", "one", "/wt/sess-1", "feat/a", client.MatchAny),
		match("sess-2", "two", "/wt/sess-2", "feat/b", client.MatchAny),
	}
	var offered int
	choose := func(m []client.SessionMatch) (client.SessionMatch, error) {
		offered = len(m)
		return m[0], nil
	}
	var out bytes.Buffer
	if err := runWorktreePath(context.Background(), &fakeResolver{matches: matches}, &out, "", false, choose); err != nil {
		t.Fatalf("runWorktreePath: %v", err)
	}
	if offered != 2 {
		t.Errorf("empty query offered %d sessions, want 2", offered)
	}
}

// TestWorktreePathResolverErrorPropagates: an index read failure is a real
// error, not an empty result silently reported as "no match".
func TestWorktreePathResolverErrorPropagates(t *testing.T) {
	boom := errors.New("index unreadable")
	var out bytes.Buffer
	err := runWorktreePath(context.Background(), &fakeResolver{err: boom}, &out, "x", false, noChooser)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the resolver's error", err)
	}
}

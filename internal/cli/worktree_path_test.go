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

// --- picker row content + height budget ------------------------------------
//
// The picker rows are the ONLY thing the user sees when disambiguating, and on
// a real machine most candidates share a project and a title fallback. These
// pin what makes two such rows tellable apart.

func TestPickerColsCarryRepoIDAndAge(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	m := match("claude-pane-df80e6-3bf602fc", "Review TODO", "/wt", "feat/x", client.MatchAny)
	m.ProjectPath = "/Users/cullen/git/sandbox"
	m.LastActivity = now.Add(-3 * time.Hour)
	// Repo basename (not the path — it is identical down every row), the id
	// suffix, and the age. The match kind is omitted for MatchAny: with an empty
	// query every row carries it, so it separates nothing and costs a column.
	assertCols(t, pickerCols(m, now), []string{"sandbox", "3bf602fc", "3h"})

	// A real query produces varied kinds, and then the kind explains the row.
	m.MatchedBy = client.MatchTitle
	assertCols(t, pickerCols(m, now), []string{"sandbox", "3bf602fc", "3h", "title"})

	// A session with no project path still gets a cell, so the columns after it
	// stay aligned with every other row's.
	m.ProjectPath, m.MatchedBy = "", client.MatchAny
	assertCols(t, pickerCols(m, now), []string{"—", "3bf602fc", "3h"})
}

func assertCols(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("cols = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("col %d = %q, want %q (full: %q)", i, got[i], want[i], got)
		}
	}
}

func TestPickerWidthCapLeavesRoomForFourColumns(t *testing.T) {
	// Before the first resize the cap is the floor, NOT the picker's much
	// narrower 60-column default — a four-column row does not fit in 60.
	if got := pickerWidthCap(0); got != pickerMinWidth {
		t.Errorf("pickerWidthCap(0) = %d, want the %d floor", got, pickerMinWidth)
	}
	// A narrow terminal still gets the floor (the picker clamps to the terminal
	// itself); a wide one is capped so the eye does not cross the whole screen.
	if got := pickerWidthCap(40); got != pickerMinWidth {
		t.Errorf("pickerWidthCap(40) = %d, want the %d floor", got, pickerMinWidth)
	}
	if got := pickerWidthCap(400); got != pickerMaxWidth {
		t.Errorf("pickerWidthCap(400) = %d, want the %d cap", got, pickerMaxWidth)
	}
	if got := pickerWidthCap(100); got != 92 {
		t.Errorf("pickerWidthCap(100) = %d, want 92 (100 - the view margin)", got)
	}
}

func TestShortAgeAndShortSessionID(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "now"},
		{30 * time.Second, "now"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h"},
		{50 * time.Hour, "2d"},
		{-time.Hour, "now"}, // a clock skew must not render "-1h"
	} {
		if got := shortAge(now.Add(-tc.d), now); got != tc.want {
			t.Errorf("shortAge(-%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
	if got := shortAge(time.Time{}, now); got != "now" {
		t.Errorf("shortAge(zero) = %q, want %q", got, "now")
	}
	for in, want := range map[string]string{
		"claude-pane-df80e6-3bf602fc": "3bf602fc",
		"opencode-server-95dadb-b23d": "b23d",
		"nodashes":                    "nodashes",
		"trailing-":                   "trailing-",
	} {
		if got := shortSessionID(in); got != want {
			t.Errorf("shortSessionID(%q) = %q, want %q", in, got, want)
		}
	}
}

// The row budget must leave room for the box's own chrome — under-counting is
// exactly what scrolled the picker's title and query line off the top.
func TestPickerRowBudgetLeavesRoomForChrome(t *testing.T) {
	if got := pickerRowBudget(0); got != 0 {
		t.Errorf("unknown height should be unbounded, got %d", got)
	}
	if got := pickerRowBudget(50); got != 50-pickerChrome {
		t.Errorf("pickerRowBudget(50) = %d, want %d", got, 50-pickerChrome)
	}
	// A tiny terminal still gets rows rather than an empty box, and never a
	// negative cap (which SetMaxRows would clamp anyway).
	for _, h := range []int{1, 5, 12} {
		if got := pickerRowBudget(h); got < 3 {
			t.Errorf("pickerRowBudget(%d) = %d, want at least 3", h, got)
		}
	}
	// The budget plus the chrome must fit the terminal, or the box overflows.
	for _, h := range []int{24, 40, 60, 100} {
		if got := pickerRowBudget(h) + pickerChrome; got > h {
			t.Errorf("height %d: %d rows + %d chrome = %d lines, overflows", h, pickerRowBudget(h), pickerChrome, got)
		}
	}
}

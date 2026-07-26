package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
)

// fakeConverter is a test double for worktreeConverter: canned resolution plus
// a canned convert outcome, recording what it was asked to convert.
type fakeConverter struct {
	matches []client.SessionMatch
	resErr  error

	result    client.BranchResult
	convErr   error
	gotID     client.ID
	gotOpt    client.ConvertOptions
	convCalls int
}

func (f *fakeConverter) ResolveSessions(_ context.Context, _ string) ([]client.SessionMatch, error) {
	return f.matches, f.resErr
}

func (f *fakeConverter) ConvertToBranch(_ context.Context, id client.ID, opt client.ConvertOptions) (client.BranchResult, error) {
	f.convCalls++
	f.gotID, f.gotOpt = id, opt
	return f.result, f.convErr
}

func oneMatch() []client.SessionMatch {
	return []client.SessionMatch{match("sess-1", "auth refactor", "/wt/sess-1", "sandbox/sess-1", client.MatchIDPrefix)}
}

// TestConvertPassesFlagsVerbatim pins the contract that this command never
// invents a branch name or a message — ConvertToBranch requires both to be
// already human-approved.
func TestConvertPassesFlagsVerbatim(t *testing.T) {
	cv := &fakeConverter{
		matches: oneMatch(),
		result:  client.BranchResult{Branch: "feat/auth", Committed: true, CommitSHA: "abcdef1234567890"},
	}
	var out bytes.Buffer
	err := runWorktreeConvert(context.Background(), cv, &out, "auth", "feat/auth", "wip: auth", noChooser)
	if err != nil {
		t.Fatalf("runWorktreeConvert: %v", err)
	}
	if cv.gotID != "sess-1" {
		t.Errorf("converted %q, want sess-1", cv.gotID)
	}
	if cv.gotOpt.BranchName != "feat/auth" || cv.gotOpt.Message != "wip: auth" {
		t.Errorf("options = %+v, want the flags verbatim", cv.gotOpt)
	}
	got := out.String()
	if !strings.Contains(got, "abcdef1") || !strings.Contains(got, "feat/auth") {
		t.Errorf("report missing the commit/branch: %q", got)
	}
}

// TestConvertCleanWorktreeReportsNoCommit distinguishes the pure-rename case,
// so the user is not told work was captured when none was.
func TestConvertCleanWorktreeReportsNoCommit(t *testing.T) {
	cv := &fakeConverter{
		matches: oneMatch(),
		result:  client.BranchResult{Branch: "feat/auth", Committed: false},
	}
	var out bytes.Buffer
	if err := runWorktreeConvert(context.Background(), cv, &out, "auth", "feat/auth", "", noChooser); err != nil {
		t.Fatalf("runWorktreeConvert: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to commit") {
		t.Errorf("clean convert did not say it was clean: %q", out.String())
	}
}

// TestConvertExplainsTheCheckoutGotcha is the whole reason this thread exists:
// after converting, the branch is held by the worktree, so `git checkout` in the
// main repo refuses. The command must say so, and offer the command that works.
func TestConvertExplainsTheCheckoutGotcha(t *testing.T) {
	cv := &fakeConverter{
		matches: oneMatch(),
		result:  client.BranchResult{Branch: "feat/auth", Committed: true, CommitSHA: "abcdef1234567890"},
	}
	var out bytes.Buffer
	if err := runWorktreeConvert(context.Background(), cv, &out, "auth", "feat/auth", "m", noChooser); err != nil {
		t.Fatalf("runWorktreeConvert: %v", err)
	}
	got := out.String()
	for _, want := range []string{"git checkout feat/auth", "will refuse", "git -C /repo merge feat/auth"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// TestConvertMapsSentinels pins that each SDK sentinel becomes an actionable
// message rather than a raw git error the user never invoked.
func TestConvertMapsSentinels(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"invalid name", client.ErrInvalidBranchName, "not a valid git branch name"},
		{"taken", client.ErrBranchNameTaken, "already exists"},
		{"dirty", client.ErrWorktreeDirty, "--message"},
		{"no worktree", client.ErrNoWorktree, "no per-session worktree"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cv := &fakeConverter{matches: oneMatch(), convErr: tc.err}
			var out bytes.Buffer
			err := runWorktreeConvert(context.Background(), cv, &out, "auth", "feat/auth", "", noChooser)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// TestConvertUnknownErrorPropagates: an error the command does not recognise
// must survive intact rather than being flattened into a guess.
func TestConvertUnknownErrorPropagates(t *testing.T) {
	boom := errors.New("git exploded")
	cv := &fakeConverter{matches: oneMatch(), convErr: boom}
	var out bytes.Buffer
	err := runWorktreeConvert(context.Background(), cv, &out, "auth", "feat/auth", "", noChooser)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the underlying error", err)
	}
}

// TestConvertRefusesSessionWithoutWorktree stops before mutating anything.
func TestConvertRefusesSessionWithoutWorktree(t *testing.T) {
	cv := &fakeConverter{matches: []client.SessionMatch{
		match("sess-1", "plain", "", "", client.MatchID),
	}}
	var out bytes.Buffer
	err := runWorktreeConvert(context.Background(), cv, &out, "sess-1", "feat/x", "", noChooser)
	if err == nil || !strings.Contains(err.Error(), "no per-session worktree") {
		t.Fatalf("err = %v, want a no-worktree error", err)
	}
	if cv.convCalls != 0 {
		t.Error("attempted a convert on a session with no worktree")
	}
}

// TestConvertAmbiguousWithoutTTYLists: same 0/N rules as `worktree path`, so
// the two commands cannot disagree about what a query means.
func TestConvertAmbiguousWithoutTTYLists(t *testing.T) {
	cv := &fakeConverter{matches: []client.SessionMatch{
		match("sess-1", "auth one", "/wt/sess-1", "feat/a", client.MatchTitle),
		match("sess-2", "auth two", "/wt/sess-2", "feat/b", client.MatchTitle),
	}}
	var out bytes.Buffer
	err := runWorktreeConvert(context.Background(), cv, &out, "auth", "feat/x", "", noChooser)
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	for _, want := range []string{"sess-1", "sess-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error missing %q: %v", want, err)
		}
	}
	if cv.convCalls != 0 {
		t.Error("converted despite an ambiguous query")
	}
}

// TestConvertNoMatchErrors: a typo must not convert something else.
func TestConvertNoMatchErrors(t *testing.T) {
	cv := &fakeConverter{}
	var out bytes.Buffer
	err := runWorktreeConvert(context.Background(), cv, &out, "nope", "feat/x", "", noChooser)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want a no-match error naming the query", err)
	}
	if cv.convCalls != 0 {
		t.Error("converted despite no match")
	}
}

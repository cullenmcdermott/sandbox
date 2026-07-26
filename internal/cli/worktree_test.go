package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/client"
)

// fakeWorktreeReaper is a test double for worktreeReaper: it returns a canned
// reap report (or error) and records every ReapOptions it was called with. gc
// classifies with a dry run before acting, so the call SEQUENCE is part of what
// these tests pin.
type fakeWorktreeReaper struct {
	reaped   []client.ReapedWorktree
	err      error
	calls    []client.ReapOptions
	callback func()
}

func (f *fakeWorktreeReaper) ReapWorktrees(_ context.Context, opt client.ReapOptions) ([]client.ReapedWorktree, error) {
	f.calls = append(f.calls, opt)
	if f.callback != nil {
		f.callback()
	}
	return f.reaped, f.err
}

// acted reports whether the reaper was ever asked to actually mutate.
func (f *fakeWorktreeReaper) acted() bool {
	for _, c := range f.calls {
		if !c.DryRun {
			return true
		}
	}
	return false
}

// yes/no are confirmFunc stubs; refuse stands in for "no terminal".
func yesConfirm(string) (bool, error) { return true, nil }
func noConfirm(string) (bool, error)  { return false, nil }
func refuseConfirm(string) (bool, error) {
	return false, errors.New("no terminal available to confirm")
}

// gcOpts is the option set most tests use: no age gate, so the fake's canned
// classification is what drives the assertions.
func gcOpts() client.ReapOptions { return client.ReapOptions{} }

// TestRunWorktreeGCReport asserts the per-dir lines and summary count, including
// that "skipped" dirs are reported but excluded from the acted-on count.
func TestRunWorktreeGCReport(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "s1", Path: "/w/s1", Branch: "sandbox/s1", Action: "removed"},
		{SessionID: "s2", Path: "/w/s2", Branch: "feat/foo", Action: "committed-then-removed", CommitSHA: "abcdef1234567890"},
		{SessionID: "s3", Path: "/w/s3", Branch: "sandbox/s3", Action: "skipped"},
	}}
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, gcOpts(), false, false, yesConfirm); err != nil {
		t.Fatalf("runWorktreeGC: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"s1", "removed", "sandbox/s1",
		"s2", "committed-then-removed", "feat/foo", "abcdef1", // short SHA
		"s3", "skipped",
		"reaped 2 of 3 worktree(s)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "abcdef1234567890") {
		t.Errorf("commit SHA should be shortened, got full SHA in:\n%s", got)
	}
	// Classify-then-act: a dry run first, the real one only after confirmation.
	if len(r.calls) != 2 || !r.calls[0].DryRun || r.calls[1].DryRun {
		t.Errorf("call sequence = %+v, want [dry-run, real]", r.calls)
	}
}

// TestRunWorktreeGCDryRun asserts the dry-run wording and that the flag is
// threaded through to ReapWorktrees.
func TestRunWorktreeGCDryRun(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "s1", Action: "removed", Branch: "sandbox/s1"},
	}}
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, gcOpts(), true, false, yesConfirm); err != nil {
		t.Fatalf("runWorktreeGC: %v", err)
	}
	if r.acted() {
		t.Error("--dry-run performed a real reap")
	}
	got := out.String()
	if !strings.Contains(got, "would reap") || !strings.Contains(got, "dry-run") {
		t.Errorf("dry-run output missing wording:\n%s", got)
	}
}

// TestRunWorktreeGCEmpty asserts the no-worktrees message (exit 0, nothing to do).
func TestRunWorktreeGCEmpty(t *testing.T) {
	r := &fakeWorktreeReaper{}
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, gcOpts(), false, false, yesConfirm); err != nil {
		t.Fatalf("runWorktreeGC: %v", err)
	}
	if !strings.Contains(out.String(), "no worktrees found") {
		t.Errorf("empty output = %q", out.String())
	}
}

// TestRunWorktreeGCAllSkipped asserts a run where everything is retained exits
// 0, says so, and never prompts — there is nothing to confirm.
func TestRunWorktreeGCAllSkipped(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "live", Action: "skipped", Branch: "sandbox/live", Reason: "session is still live"},
	}}
	prompted := false
	confirm := func(string) (bool, error) { prompted = true; return true, nil }
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, gcOpts(), false, false, confirm); err != nil {
		t.Fatalf("runWorktreeGC should exit 0 when all skipped: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "nothing to reap") {
		t.Errorf("all-skipped summary = %q", got)
	}
	if !strings.Contains(got, "session is still live") {
		t.Errorf("retention reason not reported: %q", got)
	}
	if prompted {
		t.Error("prompted with nothing to delete")
	}
	if r.acted() {
		t.Error("mutated with nothing to delete")
	}
}

// TestRunWorktreeGCConfirmationIsRequired: declining the prompt must leave
// everything in place. Deleting someone's checkout is not a silent side effect
// of a maintenance command.
func TestRunWorktreeGCConfirmationIsRequired(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "s1", Action: "removed", Branch: "sandbox/s1"},
	}}
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, gcOpts(), false, false, noConfirm); err != nil {
		t.Fatalf("runWorktreeGC: %v", err)
	}
	if r.acted() {
		t.Error("reaped despite a declined confirmation")
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("declined run did not report cancellation: %q", out.String())
	}
}

// TestRunWorktreeGCConfirmationListsVictimsFirst pins the ordering the whole
// design rests on: the user sees WHAT will be deleted before being asked.
func TestRunWorktreeGCConfirmationListsVictimsFirst(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "doomed", Action: "removed", Branch: "sandbox/doomed"},
	}}
	var out bytes.Buffer
	var promptedWith string
	confirm := func(p string) (bool, error) {
		promptedWith = p
		// The report must already be on the writer when the question is asked.
		if !strings.Contains(out.String(), "doomed") {
			t.Error("prompted before listing the victims")
		}
		return true, nil
	}
	if err := runWorktreeGC(context.Background(), r, &out, gcOpts(), false, false, confirm); err != nil {
		t.Fatalf("runWorktreeGC: %v", err)
	}
	if !strings.Contains(promptedWith, "1 worktree(s)") {
		t.Errorf("prompt does not state the count: %q", promptedWith)
	}
	if !strings.Contains(promptedWith, "Branches are preserved") {
		t.Errorf("prompt does not reassure about branches: %q", promptedWith)
	}
}

// TestRunWorktreeGCYesSkipsPrompt is the scripting path.
func TestRunWorktreeGCYesSkipsPrompt(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "s1", Action: "removed", Branch: "sandbox/s1"},
	}}
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, gcOpts(), false, true, refuseConfirm); err != nil {
		t.Fatalf("runWorktreeGC --yes: %v", err)
	}
	if !r.acted() {
		t.Error("--yes did not perform the reap")
	}
}

// TestRunWorktreeGCNoTTYFailsClosed: with victims, no --yes and no terminal,
// the run must ERROR rather than proceed unconfirmed.
func TestRunWorktreeGCNoTTYFailsClosed(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "s1", Action: "removed", Branch: "sandbox/s1"},
	}}
	var out bytes.Buffer
	err := runWorktreeGC(context.Background(), r, &out, gcOpts(), false, false, refuseConfirm)
	if err == nil {
		t.Fatal("expected an error when confirmation is impossible")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error does not point at --yes: %v", err)
	}
	if r.acted() {
		t.Error("reaped without confirmation")
	}
}

// TestRunWorktreeGCThreadsRetentionOptions pins that the retention policy
// reaches the SDK on both passes — a gate applied only to the classification
// would delete things the preview said were safe.
func TestRunWorktreeGCThreadsRetentionOptions(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "s1", Action: "removed", Branch: "sandbox/s1"},
	}}
	opt := client.ReapOptions{MinAge: 72 * time.Hour, ReapUnlanded: true, BaseBranch: "develop"}
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, opt, false, true, yesConfirm); err != nil {
		t.Fatalf("runWorktreeGC: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(r.calls))
	}
	for i, c := range r.calls {
		if c.MinAge != 72*time.Hour || !c.ReapUnlanded || c.BaseBranch != "develop" {
			t.Errorf("call %d lost retention options: %+v", i, c)
		}
	}
}

// TestParseWorktreeMode covers the flag validation, including the rejection of
// unknown values.
func TestParseWorktreeMode(t *testing.T) {
	cases := []struct {
		in      string
		want    client.WorktreeMode
		wantErr bool
	}{
		{"", client.WorktreeAuto, false},
		{"auto", client.WorktreeAuto, false},
		{"on", client.WorktreeOn, false},
		{"off", client.WorktreeOff, false},
		{"bogus", client.WorktreeAuto, true},
		{"AUTO", client.WorktreeAuto, true}, // case-sensitive
	}
	for _, c := range cases {
		got, err := parseWorktreeMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseWorktreeMode(%q) = nil error, want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseWorktreeMode(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseWorktreeMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestMapWorktreeErr asserts the client sentinels are translated onto the
// dashboard sentinels the TUI branches on, and unknown errors pass through.
func TestMapWorktreeErr(t *testing.T) {
	if got := mapWorktreeErr(nil); got != nil {
		t.Errorf("mapWorktreeErr(nil) = %v, want nil", got)
	}
	passthrough := errors.New("boom")
	if got := mapWorktreeErr(passthrough); !errors.Is(got, passthrough) {
		t.Errorf("unknown error should pass through, got %v", got)
	}
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
)

// fakeWorktreeReaper is a test double for worktreeReaper: it returns a canned
// reap report (or error) and records the DryRun flag it was called with.
type fakeWorktreeReaper struct {
	reaped   []client.ReapedWorktree
	err      error
	gotDry   bool
	callback func()
}

func (f *fakeWorktreeReaper) ReapWorktrees(_ context.Context, opt client.ReapOptions) ([]client.ReapedWorktree, error) {
	f.gotDry = opt.DryRun
	if f.callback != nil {
		f.callback()
	}
	return f.reaped, f.err
}

// TestRunWorktreeGCReport asserts the per-dir lines and summary count, including
// that "skipped" dirs are reported but excluded from the acted-on count.
func TestRunWorktreeGCReport(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "s1", Path: "/w/s1", Branch: "sandbox/s1", Action: "removed"},
		{SessionID: "s2", Path: "/w/s2", Branch: "feat/foo", Action: "committed-then-removed", CommitSHA: "abcdef1234567890"},
		{SessionID: "s3", Path: "/w/s3", Branch: "sandbox/s3", Action: "skipped"},
	}}
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, false); err != nil {
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
	if r.gotDry {
		t.Error("DryRun should be false")
	}
}

// TestRunWorktreeGCDryRun asserts the dry-run wording and that the flag is
// threaded through to ReapWorktrees.
func TestRunWorktreeGCDryRun(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "s1", Action: "removed", Branch: "sandbox/s1"},
	}}
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, true); err != nil {
		t.Fatalf("runWorktreeGC: %v", err)
	}
	if !r.gotDry {
		t.Error("DryRun should be true")
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
	if err := runWorktreeGC(context.Background(), r, &out, false); err != nil {
		t.Fatalf("runWorktreeGC: %v", err)
	}
	if !strings.Contains(out.String(), "no worktrees found") {
		t.Errorf("empty output = %q", out.String())
	}
}

// TestRunWorktreeGCAllSkipped asserts a run where everything is skipped still
// exits 0 and reports 0 of N reaped.
func TestRunWorktreeGCAllSkipped(t *testing.T) {
	r := &fakeWorktreeReaper{reaped: []client.ReapedWorktree{
		{SessionID: "live", Action: "skipped", Branch: "sandbox/live"},
	}}
	var out bytes.Buffer
	if err := runWorktreeGC(context.Background(), r, &out, false); err != nil {
		t.Fatalf("runWorktreeGC should exit 0 when all skipped: %v", err)
	}
	if !strings.Contains(out.String(), "reaped 0 of 1 worktree(s)") {
		t.Errorf("all-skipped summary = %q", out.String())
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

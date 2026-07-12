package client

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/index"
)

// listStubRunner is a syncpkg.Runner that returns a canned `mutagen sync list`
// output (or an error) for the same-path collision scan. The scan only issues
// `sync list --template <pipe-template>`, whose output is one
// "sessionID|context|identifier|name|status" line per session (the context field
// is the MF3 sandbox-context label; a real daemon renders it as an EMPTY field —
// not a missing one — for a pre-MF3 label-less sync).
type listStubRunner struct {
	out string
	err error
}

func (r listStubRunner) Output(_ context.Context, _ io.Reader, _ ...string) ([]byte, error) {
	if r.err != nil {
		return nil, r.err
	}
	return []byte(r.out), nil
}

func TestSameDirSyncWarning(t *testing.T) {
	const ours = "claude-sdk-ours"
	const dir = "/work/repo"

	// listLine formats one `mutagen sync list` template row (current 5-field
	// shape, sandbox-context label populated).
	listLine := func(id, name, status string) string {
		return id + "|some-ctx|sync_" + id + "|" + name + "|" + status
	}
	// legacyListLine formats a row for a pre-MF3 sync: the sandbox-context label
	// is absent, so the template renders an empty second field.
	legacyListLine := func(id, name, status string) string {
		return id + "||sync_" + id + "|" + name + "|" + status
	}

	cases := []struct {
		name          string
		workspacePath string // what the connecting session syncs
		list          string // canned `sync list` output
		listErr       error
		otherEntries  map[string]index.Entry // index entries for other sessions
		wantWarn      bool
	}{
		{
			name:          "collision: another live session syncs the same dir",
			workspacePath: dir,
			list:          listLine("claude-sdk-other", "sandbox-claude-sdk-other-project", "Watching for changes"),
			otherEntries:  map[string]index.Entry{"claude-sdk-other": {ProjectPath: dir}},
			wantWarn:      true,
		},
		{
			name:          "no collision: other session syncs a different dir",
			workspacePath: dir,
			list:          listLine("claude-sdk-other", "sandbox-claude-sdk-other-project", "Watching for changes"),
			otherEntries:  map[string]index.Entry{"claude-sdk-other": {ProjectPath: "/work/other"}},
			wantWarn:      false,
		},
		{
			name:          "worktree-isolated session never warns (workspace != project)",
			workspacePath: "/state/worktrees/" + ours, // != projectPath, so isolated
			list:          listLine("claude-sdk-other", "sandbox-claude-sdk-other-project", "Watching for changes"),
			otherEntries:  map[string]index.Entry{"claude-sdk-other": {ProjectPath: dir}},
			wantWarn:      false,
		},
		{
			name:          "paused other session is not a live cross-feed",
			workspacePath: dir,
			list:          listLine("claude-sdk-other", "sandbox-claude-sdk-other-project", "Paused"),
			otherEntries:  map[string]index.Entry{"claude-sdk-other": {ProjectPath: dir}},
			wantWarn:      false,
		},
		{
			name:          "our own project sync does not collide with itself",
			workspacePath: dir,
			list:          listLine(ours, "sandbox-"+ours+"-project", "Watching for changes"),
			wantWarn:      false,
		},
		{
			name:          "non-project sync of another session is ignored",
			workspacePath: dir,
			list:          listLine("claude-sdk-other", "sandbox-claude-sdk-other-config-skills", "Watching for changes"),
			otherEntries:  map[string]index.Entry{"claude-sdk-other": {ProjectPath: dir}},
			wantWarn:      false,
		},
		{
			name:          "mutagen absent (list errors) degrades silently",
			workspacePath: dir,
			listErr:       io.ErrUnexpectedEOF,
			otherEntries:  map[string]index.Entry{"claude-sdk-other": {ProjectPath: dir}},
			wantWarn:      false,
		},
		{
			// A pre-MF3 sync has no sandbox-context label (empty second field) but
			// must still be seen by the collision scan — the label is a GC scoping
			// concern, not a collision one.
			name:          "collision with a legacy (context-less) sync still warns",
			workspacePath: dir,
			list:          legacyListLine("claude-sdk-other", "sandbox-claude-sdk-other-project", "Watching for changes"),
			otherEntries:  map[string]index.Entry{"claude-sdk-other": {ProjectPath: dir}},
			wantWarn:      true,
		},
		{
			// A row in an unexpected shape (wrong field count — e.g. output from a
			// stale template) is skipped by the parser: no crash, and the scan
			// degrades to silence exactly like a list error.
			name:          "malformed (old 4-field shape) row degrades silently",
			workspacePath: dir,
			list:          "claude-sdk-other|sync_claude-sdk-other|sandbox-claude-sdk-other-project|Watching for changes",
			otherEntries:  map[string]index.Entry{"claude-sdk-other": {ProjectPath: dir}},
			wantWarn:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(WithBackend(newFakeBackend()), WithStateDir(t.TempDir()))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			c.syncRunner = listStubRunner{out: tc.list, err: tc.listErr}
			for id, e := range tc.otherEntries {
				e.SandboxSessionID = id
				if serr := c.index.Save(id, e); serr != nil {
					t.Fatalf("seed index %s: %v", id, serr)
				}
			}
			s := &Session{c: c, ref: Ref{ID: ours}}
			got := s.sameDirSyncWarning(context.Background(), tc.workspacePath, dir, ours)
			if tc.wantWarn && got == "" {
				t.Errorf("expected a collision warning, got none")
			}
			if !tc.wantWarn && got != "" {
				t.Errorf("expected no warning, got %q", got)
			}
			if tc.wantWarn && !strings.Contains(got, "cross-feed") {
				t.Errorf("warning missing the cross-feed advisory: %q", got)
			}
		})
	}
}

package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/projpath"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// TestMapStageCoversAllClientStages guards the client→dashboard stage bridge: the
// two enums are 1:1, so every client.Stage must map to its matching
// dashboard.ConnectStage. A new client stage that isn't wired here would silently
// fall through to StageCheck, freezing the connect splash on the wrong phase.
func TestMapStageCoversAllClientStages(t *testing.T) {
	want := map[client.Stage]dashboard.ConnectStage{
		client.StageCheck:    dashboard.StageCheck,
		client.StageResume:   dashboard.StageResume,
		client.StageForward:  dashboard.StageForward,
		client.StageRunner:   dashboard.StageRunner,
		client.StageSync:     dashboard.StageSync,
		client.StageOpencode: dashboard.StageOpencode,
		client.StageAttach:   dashboard.StageAttach,
	}
	for in, exp := range want {
		if got := mapStage(in); got != exp {
			t.Errorf("mapStage(%v) = %v, want %v", in, got, exp)
		}
	}
}

// TestCreatorProjectPath: the dashboard Creator's project-path resolution (T10).
// A picked path (CreateParams.ProjectPath) flows through to the CreateOptions
// path in canonical form; empty falls back to the process cwd (the pre-picker
// behavior, identical to `sandbox claude`); a vanished directory fails closed
// with a descriptive error instead of provisioning a session on a dead path.
func TestCreatorProjectPath(t *testing.T) {
	t.Run("picked path canonicalized", func(t *testing.T) {
		dir := t.TempDir()
		want, err := projpath.ValidateDir(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		got, err := creatorProjectPath(dir)
		if err != nil {
			t.Fatalf("creatorProjectPath(%q): %v", dir, err)
		}
		if got != want {
			t.Errorf("creatorProjectPath(%q) = %q, want %q", dir, got, want)
		}
	})

	t.Run("empty falls back to cwd", func(t *testing.T) {
		want, err := resolveProjectPath()
		if err != nil {
			t.Fatal(err)
		}
		got, err := creatorProjectPath("")
		if err != nil {
			t.Fatalf("creatorProjectPath(\"\"): %v", err)
		}
		if got != want {
			t.Errorf("creatorProjectPath(\"\") = %q, want the cwd %q", got, want)
		}
	})

	t.Run("missing dir fails closed", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "gone")
		if _, err := creatorProjectPath(missing); err == nil {
			t.Error("creatorProjectPath accepted a nonexistent directory")
		} else if !strings.Contains(err.Error(), "project path") {
			t.Errorf("error = %v, want it prefixed with \"project path\"", err)
		}
	})
}

// TestStageSinkNilPassthrough: a nil dashboard onStage yields a nil client OnPhase
// (the client treats nil as "no progress reporting"), not a panicking wrapper.
func TestStageSinkNilPassthrough(t *testing.T) {
	if stageSink(nil) != nil {
		t.Error("stageSink(nil) should be nil so the client skips progress reporting")
	}
	// A non-nil sink must forward without panicking.
	var gotStage dashboard.ConnectStage
	var gotDetail string
	sink := stageSink(func(s dashboard.ConnectStage, d string) { gotStage, gotDetail = s, d })
	sink(client.StageSync, "uploading")
	if gotStage != dashboard.StageSync || gotDetail != "uploading" {
		t.Errorf("stageSink forwarded (%v,%q), want (StageSync,\"uploading\")", gotStage, gotDetail)
	}
}

package cli

import (
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
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

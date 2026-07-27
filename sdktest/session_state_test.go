package sdktest

// session_state_test.go — compile-time pins + a behavioral test for two client
// capabilities promoted from internal/cli into the public SDK: session titles
// (client/title.go) and typed sync status/GC (client/syncstatus.go,
// client/sync.go). See surface_test.go's header for the general pin philosophy.
//
// The behavioral test below is the pin that matters most: it proves an
// external module can rename a session — the same thing `sandbox rename` does
// — with client.Offline and NO cluster access at all, because titles live in
// the local session index, not the pod.

import (
	"context"
	"errors"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
)

// --- client: session titles -------------------------------------------------

var (
	_ struct {
		Name string
		Auto string
	} = client.Title{}

	_ func(client.Title) string = client.Title.Display

	_ error = client.ErrEmptyTitle

	_ func(*client.Client, client.ID) (client.Title, error) = (*client.Client).Title
	_ func(*client.Client, client.ID, string) error         = (*client.Client).SetTitle
	_ func(*client.Client, client.ID, string) error         = (*client.Client).SetAutoTitle
)

// --- client: typed sync status ----------------------------------------------

var (
	_ func(*client.Client, context.Context, client.ID) (client.SyncStatus, error) = (*client.Client).SyncStatus

	// client.SyncStatus's field shape.
	_ struct {
		State     client.SyncState
		Conflicts []client.SyncConflict
		Hint      string
		Detail    string
	} = client.SyncStatus{}

	// client.SyncConflict's field shape + Describe.
	_ struct {
		Path   string
		Local  bool
		Remote bool
	} = client.SyncConflict{}
	_ func(client.SyncConflict) string = client.SyncConflict.Describe

	_ client.SyncState = client.SyncUnknown
	_ client.SyncState = client.SyncSynced
	_ client.SyncState = client.SyncSyncing
	_ client.SyncState = client.SyncPaused
	_ client.SyncState = client.SyncStalled
	_ client.SyncState = client.SyncSafetyHalted
	_ client.SyncState = client.SyncConflicted

	_ string = client.SyncConflictHint
)

// --- client: orphan sync GC --------------------------------------------------

var (
	_ struct {
		DryRun bool
	} = client.SyncGCOptions{}

	_ struct {
		Orphans    int
		BySession  map[client.ID]int
		Terminated bool
	} = client.SyncGCResult{}

	_ func(*client.Client, context.Context, client.SyncGCOptions) (client.SyncGCResult, error) = (*client.Client).SyncGC
)

// TestOfflineTitleRoundTrip proves an external module can rename a session and
// read its display label from a fully offline client — no kubeconfig, no
// cluster — the exact capability `sandbox rename` has and, before this change,
// an SDK importer did not.
func TestOfflineTitleRoundTrip(t *testing.T) {
	c, err := client.Offline(client.WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("client.Offline: %v", err)
	}
	const id = client.ID("claude-pane-sdktest")

	// A session with no index entry yields the zero Title and no error.
	title, err := c.Title(id)
	if err != nil {
		t.Fatalf("Title (unknown id): %v", err)
	}
	if title != (client.Title{}) {
		t.Fatalf("Title (unknown id) = %+v, want zero value", title)
	}

	// A blank rename is rejected.
	if err := c.SetTitle(id, "   "); !errors.Is(err, client.ErrEmptyTitle) {
		t.Fatalf("SetTitle(blank) = %v, want ErrEmptyTitle", err)
	}

	// SetTitle → Title → Display round-trips the user-chosen name.
	if err := c.SetTitle(id, "My Renamed Session"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	title, err = c.Title(id)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if title.Display() != "My Renamed Session" {
		t.Fatalf("Display() = %q, want %q", title.Display(), "My Renamed Session")
	}

	// SetAutoTitle records the agent-generated summary but never clobbers the
	// user's chosen name — Display still returns the Name.
	if err := c.SetAutoTitle(id, "agent-generated conversation summary"); err != nil {
		t.Fatalf("SetAutoTitle: %v", err)
	}
	title, err = c.Title(id)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if title.Name != "My Renamed Session" {
		t.Fatalf("Name = %q, want the rename preserved", title.Name)
	}
	if title.Auto != "agent-generated conversation summary" {
		t.Fatalf("Auto = %q, want the auto summary recorded", title.Auto)
	}
	if title.Display() != "My Renamed Session" {
		t.Fatalf("Display() = %q, want the Name to still win over Auto", title.Display())
	}
}

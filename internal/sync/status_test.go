package sync

import "testing"

func TestParseStagingPhase(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"scanning", `[{"status":"Scanning files"}]`, "scanning"},
		{"staging", `[{"status":"Staging files on beta"}]`, "uploading"},
		{"applying", `[{"status":"Applying changes"}]`, "applying"},
		{"reconciling", `[{"status":"Reconciling changes"}]`, "applying"},
		{"connecting", `[{"status":"Connecting to beta"}]`, "connecting"},
		{"watching-no-detail", `[{"status":"Watching for changes"}]`, ""},
		{"empty", `[]`, ""},
		{"worst-wins-uploading", `[{"status":"Scanning files"},{"status":"Staging files on beta"}]`, "uploading"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseStagingPhase([]byte(c.json)); got != c.want {
				t.Fatalf("parseStagingPhase(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestParseSyncState(t *testing.T) {
	cases := []struct {
		name string
		json string
		want SyncState
	}{
		{"watching-clean", `[{"status":"Watching for changes"}]`, SyncSynced},
		{"staging", `[{"status":"Staging files on beta"}]`, SyncSyncing},
		{"scanning", `[{"status":"Scanning files"}]`, SyncSyncing},
		{"halted", `[{"status":"Halted on root emptied"}]`, SyncStalled},
		{"conflicts", `[{"status":"Watching for changes","conflicts":[{"root":"x"}]}]`, SyncConflicted},
		{"empty", `[]`, SyncUnknown},
		{"two-sessions-worst-wins", `[{"status":"Watching for changes"},{"status":"Staging files on beta"}]`, SyncSyncing},
		// A conflict outranks a co-occurring transport stall so the actionable
		// problem surfaces instead of being masked as a generic error.
		{"conflict-beats-halted", `[{"status":"Halted on root emptied"},{"status":"Watching for changes","conflicts":[{"root":"x"}]}]`, SyncConflicted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseSyncState([]byte(c.json))
			if got != c.want {
				t.Fatalf("parseSyncState(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

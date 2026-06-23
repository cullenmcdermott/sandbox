package sync

import "testing"

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
		{"conflicts", `[{"status":"Watching for changes","conflicts":[{"root":"x"}]}]`, SyncStalled},
		{"empty", `[]`, SyncUnknown},
		{"two-sessions-worst-wins", `[{"status":"Watching for changes"},{"status":"Staging files on beta"}]`, SyncSyncing},
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

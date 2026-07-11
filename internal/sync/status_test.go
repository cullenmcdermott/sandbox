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

func TestConflictsFrom(t *testing.T) {
	cases := []struct {
		name string
		json string
		want []Conflict
	}{
		{
			name: "both-sides-same-path",
			json: `[{"status":"Watching for changes","conflicts":[{"alphaChanges":[{"path":"a.go"}],"betaChanges":[{"path":"a.go"}]}]}]`,
			want: []Conflict{{Path: "a.go", Alpha: true, Beta: true}},
		},
		{
			name: "local-only",
			json: `[{"conflicts":[{"alphaChanges":[{"path":"b.go"}]}]}]`,
			want: []Conflict{{Path: "b.go", Alpha: true}},
		},
		{
			name: "pod-only",
			json: `[{"conflicts":[{"betaChanges":[{"path":"c.go"}]}]}]`,
			want: []Conflict{{Path: "c.go", Beta: true}},
		},
		{
			name: "two-distinct-paths-order-preserved",
			json: `[{"conflicts":[{"alphaChanges":[{"path":"z.go"}],"betaChanges":[{"path":"z.go"}]},{"betaChanges":[{"path":"a.go"}]}]}]`,
			want: []Conflict{{Path: "z.go", Alpha: true, Beta: true}, {Path: "a.go", Beta: true}},
		},
		{
			// Defensive: an unrecognized/older shape (root-only, no changes) still
			// yields a generic entry so the count is honest.
			name: "unparseable-shape-generic-entry",
			json: `[{"conflicts":[{"root":"x"}]}]`,
			want: []Conflict{{Path: "(path unavailable)"}},
		},
		{
			name: "no-conflicts",
			json: `[{"status":"Watching for changes"}]`,
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := conflictsFrom(decodeSessions([]byte(c.json)))
			if len(got) != len(c.want) {
				t.Fatalf("conflictsFrom(%s) = %+v, want %+v", c.name, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("conflictsFrom(%s)[%d] = %+v, want %+v", c.name, i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestConflictDescribe(t *testing.T) {
	cases := []struct {
		c    Conflict
		want string
	}{
		{Conflict{Path: "a.go", Alpha: true, Beta: true}, "a.go (both sides changed it)"},
		{Conflict{Path: "a.go", Alpha: true}, "a.go (changed locally)"},
		{Conflict{Path: "a.go", Beta: true}, "a.go (changed on the pod)"},
		{Conflict{Path: "a.go"}, "a.go"},
	}
	for _, c := range cases {
		if got := c.c.Describe(); got != c.want {
			t.Errorf("Describe(%+v) = %q, want %q", c.c, got, c.want)
		}
	}
}

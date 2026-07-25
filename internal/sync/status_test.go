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
		// [V2] A safety halt (root emptied/deleted/type change) is its OWN state,
		// not the heal-eligible SyncStalled — auto-resuming it would confirm a mass
		// deletion.
		{"safety-halted", `[{"status":"Halted on root emptied"}]`, SyncSafetyHalted},
		{"root-deletion-halt", `[{"status":"Halted on root deletion"}]`, SyncSafetyHalted},
		// A transport error still stalls (heal-eligible).
		{"error-stalls", `[{"status":"Error: connection reset"}]`, SyncStalled},
		// [V14] A paused sync is its own honest state, not perpetual "syncing".
		{"paused", `[{"status":"Paused"}]`, SyncPaused},
		{"conflicts", `[{"status":"Watching for changes","conflicts":[{"root":"x"}]}]`, SyncConflicted},
		{"empty", `[]`, SyncUnknown},
		// A DROPPED transport sits in connecting-alpha/connecting-beta carrying a
		// lastError. It used to fall through to the default and render as healthy
		// in-flight "syncing", which also kept it out of healEligible so the
		// self-heal never fired. It must stall (heal-eligible) instead. Both the
		// hyphenated machine status and mutagen's human form are covered.
		{"dropped-transport-stalls", `[{"status":"connecting-beta","lastError":"beta polling error: unable to receive poll response: unable to read message length: unexpected EOF"}]`, SyncStalled},
		{"dropped-alpha-stalls", `[{"status":"Connecting to alpha","lastError":"alpha polling error: unexpected EOF"}]`, SyncStalled},
		// A sync that has NEVER connected carries no lastError — that is a genuine
		// first connect, still in flight, and must not be reported as broken.
		{"first-connect-is-syncing", `[{"status":"connecting-beta"}]`, SyncSyncing},
		{"blank-lasterror-is-syncing", `[{"status":"connecting-beta","lastError":"   "}]`, SyncSyncing},
		// One dead sync among healthy ones still surfaces (worst-of reducer).
		{"one-dropped-among-healthy", `[{"status":"Watching for changes"},{"status":"connecting-beta","lastError":"unexpected EOF"}]`, SyncStalled},
		{"two-sessions-worst-wins", `[{"status":"Watching for changes"},{"status":"Staging files on beta"}]`, SyncSyncing},
		// A conflict outranks a co-occurring safety halt so the actionable
		// resolution surfaces (both need user action; conflict wins the reducer).
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

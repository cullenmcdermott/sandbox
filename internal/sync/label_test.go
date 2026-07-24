package sync

import (
	"context"
	"strings"
	"testing"
)

// [V3] sanitizeLabelValue must coerce any kube-context name into a valid mutagen
// label value: alphanumerics + '-'/'_'/'.', <=63 chars, alphanumeric first/last.
// A value that is already valid and within length passes through UNCHANGED
// (backward compat with existing labels like "my-cluster").
func TestSanitizeLabelValue(t *testing.T) {
	longClean := strings.Repeat("a", 80) // valid chars but > 63

	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"clean-unchanged", "my-cluster"},
		{"kubeadm-default", "kubernetes-admin@kubernetes"},
		{"eks-arn", "arn:aws:eks:us-east-1:123456789012:cluster/prod"},
		{"over-63-clean", longClean},
		{"leading-symbol", "@weird"},
		{"all-symbols", "@@@///"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeLabelValue(c.in)

			if c.in == "" {
				if got != "" {
					t.Fatalf("empty in must give empty out, got %q", got)
				}
				return
			}
			// "my-cluster" is already valid → unchanged.
			if c.in == "my-cluster" && got != "my-cluster" {
				t.Fatalf("a valid label must pass through unchanged, got %q", got)
			}
			// Never longer than mutagen's 63-char cap.
			if len(got) > 63 {
				t.Fatalf("sanitize(%q) = %q exceeds 63 chars", c.in, got)
			}
			// Must be non-empty and validly charset'd for any non-empty input.
			if got == "" {
				t.Fatalf("sanitize(%q) produced an empty (invalid) label", c.in)
			}
			for i, r := range got {
				if !labelRune(r) {
					t.Fatalf("sanitize(%q) = %q has invalid rune %q at %d", c.in, got, r, i)
				}
			}
			if !alnumRune(rune(got[0])) || !alnumRune(rune(got[len(got)-1])) {
				t.Fatalf("sanitize(%q) = %q must start and end alphanumeric", c.in, got)
			}
			// Anything that had to change gets a distinguishing hash suffix, so two
			// distinct originals never collide on the same label.
			if c.in == "kubernetes-admin@kubernetes" || c.in == longClean {
				if !strings.Contains(got, "-") {
					t.Fatalf("changed/clamped value should carry a hash suffix, got %q", got)
				}
			}
		})
	}

	// Distinct invalid originals must not sanitize to the same label (hash suffix).
	a := sanitizeLabelValue("ctx@one")
	b := sanitizeLabelValue("ctx#one")
	if a == b {
		t.Fatalf("distinct originals collided: %q == %q", a, b)
	}
}

// [V28] CreateProject stamps `--label sandbox-namespace=<ns>` alongside the
// session + context labels when the Manager has a namespace set, and stamps none
// when it is unset (legacy / no-namespace callers).
func TestCreateProjectStampsNamespaceLabel(t *testing.T) {
	r := &fakeRunner{}
	m := withContext(r, "my-cluster")
	m.SetNamespace("team-b")
	spec := Spec{
		SessionID:    "s1",
		ProjectPath:  t.TempDir(),
		RemotePath:   "/session/workspace/proj",
		HomeDir:      "/Users/cullen",
		SSHHost:      "sandbox-s1",
		RemoteClaude: "/session/state/claude",
	}
	if _, err := m.CreateProject(context.Background(), spec); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	got := strings.Join(createCalls(r.calls)[0], " ") // skip the pre-create existence probe
	if !strings.Contains(got, "--label sandbox-namespace=team-b") {
		t.Errorf("project sync missing namespace label: %s", got)
	}

	// No namespace set → no namespace label (backward compat with pre-[V28] syncs).
	r2 := &fakeRunner{}
	m2 := withContext(r2, "my-cluster") // namespace unset
	if _, err := m2.CreateProject(context.Background(), spec); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if got2 := strings.Join(createCalls(r2.calls)[0], " "); strings.Contains(got2, "sandbox-namespace=") {
		t.Errorf("no namespace label should be stamped when unset: %s", got2)
	}
}

// [V28] parseSyncList reads the sandbox-namespace label from the 6th field, and
// still accepts a legacy 5-field row (empty Namespace) so a pre-[V28] daemon
// template shape keeps parsing.
func TestParseSyncListNamespaceField(t *testing.T) {
	out := strings.Join([]string{
		"sess-a|my-cluster|sync_1|sandbox-sess-a-project|Watching|team-b", // 6 fields
		"sess-b|my-cluster|sync_2|sandbox-sess-b-project|Paused",          // 5 fields (legacy)
	}, "\n")
	got := parseSyncList([]byte(out))
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(got), got)
	}
	if got[0].Namespace != "team-b" {
		t.Errorf("row 0 namespace = %q, want team-b", got[0].Namespace)
	}
	if got[1].Namespace != "" {
		t.Errorf("legacy 5-field row must have empty namespace, got %q", got[1].Namespace)
	}
}

// [V35] IsPausedStatus distinguishes an intentional pause from a transport-down
// orphan so the GC can reap a paused sync only once its session is gone.
func TestIsPausedStatus(t *testing.T) {
	cases := []struct {
		status string
		paused bool
	}{
		{"Paused", true},
		{"paused", true},
		{"Watching", false},
		{"ConnectingBeta", false},
		{"Disconnected", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsPausedStatus(c.status); got != c.paused {
			t.Errorf("IsPausedStatus(%q) = %v, want %v", c.status, got, c.paused)
		}
	}
}

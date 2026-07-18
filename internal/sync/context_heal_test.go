package sync

import (
	"context"
	"strings"
	"testing"
)

// withContext returns a Manager whose sandbox-context label resolves to ctx
// deterministically (bypassing the ambient kubeconfig), so create-arg assertions
// don't depend on the machine running the tests.
func withContext(r Runner, ctx string) *Manager {
	m := New(r)
	m.kubeContext = func() string { return ctx }
	return m
}

// MF3: CreateProject stamps `--label sandbox-context=<ctx>` alongside the
// per-session label so GC can scope to the context that created the sync.
func TestCreateProjectStampsContextLabel(t *testing.T) {
	r := &fakeRunner{}
	m := withContext(r, "my-cluster")
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
	got := strings.Join(r.calls[0], " ")
	if !strings.Contains(got, "--label sandbox-context=my-cluster") {
		t.Errorf("project sync missing context label: %s", got)
	}
	// The session label must still be present (context is additive, not a swap).
	if !strings.Contains(got, "--label sandbox-session=s1") {
		t.Errorf("project sync missing session label: %s", got)
	}
	// Endpoints stay the trailing positional args despite the extra label pair.
	alpha, beta := endpoints(t, r.calls[0])
	if alpha != spec.ProjectPath || beta != spec.SSHHost+":"+spec.RemotePath {
		t.Errorf("context label displaced endpoints: alpha=%q beta=%q", alpha, beta)
	}
}

// MF3: CreateInputs stamps the context label on every one of its 7 syncs.
func TestCreateInputsStampsContextLabel(t *testing.T) {
	r := &fakeRunner{}
	m := withContext(r, "my-cluster")
	spec := Spec{
		SessionID:    "s1",
		ProjectPath:  t.TempDir(),
		HomeDir:      "/Users/cullen",
		SSHHost:      "sandbox-s1",
		RemoteClaude: "/session/state/claude",
	}
	if err := m.CreateInputs(context.Background(), spec); err != nil {
		t.Fatalf("CreateInputs: %v", err)
	}
	for i, call := range r.calls {
		if !strings.Contains(strings.Join(call, " "), "--label sandbox-context=my-cluster") {
			t.Errorf("input sync %d missing context label: %s", i, strings.Join(call, " "))
		}
	}
}

// MF3 migration: an unresolvable context ("") stamps NO context label, so an
// in-cluster / kubeconfig-less caller still creates working (unlabeled) syncs —
// matching pre-MF3 sessions, which GC then scopes via the live-set fallback.
func TestCreateProjectNoContextLabelWhenUnresolved(t *testing.T) {
	r := &fakeRunner{}
	m := withContext(r, "")
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
	if got := strings.Join(r.calls[0], " "); strings.Contains(got, "sandbox-context=") {
		t.Errorf("no context label should be stamped when the context is unresolved: %s", got)
	}
}

// MF5: Reconcile self-heals a stalled session — resume (un-pause) then flush
// (force a cycle), both label-scoped and in that order.
func TestReconcileResumesThenFlushes(t *testing.T) {
	r := &fakeRunner{}
	m := New(r)
	if err := m.Reconcile(context.Background(), "s1"); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("Reconcile should issue resume+flush (2 calls), got %d: %v", len(r.calls), r.calls)
	}
	first := strings.Join(r.calls[0], " ")
	second := strings.Join(r.calls[1], " ")
	if !strings.Contains(first, "sync resume") || !strings.Contains(first, "sandbox-session=s1") {
		t.Errorf("first Reconcile call should be a label-scoped resume: %s", first)
	}
	if !strings.Contains(second, "sync flush") || !strings.Contains(second, "sandbox-session=s1") {
		t.Errorf("second Reconcile call should be a label-scoped flush: %s", second)
	}
}

// MF5: Reconcile treats "no sessions" as success — a session that was never
// synced (or already gone) must not error the self-heal.
func TestReconcileIgnoresNotFound(t *testing.T) {
	m := New(&errorRunner{msg: "no sessions found"})
	if err := m.Reconcile(context.Background(), "s1"); err != nil {
		t.Errorf("Reconcile should ignore not-found, got %v", err)
	}
}

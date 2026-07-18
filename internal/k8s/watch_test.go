package k8s

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes/fake"
	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestWatchDeliversAddedSandbox verifies that Watch emits a StateEvent when a
// Sandbox is added to the fake store.
func TestWatchDeliversAddedSandbox(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	ch, err := b.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Create a Sandbox object in the fake cluster after the watch is started.
	one := int32(1)
	sb := &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "watch-test-a",
			Namespace: "agent-sessions",
		},
		Spec: agentv1alpha1.SandboxSpec{Replicas: &one},
	}
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Create(ctx, sb, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	// The informer should deliver an Added event.
	select {
	case ev := <-ch:
		if ev.State.ID != "watch-test-a" {
			t.Errorf("event ID: got %q, want watch-test-a", ev.State.ID)
		}
		if ev.Deleted {
			t.Error("expected Added event, not Deleted")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for watch event")
	}
}

// TestWatchDeliversSuspendedState verifies that a Sandbox with replicas=0 is
// delivered with StatusSuspended.
func TestWatchDeliversSuspendedState(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	// Pre-create the Sandbox before starting the watch so it's in the initial
	// list sync.
	zero := int32(0)
	sb := &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "watch-test-suspended",
			Namespace: "agent-sessions",
		},
		Spec: agentv1alpha1.SandboxSpec{Replicas: &zero},
	}
	ctx := context.Background()
	if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Create(ctx, sb, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	watchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ch, err := b.Watch(watchCtx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Drain events until we find the suspended session.
	var found bool
	deadline := time.After(4 * time.Second)
	for !found {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("watch channel closed unexpectedly")
			}
			if ev.State.ID == "watch-test-suspended" {
				found = true
				if ev.State.Status != session.StatusSuspended {
					t.Errorf("suspended sandbox: got status %v, want SUSPENDED", ev.State.Status)
				}
			}
		case <-deadline:
			t.Fatal("timeout waiting for suspended event")
		}
	}
}

// TestSandboxToState verifies the sandboxToState helper directly.
func TestSandboxToState(t *testing.T) {
	tests := []struct {
		name     string
		replicas int32
		ready    string // "" = no Ready condition; else metav1.ConditionStatus
		want     session.Status
	}{
		{"starting", 1, "", session.StatusCreating},             // replicas want a pod, not Ready yet
		{"not-ready", 1, "False", session.StatusCreating},       // pod exists but not Ready
		{"ready", 1, "True", session.StatusRunning},             // Ready=True → running, no pod Get
		{"suspended", 0, "", session.StatusSuspended},           // replicas 0 → suspended
		{"suspended-ready", 0, "True", session.StatusSuspended}, // Ready=True on a 0-replica suspend doesn't un-suspend
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.replicas
			sb := &agentv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{Name: "test-" + tc.name},
				Spec:       agentv1alpha1.SandboxSpec{Replicas: &r},
			}
			if tc.ready != "" {
				sb.Status.Conditions = []metav1.Condition{{
					Type:   string(agentv1alpha1.SandboxConditionReady),
					Status: metav1.ConditionStatus(tc.ready),
				}}
			}
			st := sandboxToState(sb)
			if st.Status != tc.want {
				t.Errorf("sandboxToState(replicas=%d ready=%q): got %v, want %v", tc.replicas, tc.ready, st.Status, tc.want)
			}
		})
	}
}

// TestSandboxToStateWorkspaceProjectSplit pins the [V11] fix: sandboxToState
// must recover WorkspacePath (pod cwd / Mutagen alpha) from PROJECT_PATH and
// ProjectPath (repo root, for display/grouping) from SANDBOX_PROJECT_ROOT —
// mirroring statusFromSandbox — so a watch-inserted worktree session doesn't
// carry the worktree dir as its ProjectPath. A legacy pod without
// SANDBOX_PROJECT_ROOT falls both back to PROJECT_PATH.
func TestSandboxToStateWorkspaceProjectSplit(t *testing.T) {
	sbWithEnv := func(name string, env map[string]string) *agentv1alpha1.Sandbox {
		one := int32(1)
		vars := make([]corev1.EnvVar, 0, len(env))
		for k, v := range env {
			vars = append(vars, corev1.EnvVar{Name: k, Value: v})
		}
		return &agentv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: agentv1alpha1.SandboxSpec{
				Replicas: &one,
				PodTemplate: agentv1alpha1.PodTemplate{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "runner", Env: vars}},
					},
				},
			},
		}
	}

	t.Run("worktree pod splits workspace from repo root", func(t *testing.T) {
		sb := sbWithEnv("wt", map[string]string{
			"PROJECT_PATH":         "/state/worktrees/wt-abc",
			"SANDBOX_PROJECT_ROOT": "/home/me/repo",
		})
		st := sandboxToState(sb)
		if st.WorkspacePath != "/state/worktrees/wt-abc" {
			t.Errorf("WorkspacePath: got %q, want the worktree dir (PROJECT_PATH)", st.WorkspacePath)
		}
		if st.ProjectPath != "/home/me/repo" {
			t.Errorf("ProjectPath: got %q, want the repo root (SANDBOX_PROJECT_ROOT)", st.ProjectPath)
		}
	})

	t.Run("legacy pod without SANDBOX_PROJECT_ROOT falls both back to PROJECT_PATH", func(t *testing.T) {
		sb := sbWithEnv("legacy", map[string]string{
			"PROJECT_PATH": "/home/me/repo",
		})
		st := sandboxToState(sb)
		if st.WorkspacePath != "/home/me/repo" {
			t.Errorf("WorkspacePath: got %q, want /home/me/repo", st.WorkspacePath)
		}
		if st.ProjectPath != "/home/me/repo" {
			t.Errorf("ProjectPath: got %q, want /home/me/repo (PROJECT_PATH fallback)", st.ProjectPath)
		}
	})
}

// TestWatchDoesNotDropEventWhenChannelFull verifies that Watch blocks the
// informer goroutine rather than silently dropping events when its channel
// is full (B6). We create more Sandbox objects than the channel buffer, then
// drain them all and confirm every event arrived.
func TestWatchDoesNotDropEventWhenChannelFull(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	b := NewForClients(agents, core, "agent-sessions")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	ch, err := b.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Allow the informer to complete its initial list+sync before flooding it
	// with creates. Without this, some objects end up in the initial List call
	// and some in the watch stream; the interleaving can cause the fake
	// informer to skip events on a race.
	time.Sleep(100 * time.Millisecond)

	one := int32(1)
	// Create 40 sandboxes (> the 32-element output channel buffer) so the channel
	// would fill up under the old non-blocking send. With the coalescing buffer
	// (one latest StateEvent per session id) drained by the sender goroutine into
	// the 32-buffered out channel, every distinct session's event must still
	// arrive (B6 fix): the buffer only supersedes stale same-session events, so
	// no distinct session is dropped no matter how far behind the consumer falls.
	const n = 40
	for i := 0; i < n; i++ {
		sb := &agentv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("burst-%02d", i),
				Namespace: "agent-sessions",
			},
			Spec: agentv1alpha1.SandboxSpec{Replicas: &one},
		}
		if _, err := agents.AgentsV1alpha1().Sandboxes("agent-sessions").Create(ctx, sb, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create sandbox %d: %v", i, err)
		}
	}

	// Drain the channel and count events; all n must arrive.
	seen := make(map[string]bool)
	for len(seen) < n {
		select {
		case ev := <-ch:
			seen[string(ev.State.ID)] = true
		case <-ctx.Done():
			t.Fatalf("timeout: only got %d/%d events; dropped events detected (B6 regression)", len(seen), n)
		}
	}
}

// TestCoalescingBufferLatestWins verifies the watch's coalescing buffer (C7):
// many events for one session collapse to the latest (so a slow consumer can't
// grow memory unboundedly or block the informer), first-seen order is kept
// across distinct sessions, and a terminal Deleted event survives as the latest.
func TestCoalescingBufferLatestWins(t *testing.T) {
	b := newCoalescingBuffer()

	// push must never block even with many un-drained events for one id.
	b.push(StateEvent{State: session.State{ID: "a", Status: session.StatusCreating}})
	b.push(StateEvent{State: session.State{ID: "b", Status: session.StatusCreating}})
	b.push(StateEvent{State: session.State{ID: "a", Status: session.StatusFailed}})    // supersedes a
	b.push(StateEvent{State: session.State{ID: "a", Status: session.StatusSuspended}}) // supersedes a again

	got := b.drain()
	if len(got) != 2 {
		t.Fatalf("want 2 coalesced events (a,b), got %d: %+v", len(got), got)
	}
	if got[0].State.ID != "a" || got[0].State.Status != session.StatusSuspended {
		t.Errorf("session a should coalesce to its latest (suspended), got %+v", got[0])
	}
	if got[1].State.ID != "b" {
		t.Errorf("session b should be delivered second (first-seen order), got %+v", got[1])
	}
	if rest := b.drain(); rest != nil {
		t.Errorf("buffer should be empty after drain, got %+v", rest)
	}

	// A terminal Deleted event must supersede an earlier running state.
	b.push(StateEvent{State: session.State{ID: "c", Status: session.StatusCreating}})
	b.push(StateEvent{State: session.State{ID: "c", Status: session.StatusGone}, Deleted: true})
	d := b.drain()
	if len(d) != 1 || !d[0].Deleted {
		t.Fatalf("a Deleted event must survive coalescing as the latest, got %+v", d)
	}
}

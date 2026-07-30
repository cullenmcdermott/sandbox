package k8s

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// [R1b] dropped the Sandbox Get that used to precede every pod lookup, and with
// it the ability to tell "there is no such session" from "the session exists but
// has no pod". Those call for opposite user actions — recreate it vs. resume it
// or wait — and Exec (`sandbox shell`) and PodIP (the out-of-namespace reaper)
// both reach the lookup cold, with no prior Status to have caught the first.
//
// The fast path must stay exactly as [R1b] left it: a successful lookup costs no
// Sandbox Get. The diagnosis runs only once the lookup has already come back
// empty.

func TestPodIPOnAMissingSandboxSaysTheSessionDoesNotExist(t *testing.T) {
	b := NewForClients(agentsfake.NewSimpleClientset(), fake.NewSimpleClientset(), "agent-sessions")

	_, err := b.PodIP(context.Background(), session.Ref{ID: "sess-gone"})
	if err == nil {
		t.Fatal("PodIP on a nonexistent session returned no error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("PodIP error = %q, want it to say the session does not exist — "+
			"a destroyed session needs a different fix from one with no pod", err)
	}
}

// The complementary case: the Sandbox is there, it just has no pod (suspended,
// or still scheduling). That must NOT claim the session is gone.
func TestPodIPOnASandboxWithNoPodSaysNoPod(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	seedSandboxFor(t, agents, "sess-suspended")
	b := NewForClients(agents, fake.NewSimpleClientset(), "agent-sessions")

	_, err := b.PodIP(context.Background(), session.Ref{ID: "sess-suspended"})
	if err == nil {
		t.Fatal("PodIP with no pod returned no error")
	}
	if strings.Contains(err.Error(), "does not exist") {
		t.Errorf("PodIP error = %q, want a no-pod message: the session exists, it "+
			"is just suspended or still scheduling", err)
	}
	if !strings.Contains(err.Error(), "no pod") {
		t.Errorf("PodIP error = %q, want it to name the missing pod", err)
	}
}

func TestExecOnAMissingSandboxSaysTheSessionDoesNotExist(t *testing.T) {
	b := NewForClients(agentsfake.NewSimpleClientset(), fake.NewSimpleClientset(), "agent-sessions")

	err := b.Exec(context.Background(), session.Ref{ID: "sess-gone"},
		[]string{"true"}, nil, nil, nil, false, nil)
	if err == nil {
		t.Fatal("Exec on a nonexistent session returned no error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("Exec error = %q, want it to say the session does not exist; "+
			"`sandbox shell` reaches here with no prior Status to have caught it", err)
	}
}

// The point of [R1b] was to stop paying a Sandbox Get per lookup. The diagnosis
// above must not quietly reintroduce it: a lookup that finds its pod still costs
// zero.
func TestPodLookupStillCostsNoSandboxGetOnTheFastPath(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	seedSandboxFor(t, agents, "sess-live")
	pod := mkReadyPod("sess-live-0", "sess-live", false)
	pod.Status.PodIP = "10.1.2.3"
	b := NewForClients(agents, fake.NewSimpleClientset(pod), "agent-sessions")

	// Only count what the lookup itself does.
	agents.ClearActions()

	ip, err := b.PodIP(context.Background(), session.Ref{ID: "sess-live"})
	if err != nil {
		t.Fatalf("PodIP: %v", err)
	}
	if ip != "10.1.2.3" {
		t.Errorf("PodIP = %q, want 10.1.2.3", ip)
	}
	if n := countSandboxGets(agents.Actions()); n != 0 {
		t.Errorf("a successful pod lookup issued %d Sandbox Gets, want 0 — the "+
			"diagnosis belongs on the failure path only", n)
	}
}

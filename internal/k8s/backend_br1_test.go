package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestRunnerContainerSecurityContext verifies BR1: the runner container drops
// the default capabilities sshd/the agent don't need (NET_RAW, MKNOD) while
// keeping sshd's privsep + port-bind capabilities, and disables privilege
// escalation.
func TestRunnerContainerSecurityContext(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	ref, err := b.CreateSession(ctx, session.Spec{
		ID: "test-secctx", ProjectPath: "/tmp", Backend: "claude-sdk", RunnerImage: "test:latest",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sb, err := b.agents.AgentsV1alpha1().Sandboxes("agent-sessions").Get(ctx, string(ref.ID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}

	sc := sb.Spec.PodTemplate.Spec.Containers[0].SecurityContext
	if sc == nil {
		t.Fatal("container securityContext not set (BR1)")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("allowPrivilegeEscalation should be false (BR1)")
	}
	if sc.Capabilities == nil {
		t.Fatal("capabilities not set (BR1)")
	}

	dropsAll := false
	for _, d := range sc.Capabilities.Drop {
		if d == "ALL" {
			dropsAll = true
		}
	}
	if !dropsAll {
		t.Error("capabilities should drop ALL (BR1)")
	}

	// NET_RAW and MKNOD must NOT be added back.
	for _, a := range sc.Capabilities.Add {
		if a == "NET_RAW" || a == "MKNOD" {
			t.Errorf("capability %s must not be added back (BR1)", a)
		}
	}

	// sshd privilege separation + port-22 bind must remain available.
	need := map[corev1.Capability]bool{"SETUID": true, "SETGID": true, "SYS_CHROOT": true, "NET_BIND_SERVICE": true}
	for _, a := range sc.Capabilities.Add {
		delete(need, a)
	}
	if len(need) > 0 {
		t.Errorf("missing sshd-required capabilities in add-list (BR1): %v", need)
	}
}

package k8s

import (
	"context"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"
)

// TestSessionsForAccount verifies the read-only enumeration `auth logout` uses:
// only per-session Secrets labeled with the target account id are returned, and
// the session id comes from the labelSessionID label.
func TestSessionsForAccount(t *testing.T) {
	ctx := context.Background()
	core := fake.NewSimpleClientset()
	b := NewForClients(agentsfake.NewSimpleClientset(), core, "agent-sessions")

	seed := func(name, sessionID, account string) {
		labels := map[string]string{labelSessionID: sessionID}
		if account != "" {
			labels[labelAnthropicAccount] = account
		}
		_, err := core.CoreV1().Secrets("agent-sessions").Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agent-sessions", Labels: labels},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("seed secret %s: %v", name, err)
		}
	}

	seed("sess-1-runner", "sess-1", "acct-work")
	seed("sess-2-runner", "sess-2", "acct-work")
	seed("sess-3-runner", "sess-3", "acct-personal")
	seed("sess-4-runner", "sess-4", "") // no account label (shared-Secret session)

	got, err := b.SessionsForAccount(ctx, "acct-work")
	if err != nil {
		t.Fatalf("SessionsForAccount: %v", err)
	}
	sort.Strings(got)
	want := []string{"sess-1", "sess-2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	// An account with no live copies returns an empty (non-nil) slice.
	none, err := b.SessionsForAccount(ctx, "acct-nobody")
	if err != nil {
		t.Fatalf("SessionsForAccount(none): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected no sessions, got %v", none)
	}
}

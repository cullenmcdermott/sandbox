package k8s

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1fake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// stampSandboxUID makes the agents fake clientset assign a UID on Sandbox
// create, the way a real API server does. Without it the created Sandbox has an
// empty UID and CreateSession skips the ownerReferences pass (an owner ref with
// an empty UID is invalid and the API server would reject it), so the fake must
// emulate the server here for the owner-ref path to be exercised at all.
func stampSandboxUID(agents *agentsfake.Clientset) {
	agents.PrependReactor("create", "sandboxes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if sb, ok := action.(k8stesting.CreateAction).GetObject().(*agentv1alpha1.Sandbox); ok {
			sb.UID = types.UID("uid-" + sb.Name)
		}
		return false, nil, nil
	})
}

func ownedBySandbox(refs []metav1.OwnerReference, name string) bool {
	for _, r := range refs {
		if r.Kind == "Sandbox" && r.Name == name && r.APIVersion == agentv1alpha1.GroupVersion.String() && r.UID == types.UID("uid-"+name) {
			return true
		}
	}
	return false
}

// TestCreateSessionSetsOwnerReferences pins §6 item 3: the per-session Secret and
// PVC created by CreateSession carry an ownerReference to the Sandbox, so a
// cluster-side `kubectl delete sandbox` outside the CLI cascades to them instead
// of orphaning a live runner-token Secret and the PVC.
func TestCreateSessionSetsOwnerReferences(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := corev1fake.NewSimpleClientset()
	stampSandboxUID(agents)
	b := NewForClients(agents, core, "agent-sessions")

	spec := session.Spec{
		ID:          "owner-ref-test",
		ProjectPath: "/tmp",
		Backend:     "claude-sdk",
		RunnerImage: "test:latest",
	}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("create: %v", err)
	}

	secret, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("owner-ref-test"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if !ownedBySandbox(secret.OwnerReferences, "owner-ref-test") {
		t.Errorf("Secret missing ownerReference to Sandbox: %+v", secret.OwnerReferences)
	}

	pvc, err := core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "owner-ref-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}
	if !ownedBySandbox(pvc.OwnerReferences, "owner-ref-test") {
		t.Errorf("PVC missing ownerReference to Sandbox: %+v", pvc.OwnerReferences)
	}
}

// TestCreateSessionOwnerRefSkipsPreexistingPVC pins the C7 guard: a PVC that
// pre-existed this call (a prior session's workspace) must NOT be adopted with an
// owner ref, so it is not GC'd out from under its real owner. Here the PVC (and
// Secret) already exist before CreateSession runs and carry no owner ref; the
// call must leave them owner-ref-free.
func TestCreateSessionOwnerRefSkipsPreexistingPVC(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := corev1fake.NewSimpleClientset()
	stampSandboxUID(agents)
	b := NewForClients(agents, core, "agent-sessions")

	spec := session.Spec{
		ID:          "preexist-test",
		ProjectPath: "/tmp",
		Backend:     "claude-sdk",
		RunnerImage: "test:latest",
	}
	// First create: fresh Secret + PVC (both get an owner ref).
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Simulate the resources belonging to a prior call with NO owner ref (e.g. a
	// session created before this feature landed): strip the owner refs, then
	// re-create. The re-create sees AlreadyExists for both, so pvcPreexisted /
	// secretPreexisted are true and it must not re-adopt them.
	stripOwnerRefs(ctx, t, core, "preexist-test")
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("re-create: %v", err)
	}

	pvc, err := core.CoreV1().PersistentVolumeClaims("agent-sessions").Get(ctx, "preexist-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}
	if len(pvc.OwnerReferences) != 0 {
		t.Errorf("pre-existing PVC was adopted with an owner ref on re-create: %+v", pvc.OwnerReferences)
	}
	secret, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("preexist-test"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if len(secret.OwnerReferences) != 0 {
		t.Errorf("pre-existing Secret was adopted with an owner ref on re-create: %+v", secret.OwnerReferences)
	}
}

// TestCreateSessionOwnerRefSurvivesCredentialReconcile pins constraint (b): the
// credential reconcile that runs on a re-create (syncSessionCredential, a
// Get+Update on the Secret) must not strip the owner ref set on the first create.
func TestCreateSessionOwnerRefSurvivesCredentialReconcile(t *testing.T) {
	ctx := context.Background()
	agents := agentsfake.NewSimpleClientset()
	core := corev1fake.NewSimpleClientset()
	stampSandboxUID(agents)
	b := NewForClients(agents, core, "agent-sessions")

	spec := session.Spec{
		ID:                  "reconcile-test",
		ProjectPath:         "/tmp",
		Backend:             "claude-sdk",
		RunnerImage:         "test:latest",
		AnthropicAccountID:  "acct-a",
		AnthropicCredential: []byte("sk-ant-oat-A"),
	}
	if _, err := b.CreateSession(ctx, spec); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Re-create the same session id with a different (same-shape) account: this
	// drives syncSessionCredential to Update the Secret's Data + Labels.
	swapped := spec
	swapped.AnthropicAccountID = "acct-b"
	swapped.AnthropicCredential = []byte("sk-ant-oat-B")
	if _, err := b.CreateSession(ctx, swapped); err != nil {
		t.Fatalf("re-create: %v", err)
	}

	secret, err := core.CoreV1().Secrets("agent-sessions").Get(ctx, sessionSecretName("reconcile-test"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	// The reconcile must have updated the credential...
	if string(secret.Data[secretKeyAnthropicCredential]) != "sk-ant-oat-B" {
		t.Errorf("credential not reconciled: got %q, want sk-ant-oat-B", secret.Data[secretKeyAnthropicCredential])
	}
	// ...without dropping the owner ref set on the first create.
	if !ownedBySandbox(secret.OwnerReferences, "reconcile-test") {
		t.Errorf("credential reconcile stripped the Secret's ownerReference: %+v", secret.OwnerReferences)
	}
}

func stripOwnerRefs(ctx context.Context, t *testing.T, core *corev1fake.Clientset, id string) {
	t.Helper()
	secrets := core.CoreV1().Secrets("agent-sessions")
	secret, err := secrets.Get(ctx, sessionSecretName(id), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret to strip: %v", err)
	}
	secret.OwnerReferences = nil
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("strip secret owner refs: %v", err)
	}
	pvcs := core.CoreV1().PersistentVolumeClaims("agent-sessions")
	pvc, err := pvcs.Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc to strip: %v", err)
	}
	pvc.OwnerReferences = nil
	if _, err := pvcs.Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("strip pvc owner refs: %v", err)
	}
}

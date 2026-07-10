package k8s

import (
	"context"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func newReaperBackend() (*Backend, *fake.Clientset) {
	core := fake.NewSimpleClientset()
	b := NewForClients(agentsfake.NewSimpleClientset(), core, "agent-sessions")
	return b, core
}

func finishedJob(name string, condType batchv1.JobConditionType) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ReaperNamespace},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: condType, Status: corev1.ConditionTrue},
			},
		},
	}
}

// EnsureReaper with no existing Job must create one.
func TestEnsureReaperCreatesWhenAbsent(t *testing.T) {
	ctx := context.Background()
	b, core := newReaperBackend()

	if err := b.EnsureReaper(ctx, session.Ref{ID: "sess-1"}, ReaperOptions{}); err != nil {
		t.Fatalf("EnsureReaper: %v", err)
	}

	name := reaperJobName("sess-1")
	got, err := core.BatchV1().Jobs(ReaperNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected reaper Job %s to exist: %v", name, err)
	}
	if got.Labels[labelSessionID] != "sess-1" {
		t.Errorf("session-id label = %q, want sess-1", got.Labels[labelSessionID])
	}
	// Defaults must be applied to the reap args.
	args := got.Spec.Template.Spec.Containers[0].Args
	if !containsArg(args, "--session", "sess-1") {
		t.Errorf("reap args missing --session sess-1: %v", args)
	}
}

// EnsureReaper with a still-running Job whose spec matches the desired one must
// be a no-op (no delete, no recreate).
func TestEnsureReaperNoOpWhenRunning(t *testing.T) {
	ctx := context.Background()
	b, core := newReaperBackend()

	name := reaperJobName("sess-2")
	// Seed with the spec EnsureReaper(ReaperOptions{}) resolves to, so the C11
	// spec comparison sees a match; no terminal condition -> running.
	running := buildReaperJob(name, "sess-2", ReaperOptions{
		Image:            DefaultReaperImage,
		IdleTimeout:      15 * time.Minute,
		PollInterval:     30 * time.Second,
		SessionNamespace: "agent-sessions",
	})
	running.Annotations = map[string]string{"marker": "original"}
	if _, err := core.BatchV1().Jobs(ReaperNamespace).Create(ctx, running, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed running job: %v", err)
	}

	var created, deleted bool
	core.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		created = true
		return false, nil, nil
	})
	core.PrependReactor("delete", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		deleted = true
		return false, nil, nil
	})

	if err := b.EnsureReaper(ctx, session.Ref{ID: "sess-2"}, ReaperOptions{}); err != nil {
		t.Fatalf("EnsureReaper: %v", err)
	}
	if created {
		t.Error("expected no create when a reaper is already running")
	}
	if deleted {
		t.Error("expected no delete when a reaper is already running")
	}

	// The original Job must be untouched.
	got, err := core.BatchV1().Jobs(ReaperNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Annotations["marker"] != "original" {
		t.Errorf("running reaper Job was replaced; marker = %q", got.Annotations["marker"])
	}
}

// C11: EnsureReaper with a still-running Job whose flags differ (e.g. a
// reconnect with a different IdleTimeout — a documented override) must replace
// it, not silently leave the stale flags in force.
func TestEnsureReaperReplacesRunningJobOnSpecMismatch(t *testing.T) {
	ctx := context.Background()
	b, core := newReaperBackend()

	name := reaperJobName("sess-4")
	running := buildReaperJob(name, "sess-4", ReaperOptions{
		Image:            DefaultReaperImage,
		IdleTimeout:      15 * time.Minute,
		PollInterval:     30 * time.Second,
		SessionNamespace: "agent-sessions",
	})
	if _, err := core.BatchV1().Jobs(ReaperNamespace).Create(ctx, running, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed running job: %v", err)
	}

	if err := b.EnsureReaper(ctx, session.Ref{ID: "sess-4"}, ReaperOptions{IdleTimeout: time.Hour}); err != nil {
		t.Fatalf("EnsureReaper: %v", err)
	}
	got, err := core.BatchV1().Jobs(ReaperNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if !containsArg(got.Spec.Template.Spec.Containers[0].Args, "--idle-timeout", "1h0m0s") {
		t.Errorf("running reaper with stale flags was not replaced: %v", got.Spec.Template.Spec.Containers[0].Args)
	}
}

// EnsureReaper with a finished Job must delete it and recreate a fresh one.
func TestEnsureReaperReplacesWhenFinished(t *testing.T) {
	ctx := context.Background()
	b, core := newReaperBackend()

	name := reaperJobName("sess-3")
	finished := finishedJob(name, batchv1.JobComplete)
	finished.Annotations = map[string]string{"marker": "stale"}
	if _, err := core.BatchV1().Jobs(ReaperNamespace).Create(ctx, finished, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed finished job: %v", err)
	}

	var deleted, created bool
	core.PrependReactor("delete", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		deleted = true
		return false, nil, nil
	})
	core.PrependReactor("create", "jobs", func(k8stesting.Action) (bool, runtime.Object, error) {
		created = true
		return false, nil, nil
	})

	if err := b.EnsureReaper(ctx, session.Ref{ID: "sess-3"}, ReaperOptions{}); err != nil {
		t.Fatalf("EnsureReaper: %v", err)
	}
	if !deleted {
		t.Error("expected finished reaper Job to be deleted")
	}
	if !created {
		t.Error("expected a fresh reaper Job to be created")
	}

	// The recreated Job must not carry the stale annotation.
	got, err := core.BatchV1().Jobs(ReaperNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get recreated job: %v", err)
	}
	if got.Annotations["marker"] == "stale" {
		t.Error("recreated reaper Job still carries stale annotation; it was not replaced")
	}
}

// The reaper Job must admit into namespaces enforcing the restricted
// PodSecurity Standard: runAsNonRoot + RuntimeDefault seccomp at the pod level,
// no privilege escalation + all capabilities dropped at the container level.
func TestBuildReaperJobSatisfiesRestrictedPodSecurity(t *testing.T) {
	job := buildReaperJob("reap-x", "x", ReaperOptions{Image: DefaultReaperImage})

	pod := job.Spec.Template.Spec
	if pod.SecurityContext == nil {
		t.Fatal("pod SecurityContext is nil")
	}
	if pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Error("pod RunAsNonRoot not set to true")
	}
	if pod.SecurityContext.SeccompProfile == nil || pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("pod SeccompProfile = %v, want RuntimeDefault", pod.SecurityContext.SeccompProfile)
	}

	sc := pod.Containers[0].SecurityContext
	if sc == nil {
		t.Fatal("container SecurityContext is nil")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("container AllowPrivilegeEscalation not set to false")
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("container Capabilities.Drop = %v, want [ALL]", sc.Capabilities)
	}
}

// An explicit ImagePullPolicy override must beat the tagged-ref-implies-Always
// default — required for side-loaded images that can never be pulled.
func TestBuildReaperJobHonorsImagePullPolicyOverride(t *testing.T) {
	job := buildReaperJob("reap-x", "x", ReaperOptions{
		Image:           "ghcr.io/cullenmcdermott/sandbox-reaper:latest",
		ImagePullPolicy: "Never",
	})
	if got := job.Spec.Template.Spec.Containers[0].ImagePullPolicy; got != corev1.PullNever {
		t.Errorf("ImagePullPolicy = %q, want %q", got, corev1.PullNever)
	}
}

func TestJobFinished(t *testing.T) {
	cases := []struct {
		name string
		job  *batchv1.Job
		want bool
	}{
		{"no conditions", &batchv1.Job{}, false},
		{"complete true", finishedJob("j", batchv1.JobComplete), true},
		{"failed true", finishedJob("j", batchv1.JobFailed), true},
		{
			name: "complete false",
			job: &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionFalse},
			}}},
			want: false,
		},
		{
			name: "suspended is not finished",
			job: &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobSuspended, Status: corev1.ConditionTrue},
			}}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jobFinished(tc.job); got != tc.want {
				t.Errorf("jobFinished = %v, want %v", got, tc.want)
			}
		})
	}
}

// containsArg reports whether args contains flag immediately followed by value.
func containsArg(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

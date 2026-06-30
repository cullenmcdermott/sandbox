package k8s

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	batchv1client "k8s.io/client-go/kubernetes/typed/batch/v1"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

const (
	// ReaperNamespace hosts the per-session reaper Jobs. It is deliberately NOT
	// agent-sessions: that namespace's egress NetworkPolicy blocks the k8s API,
	// so a reaper there could not issue the suspend. See docs/session-lifecycle.md.
	ReaperNamespace = "agent-reaper"
	// ReaperServiceAccount is bound (via homelab RBAC) to get/patch sandboxes and
	// get pods/secrets in agent-sessions.
	ReaperServiceAccount = "sandbox-reaper"
	// DefaultReaperImage is the image running the `sandbox reap` subcommand.
	// Public GHCR package built by Depot CI; pulled with imagePullPolicy: Always.
	DefaultReaperImage = "ghcr.io/cullenmcdermott/sandbox-reaper:latest"
)

// ReaperOptions configures a per-session reaper Job.
type ReaperOptions struct {
	// Image is the reaper container image (the sandbox binary).
	Image string
	// SessionNamespace is where the Sandbox/pod/secret live (agent-sessions).
	SessionNamespace string
	// IdleTimeout is how long a session must be idle before suspend.
	IdleTimeout time.Duration
	// PollInterval is how often the reaper polls the runner /idle endpoint.
	PollInterval time.Duration
}

// EnsureReaper creates a reaper Job for the session if one is not already
// watching. The Job polls the runner's /idle endpoint and suspends the Sandbox
// after IdleTimeout of continuous idle, then self-cleans via TTL. Idempotent:
// a still-running reaper is left alone; a finished one is replaced.
func (b *Backend) EnsureReaper(ctx context.Context, ref session.Ref, opts ReaperOptions) error {
	if opts.Image == "" {
		opts.Image = DefaultReaperImage
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 15 * time.Minute
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 30 * time.Second
	}
	if opts.SessionNamespace == "" {
		opts.SessionNamespace = b.namespace
	}

	jobs := b.core.BatchV1().Jobs(ReaperNamespace)
	name := reaperJobName(string(ref.ID))

	existing, err := jobs.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if !jobFinished(existing) {
			return nil // a reaper is already watching this session
		}
		// Replace a finished reaper (e.g. one that suspended a prior activation).
		policy := metav1.DeletePropagationBackground
		if derr := jobs.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &policy}); derr != nil && !k8serrors.IsNotFound(derr) {
			return fmt.Errorf("k8s: delete finished reaper %s: %w", name, derr)
		}
		if werr := waitJobGone(ctx, jobs, name); werr != nil {
			return werr
		}
	} else if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("k8s: get reaper %s: %w", name, err)
	}

	job := buildReaperJob(name, string(ref.ID), opts)
	if _, err := jobs.Create(ctx, job, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("k8s: create reaper %s: %w", name, err)
	}
	return nil
}

func reaperJobName(sid string) string { return "reap-" + sid }

func jobFinished(j *batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func waitJobGone(ctx context.Context, jobs batchv1client.JobInterface, name string) error {
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return wait.PollUntilContextCancel(wctx, 500*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		_, err := jobs.Get(ctx, name, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}

func buildReaperJob(name, sid string, opts ReaperOptions) *batchv1.Job {
	// Keep watching across pod death; only a clean exit (suspend done) finishes
	// the Job. Disruptions (node drain/preemption) must not consume the budget.
	backoff := int32(1 << 20)
	ttl := int32(30)
	disruptionIgnore := &batchv1.PodFailurePolicy{
		Rules: []batchv1.PodFailurePolicyRule{{
			Action: batchv1.PodFailurePolicyActionIgnore,
			OnPodConditions: []batchv1.PodFailurePolicyOnPodConditionsPattern{{
				Type:   corev1.DisruptionTarget,
				Status: corev1.ConditionTrue,
			}},
		}},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ReaperNamespace,
			Labels: map[string]string{
				labelAppName:   "sandbox-reaper",
				labelSessionID: sid,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			PodFailurePolicy:        disruptionIgnore,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelAppName:   "sandbox-reaper",
						labelSessionID: sid,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: ReaperServiceAccount,
					RestartPolicy:      corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:            "reaper",
						Image:           opts.Image,
						ImagePullPolicy: imagePullPolicy(opts.Image),
						Args: []string{
							"reap",
							"--namespace", opts.SessionNamespace,
							"--session", sid,
							"--idle-timeout", opts.IdleTimeout.String(),
							"--poll", opts.PollInterval.String(),
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					}},
				},
			},
		},
	}
}

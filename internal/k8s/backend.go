// Package k8s implements session.Backend against the agent-sandbox Sandbox
// CRD and standard Kubernetes PVCs. One Sandbox + one PVC per session.
package k8s

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/retry"

	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	agentsclient "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

const (
	defaultNamespace = "agent-sessions"
	// defaultStorageClass is empty so PVCs fall back to the cluster's default
	// StorageClass. Override per-session via Spec.StorageClass for clusters that
	// have no default or want a specific class (e.g. "rook-ceph-block").
	defaultStorageClass = ""
	defaultStorageGiB   = 50

	labelSessionID = "sandbox.cullen.dev/session-id"
	labelAppName   = "app.kubernetes.io/name"

	// portRunner is the runner HTTP API port inside the pod.
	portRunner = 8787
	// portSSH is the sshd port for Mutagen.
	portSSH = 22
	// portOpencode is the `opencode serve` HTTP/OpenAPI port inside the pod,
	// used only by opencode-server sessions. The local `opencode attach` client
	// reaches it over a port-forward.
	portOpencode = 4096
	// terminationGraceSeconds is the pod's SIGTERM→SIGKILL window, giving the
	// runner time to emit session.terminating, abort turns, and flush on drain.
	terminationGraceSeconds = 60

	// secretKeyRunnerToken is the key in the per-session Secret holding the
	// bearer token the runner requires for all non-/healthz requests.
	secretKeyRunnerToken = "runner-token"

	// secretKeyOpencodePassword is the key in the per-session Secret holding the
	// HTTP basic-auth password for `opencode serve` (OPENCODE_SERVER_PASSWORD).
	// The local `opencode attach` client reads it via OpencodePassword. Always
	// generated; only opencode-server sessions use it.
	secretKeyOpencodePassword = "opencode-password"

	// opencodeServerUsername is the fixed HTTP basic-auth username for
	// `opencode serve` (opencode's default; passed to the client as -u).
	opencodeServerUsername = "opencode"

	// secretKeySSHAuthorizedKey is the key in the per-session Secret holding
	// the OpenSSH public key authorized for Mutagen's SSH transport.
	secretKeySSHAuthorizedKey = "ssh-authorized-key"

	// sshAuthorizedKeyMountPath is where the per-session Secret's SSH public
	// key is projected into the pod; the entrypoint installs it as the sync
	// user's authorized_keys.
	sshAuthorizedKeyMountPath = "/etc/sandbox-ssh"

	// anthropicSecretName / anthropicSecretKey identify the cluster Secret
	// that supplies Claude credentials to session pods. The value is a Claude
	// Code OAuth token (subscription auth), surfaced to the runner as
	// CLAUDE_CODE_OAUTH_TOKEN. It is provisioned out-of-band (deferred) and
	// referenced optionally so pods still start before it exists.
	anthropicSecretName = "anthropic-credentials"
	anthropicSecretKey  = "api-key"

	// opencodeSecretName is the cluster Secret supplying provider API keys to
	// opencode-server session pods. Keys are optional and referenced optionally
	// so pods start before they exist; the runner's config generator enables
	// only the providers whose env vars are present. These are real API keys
	// (distinct from the Claude subscription OAuth token in anthropicSecret).
	opencodeSecretName = "opencode-credentials"
	// opencodeSecretKeyAnthropic / OpenAI / Zen map cluster Secret keys to the
	// provider env vars opencode reads (ANTHROPIC_API_KEY, OPENAI_API_KEY,
	// OPENCODE_API_KEY for opencode Zen).
	opencodeSecretKeyAnthropic = "anthropic-api-key"
	opencodeSecretKeyOpenAI    = "openai-api-key"
	opencodeSecretKeyZen       = "opencode-api-key"
)

// sessionSecretName returns the name of the per-session Secret for a session.
func sessionSecretName(sessionID string) string { return sessionID + "-runner" }

// Backend manages remote agent sessions via the agent-sandbox Sandbox CRD.
type Backend struct {
	agents    agentsclient.Interface
	core      kubernetes.Interface
	config    *rest.Config
	namespace string

	// runForwardFn is the implementation of port-forward establishment; it is
	// overridable in tests (S4). Production code leaves it as (*Backend).runForward.
	runForwardFn func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, h *forwardHandle, ready chan struct{})

	// forwardOnceFn runs a single ForwardPorts() attempt; overridable in tests so
	// runForward's reconnect/readiness loop can be exercised without a live SPDY
	// endpoint. Production leaves it as (*Backend).forwardOnce.
	forwardOnceFn func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int, ready chan struct{}) error
}

// New creates a Backend. With no options it loads kubeconfig from the standard
// locations (in-cluster first, then ~/.kube/config or KUBECONFIG). Options
// (WithKubeconfig/WithContext/WithRESTConfig) let a programmatic caller target a
// specific cluster without relying on the ambient environment.
func New(namespace string, opts ...Option) (*Backend, error) {
	var nc newConfig
	for _, opt := range opts {
		opt(&nc)
	}
	config, err := nc.resolve()
	if err != nil {
		return nil, err
	}

	agents, err := agentsclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("k8s: create agents clientset: %w", err)
	}
	core, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("k8s: create core clientset: %w", err)
	}

	if namespace == "" {
		namespace = defaultNamespace
	}
	b := &Backend{
		agents:    agents,
		core:      core,
		config:    config,
		namespace: namespace,
	}
	b.runForwardFn = b.runForward
	b.forwardOnceFn = b.forwardOnce
	return b, nil
}

// resolveImagePullPolicy picks the imagePullPolicy for an image ref. An explicit
// override (Always/IfNotPresent/Never) wins; otherwise a digest-pinned ref
// (repo@sha256:...) uses IfNotPresent — the digest is immutable, so re-pulling on
// every pod start is wasted work and breaks offline/rate-limited/private
// registries — and any other ref (a moving tag like :latest) uses Always so it
// always reflects upstream.
func resolveImagePullPolicy(override, imageRef string) corev1.PullPolicy {
	switch corev1.PullPolicy(override) {
	case corev1.PullAlways, corev1.PullIfNotPresent, corev1.PullNever:
		return corev1.PullPolicy(override)
	}
	if strings.Contains(imageRef, "@sha256:") {
		return corev1.PullIfNotPresent
	}
	return corev1.PullAlways
}

// NewForClients creates a Backend from pre-built clientsets (for testing).
func NewForClients(agents agentsclient.Interface, core kubernetes.Interface, namespace string) *Backend {
	if namespace == "" {
		namespace = defaultNamespace
	}
	b := &Backend{agents: agents, core: core, namespace: namespace}
	b.runForwardFn = b.runForward
	b.forwardOnceFn = b.forwardOnce
	return b
}

// CreateSession creates a Sandbox and PVC. It does not wait for the pod to
// be ready; call Start for that.
func (b *Backend) CreateSession(ctx context.Context, spec session.Spec) (session.Ref, error) {
	// Every other backend method — Status, Suspend, Resume, Destroy, List and
	// the watch — is scoped to b.namespace. Creating a session's Secret/PVC/
	// Sandbox in any other namespace would orphan them (Destroy could never find
	// them, leaking the runner bearer-token Secret), so pin to b.namespace.
	// True per-namespace sessions require threading the namespace through all of
	// those methods (future work, tracked for per-user namespace isolation).
	spec.Namespace = b.namespace
	if spec.StorageClass == "" {
		spec.StorageClass = defaultStorageClass
	}
	// storageClassName: a nil pointer makes the PVC use the cluster's default
	// StorageClass, whereas a pointer to "" would explicitly request NO class
	// (which never binds). So only set it when a class is named.
	var storageClassName *string
	if spec.StorageClass != "" {
		storageClassName = strPtr(spec.StorageClass)
	}
	if spec.StorageGiB == 0 {
		spec.StorageGiB = defaultStorageGiB
	}

	name := string(spec.ID)

	// Create the per-session Secret holding the runner bearer token. The token
	// is generated once and reused on subsequent CreateSession calls for the
	// same session (idempotent — AlreadyExists is not an error).
	token, err := generateToken()
	if err != nil {
		return session.Ref{}, fmt.Errorf("k8s: generate runner token: %w", err)
	}
	// Per-session opencode basic-auth password (used only by opencode-server
	// sessions; cheap to always generate so the secret shape is uniform).
	opencodePassword, err := generateToken()
	if err != nil {
		return session.Ref{}, fmt.Errorf("k8s: generate opencode password: %w", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sessionSecretName(name),
			Namespace: spec.Namespace,
			Labels: map[string]string{
				labelSessionID: name,
				labelAppName:   "sandbox-" + spec.Backend,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			secretKeyRunnerToken:      []byte(token),
			secretKeyOpencodePassword: []byte(opencodePassword),
			secretKeySSHAuthorizedKey: []byte(spec.SSHPublicKey),
		},
	}
	if _, err := b.core.CoreV1().Secrets(spec.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return session.Ref{}, fmt.Errorf("k8s: create Secret %s: %w", secret.Name, err)
		}
	}

	// Create the PVC first so it exists when the pod starts.
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				labelSessionID: name,
				labelAppName:   "sandbox-" + spec.Backend,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: storageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", spec.StorageGiB)),
				},
			},
		},
	}
	if _, err := b.core.CoreV1().PersistentVolumeClaims(spec.Namespace).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return session.Ref{}, fmt.Errorf("k8s: create PVC %s: %w", name, err)
		}
	}

	// Create the Sandbox.
	sb := buildSandbox(spec)
	if _, err := b.agents.AgentsV1alpha1().Sandboxes(spec.Namespace).Create(ctx, sb, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return session.Ref{}, fmt.Errorf("k8s: create Sandbox %s: %w", name, err)
		}
	}

	return session.Ref{ID: spec.ID}, nil
}

// Start waits for the session pod to be running and ready.
func (b *Backend) Start(ctx context.Context, ref session.Ref) error {
	return b.waitForPodReady(ctx, ref, nil)
}

// StartWithProgress is Start with a callback invoked as the pod moves through its
// startup phases — "scheduling" → "pulling image" → "starting" — so a caller can
// animate a connect splash instead of blocking on a silent, frozen terminal while
// the node schedules the pod and pulls the runner image (Phase 2). onPhase fires
// only when the phase string changes (never with ""), and may be nil — in which
// case this is exactly Start.
func (b *Backend) StartWithProgress(ctx context.Context, ref session.Ref, onPhase func(detail string)) error {
	return b.waitForPodReady(ctx, ref, onPhase)
}

// Exec runs a command in the session pod's runner container, streaming the
// given stdio. When tty is true a pseudo-terminal is allocated and sizeQueue
// (if non-nil) propagates window resizes.
func (b *Backend) Exec(ctx context.Context, ref session.Ref, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool, sizeQueue remotecommand.TerminalSizeQueue) error {
	sb, err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Get(ctx, string(ref.ID), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("k8s: get Sandbox %s for exec: %w", ref.ID, err)
	}
	pod, err := b.getPodForSandbox(ctx, sb)
	if err != nil || pod == nil {
		return fmt.Errorf("k8s: no pod found for sandbox %s", ref.ID)
	}

	req := b.core.CoreV1().RESTClient().Post().
		Resource("pods").Name(pod.Name).Namespace(pod.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "runner",
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    true,
			Stderr:    !tty, // with a TTY, stderr is multiplexed into stdout
			TTY:       tty,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(b.config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("k8s: build exec executor: %w", err)
	}
	opts := remotecommand.StreamOptions{
		Stdin:             stdin,
		Stdout:            stdout,
		Tty:               tty,
		TerminalSizeQueue: sizeQueue,
	}
	if !tty {
		opts.Stderr = stderr
	}
	return exec.StreamWithContext(ctx, opts)
}

// RunnerToken reads the bearer token for a session from its per-session Secret.
// The CLI passes this to the runner client so authenticated requests succeed.
func (b *Backend) RunnerToken(ctx context.Context, ref session.Ref) (string, error) {
	secret, err := b.core.CoreV1().Secrets(b.namespace).Get(ctx, sessionSecretName(string(ref.ID)), metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("k8s: get runner token secret for %s: %w", ref.ID, err)
	}
	token, ok := secret.Data[secretKeyRunnerToken]
	if !ok {
		return "", fmt.Errorf("k8s: secret %s missing key %q", secret.Name, secretKeyRunnerToken)
	}
	return string(token), nil
}

// OpencodePassword returns the per-session HTTP basic-auth password for
// `opencode serve`, read from the session Secret. Used by the local
// `opencode attach` client for opencode-server sessions.
func (b *Backend) OpencodePassword(ctx context.Context, ref session.Ref) (string, error) {
	secret, err := b.core.CoreV1().Secrets(b.namespace).Get(ctx, sessionSecretName(string(ref.ID)), metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("k8s: get opencode password secret for %s: %w", ref.ID, err)
	}
	pw, ok := secret.Data[secretKeyOpencodePassword]
	if !ok {
		return "", fmt.Errorf("k8s: secret %s missing key %q", secret.Name, secretKeyOpencodePassword)
	}
	return string(pw), nil
}

// OpencodeUsername returns the fixed HTTP basic-auth username for `opencode
// serve`. Exposed so the CLI can pass -u to `opencode attach` without
// duplicating the constant.
func OpencodeUsername() string { return opencodeServerUsername }

// Suspend sets replicas to 0, terminating the pod but preserving the PVC.
func (b *Backend) Suspend(ctx context.Context, ref session.Ref) error {
	return b.setReplicas(ctx, ref, 0)
}

// Resume sets replicas back to 1 and waits for the pod to be ready.
func (b *Backend) Resume(ctx context.Context, ref session.Ref) error {
	if err := b.setReplicas(ctx, ref, 1); err != nil {
		return err
	}
	return b.waitForPodReady(ctx, ref, nil)
}

// Destroy deletes the Sandbox, PVC and per-session Secret. Irreversible.
//
// All three deletions are attempted even if one fails: a transient error on the
// Sandbox or PVC must not orphan the remaining resources — most importantly the
// per-session Secret, which holds the runner bearer token. NotFound is treated
// as success so Destroy is idempotent (C5).
func (b *Backend) Destroy(ctx context.Context, ref session.Ref) error {
	name := string(ref.ID)
	var errs []error
	if err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete Sandbox %s: %w", name, err))
	}
	if err := b.core.CoreV1().PersistentVolumeClaims(b.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete PVC %s: %w", name, err))
	}
	if err := b.core.CoreV1().Secrets(b.namespace).Delete(ctx, sessionSecretName(name), metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete Secret %s: %w", sessionSecretName(name), err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("k8s: destroy %s: %w", name, errors.Join(errs...))
	}
	return nil
}

// Status returns the current observed state.
func (b *Backend) Status(ctx context.Context, ref session.Ref) (session.State, error) {
	sb, err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Get(ctx, string(ref.ID), metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return session.State{ID: ref.ID, Status: session.StatusGone}, nil
		}
		return session.State{}, fmt.Errorf("k8s: get Sandbox %s: %w", ref.ID, err)
	}
	return b.statusFromSandbox(ctx, sb), nil
}

// statusFromSandbox derives the observed State from a Sandbox object already in
// hand. The only extra I/O is the pod-readiness probe, which degrades to
// Creating on error rather than failing. Splitting this out of Status lets List
// build every session's state straight from the bulk-list objects — with no
// per-item Get that could fail (API pressure, or the caller's list deadline
// truncating the sequence) and silently drop a live Sandbox from the snapshot.
// The dashboard reconcile treats absence-from-the-snapshot as deletion, so a
// dropped live session would be wrongly pruned.
func (b *Backend) statusFromSandbox(ctx context.Context, sb *agentv1alpha1.Sandbox) session.State {
	st := session.State{
		ID:          session.ID(sb.Name),
		SandboxName: sb.Name,
		CreatedAt:   sb.CreationTimestamp.Time,
		// Recover the identity fields the runner pod was created with. These are
		// write-once container env (see buildEnv); reading them back here means a
		// session attached from the list — not just one freshly created in this
		// process — carries its real ProjectPath (needed for Mutagen sync) and
		// Backend (needed to pick the claude vs opencode connect path).
		ProjectPath: sandboxEnv(sb, "PROJECT_PATH"),
		Backend:     sandboxEnv(sb, "SANDBOX_BACKEND"),
	}

	replicas := int32(1)
	if sb.Spec.Replicas != nil {
		replicas = *sb.Spec.Replicas
	}
	if replicas == 0 {
		st.Status = session.StatusSuspended
	} else {
		// Check pod readiness.
		pod, err := b.getPodForSandbox(ctx, sb)
		if err == nil && pod != nil {
			st.PodName = pod.Name
			st.PodReady = isPodReady(pod)
			if st.PodReady {
				st.Status = session.StatusRunning
			} else {
				st.Status = session.StatusCreating
			}
		} else {
			st.Status = session.StatusCreating
		}
	}

	// Detect failure from the Finished condition (pod exited).
	for _, cond := range sb.Status.Conditions {
		if cond.Type == string(agentv1alpha1.SandboxConditionFinished) {
			if cond.Reason == agentv1alpha1.SandboxReasonPodFailed {
				st.Status = session.StatusFailed
			}
		}
	}

	return st
}

// sandboxEnv returns the literal value of a runner-container env var from the
// Sandbox's pod template, or "" if absent or set via valueFrom (SecretKeyRef).
// Used to recover the identity fields (PROJECT_PATH, SANDBOX_BACKEND) the pod
// was created with so Status/List report them for any session, not only those
// created in the current process.
func sandboxEnv(sb *agentv1alpha1.Sandbox, name string) string {
	for i := range sb.Spec.PodTemplate.Spec.Containers {
		for _, e := range sb.Spec.PodTemplate.Spec.Containers[i].Env {
			if e.Name == name {
				return e.Value
			}
		}
	}
	return ""
}

// List returns all sessions in the namespace.
func (b *Backend) List(ctx context.Context) ([]session.State, error) {
	list, err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("k8s: list Sandboxes: %w", err)
	}

	states := make([]session.State, 0, len(list.Items))
	for i := range list.Items {
		// Build each state straight from the bulk-list object — never a per-item
		// Get that could fail and silently drop a live Sandbox from the snapshot
		// (the dashboard reconcile treats absence as deletion and would prune it).
		states = append(states, b.statusFromSandbox(ctx, &list.Items[i]))
	}
	return states, nil
}

// setReplicas updates the Sandbox replicas field. Get+Update is wrapped in
// RetryOnConflict: the idle reaper writes the same Sandbox (it suspends by
// setting replicas to 0), so a Suspend/Resume issued concurrently can lose the
// resourceVersion race and get a 409 Conflict. RetryOnConflict re-Gets the
// latest object and re-applies replicas, so the operation converges instead of
// failing the user-visible Suspend/Resume.
func (b *Backend) setReplicas(ctx context.Context, ref session.Ref, replicas int32) error {
	sandboxes := b.agents.AgentsV1alpha1().Sandboxes(b.namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		sb, getErr := sandboxes.Get(ctx, string(ref.ID), metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		sb.Spec.Replicas = &replicas
		_, updateErr := sandboxes.Update(ctx, sb, metav1.UpdateOptions{})
		return updateErr
	})
	if err != nil {
		return fmt.Errorf("k8s: update Sandbox %s replicas to %d: %w", ref.ID, replicas, err)
	}
	return nil
}

// waitForPodReady polls until the sandbox's pod is running and ready, or
// the context is cancelled. It fails fast with a descriptive error if the pod
// enters a state it cannot recover from on its own (unpullable image, bad
// config, crash loop) so a broken runner image surfaces a clear message instead
// of polling silently until the caller's deadline / forever (RV: `sandbox
// claude` could hang indefinitely with zero feedback on an ImagePullBackOff pod).
func (b *Backend) waitForPodReady(ctx context.Context, ref session.Ref, onPhase func(detail string)) error {
	var lastDetail string
	report := func(pod *corev1.Pod) {
		if onPhase == nil {
			return
		}
		// Only fire on a phase change so the connect splash channel isn't spammed
		// every poll; "" (pod ready) reports nothing — the caller advances stages.
		if d := podPhaseDetail(pod); d != "" && d != lastDetail {
			lastDetail = d
			onPhase(d)
		}
	}
	return wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		sb, err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Get(ctx, string(ref.ID), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		pod, err := b.getPodForSandbox(ctx, sb)
		if err != nil || pod == nil {
			report(nil) // no pod yet → "scheduling"
			return false, nil
		}
		// A pod being deleted is NOT a valid ready target. On resume (replicas 0→1)
		// the OLD pod terminates while still briefly Ready; it is momentarily the
		// only pod, so getPodForSandbox falls back to it (R7). Without this guard
		// waitForPodReady returns against that dying pod — resume reports "ready"
		// ~10-15s before the genuinely-new pod is up, and a turn started in that
		// window lands on the terminating/booting pod and is orphaned. Keep polling
		// until a non-terminating pod is Ready.
		if pod.DeletionTimestamp != nil {
			return false, nil
		}
		if startErr := podStartupError(pod); startErr != nil {
			return false, startErr
		}
		report(pod)
		return isPodReady(pod), nil
	})
}

// podPhaseDetail returns a short, user-facing description of where a pod is in
// its startup lifecycle, for the connect splash: "scheduling" (no pod yet, or
// scheduled to no node), "pulling image" (scheduled, kubelet creating
// containers — the image pull dominates a cold start), or "starting" (containers
// up, readiness not yet passing). It returns "" once the pod is Ready, since the
// caller then advances past the start stage.
func podPhaseDetail(pod *corev1.Pod) string {
	if pod == nil {
		return "scheduling"
	}
	if isPodReady(pod) {
		return ""
	}
	switch pod.Status.Phase {
	case corev1.PodPending:
		if !podScheduled(pod) {
			return "scheduling"
		}
		// Scheduled but not Running: kubelet is creating containers — pulling the
		// image and mounting volumes. The pull is the slow part of a cold start.
		return "pulling image"
	default:
		// Running (or any other non-ready phase): containers exist, the runner is
		// still coming up to readiness.
		return "starting"
	}
}

// podScheduled reports whether the scheduler has bound the pod to a node — the
// boundary between "scheduling" and "pulling image" in podPhaseDetail. It prefers
// the PodScheduled condition and falls back to a set NodeName when the condition
// has not been written yet on a very fresh pod.
func podScheduled(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled {
			return c.Status == corev1.ConditionTrue
		}
	}
	return pod.Spec.NodeName != ""
}

// fatalWaitingReasons are kubelet container "waiting" reasons that mean the pod
// will not become ready without intervention. Each is a state kubelet reports
// only after it has determined a real problem: the *BackOff reasons mean it has
// already retried and is backing off, and the config/name reasons are
// deterministic — so failing fast on them does not race a slow-but-healthy pull.
var fatalWaitingReasons = map[string]bool{
	"ImagePullBackOff":           true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
	"CrashLoopBackOff":           true,
}

// podStartupError returns a descriptive error if the pod is in a state it cannot
// recover from on its own (so the readiness wait should stop and surface it), or
// nil while the pod is still legitimately starting up.
func podStartupError(pod *corev1.Pod) error {
	if pod.Status.Phase == corev1.PodFailed {
		msg := strings.TrimSpace(pod.Status.Reason + " " + pod.Status.Message)
		if msg == "" {
			msg = "pod entered Failed phase"
		}
		return fmt.Errorf("pod %s failed to start: %s", pod.Name, msg)
	}
	statuses := make([]corev1.ContainerStatus, 0, len(pod.Status.InitContainerStatuses)+len(pod.Status.ContainerStatuses))
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)
	for _, cs := range statuses {
		w := cs.State.Waiting
		if w == nil || !fatalWaitingReasons[w.Reason] {
			continue
		}
		if detail := strings.TrimSpace(w.Message); detail != "" {
			return fmt.Errorf("container %q is not starting: %s: %s", cs.Name, w.Reason, detail)
		}
		return fmt.Errorf("container %q is not starting: %s", cs.Name, w.Reason)
	}
	return nil
}

// getPodForSandbox finds the pod owned by the sandbox via label selector.
// It prefers non-terminating pods: if multiple pods exist (e.g. a terminating
// old pod + a new running one), the first non-terminating pod is returned.
// Only if all pods are terminating does it fall back to the first entry (R7).
func (b *Backend) getPodForSandbox(ctx context.Context, sb *agentv1alpha1.Sandbox) (*corev1.Pod, error) {
	selector := fmt.Sprintf("%s=%s", labelSessionID, sb.Name)
	pods, err := b.core.CoreV1().Pods(sb.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, nil
	}
	// Return the first non-terminating pod; fall back to pods.Items[0] only
	// when every pod has a DeletionTimestamp set (R7).
	for i := range pods.Items {
		if pods.Items[i].DeletionTimestamp == nil {
			return &pods.Items[i], nil
		}
	}
	return &pods.Items[0], nil
}

// PodIP returns the IP of the sandbox's running pod, for direct HTTP access
// without a port-forward (e.g. an out-of-namespace reaper polling the runner).
func (b *Backend) PodIP(ctx context.Context, ref session.Ref) (string, error) {
	sb, err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Get(ctx, string(ref.ID), metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("k8s: get Sandbox %s: %w", ref.ID, err)
	}
	pod, err := b.getPodForSandbox(ctx, sb)
	if err != nil || pod == nil {
		return "", fmt.Errorf("k8s: no pod for sandbox %s", ref.ID)
	}
	if pod.Status.PodIP == "" {
		return "", fmt.Errorf("k8s: pod %s has no IP yet", pod.Name)
	}
	return pod.Status.PodIP, nil
}

// isPodReady returns true if all containers are ready.
func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// buildSandbox constructs a Sandbox CR from a session.Spec.
func buildSandbox(spec session.Spec) *agentv1alpha1.Sandbox {
	name := string(spec.ID)
	one := int32(1)

	return &agentv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				labelSessionID: name,
				labelAppName:   "sandbox-" + spec.Backend,
			},
		},
		Spec: agentv1alpha1.SandboxSpec{
			Replicas: &one,
			Service:  boolPtr(false),
			// NOTE: no VolumeClaimTemplates. The per-session PVC is created
			// standalone (name == session id) in CreateSession and mounted via
			// the explicit "session" Volume below (ClaimName: name); it is also
			// what Destroy deletes. A VolumeClaimTemplate here would make the
			// controller auto-provision a SECOND, never-mounted PVC — 2× storage
			// per session (S1).
			PodTemplate: agentv1alpha1.PodTemplate{
				ObjectMeta: agentv1alpha1.PodMetadata{
					Labels: map[string]string{
						labelSessionID: name,
						labelAppName:   "sandbox-" + spec.Backend,
					},
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: boolPtr(false),
					RestartPolicy:                corev1.RestartPolicyNever,
					// Give the runner room on SIGTERM (node drain, suspend) to
					// warn clients via session.terminating, abort in-flight
					// turns, and flush the event log before SIGKILL.
					TerminationGracePeriodSeconds: int64Ptr(terminationGraceSeconds),
					// RuntimeDefault seccomp is safe alongside sshd. Moving to
					// runAsNonRoot + dropped capabilities needs live validation
					// because sshd privilege separation depends on it.
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "runner",
							Image:           spec.RunnerImage,
							ImagePullPolicy: resolveImagePullPolicy(spec.ImagePullPolicy, spec.RunnerImage),
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: portRunner},
								{Name: "ssh", ContainerPort: portSSH},
								{Name: "opencode", ContainerPort: portOpencode},
							},
							Env:          buildEnv(spec, name),
							VolumeMounts: runnerVolumeMounts(spec),
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
							// C9: detect a crashed/hung runner. Without probes the
							// pod is Ready the instant the container starts — before
							// the runner's HTTP server is listening — and a wedged
							// runner is never restarted. Both probes hit the
							// unauthenticated GET /healthz (server.ts) on the runner
							// port. Readiness gates traffic (and is what the reaper
							// can key suspension on); liveness restarts a hung runner.
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromString("http"),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
								FailureThreshold:    3,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromString("http"),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       30,
								TimeoutSeconds:      5,
								FailureThreshold:    3,
							},
							// BR1: shrink the container's root blast radius without
							// breaking sshd. allowPrivilegeEscalation=false sets
							// no_new_privs (the agent's tools can't gain privileges
							// via setuid binaries). Capabilities drop ALL and add
							// back only the default set sshd's privilege separation
							// and the agent need — this removes NET_RAW (raw-socket
							// sniffing/spoofing on the pod network) and MKNOD (device
							// nodes), which neither uses. The larger win
							// (runAsNonRoot + fsGroup) is deferred: it needs live
							// cluster validation because sshd privsep depends on it
							// (see architecture.md / runner/Dockerfile).
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
									Add: []corev1.Capability{
										"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID",
										"SETGID", "SETUID", "SETPCAP", "SETFCAP",
										"NET_BIND_SERVICE", "SYS_CHROOT", "KILL", "AUDIT_WRITE",
									},
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "session",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: name,
								},
							},
						},
						{
							// Projects the per-session Secret's SSH public key as
							// a file the entrypoint installs as authorized_keys.
							Name: "ssh-key",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: sessionSecretName(name),
									Items: []corev1.KeyToPath{
										{Key: secretKeySSHAuthorizedKey, Path: "authorized_key"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// runnerVolumeMounts returns the runner container's volume mounts. The session
// PVC is always mounted at /session (holding workspace/ + state/). When the
// session has an absolute project path, the workspace subtree is ALSO bind-
// mounted (via subPath) at that real host path — e.g. /Users/cullen/git/homelab
// — so the Claude SDK runs with cwd equal to the host project path rather than
// /session/workspace/<path>.
//
// This is what makes a k8s-started session resumable on the laptop: the SDK keys
// its on-disk transcript directory by cwd (~/.claude/projects/<cwd with '/'→'-'>),
// so a matching cwd means the synced transcript lands in the same project folder
// `claude --resume` reads locally. Both mounts reference the same PVC, so the two
// views (/session/workspace/<path> and <path>) are the same underlying files.
// See TODO.md "Resumable transcripts (Option B)".
func runnerVolumeMounts(spec session.Spec) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{
		{Name: "session", MountPath: "/session"},
		{Name: "ssh-key", MountPath: sshAuthorizedKeyMountPath, ReadOnly: true},
	}
	if strings.HasPrefix(spec.ProjectPath, "/") && spec.ProjectPath != "/" {
		// subPath must be relative to the PVC root; ProjectPath starts with "/",
		// so "workspace"+ProjectPath yields e.g. "workspace/Users/cullen/git/x".
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "session",
			MountPath: spec.ProjectPath,
			SubPath:   "workspace" + spec.ProjectPath,
		})
	}
	return mounts
}

// buildEnv builds the runner container's env, branching on the backend. Common
// vars are set for all sessions; backend-specific credentials/config are added
// only for the matching backend. In particular ANTHROPIC_API_KEY is set ONLY
// for opencode (Claude Code would reject the subscription OAuth token if a real
// x-api-key were also present — see CLAUDE_CODE_OAUTH_TOKEN below).
func buildEnv(spec session.Spec, name string) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "CLAUDE_CONFIG_DIR", Value: "/session/state/claude"},
		{Name: "CLAUDE_CODE_DISABLE_AUTO_MEMORY", Value: "1"},
		// The runner pod runs as root (no USER directive yet — see Dockerfile).
		// The bundled `claude` binary refuses --dangerously-skip-permissions
		// (bypassPermissions / yolo mode, and the always-bypass auto-title
		// summarizer) when running as uid 0 unless IS_SANDBOX=1. The pod genuinely
		// IS a network-isolated sandbox (default-deny ingress + egress allowlist),
		// so this is semantically honest and unblocks the root guard.
		{Name: "IS_SANDBOX", Value: "1"},
		{Name: "SANDBOX_SESSION_ID", Value: name},
		{Name: "SANDBOX_BACKEND", Value: spec.Backend},
		{Name: "PROJECT_PATH", Value: spec.ProjectPath},
		// Lets the runner report an accurate countdown in session.terminating;
		// mirrors the pod grace period.
		{Name: "TERMINATION_GRACE_SECONDS", Value: fmt.Sprintf("%d", terminationGraceSeconds)},
		{
			Name: "RUNNER_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: sessionSecretName(name)},
					Key:                  secretKeyRunnerToken,
				},
			},
		},
	}

	// SANDBOX_MODEL is the optional session-default model (from `claude --model`);
	// the runner passes it to the SDK as Options.model. Empty => account default.
	if spec.Model != "" {
		env = append(env, corev1.EnvVar{Name: "SANDBOX_MODEL", Value: spec.Model})
	}

	if spec.Backend == session.BackendOpenCode {
		return append(env, opencodeEnv(name)...)
	}

	// Default (claude-sdk): Claude Code OAuth token (subscription auth) from a
	// cluster-provisioned Secret. Optional so pods still start before it is
	// created. Note: do NOT also set ANTHROPIC_API_KEY — Claude Code prefers it
	// and would reject the OAuth token as an invalid x-api-key.
	return append(env, corev1.EnvVar{
		Name: "CLAUDE_CODE_OAUTH_TOKEN",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: anthropicSecretName},
				Key:                  anthropicSecretKey,
				Optional:             boolPtr(true),
			},
		},
	})
}

// opencodeEnv returns the env vars specific to opencode-server sessions: the
// serve basic-auth credentials, the data dir + config path on the PVC, and the
// provider API keys (all optional so a pod starts before the cluster Secret
// exists; the runner enables only providers whose keys are present).
func opencodeEnv(name string) []corev1.EnvVar {
	providerKey := func(envName, secretKey string) corev1.EnvVar {
		return corev1.EnvVar{
			Name: envName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: opencodeSecretName},
					Key:                  secretKey,
					Optional:             boolPtr(true),
				},
			},
		}
	}
	return []corev1.EnvVar{
		{Name: "OPENCODE_PORT", Value: fmt.Sprintf("%d", portOpencode)},
		{Name: "OPENCODE_SERVER_USERNAME", Value: opencodeServerUsername},
		{
			Name: "OPENCODE_SERVER_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: sessionSecretName(name)},
					Key:                  secretKeyOpencodePassword,
				},
			},
		},
		// opencode session history + generated config live on the PVC so they
		// survive suspend/resume (mirrors claude state at /session/state/claude).
		{Name: "XDG_DATA_HOME", Value: "/session/state/opencode/data"},
		{Name: "OPENCODE_CONFIG", Value: "/session/state/opencode/opencode.json"},
		providerKey("ANTHROPIC_API_KEY", opencodeSecretKeyAnthropic),
		providerKey("OPENAI_API_KEY", opencodeSecretKeyOpenAI),
		providerKey("OPENCODE_API_KEY", opencodeSecretKeyZen),
	}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func int64Ptr(i int64) *int64 { return &i }

// generateToken returns a random 256-bit hex token for runner bearer auth.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

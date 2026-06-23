// Package k8s implements session.Backend against the agent-sandbox Sandbox
// CRD and standard Kubernetes PVCs. One Sandbox + one PVC per session.
package k8s

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	agentv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	agentsclient "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

const (
	defaultNamespace    = "agent-sessions"
	defaultStorageClass = "rook-ceph-block"
	defaultStorageGiB   = 50

	labelSessionID = "sandbox.cullen.dev/session-id"
	labelAppName   = "app.kubernetes.io/name"

	// portRunner is the runner HTTP API port inside the pod.
	portRunner = 8787
	// portSSH is the sshd port for Mutagen.
	portSSH = 22

	// secretKeyRunnerToken is the key in the per-session Secret holding the
	// bearer token the runner requires for all non-/healthz requests.
	secretKeyRunnerToken = "runner-token"

	// secretKeySSHAuthorizedKey is the key in the per-session Secret holding
	// the OpenSSH public key authorized for Mutagen's SSH transport.
	secretKeySSHAuthorizedKey = "ssh-authorized-key"

	// sshAuthorizedKeyMountPath is where the per-session Secret's SSH public
	// key is projected into the pod; the entrypoint installs it as the sync
	// user's authorized_keys.
	sshAuthorizedKeyMountPath = "/etc/sandbox-ssh"

	// anthropicSecretName / anthropicSecretKey identify the cluster Secret
	// that supplies the Anthropic API key to session pods. It is provisioned
	// out-of-band (deferred) and referenced optionally so pods still start
	// before it exists.
	anthropicSecretName = "anthropic-credentials"
	anthropicSecretKey  = "api-key"
)

// sessionSecretName returns the name of the per-session Secret for a session.
func sessionSecretName(sessionID string) string { return sessionID + "-runner" }

// Backend manages remote agent sessions via the agent-sandbox Sandbox CRD.
type Backend struct {
	agents    agentsclient.Interface
	core      kubernetes.Interface
	config    *rest.Config
	namespace string
}

// New creates a Backend. It loads kubeconfig from the standard locations
// (in-cluster first, then ~/.kube/config or KUBECONFIG).
func New(namespace string) (*Backend, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules, &clientcmd.ConfigOverrides{},
		).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("k8s: load kubeconfig: %w", err)
		}
		config = cfg
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
	return &Backend{
		agents:    agents,
		core:      core,
		config:    config,
		namespace: namespace,
	}, nil
}

// NewForConfig creates a Backend from an explicit rest.Config (for testing).
func NewForConfig(config *rest.Config, namespace string) (*Backend, error) {
	agents, err := agentsclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	core, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		namespace = defaultNamespace
	}
	return &Backend{agents: agents, core: core, config: config, namespace: namespace}, nil
}

// NewForClients creates a Backend from pre-built clientsets (for testing).
func NewForClients(agents agentsclient.Interface, core kubernetes.Interface, namespace string) *Backend {
	if namespace == "" {
		namespace = defaultNamespace
	}
	return &Backend{agents: agents, core: core, namespace: namespace}
}

// CreateSession creates a Sandbox and PVC. It does not wait for the pod to
// be ready; call Start for that.
func (b *Backend) CreateSession(ctx context.Context, spec session.Spec) (session.Ref, error) {
	if spec.Namespace == "" {
		spec.Namespace = b.namespace
	}
	if spec.StorageClass == "" {
		spec.StorageClass = defaultStorageClass
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
			StorageClassName: strPtr(spec.StorageClass),
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
	return b.waitForPodReady(ctx, ref)
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

// Suspend sets replicas to 0, terminating the pod but preserving the PVC.
func (b *Backend) Suspend(ctx context.Context, ref session.Ref) error {
	return b.setReplicas(ctx, ref, 0)
}

// Resume sets replicas back to 1 and waits for the pod to be ready.
func (b *Backend) Resume(ctx context.Context, ref session.Ref) error {
	if err := b.setReplicas(ctx, ref, 1); err != nil {
		return err
	}
	return b.waitForPodReady(ctx, ref)
}

// Destroy deletes the Sandbox and PVC. Irreversible.
func (b *Backend) Destroy(ctx context.Context, ref session.Ref) error {
	name := string(ref.ID)
	err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("k8s: delete Sandbox %s: %w", name, err)
	}
	err = b.core.CoreV1().PersistentVolumeClaims(b.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("k8s: delete PVC %s: %w", name, err)
	}
	err = b.core.CoreV1().Secrets(b.namespace).Delete(ctx, sessionSecretName(name), metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("k8s: delete Secret %s: %w", sessionSecretName(name), err)
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

	st := session.State{
		ID:          ref.ID,
		SandboxName: sb.Name,
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

	return st, nil
}

// List returns all sessions in the namespace.
func (b *Backend) List(ctx context.Context) ([]session.State, error) {
	list, err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("k8s: list Sandboxes: %w", err)
	}

	states := make([]session.State, 0, len(list.Items))
	for i := range list.Items {
		sb := &list.Items[i]
		st, err := b.Status(ctx, session.Ref{ID: session.ID(sb.Name)})
		if err != nil {
			continue
		}
		states = append(states, st)
	}
	return states, nil
}

// setReplicas patches the Sandbox replicas field.
func (b *Backend) setReplicas(ctx context.Context, ref session.Ref, replicas int32) error {
	sb, err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Get(ctx, string(ref.ID), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("k8s: get Sandbox %s for replicas update: %w", ref.ID, err)
	}
	sb.Spec.Replicas = &replicas
	_, err = b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Update(ctx, sb, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("k8s: update Sandbox %s replicas to %d: %w", ref.ID, replicas, err)
	}
	return nil
}

// waitForPodReady polls until the sandbox's pod is running and ready, or
// the context is cancelled.
func (b *Backend) waitForPodReady(ctx context.Context, ref session.Ref) error {
	return wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		sb, err := b.agents.AgentsV1alpha1().Sandboxes(b.namespace).Get(ctx, string(ref.ID), metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		pod, err := b.getPodForSandbox(ctx, sb)
		if err != nil || pod == nil {
			return false, nil
		}
		return isPodReady(pod), nil
	})
}

// getPodForSandbox finds the pod owned by the sandbox via label selector.
func (b *Backend) getPodForSandbox(ctx context.Context, sb *agentv1alpha1.Sandbox) (*corev1.Pod, error) {
	selector := fmt.Sprintf("%s=%s", labelSessionID, sb.Name)
	pods, err := b.core.CoreV1().Pods(sb.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, nil
	}
	return &pods.Items[0], nil
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
			VolumeClaimTemplates: []agentv1alpha1.PersistentVolumeClaimTemplate{
				{
					EmbeddedObjectMetadata: agentv1alpha1.EmbeddedObjectMetadata{
						Name: "session",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						StorageClassName: strPtr(spec.StorageClass),
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", spec.StorageGiB)),
							},
						},
					},
				},
			},
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
							Name:  "runner",
							Image: spec.RunnerImage,
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: portRunner},
								{Name: "ssh", ContainerPort: portSSH},
							},
							Env: []corev1.EnvVar{
								{Name: "CLAUDE_CONFIG_DIR", Value: "/session/state/claude"},
								{Name: "CLAUDE_CODE_DISABLE_AUTO_MEMORY", Value: "1"},
								{Name: "SANDBOX_SESSION_ID", Value: name},
								{Name: "SANDBOX_BACKEND", Value: spec.Backend},
								{Name: "PROJECT_PATH", Value: spec.ProjectPath},
								{
									Name: "RUNNER_TOKEN",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: sessionSecretName(name)},
											Key:                  secretKeyRunnerToken,
										},
									},
								},
								{
									// Anthropic API key from a cluster-provisioned Secret.
									// Optional so pods still start before it is created.
									Name: "ANTHROPIC_API_KEY",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: anthropicSecretName},
											Key:                  anthropicSecretKey,
											Optional:             boolPtr(true),
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "session", MountPath: "/session"},
								{Name: "ssh-key", MountPath: sshAuthorizedKeyMountPath, ReadOnly: true},
							},
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

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool     { return &b }

// generateToken returns a random 256-bit hex token for runner bearer auth.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

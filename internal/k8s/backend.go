// Package k8s implements session.Backend against the agent-sandbox Sandbox
// CRD and standard Kubernetes PVCs. One Sandbox + one PVC per session.
package k8s

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

	// labelAnthropicAccount records the stored Anthropic account id whose
	// credential a per-session Secret holds a copy of. `auth logout`/rotation
	// list the sessions to re-provision with one label-selector query. The value
	// is spec.AnthropicAccountID (DNS-safe, guaranteed by the cred store).
	//
	// NOTE: the plan text spells this `sandbox.dev/anthropic-account`; we use the
	// `sandbox.cullen.dev/` domain to match this file's existing label/annotation
	// convention (labelSessionID, annotationPinnedRunnerImage) rather than
	// introducing a second prefix.
	labelAnthropicAccount = "sandbox.cullen.dev/anthropic-account"

	// labelCodexAccount records the stored ChatGPT account id whose auth.json a
	// per-session Secret holds a copy of, mirroring labelAnthropicAccount for the
	// codex-app-server backend. `auth logout`/rotation list the sessions to
	// re-provision with one label-selector query. The value is spec.CodexAccountID
	// (DNS-safe, guaranteed by the cred store).
	labelCodexAccount = "sandbox.cullen.dev/codex-account"

	// annotationPinnedRunnerImage records the digest-pinned ref of the runner
	// image the session's pod actually ran (kubelet-resolved), stamped once the
	// pod is Ready. Resume rewrites the pod template's image from it before
	// scaling 0→1, so a suspended session always resumes on the same runner
	// binary it was suspended with — a moving tag (:latest) + PullAlways would
	// otherwise swap the runner under the session's persisted events.db /
	// session.json state (2026-07-01 review HIGH).
	annotationPinnedRunnerImage = "sandbox.cullen.dev/pinned-runner-image"

	// annotationOpencodeCredsHash records a short, non-reversible fingerprint
	// (first 8 hex of sha256) of the SELECTED provider key in the shared
	// opencode-credentials Secret AT THE TIME the session's pod was started, and
	// annotationOpencodeProvider records which provider that was (so a later
	// reconcile can recompute the live hash without re-deriving it from the pod
	// env). Env SecretKeyRefs are resolved once at pod start, so rotating the
	// cluster Secret does NOT reach a running pod — the create/resume reconcile
	// paths compare the live Secret's hash against this stamp and warn when they
	// drift, pointing the operator at a suspend/resume (pod restart) to adopt the
	// new key. Only stamped for opencode-server sessions.
	annotationOpencodeCredsHash = "sandbox.cullen.dev/opencode-creds-hash"
	annotationOpencodeProvider  = "sandbox.cullen.dev/opencode-provider"

	// runnerContainerName is the runner container in the session pod spec.
	runnerContainerName = "runner"

	// portRunner is the runner HTTP API port inside the pod.
	portRunner = 8787
	// portSSH is the sshd port for Mutagen.
	portSSH = 22
	// portOpencode is the `opencode serve` HTTP/OpenAPI port inside the pod,
	// used only by opencode-server sessions. The local `opencode attach` client
	// reaches it over a port-forward.
	portOpencode = 4096
	// portCodex is the `codex app-server` websocket port inside the pod, used
	// only by codex-app-server sessions. It is pod-loopback + port-forward only —
	// the app-server listens on ws://127.0.0.1:8788 and is deliberately NOT bound
	// on the pod network; the local codex client reaches it over a port-forward.
	portCodex = 8788
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

	// secretKeyAnthropicCredential is the key in the per-session Secret holding
	// the resolved Anthropic credential (OAuth token or Console API key) for an
	// account-backed claude-sdk session. Written only when spec.AnthropicCredential
	// is non-empty; the claude buildEnv branch references it (not Optional — we
	// wrote it) via CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY per
	// spec.AnthropicAuth. Absent for the shared-Secret fallback path.
	secretKeyAnthropicCredential = "anthropic-credential"

	// secretKeyCodexAuthJSON is the key in the per-session Secret holding the
	// resolved ChatGPT-OAuth auth.json document for an account-backed
	// codex-app-server session (mirrors secretKeyAnthropicCredential). Written only
	// when spec.CodexAuthJSON is non-empty; the codex buildEnv branch references it
	// (not Optional — we wrote it) via CODEX_AUTH_JSON. Absent for the shared
	// OPENAI_API_KEY fallback path.
	secretKeyCodexAuthJSON = "codex-auth-json"

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
	// anthropicAPISecretKey is the key in the same anthropic-credentials Secret
	// holding a platform.anthropic.com Console API key, surfaced to the runner as
	// ANTHROPIC_API_KEY when spec.AnthropicAuth == "api-key". Selected per session;
	// never populated alongside CLAUDE_CODE_OAUTH_TOKEN (see buildEnv).
	anthropicAPISecretKey = "console-api-key"

	// opencodeSecretName is the cluster Secret supplying provider API keys to
	// opencode-server session pods. A session references exactly ONE key from it
	// (the provider selected by spec.OpencodeProvider), fail-closed (NOT Optional):
	// a pod whose selected key is absent stalls in CreateContainerConfigError
	// rather than starting uncredentialed. These are real API keys (distinct from
	// the Claude subscription OAuth token in anthropicSecret).
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

// createRollbackTimeout bounds the best-effort cleanup CreateSession performs
// when a later create step fails partway through. It runs on a context
// independent of the caller's (context.WithoutCancel) because the caller's
// ctx may already be cancelled/expired by the time the failure is observed —
// the cleanup must still get a chance to run.
const createRollbackTimeout = 30 * time.Second

// CreateSession creates a Secret, PVC and Sandbox for the session. It does not
// wait for the pod to be ready; call Start for that.
//
// If a later step fails, everything created earlier in this call is rolled
// back (best effort) before returning the original error, so a partial
// failure never leaves an orphaned Secret (holding a live runner bearer
// token) or PVC that List/Status/Destroy — which only ever look at
// Sandboxes — can't see. Rollback failures are appended to the returned error
// rather than swallowed or allowed to mask the original cause.
func (b *Backend) CreateSession(ctx context.Context, spec session.Spec) (_ session.Ref, err error) {
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

	// If any create step below fails, roll back everything created earlier in
	// this call: without this, a partial failure (e.g. the Sandbox create
	// failing after the Secret and PVC succeeded) leaves an orphaned Secret
	// (holding a live runner bearer token) and PVC that List/Status/Destroy
	// can never see, since they only ever enumerate Sandboxes. Rollback is
	// best-effort and idempotent (deleteSessionResources treats NotFound as
	// success). It runs on an independent, short-lived context because by the
	// time a failure surfaces the caller's ctx may already be cancelled or
	// past its deadline.
	//
	// EXCEPT when the session pre-existed (the Secret OR PVC create returned
	// AlreadyExists): then the resources belong to a PRIOR CreateSession call
	// and hold live session state — most importantly the PVC's workspace data.
	// A failure later in a re-create (e.g. the credential sync erroring) must
	// return the error WITHOUT deleting them: destroying a pre-existing
	// session as collateral damage of a failed re-create is strictly worse
	// than any partial state the failure leaves behind. Both flags matter
	// (C7): keying the guard to the Secret alone meant a pre-existing PVC
	// (a prior session's workspace) could be deleted as collateral when the
	// Secret happened to be freshly created. The cost of skipping is at most
	// an orphaned fresh Secret — recoverable, unlike workspace data.
	// Fresh-create rollback semantics are unchanged.
	secretPreexisted := false
	pvcPreexisted := false
	defer func() {
		if err == nil || secretPreexisted || pvcPreexisted {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), createRollbackTimeout)
		defer cancel()
		if cleanupErr := b.deleteSessionResources(cleanupCtx, name); cleanupErr != nil {
			err = fmt.Errorf("%w (rollback also failed, resources may be orphaned: %v)", err, cleanupErr)
		}
	}()

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
	// Account-backed claude sessions carry their resolved credential in the
	// per-session Secret (the shared anthropic-credentials Secret is the
	// no-account fallback). The account id also labels the Secret so
	// logout/rotation can enumerate every session holding a copy.
	if len(spec.AnthropicCredential) > 0 {
		secret.Data[secretKeyAnthropicCredential] = spec.AnthropicCredential
		secret.Labels[labelAnthropicAccount] = spec.AnthropicAccountID
	}
	// Account-backed codex sessions carry their resolved auth.json in the
	// per-session Secret (the shared OPENAI_API_KEY Secret is the no-account
	// fallback), mirroring the anthropic path. The account id also labels the
	// Secret so logout/rotation can enumerate every session holding a copy.
	if len(spec.CodexAuthJSON) > 0 {
		secret.Data[secretKeyCodexAuthJSON] = spec.CodexAuthJSON
		secret.Labels[labelCodexAccount] = spec.CodexAccountID
	}
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

	// The Secret and PVC have no ordering dependency on each other — only the
	// Sandbox (whose pod mounts both) depends on them — so create them
	// concurrently to save one API round-trip on every new session (§5). The
	// errgroup's derived context cancels the sibling create the moment either
	// fails; whatever did land is still reachable by the rollback defer below,
	// which enumerates all three resources NotFound-tolerantly. secretPreexisted
	// is written only in the Secret goroutine and read only after g.Wait()
	// returns (which establishes the happens-before), so the defer sees it
	// race-free.
	// Built ahead of the errgroup so the re-create shape check below can compare
	// the desired pod-template env against the existing Sandbox's (C3);
	// buildSandbox is pure, so this costs nothing on the fresh path.
	sb := buildSandbox(spec)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if _, cerr := b.core.CoreV1().Secrets(spec.Namespace).Create(gctx, secret, metav1.CreateOptions{}); cerr != nil {
			if !k8serrors.IsAlreadyExists(cerr) {
				return fmt.Errorf("k8s: create Secret %s: %w", secret.Name, cerr)
			}
			// Idempotent re-create: the Secret already exists (runner-token etc. are
			// reused as-is), and from here on the session's resources belong to a
			// prior CreateSession call — the rollback defer must not delete them.
			secretPreexisted = true
			// C3: the existing Sandbox's pod template baked the credential env
			// SHAPE — the env var name (oauth vs api-key) and its source Secret
			// (per-session vs shared) — at first create, and only a destroy can
			// change it. A re-create whose shape differs (e.g. an oauth+account
			// session re-created with a console key, or an account session
			// re-created accountless) would leave the pod reading the wrong
			// env/Secret and break auth silently — and stripping/patching the
			// Secret for it could brick the next resume (the baked SecretKeyRef
			// is not Optional). Reject BEFORE mutating the Secret.
			if spec.Backend != session.BackendOpenCode {
				if existingSb, gerr := b.agents.AgentsV1alpha1().Sandboxes(spec.Namespace).Get(gctx, name, metav1.GetOptions{}); gerr == nil {
					wantEnv, wantSrc := anthropicEnvShape(sb)
					gotEnv, gotSrc := anthropicEnvShape(existingSb)
					if wantEnv != gotEnv || wantSrc != gotSrc {
						return fmt.Errorf(
							"k8s: session %s already exists with a different auth shape (%s from Secret %q; this create wants %s from Secret %q) — destroy and re-create the session, or select a matching account",
							name, gotEnv, gotSrc, wantEnv, wantSrc)
					}
				}
			}
			// A re-create must not keep a stale account state: with a credential in
			// the spec, patch its key + account label onto the existing Secret
			// (re-creating with a DIFFERENT account of the same shape must win);
			// with none, strip any old key + label (otherwise logout/rotation label
			// enumeration would false-positive on a session that no longer uses the
			// account, and a stale credential copy would linger). Other keys are
			// left untouched.
			if serr := b.syncSessionCredential(gctx, spec); serr != nil {
				return serr
			}
		}
		return nil
	})
	g.Go(func() error {
		if _, cerr := b.core.CoreV1().PersistentVolumeClaims(spec.Namespace).Create(gctx, pvc, metav1.CreateOptions{}); cerr != nil {
			if !k8serrors.IsAlreadyExists(cerr) {
				return fmt.Errorf("k8s: create PVC %s: %w", name, cerr)
			}
			// A pre-existing PVC is a prior session's workspace — the rollback
			// defer must never delete it (C7). Written only here, read after
			// g.Wait() (happens-before), same as secretPreexisted.
			pvcPreexisted = true
		}
		return nil
	})
	if werr := g.Wait(); werr != nil {
		return session.Ref{}, werr
	}

	// Create the Sandbox last: its pod mounts both the Secret and the PVC, so it
	// must not exist before they do.
	// Stamp the provider-key fingerprint the pod is starting against so a later
	// create/resume reconcile can detect the cluster Secret was rotated out from
	// under the running pod (opencode sessions only; best-effort).
	b.stampOpencodeCredsFreshness(ctx, sb, spec)
	// sbLive is the Sandbox as the API server sees it (with its assigned UID),
	// captured for the ownerReferences pass below. On a fresh create it is the
	// Create return value; on a re-create it is the existing object we Get.
	sbLive, err := b.agents.AgentsV1alpha1().Sandboxes(spec.Namespace).Create(ctx, sb, metav1.CreateOptions{})
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return session.Ref{}, fmt.Errorf("k8s: create Sandbox %s: %w", name, err)
		}
		// Idempotent re-create against a session whose pod is already running:
		// Get the existing Sandbox once and reuse it both for the creds/shape
		// checks and as the owner-ref UID source.
		existing, gerr := b.agents.AgentsV1alpha1().Sandboxes(spec.Namespace).Get(ctx, name, metav1.GetOptions{})
		if gerr == nil {
			sbLive = existing
			if spec.Backend == session.BackendOpenCode {
				// The existing Sandbox keeps its original creds stamp, so warn if the
				// live Secret has since rotated (the running pod still holds the old key).
				b.warnIfOpencodeCredsRotated(ctx, existing)
			} else {
				// C3: the existing pod template baked the credential env SHAPE — the
				// env var name (oauth vs api-key) and its source Secret (per-session
				// vs shared) — at first create; the Secret sync above only refreshes
				// bytes. A re-create that changes the shape (e.g. an oauth session
				// re-created with a console account) would inject the new credential
				// under the OLD env var and break auth silently. Reject it loudly.
				wantEnv, wantSrc := anthropicEnvShape(sb)
				gotEnv, gotSrc := anthropicEnvShape(existing)
				if wantEnv != gotEnv || wantSrc != gotSrc {
					return session.Ref{}, fmt.Errorf(
						"k8s: session %s already exists with a different auth shape (%s from Secret %q; this create wants %s from Secret %q) — destroy and re-create the session, or select a matching account",
						name, gotEnv, gotSrc, wantEnv, wantSrc)
				}
			}
		}
	}

	// Set ownerReferences (Secret + PVC → Sandbox) so a cluster-side
	// `kubectl delete sandbox` outside the CLI cascades to the per-session Secret
	// (a live runner bearer token) and PVC that List/Status/Destroy — which only
	// enumerate Sandboxes — can never see and would otherwise orphan (§6). This
	// runs only on resources THIS call created: a pre-existing Secret or PVC
	// belongs to a prior CreateSession call (the PVC especially holds a prior
	// session's workspace data — C7), so it must not be adopted or mutated here.
	// Best-effort — a failed owner-ref patch degrades to the pre-owner-ref
	// behavior (Destroy still cleans up; only an out-of-band delete leaks), so it
	// must never fail an otherwise-successful create or tear down a live session.
	if sbLive != nil && sbLive.UID != "" {
		owner := sandboxOwnerRef(sbLive)
		if !secretPreexisted {
			b.setSecretOwnerRef(ctx, spec.Namespace, secret.Name, owner)
		}
		if !pvcPreexisted {
			b.setPVCOwnerRef(ctx, spec.Namespace, name, owner)
		}
	}

	return session.Ref{ID: spec.ID}, nil
}

// sandboxOwnerRef builds an ownerReference pointing at the given Sandbox, used to
// GC the per-session Secret and PVC when the Sandbox is deleted out-of-band.
func sandboxOwnerRef(sb *agentv1alpha1.Sandbox) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: agentv1alpha1.GroupVersion.String(),
		Kind:       "Sandbox",
		Name:       sb.Name,
		UID:        sb.UID,
	}
}

// hasOwnerRef reports whether refs already contains an owner with the given UID,
// so setting the owner ref is idempotent across re-create calls.
func hasOwnerRef(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, r := range refs {
		if r.UID == uid {
			return true
		}
	}
	return false
}

// setSecretOwnerRef patches the ownerReference onto the per-session Secret under
// RetryOnConflict (the setReplicas/pin/syncSessionCredential idiom). Get+Update
// preserves every other field (Data, Labels, existing owner refs), so a
// concurrent credential reconcile does not race it into stripping the ref.
// Best-effort: a persistent failure is warned, not returned — see the call site.
func (b *Backend) setSecretOwnerRef(ctx context.Context, namespace, name string, owner metav1.OwnerReference) {
	secrets := b.core.CoreV1().Secrets(namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, getErr := secrets.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		if hasOwnerRef(secret.OwnerReferences, owner.UID) {
			return nil
		}
		secret.OwnerReferences = append(secret.OwnerReferences, owner)
		_, updateErr := secrets.Update(ctx, secret, metav1.UpdateOptions{})
		return updateErr
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not set ownerReference on Secret %s (out-of-band Sandbox delete may orphan it): %v\n", name, err)
	}
}

// setPVCOwnerRef is setSecretOwnerRef for the per-session PVC. Called only when
// the PVC was created by THIS call — never on a pre-existing PVC, whose workspace
// data belongs to a prior session (C7).
func (b *Backend) setPVCOwnerRef(ctx context.Context, namespace, name string, owner metav1.OwnerReference) {
	pvcs := b.core.CoreV1().PersistentVolumeClaims(namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pvc, getErr := pvcs.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		if hasOwnerRef(pvc.OwnerReferences, owner.UID) {
			return nil
		}
		pvc.OwnerReferences = append(pvc.OwnerReferences, owner)
		_, updateErr := pvcs.Update(ctx, pvc, metav1.UpdateOptions{})
		return updateErr
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not set ownerReference on PVC %s (out-of-band Sandbox delete may orphan it): %v\n", name, err)
	}
}

// anthropicEnvShape extracts the Anthropic credential env shape from a Sandbox
// pod template: the env var name (the credential TYPE — CLAUDE_CODE_OAUTH_TOKEN
// vs ANTHROPIC_API_KEY) and the Secret it reads from (the SOURCE — per-session
// vs shared). Zero values when no credential env is present. Used by the C3
// re-create shape check; see buildEnv for the shape's semantics.
func anthropicEnvShape(sb *agentv1alpha1.Sandbox) (envName, secretName string) {
	for i := range sb.Spec.PodTemplate.Spec.Containers {
		for _, e := range sb.Spec.PodTemplate.Spec.Containers[i].Env {
			if e.Name != "CLAUDE_CODE_OAUTH_TOKEN" && e.Name != "ANTHROPIC_API_KEY" {
				continue
			}
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				return e.Name, e.ValueFrom.SecretKeyRef.Name
			}
			return e.Name, ""
		}
	}
	return "", ""
}

// syncSessionCredential reconciles the per-session Secret's account-credential
// keys and labels with the spec — both the anthropic-credential key/label and the
// codex-auth-json key/label — leaving every other key (runner-token,
// opencode-password, ssh-authorized-key) untouched. Used by CreateSession when
// the Secret already exists: with an account credential in the spec it patches
// the key + label (re-creating a session id with a different account must not
// keep the old credential); with none it strips any stale key + label, so
// logout/rotation label enumeration never false-positives on a session that no
// longer uses an account and no stale credential copy lingers. A session is a
// single backend, so at most one credential family is non-empty; the other's
// strip branch is a no-op. Both families reconcile under ONE Get+Update. A no-op
// Update is skipped when the Secret already matches. Get+Update under
// RetryOnConflict matches the existing setReplicas/pin idiom. The returned error
// never carries credential bytes.
func (b *Backend) syncSessionCredential(ctx context.Context, spec session.Spec) error {
	name := sessionSecretName(string(spec.ID))
	secrets := b.core.CoreV1().Secrets(spec.Namespace)
	// [V33] Track whether this re-create rotates a DIFFERENT non-empty credential
	// onto an existing session Secret (a same-shape account swap). The pod
	// resolved its credential env from the Secret ONCE at start (SecretKeyRef),
	// so the running pod keeps authenticating as the OLD account until a pod
	// restart — mirroring warnIfOpencodeCredsRotated on the opencode path. We
	// warn whenever the bytes change on an existing credential rather than gating
	// on live pod state: determining pod-running here would need an extra API
	// call this path doesn't otherwise make, and slightly over-warning (e.g. on a
	// currently-suspended session, whose next resume adopts the new creds anyway)
	// is acceptable versus silently swapping the account under a live pod.
	credRotated := false
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		credRotated = false // recompute per attempt; the last attempt's value wins
		secret, getErr := secrets.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		changed := false
		if len(spec.AnthropicCredential) > 0 &&
			len(secret.Data[secretKeyAnthropicCredential]) > 0 &&
			string(secret.Data[secretKeyAnthropicCredential]) != string(spec.AnthropicCredential) {
			credRotated = true // an existing non-empty credential is being replaced
		}
		changed = reconcileSecretCredential(secret, secretKeyAnthropicCredential, labelAnthropicAccount, spec.AnthropicCredential, spec.AnthropicAccountID) || changed
		changed = reconcileSecretCredential(secret, secretKeyCodexAuthJSON, labelCodexAccount, spec.CodexAuthJSON, spec.CodexAccountID) || changed
		if !changed {
			return nil
		}
		_, updateErr := secrets.Update(ctx, secret, metav1.UpdateOptions{})
		return updateErr
	})
	if err != nil {
		return fmt.Errorf("k8s: sync account credential on Secret %s: %w", name, err)
	}
	if credRotated {
		fmt.Fprintf(os.Stderr,
			"warning: session %s was re-created with a different Anthropic account (Secret updated to %q). "+
				"The running pod still authenticates with the OLD account — the credential is resolved from the "+
				"Secret only at pod start. Restart the pod to adopt the new account: "+
				"`sandbox suspend %s && sandbox resume %s` (or destroy + recreate). Until then `sandbox auth logout` "+
				"for the old account will not list this session.\n",
			spec.ID, spec.AnthropicAccountID, spec.ID, spec.ID)
	}
	return nil
}

// reconcileSecretCredential patches (credential non-empty) or strips (empty) one
// account-credential key + account label on a per-session Secret, returning
// whether it changed anything. Shared by syncSessionCredential across the
// anthropic and codex credential families so both reconcile with identical
// patch/strip semantics under one Update. It never echoes the credential bytes.
func reconcileSecretCredential(secret *corev1.Secret, dataKey, labelKey string, credential []byte, accountID string) bool {
	changed := false
	if len(credential) > 0 {
		if string(secret.Data[dataKey]) != string(credential) {
			if secret.Data == nil {
				secret.Data = map[string][]byte{}
			}
			secret.Data[dataKey] = credential
			changed = true
		}
		if secret.Labels[labelKey] != accountID {
			if secret.Labels == nil {
				secret.Labels = map[string]string{}
			}
			secret.Labels[labelKey] = accountID
			changed = true
		}
	} else {
		if _, ok := secret.Data[dataKey]; ok {
			delete(secret.Data, dataKey)
			changed = true
		}
		if _, ok := secret.Labels[labelKey]; ok {
			delete(secret.Labels, labelKey)
			changed = true
		}
	}
	return changed
}

// Start waits for the session pod to be running and ready.
func (b *Backend) Start(ctx context.Context, ref session.Ref) error {
	return b.StartWithProgress(ctx, ref, nil)
}

// StartWithProgress is Start with a callback invoked as the pod moves through its
// startup phases — "scheduling" → "pulling image" → "starting" — so a caller can
// animate a connect splash instead of blocking on a silent, frozen terminal while
// the node schedules the pod and pulls the runner image (Phase 2). onPhase fires
// only when the phase string changes (never with ""), and may be nil — in which
// case this is exactly Start.
func (b *Backend) StartWithProgress(ctx context.Context, ref session.Ref, onPhase func(detail string)) error {
	if err := b.waitForPodReady(ctx, ref, onPhase); err != nil {
		return err
	}
	// Best-effort: a failed pin must not fail the start — the session is up;
	// it only means a later resume falls back to the (moving) tag ref.
	_ = b.pinRunnerImageDigest(ctx, ref)
	return nil
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
			Container: runnerContainerName,
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

// SessionsForAccount lists the ids of sessions whose per-session Secret still
// carries a copy of the given Anthropic account's credential — enumerated by
// the labelAnthropicAccount=<accountID> label the credential provisioning
// stamps on each copy. Used by `sandbox auth logout` to report which live
// sessions still hold a copy after a local account removal: local removal does
// not scrub those per-session copies (running pods hold the env var regardless)
// nor revoke the credential at Anthropic. Read-only; returns an empty slice
// when nothing matches.
//
// [V33] Swap-window caveat: this enumerates by the Secret's CURRENT
// labelAnthropicAccount, so a session whose Secret was just re-created onto a
// different account (a same-shape swap) is reported under the NEW account even
// though its running pod still authenticates with the OLD one until a pod
// restart. syncSessionCredential prints a rotation warning at swap time; this
// report intentionally does not do pod-start-time comparison.
func (b *Backend) SessionsForAccount(ctx context.Context, accountID string) ([]string, error) {
	selector := fmt.Sprintf("%s=%s", labelAnthropicAccount, accountID)
	list, err := b.core.CoreV1().Secrets(b.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("k8s: list secrets for account %s: %w", accountID, err)
	}
	ids := make([]string, 0, len(list.Items))
	for i := range list.Items {
		sec := &list.Items[i]
		// Prefer the session-id label; fall back to stripping the Secret's
		// "-runner" suffix if it is somehow absent.
		id := sec.Labels[labelSessionID]
		if id == "" {
			id = strings.TrimSuffix(sec.Name, "-runner")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Suspend sets replicas to 0, terminating the pod but preserving the PVC.
func (b *Backend) Suspend(ctx context.Context, ref session.Ref) error {
	return b.setReplicas(ctx, ref, 0)
}

// Resume sets replicas back to 1 and waits for the pod to be ready.
//
// Before scaling up it rewrites the pod template's runner image from the
// pinned-digest annotation (stamped at first ready — see pinRunnerImageDigest),
// so the resumed pod runs the exact binary the session was suspended with
// rather than whatever a moving tag (:latest, PullAlways) resolves to weeks
// later — which would reinterpret the session's persisted events.db /
// session.json under new-shape assumptions. Image + replicas go in one Update:
// at replicas 0 there is no pod, so the template change cannot churn a live one.
func (b *Backend) Resume(ctx context.Context, ref session.Ref) error {
	sandboxes := b.agents.AgentsV1alpha1().Sandboxes(b.namespace)
	one := int32(1)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		sb, getErr := sandboxes.Get(ctx, string(ref.ID), metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		applyPinnedRunnerImage(sb)
		// The resumed pod resolves provider keys from the CURRENT opencode-
		// credentials Secret, so refresh the freshness stamp to match (a no-op for
		// non-opencode sessions). Keeps later drift detection accurate.
		b.refreshOpencodeCredsStamp(ctx, sb)
		sb.Spec.Replicas = &one
		_, updateErr := sandboxes.Update(ctx, sb, metav1.UpdateOptions{})
		return updateErr
	})
	if err != nil {
		return fmt.Errorf("k8s: update Sandbox %s for resume: %w", ref.ID, err)
	}
	if err := b.waitForPodReady(ctx, ref, nil); err != nil {
		return err
	}
	// Re-pin best-effort: a no-op when the annotation already matches (the
	// common case, since the template now carries the pinned digest ref).
	_ = b.pinRunnerImageDigest(ctx, ref)
	return nil
}

// applyPinnedRunnerImage rewrites the Sandbox pod template's runner container
// image to the digest-pinned ref recorded by pinRunnerImageDigest, if any. It
// also relaxes an auto-resolved PullAlways to IfNotPresent — the digest is
// immutable, so re-pulling is wasted work and would break resume when the
// registry is unreachable (an explicit IfNotPresent/Never override is left
// untouched). No-op when the annotation is absent or already applied.
func applyPinnedRunnerImage(sb *agentv1alpha1.Sandbox) {
	pinned := sb.Annotations[annotationPinnedRunnerImage]
	if pinned == "" {
		return
	}
	for i := range sb.Spec.PodTemplate.Spec.Containers {
		c := &sb.Spec.PodTemplate.Spec.Containers[i]
		if c.Name != runnerContainerName {
			continue
		}
		if c.Image != pinned {
			c.Image = pinned
			if c.ImagePullPolicy == corev1.PullAlways {
				c.ImagePullPolicy = corev1.PullIfNotPresent
			}
		}
		return
	}
}

// pinRunnerImageDigest stamps annotationPinnedRunnerImage with the digest-pinned
// ref of the runner image the session's pod is ACTUALLY running, derived from
// the kubelet-resolved containerStatuses imageID (no registry client or
// credentials needed). Called after waitForPodReady succeeds, so the running
// pod — not a hope about the tag — defines the session's binary. Re-stamps if
// the running digest ever differs (e.g. a mid-session reschedule pulled a newer
// tag): the state on the PVC was last written by the binary that is running
// NOW, so that is what resume must reproduce.
func (b *Backend) pinRunnerImageDigest(ctx context.Context, ref session.Ref) error {
	sandboxes := b.agents.AgentsV1alpha1().Sandboxes(b.namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		sb, err := sandboxes.Get(ctx, string(ref.ID), metav1.GetOptions{})
		if err != nil {
			return err
		}
		pod, err := b.getPodForSandbox(ctx, sb)
		if err != nil {
			return err
		}
		if pod == nil || pod.DeletionTimestamp != nil {
			return nil
		}
		pinned := pinnedRunnerImageRef(sb, pod)
		if pinned == "" || sb.Annotations[annotationPinnedRunnerImage] == pinned {
			return nil
		}
		if sb.Annotations == nil {
			sb.Annotations = map[string]string{}
		}
		sb.Annotations[annotationPinnedRunnerImage] = pinned
		_, err = sandboxes.Update(ctx, sb, metav1.UpdateOptions{})
		return err
	})
}

// pinnedRunnerImageRef derives the digest-pinned image ref for the runner
// container: the spec image's repo + the digest from the pod's kubelet-reported
// imageID. Returns "" when the digest cannot be determined (container not
// started, or an imageID with no registry digest — e.g. a locally-loaded dev
// image), in which case there is nothing safe to pin.
func pinnedRunnerImageRef(sb *agentv1alpha1.Sandbox, pod *corev1.Pod) string {
	var specImage string
	for _, c := range sb.Spec.PodTemplate.Spec.Containers {
		if c.Name == runnerContainerName {
			specImage = c.Image
			break
		}
	}
	if specImage == "" {
		return ""
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != runnerContainerName {
			continue
		}
		if digest := imageIDDigest(cs.ImageID); digest != "" {
			return imageRepo(specImage) + "@" + digest
		}
	}
	return ""
}

// imageRepo strips any tag or digest from an image ref, preserving a registry
// host:port (a colon only counts as a tag separator after the last slash).
func imageRepo(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		ref = ref[:colon]
	}
	return ref
}

// imageIDDigest extracts the "sha256:…" digest from a kubelet-reported imageID
// ("repo@sha256:…" on containerd, "docker-pullable://repo@sha256:…" on older
// docker runtimes). Returns "" when the imageID carries no digest.
func imageIDDigest(imageID string) string {
	i := strings.LastIndex(imageID, "@")
	if i < 0 {
		return ""
	}
	digest := imageID[i+1:]
	if !strings.HasPrefix(digest, "sha256:") {
		return ""
	}
	return digest
}

// Destroy deletes the Sandbox, PVC and per-session Secret. Irreversible.
//
// All three deletions are attempted even if one fails: a transient error on the
// Sandbox or PVC must not orphan the remaining resources — most importantly the
// per-session Secret, which holds the runner bearer token. NotFound is treated
// as success so Destroy is idempotent (C5).
func (b *Backend) Destroy(ctx context.Context, ref session.Ref) error {
	name := string(ref.ID)
	if err := b.deleteSessionResources(ctx, name); err != nil {
		return fmt.Errorf("k8s: destroy %s: %w", name, err)
	}
	return nil
}

// deleteSessionResources deletes the Sandbox, PVC and per-session Secret for a
// given session name. All three deletions are attempted even if one fails —
// a transient error on the Sandbox or PVC must not orphan the remaining
// resources, most importantly the per-session Secret, which holds the runner
// bearer token. NotFound is treated as success so this is idempotent whether
// called from Destroy (tearing down a live session) or from CreateSession's
// rollback (cleaning up a partially created one). Returns nil if every
// resource was deleted (or already absent), otherwise the joined errors.
func (b *Backend) deleteSessionResources(ctx context.Context, name string) error {
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
		return errors.Join(errs...)
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
	// Recover the identity fields the runner pod was created with. These are
	// write-once container env (see buildEnv); reading them back here means a
	// session attached from the list — not just one freshly created in this
	// process — carries its real paths (needed for Mutagen sync) and Backend
	// (needed to pick the claude vs opencode connect path). PROJECT_PATH is the
	// pod's cwd / bind-mount = the WORKSPACE path (the local Mutagen alpha);
	// SANDBOX_PROJECT_ROOT is the repo-root ProjectPath for display/grouping.
	// Pre-existing pods created before SANDBOX_PROJECT_ROOT existed carry only
	// PROJECT_PATH, so fall back to it (workspace == repo root there anyway).
	workspace := sandboxEnv(sb, "PROJECT_PATH")
	projectRoot := sandboxEnv(sb, "SANDBOX_PROJECT_ROOT")
	if projectRoot == "" {
		projectRoot = workspace
	}
	st := session.State{
		ID:            session.ID(sb.Name),
		SandboxName:   sb.Name,
		CreatedAt:     sb.CreationTimestamp.Time,
		ProjectPath:   projectRoot,
		WorkspacePath: workspace,
		Backend:       sandboxEnv(sb, "SANDBOX_BACKEND"),
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
			switch {
			case podStale(pod, time.Now()):
				// Node-eviction lag (§1d): a pod on a dead/unreachable node keeps
				// reading Running/Ready for minutes while the kubelet is silent. A
				// staleness signal (terminating, NodeLost, or Ready-not-True too long)
				// means we can't trust that — report UNKNOWN rather than a confident
				// RUNNING whose SSE stream would be silently stalled.
				st.PodReady = false
				st.Status = session.StatusUnknown
			case isPodReady(pod):
				st.PodReady = true
				st.Status = session.StatusRunning
			default:
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
// Used to recover the identity fields (PROJECT_PATH, SANDBOX_PROJECT_ROOT,
// SANDBOX_BACKEND) the pod was created with so Status/List report them for any
// session, not only those created in the current process.
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
	// Poll at 1s (was 2s): the tail wait after the pod actually becomes ready is
	// bounded by the poll interval, so halving it shaves up to ~1s off every
	// cold start / resume for one extra lightweight Sandbox+pod Get per second
	// (§5). Kept at 1s rather than 500ms to stay gentle on the API server.
	return wait.PollUntilContextCancel(ctx, 1*time.Second, true, func(ctx context.Context) (bool, error) {
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

// stalenessThreshold bounds how long we keep trusting a k8s object's Ready/phase
// reporting after readiness lapses. When a node dies the kubelet stops
// heart-beating and k8s takes minutes (node-monitor-grace-period + eviction lag)
// to mark the pod NotReady/Failed, so a crashed session keeps reading Running
// with a silently-stalled SSE stream (§1d). Once a not-True Ready condition has
// held past this threshold we stop trusting the phase and report UNKNOWN. Set
// above the default node-monitor-grace-period (~40s) so a pod that is briefly
// not-Ready while starting isn't misflagged.
const stalenessThreshold = 90 * time.Second

// podReasonNodeLost is the status reason the node lifecycle controller stamps on
// pods whose node became unreachable/lost (k8s pkg/controller/util/node:
// NodeUnreachablePodReason). The kubelet has gone silent, so the pod's phase and
// Ready condition are stale even when they still read Running/True.
const podReasonNodeLost = "NodeLost"

// readyStale reports whether a Ready-style condition that is no longer True has
// held that way past stalenessThreshold. A True condition is never stale; a
// condition with no recorded transition time is treated as not-yet-stale (we
// can't age it). Shared by the pod (Status/List) and Sandbox (watch) paths so
// both apply the same cross-check to whatever object they hold.
func readyStale(status string, lastTransition metav1.Time, now time.Time) bool {
	if status == string(metav1.ConditionTrue) {
		return false
	}
	return !lastTransition.IsZero() && now.Sub(lastTransition.Time) >= stalenessThreshold
}

// podStale reports whether a pod that might otherwise read Running/Ready is
// actually in a state we can't trust (§1d): being deleted (eviction/drain),
// reported lost by the node controller, or Running-but-Ready-not-True past the
// staleness threshold. Cross-checking this before mapping to StatusRunning stops
// a dead-node pod — whose phase/Ready lag by minutes — from masquerading as a
// healthy session with a stalled SSE stream. A still-Pending pod that is slow to
// start is left to the normal Creating path, not flagged here.
func podStale(pod *corev1.Pod, now time.Time) bool {
	if pod.DeletionTimestamp != nil {
		return true
	}
	if pod.Status.Reason == podReasonNodeLost {
		return true
	}
	if pod.Status.Phase == corev1.PodRunning {
		for _, cond := range pod.Status.Conditions {
			if cond.Type != corev1.PodReady {
				continue
			}
			if cond.Reason == podReasonNodeLost {
				return true
			}
			return readyStale(string(cond.Status), cond.LastTransitionTime, now)
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
							Name:            runnerContainerName,
							Image:           spec.RunnerImage,
							ImagePullPolicy: resolveImagePullPolicy(spec.ImagePullPolicy, spec.RunnerImage),
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: portRunner},
								{Name: "ssh", ContainerPort: portSSH},
								{Name: "opencode", ContainerPort: portOpencode},
								// Informational, mirroring the opencode entry: the SPDY
								// port-forward targets the pod port directly and does not
								// require a declared containerPort. codex app-server binds
								// this on loopback only.
								{Name: "codex", ContainerPort: portCodex},
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
// session has an absolute workspace path, the workspace subtree is ALSO bind-
// mounted (via subPath) at that real host path — e.g. /Users/cullen/git/homelab
// — so the Claude SDK runs with cwd equal to the host workspace path rather than
// /session/workspace/<path>. The mount tracks WorkspacePath (the worktree dir
// once one exists), not the repo-root ProjectPath.
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
	wp := workspacePath(spec)
	if strings.HasPrefix(wp, "/") && wp != "/" {
		// subPath must be relative to the PVC root; the workspace path starts
		// with "/", so "workspace"+wp yields e.g. "workspace/Users/cullen/git/x".
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "session",
			MountPath: wp,
			SubPath:   "workspace" + wp,
		})
	}
	return mounts
}

// workspacePath resolves the pod's bind-mount / SDK cwd: Spec.WorkspacePath when
// set, else Spec.ProjectPath (no worktree — the workspace IS the repo root).
func workspacePath(spec session.Spec) string {
	if spec.WorkspacePath != "" {
		return spec.WorkspacePath
	}
	return spec.ProjectPath
}

// buildEnv builds the runner container's env, branching on the backend. Common
// vars are set for all sessions; backend-specific credentials/config are added
// only for the matching backend. For the claude backend exactly one Anthropic
// credential is populated per pod (OAuth token vs Console API key), selected by
// spec.AnthropicAuth — never both, since Claude Code would reject the OAuth
// token if a real x-api-key were also present (see the claude-sdk branch below).
// For opencode, exactly one provider key (selected by spec.OpencodeProvider) is
// injected fail-closed from the shared opencode-credentials Secret.
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
		// PROJECT_PATH is the pod's cwd / bind-mount (the runner reads it as the
		// SDK cwd — see runner/src/session.ts, exec.ts). It is the WORKSPACE path
		// (the worktree dir once one exists), which equals ProjectPath when there
		// is no worktree. SANDBOX_PROJECT_ROOT carries the repo-root ProjectPath
		// for display/grouping so Status/List can recover both after the fact
		// (statusFromSandbox); older pods without it fall back to PROJECT_PATH.
		{Name: "PROJECT_PATH", Value: workspacePath(spec)},
		{Name: "SANDBOX_PROJECT_ROOT", Value: spec.ProjectPath},
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
		return append(env, opencodeEnv(spec, name)...)
	}

	if spec.Backend == session.BackendCodex {
		return append(env, codexEnv(spec, name)...)
	}

	// Default (claude-sdk): exactly ONE Anthropic credential env is populated per
	// pod. The env var (the credential TYPE) is always selected by
	// spec.AnthropicAuth — "api-key" → ANTHROPIC_API_KEY, ""/"oauth" →
	// CLAUDE_CODE_OAUTH_TOKEN. Claude Code prefers x-api-key over the subscription
	// OAuth token and rejects the OAuth token as an invalid x-api-key when
	// ANTHROPIC_API_KEY is also set — so this is a strict either/or, never both.
	//
	// The credential SOURCE branches on spec.AnthropicAccountID (the fail-closed
	// signal — an account was selected, so its bytes were written to the
	// per-session Secret; NOT len(credential), which the pod template can't see):
	//
	//   AnthropicAccountID != "" → the per-session Secret sessionSecretName(name),
	//     key "anthropic-credential", NOT Optional — CreateSession wrote that key,
	//     so a missing key means a provisioning bug that should fail the pod
	//     loudly rather than start it unauthenticated.
	//   AnthropicAccountID == "" → the shared anthropic-credentials Secret
	//     (key api-key / console-api-key), Optional so pods still start before the
	//     out-of-band Secret is provisioned. The backward-compatible fallback.
	//
	// The choice lives in the Sandbox pod template written at CreateSession;
	// Resume only flips replicas, so the selected auth path + source persist for
	// the session's lifetime across suspend/resume.
	envName := "CLAUDE_CODE_OAUTH_TOKEN"
	if spec.AnthropicAuth == "api-key" {
		envName = "ANTHROPIC_API_KEY"
	}
	if spec.AnthropicAccountID != "" {
		return append(env, corev1.EnvVar{
			Name: envName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: sessionSecretName(name)},
					Key:                  secretKeyAnthropicCredential,
				},
			},
		})
	}
	sharedKey := anthropicSecretKey
	if spec.AnthropicAuth == "api-key" {
		sharedKey = anthropicAPISecretKey
	}
	return append(env, corev1.EnvVar{
		Name: envName,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: anthropicSecretName},
				Key:                  sharedKey,
				Optional:             boolPtr(true),
			},
		},
	})
}

// opencodeProviderRef maps a session's selected opencode provider to the env var
// opencode reads for it and the opencode-credentials Secret key holding it.
type opencodeProviderRef struct {
	envName   string
	secretKey string
}

// resolveOpencodeProvider maps Spec.OpencodeProvider to its (env var, Secret key)
// pair. Empty or unrecognized defaults to Anthropic — the documented opencode
// default and the only provider currently reachable, since the user-facing
// provider selector is deferred to the client/cred generalization item.
func resolveOpencodeProvider(provider string) opencodeProviderRef {
	switch provider {
	case session.OpencodeProviderOpenAI:
		return opencodeProviderRef{"OPENAI_API_KEY", opencodeSecretKeyOpenAI}
	case session.OpencodeProviderZen:
		return opencodeProviderRef{"OPENCODE_API_KEY", opencodeSecretKeyZen}
	default: // "" or session.OpencodeProviderAnthropic
		return opencodeProviderRef{"ANTHROPIC_API_KEY", opencodeSecretKeyAnthropic}
	}
}

// opencodeCredsHash returns a short, non-reversible fingerprint (first 8 hex of
// sha256) of the SELECTED provider's key bytes in the opencode-credentials
// Secret, or "" when that key is absent/empty. Used to stamp the Sandbox at
// create time (annotationOpencodeCredsHash) and to detect drift on the reconcile
// paths. Deliberately truncated: it identifies a rotation, it does not reconstruct
// the key.
func opencodeCredsHash(data map[string][]byte, provider string) string {
	ref := resolveOpencodeProvider(provider)
	v, ok := data[ref.secretKey]
	if !ok || len(v) == 0 {
		return ""
	}
	sum := sha256.Sum256(v)
	return hex.EncodeToString(sum[:])[:8]
}

// stampOpencodeCredsFreshness records, on the Sandbox being created, the provider
// the opencode session was started against and a fingerprint of that provider's
// key in the live opencode-credentials Secret. Best-effort: a read failure or an
// absent key leaves the Sandbox unstamped (the fail-closed SecretKeyRef already
// surfaces a missing key at pod start), so this never fails CreateSession. Only
// meaningful for opencode-server sessions.
func (b *Backend) stampOpencodeCredsFreshness(ctx context.Context, sb *agentv1alpha1.Sandbox, spec session.Spec) {
	if spec.Backend != session.BackendOpenCode {
		return
	}
	sec, err := b.core.CoreV1().Secrets(spec.Namespace).Get(ctx, opencodeSecretName, metav1.GetOptions{})
	if err != nil {
		return // secret not readable yet; the fail-closed env ref will surface it
	}
	hash := opencodeCredsHash(sec.Data, spec.OpencodeProvider)
	if hash == "" {
		return
	}
	if sb.Annotations == nil {
		sb.Annotations = map[string]string{}
	}
	sb.Annotations[annotationOpencodeCredsHash] = hash
	sb.Annotations[annotationOpencodeProvider] = resolveOpencodeProvider(spec.OpencodeProvider).secretKey
}

// liveOpencodeCredHash reads the shared opencode-credentials Secret and returns
// the current fingerprint of the given provider key plus the Secret's
// resourceVersion. ok is false on any read error or absent/empty key.
func (b *Backend) liveOpencodeCredHash(ctx context.Context, secretKey string) (hash, resourceVersion string, ok bool) {
	sec, err := b.core.CoreV1().Secrets(b.namespace).Get(ctx, opencodeSecretName, metav1.GetOptions{})
	if err != nil {
		return "", "", false
	}
	v, present := sec.Data[secretKey]
	if !present || len(v) == 0 {
		return "", "", false
	}
	sum := sha256.Sum256(v)
	return hex.EncodeToString(sum[:])[:8], sec.ResourceVersion, true
}

// warnIfOpencodeCredsRotated compares an existing Sandbox's create-time creds
// stamp against the live opencode-credentials Secret and prints a warning to
// stderr when they differ: the running pod resolved its provider key ONCE at pod
// start, so a rotated cluster Secret is not reaching it. Adopting the new key
// requires a pod restart (suspend/resume, or destroy + recreate). Best-effort:
// any read error or missing stamp is a silent no-op.
func (b *Backend) warnIfOpencodeCredsRotated(ctx context.Context, sb *agentv1alpha1.Sandbox) {
	stamp := sb.Annotations[annotationOpencodeCredsHash]
	secretKey := sb.Annotations[annotationOpencodeProvider]
	if stamp == "" || secretKey == "" {
		return
	}
	live, rv, ok := b.liveOpencodeCredHash(ctx, secretKey)
	if !ok || live == stamp {
		return
	}
	fmt.Fprintf(os.Stderr,
		"warning: opencode-credentials (key %q) for session %s was rotated since the pod started "+
			"(stamped %s, live %s, secret resourceVersion %s). The running pod still authenticates with the "+
			"OLD key — provider keys are resolved from the Secret only at pod start. Restart the pod to adopt "+
			"the new key: `sandbox suspend %s && sandbox resume %s` (or destroy + recreate).\n",
		secretKey, sb.Name, stamp, live, rv, sb.Name, sb.Name)
}

// refreshOpencodeCredsStamp rewrites the Sandbox's creds fingerprint to the live
// Secret's value in place, so a pod that is about to (re)start against the
// current Secret carries an accurate stamp. No-op unless the Sandbox already
// carries a provider stamp (i.e. an opencode session) and the live key resolves.
func (b *Backend) refreshOpencodeCredsStamp(ctx context.Context, sb *agentv1alpha1.Sandbox) {
	secretKey := sb.Annotations[annotationOpencodeProvider]
	if secretKey == "" {
		return
	}
	live, _, ok := b.liveOpencodeCredHash(ctx, secretKey)
	if !ok {
		return
	}
	if sb.Annotations == nil {
		sb.Annotations = map[string]string{}
	}
	sb.Annotations[annotationOpencodeCredsHash] = live
}

// opencodeEnv returns the env vars specific to opencode-server sessions: the
// serve basic-auth credentials, the data dir + config path on the PVC, and the
// SINGLE selected provider's API key.
//
// Only the selected provider's key is injected (resolveOpencodeProvider), and its
// SecretKeyRef is NOT Optional — a fail-closed hardening change from the prior
// all-providers, all-optional fan-out. If the opencode-credentials Secret is
// absent, or present but missing the selected key, the kubelet cannot populate
// the env var and the container never starts: the pod stalls in
// CreateContainerConfigError with a "couldn't find key <key> in Secret
// agent-sessions/opencode-credentials" (or "secret ... not found") event, rather
// than silently starting an agent with no provider credential. The runner still
// drives opencode config generation off which provider env vars are present
// (buildOpencodeConfig) — it now sees exactly one.
func opencodeEnv(spec session.Spec, name string) []corev1.EnvVar {
	prov := resolveOpencodeProvider(spec.OpencodeProvider)
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
		{
			Name: prov.envName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: opencodeSecretName},
					Key:                  prov.secretKey,
					// NOT Optional: fail closed if the selected provider key is absent.
				},
			},
		},
	}
}

// codexEnv builds the env for a codex-app-server pod, mirroring opencodeEnv's
// fail-closed shape. CODEX_HOME is always set to the PVC-persisted state dir
// (mirrors CLAUDE_CONFIG_DIR / XDG_DATA_HOME) so app-server state survives
// suspend/resume. The credential SOURCE branches on spec.CodexAuthJSON (the pod
// template cannot see len(bytes) at resume, so the branch keys off it at create):
//
//	CodexAuthJSON non-empty → the per-session Secret sessionSecretName(name), key
//	  "codex-auth-json", NOT Optional — CreateSession wrote it, so a missing key
//	  means a provisioning bug that should fail the pod loudly rather than start
//	  it unauthenticated. The pod materializes it as a file at $CODEX_HOME/auth.json.
//	CodexAuthJSON empty → OPENAI_API_KEY from the shared opencode-credentials
//	  Secret (key openai-api-key), NOT Optional (fail-closed like opencodeEnv:
//	  starting uncredentialed is worse than failing the pod).
func codexEnv(spec session.Spec, name string) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "CODEX_HOME", Value: "/session/state/codex"},
	}
	if len(spec.CodexAuthJSON) > 0 {
		return append(env, corev1.EnvVar{
			Name: "CODEX_AUTH_JSON",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: sessionSecretName(name)},
					Key:                  secretKeyCodexAuthJSON,
					// NOT Optional: CreateSession wrote the key.
				},
			},
		})
	}
	return append(env, corev1.EnvVar{
		Name: "OPENAI_API_KEY",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: opencodeSecretName},
				Key:                  opencodeSecretKeyOpenAI,
				// NOT Optional: fail closed if the shared OpenAI key is absent.
			},
		},
	})
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

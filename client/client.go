package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/rest"

	"github.com/cullenmcdermott/sandbox/internal/index"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
)

// Public type aliases for the normalized session model. These are identical to
// the engine types (a Go alias is type identity, not a copy), so callers and the
// CLI/TUI share the exact same structs with no conversion or drift.
type (
	ID                 = session.ID
	Ref                = session.Ref
	Spec               = session.Spec
	State              = session.State
	Status             = session.Status
	Event              = session.Event
	TurnInput          = session.TurnInput
	TurnRef            = session.TurnRef
	TurnID             = session.TurnID
	PermissionDecision = session.PermissionDecision
	ExecResult         = session.ExecResult
	IdleStatus         = session.IdleStatus
)

// RunnerClient is the live connection to a session's in-pod runner: start and
// interrupt turns, resolve permissions, run one-shot commands, and stream the
// normalized event model. It is the narrow public surface exposed by
// Connection.Runner and Session.Runner() — deliberately an interface, not the
// concrete engine type, so the package stays a thin façade and the runner's
// internal signatures aren't frozen as public API.
type RunnerClient interface {
	Health(ctx context.Context) error
	StartTurn(ctx context.Context, ref Ref, input TurnInput) (TurnRef, error)
	InterruptTurn(ctx context.Context, ref Ref, turn TurnRef) error
	ResolvePermission(ctx context.Context, ref Ref, decision PermissionDecision) error
	Events(ctx context.Context, ref Ref, afterSeq uint64) (<-chan Event, error)
	EventsPassive(ctx context.Context, ref Ref, afterSeq uint64) (<-chan Event, error)
	SessionState(ctx context.Context, ref Ref) (State, error)
	Exec(ctx context.Context, ref Ref, command string) (ExecResult, error)
	Idle(ctx context.Context, ref Ref) (IdleStatus, error)
}

// The concrete runner client satisfies the public interface.
var _ RunnerClient = (*runner.Client)(nil)

// Backend is the cluster-side seam the SDK orchestration depends on: exactly the
// Sandbox/PVC lifecycle, port-forward, reaper, and credential operations that
// Create, Session.Connect, Suspend/Resume/Destroy, and DialRunner call — no
// more. Narrowing Client's dependency to this interface (rather than the
// concrete *internal/k8s.Backend) is what lets those orchestration paths be
// unit-tested against an injected fake (see WithBackend). *internal/k8s.Backend
// is the production implementation.
//
// Like WithBackend, this is not implementable by external modules today:
// EnsureReaper's k8s.ReaperOptions cannot be named outside the main module
// (tracked in TODO.md §8) — the seam's present value is in-module fake
// injection, not a third-party backend.
type Backend interface {
	// Namespace is the namespace this backend addresses.
	Namespace() string
	// CreateSession provisions the Sandbox + PVC for a spec (Create).
	CreateSession(ctx context.Context, spec Spec) (Ref, error)
	// Status / List report observed session state.
	Status(ctx context.Context, ref Ref) (State, error)
	List(ctx context.Context) ([]State, error)
	// Suspend / Resume / Destroy drive the pod lifecycle.
	Suspend(ctx context.Context, ref Ref) error
	Resume(ctx context.Context, ref Ref) error
	Destroy(ctx context.Context, ref Ref) error
	// StartWithProgress blocks until the pod is ready, reporting phase detail.
	StartWithProgress(ctx context.Context, ref Ref, onPhase func(detail string)) error
	// PortForward opens the requested local→pod forwards.
	PortForward(ctx context.Context, ref Ref, ports []session.PortSpec) ([]session.ForwardHandle, error)
	// RunnerToken / OpencodePassword fetch per-session secrets Connect needs.
	RunnerToken(ctx context.Context, ref Ref) (string, error)
	OpencodePassword(ctx context.Context, ref Ref) (string, error)
	// EnsureReaper installs the idle reaper (Connect's background phase).
	EnsureReaper(ctx context.Context, ref Ref, opts k8s.ReaperOptions) error
}

// The concrete k8s backend satisfies the narrowed public interface.
var _ Backend = (*k8s.Backend)(nil)

// Backend identifiers and lifecycle statuses, re-exported for convenience.
const (
	BackendClaudeSDK = session.BackendClaudeSDK
	BackendOpenCode  = session.BackendOpenCode

	StatusUnknown   = session.StatusUnknown
	StatusCreating  = session.StatusCreating
	StatusRunning   = session.StatusRunning
	StatusSuspended = session.StatusSuspended
	StatusFailed    = session.StatusFailed
	StatusGone      = session.StatusGone
)

const (
	// DefaultRunnerImage is the runner container image used when CreateOptions /
	// WithRunnerImage does not specify one.
	DefaultRunnerImage = "ghcr.io/cullenmcdermott/sandbox-claude-runner:latest"

	// DefaultIdleTimeout is the idle-reaper's default suspend window.
	DefaultIdleTimeout = 15 * time.Minute

	// remoteClaudeDir mirrors the runner's CLAUDE_CONFIG_DIR for Mutagen sync.
	remoteClaudeDir = "/session/state/claude"
)

// options collects New's configuration before it builds a Client.
type options struct {
	namespace        string
	kubeconfigPath   string
	contextName      string
	restConfig       *rest.Config
	stateDir         string
	runnerImage      string
	reaperImage      string
	reaperPullPolicy string
	idleTimeout      time.Duration
	backend          Backend
}

// Option configures a Client built by New.
type Option func(*options)

// WithNamespace sets the Kubernetes namespace for sessions (default
// "agent-sessions").
func WithNamespace(ns string) Option { return func(o *options) { o.namespace = ns } }

// WithKubeconfig targets an explicit kubeconfig path (skips the in-cluster probe).
func WithKubeconfig(path string) Option { return func(o *options) { o.kubeconfigPath = path } }

// WithContext selects a named kubeconfig context (skips the in-cluster probe).
func WithContext(name string) Option { return func(o *options) { o.contextName = name } }

// WithRESTConfig injects a pre-built *rest.Config, bypassing kubeconfig loading.
func WithRESTConfig(rc *rest.Config) Option { return func(o *options) { o.restConfig = rc } }

// WithStateDir overrides the local state directory (session index + per-session
// SSH keys). Defaults to ~/.local/share/sandbox/remote-sessions.
//
// Note the per-session SSH alias config is written to an "ssh" directory that
// is a SIBLING of the state dir (dir(stateDir)/ssh/config — for the default,
// ~/.local/share/sandbox/ssh/config) and an Include line for it is added to
// ~/.ssh/config on the first Connect. Point WithStateDir at a dedicated
// subdirectory (e.g. <appdir>/remote-sessions) so the ssh dir lands inside
// your app's directory rather than beside it.
func WithStateDir(dir string) Option { return func(o *options) { o.stateDir = dir } }

// WithRunnerImage sets the default runner image for Create (overridable per call).
func WithRunnerImage(img string) Option { return func(o *options) { o.runnerImage = img } }

// WithReaperImage sets the default idle-reaper image (overridable per Connect).
func WithReaperImage(img string) Option { return func(o *options) { o.reaperImage = img } }

// WithReaperImagePullPolicy sets the default imagePullPolicy for the idle-reaper
// Job's container (overridable per Connect). Case-sensitive; must be exactly
// "Always", "IfNotPresent", or "Never". Empty derives the policy from the image
// ref (IfNotPresent for digest-pinned, else Always). Needed for side-loaded
// reaper images, where a tagged ref would otherwise resolve to Always and the
// kubelet's pull could never succeed.
func WithReaperImagePullPolicy(p string) Option {
	return func(o *options) { o.reaperPullPolicy = p }
}

// WithIdleTimeout sets the default idle window before the reaper suspends a
// session (overridable per Connect).
func WithIdleTimeout(d time.Duration) Option { return func(o *options) { o.idleTimeout = d } }

// WithBackend injects an already-built Backend, bypassing kubeconfig resolution
// (advanced/testing — reuse a shared backend, or inject a fake for orchestration
// unit tests).
func WithBackend(b Backend) Option { return func(o *options) { o.backend = b } }

// Client is the entry point: it owns the Kubernetes backend, the local session
// index, and default image/idle settings, and mints Sessions.
type Client struct {
	backend          Backend
	index            *index.Index
	stateDir         string
	runnerImage      string
	reaperImage      string
	reaperPullPolicy string
	idleTimeout      time.Duration

	// syncRunner backs syncManager(). Nil in production (syncManager defaults it
	// to the mutagen-CLI runner); a test injects a fake to observe/stub the
	// Mutagen calls the orchestration paths make.
	syncRunner syncpkg.Runner
}

// New builds a Client. With no options it loads kubeconfig from the standard
// locations (in-cluster first, then KUBECONFIG/~/.kube/config) and uses the
// default namespace and state directory.
func New(opts ...Option) (*Client, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	// Fail fast at construction rather than at first Connect.
	if err := validateImagePullPolicy(o.reaperPullPolicy); err != nil {
		return nil, err
	}

	backend := o.backend
	if backend == nil {
		var kopts []k8s.Option
		if o.kubeconfigPath != "" {
			kopts = append(kopts, k8s.WithKubeconfig(o.kubeconfigPath))
		}
		if o.contextName != "" {
			kopts = append(kopts, k8s.WithContext(o.contextName))
		}
		if o.restConfig != nil {
			kopts = append(kopts, k8s.WithRESTConfig(o.restConfig))
		}
		b, err := k8s.New(o.namespace, kopts...)
		if err != nil {
			return nil, err
		}
		backend = b
	}

	stateDir := o.stateDir
	if stateDir == "" {
		root, err := index.DefaultRoot()
		if err != nil {
			return nil, fmt.Errorf("sandbox: resolve state dir: %w", err)
		}
		stateDir = root
	}

	runnerImage := o.runnerImage
	if runnerImage == "" {
		runnerImage = DefaultRunnerImage
	}
	reaperImage := o.reaperImage
	if reaperImage == "" {
		reaperImage = k8s.DefaultReaperImage
	}
	// idleTimeout stays 0 when WithIdleTimeout was not used: Connect needs to
	// distinguish "explicitly configured" (beats the SANDBOX_REAPER_IDLE_TIMEOUT
	// test hook) from "defaulted" (the hook applies).
	return &Client{
		backend:          backend,
		index:            index.New(stateDir),
		stateDir:         stateDir,
		runnerImage:      runnerImage,
		reaperImage:      reaperImage,
		reaperPullPolicy: o.reaperPullPolicy,
		idleTimeout:      o.idleTimeout,
	}, nil
}

// Namespace returns the namespace this client addresses.
func (c *Client) Namespace() string { return c.backend.Namespace() }

// StateDir returns the resolved local state directory.
func (c *Client) StateDir() string { return c.stateDir }

// CreateOptions parameterizes Create.
type CreateOptions struct {
	// Backend selects the agent backend (default "claude-sdk").
	Backend string
	// ProjectPath is the absolute workspace path mirrored into the pod. Required.
	ProjectPath string
	// RunnerImage overrides the client default runner image.
	RunnerImage string
	// ImagePullPolicy overrides the runner pod's imagePullPolicy. Case-sensitive;
	// must be exactly "Always", "IfNotPresent", or "Never". Empty auto-selects
	// (IfNotPresent for digest-pinned refs, else Always).
	ImagePullPolicy string
	// Model is an optional session-default model id/alias.
	Model string
	// AnthropicAuth selects the Anthropic credential TYPE for a claude-sdk
	// session: ""/"oauth" uses the subscription OAuth token; "api-key" uses the
	// Console API key. Any other value is rejected with ErrInvalidAnthropicAuth.
	// It alone drives which env var the pod's credential lands under
	// (CLAUDE_CODE_OAUTH_TOKEN vs ANTHROPIC_API_KEY) — including on the
	// account path — so a caller setting AnthropicAccountID MUST set this to
	// match the account's type (see AnthropicAccountID). Ignored by non-claude
	// backends.
	AnthropicAuth string
	// AnthropicAccountID names the stored Anthropic account this session runs on
	// (see client/cred). When set, AnthropicCredential MUST hold the resolved
	// bytes for that account — the caller (CLI/TUI) resolves account → bytes
	// before calling Create; the client layer only carries and writes them.
	// Setting the id without bytes fails closed with ErrAnthropicCredentialMissing
	// rather than falling back to the shared cluster Secret. The caller MUST also
	// set AnthropicAuth to match the account's type ("oauth" for subscription
	// accounts, "api-key" for console) — env-var selection is driven solely by
	// AnthropicAuth and the account's type is not visible at this layer, so the
	// correlation cannot be validated here: forgetting it lands the right bytes
	// under the wrong env var with no error. client/cred's
	// AuthForType(account.Type) is the canonical way to derive it. Must be a
	// valid Kubernetes label value (the id labels the per-session Secret;
	// guaranteed by the cred store) — else ErrInvalidAnthropicAccountID. Empty
	// selects the shared-Secret fallback (backward-compatible). Ignored by
	// non-claude backends.
	AnthropicAccountID string
	// AnthropicCredential is the resolved secret bytes (OAuth token or Console
	// API key) for AnthropicAccountID. Never serialized (json:"-" keeps a
	// consumer's debug json.Marshal of the options from leaking it); provisioned
	// into the per-session Secret and surfaced to the pod as a SecretKeyRef env
	// var only. Bytes without an AnthropicAccountID are rejected with
	// ErrAnthropicAccountRequired. Ignored by non-claude backends.
	AnthropicCredential []byte `json:"-"`
	// OpencodeProvider selects which SINGLE model-provider API key an
	// opencode-server session's pod receives from the shared opencode-credentials
	// Secret (fail-closed — the pod refuses to start if the selected provider's
	// key is absent). One of session.OpencodeProviderAnthropic,
	// session.OpencodeProviderOpenAI, session.OpencodeProviderZen. Empty keeps
	// the documented default (Anthropic); any OTHER value is rejected with
	// ErrInvalidOpencodeProvider rather than silently defaulting (a typo must
	// not select a different provider's credential). Ignored by non-opencode
	// backends.
	OpencodeProvider string
	// StorageClass is the PVC storage class (empty uses the cluster default).
	StorageClass string
	// StorageGiB is the PVC size in GiB (0 uses the backend default, 50).
	StorageGiB int
	// ID optionally pins the session id (for idempotent create — re-creating with
	// the same ID is a no-op at the cluster layer). Empty mints a fresh unique id.
	ID ID
}

// Create provisions a new session: it mints an id (unless ID is set), prepares
// the per-session SSH key, creates the Sandbox + PVC, and records the session in
// the local index. It does NOT wait for the pod or connect — call
// Session.Connect for that.
func (c *Client) Create(ctx context.Context, opt CreateOptions) (*Session, error) {
	// §10 observability: time create end-to-end (and the ssh-key + cluster-create
	// phases below) under one correlation id. tr is nil unless SANDBOX_TRACE is
	// set, so this is a no-op when off.
	tr := newTracer()
	defer tr.start("create.total").end()
	backendName := opt.Backend
	if backendName == "" {
		backendName = session.BackendClaudeSDK
	}
	if opt.ProjectPath == "" {
		return nil, ErrProjectPathRequired
	}
	if err := validateImagePullPolicy(opt.ImagePullPolicy); err != nil {
		return nil, err
	}
	if err := validateAnthropicAuth(opt.AnthropicAuth); err != nil {
		return nil, err
	}
	if err := validateOpencodeProvider(opt.OpencodeProvider); err != nil {
		return nil, err
	}
	// Fail closed on account/credential mismatch (a design-review requirement):
	// a named account with no resolved bytes must NOT silently fall back to the
	// shared cluster Secret, and bytes with no account id would provision an
	// unlabeled, unenumerable Secret. Both are rejected before any cluster call.
	if err := validateAnthropicAccount(opt.AnthropicAccountID, opt.AnthropicCredential); err != nil {
		return nil, err
	}
	runnerImage := opt.RunnerImage
	if runnerImage == "" {
		runnerImage = c.runnerImage
	}

	sid := opt.ID
	if sid == "" {
		s, err := NewID(backendName, opt.ProjectPath)
		if err != nil {
			return nil, err
		}
		sid = s
	}

	// Prepare the per-session SSH key for Mutagen before creating the pod, since
	// its public half is baked into the session Secret. The private-key path is
	// stamped onto the returned Session so the first Connect can reuse it instead
	// of re-deriving the key (§5).
	keySpan := tr.start("create.ssh_key")
	privPath, authKey, err := c.ensureSSHKey(string(sid))
	keySpan.end()
	if err != nil {
		return nil, fmt.Errorf("prepare ssh key: %w", err)
	}

	spec := session.Spec{
		ID:                  sid,
		ProjectPath:         opt.ProjectPath,
		Backend:             backendName,
		RunnerImage:         runnerImage,
		ImagePullPolicy:     opt.ImagePullPolicy,
		SSHPublicKey:        authKey,
		Model:               opt.Model,
		AnthropicAuth:       opt.AnthropicAuth,
		AnthropicAccountID:  opt.AnthropicAccountID,
		AnthropicCredential: opt.AnthropicCredential,
		OpencodeProvider:    opt.OpencodeProvider,
		StorageClass:        opt.StorageClass,
		StorageGiB:          opt.StorageGiB,
	}

	createSpan := tr.start("create.session")
	ref, err := c.backend.CreateSession(ctx, spec)
	createSpan.end()
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Record locally so status/reconnect can find it even once it is gone from
	// the cluster.
	now := time.Now()
	_ = c.index.Save(string(sid), index.Entry{
		SandboxSessionID: string(sid),
		Backend:          backendName,
		ProjectPath:      opt.ProjectPath,
		Namespace:        c.backend.Namespace(),
		SandboxName:      string(sid),
		CreatedAt:        now,
		LastActivity:     now,
	})

	// Stamp the fresh-path shortcuts so the first Connect skips the redundant
	// cluster Status Get and SSH-key regeneration this call already performed (§5).
	sess := c.newSession(ref, opt.ProjectPath)
	sess.fresh = true
	sess.freshBackend = backendName
	sess.sshPrivPath = privPath
	return sess, nil
}

// Open returns a Session handle for an existing session id. It performs no I/O;
// call Connect to establish the connection (which discovers the project path from
// cluster status if not already known).
func (c *Client) Open(id ID) *Session { return c.newSession(Ref{ID: id}, "") }

func (c *Client) newSession(ref Ref, projectPath string) *Session {
	return &Session{c: c, ref: ref, projectPath: projectPath}
}

// Status returns the observed state of a session.
func (c *Client) Status(ctx context.Context, id ID) (State, error) {
	return c.backend.Status(ctx, Ref{ID: id})
}

// List returns the observed state of all sessions in the namespace.
func (c *Client) List(ctx context.Context) ([]State, error) { return c.backend.List(ctx) }

// Suspend suspends a session (terminate pod, keep PVC) and pauses its file sync.
func (c *Client) Suspend(ctx context.Context, id ID) error {
	if err := c.backend.Suspend(ctx, Ref{ID: id}); err != nil {
		return err
	}
	// Best-effort: the pod (and SSH forward) is gone while suspended, so leaving
	// sync running would thrash a dead transport. Resume re-enables it.
	_ = c.syncManager().PauseAll(ctx, string(id))
	return nil
}

// Resume resumes a suspended session and resumes its file sync.
func (c *Client) Resume(ctx context.Context, id ID) error {
	if err := c.backend.Resume(ctx, Ref{ID: id}); err != nil {
		return err
	}
	_ = c.syncManager().ResumeAll(ctx, string(id))
	return nil
}

// Destroy stops the session's file sync, destroys the session and its PVC
// (irreversible), then removes local state (SSH alias, key dir, index entry).
//
// Sync is stopped BEFORE the cluster destroy (mirroring the TUI's
// PreDestroyHook ordering): the mutagen-over-SSH stream must be torn down while
// the pod is still up, or it races the pod's disappearance into "connection
// closed"/EOF errors and leaves orphaned mutagen sessions pointing at a dead
// endpoint. Best-effort and recoverable, so it runs before — not gated on — the
// destroy.
func (c *Client) Destroy(ctx context.Context, id ID) error {
	c.StopSync(ctx, id)
	if err := c.backend.Destroy(ctx, Ref{ID: id}); err != nil {
		return err
	}
	c.RemoveLocalState(id)
	return nil
}

// DialRunner opens a port-forward to the session's runner pod and returns a
// connected client plus a cleanup func that tears the forward down. For one-shot
// runner calls (e.g. reading session state) outside a full Connect.
func (c *Client) DialRunner(ctx context.Context, ref Ref) (RunnerClient, func(), error) {
	// One-shot runner calls only speak HTTP; the SSH forward exists solely for
	// mutagen sync, which DialRunner never runs — so forward the runner port only
	// rather than paying for an unused SSH SPDY stream.
	handles, err := c.backend.PortForward(ctx, ref, k8s.ForwardSpecsRunnerOnly(0))
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		for _, h := range handles {
			h.Close()
		}
	}
	token, err := c.backend.RunnerToken(ctx, ref)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("get runner token: %w", err)
	}
	rc := runner.New(fmt.Sprintf("http://127.0.0.1:%d", handles[0].LocalPort()), token)
	return rc, cleanup, nil
}

// NewID mints a fresh, unique session id for a project: the backend name, a short
// hash of the project path (so sessions are grouped by project at a glance), and
// a random suffix that guarantees uniqueness. The result is a valid Kubernetes
// DNS label for any backend/projectPath input: the backend part is sanitized,
// trimmed, defaulted ("session") when it sanitizes away entirely, and truncated
// so the id stays within the 63-character label limit.
func NewID(backend, projectPath string) (ID, error) {
	sum := sha256.Sum256([]byte(projectPath))
	pathHash := hex.EncodeToString(sum[:])[:6]

	rnd := make([]byte, 4)
	if _, err := rand.Read(rnd); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}

	// A DNS label is at most 63 chars and must start/end alphanumeric. The
	// fixed suffix ("-" + 6 hash + "-" + 8 random) leaves 47 for the backend.
	prefix := strings.Trim(sanitizeLabel(backend), "-")
	if len(prefix) > 47 {
		prefix = strings.TrimRight(prefix[:47], "-")
	}
	if prefix == "" {
		prefix = "session"
	}
	return ID(prefix + "-" + pathHash + "-" + hex.EncodeToString(rnd)), nil
}

// validateImagePullPolicy rejects a non-empty override that isn't one of the
// exact, case-sensitive corev1 spellings — otherwise a typo like "ifnotpresent"
// would silently fall through to the auto policy (the opposite of intent).
func validateImagePullPolicy(p string) error {
	switch p {
	case "", "Always", "IfNotPresent", "Never":
		return nil
	default:
		return fmt.Errorf("%w: %q (must be \"Always\", \"IfNotPresent\", or \"Never\")", ErrInvalidImagePullPolicy, p)
	}
}

// validateAnthropicAuth rejects a non-empty AnthropicAuth that isn't one of the
// exact spellings "oauth" or "api-key" — otherwise a typo like "apikey" would
// silently fall through to the default OAuth path (the opposite of intent).
func validateAnthropicAuth(a string) error {
	switch a {
	case "", "oauth", "api-key":
		return nil
	default:
		return fmt.Errorf("%w: %q (must be \"oauth\" or \"api-key\")", ErrInvalidAnthropicAuth, a)
	}
}

// validateOpencodeProvider rejects a non-empty OpencodeProvider that isn't one
// of the exact session.OpencodeProvider* spellings — otherwise a typo like
// "zen" would silently fall through to the backend's Anthropic default and
// select a DIFFERENT provider's credential than the caller intended (the §7a
// "validate, not default" contract for the user-facing selector).
func validateOpencodeProvider(p string) error {
	switch p {
	case "", session.OpencodeProviderAnthropic, session.OpencodeProviderOpenAI, session.OpencodeProviderZen:
		return nil
	default:
		return fmt.Errorf("%w: %q (must be %q, %q, or %q)", ErrInvalidOpencodeProvider,
			p, session.OpencodeProviderAnthropic, session.OpencodeProviderOpenAI, session.OpencodeProviderZen)
	}
}

// validateAnthropicAccount enforces the fail-closed account/credential contract:
// a named account requires resolved credential bytes (else the session would
// silently launch on the shared Secret with the wrong or no account), and
// credential bytes require a naming account (else the per-session Secret would
// be unlabeled and unenumerable for rotation/logout). The account id must also
// be a valid Kubernetes label value — it labels the per-session Secret — so an
// invalid id fails fast here with a clear sentinel instead of surfacing as an
// apiserver Invalid error mid-create (which would trip the backend's failure
// handling). The empty/empty case is the shared-Secret fallback and is allowed.
// It never inspects or echoes the credential bytes.
func validateAnthropicAccount(accountID string, credential []byte) error {
	switch {
	case accountID != "" && len(credential) == 0:
		return ErrAnthropicCredentialMissing
	case accountID == "" && len(credential) > 0:
		return ErrAnthropicAccountRequired
	}
	if accountID != "" {
		if errs := validation.IsValidLabelValue(accountID); len(errs) > 0 {
			return fmt.Errorf("%w: %q (%s)", ErrInvalidAnthropicAccountID, accountID, strings.Join(errs, "; "))
		}
	}
	return nil
}

// sanitizeLabel lowercases and replaces any non-[a-z0-9-] rune with '-' so the
// value is safe in a Kubernetes resource name.
func sanitizeLabel(s string) string {
	b := make([]byte, 0, len(s))
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z':
			b = append(b, byte(c-'A'+'a'))
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-':
			b = append(b, byte(c))
		default:
			b = append(b, '-')
		}
	}
	return string(b)
}

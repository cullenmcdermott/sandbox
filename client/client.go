package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	"github.com/cullenmcdermott/sandbox/internal/index"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
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
	PortSpec           = session.PortSpec
	ForwardHandle      = session.ForwardHandle
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
	backend          *k8s.Backend
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

// WithBackend injects an already-built *Backend, bypassing kubeconfig resolution
// (advanced/testing — reuse a shared backend).
func WithBackend(b *k8s.Backend) Option { return func(o *options) { o.backend = b } }

// Client is the entry point: it owns the Kubernetes backend, the local session
// index, and default image/idle settings, and mints Sessions.
type Client struct {
	backend          *k8s.Backend
	index            *index.Index
	stateDir         string
	runnerImage      string
	reaperImage      string
	reaperPullPolicy string
	idleTimeout      time.Duration
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
	// its public half is baked into the session Secret.
	_, authKey, err := c.ensureSSHKey(string(sid))
	if err != nil {
		return nil, fmt.Errorf("prepare ssh key: %w", err)
	}

	spec := session.Spec{
		ID:              sid,
		ProjectPath:     opt.ProjectPath,
		Backend:         backendName,
		RunnerImage:     runnerImage,
		ImagePullPolicy: opt.ImagePullPolicy,
		SSHPublicKey:    authKey,
		Model:           opt.Model,
		StorageClass:    opt.StorageClass,
		StorageGiB:      opt.StorageGiB,
	}

	ref, err := c.backend.CreateSession(ctx, spec)
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

	return c.newSession(ref, opt.ProjectPath), nil
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

// Destroy destroys a session and its PVC (irreversible), then tears down its
// file sync and removes local state (SSH alias, key dir, index entry).
func (c *Client) Destroy(ctx context.Context, id ID) error {
	if err := c.backend.Destroy(ctx, Ref{ID: id}); err != nil {
		return err
	}
	c.StopSync(ctx, id)
	c.RemoveLocalState(id)
	return nil
}

// DialRunner opens a port-forward to the session's runner pod and returns a
// connected client plus a cleanup func that tears the forward down. For one-shot
// runner calls (e.g. reading session state) outside a full Connect.
func (c *Client) DialRunner(ctx context.Context, ref Ref) (RunnerClient, func(), error) {
	handles, err := c.backend.PortForward(ctx, ref, k8s.ForwardSpecs(0, 0))
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

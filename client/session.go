package client

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Stage is a coarse connect phase, reported via ConnectOptions.OnPhase so a
// caller (e.g. a TUI splash) can show live progress through a cold-pod resume +
// image pull + file sync.
type Stage int

const (
	StageCheck    Stage = iota // checking session status
	StageResume                // resuming a suspended pod / waiting for ready
	StageForward               // establishing the port-forward
	StageRunner                // waiting for the runner to be healthy
	StageSync                  // setting up / flushing file sync
	StageOpencode              // waiting for `opencode serve` to answer
	StageAttach                // connected
)

// String returns a short token for the stage.
func (s Stage) String() string {
	switch s {
	case StageCheck:
		return "check"
	case StageResume:
		return "resume"
	case StageForward:
		return "forward"
	case StageRunner:
		return "runner"
	case StageSync:
		return "sync"
	case StageOpencode:
		return "opencode"
	case StageAttach:
		return "attach"
	default:
		return "unknown"
	}
}

// OpencodeCreds holds the local endpoint and HTTP basic-auth credentials for an
// opencode-server session (nil for claude-sdk sessions).
type OpencodeCreds struct {
	Username string
	Password string
	URL      string // e.g. http://127.0.0.1:4096
}

// Connection is the successful outcome of Session.Connect: a live runner client,
// the local runner endpoint, the resolved backend, and any opencode credentials.
type Connection struct {
	Runner   RunnerClient
	Endpoint string         // runner HTTP base URL
	Backend  string         // resolved backend id
	Opencode *OpencodeCreds // nil for claude-sdk
	// Warning is a non-fatal advisory (e.g. file sync failed) the caller should
	// surface rather than discard.
	Warning string
}

// ConnectOptions parameterizes Session.Connect.
type ConnectOptions struct {
	// ProjectPath overrides the project path used for file sync. Empty discovers
	// it from cluster status.
	ProjectPath string
	// ReaperImage overrides the idle-reaper image (empty => client default).
	ReaperImage string
	// ReaperImagePullPolicy overrides the idle-reaper imagePullPolicy (empty =>
	// client default). Case-sensitive; must be exactly "Always", "IfNotPresent",
	// or "Never".
	ReaperImagePullPolicy string
	// IdleTimeout overrides the reaper idle window (0 => client default).
	IdleTimeout time.Duration
	// Observer requests a lightweight, read-only connection: port-forward +
	// runner health only, skipping file-sync setup, the idle-reaper ensure, and
	// the opencode readiness wait. It never mutates the cluster: connecting to a
	// suspended session fails with ErrSessionSuspended instead of resuming it.
	// Used for background status streams.
	Observer bool
	// OnPhase receives coarse connect progress (nil => ignored).
	OnPhase func(Stage, string)
}

// Session is a handle to a single remote session. It owns the port-forward and
// can be (re)connected: calling Connect again resumes the pod if needed,
// re-forwards, and returns a fresh client — prior forwards are closed.
type Session struct {
	c           *Client
	ref         Ref
	projectPath string

	mu      sync.Mutex
	handles []session.ForwardHandle
	runner  *runner.Client
}

// ID returns the session id.
func (s *Session) ID() ID { return s.ref.ID }

// Ref returns the session ref.
func (s *Session) Ref() Ref { return s.ref }

// ProjectPath returns the project path (known after Create or a successful
// Connect).
func (s *Session) ProjectPath() string { return s.projectPath }

// Runner returns the live runner client from the last successful Connect, or nil
// if not connected (or after Close). The explicit nil return avoids handing back
// a typed-nil interface.
func (s *Session) Runner() RunnerClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runner == nil {
		return nil
	}
	return s.runner
}

// Connect establishes (or re-establishes) a live runner connection: resume the
// pod if suspended, wait for it to be ready, port-forward, wait for runner
// health, and — unless Observer is set — set up file sync, ensure the idle
// reaper, and (for opencode sessions) wait for `opencode serve`. Safe to call
// repeatedly; prior port-forwards are closed.
func (s *Session) Connect(ctx context.Context, opt ConnectOptions) (*Connection, error) {
	if err := validateImagePullPolicy(opt.ReaperImagePullPolicy); err != nil {
		return nil, err
	}
	s.closeHandles()
	onPhase := opt.OnPhase
	if onPhase == nil {
		onPhase = func(Stage, string) {}
	}
	stage := func(st Stage) { onPhase(st, "") }
	full := !opt.Observer

	stage(StageCheck)
	st, err := s.c.backend.Status(ctx, s.ref)
	if err != nil {
		return nil, err
	}
	if opt.ProjectPath != "" {
		s.projectPath = opt.ProjectPath
	} else if s.projectPath == "" {
		s.projectPath = st.ProjectPath
	}

	switch st.Status {
	case session.StatusGone:
		return nil, fmt.Errorf("session %s: %w", s.ref.ID, session.ErrSessionGone)
	case session.StatusSuspended:
		// Observer connects are read-only: resuming (a cluster mutation that
		// defeats the idle reaper) is a full-Connect decision. There is nothing
		// to observe on a suspended session anyway — its pod is gone.
		if opt.Observer {
			return nil, fmt.Errorf("session %s: %w", s.ref.ID, ErrSessionSuspended)
		}
		stage(StageResume)
		if err := s.c.backend.Resume(ctx, s.ref); err != nil {
			return nil, fmt.Errorf("resume: %w", err)
		}
	}

	// Block until the pod is running and ready before port-forwarding. For a
	// freshly created session the pod is still scheduling and pulling the image;
	// for a just-resumed one the new pod is booting. Observer streams only attach
	// to already-warm sessions, so they skip the explicit wait.
	if full {
		if err := s.c.backend.StartWithProgress(ctx, s.ref, func(detail string) {
			onPhase(StageResume, detail)
		}); err != nil {
			return nil, fmt.Errorf("wait for pod: %w", err)
		}
	}

	stage(StageForward)
	opencode := st.Backend == session.BackendOpenCode
	var handles []session.ForwardHandle
	switch {
	case opencode:
		handles, err = s.c.backend.PortForward(ctx, s.ref, k8s.ForwardSpecsWithOpencode(0, 0, 0))
	case !full:
		// Observer mode reads only the runner event stream and never runs mutagen
		// sync, so the SSH forward is pure waste — forward the runner HTTP port
		// only.
		handles, err = s.c.backend.PortForward(ctx, s.ref, k8s.ForwardSpecsRunnerOnly(0))
	default:
		handles, err = s.c.backend.PortForward(ctx, s.ref, k8s.ForwardSpecs(0, 0))
	}
	if err != nil {
		return nil, fmt.Errorf("port-forward: %w", err)
	}
	s.setHandles(handles)

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", handles[0].LocalPort())
	token, err := s.c.backend.RunnerToken(ctx, s.ref)
	if err != nil {
		// Tear down the forward on every post-forward failure: leaving it (and
		// the runner client) in place would leak the SPDY goroutines and make
		// Runner() hand back a client over an unproven transport after a failed
		// Connect.
		s.closeHandles()
		return nil, err
	}
	rc := runner.New(endpoint, token)
	s.setRunner(rc)
	stage(StageRunner)
	if err := waitHealthy(ctx, rc); err != nil {
		s.closeHandles()
		return nil, fmt.Errorf("runner health: %w", err)
	}

	var syncWarning string
	if full {
		stage(StageSync)
		reaperImage := opt.ReaperImage
		if reaperImage == "" {
			reaperImage = s.c.reaperImage
		}
		reaperPullPolicy := opt.ReaperImagePullPolicy
		if reaperPullPolicy == "" {
			reaperPullPolicy = s.c.reaperPullPolicy
		}
		// Reaper idle-window precedence: per-Connect option, then the client
		// default (WithIdleTimeout), then the SANDBOX_REAPER_IDLE_TIMEOUT test
		// hook, then the built-in default. The env hook must NOT override an
		// explicit programmatic choice.
		idleTimeout := opt.IdleTimeout
		if idleTimeout == 0 {
			idleTimeout = s.c.idleTimeout
		}
		if idleTimeout == 0 {
			if v := os.Getenv("SANDBOX_REAPER_IDLE_TIMEOUT"); v != "" {
				if d, derr := time.ParseDuration(v); derr == nil {
					idleTimeout = d
				}
			}
		}
		if idleTimeout == 0 {
			idleTimeout = DefaultIdleTimeout
		}

		privPath, _, kerr := s.c.ensureSSHKey(string(s.ref.ID))
		if kerr != nil {
			syncWarning = fmt.Sprintf("file sync unavailable (ssh key): %v", kerr)
		} else if created, serr := s.c.startMutagen(ctx, string(s.ref.ID), s.projectPath, privPath, handles[1].LocalPort()); serr != nil {
			syncWarning = fmt.Sprintf("file sync unavailable: %v", serr)
		} else if created {
			// First-ever sync: `mutagen sync create` returns before the transport
			// is proven or files have staged, so block on a bounded flush. A broken
			// transport errors fast (surfaced as a warning); a healthy-but-large
			// first sync may time out (just "still uploading in the background").
			flushCtx, cancelFlush := context.WithTimeout(ctx, 12*time.Second)
			ferr := s.flushWithProgress(flushCtx, onPhase)
			timedOut := flushCtx.Err() == context.DeadlineExceeded
			cancelFlush()
			switch {
			case ferr != nil && timedOut:
				syncWarning = "initial file sync still in progress (continuing in the background)"
			case ferr != nil:
				syncWarning = fmt.Sprintf("file sync error: %v", ferr)
			}
		} else {
			// Reconnect to an already-synced session: the mutagen session persists
			// and reconciles on its own, so don't block on a full flush. Kick a
			// detached flush so mutagen re-establishes the transport on the new
			// port-forward promptly.
			go func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = s.c.syncManager().FlushAll(bgCtx, string(s.ref.ID))
			}()
		}

		// Make sure the idle reaper is watching (a prior one may have completed
		// when the session was suspended).
		if w := s.c.ensureReaper(ctx, s.ref, reaperImage, reaperPullPolicy, idleTimeout); w != "" {
			if syncWarning != "" {
				syncWarning += "; " + w
			} else {
				syncWarning = w
			}
		}
	}

	conn := &Connection{
		Runner:   rc,
		Endpoint: endpoint,
		Backend:  st.Backend,
		Warning:  syncWarning,
	}
	if full && opencode {
		pass, perr := s.c.backend.OpencodePassword(ctx, s.ref)
		if perr != nil {
			s.closeHandles()
			return nil, fmt.Errorf("opencode password: %w", perr)
		}
		addr := fmt.Sprintf("127.0.0.1:%d", handles[2].LocalPort())
		stage(StageOpencode)
		if werr := waitOpencodeReady(ctx, "http://"+addr+"/"); werr != nil {
			s.closeHandles()
			return nil, fmt.Errorf("opencode serve not ready: %w", werr)
		}
		conn.Opencode = &OpencodeCreds{
			Username: k8s.OpencodeUsername(),
			Password: pass,
			URL:      "http://" + addr,
		}
	}
	stage(StageAttach)
	return conn, nil
}

// flushWithProgress runs the bounded initial sync flush while polling mutagen for
// a live staging phase, reporting it as a StageSync sub-detail. Returns the
// flush's own result (a timeout surfaces via ctx).
func (s *Session) flushWithProgress(ctx context.Context, onPhase func(Stage, string)) error {
	mgr := s.c.syncManager()
	id := string(s.ref.ID)
	done := make(chan error, 1)
	go func() { done <- mgr.FlushAll(ctx, id) }()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if phase := mgr.StagingPhase(ctx, id); phase != "" {
				onPhase(StageSync, phase)
			}
		}
	}
}

// Close tears down any active port-forwards. It does not destroy the session.
func (s *Session) Close() error {
	s.closeHandles()
	return nil
}

func (s *Session) setHandles(h []session.ForwardHandle) {
	s.mu.Lock()
	s.handles = h
	s.mu.Unlock()
}

func (s *Session) setRunner(rc *runner.Client) {
	s.mu.Lock()
	s.runner = rc
	s.mu.Unlock()
}

func (s *Session) closeHandles() {
	s.mu.Lock()
	handles := s.handles
	s.handles = nil
	// Clear the runner alongside its transport so the convenience methods report
	// ErrNotConnected after Close (or a failed reconnect) instead of handing back
	// a client whose port-forward is gone.
	s.runner = nil
	s.mu.Unlock()
	for _, h := range handles {
		h.Close()
	}
}

// --- Turn / stream convenience methods (delegating to the connected runner) ---

// StartTurn starts a turn. Requires a prior successful Connect.
func (s *Session) StartTurn(ctx context.Context, in TurnInput) (TurnRef, error) {
	rc := s.Runner()
	if rc == nil {
		return TurnRef{}, ErrNotConnected
	}
	return rc.StartTurn(ctx, s.ref, in)
}

// Interrupt interrupts a specific turn.
func (s *Session) Interrupt(ctx context.Context, turn TurnRef) error {
	rc := s.Runner()
	if rc == nil {
		return ErrNotConnected
	}
	return rc.InterruptTurn(ctx, s.ref, turn)
}

// CancelTurn interrupts the session's active turn, or returns ErrNoActiveTurn.
func (s *Session) CancelTurn(ctx context.Context) error {
	rc := s.Runner()
	if rc == nil {
		return ErrNotConnected
	}
	st, err := rc.SessionState(ctx, s.ref)
	if err != nil {
		return err
	}
	if st.ActiveTurnID == "" {
		return ErrNoActiveTurn
	}
	return rc.InterruptTurn(ctx, s.ref, TurnRef{Session: s.ref.ID, Turn: st.ActiveTurnID})
}

// ResolvePermission answers a pending permission request.
func (s *Session) ResolvePermission(ctx context.Context, decision PermissionDecision) error {
	rc := s.Runner()
	if rc == nil {
		return ErrNotConnected
	}
	return rc.ResolvePermission(ctx, s.ref, decision)
}

// Events opens the session's SSE event stream, replaying from afterSeq (0 for
// the full history).
func (s *Session) Events(ctx context.Context, afterSeq uint64) (<-chan Event, error) {
	rc := s.Runner()
	if rc == nil {
		return nil, ErrNotConnected
	}
	return rc.Events(ctx, s.ref, afterSeq)
}

// EventsPassive opens a status-observer stream that does not count as an attached
// client for idle detection.
func (s *Session) EventsPassive(ctx context.Context, afterSeq uint64) (<-chan Event, error) {
	rc := s.Runner()
	if rc == nil {
		return nil, ErrNotConnected
	}
	return rc.EventsPassive(ctx, s.ref, afterSeq)
}

// SessionState reads the runner's view of the session state.
func (s *Session) SessionState(ctx context.Context) (State, error) {
	rc := s.Runner()
	if rc == nil {
		return State{}, ErrNotConnected
	}
	return rc.SessionState(ctx, s.ref)
}

// Exec runs a one-shot shell command in the session cwd.
func (s *Session) Exec(ctx context.Context, command string) (ExecResult, error) {
	rc := s.Runner()
	if rc == nil {
		return ExecResult{}, ErrNotConnected
	}
	return rc.Exec(ctx, s.ref, command)
}

// Idle reports whether the session is idle (and since when).
func (s *Session) Idle(ctx context.Context) (IdleStatus, error) {
	rc := s.Runner()
	if rc == nil {
		return IdleStatus{}, ErrNotConnected
	}
	return rc.Idle(ctx, s.ref)
}

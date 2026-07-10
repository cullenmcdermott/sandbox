package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
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

	// fresh/freshBackend/sshPrivPath are shortcuts stamped by Client.Create for a
	// just-created session so its first Connect can skip the redundant cluster
	// Status Get and SSH-key regeneration Create already performed (§5). fresh is
	// consumed (cleared) on the first Connect; a later reconnect re-Statuses like
	// any attach.
	fresh        bool
	freshBackend string
	sshPrivPath  string

	// bgCancel cancels the context rooting Connect's post-health background work
	// (config/transcript sync creation, the bounded first-sync flush, the idle
	// reaper); closeHandles cancels it so the goroutine can't outlive the session.
	// syncTask is the observable handle to that work (see AwaitSync).
	bgCancel context.CancelFunc
	syncTask *syncTask
}

// syncTask is the handle to Connect's background file-sync + idle-reaper work.
// Connect returns before it finishes (so the transcript opens as soon as the
// runner is healthy, §5); a caller gates the first turn on it and collects any
// late advisory via Session.AwaitSync. done closes when the work settles; warning
// holds the joined advisory (empty on clean success).
type syncTask struct {
	done    chan struct{}
	mu      sync.Mutex
	warning string
}

func (t *syncTask) finish(warning string) {
	t.mu.Lock()
	t.warning = warning
	t.mu.Unlock()
	close(t.done)
}

func (t *syncTask) result() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.warning
}

// ID returns the session id.
func (s *Session) ID() ID { return s.ref.ID }

// Ref returns the session ref.
func (s *Session) Ref() Ref { return s.ref }

// ProjectPath returns the project path (known after Create or a successful
// Connect).
func (s *Session) ProjectPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.projectPath
}

// Runner returns the live runner client from the last successful Connect, or nil
// if not connected (or after Close). The explicit nil return avoids handing back
// a typed-nil interface. The returned client gates StartTurn on the background
// first-sync staging (see stagedRunner).
func (s *Session) Runner() RunnerClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runner == nil {
		return nil
	}
	return &stagedRunner{RunnerClient: s.runner, s: s}
}

// stagedRunner wraps the connected runner client so StartTurn waits for the
// session's background sync/staging work (AwaitSync) before submitting a turn:
// Connect no longer blocks on the initial project flush (§5), but a turn must
// not reach the agent before the workspace is staged. Every other method —
// health, interrupts, permission decisions, SSE streams — passes through
// ungated. AwaitSync returns immediately once the background work has settled
// (and when none is in flight), so the gate costs nothing in steady state.
type stagedRunner struct {
	RunnerClient
	s *Session
}

func (g *stagedRunner) StartTurn(ctx context.Context, ref Ref, in TurnInput) (TurnRef, error) {
	// The advisory is intentionally dropped here — warnings surface via the
	// caller's own AwaitSync (e.g. the dashboard connector); the gate only
	// cares that staging has settled.
	if _, err := g.s.AwaitSync(ctx); err != nil {
		return TurnRef{}, err
	}
	return g.RunnerClient.StartTurn(ctx, ref, in)
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
	// §10 observability: time the whole connect flow (and each phase below) under
	// one correlation id. tr is nil unless SANDBOX_TRACE is set, so this costs
	// ~nothing when off; the total span fires on every return path (incl. errors).
	tr := newTracer()
	defer tr.start("connect.total").end()
	onPhase := opt.OnPhase
	if onPhase == nil {
		onPhase = func(Stage, string) {}
	}
	stage := func(st Stage) { onPhase(st, "") }
	full := !opt.Observer

	// Consume the freshly-created shortcuts (see Session.fresh): a session
	// straight out of Client.Create is known to exist, be non-suspended, and
	// carry a known backend + project path + SSH key, so its first Connect can
	// skip the redundant Status Get and SSH-key regeneration Create already paid
	// (§5). A reconnect finds fresh already cleared and takes the normal path.
	s.mu.Lock()
	fresh := s.fresh && full
	s.fresh = false
	freshBackend := s.freshBackend
	freshPrivPath := s.sshPrivPath
	freshProjectPath := s.projectPath
	s.mu.Unlock()

	stage(StageCheck)
	var st session.State
	if fresh {
		// Synthesize the state Create already established rather than round-trip to
		// the API server. waitForPodReady below still blocks on genuine pod
		// readiness, so this only elides the status probe, not the readiness gate.
		st = session.State{ID: s.ref.ID, Status: session.StatusCreating, Backend: freshBackend, ProjectPath: freshProjectPath}
	} else {
		sp := tr.start("connect.status")
		var serr error
		st, serr = s.c.backend.Status(ctx, s.ref)
		sp.end()
		if serr != nil {
			return nil, serr
		}
	}
	// C8: projectPath is read lock-free elsewhere (ProjectPath()); guard the
	// write like the neighboring fields and use the captured local below.
	s.mu.Lock()
	if opt.ProjectPath != "" {
		s.projectPath = opt.ProjectPath
	} else if s.projectPath == "" {
		s.projectPath = st.ProjectPath
	}
	projectPath := s.projectPath
	s.mu.Unlock()

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
		sp := tr.start("connect.pod_ready")
		err := s.c.backend.StartWithProgress(ctx, s.ref, func(detail string) {
			onPhase(StageResume, detail)
		})
		sp.end()
		if err != nil {
			return nil, fmt.Errorf("wait for pod: %w", err)
		}
	}

	stage(StageForward)
	opencode := st.Backend == session.BackendOpenCode
	var handles []session.ForwardHandle
	var err error
	fwdSpan := tr.start("connect.port_forward")
	switch {
	case !full:
		// Observer mode reads only the runner event stream and never runs mutagen
		// sync or the opencode client, so the SSH and opencode forwards are pure
		// waste — forward the runner HTTP port only, whatever the backend (C4:
		// this case must be tested before the opencode one, or every background
		// observer stream to an opencode session carries 3 forwards).
		handles, err = s.c.backend.PortForward(ctx, s.ref, k8s.ForwardSpecsRunnerOnly(0))
	case opencode:
		handles, err = s.c.backend.PortForward(ctx, s.ref, k8s.ForwardSpecsWithOpencode(0, 0, 0))
	default:
		handles, err = s.c.backend.PortForward(ctx, s.ref, k8s.ForwardSpecs(0, 0))
	}
	fwdSpan.end()
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
	healthSpan := tr.start("connect.runner_health")
	err = waitHealthy(ctx, rc)
	healthSpan.end()
	if err != nil {
		s.closeHandles()
		return nil, fmt.Errorf("runner health: %w", err)
	}

	// Protocol-version handshake: warn (never refuse) on CLI/runner skew. OSS
	// users build and push their own runner images, so a mismatched pair is the
	// steady state, not an edge case — see protocolVersionWarning.
	syncWarning := protocolVersionWarning(rc)
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

		// Foreground: only the load-bearing project sync — the one the agent needs
		// staged before it can work on the repo. The 7 non-load-bearing
		// config/transcript syncs, the bounded first-sync flush, and the idle
		// reaper all move off the foreground into startBackgroundSync so the
		// visible prompt is not gated on them (§5). Reuse the SSH key Create
		// prepared on the fresh path rather than re-reading it.
		privPath := freshPrivPath
		var kerr error
		if privPath == "" {
			privPath, _, kerr = s.c.ensureSSHKey(string(s.ref.ID))
		}
		switch {
		case kerr != nil:
			// No usable SSH key → no file sync at all, but the reaper must still run.
			syncWarning = appendWarning(syncWarning, fmt.Sprintf("file sync unavailable (ssh key): %v", kerr))
			s.startBackgroundSync(tr, syncpkg.Spec{}, false, false, reaperImage, reaperPullPolicy, idleTimeout)
		default:
			syncSpan := tr.start("connect.project_sync")
			created, spec, serr := s.c.startProjectSync(ctx, string(s.ref.ID), projectPath, privPath, handles[1].LocalPort())
			syncSpan.end()
			if serr != nil {
				syncWarning = appendWarning(syncWarning, fmt.Sprintf("file sync unavailable: %v", serr))
				s.startBackgroundSync(tr, syncpkg.Spec{}, false, false, reaperImage, reaperPullPolicy, idleTimeout)
			} else {
				s.startBackgroundSync(tr, spec, created, true, reaperImage, reaperPullPolicy, idleTimeout)
			}
		}
	}

	conn := &Connection{
		// Same staging gate as Session.Runner(): a turn submitted through the
		// connection must not beat the background project-sync flush.
		Runner:   &stagedRunner{RunnerClient: rc, s: s},
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
		ocSpan := tr.start("connect.opencode_ready")
		werr := waitOpencodeReady(ctx, "http://"+addr+"/")
		ocSpan.end()
		if werr != nil {
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

// appendWarning joins a new advisory onto an existing Connection.Warning,
// separating multiple warnings with "; " rather than clobbering an earlier one
// (e.g. a protocol-version mismatch noticed at health time must survive a
// later file-sync warning).
func appendWarning(existing, addition string) string {
	if existing == "" {
		return addition
	}
	if addition == "" {
		return existing
	}
	return existing + "; " + addition
}

// protocolVersionWarning compares the runner's protocol version (as reported
// by the Health call that must already have succeeded) against this CLI's
// session.ProtocolVersion and returns a human-readable advisory if they
// differ, or "" if they match. Deliberately advisory, not fatal: OSS users
// build and push their own runner images, so CLI/runner skew is the steady
// state, not an edge case (an unknown/renamed event type is dropped
// gracefully by the TUI's reducers — see
// internal/tui/dashboard/session.go's ApplyRunnerEvent default case — rather
// than crashing), and a hard refusal here would brick a perfectly-working
// pair that differs only in a patch that didn't change the wire contract. The
// wording lives in runner.ProtocolMismatchWarning so this and the headless
// internal/cli commands (turn, trace) report identically.
func protocolVersionWarning(rc *runner.Client) string {
	return runner.ProtocolMismatchWarning(rc.ProtocolVersion())
}

// startBackgroundSync launches Connect's post-health background work off the
// foreground (§5): the bounded first-sync flush (or a detached reconnect flush),
// creation of the 7 non-load-bearing config/transcript syncs, and the idle-reaper
// ensure. It records the resulting advisory on a syncTask observable via
// AwaitSync and roots the goroutine at a context closeHandles cancels, so it
// can't outlive the session. When doSync is false (SSH key or project sync failed
// upstream) only the reaper is ensured; created marks this session's first-ever
// sync (gate the flush) versus a reconnect (detached flush).
func (s *Session) startBackgroundSync(tr *tracer, spec syncpkg.Spec, created, doSync bool, reaperImage, reaperPullPolicy string, idleTimeout time.Duration) {
	// C6: the whole background phase gets one generous overall deadline. The
	// first flush is bounded (12s below) but CreateInputs (7 mutagen execs) and
	// the reaper retry loop were not — a wedged mutagen daemon would hang this
	// goroutine forever, task.finish would never run, and the AwaitSync gate
	// would turn every StartTurn into "prompt submitted, nothing happens" with
	// no advisory.
	bgCtx, cancel := context.WithTimeoutCause(context.Background(), bgSyncOverallTimeout, errBgSyncTimeout)
	task := &syncTask{done: make(chan struct{})}
	s.mu.Lock()
	s.bgCancel = cancel
	s.syncTask = task
	s.mu.Unlock()

	go func() {
		// §10: time the background phase under the same connect id, so the cost
		// that Connect deferred off the foreground (§5) is still visible in a trace.
		bgSpan := tr.start("connect.background")
		defer bgSpan.end()
		var warn string
		id := string(s.ref.ID)
		mgr := s.c.syncManager()
		if doSync {
			if created {
				// First-ever sync: `mutagen sync create` returns before the transport
				// is proven or files have staged, so flush to settle the initial
				// project upload and surface a broken transport (RV20/RV21). Bounded:
				// a healthy-but-large first sync just keeps uploading in the
				// background. This is the step AwaitSync gates the first turn on.
				flushSpan := tr.start("connect.first_flush")
				flushCtx, fc := context.WithTimeout(bgCtx, 12*time.Second)
				ferr := mgr.FlushAll(flushCtx, id)
				timedOut := flushCtx.Err() == context.DeadlineExceeded
				fc()
				flushSpan.end()
				switch {
				case ferr != nil && timedOut:
					warn = appendWarning(warn, "initial file sync still in progress (continuing in the background)")
				case ferr != nil && bgCtx.Err() == nil:
					warn = appendWarning(warn, fmt.Sprintf("file sync error: %v", ferr))
				}
			} else {
				// Reconnect to an already-synced session: the mutagen session persists
				// and reconciles on its own, so don't hold the gate on a full flush —
				// kick a detached one so mutagen re-establishes the transport on the
				// new port-forward promptly.
				go func() {
					fctx, fc := context.WithTimeout(bgCtx, 30*time.Second)
					defer fc()
					_ = mgr.FlushAll(fctx, id)
				}()
			}
			// Create the config/transcript syncs now, off the foreground. A real
			// failure is surfaced via the advisory (AwaitSync), never dropped.
			inputsSpan := tr.start("connect.create_inputs")
			ierr := mgr.CreateInputs(bgCtx, spec)
			inputsSpan.end()
			if ierr != nil && bgCtx.Err() == nil {
				warn = appendWarning(warn, fmt.Sprintf("config/transcript sync setup failed: %v", ierr))
			}
		}
		// Ensure the idle reaper (with bounded retry): it caps runaway pod cost, so
		// it must run reliably even off the foreground — a transient blip must not
		// silently leave a session with no auto-suspend.
		if bgCtx.Err() == nil {
			reaperSpan := tr.start("connect.reaper")
			w := s.c.ensureReaperWithRetry(bgCtx, s.ref, reaperImage, reaperPullPolicy, idleTimeout)
			reaperSpan.end()
			if w != "" {
				warn = appendWarning(warn, w)
			}
		}
		// A timed-out background phase must be visible: the bgCtx.Err() guards
		// above suppress per-step errors on cancellation (closeHandles), which
		// would otherwise also swallow the deadline case.
		if context.Cause(bgCtx) == errBgSyncTimeout {
			warn = appendWarning(warn, fmt.Sprintf("background sync/reaper setup timed out after %s (file sync may be unavailable)", bgSyncOverallTimeout))
		}
		task.finish(warn)
	}()
}

// bgSyncOverallTimeout bounds Connect's whole background phase (C6): generous
// enough for a slow first upload's flush + 7 config-sync creates + reaper
// retries, but finite so a hung mutagen daemon can't wedge the AwaitSync gate.
const bgSyncOverallTimeout = 60 * time.Second

// errBgSyncTimeout distinguishes the C6 deadline from an ordinary cancel
// (closeHandles), which must stay silent.
var errBgSyncTimeout = errors.New("background sync setup timed out")

// AwaitSync blocks until Connect's background file-sync + idle-reaper work (see
// startBackgroundSync) has settled, returning any non-fatal advisory to surface
// (empty on clean success). It is the seam a caller uses to gate the first turn
// submission on the initial project-sync staging: Connect no longer blocks on
// that flush itself (§5), so a caller that needs the workspace staged before the
// agent acts must AwaitSync first. Returns immediately with no warning when there
// is no background work in flight (an Observer connect, or before the first
// Connect). Safe to call repeatedly and from multiple goroutines.
func (s *Session) AwaitSync(ctx context.Context) (warning string, err error) {
	s.mu.Lock()
	t := s.syncTask
	s.mu.Unlock()
	if t == nil {
		return "", nil
	}
	select {
	case <-t.done:
		return t.result(), nil
	case <-ctx.Done():
		return "", ctx.Err()
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
	// Cancel any in-flight background sync/reaper work so it can't outlive the
	// session (or leak past a reconnect, which re-runs it). A caller that already
	// captured the prior syncTask via AwaitSync still observes its completion —
	// the goroutine unblocks via the cancelled context and closes done — but new
	// AwaitSync callers see the fresh (or absent) task.
	if s.bgCancel != nil {
		s.bgCancel()
		s.bgCancel = nil
	}
	s.syncTask = nil
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

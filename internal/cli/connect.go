package cli

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// *runner.Client must satisfy the wider dashboard.RunnerClient surface (a
// structural superset of session.RunnerClient that adds EventsPassive for RV6
// status-observer streams). This assertion lives here — where both packages are
// already imported — so the TUI dependency tree isn't pulled into internal/runner.
var _ dashboard.RunnerClient = (*runner.Client)(nil)

// sessionConnector (re)establishes a live connection to a session's runner. Its
// connect method is handed to the TUI as the reconnect callback: on stream loss
// (node drain, suspend/resume, transient port-forward drop) the TUI calls it to
// resume the pod if needed, re-port-forward, and hand back a fresh client. It
// owns the port-forward handles and replaces them on each reconnect.
type sessionConnector struct {
	backend     *k8s.Backend
	ref         session.Ref
	projectPath string
	reaperImage string

	mu      sync.Mutex
	handles []session.ForwardHandle
}

// connection bundles a live runner client with the local endpoints and any
// backend-specific credentials needed by the dashboard pane.
type connection struct {
	client   *runner.Client
	endpoint string
	opencode *opencodeCreds
	backend  string
	// warning is a non-fatal advisory (e.g. sync failure) to surface to the
	// user via the dashboard rather than discarding to hidden stderr (C9).
	warning string
}

// opencodeCreds holds the local endpoint and basic-auth credentials for an
// opencode-server session.
type opencodeCreds struct {
	username string
	password string
	url      string
}

// connect establishes (or re-establishes) the runner connection and returns a
// healthy client plus endpoint/credential metadata. Safe to call repeatedly;
// prior port-forwards are closed. The concrete *runner.Client satisfies
// dashboard.RunnerClient, so it threads straight into the dashboard
// Connector/reconnect callbacks.
//
// onStage reports connect progress to the connecting screen. Pass nil on
// reconnect: the original connecting screen's update channel has been closed by
// then, so reusing its sender would send on a closed channel and panic.
func (sc *sessionConnector) connect(ctx context.Context, onStage func(dashboard.ConnectStage, string)) (*connection, error) {
	return sc.establish(ctx, onStage, true)
}

// connectObserver establishes a lightweight, read-only connection for a
// background status stream: port-forward + runner health only. It skips file
// sync setup, the idle-reaper ensure, and the opencode-serve readiness wait —
// all attach-time concerns — so the dashboard's per-session background streams
// stay cheap (RV8) instead of each running mutagen sync create + flush just to
// observe events.
func (sc *sessionConnector) connectObserver(ctx context.Context, onStage func(dashboard.ConnectStage, string)) (*connection, error) {
	return sc.establish(ctx, onStage, false)
}

// establish is the shared connect body. full=true performs the complete attach
// setup (file sync, idle reaper, opencode readiness); full=false stops at a
// healthy runner client, for a background observer stream.
func (sc *sessionConnector) establish(ctx context.Context, onStage func(dashboard.ConnectStage, string), full bool) (*connection, error) {
	sc.closeHandles()
	if onStage == nil {
		onStage = func(dashboard.ConnectStage, string) {}
	}
	// stage reports a coarse phase with no sub-detail; onStage carries a live
	// detail string (used during the initial file sync).
	stage := func(s dashboard.ConnectStage) { onStage(s, "") }

	stage(dashboard.StageCheck)
	st, err := sc.backend.Status(ctx, sc.ref)
	if err != nil {
		return nil, err
	}
	switch st.Status {
	case session.StatusGone:
		return nil, fmt.Errorf("session %s: %w", sc.ref.ID, session.ErrSessionGone)
	case session.StatusSuspended:
		stage(dashboard.StageResume)
		if err := sc.backend.Resume(ctx, sc.ref); err != nil {
			return nil, fmt.Errorf("resume: %w", err)
		}
	}

	// Block until the pod is actually running and ready before port-forwarding.
	// For a freshly created session the pod is still scheduling and pulling the
	// runner image; for a just-resumed one the new pod is booting. Reporting the
	// phase under StageResume keeps the connect splash animating ("Starting pod —
	// pulling image") with the elapsed timer, instead of the caller blocking on a
	// frozen terminal before the TUI even draws (Phase 2). Observer streams
	// (full=false) only attach to already-warm sessions, so they skip the explicit
	// wait and lean on the runner /healthz poll below.
	if full {
		if err := sc.backend.StartWithProgress(ctx, sc.ref, func(detail string) {
			onStage(dashboard.StageResume, detail)
		}); err != nil {
			return nil, fmt.Errorf("wait for pod: %w", err)
		}
	}

	stage(dashboard.StageForward)
	opencode := st.Backend == session.BackendOpenCode
	var handles []session.ForwardHandle
	switch {
	case opencode:
		handles, err = sc.backend.PortForward(ctx, sc.ref, k8s.ForwardSpecsWithOpencode(0, 0, 0))
	case !full:
		// Observer mode (background status streams) only reads the runner event
		// stream and never runs mutagen sync, so the SSH forward is pure waste.
		// Forward the runner HTTP port only to keep the launch-time fan-out across
		// every known session cheap (one SPDY stream per session, not two). The
		// SSH-dependent code below is all gated on `full`, so handles[1] is never
		// touched here.
		handles, err = sc.backend.PortForward(ctx, sc.ref, k8s.ForwardSpecsRunnerOnly(0))
	default:
		handles, err = sc.backend.PortForward(ctx, sc.ref, k8s.ForwardSpecs(0, 0))
	}
	if err != nil {
		return nil, fmt.Errorf("port-forward: %w", err)
	}
	sc.setHandles(handles)

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", handles[0].LocalPort())

	token, err := sc.backend.RunnerToken(ctx, sc.ref)
	if err != nil {
		return nil, err
	}
	client := runner.New(endpoint, token)
	stage(dashboard.StageRunner)
	if err := waitHealthy(ctx, client); err != nil {
		return nil, fmt.Errorf("runner health: %w", err)
	}

	var syncWarning string
	if full {
		stage(dashboard.StageSync)
		privPath, _, kerr := ensureSSHKey(string(sc.ref.ID))
		if kerr != nil {
			syncWarning = fmt.Sprintf("file sync unavailable (ssh key): %v", kerr)
		} else if created, serr := startMutagen(ctx, string(sc.ref.ID), sc.projectPath, privPath, handles[1].LocalPort()); serr != nil {
			syncWarning = fmt.Sprintf("file sync unavailable: %v", serr)
		} else if created {
			// First-ever sync for this session: `mutagen sync create` returns before
			// the SSH transport is proven or any files have staged, so a broken
			// transport (auth/agent-install) or a not-yet-uploaded workspace would
			// otherwise be invisible. Block on a bounded flush: a broken transport
			// errors fast (surfaced as a warning, RV20); a healthy-but-large first
			// sync may time out, which just means "still uploading in the background"
			// rather than a failure (RV21).
			flushCtx, cancelFlush := context.WithTimeout(ctx, 12*time.Second)
			ferr := sc.flushWithProgress(flushCtx, onStage)
			timedOut := flushCtx.Err() == context.DeadlineExceeded
			cancelFlush()
			switch {
			case ferr != nil && timedOut:
				syncWarning = "initial file sync still in progress (continuing in the background)"
			case ferr != nil:
				syncWarning = fmt.Sprintf("file sync error: %v", ferr)
			}
		} else {
			// Reconnect to an already-synced session: the mutagen session persists in
			// the daemon and reconciles on its own, so DON'T block the user behind a
			// full flush — that blocking flush is what made reattaching feel like a
			// fresh "Syncing Files…" every time. Kick a detached flush so mutagen
			// re-establishes the transport on the new port-forward promptly instead of
			// waiting out its reconnect backoff; the dashboard's per-session sync
			// indicator shows progress. This is what makes reattaching feel instant.
			go func() {
				bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = syncManager().FlushAll(bgCtx, string(sc.ref.ID))
			}()
		}

		// Make sure the idle reaper is watching (a prior one may have completed when
		// the session was suspended).
		ensureReaperForSession(ctx, sc.backend, sc.ref, sc.reaperImage)
	}

	conn := &connection{
		client:   client,
		endpoint: endpoint,
		backend:  st.Backend,
		warning:  syncWarning,
	}
	if full && opencode {
		pass, perr := sc.backend.OpencodePassword(ctx, sc.ref)
		if perr != nil {
			sc.closeHandles()
			return nil, fmt.Errorf("opencode password: %w", perr)
		}
		addr := fmt.Sprintf("127.0.0.1:%d", handles[2].LocalPort())
		stage(dashboard.StageOpencode)
		// `opencode serve` is a child of the runner and comes up a beat after
		// /healthz, so wait for it to actually answer before handing the address to
		// the local `opencode attach` — otherwise attach hits connection-refused and
		// exits immediately, bouncing the user back to the dashboard. We probe with
		// a real HTTP request (not a bare TCP dial): a client-go SPDY port-forward
		// accepts the *local* connection the instant its listener binds, so a dial
		// false-passes even when nothing is listening in the pod (T1). An HTTP
		// round-trip forces the forward to connect through to the pod port.
		if werr := waitOpencodeReady(ctx, "http://"+addr+"/"); werr != nil {
			sc.closeHandles()
			return nil, fmt.Errorf("opencode serve not ready: %w", werr)
		}
		conn.opencode = &opencodeCreds{
			username: k8s.OpencodeUsername(),
			password: pass,
			url:      "http://" + addr,
		}
	}
	stage(dashboard.StageAttach)
	return conn, nil
}

// waitOpencodeReady blocks until an HTTP request to url elicits any HTTP
// response (confirming `opencode serve` is actually listening in the pod), or
// the budget/context expires. ANY status code counts as ready — even a 401/404
// proves the pod-side server answered; only transport errors (the forward
// failing to reach the pod port) are treated as not-ready and retried. This is
// the fix for T1: a bare TCP dial passed as soon as the local SPDY listener
// bound, regardless of whether anything was up in the pod.
func waitOpencodeReady(ctx context.Context, url string) error {
	deadline := time.Now().Add(30 * time.Second)
	client := &http.Client{Timeout: 3 * time.Second}
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil // any HTTP response means the pod-side server answered
		}
		lastErr = err
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// flushWithProgress runs the bounded initial sync flush while polling mutagen
// for a live staging phase, reporting it as a StageSync sub-detail so the
// connect screen shows "Syncing files — uploading…" instead of a frozen label.
// It returns the flush's own result (a timeout shows up via ctx, as before).
func (sc *sessionConnector) flushWithProgress(ctx context.Context, onStage func(dashboard.ConnectStage, string)) error {
	mgr := syncManager()
	id := string(sc.ref.ID)
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
				onStage(dashboard.StageSync, phase)
			}
		}
	}
}

func (sc *sessionConnector) setHandles(h []session.ForwardHandle) {
	sc.mu.Lock()
	sc.handles = h
	sc.mu.Unlock()
}

// closeHandles tears down any active port-forwards.
func (sc *sessionConnector) closeHandles() {
	sc.mu.Lock()
	handles := sc.handles
	sc.handles = nil
	sc.mu.Unlock()
	for _, h := range handles {
		h.Close()
	}
}

// waitHealthy polls the runner /healthz until it responds OK or ctx is done.
// A freshly resumed pod (or new port-forward) may need a moment.
func waitHealthy(ctx context.Context, client *runner.Client) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := client.Health(hctx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

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
func (sc *sessionConnector) connect(ctx context.Context, onStage func(dashboard.ConnectStage)) (*connection, error) {
	sc.closeHandles()
	if onStage == nil {
		onStage = func(dashboard.ConnectStage) {}
	}

	onStage(dashboard.StageCheck)
	st, err := sc.backend.Status(ctx, sc.ref)
	if err != nil {
		return nil, err
	}
	switch st.Status {
	case session.StatusGone:
		return nil, fmt.Errorf("session %s no longer exists", sc.ref.ID)
	case session.StatusSuspended:
		onStage(dashboard.StageResume)
		if err := sc.backend.Resume(ctx, sc.ref); err != nil {
			return nil, fmt.Errorf("resume: %w", err)
		}
	}

	onStage(dashboard.StageForward)
	opencode := st.Backend == session.BackendOpenCode
	var handles []session.ForwardHandle
	if opencode {
		handles, err = sc.backend.PortForward(ctx, sc.ref, k8s.ForwardSpecsWithOpencode(0, 0, 0))
	} else {
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
	onStage(dashboard.StageRunner)
	if err := waitHealthy(ctx, client); err != nil {
		return nil, fmt.Errorf("runner health: %w", err)
	}

	onStage(dashboard.StageSync)
	var syncWarning string
	privPath, _, kerr := ensureSSHKey(string(sc.ref.ID))
	if kerr != nil {
		syncWarning = fmt.Sprintf("file sync unavailable (ssh key): %v", kerr)
	} else if serr := startMutagen(ctx, string(sc.ref.ID), sc.projectPath, privPath, handles[1].LocalPort()); serr != nil {
		syncWarning = fmt.Sprintf("file sync unavailable: %v", serr)
	} else {
		// `mutagen sync create` returns before the SSH transport is proven or any
		// files have staged, so a broken transport (auth/agent-install) or a
		// not-yet-uploaded workspace would otherwise be invisible. Flush with a
		// bound: a broken transport errors fast (surfaced as a warning, RV20); a
		// healthy-but-large first sync may time out, which just means "still
		// uploading in the background" rather than a failure (RV21).
		flushCtx, cancelFlush := context.WithTimeout(ctx, 12*time.Second)
		ferr := syncManager().FlushAll(flushCtx, string(sc.ref.ID))
		timedOut := flushCtx.Err() == context.DeadlineExceeded
		cancelFlush()
		switch {
		case ferr != nil && timedOut:
			syncWarning = "initial file sync still in progress (continuing in the background)"
		case ferr != nil:
			syncWarning = fmt.Sprintf("file sync error: %v", ferr)
		}
	}

	// Make sure the idle reaper is watching (a prior one may have completed when
	// the session was suspended).
	ensureReaperForSession(ctx, sc.backend, sc.ref, sc.reaperImage)

	conn := &connection{
		client:   client,
		endpoint: endpoint,
		backend:  st.Backend,
		warning:  syncWarning,
	}
	if opencode {
		pass, perr := sc.backend.OpencodePassword(ctx, sc.ref)
		if perr != nil {
			sc.closeHandles()
			return nil, fmt.Errorf("opencode password: %w", perr)
		}
		addr := fmt.Sprintf("127.0.0.1:%d", handles[2].LocalPort())
		onStage(dashboard.StageOpencode)
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
	onStage(dashboard.StageAttach)
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

package cli

import (
	"context"
	"fmt"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// newDashboardConnector returns a dashboard.Connector that adapts the CLI's
// sessionConnector into the interface the dashboard package expects. This is
// the sole bridge between internal/cli and internal/tui/dashboard: the
// dashboard never imports cli; cli passes in this adapter at startup.
//
// Each call to the returned Connector creates a fresh sessionConnector scoped
// to the given session, performs resume-if-suspended + port-forward + health,
// and returns the resulting RunnerClient plus a reconnect callback.
func newDashboardConnector(backend *k8s.Backend, reaperImage string) dashboard.Connector {
	if reaperImage == "" {
		reaperImage = k8s.DefaultReaperImage
	}
	return func(ctx context.Context, ref session.Ref, projectPath string, onStage func(dashboard.ConnectStage)) (dashboard.ConnectResult, error) {
		sc := &sessionConnector{
			backend:     backend,
			ref:         ref,
			projectPath: projectPath,
			reaperImage: reaperImage,
		}

		conn, err := sc.connect(ctx, onStage)
		if err != nil {
			return dashboard.ConnectResult{}, fmt.Errorf("connect %s: %w", ref.ID, err)
		}

		// The reconnect callback closes over the same sessionConnector so it
		// re-uses the port-forward state when the stream drops. It passes a nil
		// onStage: the connecting screen (and its update channel) is long gone by
		// reconnect time, so emitting stage updates there would panic.
		reconnect := func(ctx context.Context) (dashboard.RunnerClient, error) {
			c, rerr := sc.connect(ctx, nil)
			if rerr != nil {
				return nil, rerr
			}
			return c.client, nil
		}

		var oc *dashboard.OpencodeCreds
		if conn.opencode != nil {
			oc = &dashboard.OpencodeCreds{
				Username: conn.opencode.username,
				Password: conn.opencode.password,
				URL:      conn.opencode.url,
			}
		}

		return dashboard.ConnectResult{
			Client:        conn.client,
			Reconnect:     reconnect,
			Endpoint:      conn.endpoint,
			OpencodeCreds: oc,
			Warning:       conn.warning,
		}, nil
	}
}

// newDashboardObserverConnector returns a dashboard.Connector wired to the
// lightweight observer connect (port-forward + runner health only, no file-sync
// setup). The dashboard uses it for background passive status streams so each
// per-session stream stops paying for mutagen sync create + flush just to
// observe events (RV8). The returned reconnect callback reuses the same
// lightweight path.
func newDashboardObserverConnector(backend *k8s.Backend, reaperImage string) dashboard.Connector {
	if reaperImage == "" {
		reaperImage = k8s.DefaultReaperImage
	}
	return func(ctx context.Context, ref session.Ref, projectPath string, onStage func(dashboard.ConnectStage)) (dashboard.ConnectResult, error) {
		sc := &sessionConnector{
			backend:     backend,
			ref:         ref,
			projectPath: projectPath,
			reaperImage: reaperImage,
		}

		conn, err := sc.connectObserver(ctx, onStage)
		if err != nil {
			return dashboard.ConnectResult{}, fmt.Errorf("observe %s: %w", ref.ID, err)
		}

		reconnect := func(ctx context.Context) (dashboard.RunnerClient, error) {
			c, rerr := sc.connectObserver(ctx, nil)
			if rerr != nil {
				return nil, rerr
			}
			return c.client, nil
		}

		return dashboard.ConnectResult{
			Client:    conn.client,
			Reconnect: reconnect,
			Endpoint:  conn.endpoint,
		}, nil
	}
}

// newDashboardCreator returns a dashboard.Creator that provisions a brand-new
// session for the current working directory and connects to it — the `n` (new
// session) action. It mirrors `sandbox claude` without a prompt: ID generation,
// SSH-key prep, Sandbox/PVC creation, pod start, and the port-forward +
// health-check that the connector performs on attach. runnerImage and
// reaperImage are threaded through so the dashboard `n` / bare-`sandbox` path
// honors --runner-image / --reaper-image (empty => the respective defaults).
func newDashboardCreator(backend *k8s.Backend, runnerImage, reaperImage string) dashboard.Creator {
	if reaperImage == "" {
		reaperImage = k8s.DefaultReaperImage
	}
	return func(ctx context.Context, backendName string, onStage func(dashboard.ConnectStage)) (dashboard.CreateResult, error) {
		if backendName == "" {
			backendName = session.BackendClaudeSDK
		}

		// Dashboard-created sessions use the account default model; the in-session
		// /model command can switch it per turn afterwards.
		sid, ref, projectPath, err := provisionSession(ctx, backend, backendName, runnerImage, "")
		if err != nil {
			return dashboard.CreateResult{}, err
		}

		if err := backend.Start(ctx, ref); err != nil {
			return dashboard.CreateResult{}, fmt.Errorf("start session: %w", err)
		}

		onStage(dashboard.StageResume)
		sc := &sessionConnector{
			backend:     backend,
			ref:         ref,
			projectPath: projectPath,
			reaperImage: reaperImage,
		}
		conn, err := sc.connect(ctx, onStage)
		if err != nil {
			sc.closeHandles()
			return dashboard.CreateResult{}, fmt.Errorf("connect: %w", err)
		}

		st, err := backend.Status(ctx, ref)
		if err != nil {
			st = session.State{
				ID:          sid,
				Backend:     backendName,
				ProjectPath: projectPath,
				Status:      session.StatusRunning,
			}
		}

		reconnect := func(ctx context.Context) (dashboard.RunnerClient, error) {
			c, rerr := sc.connect(ctx, nil)
			if rerr != nil {
				return nil, rerr
			}
			return c.client, nil
		}

		var oc *dashboard.OpencodeCreds
		if conn.opencode != nil {
			oc = &dashboard.OpencodeCreds{
				Username: conn.opencode.username,
				Password: conn.opencode.password,
				URL:      conn.opencode.url,
			}
		}

		return dashboard.CreateResult{
			Client:        conn.client,
			Reconnect:     reconnect,
			State:         st,
			Endpoint:      conn.endpoint,
			OpencodeCreds: oc,
			Warning:       conn.warning,
		}, nil
	}
}

package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// This file is the sole bridge between the public client package and the
// dashboard: it adapts client.Session connects into the Connector/Creator
// function types the dashboard expects, so the dashboard never imports cli or
// client, and the CLI/TUI drive sessions through the exact same public API an
// external Go program would.

// stageSink adapts a dashboard onStage callback into the client's OnPhase
// callback, mapping client.Stage to dashboard.ConnectStage. Returns nil when
// onStage is nil so the client treats it as "no progress reporting".
func stageSink(onStage func(dashboard.ConnectStage, string)) func(client.Stage, string) {
	if onStage == nil {
		return nil
	}
	return func(st client.Stage, detail string) { onStage(mapStage(st), detail) }
}

// mapStage maps a client connect stage to the dashboard's connect stage. The two
// enums are intentionally 1:1.
func mapStage(st client.Stage) dashboard.ConnectStage {
	switch st {
	case client.StageResume:
		return dashboard.StageResume
	case client.StageForward:
		return dashboard.StageForward
	case client.StageRunner:
		return dashboard.StageRunner
	case client.StageSync:
		return dashboard.StageSync
	case client.StageOpencode:
		return dashboard.StageOpencode
	case client.StageAttach:
		return dashboard.StageAttach
	default: // StageCheck and anything else
		return dashboard.StageCheck
	}
}

// mapOpencode adapts the client's generic external-service creds
// (Connection.External) to the dashboard's opencode pane type. Today the only
// backend with an external service is opencode-server; codex will reuse the
// client.ExternalCreds shape with its own dashboard pane.
func mapOpencode(oc *client.ExternalCreds) *dashboard.OpencodeCreds {
	if oc == nil {
		return nil
	}
	return &dashboard.OpencodeCreds{Username: oc.Username, Password: oc.Password, URL: oc.URL}
}

// paneDialTimeout bounds a pane WebSocket dial. The dial rides an
// already-established localhost port-forward, so it is normally instant; the
// budget only has to cover the runner lazily spawning the interactive child on
// a first-ever attach.
const paneDialTimeout = 30 * time.Second

// mapPaneDial adapts Session.AttachPane to the dashboard's PaneDial seam for a
// claude-pane session (nil for every other backend, which keeps the dashboard's
// pane routing off). client.PaneStream satisfies dashboard.PaneTransport
// structurally, so the stream crosses the seam without an adapter — this
// function exists so the dashboard never imports client.
func mapPaneDial(sess *client.Session, backend string) dashboard.PaneDial {
	if backend != client.BackendClaudePane {
		return nil
	}
	return func(cols, rows int) (dashboard.PaneTransport, error) {
		// The context governs only the dial/handshake; the established stream
		// lives until Close (same ownership rule as the port-forwards).
		ctx, cancel := context.WithTimeout(context.Background(), paneDialTimeout)
		defer cancel()
		return sess.AttachPane(ctx, cols, rows)
	}
}

// newDashboardConnector returns a dashboard.Connector that drives a client.Session
// connect (resume-if-suspended + wait-ready + port-forward + health + file sync +
// idle reaper) and adapts the result into dashboard types. Each call scopes a
// fresh Session to the given ref; the reconnect callback reuses it so the
// port-forward state carries across stream drops.
func newDashboardConnector(c *client.Client, reaperImage string) dashboard.Connector {
	return func(ctx context.Context, ref session.Ref, projectPath string, onStage func(dashboard.ConnectStage, string)) (dashboard.ConnectResult, error) {
		sess := c.Open(ref.ID)
		opt := client.ConnectOptions{ProjectPath: projectPath, ReaperImage: reaperImage, OnPhase: stageSink(onStage)}

		conn, err := sess.Connect(ctx, opt)
		if err != nil {
			sess.Close()
			return dashboard.ConnectResult{}, fmt.Errorf("connect %s: %w", ref.ID, err)
		}

		reconnect := func(ctx context.Context, onStage func(dashboard.ConnectStage, string)) (dashboard.RunnerClient, error) {
			ropt := opt
			ropt.OnPhase = stageSink(onStage)
			c2, rerr := sess.Connect(ctx, ropt)
			if rerr != nil {
				return nil, rerr
			}
			return c2.Runner, nil
		}

		return dashboard.ConnectResult{
			Client:        conn.Runner,
			Reconnect:     reconnect,
			Endpoint:      conn.Endpoint,
			OpencodeCreds: mapOpencode(conn.External),
			PaneDial:      mapPaneDial(sess, conn.Backend),
			Warning:       conn.Warning,
			// §1d C1: forwards outlive the connect ctx by design; this is the only
			// handle that actually releases them. sess is per-connector-call, so
			// closing here can't touch another connect's forwards.
			Close: func() { _ = sess.Close() },
		}, nil
	}
}

// newDashboardObserverConnector returns a dashboard.Connector wired to the
// lightweight observer connect (port-forward + runner health only, no file-sync
// setup or reaper), used for background passive status streams.
func newDashboardObserverConnector(c *client.Client, reaperImage string) dashboard.Connector {
	_ = reaperImage // observer streams never ensure the reaper
	return func(ctx context.Context, ref session.Ref, projectPath string, onStage func(dashboard.ConnectStage, string)) (dashboard.ConnectResult, error) {
		sess := c.Open(ref.ID)
		opt := client.ConnectOptions{ProjectPath: projectPath, Observer: true, OnPhase: stageSink(onStage)}

		conn, err := sess.Connect(ctx, opt)
		if err != nil {
			sess.Close()
			return dashboard.ConnectResult{}, fmt.Errorf("observe %s: %w", ref.ID, err)
		}

		reconnect := func(ctx context.Context, onStage func(dashboard.ConnectStage, string)) (dashboard.RunnerClient, error) {
			ropt := opt
			ropt.OnPhase = stageSink(onStage)
			c2, rerr := sess.Connect(ctx, ropt)
			if rerr != nil {
				return nil, rerr
			}
			return c2.Runner, nil
		}

		return dashboard.ConnectResult{
			Client:    conn.Runner,
			Reconnect: reconnect,
			Endpoint:  conn.Endpoint,
			Close:     func() { _ = sess.Close() },
		}, nil
	}
}

// newDashboardCreator returns a dashboard.Creator that provisions a brand-new
// session for the current working directory and connects to it — the `n` (new
// session) action. It mirrors `sandbox claude` without a prompt, driven entirely
// through the public client API.
func newDashboardCreator(c *client.Client, runnerImage, reaperImage string) dashboard.Creator {
	return func(ctx context.Context, params dashboard.CreateParams, onStage func(dashboard.ConnectStage, string)) (dashboard.CreateResult, error) {
		backendName := params.Backend
		if backendName == "" {
			backendName = client.BackendClaudeSDK
		}
		projectPath, err := resolveProjectPath()
		if err != nil {
			return dashboard.CreateResult{}, err
		}

		// Dashboard-created sessions use the account default model; the in-session
		// /model command can switch it per turn afterwards.
		opts := client.CreateOptions{Backend: backendName, ProjectPath: projectPath, RunnerImage: runnerImage}
		switch {
		case backendName == client.BackendClaudePane:
			// claude-pane: full credential material, fail closed (no shared-Secret
			// fallback exists). An empty picked id selects the host's own Claude
			// Code login (Max mode) — same semantics as the CLI's --pane flag.
			store, serr := newCredStore()
			if serr != nil {
				return dashboard.CreateResult{}, serr
			}
			if aerr := opts.SelectClaudePaneMaterial(store, params.AnthropicAccountID); aerr != nil {
				return dashboard.CreateResult{}, aerr
			}
		case params.AnthropicAccountID != "":
			// A picked Anthropic account is resolved to a per-session credential here,
			// via the SAME fail-closed SDK helper the CLI's `--account` flag uses: any
			// resolution/Keychain error is returned (surfaced in the dashboard's
			// connect-error UI), never a silent fall-back to the shared Secret. An empty
			// id is the legacy/cluster-default path — opts is left untouched.
			store, serr := newCredStore()
			if serr != nil {
				return dashboard.CreateResult{}, serr
			}
			if aerr := opts.UseAnthropicAccount(store, params.AnthropicAccountID); aerr != nil {
				return dashboard.CreateResult{}, aerr
			}
		}

		sess, err := c.Create(ctx, opts)
		if err != nil {
			return dashboard.CreateResult{}, err
		}

		opt := client.ConnectOptions{ReaperImage: reaperImage, OnPhase: stageSink(onStage)}
		conn, err := sess.Connect(ctx, opt)
		if err != nil {
			sess.Close()
			return dashboard.CreateResult{}, fmt.Errorf("connect: %w", err)
		}

		st, err := c.Status(ctx, sess.ID())
		if err != nil {
			st = session.State{ID: sess.ID(), Backend: backendName, ProjectPath: projectPath, Status: session.StatusRunning}
		}

		reconnect := func(ctx context.Context, onStage func(dashboard.ConnectStage, string)) (dashboard.RunnerClient, error) {
			ropt := opt
			ropt.OnPhase = stageSink(onStage)
			c2, rerr := sess.Connect(ctx, ropt)
			if rerr != nil {
				return nil, rerr
			}
			return c2.Runner, nil
		}

		return dashboard.CreateResult{
			State:         st,
			Client:        conn.Runner,
			Reconnect:     reconnect,
			Endpoint:      conn.Endpoint,
			OpencodeCreds: mapOpencode(conn.External),
			PaneDial:      mapPaneDial(sess, conn.Backend),
			Warning:       conn.Warning,
			Close:         func() { _ = sess.Close() },
		}, nil
	}
}

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/index"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// defaultRunnerImage is the runner image pulled by session pods. It points at
// the zot registry (anonymous pull, same as the reaper image) rather than
// GHCR, whose package is private and 403s on the cluster's anonymous pull.
const defaultRunnerImage = "registry.cullen.rocks/sandbox-claude-runner:latest"

func newClaudeRemoteCmd() *cobra.Command {
	var (
		runnerImage string
		reaperImage string
		nameFlag    string
		modelFlag   string
	)
	cmd := &cobra.Command{
		Use:   "claude [prompt]",
		Short: "Start or attach a remote Claude SDK session and open the local TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := ""
			if len(args) > 0 {
				prompt = args[0]
			}
			return runStartSession(cmd, session.BackendClaudeSDK, prompt, runnerImage, reaperImage, nameFlag, modelFlag)
		},
	}
	cmd.Flags().StringVar(&runnerImage, "runner-image", defaultRunnerImage, "runner container image")
	cmd.Flags().StringVar(&reaperImage, "reaper-image", k8s.DefaultReaperImage, "idle-reaper container image")
	cmd.Flags().StringVar(&nameFlag, "name", "", "custom display name for the session (overrides the auto title)")
	cmd.Flags().StringVar(&modelFlag, "model", "", "model id/alias for the session default (e.g. opus, sonnet, haiku); empty uses the account default. Switch in-session with /model")
	return cmd
}

// newOpencodeCmd starts (or, via the dashboard, reuses) a remote opencode-server
// session and hands the terminal to the local `opencode attach` TUI. Unlike
// `claude`, opencode owns its own input loop, so there is no initial-prompt
// argument: the prompt is typed into the opencode TUI itself.
func newOpencodeCmd() *cobra.Command {
	var (
		runnerImage string
		reaperImage string
		nameFlag    string
	)
	cmd := &cobra.Command{
		Use:   "opencode",
		Short: "Start a remote opencode-server session and open the local opencode TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			// opencode manages its own model selection, so there is no --model
			// flag here (TODO.md): pass an empty model.
			return runStartSession(cmd, session.BackendOpenCode, "", runnerImage, reaperImage, nameFlag, "")
		},
	}
	cmd.Flags().StringVar(&runnerImage, "runner-image", defaultRunnerImage, "runner container image")
	cmd.Flags().StringVar(&reaperImage, "reaper-image", k8s.DefaultReaperImage, "idle-reaper container image")
	cmd.Flags().StringVar(&nameFlag, "name", "", "custom display name for the session (overrides the auto title)")
	return cmd
}

// provisionSession runs the create-side of a new remote session that is shared
// by `sandbox claude`/`opencode` (runStartSession) and the dashboard's `n`
// (new-session) action (newDashboardCreator): resolve the cwd, mint a session
// id, prepare the per-session SSH key, build the Spec, create the Sandbox/PVC,
// and record the session in the local index. It does NOT start the pod or
// connect — callers do that, since the start/connect/attach flow differs.
func provisionSession(ctx context.Context, backend *k8s.Backend, backendName, runnerImage, model string) (session.ID, session.Ref, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", session.Ref{}, "", fmt.Errorf("get cwd: %w", err)
	}
	projectPath, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		projectPath = cwd
	}

	if backendName == "" {
		backendName = session.BackendClaudeSDK
	}
	if runnerImage == "" {
		runnerImage = defaultRunnerImage
	}

	sid, err := newSessionID(backendName, projectPath)
	if err != nil {
		return "", session.Ref{}, "", err
	}

	// Prepare the per-session SSH key for Mutagen before creating the pod,
	// since its public half is baked into the session Secret. (connect
	// re-derives the private key path for the sync transport.)
	_, authKey, err := ensureSSHKey(string(sid))
	if err != nil {
		return "", session.Ref{}, "", fmt.Errorf("prepare ssh key: %w", err)
	}

	spec := session.Spec{
		ID:           sid,
		ProjectPath:  projectPath,
		Backend:      backendName,
		RunnerImage:  runnerImage,
		SSHPublicKey: authKey,
		Model:        model,
	}

	ref, err := backend.CreateSession(ctx, spec)
	if err != nil {
		return "", session.Ref{}, "", fmt.Errorf("create session: %w", err)
	}

	// Record the session locally so `status --all` and reconnects can find it
	// even when it is gone from the cluster.
	if idx, ierr := newIndex(); ierr == nil {
		now := time.Now()
		_ = idx.Save(string(sid), index.Entry{
			SandboxSessionID: string(sid),
			Backend:          backendName,
			ProjectPath:      projectPath,
			Namespace:        namespaceFlag,
			SandboxName:      string(sid),
			CreatedAt:        now,
			LastActivity:     now,
		})
	}

	return sid, ref, projectPath, nil
}

// runStartSession creates a new remote session for the given backend in the
// current working directory, starts its pod, and launches the command center
// attached straight to it. It is the shared body of the `claude` and `opencode`
// commands; the only per-backend differences are the backend id and whether an
// initial prompt is supplied (opencode owns its own input loop, so it passes
// "" and the dashboard routes it to the external opencode TUI rather than the
// Go transcript).
func runStartSession(cmd *cobra.Command, backendName, prompt, runnerImage, reaperImage, name, model string) error {
	backend, err := newBackend()
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	sid, ref, projectPath, err := provisionSession(ctx, backend, backendName, runnerImage, model)
	if err != nil {
		return err
	}

	// A --name override is persisted through the same path as the TUI rename and
	// the `rename` command, so a custom name wins over the runner auto title.
	applyCreateName(indexTitleStore{}, sid, name)

	// Wait for pod ready.
	if err := backend.Start(ctx, ref); err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	// Launch the command center attached straight to this session. The
	// dashboard's Connector owns the port-forward + client + reconnect, and
	// also starts the idle reaper and file sync; the dashboard list loads
	// underneath so esc detaches to it.
	sess := dashboard.SessionFromState(session.State{
		ID:          sid,
		Backend:     backendName,
		ProjectPath: projectPath,
		Status:      session.StatusRunning,
	})
	return afterTUI(func() error {
		return dashboard.RunAttached(
			backend,
			newDashboardConnector(backend, reaperImage),
			newDashboardCreator(backend, runnerImage, reaperImage),
			sess,
			prompt,
			dashboard.RunOptions{DestroyHook: newLocalDestroyHook(), PreDestroyHook: newPreDestroySyncStop(), TitleStore: indexTitleStore{}, SnapshotStore: indexSnapshotStore{}, ObserverConnector: newDashboardObserverConnector(backend, reaperImage), SyncProber: dashboardSyncProber(), IdleTimeout: defaultReaperIdleTimeout},
		)
	})
}

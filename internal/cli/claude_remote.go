package cli

import (

	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/index"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui"
)

const defaultRunnerImage = "ghcr.io/cullenmcdermott/sandbox-claude-runner:latest"

func newClaudeRemoteCmd() *cobra.Command {
	var (
		prompt      string
		runnerImage string
	)
	cmd := &cobra.Command{
		Use:   "claude [prompt]",
		Short: "Start or attach a remote Claude SDK session and open the local TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				prompt = args[0]
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get cwd: %w", err)
			}
			projectPath, err := filepath.EvalSymlinks(cwd)
			if err != nil {
				projectPath = cwd
			}

			backend, err := newBackend()
			if err != nil {
				return err
			}

			sid := deriveSessionID("claude-sdk", projectPath)

			// Prepare the per-session SSH key for Mutagen before creating the
			// pod, since its public half is baked into the session Secret.
			privPath, authKey, err := ensureSSHKey(string(sid))
			if err != nil {
				return fmt.Errorf("prepare ssh key: %w", err)
			}

			spec := session.Spec{
				ID:           sid,
				ProjectPath:  projectPath,
				Backend:      "claude-sdk",
				RunnerImage:  runnerImage,
				SSHPublicKey: authKey,
			}

			ctx := cmd.Context()

			// Create or reuse the session.
			ref, err := backend.CreateSession(ctx, spec)
			if err != nil {
				return fmt.Errorf("create session: %w", err)
			}

			// Record the session locally so `status --all` and reconnects can
			// find it even when it is gone from the cluster.
			if idx, ierr := newIndex(); ierr == nil {
				now := time.Now()
				_ = idx.Save(string(sid), index.Entry{
					SandboxSessionID: string(sid),
					Backend:          "claude-sdk",
					ProjectPath:      projectPath,
					Namespace:        namespaceFlag,
					SandboxName:      string(sid),
					CreatedAt:        now,
					LastActivity:     now,
				})
			}

			// Wait for pod ready.
			if err := backend.Start(ctx, ref); err != nil {
				return fmt.Errorf("start session: %w", err)
			}

			// Port-forward to the runner.
			specs := k8s.ForwardSpecs(0, 0)
			handles, err := backend.PortForward(ctx, ref, specs)
			if err != nil {
				return fmt.Errorf("port-forward: %w", err)
			}
			defer func() {
				for _, h := range handles {
					h.Close()
				}
			}()

			httpPort := handles[0].LocalPort()

			// Create the runner client. The bearer token lives in the
			// per-session Secret created by CreateSession.
			token, err := backend.RunnerToken(ctx, ref)
			if err != nil {
				return fmt.Errorf("get runner token: %w", err)
			}
			client := runner.New(fmt.Sprintf("http://127.0.0.1:%d", httpPort), token)

			// Check health.
			if err := client.Health(ctx); err != nil {
				return fmt.Errorf("runner health check: %w", err)
			}

			// Bring up file sync over the SSH port-forward. A sync failure is
			// non-fatal (the user may want the TUI to debug), but the workspace
			// will be empty until it succeeds.
			sshPort := handles[1].LocalPort()
			if err := startMutagen(ctx, string(sid), projectPath, privPath, sshPort); err != nil {
				fmt.Fprintf(os.Stderr, "warning: file sync setup failed (workspace may be empty): %v\n", err)
			}

			// Start the TUI.
			model := tui.NewModel(client, ref, projectPath, prompt)
			return tui.Run(model)
		},
	}
	cmd.Flags().StringVar(&runnerImage, "runner-image", defaultRunnerImage, "runner container image")
	return cmd
}

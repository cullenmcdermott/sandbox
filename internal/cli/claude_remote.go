package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

func newClaudeRemoteCmd() *cobra.Command {
	var (
		runnerImage  string
		reaperImage  string
		nameFlag     string
		modelFlag    string
		accountFlag  string
		worktreeFlag string
	)
	cmd := &cobra.Command{
		Use:   "claude [prompt]",
		Short: "Start a new remote Claude SDK session and open the local TUI (resume with `attach`)",
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := ""
			if len(args) > 0 {
				prompt = args[0]
			}
			return runStartSession(cmd, session.BackendClaudeSDK, prompt, runnerImage, reaperImage, nameFlag, modelFlag, accountFlag, worktreeFlag)
		},
	}
	cmd.Flags().StringVar(&runnerImage, "runner-image", client.DefaultRunnerImage, "runner container image")
	cmd.Flags().StringVar(&reaperImage, "reaper-image", k8s.DefaultReaperImage, "idle-reaper container image")
	cmd.Flags().StringVar(&nameFlag, "name", "", "custom display name for the session (overrides the auto title)")
	cmd.Flags().StringVar(&modelFlag, "model", "", "model id/alias for the session default (e.g. opus, sonnet, haiku); empty uses the account default. Switch in-session with /model")
	cmd.Flags().StringVar(&accountFlag, "account", "", "stored Anthropic account (id or label from `sandbox auth list`) to run the session on; empty uses the default account, or the shared cluster Secret when none are stored")
	cmd.Flags().StringVar(&worktreeFlag, "worktree", "auto", "per-session git worktree isolation: auto (worktree iff the project is a git repo), on (require a git repo), off (never)")
	return cmd
}

// newOpencodeCmd starts a new remote opencode-server session and hands the
// terminal to the local `opencode attach` TUI. (Resuming an existing session is
// `sandbox attach`, or picking it from the dashboard.) Unlike `claude`, opencode
// owns its own input loop, so there is no initial-prompt argument: the prompt is
// typed into the opencode TUI itself.
func newOpencodeCmd() *cobra.Command {
	var (
		runnerImage  string
		reaperImage  string
		nameFlag     string
		worktreeFlag string
	)
	cmd := &cobra.Command{
		Use:   "opencode",
		Short: "Start a remote opencode-server session and open the local opencode TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			// opencode manages its own model selection, so there is no --model
			// flag here (TODO.md): pass an empty model. opencode has no Anthropic
			// account step either, so the account selector is always empty.
			return runStartSession(cmd, session.BackendOpenCode, "", runnerImage, reaperImage, nameFlag, "", "", worktreeFlag)
		},
	}
	cmd.Flags().StringVar(&runnerImage, "runner-image", client.DefaultRunnerImage, "runner container image")
	cmd.Flags().StringVar(&reaperImage, "reaper-image", k8s.DefaultReaperImage, "idle-reaper container image")
	cmd.Flags().StringVar(&nameFlag, "name", "", "custom display name for the session (overrides the auto title)")
	cmd.Flags().StringVar(&worktreeFlag, "worktree", "auto", "per-session git worktree isolation: auto (worktree iff the project is a git repo), on (require a git repo), off (never)")
	return cmd
}

// parseWorktreeMode maps the `--worktree` flag string onto a client.WorktreeMode,
// rejecting any value other than auto|on|off (mirrors how the other enum-ish
// flags fail closed on an unknown value). Empty and "auto" both select the
// default WorktreeAuto.
func parseWorktreeMode(s string) (client.WorktreeMode, error) {
	switch s {
	case "", "auto":
		return client.WorktreeAuto, nil
	case "on":
		return client.WorktreeOn, nil
	case "off":
		return client.WorktreeOff, nil
	default:
		return client.WorktreeAuto, fmt.Errorf("invalid --worktree %q: want auto, on, or off", s)
	}
}

// resolveProjectPath returns the absolute, symlink-resolved current working
// directory — the project path mirrored into the session pod so the SDK's
// transcript keys stay host-compatible.
func resolveProjectPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	if p, err := filepath.EvalSymlinks(cwd); err == nil {
		return p, nil
	}
	return cwd, nil
}

// runStartSession creates a new remote session for the given backend in the
// current working directory and launches the command center attached straight to
// it. It is the shared body of the `claude` and `opencode` commands; the only
// per-backend differences are the backend id and whether an initial prompt is
// supplied (opencode owns its own input loop, so it passes "" and the dashboard
// routes it to the external opencode TUI rather than the Go transcript).
//
// Creation and connection go through the public client package — the same path
// an external Go program uses — so the CLI dogfoods the library rather than
// keeping a parallel session engine.
//
// account is the raw `--account` selector (id|label, may be ""); it is honored
// only for the claude backend (opencode has no Anthropic account step). worktree
// is the raw `--worktree` selector (auto|on|off), validated here.
func runStartSession(cmd *cobra.Command, backendName, prompt, runnerImage, reaperImage, name, model, account, worktree string) error {
	worktreeMode, err := parseWorktreeMode(worktree)
	if err != nil {
		return err
	}

	c, backend, err := newClientAndBackend()
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	projectPath, err := resolveProjectPath()
	if err != nil {
		return err
	}

	opts := client.CreateOptions{
		Backend:     backendName,
		ProjectPath: projectPath,
		RunnerImage: runnerImage,
		Model:       model,
		Worktree:    worktreeMode,
	}
	// Resolve the Anthropic account → per-session credential (fail closed): a
	// requested account that can't be resolved/read is a hard error, never a
	// silent fall-back to the shared cluster Secret. Only claude is
	// account-aware; opencode always uses the empty selector.
	if backendName == session.BackendClaudeSDK {
		store, serr := newCredStore()
		if serr != nil {
			return serr
		}
		if aerr := applyAccountSelection(store, account, &opts); aerr != nil {
			return aerr
		}
	}

	// Create-but-don't-start: the pod-ready wait happens inside the dashboard's
	// connect path so the animated connect splash (pod phase + elapsed timer) is
	// on screen during schedule + image-pull instead of a frozen terminal.
	sess, err := c.Create(ctx, opts)
	if err != nil {
		return err
	}
	sid := sess.ID()

	// A --name override is persisted through the same path as the TUI rename and
	// the `rename` command, so a custom name wins over the runner auto title.
	applyCreateName(indexTitleStore{}, sid, name)

	dashSess := dashboard.SessionFromState(session.State{
		ID:          sid,
		Backend:     backendName,
		ProjectPath: projectPath,
		Status:      session.StatusRunning,
	})
	return afterTUI(func() error {
		return dashboard.RunAttached(
			backend,
			newDashboardConnector(c, reaperImage),
			newDashboardCreator(c, runnerImage, reaperImage),
			dashSess,
			prompt,
			dashboard.RunOptions{DestroyHook: newLocalDestroyHook(c), PreDestroyHook: newPreDestroySyncStop(c), TitleStore: indexTitleStore{}, SnapshotStore: indexSnapshotStore{}, EventCache: newIndexEventCache(), DriverStore: indexDriverStore{}, ObserverConnector: newDashboardObserverConnector(c, reaperImage), SyncProber: dashboardSyncProber(), SyncReaper: dashboardSyncReaper(), IdleTimeout: defaultReaperIdleTimeout, AccountStore: newDashboardAccountStore(), WorktreeOps: newWorktreeOps(c)},
		)
	})
}

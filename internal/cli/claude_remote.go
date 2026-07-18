package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
			// [V26] Join all positionals so an unquoted `sandbox claude fix the
			// build` sends the whole prompt, not just "fix" (extra args were
			// silently dropped before).
			prompt := strings.Join(args, " ")
			// claude has no opencode provider step, so the provider selector is empty.
			return runStartSession(cmd, session.BackendClaudeSDK, prompt, runnerImage, reaperImage, nameFlag, modelFlag, accountFlag, worktreeFlag, "")
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
// `sandbox attach`, or picking it from the dashboard.)
//
// Flags mirror `claude` where they map cleanly: --model sets the session default
// (opencode ids are "provider/model", e.g. anthropic/claude-sonnet-4-5) and an
// optional [prompt] positional seeds the first turn. --provider is opencode-only:
// it selects which provider API key the pod receives from the shared
// opencode-credentials Secret (validated fail-closed by client.Create). opencode
// has no Anthropic account step, so the account selector is always empty.
func newOpencodeCmd() *cobra.Command {
	var (
		runnerImage  string
		reaperImage  string
		nameFlag     string
		modelFlag    string
		providerFlag string
		worktreeFlag string
	)
	cmd := &cobra.Command{
		Use:   "opencode [prompt]",
		Short: "Start a remote opencode-server session and open the local opencode TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			// [V26] Join all positionals so an unquoted multi-word prompt is sent
			// whole rather than dropping everything after the first word.
			prompt := strings.Join(args, " ")
			return runStartSession(cmd, session.BackendOpenCode, prompt, runnerImage, reaperImage, nameFlag, modelFlag, "", worktreeFlag, providerFlag)
		},
	}
	cmd.Flags().StringVar(&runnerImage, "runner-image", client.DefaultRunnerImage, "runner container image")
	cmd.Flags().StringVar(&reaperImage, "reaper-image", k8s.DefaultReaperImage, "idle-reaper container image")
	cmd.Flags().StringVar(&nameFlag, "name", "", "custom display name for the session (overrides the auto title)")
	cmd.Flags().StringVar(&modelFlag, "model", "", "model id for the session default as provider/model (e.g. anthropic/claude-sonnet-4-5); empty uses the opencode server default")
	cmd.Flags().StringVar(&providerFlag, "provider", "", "opencode model provider whose API key the pod receives from the shared Secret: anthropic (default), openai, or opencode-zen")
	cmd.Flags().StringVar(&worktreeFlag, "worktree", "auto", "per-session git worktree isolation: auto (worktree iff the project is a git repo), on (require a git repo), off (never)")
	return cmd
}

// newCodexCmd starts a new remote codex-app-server session, mirroring
// newOpencodeCmd. Flags mirror `opencode` minus the provider flag (codex has no
// provider selector — its credential is the ChatGPT-OAuth auth.json, or the shared
// OPENAI_API_KEY fallback). Unlike claude/opencode it takes NO positional prompt:
// codex owns its own interactive input loop, so there is no headless first turn to
// seed. Phase 1 is Go-side plumbing — the interactive codex pane is a later wave, so
// the post-create attach UX is degraded (the dashboard connects but has no codex
// turn path yet); the session still creates and health-checks.
func newCodexCmd() *cobra.Command {
	var (
		runnerImage  string
		reaperImage  string
		nameFlag     string
		modelFlag    string
		worktreeFlag string
	)
	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Start a remote codex-app-server session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// No initial prompt: codex owns its own input loop.
			return runStartSession(cmd, session.BackendCodex, "", runnerImage, reaperImage, nameFlag, modelFlag, "", worktreeFlag, "")
		},
	}
	cmd.Flags().StringVar(&runnerImage, "runner-image", client.DefaultRunnerImage, "runner container image")
	cmd.Flags().StringVar(&reaperImage, "reaper-image", k8s.DefaultReaperImage, "idle-reaper container image")
	cmd.Flags().StringVar(&nameFlag, "name", "", "custom display name for the session (overrides the auto title)")
	cmd.Flags().StringVar(&modelFlag, "model", "", "model id for the session default; empty uses the codex server default. Switch in-session with /model")
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
// is the raw `--worktree` selector (auto|on|off), validated here. opencodeProvider
// is the raw `--provider` selector (may be ""); honored only for opencode and
// validated fail-closed inside client.Create (ErrInvalidOpencodeProvider).
func runStartSession(cmd *cobra.Command, backendName, prompt, runnerImage, reaperImage, name, model, account, worktree, opencodeProvider string) error {
	worktreeMode, err := parseWorktreeMode(worktree)
	if err != nil {
		return err
	}

	c, backend, err := newClientAndBackend()
	if err != nil {
		return err
	}

	// Best-effort orphan-sync GC at startup (SF1), matching bare-`sandbox` and
	// `attach`: clean up syncs left by destroyed/reaped/dev-reset pods now rather
	// than waiting for the first in-TUI reconcile. Backgrounded + bounded so it
	// never delays session creation.
	startupSyncGC()

	ctx := cmd.Context()
	projectPath, err := resolveProjectPath()
	if err != nil {
		return err
	}

	opts := client.CreateOptions{
		Backend:          backendName,
		ProjectPath:      projectPath,
		RunnerImage:      runnerImage,
		Model:            model,
		Worktree:         worktreeMode,
		OpencodeProvider: opencodeProvider,
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

	// opencode: deliver a positional prompt as a headless first turn through the
	// runner's turn adapter BEFORE the TUI launches. The claude path hands its
	// prompt to the dashboard transcript (initialPrompt, submitted once the stream
	// is live), but opencode sessions render through the external `opencode attach`
	// pane, which has no such hook — passing the prompt through RunAttached would
	// silently drop it. This trades the animated connect splash for plain stderr
	// progress (pod phase lines) on the prompt-seeded path only; `opencode attach
	// --continue` then attaches to the in-flight turn. A failure is a hard error,
	// never a silent drop; the session stays up for a manual `sandbox attach`.
	if backendName == session.BackendOpenCode && prompt != "" {
		if err := submitInitialOpencodeTurn(ctx, c, backend, sid, prompt, cmd.ErrOrStderr()); err != nil {
			return fmt.Errorf("submit initial prompt: %w\nsession %s is still running — `sandbox attach %s` and type the prompt there", err, sid, sid)
		}
		prompt = "" // delivered; RunAttached must not carry it too
	}

	dashSess := dashboard.SessionFromState(session.State{
		ID:          sid,
		Backend:     backendName,
		ProjectPath: projectPath,
		Status:      session.StatusRunning,
	})
	return afterTUI(func() error {
		return dashboard.RunAttached(
			newClientLifecycleBackend(c, backend),
			newDashboardConnector(c, reaperImage),
			newDashboardCreator(c, runnerImage, reaperImage),
			dashSess,
			prompt,
			dashboard.RunOptions{TitleStore: indexTitleStore{}, SnapshotStore: indexSnapshotStore{}, EventCache: newIndexEventCache(), DriverStore: indexDriverStore{}, ObserverConnector: newDashboardObserverConnector(c, reaperImage), SyncProber: dashboardSyncProber(), SyncReaper: dashboardSyncReaper(), IdleTimeout: defaultReaperIdleTimeout, AccountStore: newDashboardAccountStore(), WorktreeOps: newWorktreeOps(c)},
		)
	})
}

// submitInitialOpencodeTurn posts `sandbox opencode "prompt"`'s positional prompt
// as a headless first turn against a freshly created session, mirroring the
// hidden `sandbox turn` command's flow (turn.go): pod-ready wait → dial → health
// → StartTurn. Fire-and-return — POST /turns registers the turn and the runner
// drives it server-side, so the dial is torn down immediately and the TUI attach
// that follows finds the turn already streaming. Progress goes to errOut (the
// alt-screen TUI is not up yet, so plain lines are the honest substitute for the
// connect splash).
func submitInitialOpencodeTurn(ctx context.Context, c *client.Client, backend *k8s.Backend, sid session.ID, prompt string, errOut io.Writer) error {
	ref := session.Ref{ID: sid}
	// Block until the pod is running (schedule + image pull) — DialRunner's
	// port-forward needs a live pod, and waitHealthy's budget is far shorter than
	// a cold image pull. Same wait the dashboard connect path performs, minus the
	// animation.
	lastPhase := ""
	if err := backend.StartWithProgress(ctx, ref, func(detail string) {
		if detail != lastPhase {
			lastPhase = detail
			fmt.Fprintf(errOut, "pod: %s\n", detail)
		}
	}); err != nil {
		return fmt.Errorf("wait for pod: %w", err)
	}
	rc, cleanup, err := c.DialRunner(ctx, ref)
	if err != nil {
		return fmt.Errorf("dial runner: %w", err)
	}
	defer cleanup()
	turn, err := startPromptTurn(ctx, rc, ref, prompt)
	if err != nil {
		return err
	}
	fmt.Fprintf(errOut, "initial prompt submitted (turn %s)\n", turn.Turn)
	return nil
}

// startPromptTurn health-waits the runner then posts the prompt as a turn. Split
// from submitInitialOpencodeTurn on the client.RunnerClient interface seam so it
// is unit-testable with a fake runner (the pod wait + dial above need a cluster).
// No mode/model overrides: the opencode turn adapter ignores mode (permissions
// are OPENCODE_AUTO_PERMISSION-governed) and the session's default model was
// already provisioned via CreateOptions.Model.
func startPromptTurn(ctx context.Context, rc client.RunnerClient, ref session.Ref, prompt string) (client.TurnRef, error) {
	if err := waitHealthy(ctx, rc); err != nil {
		return client.TurnRef{}, fmt.Errorf("runner health: %w", err)
	}
	turn, err := rc.StartTurn(ctx, ref, client.TurnInput{Prompt: prompt})
	if err != nil {
		return client.TurnRef{}, fmt.Errorf("start turn: %w", err)
	}
	return turn, nil
}

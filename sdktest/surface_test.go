package sdktest

// surface_test.go — compile-time pins of the public SDK surface. Every var
// below re-states a signature the SDK promises; changing that signature (or
// making it require an internal/... type an outside module cannot name) fails
// THIS module's compilation, which is the earliest possible "you just broke a
// consumer" signal. Behavior is covered in conformance_test.go.
//
// When a pin fails to compile because of an INTENTIONAL break: fix the pin in
// the same change and call the break out in the commit/PR — that is the
// consumer-visible changelog entry.
//
// client.WithBackend now takes the exported client.Backend interface (pinned
// below), so its signature is nameable here. Known caveat (deliberately not
// pinned): client.Backend is not IMPLEMENTABLE outside the main module —
// EnsureReaper's argument is internal/k8s.ReaperOptions, which an external
// consumer cannot name — so the interface is an in-module fake-injection seam,
// not yet a third-party backend contract (tracked in TODO.md §8). Likewise
// client.WithRESTConfig takes *rest.Config.

import (
	"context"
	"time"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/client/cred"
	"github.com/cullenmcdermott/sandbox/client/models"
)

// --- client: constructor + options -----------------------------------------

var (
	_ func(...client.Option) (*client.Client, error) = client.New

	_ func(string) client.Option        = client.WithContext
	_ func(string) client.Option        = client.WithNamespace
	_ func(string) client.Option        = client.WithKubeconfig
	_ func(string) client.Option        = client.WithRunnerImage
	_ func(string) client.Option        = client.WithReaperImage
	_ func(string) client.Option        = client.WithReaperImagePullPolicy
	_ func(string) client.Option        = client.WithStateDir
	_ func(time.Duration) client.Option = client.WithIdleTimeout

	// WithBackend takes the narrowed public client.Backend interface (a change
	// from the old *internal/k8s.Backend). Retyping it — or widening client.Backend
	// so the k8s backend no longer satisfies it — breaks this pin.
	_ func(client.Backend) client.Option = client.WithBackend

	// Backend.Watch is part of the cluster-side seam: a method expression on the
	// interface pins that Watch stays on client.Backend with this signature, so
	// dropping or retyping it fails to compile HERE. (The interface isn't
	// implementable outside the module — see the header note — so this is the
	// way to pin the method rather than a consumer implementation.)
	_ func(client.Backend, context.Context) (<-chan client.StateEvent, error) = client.Backend.Watch
)

// --- client: Client method set ----------------------------------------------

var (
	_ func(*client.Client, context.Context, client.CreateOptions) (*client.Session, error) = (*client.Client).Create
	_ func(*client.Client, client.ID) *client.Session                                      = (*client.Client).Open
	_ func(*client.Client, context.Context) ([]client.State, error)                        = (*client.Client).List
	_ func(*client.Client, context.Context) (<-chan client.StateEvent, error)              = (*client.Client).Watch
	_ func(*client.Client, context.Context, client.ID) (client.State, error)               = (*client.Client).Status
	_ func(*client.Client, context.Context, client.ID) error                               = (*client.Client).Suspend
	_ func(*client.Client, context.Context, client.ID) error                               = (*client.Client).Resume
	_ func(*client.Client, context.Context, client.ID) error                               = (*client.Client).Destroy
	_ func(*client.Client, context.Context, client.ID) error                               = (*client.Client).SyncPause
	_ func(*client.Client, context.Context, client.ID) error                               = (*client.Client).SyncResume
	_ func(*client.Client, context.Context, client.ID) error                               = (*client.Client).SyncTerminate
	_ func(*client.Client, context.Context, client.ID) ([]byte, error)                     = (*client.Client).SyncStatus
	_ func(*client.Client, context.Context, client.ID)                                     = (*client.Client).StopSync
	_ func(*client.Client, client.ID)                                                      = (*client.Client).RemoveLocalState
	_ func(*client.Client) string                                                          = (*client.Client).Namespace
	_ func(*client.Client) string                                                          = (*client.Client).StateDir
)

// --- client: Session method set ----------------------------------------------

var (
	_ func(*client.Session, context.Context, client.ConnectOptions) (*client.Connection, error)  = (*client.Session).Connect
	_ func(*client.Session) error                                                                = (*client.Session).Close
	_ func(*client.Session) client.ID                                                            = (*client.Session).ID
	_ func(*client.Session) client.Ref                                                           = (*client.Session).Ref
	_ func(*client.Session) string                                                               = (*client.Session).ProjectPath
	_ func(*client.Session) client.RunnerClient                                                  = (*client.Session).Runner
	_ func(*client.Session, context.Context, client.TurnInput) (client.TurnRef, error)           = (*client.Session).StartTurn
	_ func(*client.Session, context.Context, client.TurnRef) error                               = (*client.Session).Interrupt
	_ func(*client.Session, context.Context) error                                               = (*client.Session).CancelTurn
	_ func(*client.Session, context.Context, uint64) (<-chan client.Event, error)                = (*client.Session).Events
	_ func(*client.Session, context.Context, uint64) (<-chan client.Event, error)                = (*client.Session).EventsPassive
	_ func(*client.Session, context.Context, client.PermissionDecision) error                    = (*client.Session).ResolvePermission
	_ func(*client.Session, context.Context) (client.State, error)                               = (*client.Session).SessionState
	_ func(*client.Session, context.Context) (client.IdleStatus, error)                          = (*client.Session).Idle
	_ func(*client.Session, context.Context, string) (client.ExecResult, error)                  = (*client.Session).Exec
	_ func(*client.Session, context.Context, client.AutopilotRequest) (client.State, error)      = (*client.Session).ArmAutopilot
	_ func(*client.Session, context.Context) (client.State, error)                               = (*client.Session).DisarmAutopilot
	_ func(*client.Session) string                                                               = (*client.Session).WorktreePath
	_ func(*client.Session, context.Context) (client.WorktreeStatus, error)                      = (*client.Session).WorktreeStatus
	_ func(*client.Session, context.Context, client.ConvertOptions) (client.BranchResult, error) = (*client.Session).ConvertToBranch
	_ func(*client.Client, context.Context, client.ReapOptions) ([]client.ReapedWorktree, error) = (*client.Client).ReapWorktrees

	// §8 interactive-shell surface: the one-call PTY Shell and the lower
	// SSHTarget seam it is built on (the CLI `sandbox shell` dogfoods Shell). The
	// interactive path can only be signature-pinned here — it needs a real pod
	// and PTY — while SSHTarget resolution is behavior-tested in client's
	// TestSSHTarget against a fake backend.
	_ func(*client.Session, context.Context, client.ShellOptions) (int, error)  = (*client.Session).Shell
	_ func(*client.Session, context.Context) (*client.SSHTarget, func(), error) = (*client.Session).SSHTarget
)

// --- client: per-session worktree surface (wave 2) --------------------------

var (
	// WorktreeMode enum + the three modes. Retyping the constant or dropping a
	// mode breaks a consumer here first.
	_ client.WorktreeMode = client.WorktreeAuto
	_ client.WorktreeMode = client.WorktreeOff
	_ client.WorktreeMode = client.WorktreeOn

	// Worktree sentinel errors a consumer branches on with errors.Is.
	_ error = client.ErrNotAGitRepo
	_ error = client.ErrWorktreeExists
	_ error = client.ErrWorktreeDirty
	// Wave 3: the deterministic git-surface sentinels.
	_ error = client.ErrNoWorktree
	_ error = client.ErrInvalidBranchName
	_ error = client.ErrBranchNameTaken
)

// --- client: per-session worktree surface (wave 3) --------------------------

// Option/result struct-literal pins: removing or retyping a field breaks these.
var (
	_ = client.WorktreeStatus{
		Path:    "/wt",
		Branch:  "sandbox/x",
		Dirty:   true,
		Changed: []string{"a.go"},
	}
	_ = client.ConvertOptions{
		BranchName: "feat/x",
		Message:    "msg",
	}
	_ = client.BranchResult{
		Branch:    "feat/x",
		Committed: true,
		CommitSHA: "deadbeef",
	}
	_ = client.ReapOptions{
		DryRun: true,
	}
	_ = client.ReapedWorktree{
		SessionID: "x",
		Path:      "/wt",
		Branch:    "sandbox/x",
		Action:    "removed",
		CommitSHA: "deadbeef",
	}
)

// --- client: option/result structs keep their fields -------------------------

// Struct-literal pins: removing or retyping a field breaks these.
var _ = client.CreateOptions{
	Backend:             client.BackendClaudeSDK,
	ProjectPath:         "/work/repo",
	RunnerImage:         "img",
	ImagePullPolicy:     "IfNotPresent",
	Model:               "opus",
	AnthropicAuth:       "oauth",
	AnthropicAccountID:  "acct-1234",
	AnthropicCredential: []byte("secret"),
	CodexAccountID:      "acct-chatgpt",
	CodexAuthJSON:       []byte("auth-json"),
	OpencodeProvider:    client.OpencodeProviderAnthropic,
	StorageClass:        "fast",
	StorageGiB:          10,
	Worktree:            client.WorktreeAuto,
}

// OpencodeProvider vocabulary: re-exported so consumers pass a named constant
// as CreateOptions.OpencodeProvider instead of a raw string. Removing or
// renaming any of the three breaks the build here.
var (
	_ = client.OpencodeProviderAnthropic
	_ = client.OpencodeProviderOpenAI
	_ = client.OpencodeProviderZen
)

var _ = client.ConnectOptions{
	ProjectPath:           "/work/repo",
	ReaperImage:           "img",
	ReaperImagePullPolicy: "Never",
	IdleTimeout:           time.Minute,
	Observer:              true,
	OnPhase:               func(client.Stage, string) {},
}

// §8 interactive-shell option/result structs: removing or retyping a field
// breaks a consumer here first.
var _ = client.ShellOptions{
	Command: "uname -a",
	Stdin:   nil,
	Stdout:  nil,
	Stderr:  nil,
	Term:    "xterm-256color",
}

var _ = client.SSHTarget{
	Host:         "127.0.0.1",
	Port:         2222,
	User:         "root",
	IdentityFile: "/state/sess/id_ed25519",
}

// Spec/State are public via client aliases; pin the worktree data-model split
// (repo-root ProjectPath vs bind-mount/sync WorkspacePath) so removing or
// retyping either field breaks a consumer here first.
var _ = client.Spec{
	ProjectPath:   "/work/repo",
	WorkspacePath: "/work/repo",
}

var _ = client.State{
	ProjectPath:   "/work/repo",
	WorkspacePath: "/work/repo",
}

// StateEvent is the cluster-watch delivery unit (Client.Watch): a snapshot-or-
// tombstone per session. Removing or retyping either field breaks a consumer's
// watch read-model here first.
var _ = client.StateEvent{
	State:   client.State{},
	Deleted: true,
}

var _ = client.Connection{
	Runner:   nil,
	Endpoint: "http://127.0.0.1:8787",
	Backend:  client.BackendClaudeSDK,
	External: (*client.ExternalCreds)(nil),
	Warning:  "",
}

var _ = client.ExternalCreds{Username: "opencode", Password: "", URL: "http://127.0.0.1:4096"}

var _ = client.TurnInput{
	Prompt:         "fix the build",
	Resume:         client.TurnID("t1"),
	AllowedTools:   []string{"Bash"},
	ApprovalPolicy: client.ApprovalAcceptEdits,
	Model:          "opus",
}

// ApprovalPolicy enum + Activity enum are part of the owned public vocabulary
// (§8 De-Claude): retyping a constant or dropping one fails the build here.
var (
	_ client.ApprovalPolicy = client.ApprovalDefault
	_ client.ApprovalPolicy = client.ApprovalAcceptEdits
	_ client.ApprovalPolicy = client.ApprovalPlan
	_ client.ApprovalPolicy = client.ApprovalBypass

	_ client.Activity = client.ActivityIdle
	_ client.Activity = client.ActivityBusy
	_ client.Activity = client.ActivityError
)

// State carries the renamed AgentSessionID + the distinct Activity field (D9).
var _ = client.State{
	Status:         client.StatusRunning,
	Activity:       client.ActivityIdle,
	AgentSessionID: "sdk-session-uuid",
}

// --- client: Anthropic account selection --------------------------------------

var (
	_ func(*client.CreateOptions, cred.Store, string) error = (*client.CreateOptions).UseAnthropicAccount
	_ func(*client.CreateOptions, cred.Store, string) error = (*client.CreateOptions).SelectAnthropicAccount
	_ error                                                 = client.ErrNoDefaultAnthropicAccount
)

// --- client: codex backend + credential contract (Phase 1) --------------------

var (
	// BackendCodex re-export: dropping or retyping it breaks a consumer here.
	_ string = client.BackendCodex

	// Codex account/credential sentinels a consumer branches on with errors.Is.
	_ error = client.ErrCodexCredentialMissing
	_ error = client.ErrCodexAccountRequired
	_ error = client.ErrInvalidCodexAccountID
)

// --- cred: store + account model ----------------------------------------------

var (
	_ func() (cred.Store, error)                         = cred.DefaultStore
	_ func(string) cred.Store                            = cred.NewFileStore
	_ func(string, cred.AccountType) cred.Account        = cred.NewAccount
	_ func([]cred.Account, string) (cred.Account, error) = cred.Resolve
	_ func(string) (string, error)                       = cred.ParseSetupToken
	_ func(string) (string, error)                       = cred.ValidateConsoleKey
	_ func(cred.AccountType) (string, error)             = cred.AuthForType
)

var _ = cred.Account{
	ID:        "acct-1234",
	Label:     "work",
	Type:      cred.AccountConsole,
	CreatedAt: time.Time{},
}

// --- models: context-window + pricing resolver --------------------------------

// Limit resolves a model id to its context limit + per-Mtok prices; the struct
// literal pins every Info field. Retyping either breaks a consumer here.
var (
	_ func(string) models.Info = models.Limit

	_ = models.Info{
		ContextLimit: 200000,
		InputPrice:   0,
		OutputPrice:  0,
	}
)

var (
	_ cred.AccountType = cred.AccountSubscription
	_ cred.AccountType = cred.AccountConsole

	_ error = cred.ErrNoAccounts
	_ error = cred.ErrNotFound
	_ error = cred.ErrAccountExists
	_ error = cred.ErrUnknownAccount
	_ error = cred.ErrInvalidAccountID
	_ error = cred.ErrInvalidAccountType
	_ error = cred.ErrInvalidSecret
	_ error = cred.ErrNoSetupToken
	_ error = cred.ErrMalformedToken
	_ error = cred.ErrInvalidConsoleKey
)

// consumerStore proves cred.Store stays implementable by consumers: WIDENING
// the interface (adding a method) is a breaking change and must fail here.
type consumerStore struct{}

func (consumerStore) List() ([]cred.Account, error)  { return nil, nil }
func (consumerStore) Add(cred.Account, []byte) error { return nil }
func (consumerStore) Secret(string) ([]byte, error)  { return nil, nil }
func (consumerStore) Remove(string) error            { return nil }
func (consumerStore) SetDefault(string) error        { return nil }
func (consumerStore) Default() (string, error)       { return "", nil }

var _ cred.Store = consumerStore{}

// consumerRunnerClient proves client.RunnerClient stays implementable by outside
// consumers (they build fakes of it for Create/Connect orchestration tests):
// WIDENING the interface (adding a method) is a breaking change and must fail
// HERE, not silently at every downstream fake. Mirrors consumerStore above.
type consumerRunnerClient struct{}

func (consumerRunnerClient) Health(context.Context) error { return nil }
func (consumerRunnerClient) StartTurn(context.Context, client.Ref, client.TurnInput) (client.TurnRef, error) {
	return client.TurnRef{}, nil
}
func (consumerRunnerClient) InterruptTurn(context.Context, client.Ref, client.TurnRef) error {
	return nil
}
func (consumerRunnerClient) ResolvePermission(context.Context, client.Ref, client.PermissionDecision) error {
	return nil
}
func (consumerRunnerClient) Events(context.Context, client.Ref, uint64) (<-chan client.Event, error) {
	return nil, nil
}
func (consumerRunnerClient) EventsPassive(context.Context, client.Ref, uint64) (<-chan client.Event, error) {
	return nil, nil
}
func (consumerRunnerClient) SessionState(context.Context, client.Ref) (client.State, error) {
	return client.State{}, nil
}
func (consumerRunnerClient) Exec(context.Context, client.Ref, string) (client.ExecResult, error) {
	return client.ExecResult{}, nil
}
func (consumerRunnerClient) Idle(context.Context, client.Ref) (client.IdleStatus, error) {
	return client.IdleStatus{}, nil
}
func (consumerRunnerClient) ArmAutopilot(context.Context, client.Ref, client.AutopilotRequest) (client.State, error) {
	return client.State{}, nil
}
func (consumerRunnerClient) DisarmAutopilot(context.Context, client.Ref) (client.State, error) {
	return client.State{}, nil
}

var _ client.RunnerClient = consumerRunnerClient{}

// --- client: autopilot arm/disarm surface (server-side loop ADR) -------------

var (
	// Kind constants + the arm/disarm sentinel errors a consumer branches on.
	_       = client.AutopilotKindLoop
	_       = client.AutopilotKindGoal
	_ error = client.ErrAutopilotUnsupported
	_ error = client.ErrAutopilotNotArmed

	// Struct-literal pins: removing or retyping a field breaks a consumer here.
	_ = client.AutopilotRequest{
		Kind:          client.AutopilotKindLoop,
		Prompt:        "keep working through TODO.md",
		Sentinel:      "ALL_DONE",
		IntervalMs:    0,
		Overrides:     client.AutopilotOverrides{Model: "opus", Effort: "high", Mode: "acceptEdits"},
		MaxIterations: 50,
		TokenBudget:   nil,
	}
	_ = client.Capabilities{Autopilot: true}
	// State carries the backend capability map the TUI reads to pick a code path.
	_ = client.State{Capabilities: client.Capabilities{Autopilot: true}}
	// The autopilot.state event + its payload, for a consumer rendering driver state.
	_ client.EventType = client.EventAutopilotState
	_                  = client.AutopilotStatePayload{State: "armed", Kind: "loop", Reason: "", Iteration: 0, Gen: 1}
)

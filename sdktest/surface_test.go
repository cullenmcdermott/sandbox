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
// Known caveat (deliberately not pinned): client.WithBackend and
// client.WithRESTConfig take *internal/k8s.Backend / *rest.Config; the former
// cannot be named outside the main module at all, so it is unusable to
// external consumers (tracked in TODO.md).

import (
	"context"
	"time"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/client/cred"
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
)

// --- client: Client method set ----------------------------------------------

var (
	_ func(*client.Client, context.Context, client.CreateOptions) (*client.Session, error) = (*client.Client).Create
	_ func(*client.Client, client.ID) *client.Session                                      = (*client.Client).Open
	_ func(*client.Client, context.Context) ([]client.State, error)                        = (*client.Client).List
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
	_ func(*client.Session, context.Context, client.ConnectOptions) (*client.Connection, error) = (*client.Session).Connect
	_ func(*client.Session) error                                                               = (*client.Session).Close
	_ func(*client.Session) client.ID                                                           = (*client.Session).ID
	_ func(*client.Session) client.Ref                                                          = (*client.Session).Ref
	_ func(*client.Session) string                                                              = (*client.Session).ProjectPath
	_ func(*client.Session) client.RunnerClient                                                 = (*client.Session).Runner
	_ func(*client.Session, context.Context, client.TurnInput) (client.TurnRef, error)          = (*client.Session).StartTurn
	_ func(*client.Session, context.Context, client.TurnRef) error                              = (*client.Session).Interrupt
	_ func(*client.Session, context.Context) error                                              = (*client.Session).CancelTurn
	_ func(*client.Session, context.Context, uint64) (<-chan client.Event, error)               = (*client.Session).Events
	_ func(*client.Session, context.Context, uint64) (<-chan client.Event, error)               = (*client.Session).EventsPassive
	_ func(*client.Session, context.Context, client.PermissionDecision) error                   = (*client.Session).ResolvePermission
	_ func(*client.Session, context.Context) (client.State, error)                              = (*client.Session).SessionState
	_ func(*client.Session, context.Context) (client.IdleStatus, error)                         = (*client.Session).Idle
	_ func(*client.Session, context.Context, string) (client.ExecResult, error)                 = (*client.Session).Exec
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
	StorageClass:        "fast",
	StorageGiB:          10,
}

var _ = client.ConnectOptions{
	ProjectPath:           "/work/repo",
	ReaperImage:           "img",
	ReaperImagePullPolicy: "Never",
	IdleTimeout:           time.Minute,
	Observer:              true,
	OnPhase:               func(client.Stage, string) {},
}

var _ = client.Connection{
	Runner:   nil,
	Endpoint: "http://127.0.0.1:8787",
	Backend:  client.BackendClaudeSDK,
	Opencode: (*client.OpencodeCreds)(nil),
	Warning:  "",
}

var _ = client.TurnInput{
	Prompt:       "fix the build",
	Resume:       client.TurnID("t1"),
	AllowedTools: []string{"Bash"},
	Mode:         "acceptEdits",
	Model:        "opus",
}

// --- client: Anthropic account selection --------------------------------------

var (
	_ func(*client.CreateOptions, cred.Store, string) error = (*client.CreateOptions).UseAnthropicAccount
	_ func(*client.CreateOptions, cred.Store, string) error = (*client.CreateOptions).SelectAnthropicAccount
	_ error                                                 = client.ErrNoDefaultAnthropicAccount
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

var _ client.RunnerClient = consumerRunnerClient{}

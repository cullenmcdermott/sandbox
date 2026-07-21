package client

import (
	"errors"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// Typed errors callers can branch on with errors.Is. They are the public,
// stable error vocabulary for the package — the idiomatic Go alternative to a
// subprocess JSON error-code envelope.
var (
	// ErrSessionGone reports that a session no longer exists in the cluster (its
	// Sandbox CRD was deleted). It is permanent and non-retryable. Returned by
	// Connect against a gone session; re-exported from the session model so
	// errors.Is works across the seam.
	ErrSessionGone = session.ErrSessionGone

	// ErrNoActiveTurn is returned by Session.CancelTurn when there is no turn in
	// flight to interrupt.
	ErrNoActiveTurn = errors.New("sandbox: no active turn")

	// ErrSessionSuspended is returned by an Observer Connect against a suspended
	// session: observer connects are read-only and never resume (the pod is gone,
	// so there is nothing to observe). Resume the session or use a full Connect.
	ErrSessionSuspended = errors.New("sandbox: session is suspended")

	// ErrProjectPathRequired is returned by Create when CreateOptions.ProjectPath
	// is empty. The project path is the absolute workspace path mirrored into the
	// pod; the library does not assume a current working directory.
	ErrProjectPathRequired = errors.New("sandbox: CreateOptions.ProjectPath is required")

	// ErrNotConnected is returned by the Session turn/stream convenience methods
	// when called before a successful Connect.
	ErrNotConnected = errors.New("sandbox: session not connected (call Connect first)")

	// ErrInvalidImagePullPolicy is returned when an image pull-policy override
	// (CreateOptions.ImagePullPolicy, WithReaperImagePullPolicy, or
	// ConnectOptions.ReaperImagePullPolicy) is a non-empty value other than the
	// exact spellings "Always", "IfNotPresent", or "Never".
	ErrInvalidImagePullPolicy = errors.New("sandbox: invalid image pull policy")

	// ErrInvalidAnthropicAuth is returned by Create when CreateOptions.AnthropicAuth
	// is a non-empty value other than the exact spellings "oauth" or "api-key".
	// A typo like "apikey" errors here rather than silently falling through to the
	// default OAuth path.
	ErrInvalidAnthropicAuth = errors.New("sandbox: invalid anthropic auth")

	// ErrInvalidOpencodeProvider is returned by Create when
	// CreateOptions.OpencodeProvider is a non-empty value other than the exact
	// session.OpencodeProvider* spellings ("anthropic", "openai",
	// "opencode-zen"). A typo errors here rather than silently selecting the
	// backend's Anthropic default — and with it a different provider's
	// credential than the caller intended.
	ErrInvalidOpencodeProvider = errors.New("sandbox: invalid opencode provider")

	// ErrAnthropicCredentialMissing is returned by Create when
	// CreateOptions.AnthropicAccountID names an account but AnthropicCredential
	// is empty — the resolver produced no bytes (e.g. a denied Keychain read or
	// manifest/store drift). Create fails closed rather than silently launching
	// on the shared-Secret fallback, since a wrong-account session (personal vs
	// work billing/data) is a worse failure than a refused launch. The error
	// carries no credential material.
	ErrAnthropicCredentialMissing = errors.New("sandbox: anthropic account selected but credential is empty")

	// ErrAnthropicAccountRequired is returned by Create when AnthropicCredential
	// bytes are supplied without an AnthropicAccountID. The account id is the
	// branch signal and the Secret label the backend keys rotation/logout on, so
	// credential bytes with no id would provision an unlabeled, unenumerable
	// Secret — rejected. The error carries no credential material.
	ErrAnthropicAccountRequired = errors.New("sandbox: anthropic credential supplied without an account id")

	// ErrInvalidAnthropicAccountID is returned by Create when
	// CreateOptions.AnthropicAccountID is not a valid Kubernetes label value
	// (the id labels the per-session Secret for rotation/logout enumeration).
	// The cred store guarantees DNS-safe ids, so this only fires on ids from
	// other sources; failing fast here beats an apiserver Invalid error surfacing
	// mid-create. The error carries no credential material.
	ErrInvalidAnthropicAccountID = errors.New("sandbox: anthropic account id is not a valid kubernetes label value")

	// ErrCodexCredentialMissing is returned by Create when
	// CreateOptions.CodexAccountID names an account but CodexAuthJSON is empty —
	// the resolver produced no bytes (e.g. a denied Keychain read or manifest/store
	// drift). Create fails closed rather than silently launching on the shared
	// OPENAI_API_KEY fallback, since a wrong-account session is a worse failure than
	// a refused launch. The error carries no credential material.
	ErrCodexCredentialMissing = errors.New("sandbox: codex account selected but credential is empty")

	// ErrCodexAccountRequired is returned by Create when CodexAuthJSON bytes are
	// supplied without a CodexAccountID. The account id is the branch signal and the
	// Secret label the backend keys rotation/logout on, so credential bytes with no
	// id would provision an unlabeled, unenumerable Secret — rejected. The error
	// carries no credential material.
	ErrCodexAccountRequired = errors.New("sandbox: codex credential supplied without an account id")

	// ErrInvalidCodexAccountID is returned by Create when
	// CreateOptions.CodexAccountID is not a valid Kubernetes label value (the id
	// labels the per-session Secret for rotation/logout enumeration). The cred store
	// guarantees DNS-safe ids, so this only fires on ids from other sources; failing
	// fast here beats an apiserver Invalid error surfacing mid-create. The error
	// carries no credential material.
	ErrInvalidCodexAccountID = errors.New("sandbox: codex account id is not a valid kubernetes label value")

	// ErrClaudePaneCredentialMissing is returned by Create for a claude-pane
	// session missing the full provisioning material (the credential +
	// oauthAccount documents). The interactive pane authenticates ONLY from the
	// materialized credentials file, and the pod template references the Secret
	// keys non-Optionally — creating without material would stall the pod in
	// CreateContainerConfigError; failing here is the actionable version.
	// Populate CreateOptions via SelectClaudePaneMaterial (the system Claude
	// Code login — the Max-mode source) or UseClaudePaneMaterial. The error
	// carries no credential material.
	ErrClaudePaneCredentialMissing = errors.New("sandbox: claude-pane session requires full Claude Code credential material — log in with `claude` on this machine so the host login can be provisioned (CreateOptions.SelectClaudePaneMaterial)")

	// ErrNotAGitRepo is returned by Create when CreateOptions.Worktree is
	// WorktreeOn but ProjectPath is not inside a git work tree (or the git binary
	// is unavailable), so a per-session worktree cannot be created. Under
	// WorktreeAuto the same condition instead falls back silently to the no-worktree
	// behavior (WorkspacePath == ProjectPath), never surfacing this error.
	ErrNotAGitRepo = errors.New("sandbox: project path is not a git repository")

	// ErrWorktreeExists is returned by Create when the target per-session worktree
	// path or its auto-branch (sandbox/<id>) already exists, so `git worktree add`
	// refuses. It signals a residue from a prior create with the same id — destroy
	// the stale session (or reap the worktree) and retry.
	ErrWorktreeExists = errors.New("sandbox: worktree path or branch already exists")

	// ErrWorktreeDirty is returned by a teardown/convert path that would discard
	// uncommitted work in a worktree and was not authorized to capture it. Destroy
	// defaults to a silent WIP commit to the session branch rather than returning
	// this (I2 never-lose), so it is part of the stable error vocabulary for
	// callers that opt into a block-instead-of-commit policy in a later wave.
	// ConvertToBranch returns it when the worktree is dirty but ConvertOptions.Message
	// is empty — a commit message is required to capture the work before renaming.
	ErrWorktreeDirty = errors.New("sandbox: worktree has uncommitted changes")

	// ErrNoWorktree is returned by Session.WorktreeStatus / Session.ConvertToBranch
	// when the session has no per-session worktree (non-git fallback / WorktreeOff),
	// or when its recorded worktree directory is missing on disk. It is the clear
	// signal that there is no deterministic git surface to act on for this session.
	ErrNoWorktree = errors.New("sandbox: session has no worktree")

	// ErrInvalidBranchName is returned by Session.ConvertToBranch when
	// ConvertOptions.BranchName is not a valid git branch ref (rejected up front by
	// `git check-ref-format --branch`) — so the caller learns the name is bad before
	// any commit or rename runs, never mid-flow.
	ErrInvalidBranchName = errors.New("sandbox: invalid git branch name")

	// ErrBranchNameTaken is returned by Session.ConvertToBranch when the requested
	// target branch already exists: convert renames the session's auto-branch with
	// `git branch -m` and deliberately never force-renames (no -M), so an existing
	// target is a hard error the human resolves by choosing another name. Any work
	// committed before the rename stays on the original branch (never lost).
	ErrBranchNameTaken = errors.New("sandbox: target branch name already exists")
)

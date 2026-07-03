# Per-session Anthropic account selection

Status: **implemented** (branch `feat/anthropic-api-key-auth`; all five stages
landed — see §Sequencing). Kept as the design record. One deferred UX follow-up
is tracked in `TODO.md` (no TUI path to a *first* account).

## Goal

When a user creates a Claude session — from the TUI (`n` → claude) or
`sandbox claude` — let them:

1. Pick from **multiple stored Anthropic accounts**, each either a **claude.ai
   subscription** (OAuth token) or an **Anthropic Console** account (API key).
   E.g. two console logins + one claude.ai login are all selectable.
2. **Add an account** by logging in: claude.ai runs `claude setup-token`;
   console pastes an API key.
3. The **chosen account's credential is provisioned into the per-session
   Secret**, so the agent pod sees exactly one credential (the one it uses).
   Whether accounts share or use separate k8s Secrets is an implementation
   detail the user does not care about — the pod only ever mounts its own.

Decisions locked with the maintainer (2026-07-02):

- **Subscription login** = shell out to `claude setup-token` (host `claude` CLI).
- **Host credential storage** = macOS Keychain (file fallback for Linux/CI).
- **Provisioning** = per-session Secret (drops the shared-operator-Secret
  requirement for account-backed sessions; the shared Secret stays as the
  no-accounts-configured fallback for backward compatibility).

## Where this sits today

- **TUI**: `internal/tui/dashboard/backend_picker.go` — `n` opens an overlay to
  pick claude/opencode, then a `Creator` func provisions + connects. This is the
  insertion point for a second **account** step after "claude".
- **`internal/cred`**: read-only today (`sandbox auth status` red/green). The
  **write side** (local store + `login`/`sync`/`logout`) is designed-but-unbuilt
  in `docs/codex-integration-plan.md` §Authentication. This feature *is* that
  write side, scoped to Claude and wired into the TUI.
- **`internal/k8s/backend.go`**: `buildEnv` claude branch currently references
  the shared `anthropic-credentials` Secret. `Spec.AnthropicAuth`
  (`oauth`|`api-key`) already selects the env var / key; it just can't yet hold
  *multiple* accounts or a per-session credential.
- **Per-session Secret** `<id>-runner` already exists (runner-token,
  opencode-password, ssh-authorized-key), created in `CreateSession`, deleted on
  `destroy`, and survives suspend/resume. Adding one credential key to it is the
  provisioning mechanism.

## Design

### 1. Local multi-account store (`internal/cred` write side)

- **`Account`** (metadata only, no secret material):
  `{ ID, Label, Type AccountType (subscription|console), CreatedAt }`.
- **Manifest** `~/.local/share/sandbox/anthropic-accounts.json` — enumerable
  account metadata plus a single top-level `DefaultID` (a per-account `Default`
  bool invites two-defaults drift), **never** token bytes (keeps the "checks
  never read token material" invariant).
- **Keychain** holds only the secret bytes, one generic-password item per
  account (service `sandbox-anthropic`, account = `Account.ID`). Backend via the
  macOS `security` CLI — no cgo dep, matches the shell-out theme.
  **Never pass the secret as an argv** (`add-generic-password -w <token>` is
  visible in `ps`): feed the command through `security -i` (stdin interactive
  mode) for writes; reads (`find-generic-password -w`) return the secret on
  stdout, which we capture in-process. Caveat: because the ACL party is
  `/usr/bin/security` (not our binary), a user clicking "Always Allow" lets any
  local process read the item via the same CLI — acceptable single-operator
  tradeoff, but each read may otherwise prompt. **File fallback** (`0600` under
  the state dir, same as per-session SSH keys) for Linux/CI where there's no
  Keychain.
- **`Store` interface**:
  `List() []Account`, `Add(Account, secret []byte) error`,
  `Secret(id string) ([]byte, error)`, `Remove(id string) error`,
  `SetDefault(id string) error`. Keychain and file backends both satisfy it;
  selection is by GOOS + availability.

### 2. Login flows

- **Subscription (`claude setup-token`)**: run as a subprocess that needs the
  terminal for the interactive browser-auth + paste-code, but whose stdout
  carries the final token. Approach: **plain stdout-pipe capture** — `exec.Cmd`
  with Stdin/Stderr wired to the real terminal and Stdout captured to a buffer.
  This is exactly the invocation shape the README already documents as working
  (`--from-literal=api-key="$(claude setup-token)"` — command substitution =
  stdout piped, stdin/stderr TTY), so it is validated by precedent, not a bet.
  The token is the trimmed stdout (last line matching `sk-ant-oat…`);
  shape-validate (prefix/charset/length) before storing. From the TUI, run via
  `tea.Exec` with a custom `ExecCommand` that keeps stdin/stderr on the terminal
  and swaps stdout for the buffer (dashboard suspended for the duration, reusing
  the existing shell-out pattern). Store as a `subscription` account; prompt for
  a label (default "claude.ai").
  - Explicitly **not** a pty: a pty mirrors UI escape sequences into the capture
    and the terminal driver wraps lines at the pty width — a token printed near
    the right edge gets a newline injected mid-token, making stream-scanning a
    bug factory.
  - **Residual risk**: a future `claude` release could stop separating UI
    (stderr) from result (stdout). Stage 2 still validates live and keeps a
    "paste the token it printed" form as the fallback.
- **Console (paste key)**: a text-input form (Bubble Tea in the TUI, prompt in
  the CLI). Format-validate `sk-ant-api…`; store as a `console` account with a
  user label (e.g. "Work console"). Live validation (a cheap models call) is
  out of scope for v1.

### 3. Per-session provisioning (`client` + `internal/k8s`)

- **`CreateOptions`** (client) gains, alongside the existing `AnthropicAuth`:
  - `AnthropicAccountID string` — which stored account the session uses.
    Plain metadata, serialized normally; recorded so `status`/the picker can
    show which account a session runs on, and so rotation/logout can find
    affected sessions.
  - `AnthropicCredential []byte` (`json:"-"`, never serialized) — the resolved
    secret bytes for the chosen account. The **CLI/TUI resolves account → bytes**
    (reads Keychain); the `client` layer stays a thin façade that just carries
    and writes them. Mirror both onto `session.Spec` (credential like
    `SSHPublicKey`, `json:"-"`).
- **Fail closed, fall back only when no account was chosen.** The branch signal
  is `AnthropicAccountID != ""`, **not** `len(credential) > 0`: if an account
  was selected but the credential bytes are empty (Keychain read denied,
  manifest/Keychain drift), `Create` returns an error rather than silently
  launching on the shared Secret — a wrong-account session (work vs personal
  billing/data) is a worse failure than a refused launch. The shared-Secret
  fallback applies only to the explicit no-account path.
- **`CreateSession`**: when an account credential is present, add it to the
  per-session Secret `Data` under key `anthropic-credential`, and label the
  Secret `sandbox.dev/anthropic-account=<id>` so `auth logout`/rotation can
  enumerate the sessions holding a copy with one label-selector list.
  - **AlreadyExists gap**: `CreateSession` currently treats an existing Secret
    as success without updating it — re-creating a session id with a different
    account would silently keep the old credential. When the Secret exists and
    an account credential was provided, **patch** the `anthropic-credential` key
    (and label) instead of skipping.
- **`buildEnv` claude branch**:
  - If `spec.AnthropicAccountID != ""` → reference the **per-session** Secret
    (`sessionSecretName(name)`) key `anthropic-credential`, env chosen by
    `AnthropicAuth` (`CLAUDE_CODE_OAUTH_TOKEN` for subscription/`oauth`,
    `ANTHROPIC_API_KEY` for console/`api-key`), **not** Optional (we wrote it).
  - Else → today's behavior: shared `anthropic-credentials` Secret, Optional
    (unchanged, backward compatible).
- **One key, one env, one credential per pod** — the type is encoded by
  `AnthropicAuth`/env-var name, so the pod literally only sees its one credential.
- **Suspend/resume**: per-session Secret persists (deleted only on destroy) and
  the pod template referencing it persists; Resume only flips replicas — so the
  account selection lasts the session's lifetime (same argument as the
  `AnthropicAuth` commit). Corollary: SecretKeyRef env is resolved at container
  start, so **updating the per-session Secret + a suspend/resume cycle is the
  credential-rotation mechanism** for a live session — no pod-template change
  needed.
- **Record the account in the local index** (`session.json` entry): account ID
  and label only, never bytes — powers `sandbox status` and the picker's
  "in use" hints.

### 4. TUI account picker (`internal/tui/dashboard`)

- Selecting "claude" in the backend picker transitions to a new **account
  picker** overlay (rather than creating immediately): rows for each stored
  account (label + type glyph + default marker) and a trailing **＋ add account**.
- **Zero accounts stored** → skip the picker entirely and create with today's
  shared-Secret behavior (current UX unchanged; the account flow is opt-in via
  `auth login` / ＋ add account). When accounts *do* exist but the operator wants
  the cluster credential, an explicit **"cluster default (shared Secret)"** row
  makes the fallback a visible choice instead of a silent branch.
- **Enter on an account** → create with that account: only the **account ID**
  is threaded to the Creator (via `CreateParams` below); the CLI-side creator
  resolves ID → type + credential bytes from the store, keeping Keychain access
  out of the dashboard.
- **Enter on ＋ add account** → sub-flow: choose type (claude.ai / console) → run
  the matching login → return to the picker with the new account selected.
- **`Creator` signature** changes from `(ctx, backend string, onStage)` to carry
  a small `CreateParams{ Backend string; AnthropicAccountID string }` so the
  dashboard stays decoupled from Keychain.
- **`AccountStore`** is injected via `RunOptions` (like `TitleStore`/
  `SnapshotStore`); the CLI supplies the Keychain-backed concrete impl. The
  login subprocess runs via `tea.Exec` (terminal handed over, TUI suspended).
- opencode path is unchanged (no account step).

### 5. `sandbox auth` CLI parity

Share the same store so CLI and TUI never diverge:

- `sandbox auth login --subscription` / `--console` (acquire + store),
  `auth list`, `auth logout <id>`, `auth default <id>`.
- `sandbox claude --account <id|label>` to pick non-interactively; no flag →
  the default account, else (no accounts) the legacy shared-Secret fallback.
  Resolution: exact ID match first, then a **unique** label match; an ambiguous
  label is an error that lists the matches (labels aren't unique keys).
- `auth logout <id>` removes the local account but **does not scrub live
  sessions** — their per-session Secrets hold copies and running pods hold the
  env var regardless. Logout should list affected sessions (via the
  `sandbox.dev/anthropic-account` label) and say so; actually cutting access =
  revoking the token/key at Anthropic. Same story for rotation: re-login, then
  update the labeled Secrets + suspend/resume each session.

## Security model

Who can read a per-session credential, and what changes vs today:

- **The session pod** sees exactly one credential, as an env var. Strictly
  better than the shared Secret at pod level (a compromised pod today exposes
  the one shared credential; after this change it exposes only its own
  account's). Env-var exposure (child inheritance, `/proc/<pid>/environ`,
  `kubectl exec ... env`) is unchanged and unavoidable — Claude Code consumes
  env vars. Pods can't reach the k8s API at all
  (`AutomountServiceAccountToken: false` + default-deny egress), so a pod
  cannot enumerate other sessions' Secrets.
- **Namespace-level principals**: anything with `secrets:get` in
  `agent-sessions` — the operator's kubeconfig and the **reaper
  ServiceAccount** (`sandbox-reaper`, which reads the runner-token key) — can
  read every per-session Secret. Not a new capability (both can read the shared
  `anthropic-credentials` Secret today), but the **aggregate blast radius
  grows**: the namespace now holds a copy of *every* account with a live
  session, not one credential. k8s RBAC can't scope `get` to specific keys or
  dynamic names, so this is accepted; it's worth remembering that the reaper
  image ref is operator-controlled (`--reaper-image`) and defaults to a moving
  tag.
- **etcd**: same class as today — Secrets at rest are the cluster's
  encryption-at-rest problem (private wiring, out of scope here).
- **Copies, not references**: N sessions on one account = N Secret copies.
  Revoking at Anthropic is the only true kill switch; the
  `sandbox.dev/anthropic-account` label keeps the copies enumerable for
  rotation (see §5). The alternative — one Secret per *account* referenced by
  many sessions — makes rotation a one-object update but needs refcounted
  lifecycle and DNS-safe account IDs; per-session copies tie cleanup to the
  existing destroy path and were judged the simpler contract.

## Backward compatibility

- **No accounts stored** → unchanged behavior: shared `anthropic-credentials`
  Secret, Optional ref. Existing deployments keep working; the whole account
  flow is additive.
- The `AnthropicAuth` primitive and its tests (already committed) are reused
  as-is; only the Secret *source* (per-session vs shared) becomes conditional.

## Testing

- **Store**: file-fallback round-trip (add/list/secret/remove/default);
  Keychain backend behind the interface, exercised via the file fallback in CI
  (no Keychain in CI).
- **backend `buildEnv`**: extend the existing table — account path references
  the per-session Secret with the right env var (not Optional); fallback path
  unchanged. Plus an anti-regression assertion that the built Sandbox object
  **never contains the literal credential bytes** (env must be SecretKeyRef,
  no `Value`) — guards against someone later inlining the value.
- **`CreateSession`**: per-session Secret carries `anthropic-credential` +
  account label when provided; absent otherwise; existing-Secret path patches
  the credential key.
- **client**: `AnthropicAccountID`/`AnthropicCredential` plumb through to the
  Spec; account-selected-but-empty-credential fails closed
  (`ErrAnthropicCredentialMissing` or similar); `AnthropicAuth` validation
  unchanged.
- **login parsing**: token shape validation + last-`sk-ant-oat…`-line extraction
  from a captured stdout fixture (trivial now that capture is a plain pipe, not
  a pty stream).
- **TUI**: account-picker navigation via headless model tests (mirror the
  existing backend-picker tests).

## Open questions / risks

1. `claude setup-token` stdout/stderr separation surviving future CLI releases
   (Stage 2 live check; paste-fallback as insurance). The `$(…)` capture shape
   is already README-documented as working today.
2. Subscription token expiry — `sk-ant-oat…` setup-tokens are long-lived
   (~1 year) and **opaque, not JWTs**, so the `cred` read side's `jwtExp` cannot
   decode them. Show account *age* from `CreatedAt` instead; the real expiry
   signal is a 401 mid-session, which should surface in the TUI as
   "re-login + rotate" guidance (update the labeled Secret, suspend/resume),
   not an opaque SDK error. Not blocking for v1.
3. Console key validation depth (format-only for v1; validate the `sk-ant-`
   prefix loosely — Anthropic has changed key formats before).
4. macOS Keychain access prompts on read — acceptable UX (see the
   "Always Allow"/`security` ACL caveat in §1).

## Sequencing (independently reviewable stages)

1. **cred write side** — `Store` + Keychain/file backends + manifest. Tests. No UI.
2. **backend + client** — per-session credential provisioning + `buildEnv`
   branch + fallback. Tests. (Builds directly on the `AnthropicAuth` commit.)
3. **CLI** — `auth login/list/logout/default` + `claude --account`. Tests.
4. **TUI** — account picker + add-account/login sub-flow. Tests.
5. **Docs + status** — `architecture.md` Secret table & auth-flow section;
   extend `sandbox auth status` to enumerate accounts.

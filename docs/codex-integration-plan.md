# Codex backend — integration plan (Option B: remote app-server + local TUI)

Status: **planning**. Decision (2026-06-24): ship **Option B** — run the Codex
**app-server daemon** in the pod and drive it from a local `codex --remote …` TUI
embedded as an external pane, mirroring the OpenCode backend. **Auth (revised
2026-06-24):** ChatGPT-plan **OAuth tokens delivered via a CLI-injected Secret**
(recommended — uses the existing plan, no API-credit billing), with `OPENAI_API_KEY`
as the simple fallback. See the **Authentication** section. Backend id
`codex-app-server` is already reserved in `internal/session/types.go:52`;
`runner/src/agent.ts` already names Codex as a target.

> First-class native-transcript integration (Option A — runner drives
> `codex app-server` over stdio and maps the v2 *Thread* protocol → normalized
> events) is deferred; see "Future" at the end. The generated protocol bindings
> (`codex app-server generate-ts --out <dir>` / `generate-json-schema --out <dir>`)
> are reproducible locally — no need to vendor them until Option A.

## How this mirrors OpenCode

OpenCode is the template: the runner supervises a child server in the pod, the
runner's turn path is **not** used (`selectAgent('opencode-server') → null`,
`runner/src/agent.ts:43-52`), and a local native client embedded as a PTY pane
(`internal/tui/dashboard/external_pane.go`) talks to the pod server over a
port-forward. Codex is the same shape with three differences: the server is
`codex app-server` (daemon + remote-control), the transport is a WebSocket/unix
socket (not opencode's HTTP), and auth is an `OPENAI_API_KEY` env (not basic-auth).

| Concern | OpenCode (template) | Codex (this plan) |
|---|---|---|
| Pod server | `opencode serve --hostname 0.0.0.0 --port 4096` (`opencode.ts`) | `codex app-server daemon start` with remote-control enabled, listening on a ws port |
| Runner turn path | none — `selectAgent → null` | none — `selectAgent → null` |
| Local client | `opencode attach <url> -u <user>` PTY pane | `codex --remote ws://localhost:<lp>` PTY pane |
| Transport over forward | HTTP basic-auth on :4096 | WebSocket on a pod-loopback port |
| Auth | `OPENCODE_SERVER_PASSWORD` (fail-closed) | ChatGPT-OAuth `auth.json` Secret (recommended) / `OPENAI_API_KEY` (fallback) |
| Idle/reaper signal | established TCP conns on :4096 (`establishedConnections`) | established conns on the codex ws port |
| Dashboard | external pane, `StatusIdle`, no native metrics (gap) | external pane, **but the runner observes the app-server** → live status/ctx%/cost/recent-tools on the statusline |

## Parity bar (the standing requirement across all agents)

Some per-agent difference is fine (each agent owns its **in-pane input loop** and
its own transcript rendering in Option B). But these must be **similar across
claude / opencode / codex**, because they're the wrapper UX the `sandbox` shell
owns, not the agent:

- **Startup speed** — same create/connect path; the only delta is image pull
  (addressed by image-shrink + Spegel, below).
- **Detach + keybindings** — Ctrl+] detach and the surrounding chrome behave the
  same regardless of backend (the external pane already owns detach; verify it's
  identical for codex).
- **Prompt/affordance UX** — how you start/submit and what on-screen hints you get
  around the agent.
- **Metrics we surface** — ctx% / cost / token usage / recent tools / idle-soon /
  sync state on the statusline + detail pane.

**Key principle (maintainer, 2026-06-24): the runner is the control plane and
should be the metrics source for _every_ backend — even ones it doesn't drive
turns for.** So for Option B, the runner does NOT just blindly supervise the
daemon: it ALSO opens a **passive observer** connection to the app-server,
subscribes to the active thread's notifications, and maps a **subset** (token
usage, tool start/finish, turn lifecycle) → normalized events → the existing SSE →
the dashboard statusline. The local `codex --remote` TUI still drives the turns;
the runner just watches. This is what makes external-pane backends first-class on
**metrics** without paying for the full Option A event mapping. (The same
runner-as-observer fix retrofits OpenCode's metrics gap by subscribing to
`opencode serve`'s event stream — see `docs/archive/review-2026-06-24.md` agent-parity.)

## Authentication

Goal: use the **ChatGPT plan** (not API credits) headlessly in the pod, the way
`claude setup-token` gives Claude a long-lived OAuth token. Codex supports this:

- `codex login --device-auth` — **device-code OAuth flow** (show a code, authorize
  on any device; no browser callback). The headless-friendly bootstrap.
- `codex login --with-access-token` / `--with-api-key` — read a token/key from
  stdin (scriptable).
- The resulting `~/.codex/auth.json` holds `{auth_mode, tokens:{id_token,
  access_token, refresh_token, account_id}, last_refresh}`.

**Token lifetimes (measured from a live `auth.json`, 2026-06-24):** access_token
**10 days**, id_token 1 hour (identity only, auto-refreshed), refresh_token
long-lived (no encoded expiry). So a seeded token keeps a session working ~10 days
even with no refresh wiring; the refresh token + egress extends it further.

**⚠ Plan-eligibility finding (live `account/rateLimits/read` + `account/read`,
2026-06-24):** the maintainer's ChatGPT account currently reports `planType: "free"`
for Codex with the primary window at `usedPercent: 100` (`rate_limit_reached`,
30-day window). **On a free ChatGPT tier the OAuth path yields no usable Codex
quota** — Codex-over-subscription needs a Codex-eligible paid plan (Plus/Pro/
Business). So unless the account is upgraded (or a different account is used), the
**`OPENAI_API_KEY` (API-credits) path is the practical default**. The credential
manager must support both and the **status surface will reveal this** (shows
`chatgpt / free / rate-limited`) before a doomed session launches. Verified live:
`getAuthStatus → authMethod "chatgpt"`, `account/read → planType "free"`,
`rateLimits/read → usedPercent 100`. Refresh delegation did NOT fire for read-only
calls (daemon self-serves with the valid access token; delegation only at a refresh
boundary).

### Chosen approach — a CLI-owned credential manager

Decision (2026-06-24): **the `sandbox` CLI is the credential authority.** It stores
the secret locally on the Mac, reconciles the cluster Secret on every create/connect,
and prompts the user to renew when the credential can't be auto-refreshed. Design it
**agent-agnostic** from the start — codex first, but the same subsystem will own
Claude's OAuth token, OpenAI/provider keys, etc. This generalizes the existing
`ensureSSHKey` pattern (ensure a local credential → project it into a cluster Secret).

New `internal/cred` (or `internal/secrets`) subsystem:
- **`Store`** — local secret store. macOS primary; file/env fallback for Linux/CI.
- **Per-agent `Provider`** — knows how to (a) check validity (decode the access-token
  JWT `exp`, or call a status endpoint), (b) refresh or re-auth (codex: `codex login
  --device-auth`; claude: setup-token; key-based: paste/`--with-api-key`), and
  (c) serialize to the form the pod needs (codex → `auth.json` blob; claude → token;
  openai → key).
- **`sandbox auth` commands** — `login <agent>` (acquire + store), `status` (per-agent
  validity/expiry), `sync` (push to the cluster Secret), `logout`.

Reconcile flow (in `provisionSession`/connect, beside `ensureSSHKey`):
1. Load the agent credential from the local Store; decode `exp`.
2. If expired/near-expiry: try refresh; if that fails, **prompt the user to renew**
   (`run: sandbox auth login codex`) and block the session start rather than launch a
   pod that will fail auth.
3. Upsert the `agent-sessions` Secret with the current credential (idempotent;
   **never log token values — redact**).
4. The pod mounts the Secret at `~/.codex/auth.json` under the PVC path
   `/session/state/codex/` so in-flight refreshes persist; fail-closed if absent.

In-flight refresh (sessions running past the 10-day access token) stays the pod
daemon's job (refresh_token + egress) or is re-seeded on the next reconnect — the
CLI owns *seeding + renewal-prompting*, not live mid-session rotation.

### Auth + cluster status surface (preflight health — red/green)

The read side of the credential manager: validate the configured auth for every
supported agent and surface it (the maintainer's ask, 2026-06-24). Each `Provider`
exposes `Status() → {configured, method, plan?, healthy?, detail}`; the dashboard
renders a compact red/green strip (fits the cluster strip from
`docs/archive/dashboard-redesign.md`) and `sandbox auth status` / `sandbox doctor` prints it.

- **Kubernetes** — 🟢/🔴 can we reach the cluster API? (lightweight `/healthz` /
  `kubectl` ping; the backend already builds the kubeconfig). Surface the
  namespace/context too.
- **Claude** — marker: `oauth (subscription)` if `CLAUDE_CODE_OAUTH_TOKEN`/setup-token
  (cross-ref the `subscription_type` readout, [[sandbox-usage-limits-local-readout]]),
  `api key` if `ANTHROPIC_API_KEY`, else `none`.
- **Codex** — `subscription (chatgpt)` vs `api key` vs `none`, from `auth.json`
  `auth_mode` / `codex login status` (offline), enriched by the app-server
  `getAuthStatus` (`AuthMode`) + `account/read` (`PlanType`) when a daemon is up.
- **OpenCode** — **per configured provider** (anthropic / openai / opencode-zen):
  🟢/🔴 key present, and optionally 🟢/🔴 healthy via a cheap validation ping. This
  matches opencode's multi-provider config (`buildOpencodeConfig`).

Default checks are **cheap + offline** (token presence + JWT `exp` decode). A
`--check` flag opts into live health pings (1 network call per provider). This is
exactly what the credential manager already needs internally for the
reconcile/renewal-prompt decision, so the surface is almost free once it exists.

### Local secret storage on macOS (Keychain, optionally Secure-Enclave-wrapped)

- **Tier 1 (default): macOS Keychain.** Store the `auth.json` blob as a generic
  password item via the Security framework (Go: `github.com/keybase/go-keychain`,
  which supports accessible flags + `SecAccessControl`). Mark it
  `WhenUnlockedThisDeviceOnly` (not iCloud-synced, not in backups). Optionally gate
  reads on **Touch ID / user presence** via `SecAccessControl`.
- **Tier 2 (hardening): Secure-Enclave-wrapped blob.** The SE holds *keys*, not blobs
  — so generate an SE-backed EC key (biometric/presence-gated), envelope-encrypt the
  `auth.json` with it, and store the wrapped blob. Decryption is hardware-bound and
  presence-gated; the key never leaves the SE.
- **Caveat — code signing.** Keychain ACLs key off the binary's signing identity; an
  unsigned/ad-hoc `sandbox` binary will re-prompt or store items broadly. Fine for a
  single-operator homelab; for stronger guarantees sign with a Developer ID + a
  keychain access group.
- **Fallback (Linux/CI):** file under `~/.config/sandbox` (0600) or env, behind the
  same `Store` interface.

This **replaces** the earlier 1Password+ESO idea for agent creds (the CLI is the
authority now), though ESO/1Password remains the pattern for true *cluster* secrets
([[homelab-secrets-external-secrets]]).

**Open auth questions (fold into the spike):**
- **Who refreshes?** Does the pod daemon refresh tokens itself from `auth.json`
  (using `refresh_token`), or does it delegate via the protocol's
  `ChatgptAuthTokensRefresh` *server→client* request — in which case the **runner**
  (not the remote TUI) must own auth and answer it? This nudges toward the runner
  holding auth and being the app-server's primary client. Determine in the spike.
- **Egress.** `agent-sessions` is default-deny egress with an allowlist; the token
  refresh endpoint + the ChatGPT/OpenAI API hosts must be added to the
  NetworkPolicy allowlist or refresh/inference fails.
- **Secret sensitivity.** `auth.json` is the user's ChatGPT OAuth — now in etcd.
  Acceptable single-operator; revisit for multi-user. Per the homelab pattern this
  is a CLI-injected per-user credential, not an ESO/1Password cluster secret.

**Fallback:** `OPENAI_API_KEY` via Secret — trivial, fully headless, no refresh, but
bills API credits. Keep it wired as an alternative `auth_mode`.

## The crux: transport over a port-forward — **Phase 0 spike**

`kubectl port-forward` forwards **TCP** to the **pod's loopback** network
namespace. The Codex daemon's native channel is a **Unix domain socket**
(`codex app-server proxy --sock <SOCKET_PATH>`), but `codex --remote` also accepts
`ws://host:port` / `wss://…` / `unix://PATH`. So Option B needs the daemon
listening on a **ws TCP port**, and `--remote ws://…` from the laptop.

**Spike to run locally (codex 0.139.0 is installed; no network needed):**
1. Start `codex remote-control start` (or `codex app-server daemon start` +
   `enable-remote-control`) and discover **how the ws endpoint is addressed** —
   is there a default `ws://127.0.0.1:<port>`? A config key
   (`-c app_server.listen=…`)? An endpoint file under `~/.codex`?
   (`codex app-server daemon version --json` and the `~/.codex` dir are the first
   places to look.)
2. Confirm `codex --remote ws://127.0.0.1:<port>` connects and drives a turn.
3. Decide the bind address. **Prefer `127.0.0.1` in the pod**, reached via
   port-forward (which targets pod-loopback) — this keeps the agent-with-shell
   **off the pod network entirely**, sidestepping the O3 "unauthenticated agent
   bound to 0.0.0.0" concern OpenCode had to solve with basic-auth. The daemon's
   ws endpoint likely has no auth token, so do **not** bind `0.0.0.0`.
4. **Observer feasibility (for the metrics parity bar):** confirm a SECOND client
   can attach to the same daemon and subscribe to the active thread's
   notifications read-only (`threadRead`/subscribe + ServerNotifications). This is
   what lets the runner surface metrics without driving turns. Also settle the
   token-refresh ownership question (daemon-self vs delegated `ChatgptAuthTokensRefresh`
   to a client) here, since it decides whether the runner must be the auth-owning
   client.

**Fallbacks if a ws TCP listener isn't configurable:**
- **SSH-tunnel the unix socket.** The pod already runs `sshd` (for Mutagen) with a
  per-session key. Tunnel the daemon's unix socket to the laptop and use
  `codex --remote unix://<local-socket>` — uses the existing key-authenticated
  transport, no new exposed port.
- **`codex app-server proxy --sock`** piped over the SSH channel (stdio bridge).

The spike's outcome (ws-port vs unix-over-ssh) decides the port-forward wiring in
Phase 2; everything else is independent of it.

### Spike results (2026-06-24 — partial; off-airplane items flagged)

Run against the installed Homebrew codex 0.139.0, offline. **Conclusions:**

- **Bare `codex app-server` works over stdio** — newline-delimited JSON-RPC,
  `initialize` needs **no auth and no network** (returned `userAgent`, `codexHome`,
  `platformOs`). This is the transport `proxy` bridges and that Option A would use.
- **`remote-control` / `app-server daemon` require the STANDALONE managed install**
  (`~/.codex/packages/standalone/current/codex`, via the official `curl … |
  install.sh`) — the Homebrew build refuses to start the managed daemon. The
  daemon's native channel is a **unix socket**
  (`~/.codex/app-server-control/app-server-control.sock`); `codex app-server proxy
  --sock` bridges stdio↔that socket. **Implication:** Option B's `codex --remote
  ws://` path is **gated on bundling the standalone install in the pod image** (fine
  — we build the image), OR we sidestep the managed daemon and bridge the bare
  stdio/unix-socket app-server over the existing SSH transport.
- **Refresh + approvals are delegated to the CLIENT.** `account/chatgptAuthTokens/refresh`
  is a **ServerRequest** (server→client), alongside every approval
  (`execCommandApproval`, `applyPatchApproval`, `item/permissions/requestApproval`,
  `item/tool/requestUserInput`, `mcpServer/elicitation/request`). So **whoever is the
  daemon's client owns token refresh + approvals.** In Option B that's the local
  `codex --remote` TUI (handles them while attached, using the laptop's tokens — so
  the laptop credential manager is the natural refresh owner). To make the runner own
  refresh, the runner must be a client.
- **Metrics, auth-status, and control are CLIENT requests** (clean to consume):
  `account/rateLimits/read`, `account/usage/read`, `model/list`, `thread/list`,
  `thread/read`, `turn/start|steer|interrupt`, `getAuthStatus`, `account/read`. The
  runner-observer just *calls* `rateLimits/read` + `usage/read` for the statusline —
  no notification-scraping required — and `getAuthStatus`/`account/read` power the
  CLI auth-status surface (`AuthMode = apikey|chatgpt|chatgptAuthTokens|…`, plus
  `PlanType`).

**Still needs the live network / a managed daemon (do off-airplane):** how the
remote-control daemon addresses its ws endpoint (port/bind), and a live check that a
SECOND client can observe another client's thread (`thread/list`+`thread/read` exist
and the daemon is multi-client, so plausible). The standalone install also can't be
set up here (no network; and not imperatively on a Nix host).

### Spike results (2026-07-06 — the off-airplane items, run in a container; SPIKE COMPLETE)

Run against codex **0.142.5** (npm build + standalone, in a disposable
`node:24-slim` container — matching the pod-image shape). **Both remaining
questions answered; Option B's transport is settled:**

- **ws endpoint addressing: `codex app-server --listen ws://IP:PORT` — no
  daemon needed.** The bare app-server takes `--listen` (`stdio://` default,
  `unix://PATH`, `ws://IP:PORT`, `off`) and binds a **fixed, chosen port**. It
  also serves **`GET /readyz` and `/healthz`** on the same port — the runner
  supervisor's health-probe surface for free. Auth: `--ws-auth
  {capability-token,signed-bearer-token}` + `--ws-token-file` exist **for
  non-loopback listeners**; a `127.0.0.1` bind (our plan) needs none, and the
  TUI's `--remote-auth-token-env` carries the bearer for the non-loopback
  case if we ever want it.
- **The npm build serves `--listen ws://` fine** (verified on 8789, listener +
  readyz up) — **the STANDALONE managed install is NOT needed for Option B.**
  The standalone/managed-daemon path (`app-server daemon bootstrap
  --remote-control`, `codex remote-control start`) turns out to be a
  **cloud-relay pairing flow**: it dials OUT (status `connecting`,
  `environmentId`, pairing codes/enrollments in the binary strings — the
  "use codex from another device" product feature), requires ChatGPT auth to
  register, and never binds a local TCP port. Not our transport; ignore it.
  (Host Homebrew 0.139.0 also has `--listen` — the 2026-06-24 "standalone
  required" conclusion applied only to the managed daemon.)
- **Observer feasibility: CONFIRMED.** Two concurrent ws clients (Node 24
  global `WebSocket`, one JSON-RPC message per text frame): A `initialize` +
  `thread/start` → **B receives the `thread/started` notification broadcast
  and `thread/read`s A's thread by id.** Server notifications are broadcast
  to all connected clients — exactly the runner-as-metrics-observer shape.
  Caveats: `thread/list` returned empty for A's fresh in-memory thread (list
  appears to cover persisted threads only — the observer should key on
  notifications + `thread/read`, not list); a live **turn** observation
  (deltas streaming to B) still needs authed model access — do it as part of
  Phase 2 with the real auth Secret, but nothing structural remains in doubt.
- **Implications for the plan:** pod runs `codex app-server --listen
  ws://127.0.0.1:8788` under a runner supervisor (mirror `opencode.ts`);
  `portCodex = 8788` port-forward; local client = `codex --remote
  ws://127.0.0.1:<lp>`; runner observer = a second ws client on pod-loopback.
  The unix-over-SSH fallback is unnecessary. Server→client requests
  (approvals, `account/chatgptAuthTokens/refresh`) arrive at **every**
  client — the observer must answer/refuse them politely (JSON-RPC error) so
  it never wedges an approval the TUI should own; refresh ownership remains
  the auth question flagged above. Codex installs into the pod image via the
  normal package path (npm today, Flox closure per the §7b directive) — no
  vendor self-installer layer.

## Phase 1 — backend plumbing (Go + runner)

1. **`internal/session/types.go`** — add `BackendCodex = "codex-app-server"`
   alongside `BackendClaudeSDK`/`BackendOpenCode` (the doc comment already lists it).
2. **`internal/cli/claude_remote.go`** — add `newCodexCmd()` mirroring
   `newOpencodeCmd()`: `RunE → runStartSession(cmd, session.BackendCodex, "", …)`.
   Codex owns its own input loop, so no initial-prompt arg. Register in `root.go`.
   Optionally a `--model` flag threaded into `Spec.Model` (codex takes it via
   `-c model=<id>`), unlike opencode which has none.
3. **Auth Secret (CLI side).** In `provisionSession`, beside `ensureSSHKey`, add
   `ensureCodexAuth`: reuse the laptop's `~/.codex/auth.json` if valid, else run
   `codex login --device-auth` and surface the code/URL; create/refresh the
   `agent-sessions` Secret holding the `auth.json` blob (redact in logs). Fallback
   path: `OPENAI_API_KEY` Secret.
4. **`internal/k8s/backend.go`** — add `portCodex` (the spike's ws port, e.g.
   8788 on pod-loopback); mount the codex-auth Secret at the pod's
   `~/.codex/auth.json` under the PVC path `/session/state/codex/` (so token
   refreshes persist across suspend/resume — mirror how Claude state is persisted);
   fail-closed if neither the auth Secret nor `OPENAI_API_KEY` is present. Set
   workspace trust non-interactively (`-c projects."<path>".trust_level=trusted`)
   and a non-interactive approval policy so the headless daemon never blocks on a
   prompt. **Add the OpenAI/ChatGPT auth + API hosts to the egress allowlist
   NetworkPolicy** (token refresh + inference) — without it codex can't refresh or
   call the model.
4. **`internal/k8s/portforward.go`** — add `ForwardSpecsWithCodex(httpLocal,
   sshLocal, codexLocal)` (or generalize `ForwardSpecsWithOpencode` to take an
   extra backend port). If the spike picks unix-over-SSH, no extra forward — reuse
   the SSH forward.
5. **`runner/src/codex.ts`** — a supervisor modeled on `opencode.ts`:
   - fail-closed if neither the codex-auth `auth.json` nor `OPENAI_API_KEY` is present;
   - spawn `codex remote-control start` (or daemon + enable-remote-control),
     binding ws to `127.0.0.1:<portCodex>`, restart-on-exit like the opencode
     supervisor, `stop()` with SIGTERM→SIGKILL grace;
   - activity poller reusing `establishedConnections(portCodex)` → `setExternalActivity()`
     so the idle reaper sees a live local client.
   - **Metrics observer (the parity-bar requirement):** open a SECOND, passive
     connection to the daemon (stdio/unix socket — the daemon is multi-client),
     subscribe to the active thread's notifications, and map a subset
     (token usage → `rate_limit`/usage, tool start/finish → `tool.*`, turn
     lifecycle) into the runner's normalized event log via `appendEvent`. The local
     `--remote` TUI keeps driving turns; this connection only watches. Reuses the
     existing SSE → the dashboard statusline gets ctx%/cost/recent-tools for codex
     sessions exactly like claude. (Confirm in the spike that a second client can
     subscribe to another client's thread; if auth-refresh is delegated to the
     primary client, this observer connection is also where the runner owns it.)
6. **`runner/src/agent.ts`** — add `case 'codex-app-server': return null;` (no
   runner turn path) and extend the `selectAgent` "known backends" error string.
7. **`runner/src/index.ts`** — start the codex supervisor when
   `SANDBOX_BACKEND === 'codex-app-server'` (mirror the opencode boot branch).
8. **`runner/Dockerfile`** — install the `codex` binary in the runtime image. This
   grows the image. Startup-speed plan (maintainer, 2026-06-24), addresses the
   cold-start image-pull finding in `docs/archive/review-2026-06-24.md`:
   - **Shrink + split per-backend images** (claude-only default; opencode image;
     codex image) so each `sandbox <agent>` pulls only what it needs.
   - **Deploy Spegel** (stateless, P2P/cluster-local OCI mirror) so the first node
     to pull seeds the rest — subsequent session pods on any node hit a local
     cache instead of the upstream registry. Cluster-side infra (DaemonSet), so it
     lands via Argo/GitOps like the rest of the homelab, not `kubectl apply`.
   - Together these make startup parity across agents a question of "first pull on
     a cold node" only, which Spegel largely removes.

## Phase 2 — local client pane

1. **Generalize `ExternalPane`.** It is already a generic vt-emulator PTY pane with
   the reply-pump fix; only `exec.Command("opencode","attach",url,"-u",user)`
   (`external_pane.go:129`) is opencode-specific. Parameterize the launch command
   (struct/closure) so codex runs `codex --remote ws://localhost:<lp>` (env
   `OPENAI_API_KEY` if the local client needs it; likely not — the *pod* daemon
   authenticates). The reply-pump (terminal-cap probe drain) the opencode work
   added applies directly: codex's TUI does the same cap probing.
2. **`internal/cli/connect.go`** — add a codex branch beside the opencode one:
   wait for the daemon's ws endpoint to be reachable (readiness probe, like the
   `opencode serve` readiness wait at `connect.go:201-213`), then build the
   client endpoint (ws URL or unix socket path).
3. **`internal/tui/dashboard/connector.go` + `internal/cli/dashboard_connector.go`**
   — generalize `OpencodeCreds` into an `ExternalClient{Kind, URL/Endpoint, …}`
   (or add a parallel `CodexEndpoint`), carried through `RunOptions`/the connection
   the same way opencode creds are at `dashboard_connector.go:50-63`.
4. **`internal/tui/dashboard/app.go`** — generalize `connectingOpencode`/
   `StageOpencode` ("Starting opencode") to cover codex ("Starting Codex"); the
   `ScreenExternal` lifecycle already handles any external pane.

## Phase 3 — parity polish (consistency with the other agents)

- **`tui/theme/brand.go`** — add `MarkCodex`/`MascotCodex` + color, and the
  dashboard mapping helpers (`BackendMark`/`BackendGlyph`/`BackendColor`/
  `MarkedClientLabel`, `session.go:253-296`) so Codex gets a brand glyph in the
  list, picker, and status row.
- **`internal/tui/dashboard/backend_picker.go`** — add the Codex option (and this
  is where the "Tekken-style picker" idea in `TODO.md` would shine — 3 agents now).
- **`internal/tui/dashboard/session.go`** — backend-conditional rendering. Codex is
  an external pane for *input*, but with the runner metrics-observer (Phase 1.5) it
  is NOT second-class on metrics: the dashboard should show live status (driven by
  observed turn events, not stuck at `StatusIdle`), ctx%/cost, recent tools, and an
  auto-title — same as claude. The intended-difference set is just the in-pane
  rendering (codex draws its own transcript). Apply the same observer-driven
  status to **opencode** to close its parity gap too.
- **Reaper/idle** — verify the activity poll keeps Codex sessions warm while a
  local `--remote` client is attached and lets them reap when detached.

## Phase 4 — docs + tests

- **Docs:** add Codex to the backend table in `docs/architecture.md`, the backends
  section of `README.md`, and note the new env/secret + image in `k8s/README.md`
  and `docs/runner-api.md`. Document the local `codex` version-match requirement
  (like opencode's) and the `OPENAI_API_KEY` billing note.
- **Tests:** mirror `runner/test/opencode.test.ts` (supervisor start/stop/restart,
  fail-closed on missing `OPENAI_API_KEY`, activity poll), `permission.test.ts`
  (n/a for Option B — no runner permission path), and Go tests for the new
  `newCodexCmd`, port-forward spec, and backend env (mirror the opencode backend
  tests in `internal/k8s/backend_test.go` and `internal/cli/root_test.go`).

## Open questions / risks

- **[spike] ws TCP listener** — can `remote-control` bind a configurable ws port,
  or only the unix socket? Decides Phase 2 transport. (Phase 0.)
- **Endpoint auth/exposure** — bind `127.0.0.1` in the pod (reachable via
  port-forward to pod-loopback) to avoid an unauthenticated agent-with-shell on
  the pod network; do not copy opencode's `0.0.0.0` bind without an auth story.
- **Headless trust/approval** — Codex's interactive trust + approval prompts must
  be disabled non-interactively in the pod (`-c projects."<path>".trust_level=trusted`,
  approval policy), or the daemon will stall.
- **`~/.codex` persistence** — sessions/threads/logs live under `~/.codex`; map it
  onto the PVC so suspend/resume and `codex resume` survive a pod restart.
- **Version match** — pin the `codex` version in the image; the local client must
  match (document, like opencode). 
- **Image size** — the codex binary inflates the runner image; pairs with the
  per-backend-image split in the startup-latency review item.

## Future — Option A (first-class native transcript)

The app-server's **v2 Thread protocol** (`threadStart`/`threadResume`, streamed
server notifications, `Exec/ApplyPatch/FileChange/PermissionsRequest` approval
requests, `modelList`, `threadSetName`) maps cleanly onto this repo's normalized
event model. A future `runner/src/codex.ts` implementing the `Agent` interface
(driving `codex app-server` over **stdio** JSON-RPC and mapping notifications →
`appendEvent`, approval requests → the runner permission flow) would give Codex
the **same native transcript/tool-cards/permission-modal/statusline/warm-hide UX
as Claude** — directly closing the agent-parity gap the 2026-06-24 review flagged
for OpenCode. Build it once Option B validates auth, image, and idle/reaper wiring.

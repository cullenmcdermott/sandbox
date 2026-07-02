# TODO — running notes

> **UX/UI polish push (2026-06-28):** the in-scope UX work below is now organized
> into a phased, resumable plan at [`docs/ux-polish-plan.md`](docs/ux-polish-plan.md)
> (6 phases, each with a self-contained restart prompt). Branch: `feat/ux-polish`.
> Under-the-hood perf/infra items here stay deferred per that plan's "Deferred" section.

## Human added needs triage
* Do we need a "claude-runner" specific image or is that name a misnomer at this point?
* automatically create worktrees? How to handle ensuring they are merged in and not accidentally "lost"? Since mutagen should sync from my laptop it can merge into "main" from my laptop it just shouldn't try to pull from remote? TUI creates a work tree as part of creating a container. Agent should know to clean it up/give it a proper name before turning it into a pushable branch to be pushed by a human
* Match claude codex ux, make it feel familiar to claude code users to make sure we can expose all of the necessary options/features/configs but make sure its unique enough for us to not cause confusion. There should already be a plan for this, if not lets create one
* Consider KRO to wrap resources into our own "custom resource"? Does it support custom status/conditions?
* Expose CLI as a go library to be consumed by another go package for backend and tui stuff? Or just drive via pre-built CLI for now?
* ~~why is hall.kvitch.dev not in use?~~ **DONE 2026-06-28 (investigation).** Typo — the real URL is **hall.kvick.dev** (one `k`). Hall IS wired in: `justfile:156-162` auto-detects `hall status` and skips `kind load` when active, `dev/local/README.md:57-81` documents the one-time host setup, and `dev/local/Tiltfile:22` records the swap. It is **optional** — `SANDBOX_USE_HALL=0` forces `kind load`; without the daemon running, `dev-image` falls back. Built images are NOT auto-loaded into KIND via Tilt (Tilt's KIND integration handles it; the scripted `dev-image` recipe is the manual path) — see the `dev/local/README.md` "Image delivery" section.
* It seems like opencode doesn't get an agent generated title and reports "idle" even when its working?
* Should we make sure the opencode window feels like a modal on top of the dash like the claude code tui?
* ~~make sure the background is opaque and NOT translucent everywhere!~~ **DONE 2026-06-28 (investigation).** `internal/tui/dashboard/app.go` `View()` forces `v.BackgroundColor = theme.Page` on every screen except `ScreenExternal` (which owns its terminal — opencode paints its own bg), and `modalView` composites a fully opaque page-colored backdrop layer behind the transcript modal (Fix E). Already enforced.
* opencode tui seems to want to have a few spots be clickable... can we enable that?
* ~~esc can't be used to close the chat because opencode needs esc to get out of some windows~~ **DONE 2026-06-28.** Dropped `esc` from the `ScreenExternal` detach set in `internal/tui/dashboard/app.go` (~:660); only `ctrl+]` / `ctrl+4` detach from the opencode pane now, so `esc` is forwarded to opencode for its own overlays. Regression test: `TestAppExternalPaneEscIsForwardedNotDetached` (`internal/tui/dashboard/actions_test.go`).
* ~~When starting up fresh, drop into an empty dashboard don't automatically launch a claude session~~ **DONE 2026-06-28 (investigation).** Bare `sandbox` already drops into an empty dashboard — `internal/cli/root.go:60-82` calls `dashboard.Run` (no auto-attach); `Model.Init` (`internal/tui/dashboard/model.go:598`) only seeds the list + starts the cluster watch + warm-session poller, never emits `createSessionMsg` (which is only sent by the `n` key, `model.go:1743`). The only paths that auto-attach are `sandbox claude` / `sandbox attach` (intentional).
* Speed up startup? - pre-cache agent images? What other things can we do, should we add some metrics/tracing/observability to this and other parts of the system for us to analyze later?
* Add flox/nix support. Install by default and include agent guidance to use flox/nix. Additionally setup to pull from nix binary cache running inside k8s cluster (how to publish/update cache?)
* **opencode wheel scroll hijacks prompt history (observation).** Inside the opencode external pane, scrolling the mouse wheel up navigates the
  *user's previous prompts* (behaves like Up-arrow) instead of scrolling the conversation transcript. Root cause:
  `app.go.View()` (around `if a.screen != ScreenExternal`) intentionally clears `v.MouseMode` on
  `ScreenExternal` so the embedded opencode TUI owns the terminal — but with mouse reporting **off** on the host,
  the terminal (Ghostty) falls back to the standard "wheel = Up/Down arrow keys" mapping. Those arrow keys fall
  through `ScreenExternal` Update's `default` branch and are forwarded to opencode via `external.handleKey`. Fix
  direction: enable `tea.MouseModeCellMotion` on `ScreenExternal` too (so wheel events arrive as `MouseWheelMsg`,
  not arrow keys), then translate wheel events into opencode's scroll key (`PageUp`/`PageDown` most likely).
  Requires verifying what key opencode actually uses to scroll its transcript view before committing to a key.
  **DONE (Phase 3, item 3):** enabled `tea.MouseModeCellMotion` on `ScreenExternal`.
  A live PTY capture confirmed opencode enables mouse tracking itself (DECSET
  1000/1002/1003 + SGR 1006), so `handleMouse` forwarding real SGR mouse lets
  opencode's OWN wheel-scroll + clicks work — no manual `PageUp`/`PageDown`
  translation needed. Tests in `phase3_item3_test.go`.
Why does jst dev start a claude sessinon automatically?

Forward-looking backlog. Completed-work history was pruned on 2026-06-24 into
[`docs/archive/done-log-2026-06.md`](docs/archive/done-log-2026-06.md). The full
verified review behind most items below is
[`docs/review-2026-06-24.md`](docs/review-2026-06-24.md) (file:line + evidence for
each). Keep this file as running notes — when a batch lands, summarize it in one
line and move the detail to the archive.

## Must-fix (correctness / honesty bugs)

- [x] **Detail-pane "needs you" key hints were wrong on 3 of 4 actions.** Fixed
  2026-06-28: the row was unchanged in *intent* (attach / rename / suspend / destroy
  for a waiting/needs-input/failed session) but each key char is now correct —
  `↵ attach / R rename / x suspend / ! destroy` — matching `keymap.go` (rename is
  the capital `R` binding; lowercase `r` is resume, which doesn't apply to these
  states). `internal/tui/dashboard/model.go:2268`.
- [x] **Permission approve/deny errors are silently swallowed** (chat + dashboard).
  Fixed (Phase 1, `feat/ux-polish`): added `case approveResultMsg` → `m.actionErr`
  in `model.go` Update; chat `resolvePermission` now returns `permResolveErrMsg`
  and appends a `blockError`. Tests in `phase1_ux_test.go`.
- [x] **Dashboard sits on skeleton bars forever (no error) when the cluster is
  unreachable.** Fixed (Phase 1): `seedCmd` returns `seedFailedMsg`, `m.seedErr`
  drives an error+`r`-retry branch in `renderRowLines`, self-heals on next
  seed/watch. Tests in `phase1_ux_test.go`.
- [x] **OSC 9;4 tab-progress is dropped by the v2 cell renderer** (same class as
  the desktop-notification bug fixed 2026-06-28). Fixed (Phase 3, `feat/ux-polish`):
  the progress signal now rides `tea.Raw` from `App.Update`, edge-triggered against
  `App.lastProgress` (replaces `progressActive`) so it emits once per aggregate-state
  transition (idle/busy/error) and goes quiet when steady; `withTerminalSignals`
  keeps only the Kitty prepend. `lastProgress` resets under `ScreenExternal` so it
  re-asserts on detach. Tests in `osc_signals_test.go`.

## New-session startup speed (ordered by likely win)

- [ ] **Shrink + split images, and deploy Spegel** — the image pull dominates cold
  start and nothing warms it; the default image carries an opencode-only `npm i -g
  opencode-ai` layer the claude path doesn't need (and codex will add more). Plan:
  split per-backend images (claude-only default / opencode / codex) so each agent
  pulls only what it needs, **and run Spegel** (P2P/cluster-local OCI mirror, via
  Argo/GitOps) so a cold node hits a peer cache instead of the upstream registry.
  `internal/k8s/backend.go:516`, `runner/Dockerfile:59`.
- [ ] **Stop gating the visible prompt on the 12s blocking first-sync flush** —
  open the transcript as soon as the runner is healthy; background the bounded
  flush (reuse the reconnect pattern). Keep the *turn submission* gated on
  staging so codebase prompts don't run against an empty workspace (see verifier
  note in the report). `internal/cli/connect.go:142-158`, `internal/sync/sync.go:185`.
- [ ] **Parallelize independent serial steps** — Secret+PVC creates (errgroup,
  then Sandbox); the 8 serial `mutagen sync create` execs (only the project sync
  is load-bearing, create the 7 config/transcript syncs lazily); the two
  serial port-forwards (HTTP+SSH). `backend.go:226-260`, `sync.go:131-150`,
  `portforward.go:47`.
- [~] **Tighten `waitForPodReady` poll 2s→~500ms-1s** and surface pod-phase detail
  to the connect stepper. Pod-phase detail DONE (Phase 2): `waitForPodReady` now
  takes an `onPhase(detail)` callback (`podPhaseDetail` classifier) reported under
  StageResume. Poll-interval tightening still TODO (deferred perf, out of UX scope).
- [ ] Defer/deprioritize `ensureReaper` and the launch-burst observer connects off
  the foreground connect path; drop the redundant connect-time Status Get +
  re-`ensureSSHKey` on the freshly-created path. `connect.go:93,144,184`.
- [~] **Mutagen sync GC — leaked-sync cleanup (host-side resource leak).** Root cause:
  the host mutagen daemon outlives the CLI and is blind to the cluster, so a session
  that dies any way other than CLI `destroy`/`suspend` (the in-cluster **idle reaper**
  replicas→0, `just dev-reset`/`dev-nuke` + `kubectl delete`, eviction/node-drain, CLI
  crash/SIGKILL) leaks ~8 syncs (project + config-* + transcripts-*) retrying the gone
  pod forever (`ConnectingBeta`) — observed 634 dead syncs in one daemon. **DONE
  (this branch):** `Manager.List`/`IsOrphanStatus`/`TerminateByIdentifier` (scoped to
  the `sandbox-session` label, never the lima `sandbox-vm-id` syncs); a dashboard GC on
  the reconcile tick that reaps an orphan only when its session's pod is NOT
  Running/Creating per the authoritative snapshot (so idle-reaped **Suspended** sessions
  are cleaned, not protected — MF1) after a 90s grace; `sandbox sync gc [--dry-run]`
  with a cluster-outage guard; `ResumeAll` on attach so a CLI-suspended session re-syncs
  on re-attach (MF2); partial CreateAll recovery on empty ProjectPath (MF4). Adversarial
  review verified. **Follow-ups (deferred, from the review):**
  - **MF3 — cross-context over-reap.** The daemon is shared across kubeconfig contexts;
    GC liveness is checked against one context, so a *different* k8s cluster's down sync
    is reaped (churn, not data-loss: re-created on next attach). Fix: stamp syncs with
    `--label sandbox-context=<ctx>` in `CreateAll` and scope `List`/`sync gc` to the
    current context. Not triggered today (only one live k8s context in use).
  - **MF5 — mid-session sync loss doesn't self-heal** while SSE is healthy (recovery
    only runs in `establish` on SSE drop). Fix: when `SyncProber` reports an attached
    session's sync is stalled/absent, re-run `CreateAll`+`Flush`.
  - **SF1 — auto-GC only runs T+30s into an open TUI.** Fire `reconcileListCmd` in
    `Init` so the clock starts at launch, and run the `sync gc` core best-effort at CLI
    startup (after MF3 scoping). `internal/sync/sync.go`, `internal/tui/dashboard/model.go`,
    `internal/cli/commands.go`.
  - Make `just dev-reset`/`kind-down` run `sandbox sync gc` (or terminate the
    local-cluster syncs) before deleting pods, so dev churn self-cleans.

## UX / communication

- [x] **Cold start shows a blank, frozen, TUI-less terminal** during pod
  schedule + image pull. DONE + live-verified (Phase 2): the pod-ready wait moved
  out of the pre-TUI `backend.Start` into the connect path (`establish`), so the
  animated splash (per-phase detail `Starting pod — scheduling`/`pulling image`/
  `starting` + elapsed timer) is already on screen during schedule + image pull.
- [x] Connect/create splash has no elapsed timer (reconnect header does). Fixed
  (Phase 2): `App.connectStartedAt` rendered on the `connectingView` title via the
  `roundDur` reconnect idiom. Test in `phase2_ux_test.go`.
- [x] Chat status line never surfaces file-sync state; a stalled sync is invisible
  while attached. Fixed (Phase 3): a trailing row-1 sync segment (`syncSegment()`,
  ✓ synced / ⟳ syncing / ⚠ stalled, coral on stall) driven by a new
  `TranscriptModel.syncStatus` fed from the dashboard's warm-session poll after each
  delegation (no new probe). Gated on non-empty so the default status line stays
  byte-identical. Tests in `phase3_ux_test.go`. `statusline.go`, `app.go`.
- [x] connectErr/actionErr persist in the detail pane with no dismiss. Fixed
  (Phase 1): bare `esc` dismisses both in `handleKey` (`model.go`). Test in `phase1_ux_test.go`.

## Performance

- [ ] **Mutagen `sync list` subprocess forked per warm session every 4s** for the
  dashboard's whole life, regardless of focus. Gate on focus/visibility + back
  off. `model.go:679`, `sync_support.go:118`, `status.go:42`.
- [ ] Warm-session detail preview re-lays-out + reconciles the *entire* retained
  transcript every frame (no unchanged-guard). `model.go:2253`, `transcript.go:2008-2025`.
- [ ] `visibleSessions()` re-filters+re-sorts the list 4+ times per frame
  (called twice in one statement at `groups.go:145`). Memoize per frame.
- [ ] `bodyView` still ~283µs/frame: `fitModal` does two ANSI `lipgloss.Width`
  scans per visible line every frame. `transcript_list.go:302`. (Open caveat from
  the prior perf pass.)
- [ ] SSE `broadcast()` re-`JSON.stringify`s each event once per client; serialize
  once. `runner/src/events.ts:200`.
- [ ] Streaming-markdown safe-boundary predicates rescan the whole growing buffer
  per delta (O(N²) over a turn). `internal/tui/dashboard/chat/streaming_markdown.go:111-233`.

## Agent UX parity (Claude vs OpenCode vs Codex) — see also the Codex plan

**Parity bar (maintainer 2026-06-24):** startup speed, detach + keybindings,
prompt/affordance UX, and the **metrics we surface** must be similar across all
agents. Per-agent in-pane rendering can differ. **The runner is the control plane
and should be the metrics source for every backend** — incl. external-pane ones —
via a passive observer connection to the agent's event stream. See
`docs/codex-integration-plan.md` "Parity bar".

- [x] **Runner-as-metrics-observer for external-pane backends.** DONE + live-verified
  for opencode (Phase 4): `runner/src/opencode-observer.ts` subscribes to `opencode
  serve`'s event stream always-on and emits normalized turn/usage/tool/title events
  → SSE → the list row + external-pane statusline get live status/ctx%/cost/title.
  No schema change. Codex remains TODO (same pattern, app-server thread
  notifications). Verified in events.db: a real interactive turn produced the full
  turn-1 sequence; `external_pane.go` statusRow now reads the live read-model.
- [x] OpenCode in-agent tool use is **neither audited nor gated** by the runner's
  Bash blocklist (Claude is). FIXED 2026-07-01: **gating** via a guardrail plugin
  generated at boot from `guards.ts` (`serializeBlockedPatterns` → lossless
  `new RegExp(source, flags)` embed) and registered in the opencode config
  `plugin` array (v1.17.7 file-plugin spec, verified against the pinned
  `@opencode-ai/plugin` types + sst/opencode source); its `tool.execute.before`
  throws on a blocked `bash` command. Fail-open with a loud log (defense-in-depth
  only). **Audit** via an injectable `AuditTool` in `createOpencodeTurnMapper`
  (headless `/turns`) + `ObserverDeps.audit` (interactive cycles) → `audit.jsonl`.
  Tests in `runner/test/opencode-guardrail.test.ts` (incl. importing the emitted
  plugin and asserting `kubectl get pods` throws) + mapper/observer audit tests.
- [~] CLI `opencode` has no `--model` flag and no initial-prompt arg; `cancel` and
  the suspend active-turn warning are inert for opencode. `cancel` now WORKS
  (Phase 4): the observer sets `last_turn_id` and the interrupt route gained an
  opencode-abort fallback (`server.ts`) — live-verified (`sandbox cancel` emits
  `turn.interrupted`, was a 404 no-op). The suspend warning's (and CancelTurn's)
  monotonic-`last_turn_id` false-positive was FIXED 2026-07-01: `/status` now
  exposes a live `activeTurnId` (registry-derived, opencode busy fallback) and
  cancel/suspend key off it. `--model`/initial-prompt arg still TODO.
  `claude_remote.go:23-71`.
- [ ] Verify detach (Ctrl+]) + surrounding chrome behave identically for every
  backend's external pane.
- [x] **opencode in-turn missing-session recovery (claude parity).** SUBSUMED by the
  Phase C `ensureSession` fix: when the persisted session is gone, `session.get`
  404s → the id is cleared → it falls through and **creates a fresh session in the
  same call**, so the turn proceeds and succeeds (exactly the review's "Turn 2
  resumes a lost session" scenario, now fixed in-turn). The only residual is a 404
  in the tiny window between a successful probe and the prompt — negligible,
  self-heals next turn — so the risky `runTurn` restructure to mirror
  `claude.ts:511-573` is not warranted. `runner/src/opencode-turn.ts`.
- [ ] **Per-backend CLI smoke.** `internal/k8sit/cli_smoke_test.go` `TestCLISmoke`
  is opencode-only; make it table-driven over `backendCases` (gate the non-empty-
  output assertion on `expectRealReply`) so claude/codex fill the column.

## Done recently (parity testing — uncommitted)

- **opencode turn parity (Phases G/B/C).** Streaming deltas, permission auto-respond
  flow, and resume/continuity in `runner/src/opencode-turn.ts`; model-selection `||`
  precedence fix; k8sit conformance suite (interrupt/error-surface/reconnect/lifecycle,
  table-driven). Two real bugs found+fixed by the suite: **`Backend.Resume` returned
  early on the terminating pod** (`internal/k8s/backend.go` `waitForPodReady` now
  ignores a pod with a `DeletionTimestamp`; `backend_resume_ready_test.go`), and the
  **opencode resume path raced `opencode serve` boot** (now probes `session.get` with
  retry). See `docs/parity-RESUME.md`.

## Stale docs to fix (review confirmed; not yet edited)

- [x] Probes ARE implemented (`backend.go:698-721`); SECURITY.md / LAUNCH-CHECKLIST /
  HARDENING-BACKLOG now reflect that (C9 moved to "Already fixed"; L-CHECK checked
  off; SECURITY bullets pruned). Done 2026-06-28.
- [x] `architecture.md` "Security model" / "Unvalidated paths" now records that
  capability drops + `allowPrivilegeEscalation:false` landed (BR1, `backend.go`), with
  only non-root + `fsGroup` left as M20; the dead `k8s/agent-sessions/networkpolicy.yaml`
  link replaced with the real flat `k8s/networkpolicy-*.yaml` files. Done 2026-06-28.
- [x] `runner-api.md` corrected: `/exec` blocked → "refused pre-spawn, exit 126"
  (127 for spawn failures); runner-side `SANDBOX_DEBUG` claim removed (C10 tracks
  it); `rate_limit.updated` payload now lists all 10 fields
  (incl. `subscriptionType` + per-model `sevenDay[Opus|Sonnet]*`). Done 2026-06-28.
- [x] README marketed `sandbox claude` as "start (or reuse)" — it always mints a new
  session. Fixed (Phase 5): "start a **new** session" across `README.md` (Quickstart
  comment, prose, Commands table ×2), the `claude` command help string, and the
  misleading "via the dashboard, reuses" comment in `internal/cli/claude_remote.go`.
  Resume is only `sandbox attach` / the dashboard list.
- [x] ~~ghostty header "proposed/not started" + verification-protocol dead spec
  refs~~ — fixed 2026-06-24.

## Unbuilt features / extracted plan remnants

- [ ] **T10 — working-directory picker** (only unexecuted superpowers plan;
  `docs/superpowers/plans/2026-06-22-t10-working-dir-picker.md`): dirPicker overlay
  end-to-end — `dirpicker_path.go` (~-expansion, child listing, longest-common-prefix
  completion, validation) + overlay struct (open/close, prefill, Tab, recents) +
  wiring before the backend picker + thread `projectPath` into the Creator. None exists.
- [ ] **Tekken-style agent-picker modal** — cool animations + per-agent
  "portrait"/avatar (ascii/ansi). Carried from the old Manual Additions.
- [ ] Ops: new CLI-created sessions use `:latest` and can hit the stale traefik
  manifest cache — bust the cache or pin digests CLI-side.

## Codex backend (new) — Option B: remote app-server + local `codex --remote` TUI

Plan: [`docs/codex-integration-plan.md`](docs/codex-integration-plan.md). Mirrors the
OpenCode supervisor/external-pane pattern + runner metrics-observer. Backend id
`codex-app-server` already reserved in `internal/session/types.go:52`. Auth =
ChatGPT-plan OAuth (`codex login --device-auth`; access token lasts 10 days) owned by
the CLI credential manager below.

- [x] **Auth + cluster status surface (read-side)** — DONE 2026-06-24. New
  `internal/cred` package (Provider abstraction + Claude/Codex/OpenCode providers,
  offline, secret-free; JWT-exp decode for codex) + `internal/k8s` `Ping`/`Host`/
  `Namespace` + `sandbox auth status` rendering red/green per agent/provider + k8s
  reachability. Tested (`internal/cred/cred_test.go`, `internal/cli/auth_test.go`).
  **Remaining:** dashboard strip rendering (currently CLI-only); `--check` live
  pings (codex plan/rate-limit via app-server; provider key liveness); Claude
  reads env only (the real setup-token may live in a keychain — folds into the
  store below).
- [ ] **CLI-owned credential manager — write side.** Build on `internal/cred`:
  add the **macOS Keychain** store (optional Secure-Enclave-wrapped blob + Touch
  ID; file/env fallback on Linux), `sandbox auth {login,sync,logout}` (device-auth
  / setup-token / paste-key), and the create/connect **reconcile** that seeds the
  `agent-sessions` Secret + **prompts for renewal** when a cred can't auto-refresh.
  Agent-agnostic (codex first; then Claude OAuth + provider keys). Generalizes
  `ensureSSHKey`. Egress allowlist must gain OpenAI/ChatGPT auth+API hosts.
- [ ] **Codex transport spike — remaining (off-airplane).** Partial results recorded
  in the plan: stdio app-server works (newline JSON-RPC, no-auth initialize);
  remote-control/ws needs the STANDALONE managed install (bundle in the pod image);
  refresh+approvals are delegated to the client; metrics/auth-status are client
  requests (`rateLimits/read`, `usage/read`, `getAuthStatus`, `account/read`).
  Still TODO live: ws endpoint addressing + a 2nd-client thread-observe check.

## Deferred from the 2026-07-01 review sweep (client package + TUI)

API-design decisions deliberately NOT auto-fixed (maintainer call), from the
three-agent review of `feat/public-client-package` (client/branch, public `tui/`,
dashboard). All the verified *bugs* from that sweep were fixed on the branch.

- [ ] **client: no external test seam.** `WithBackend` takes the concrete
  `internal/k8s.Backend`, which importers can't name — so the option is unusable
  outside the module and there's no way to inject a fake for `Create`/`Connect`
  orchestration tests (which have zero unit coverage). Consider a narrow public
  backend interface. `client/client.go:141`.
- [ ] **tui/theme: closed registry + missing exported tokens.** No `Register(Theme)`
  despite the package doc promising pluggable themes; `Theme` fields
  `Denied/Info/Success/Warning(+Subtle)` have no exported active vars (only Busy
  made it), so apps can't reach those tones directly. `tui/theme/theme.go:63,107-144`.
- [ ] **tui/kit: unsynchronized global palette.** `SetComponentColors` writes a
  plain map read on every render; `theme.ApplyTheme` off the render goroutine is a
  concurrent-map panic; two tea.Programs share one palette. Atomic-pointer swap of
  an immutable palette, or document single-goroutine ownership.
  `tui/kit/style.go:21`, `tui/kit/components.go:32`.
- [ ] **tui/list: `Item.Finished()` is dead API** — never called, every implementer
  must write it, doc points at a spec that doesn't ship. Drop it (breaking OK
  pre-OSS). `tui/list/list.go:12`.
- [ ] **dashboard: consolidate the two row models.** `visibleSessions()` vs
  `visibleRows()` both interpret `m.cursor`; the group-view mis-selection class
  (fixed 2026-07-01) exists because of the split. One row abstraction with a
  `sessionAt(cursor)` accessor for render+nav+actions; also `groupedSessions()`
  ignores the active filter/attention sort. `internal/tui/dashboard/groups.go:57`.
- [ ] **dashboard: `applySeed` re-seed drops live fields** (seenSeq, usage tokens,
  Model, Branch, RecentTools) — unread badges + ctx%/cost reset on a manual `r`
  retry. `internal/tui/dashboard/model.go:1452-1483`.
- [ ] **dashboard: `partition()` computed 3× per frame** (topBar, clusterStrip,
  progressState). Compute once per render. `internal/tui/dashboard/zones.go:319`,
  `model.go:2572`.
- [ ] **client: `Destroy` stops sync *after* the cluster destroy** (pre-existing;
  the TUI's PreDestroyHook covers the interactive path, library callers race EOF
  errors). `client/client.go` Destroy.
- [ ] **client: `DialRunner` forwards the unused SSH port** (pre-existing; minor
  waste per one-shot call). `client/client.go` DialRunner.
- [ ] **kit.FormatTokens caps at "1000M"** — no unit above M; fine until a billion
  tokens is a real number. `tui/kit/style.go`.
- [ ] **WithStateDir ssh-dir layout**: the per-session SSH include lives in a
  *sibling* `ssh/` dir of the state root (doc now says so honestly). If a cleaner
  contained layout is wanted pre-OSS, it's a breaking change needing an include-path
  migration. `client/sync.go` sshConfig.

## Whole-system design review — 2026-07-01 (deep multi-domain)

Second review pass on 2026-07-01: 7 domains (CLI↔runner seam, event/API contract,
scalability, security reviewed directly; k8s lifecycle, file-sync/storage,
abstraction seams via subagents). Every HIGH + load-bearing MEDIUM re-verified
against source at the cited `file:line`. Deduped against `docs/review-2026-06-24.md`
+ the hardening backlog — these are **new**. Two items were already tracked and are
cross-referenced (not re-listed): `WithBackend` internal type → "client: no external
test seam" above; tui/kit/theme global palette state → "tui/kit: unsynchronized
global palette" above.

### 🔴 No version story anywhere (one root gap, three manifestations)

Because OSS users build + pass their own `--runner-image`, skew between CLI, runner,
and on-disk PVC state is the steady state, not an edge case.

- [x] **No CLI↔runner protocol version handshake (HIGH).** FIXED 2026-07-01, schema-
  driven: `schema/events.json` gained top-level `protocolVersion: 1`; `just gen` now
  emits `session.ProtocolVersion` (Go) + `PROTOCOL_VERSION` (TS) with a drift test
  (`TestProtocolVersionMatchesSchema`). Runner reports it on `/healthz` +
  `StatusResponse`; `runner.Client.Health` caches it (0 = pre-handshake image),
  and both `client.Session.Connect` (→ `Connection.Warning`, via a new
  `appendWarning` so it survives later sync warnings) and headless `waitHealthy`
  (stderr) **warn, never refuse** via the shared `runner.ProtocolMismatchWarning`.
  Correction to the review claim: the TUI `ApplyRunnerEvent` switch already HAD a
  `default: return false`; it's now explicitly documented as the skew safety net.
  Bump the schema field whenever an event/payload/SSE change could silently
  misbehave across versions.
- [ ] **`events.db` schema version is write-only; no migration (HIGH).** `SCHEMA_VERSION`
  is stamped to `user_version` on every open but never read-compared, and there is no
  migration fn — the "bump + migrate on shape changes" comment describes a mechanism
  that was never built. `session.json` has no version field at all. Read-compare-migrate
  on open; stamp a version into `session.json`. `runner/src/events.ts:31,91`.
- [ ] **`:latest` + `PullAlways` swaps the binary under old PVC state on resume (HIGH).**
  `resolveImagePullPolicy` gives any non-digest ref `PullAlways`; resume only touches
  `.spec.replicas` (never rewrites the image), so resuming a weeks-old suspended session
  pulls the current `:latest` and runs a newer runner against the old `events.db`/
  `session.json` — reinterpreting old rows under new-shape assumptions, or feeding
  `after=0` replay of stale-shaped events to a new CLI. Consider digest-pinning the
  default runner image so resume is reproducible. `internal/k8s/backend.go:164`,
  `client/client.go:77`.

### 🔴 The pod↔laptop sync boundary is porous both ways

- [x] **Project sync ignores `.gitignore`; secrets flow laptop→pod (HIGH).** FIXED
  2026-07-01: `createProjectSync` now translates the project root's `.gitignore`
  verbatim into `--ignore` flags (`internal/sync/gitignore.go`; mutagen's syntax is
  gitignore-modeled, `--ignore-syntax` only offers mutagen|docker so pass-through it
  is). Layering is mutagen later-wins: build-tree defaults (overridable by a
  `!vendor`-style negation) → `.gitignore` → security set LAST (non-overridable).
  The false "mutagen.yml" escape-hatch comment is gone. Limits (documented in the
  code + architecture.md): root `.gitignore` only — nested files + git's global
  excludesFile are not consulted. Unreadable-but-present `.gitignore` fails the
  create (fail-closed). Tests: `gitignore_test.go`,
  `TestCreateProjectSyncIgnoreLayering`.
- [x] **Two-way sync propagates pod-authored auto-executing files back to the laptop
  (HIGH, conditional).** FIXED 2026-07-01: the non-overridable security ignore layer
  now also excludes `.envrc`, `.direnv`, `.vscode`, `.idea` (host-auto-executing
  classes). Makefile-class files deliberately NOT ignored — explicit user action
  only, and agents legitimately edit them. `internal/sync/sync.go` securityIgnores.
- [x] **Egress allowlist is a lateral-movement boundary, not an exfil one — undocumented
  (MED).** DONE 2026-07-01: architecture.md "Security model" now states it plainly
  (compromised agent can POST anything readable to any public :443 host; exfil
  control = the file-sync ignore boundary) and gained a "File-sync boundary" bullet
  describing the ignore layering.

### 🔴 Partial create leaks a live auth token invisibly

- [x] **`CreateSession` has no rollback; orphans a bearer-token Secret + PVC (HIGH).**
  FIXED 2026-07-01: `CreateSession` now rolls back on partial failure via a deferred
  best-effort `deleteSessionResources` (extracted from `Destroy`, shared; NotFound-
  tolerant, so it also sweeps any earlier orphan at the same id) on an independent
  30s `context.WithoutCancel` context; rollback failure is appended to (never masks)
  the original error. Tests: `TestCreateSessionRollsBack*` in
  `internal/k8s/backend_c5_test.go`. A `sandbox gc` reconciler + pre-derived retry
  ids were judged unnecessary once rollback exists.

### 🟠 Scalability + reliability

- [ ] **O(sessions) laptop cost with no steady-state cap (MED-HIGH).** `connectSem` (cap 4)
  throttles the connect *burst* only — its own comment says it does not limit open streams.
  Steady state: N warm sessions = N SPDY port-forwards through one kube-apiserver + N SSE
  streams + ~2N goroutines + N 30s heartbeat timers, no LRU eviction. First breakage is
  API-server port-forward pressure (~30 sessions). Cap concurrently-*established* observer
  forwards and evict the coldest. `internal/tui/dashboard/model.go:1166`.
- [ ] **SSE consumer backpressure → forced disconnect/replay loop (MED).** Events channel is
  buffered at 64; if the TUI stalls during a heavy turn the scanner blocks on send, the 90s
  watchdog sees no reads and force-closes the body → reconnect-with-replay. A slow consumer
  manufactures reconnects. `internal/runner/client.go:234,250`.
- [ ] **Port-forward retries a dead pod forever (MED).** On `resolvePodForForward` error the
  loop keeps the *stale* pod and retries with no terminal state — after `sandbox destroy`
  from another shell, an observer forward hammers the vanished pod at ≤10s cadence
  indefinitely; no "gone forever" vs "rescheduling" distinction surfaced to the handle owner.
  `internal/k8s/portforward.go:148`.
- [ ] **Dead-node pods read as Running for minutes (MED).** Both status paths trust
  k8s-reported conditions with no staleness cross-check (`LastTransitionTime` vs now, or an
  independent runner probe) — standard node-eviction lag, so a node-crashed session looks
  healthy with a silently-stalled SSE stream and no "unreachable" status distinct from
  "running". `internal/k8s/backend.go:431`, `internal/k8s/watch.go:203`.

### 🟠 Abstraction + contract

- [ ] **The "normalized" turn/state model is Claude-SDK-shaped (MED).** `TurnInput.Mode` is
  documented as the literal SDK permission-mode enum (opencode discards it, `_mode`; Codex
  will too); `Connection.Opencode` is a backend-specific field in the library's central
  return type (the Codex plan already calls for generalizing it — a pre-announced breaking
  change to a public struct with no versioning promise); `State.ClaudeSession` has no slot
  for opencode's resume id so it's unconditionally `""` for every opencode session. Model
  execution-policy/state abstractly, not as SDK unions. `internal/session/types.go:132,107`,
  `client/session.go:62`.
- [ ] **`destroy` gives no active-turn warning though reversible `suspend` does (LOW-MED).**
  `suspend` dials the runner and warns on `ActiveTurnID`; the irreversible `destroy`'s
  `confirmDestroy` prints only a generic prompt — the more destructive command tells you
  less about interrupting live work. `internal/cli/commands.go:168` (vs `:97`).

### 🟢 Lower / contained

- [ ] **Transcript sync merges pod-agent history into local `~/.claude` unscoped (LOW-MED).**
  By design (subPath bind so the cwd/transcript-dir encoding matches), but pod-run agent
  conversations become locally `--resume`-able alongside the user's own sessions with no tag
  or audit trail back to the sandbox session. `internal/k8s/backend.go:873`,
  `internal/sync/sync.go:62`.
- [ ] **Concurrent sessions on the same project share one local sync endpoint, no dedup
  (LOW-MED).** Mutagen session name keys on SessionID only, not ProjectPath, and nothing
  checks for a collision — two agents on the same repo silently cross-feed edits (and, on a
  same-file race, surface as perpetual conflicts). `internal/sync/sync.go:130`.
- [ ] **Mutagen conflicts are invisible in the TUI (LOW-MED).** `classify()` collapses
  `len(Conflicts)>0` into the same `SyncStalled` glyph as a transport error, discarding which
  file/side; the detail pane shows a bare "⚠ stalled" with no resolution hint (`mutagen sync
  list <name>`). `internal/sync/status.go:154`, `internal/tui/dashboard/model.go:2431`.
- [ ] **`sandbox shell` has no `client/` equivalent — dogfooding gap (LOW).** Interactive
  pod-exec is implemented directly against `internal/k8s` with no public-library path; an
  external consumer can't replicate a shipped CLI command, so nothing validates whether the
  façade *could* support it. `internal/cli/shell.go`.

## Open caveats (carry-forward)

- [ ] Resumable-transcripts migration: pre-existing sessions' old
  `-session-workspace-…` transcripts may break in-session resume-by-id across the
  host-path migration → call out in release notes.
- [ ] rate-limit/usage: unverified against a live max/pro session; consider pinning
  the Agent SDK version; `seven_day_oauth_apps` + `extra_usage` (overage) are
  dropped runner-side; black-line/opacity fixes unverified in a live attach.
- [ ] `~/.claude/todos` + `~/.claude/tasks` sync is ancillary (not required for
  resume) — keep but low priority.

## Claude-pane visual/UX pass — 2026-07-01 (deferred items)

Fixed on this pass: permission box shows the tool arg + gold badge header +
contextual ↵ hint (was blind approvals + invisible OnGold header), queued-prompt
chip on the hint row (was invisible state that silently changed esc semantics),
perm queue + dashboard detail show the pending arg, palette esc-to-close, chat
help drift (ctrl+f/o/g, shift+enter), `?` overlay drift (detach/group/rename/
archive), tool.delta raw-JSON leak (parsed live preview), status-line style
memoization, single-pass list.Metrics(), ∴/▤ glyphs for thought/todo emoji,
subagent child cards match the flat calm style, blockWarn tone for pod
reschedule, footer "◇ —" placeholder. Deferred:

- [ ] **No expansion for flat tool-card output (MED — acknowledged "slice 5i").** Agent
  Bash output collapses to "N lines" with no way to view it; user `!cmd` shows full
  output. Also: post-approval diffs vanish from scrollback (only the permission box
  ever renders the diff). `internal/tui/dashboard/transcript.go:1258`.
- [ ] **Multi-line reasoning unrecoverable (MED).** `∴ Thought (N lines): <first line
  truncated to 40>…` — full thinking text is never viewable; needs expand or a
  wrapped multi-line render. `internal/tui/dashboard/transcript.go:1073`.
- [ ] **No prompt history (MED).** No up-arrow recall of previously sent prompts in the
  composer. `internal/tui/dashboard/transcript.go:1762` (scrollKey owns ↑/↓).
- [ ] **`q`/`g` overloads on the dashboard (LOW-MED).** `q` opens the perm queue when
  any session waits (footer still says quit); lone `g` toggles group view, `gg` = top.
  Surprising vs the advertised bindings. `internal/tui/dashboard/model.go:1817,1866`.
- [ ] **Permission scope is always "once" (LOW-MED).** No allow-for-session option in
  the inline box or queue even though the event contract has `allow-session`.
  `internal/tui/dashboard/transcript.go:2042`, `internal/session/event.go:59`.
- [ ] **ctrl+g/ctrl+k dead in the external pane; no next-attention key on the dashboard
  screen (LOW).** Cross-session nav is inconsistent by screen. `internal/tui/dashboard/app.go:711`.
- [ ] **Fresh Claude session renders a blank body (LOW).** No welcome/first-hint block,
  unlike the dashboard's firstRunView. `internal/tui/dashboard/transcript_list.go:290`.
- [ ] **ctx% fallback inconsistency (LOW).** Chat status line assumes 200k when the
  model limit is unknown; dashboard hides the gauge instead.
  `internal/tui/dashboard/statusline.go:402`, `internal/tui/dashboard/session.go:197`.
- [ ] **Reconcile is O(n) per tool/subagent event (perf, LOW until transcripts get very
  long).** `reconcileItems` rebuilds the item slice + survivor map per non-delta event;
  fine at hundreds of blocks. `internal/tui/dashboard/transcript_list.go:180`.
- [ ] **Resize is uncoalesced (perf, LOW).** Width change drops the whole list cache and
  rebuilds a pooled renderer; a drag-resize repeats this per WindowSizeMsg. `tui/list/list.go:74`.
- [ ] **Glamour pads wrapped lines with per-space SGR runs (bytes, LOW).** Every
  assistant line carries ~width styled single-space cells in the frame string;
  bubbletea's renderer absorbs it, but it inflates parse work. Upstream glamour style.
- [ ] **Failed sessions aren't floated by attention-first sort (LOW).** Only
  Waiting/NeedsInput partition to top. `internal/tui/dashboard/attention.go:16`.
- [ ] **TestAppExternalPaneEscIsForwardedNotDetached fails in-sandbox (test env).**
  PTY spawn blocked; passes with sandbox disabled — add to the in-sandbox caveat list.
  `internal/tui/dashboard/actions_test.go:403`.

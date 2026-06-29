# TODO — running notes

> **UX/UI polish push (2026-06-28):** the in-scope UX work below is now organized
> into a phased, resumable plan at [`docs/ux-polish-plan.md`](docs/ux-polish-plan.md)
> (6 phases, each with a self-contained restart prompt). Branch: `feat/ux-polish`.
> Under-the-hood perf/infra items here stay deferred per that plan's "Deferred" section.

## Human added needs triage
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
- [ ] OpenCode in-agent tool use is **neither audited nor gated** by the runner's
  Bash blocklist (Claude is). Security-relevant. `runner/src/server.ts:225-247`,
  `guards.ts`.
- [~] CLI `opencode` has no `--model` flag and no initial-prompt arg; `cancel` and
  the suspend active-turn warning are inert for opencode. `cancel` now WORKS
  (Phase 4): the observer sets `last_turn_id` and the interrupt route gained an
  opencode-abort fallback (`server.ts`) — live-verified (`sandbox cancel` emits
  `turn.interrupted`, was a 404 no-op). `--model`/initial-prompt arg + the suspend
  warning's monotonic-`last_turn_id` false-positive still TODO. `claude_remote.go:23-71`.
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

## Open caveats (carry-forward)

- [ ] Resumable-transcripts migration: pre-existing sessions' old
  `-session-workspace-…` transcripts may break in-session resume-by-id across the
  host-path migration → call out in release notes.
- [ ] rate-limit/usage: unverified against a live max/pro session; consider pinning
  the Agent SDK version; `seven_day_oauth_apps` + `extra_usage` (overage) are
  dropped runner-side; black-line/opacity fixes unverified in a live attach.
- [ ] `~/.claude/todos` + `~/.claude/tasks` sync is ancillary (not required for
  resume) — keep but low priority.

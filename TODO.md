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
* ~~Why does `just dev` start a claude session automatically?~~ **DONE 2026-06-30
  (investigation).** By design, not a bug. `just dev` runs the **`sandbox claude`**
  subcommand (the explicit "start a new session" entrypoint), not bare `sandbox`
  (the empty dashboard). Chain: `justfile:217` `dev backend="claude"` → `dev-tui`
  (`:326`) → `exec go run ./cmd/sandbox {{backend}}` = `sandbox claude`
  (`claude_remote.go:33-38` → `runStartSession(..., BackendClaudeSDK, ...)`, whose
  own help reads "Start a **new** remote Claude SDK session"). It is routed through
  the subcommand on purpose: `--runner-image`/`--reaper-image` (which pin the local
  `:dev` build, `justfile:336-337`) exist **only** on the `claude`/`opencode`
  subcommands, not on root (`root.go:85-86`). Bare `sandbox` passes empty image
  strings (`root.go:69` → `newDashboardCreator(backend, "", "")`), so a dashboard
  `n`-created session would pull the default `:latest` runner from GHCR
  (`dashboard_connector.go:116-126`) instead of your fresh local build. **Open
  product call (separate from this bug-or-not question):** if `just dev` should drop
  into the *empty* dashboard instead, add a `dev-dash` recipe (or root-level image
  flags so the empty path can still pin `:dev`).

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
- [~] `visibleSessions()` re-filters+re-sorts the list 4+ times per frame
  (called twice in one statement at `groups.go:145`). Memoize per frame.
  **Partial (2026-06-30):** the literal double-call in `visibleRows()` is fixed —
  `groups.go:145-146` now computes `visible := m.visibleSessions()` once and ranges
  the local (behavior-identical; halves the FilterSessions+sortByAttention+copy work
  in the render helper). **Remaining:** a true per-frame memo so repeated
  `visibleRows()` calls within one render share a result. Deferred — needs cache
  invalidation and `View()` is a value receiver, so it can't cheaply persist a cache.
- [ ] `bodyView` still ~283µs/frame: `fitModal` does two ANSI `lipgloss.Width`
  scans per visible line every frame. `transcript_list.go:302`. (Open caveat from
  the prior perf pass.)
- [x] SSE `broadcast()` re-`JSON.stringify`s each event once per client; serialize
  once. `runner/src/events.ts:200`. **DONE (verified 2026-06-30):** `broadcast()`
  already hoists `sseFrame(evt)` to a single `const frame` before the client loop,
  so fan-out is O(clients) writes with one O(payload) serialize. Stale checkbox.
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


iiiiii

## Agent visualization (claude TUI) — investigation + ideas (2026-06-29)

Triggered by a live `claude-sdk` session screenshot: the tool-call transcript is a
hard-to-scan monochrome wall, and sub-agent dispatch renders badly.

**Observed problems**
- **Sub-agent dispatch renders as a raw flat card, not the nested `subagentCard`.**
  Detection is `p.Tool == "Task" || p.AgentName != ""` (`transcript.go:2291`), but the
  SDK emitted tool name **`Agent`** with empty `AgentName`, so it fell through to
  `startOrUpdateToolCard` (flat). Result: raw/garbled input shown
  (`{"description": "…{"description": "…`, doubled — looks like streaming partial-JSON
  accumulation), and no child-tool tree. **This is the #1 bug behind the screenshot.**
- **`toolArg` has no `Agent`/`Task` case** (`transcript.go:1227-1264`): an Agent dispatch
  on a flat card has no clean label — the generic field-getter looks for
  file_path/command/pattern/url/query, none of which exist on an Agent input
  (`description`/`prompt`/`subagent_type`).
- **Args aren't sanitized for display.** Grep/Glob patterns show raw escaped regex with
  literal `\n` and bled-in prompt text (`func \(m \*Model\) visibleRows\(\)…?\n`).
  `collapseSpaces` only folds whitespace.
- **`toolSummary` is generic** (`transcript.go:1266-1279`): every multi-line result
  collapses to "N lines" — uninformative, and the line gets visually doubled
  (header + echoed truncated continuation).

**Ideas / fixes (cheap → richer)**
1. **Fix sub-agent detection** (highest ROI): match `p.Tool == "Task" || p.Tool == "Agent"
   || p.AgentName != ""`, and when `AgentName` is empty parse `subagent_type` from the
   Agent input into the card's `agentName`. Restores the nested card + child tree and
   kills the raw-JSON garble.
2. **Add an `Agent`/`Task` case to `toolArg`** (description → first line of prompt) so even
   a flat fallback reads cleanly.
3. **Sanitize args**: strip/condense literal `\n`, fold escaped-regex noise, and never
   render raw partial-JSON while a tool's input streams — show `<Tool> <spinner>` until the
   input parses, then the clean label.
4. **De-dupe the card to one line** per tool (`<glyph> <Tool> <arg> … <result>` with the
   result right-aligned), not header + echoed continuation.
5. **Tool-aware summaries** in `toolSummary` (pass the tool name, already available at
   `transcript.go:2302-2303`): Grep → "N matches in M files", Edit/Write → "+X/−Y", Bash →
   exit code + first output line, Read → "N lines".
6. **Visual hierarchy**: color+icon by tool category (read/edit/exec/search/web) so the wall
   isn't monochrome; consider collapsing consecutive same-tool runs.
7. **Expandable flat cards** (like the subagent collapse) so truncated args/results can be
   opened; right-align the result so the arg gets full width.
8. **Diff affordance for Edit/Write/MultiEdit**: a 1-line +/- summary now; inline mini-diff
   on expand later.

Pointers: `internal/tui/dashboard/transcript.go` (`toolArg` 1227, `toolSummary` 1266,
dispatch detection 2291, `finishToolCard` 1161, `startOrUpdateToolCard` 1131),
`internal/tui/dashboard/subagent.go` (nested card render).

## Session checkpoint — 2026-06-29 (host), branch `perf/tui-hotpath`

Perf-cluster push + a live mutagen-sync debugging detour.

- **Perf cluster (see "## Performance"):** item 4 `fitModal` single width scan
  (`transcript.go:804`) **DONE** — checkbox there is stale. Item 5 SSE `broadcast`
  serialize-once (`events.ts:200`) **DONE**. `visibleRows()` double-call **DONE**
  (synced in from a pod agent). **Correction to the item-3 deferral:** `Model.View()` is a
  **pointer** receiver (`model.go:995`), so the render-scoped memo IS feasible — set
  `m.rendering=true` at View() top, keep a 1-frame cache, return it from
  `visibleSessions()` only while rendering (must NOT cache across `Update`: element
  mutations like `DashStatus`/`SyncStatus`/title change filter/sort output). The "value
  receiver, can't persist a cache" note is wrong.
  - **Item 1 (sync poll N→1):** add `Manager.StatusSummaryAll` = one `mutagen sync list
    --template '…{{index .Labels "sandbox-session"}}|{{.Status}}|{{len .Conflicts}}…'`,
    group by label, worst-of via `classify`; add a `BatchSyncProber` option; `syncPollTickMsg`
    (`model.go:839`) issues 1 batch Cmd/tick. `--template` confirmed to expose Labels/Status/Conflicts.
  - **Item 2 (warm preview):** cache `tailLines` (`transcript.go:2037`) keyed on `(lastSeq,width,n)`.
  - **Item 6 (streaming-markdown):** rendering is already cached; the O(N²) is the boundary
    predicates rescanning the whole buffer per delta. Bound them to `content[base:]`,
    `base=len(stableSource)` (a safe boundary ⇒ fence closed + no open hazard; link-ref-defs
    already excluded by the top-of-Render `hasLinkRefDef` reset ⇒ provably byte-identical).
    Lowest payoff (byte ops vs the already-fast glamour renderer) — do last.
- **Mutagen sync (airplane-wifi finding):** the shared host daemon accumulated **25 orphaned
  lima (`sandbox-vm-id`) syncs** after VM deletion; their reconnect thrash starved real k8s
  session syncs (`ConnectingBeta`, "files not showing up in pod"). Reaping them (38→13)
  unblocked the rest — fresh connects then staged + settled to `Watching` in ~5s. Lima is
  **fully retired** → its `sandbox-vm-id` syncs are pure garbage; wire lima teardown /
  `just dev-reset` to terminate them. **BUMP MF5** (mid-session sync self-heal): the SSH
  port-forward drops independently of HTTP/SSE, and recovery only runs in `establish`
  (attach/SSE-drop), so a stalled-but-attached session never self-heals — the recurring
  symptom. Fix: when `SyncProber` reports an attached session stalled/absent, re-run
  `CreateAll`+`Flush`.
- **Opacity bleed-through (connecting screen) — task open:** "Connecting to new session…"
  shows ghost dashboard/transcript text behind the stepper. The transcript modal uses
  `opaqueBackdrop` (solid `theme.Page` — fully opaque), but the connect/reconnect splash uses
  `dimBackdrop` (intentional dim ghost). A *new* session has no convo to dim, yet ghost text
  leaks → the connecting (non-reconnect) view composites a dim/partial backdrop instead of an
  opaque fill. Fix: new-session connect should use `opaqueBackdrop`. Pointers:
  `internal/tui/dashboard/app.go` `opaqueBackdrop`/`dimBackdrop` (~997-1032), connecting view (~1180-1270).

# TODO — running notes

## Human added needs triage
* automatically create worktrees? How to handle ensuring they are merged in and not accidentally "lost"? Since mutagen should sync from my laptop it can merge into "main" from my laptop it just shouldn't try to pull from remote? TUI creates a work tree as part of creating a container. Agent should know to clean it up/give it a proper name before turning it into a pushable branch to be pushed by a human
* Match claude codex ux, make it feel familiar to claude code users to make sure we can expose all of the necessary options/features/configs but make sure its unique enough for us to not cause confusion. There should already be a plan for this, if not lets create one
* Consider KRO to wrap resources into our own "custom resource"? Does it support custom status/conditions?
* Expose CLI as a go library to be consumed by another go package for backend and tui stuff? Or just drive via pre-built CLI for now?
* why is hall.kvitch.dev not in use? Should we just remove that? Are we building our images in tilt and are they then automatically loaded into kind? 

Forward-looking backlog. Completed-work history was pruned on 2026-06-24 into
[`docs/archive/done-log-2026-06.md`](docs/archive/done-log-2026-06.md). The full
verified review behind most items below is
[`docs/review-2026-06-24.md`](docs/review-2026-06-24.md) (file:line + evidence for
each). Keep this file as running notes — when a batch lands, summarize it in one
line and move the detail to the archive.

## Must-fix (correctness / honesty bugs)

- [ ] **Detail-pane "needs you" key hints are wrong on 3 of 4 actions.** Says
  x=destroy / s=suspend / r=rename, but really x=suspend, s=sort, r=resume,
  destroy=`!`. Actively misleading. `internal/tui/dashboard/model.go:2232`,
  `keymap.go:61-65`.
- [ ] **Permission approve/deny errors are silently swallowed** (chat + dashboard).
  The chat path `_ =`-drops `ResolvePermission` errors after optimistically
  showing "approved"; `approveResultMsg` is constructed in 3 places with **no
  handler**. Sibling of the interrupt-error fix. `transcript.go:1981-1995`,
  `model.go:200-204,616-804,1189-1228`.
- [ ] **Dashboard sits on skeleton bars forever (no error) when the cluster is
  unreachable.** seed/watch/reconcile swallow List/Watch errors and `seeded`
  never flips → no actionable failure state. `model.go:832-858,1931-1941`.
- [ ] **OSC 9;4 tab-progress is dropped by the v2 cell renderer** (same class as
  the desktop-notification bug fixed 2026-06-28). It's still prepended to
  `v.Content` in `app.go` `withTerminalSignals` instead of going out-of-band via
  `tea.Raw`, so Ghostty never paints tab progress. Fix needs edge-detection
  (emit a `tea.Raw` only on the aggregate-state transition, not every frame).
  `internal/tui/dashboard/app.go` `withTerminalSignals`, `progressState()`.

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
- [ ] **Tighten `waitForPodReady` poll 2s→~500ms-1s** and surface pod-phase detail
  to the connect stepper (it has full phase visibility today, reports nothing).
  `backend.go:516-531`.
- [ ] Defer/deprioritize `ensureReaper` and the launch-burst observer connects off
  the foreground connect path; drop the redundant connect-time Status Get +
  re-`ensureSSHKey` on the freshly-created path. `connect.go:93,144,184`.

## UX / communication

- [ ] **Cold start shows a blank, frozen, TUI-less terminal** during pod
  schedule + image pull (`sandbox claude`/`opencode`). Give it a TUI splash with
  per-phase detail + elapsed timer (the team did this for SYNC/RECONNECT but not
  the create/resume stage). `claude_remote.go:156-189`, `backend.go:516-531`.
- [ ] Connect/create splash has no elapsed timer (reconnect header does).
  `app.go:1130-1219`.
- [ ] Chat status line never surfaces file-sync state; a stalled sync is invisible
  while attached, and "stalled" offers no remedy hint. `statusline.go:372-476`,
  `model.go:2153-2161`.
- [ ] connectErr/actionErr persist in the detail pane with no dismiss. `model.go:2235-2249`.

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

- [ ] **Runner-as-metrics-observer for external-pane backends.** Make the runner
  subscribe to `opencode serve`'s event stream (and, for codex, the app-server
  thread notifications) and emit normalized usage/tool/turn events → SSE → the
  statusline gets ctx%/cost/recent-tools/live-status for opencode + codex like
  claude. This is the real fix for the "permanently `StatusIdle`, no metrics" gap,
  not accepting it as a PTY-model tradeoff. `session.go:427-529`, `external_pane.go`.
- [ ] OpenCode in-agent tool use is **neither audited nor gated** by the runner's
  Bash blocklist (Claude is). Security-relevant. `runner/src/server.ts:225-247`,
  `guards.ts`.
- [ ] CLI `opencode` has no `--model` flag and no initial-prompt arg; `cancel` and
  the suspend active-turn warning are inert for opencode. `claude_remote.go:23-71`,
  `commands.go:52-61`.
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

- [ ] Probes ARE implemented (`backend.go:698-721`) but `SECURITY.md:66`,
  `oss-launch/LAUNCH-CHECKLIST.md:42`, and `oss-launch/HARDENING-BACKLOG.md:44`
  (C9, with a now-false "Verified: no probes" note) still list them missing.
- [ ] `architecture.md:200-218` calls capability drops / `allowPrivilegeEscalation:false`
  future work — already landed (BR1, `backend.go:733-743`); and `:198` links a
  NetworkPolicy path that doesn't exist (real files are flat under `k8s/`).
- [ ] `runner-api.md`: Debug-logging section claims the runner emits structured
  JSON logs (it doesn't — only the CLI does); `/exec` "blocked but still executed"
  is wrong (refused pre-spawn, exit 126); 126/127 exit codes + 4 `rate_limit.updated`
  fields undocumented. `runner-api.md:130-245`.
- [ ] README markets `sandbox claude` as "start (or reuse)" — it always mints a new
  session. `README.md:32-33,108`.
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

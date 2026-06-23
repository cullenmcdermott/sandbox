# TODO

## ~~Warm "hide" sessions — rework attach/detach (design approved)~~ **DONE (2026-06-23)**

Implemented the full 14-task TDD plan
([`docs/superpowers/plans/2026-06-22-warm-hide-sessions.md`](docs/superpowers/plans/2026-06-22-warm-hide-sessions.md)).
Leaving a chat now *hides* it: every running-pod session stays warm (passive SSE
+ a retained `TranscriptModel` fed in the background + Mutagen sync), so show is
an O(1) instance swap that surfaces progress made while away. New code:
`internal/tui/dashboard/warm.go` (retained store, `ensureRetained`/`dropRetained`/
`warmCount`, `idleRemaining`/`roundDur`), `TranscriptModel.ingest`+`seedSize`+
`tailLines` (`transcript.go`), warm build/feed/drop in `model.go`
(`liveSSEReadyMsg`/`handleRunnerEvent`/`applyPodEvent`), reuse-on-attach +
keep-warm-on-detach in `app.go`. Liveness signals: unread badge (`Session.seenSeq`/
`Unread()`), detail-pane tail preview, polled sync status
(`internal/sync/status.go` `StatusSummary`→`SyncState`, `SyncProber` injected via
`RunOptions`), idle-soon "suspends in ~X" hint (`Idle` added to the dashboard
`RunnerClient`; reaper `defaultReaperIdleTimeout` threaded), and a `⚡N warm`
footer count. All gated green by `just check` (tests, vet, race-twice, e2e).
Verified by `internal/tui/dashboard/warm_test.go` + `internal/sync/status_test.go`.

Follow-up (optional): a live-TUI smoke against ≥2 running sessions (hide/show
instant resume, unread badge appears, hidden idle session still reaped → footer
drops). Plan Task 14 Step 3 — not runnable in the dev sandbox (no live cluster
auth).

## Manual Additions (needs triage)
* Improve the agent picker modal, shuld feel like picking a fighter in tekken or something with cool animations and a good "avatar"/"portrait" for each one. If you waant I can have chatgpt generate images that we can turn into ascii/ansi art?
* ~~Investigate "Syncing Files" taking a long time ... reconnecting should feel much faster~~ **MOSTLY FIXED (2026-06-23).** Root cause: `connect()` (`internal/cli/connect.go`) ran a **blocking 12s `mutagen FlushAll`** in `StageSync` on *every* connection — both foreground attach AND every background passive-stream open (`startLiveSSECmd`, now one per running/warm session). `mutagen sync flush` forces a full scan/reconcile, so on a large repo over slow wifi it consumed the whole budget, making every reattach feel like a fresh "Syncing Files…" even when nothing changed. Fix: `CreateAll` now returns whether the load-bearing **project** sync session was freshly created (first-ever sync) vs already existed (reconnect); `connect()` only blocks on the initial flush for a first sync (where it settles the upload + surfaces a broken transport, RV20/RV21) and on reconnect returns immediately, kicking a **detached** flush so mutagen re-establishes the transport on the new port-forward promptly. The per-session **sync indicator** (synced/syncing/stalled glyph) added in the warm-hide work now surfaces progress live on the dashboard. Tests: `internal/sync/sync_test.go` (`TestCreateAll` created=true, `TestCreateAllIdempotent` created=false). **Both follow-ups also done (2026-06-23):** (1) **Observer connect** — background passive streams now use a lightweight `connectObserver` (port-forward + runner health only; skips mutagen sync create/flush, reaper-ensure, opencode-readiness), injected as `RunOptions.ObserverConnector` and used by `startLiveSSECmd`/`reconnectLiveSSECmd`/approve-fallback (closes the long-standing RV8 "heavyweight per-session bg connector"). (2) **First-sync progress** — the connect stage callback now carries a detail string; the first-sync flush polls `internal/sync` `StagingPhase` (robust substring match) and the connecting stepper shows "Syncing files — uploading/scanning/applying" instead of a frozen label. Tests: `TestBackgroundStreamPrefersObserverConnector`/`…FallsBackToFullConnector`, `TestConnectingStepperDetail`, `TestParseStagingPhase`.
* ~~renaming doesnt work? I can hit shift+R but then can't type in the box or edit the name~~ **FIXED** — `handleKey` never routed keypresses while `m.renaming` was true (overlay rendered, but every key fell through to navigation). Added a `m.renaming` guard + `handleRenameKey` (enter commits, esc cancels, backspace deletes, printable runes append) in `internal/tui/dashboard/model.go`; tests in `triage_fixes_test.go` (`TestRenameKeyboardInput`, `TestRenameEscapeCancels`).
* usage limits are "unavailble" in the status line??? — **ROOT-CAUSED (2026-06-22): not a code bug. It's an SDK auth-mode limitation.** The runner *is* emitting `rate_limit.updated` with `{"available":false}` (confirmed: 3 rows in a live pod's `events.db`), and the status line correctly renders that as "unavailable" (`statusline.go:436`, gated on `m.rlSeen && m.rlAvailable`). The runner emits `available:false` because the experimental usage API returns `rate_limits_available:false` under the pod's headless `CLAUDE_CODE_OAUTH_TOKEN` (the `claude setup-token` long-lived OAuth token, `subscription_type:null`). Verified by running `q.usage_EXPERIMENTAL_…()` two ways: laptop **keychain** subscription → `subscription_type:max`, `rate_limits_available:true`, real data (5h 73%/7d 44%); same call with the **pod's CLAUDE_CODE_OAUTH_TOKEN** → `subscription_type:null`, `rate_limits_available:false`. Pod SDK = `0.3.181` (== package.json), so not a version skew. **Conclusion:** the experimental `/usage` API doesn't carry subscription rate limits over setup-token auth; nothing to fix in this repo until the upstream API (marked `DO_NOT_RELY_ON_THIS_API_YET`) supports OAuth tokens. *Possible cosmetic follow-up:* pass `subscription_type` through `rate_limit.updated` so the TUI can show "usage n/a (headless auth)" instead of the bug-sounding "unavailable", or hide the 5h/weekly rows entirely when `subscription_type==null`.
* no `/model` that lets me select from available models (by querying the anthropic api or something...? how to make this dynamic?) — partially DONE (see "Done" section: `/model` palette + `--model`). Dynamic list still open: wire the SDK's `supportedModels()` instead of hardcoded `opus`/`sonnet`/`haiku` aliases.
* ~~The chat pane floating window/modal should make the background more grayed out and should have a little border~~ **DONE (2026-06-22).** `app.go:modalView` now (1) wraps the transcript in a rounded-border `kit.Card` (purple `theme.Charple` border, `theme.Surface` fill), sizing the inner content to `mw-2 × mh-2` so the framed card stays exactly `mw×mh` and lines up with the drop shadow; (2) replaces the live dashboard background with `dimBackdrop()` — strips each line's ANSI colors and re-renders them as dim text on the flat page bg, so the dashboard reads as ghosted/out-of-focus context and live rows (e.g. the colored status line) no longer bleed through at full brightness past the modal edges.

## Resumable transcripts: make the pod cwd the real host path (Option B)

**Goal.** Be able to resume a k8s-started session locally with `claude --resume`
from the laptop. Claude keys its on-disk transcript dir by cwd
(`~/.claude/projects/<cwd with '/'→'-'>/<session-uuid>.jsonl`).

**The bug this fixes.** The runner ran the SDK with cwd
`/session/workspace/<host project path>`, so transcripts landed under
`~/.claude/projects/-session-workspace-Users-...`. The transcript Mutagen sync
mirrored that folder verbatim to the laptop, but local `claude --resume` (run
from `~/git/<repo>`) only scans `~/.claude/projects/-Users-...` — so it never
saw the k8s session. The files arrived; resume still couldn't find them. (The
SSE event stream is sufficient for the TUI, but `claude --resume` reads the
`.jsonl` files, not the event DB — so the transcript sync is NOT redundant for
this goal.)

**Fix (Option B).** Run the SDK with cwd equal to the real host project path
(e.g. `/Users/cullen/git/homelab`) so the on-pod transcript dir matches the
laptop's and a straight mirror just works. The workspace subtree is bind-mounted
from the session PVC at the real path (k8s `subPath`), so the project lives at
both `/session/workspace/<path>` (legacy `/session` mount) and `<path>` (new
bind mount) — same underlying PVC files.

Changed:
- `internal/k8s/backend.go` — new `runnerVolumeMounts(spec)`: adds a
  `{Name: session, MountPath: <projectPath>, SubPath: "workspace"+<projectPath>}`
  bind mount when ProjectPath is absolute.
- `runner/src/exec.ts` — `resolveWorkspaceDir` now returns the real projectPath
  (absolute + no-`..` guard), not `WORKSPACE_ROOT`-joined.
- `internal/cli/sync_support.go` — Mutagen `RemotePath` = projectPath (dropped
  the `/session/workspace` join + `remoteWorkspaceRoot` const).
- Doc/comment updates: `runner/src/types.ts` (WORKSPACE_ROOT now = PVC-internal
  location only), `internal/sync/sync.go`, `internal/tui/dashboard/statusline.go`
  (`podWorkspacePrefix` strip is now a legacy fallback).
- Test updated: `runner/test/medium-batch.test.ts` resolveWorkspaceDir contract.

**Verified on the live cluster (2026-06-23).** New runner image
(`registry.cullen.rocks/sandbox-claude-runner@sha256:63162802…`, amd64; pushed
direct to the zot ClusterIP to dodge a traefik `499` on the 153 MB layer) running
in pod `claude-sdk-df80e6-9067bbc1`: (a) `subPath` bind mount surfaces the
workspace at `/Users/cullen/git/sandbox`; (b) the SDK writes transcripts under
`-Users-cullen-git-sandbox` with `"cwd":"/Users/cullen/git/sandbox"` inside;
(c) Mutagen synced the `.jsonl` down to the laptop; (d) `claude --resume <id>`
returns `RESUME_OK` — **Agent-SDK transcript ↔ Claude Code CLI resume compat
confirmed**.

**Resume-picker bridge (implemented — supersedes the earlier "surface the id"
plan).** Resume-by-id worked, but the interactive `claude --resume` picker reads
`~/.claude/history.jsonl` (a per-machine prompt log the Agent SDK never writes),
so synced k8s sessions didn't appear in the list. Fix:
- Runner already emits `claudeSessionId` on `session.started`; the CLI now
  captures it (`dashboard.Session.ClaudeSessionID`) and persists it to the local
  index via `TitleStore.SaveClaudeSessionID` (`index.Entry.ClaudeSessionID`).
- On TUI exit, `afterTUI` → `syncResumeHistory` (`internal/cli/resume_history.go`)
  appends a `history.jsonl` entry for every index session whose transcript has
  actually synced down (`projects/*/<id>.jsonl` exists), deduped by `sessionId`.
  Guarded on transcript-presence so the picker never lists a session that would
  fail to resume; idempotent/self-healing; append-only single-line write so it
  can't clobber Claude Code's concurrent writes. Test: `resume_history_test.go`.

**Still to do / caveats:**
- **Migration.** Pre-existing sessions have transcripts under the old
  `-session-workspace-...` folder and were created with the old mount layout. On
  resume after this change their cwd moves to the real path → new transcripts go
  to the new folder; in-session resume-by-id continuity across the migration may
  break. Acceptable per OSS-prep "aggressive breaking changes OK"; call out in
  release notes.
- **Stale docs.** `docs/architecture.md` (lines ~91/111/153) and
  `docs/runner-api.md` (~34) still describe cwd/sync as `/session/workspace/...`.
  Update to the real-host-path model.
- **todos/tasks sync.** `projects` is the load-bearing one for resume;
  `~/.claude/todos` + `~/.claude/tasks` are keyed by session id and ancillary —
  keep syncing (cheap) but they're not required for conversation resume.

## Dashboard redesign — Triage Console (implemented)

Full spec → **`docs/dashboard-redesign.md`**. Chosen from a 4-way design
exploration of the FleetView dashboard (`internal/tui/dashboard`) prompted by a
screenshot review. **Done:** Phases 1–4 landed; P1–P13 each have an
implementation + passing test (`internal/tui/dashboard/triage_console_test.go`);
`renderDetailLines` is wired into `renderZoned`. Only the doc's "Open follow-ups"
(seed `LastActivity`, fleet-scale revisit) remain optional.

Replaces the right 3-box stack (NEEDS YOU / USAGE / CLUSTER) with a 1-line
cluster strip + a real **detail pane** for the selected session — wiring in the
already-written-but-dead `renderDetailLines` (`model.go:1507`). Fixes 13
reviewed problems (P1–P13); biggest are the opaque-background bug (terminal
bleeds through everything, P1) and the three indistinguishable `sandbox` rows
(P2). Live ctx%/cost/recent-tools are cheap: the dashboard **already** streams
events for all running sessions (`model.go:872`, `EventsPassive`), so only two
no-op cases in `ApplyRunnerEvent` (`session.go:232`, usage + tool.started) plus
a few `Session` fields are new.

Phases: (1) layout + opaque surfaces + CLUSTER backend bug, (2) row identity
(short id, model, ctx%), (3) live token/cost metrics, (4) `─ recent ─` last-3
tool calls — **real tool names** (not friendly verbs), reusing `toolArg`
(`transcript.go:798`). See the doc for per-phase `file:line` steps and the
P1–P13 coverage table.

## Done (this pass)

- **Model switching (`/model`).** `sandbox claude --model <id|alias>` sets the
  session default (threaded `Spec.Model` → `SANDBOX_MODEL` → runner `cfg.model`
  → SDK `options.model`); the in-session `/model` palette group (`/opus`,
  `/sonnet`, `/haiku`, `/model-default`) overrides it per turn via
  `TurnInput.Model`. See `runner/src/claude.ts` (`resolveModel`/`buildOptions`),
  `internal/tui/dashboard/commands.go` (`setModelCmd`).

- **Real usage/reset times in the status line.** Replaced the mocked 5h/weekly
  bars + wall-clock-projected reset times with real claude.ai plan data: the
  runner fetches the SDK's structured `/usage`
  (`usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET`) on the init
  message of each turn and emits a new `rate_limit.updated` event
  (`schema/events.json`); the status line renders the real utilization +
  reset countdowns, and hides the windows (never fabricates) when plan limits
  don't apply. See `runner/src/claude.ts` (`fetchAndEmitRateLimits`),
  `internal/tui/dashboard/statusline.go` (`fmtReset`).

- **Per-model weekly usage windows (`/usage` parity).** The `rate_limit.updated`
  event now also carries the optional per-model weekly Opus/Sonnet caps
  (`sevenDayOpus*` / `sevenDaySonnet*`, schema `floatPtr` so a nil pointer =
  "no per-model cap" stays distinct from a present 0%). The runner reads them
  from the SDK `/usage` `rate_limits.seven_day_opus|seven_day_sonnet`; the status
  line surfaces the one matching the attached model (percent-only to stay within
  the fixed 4-row block), and hides it for Haiku/unknown/unset models. See
  `runner/src/claude.ts` (`fetchAndEmitRateLimits`), `internal/session/event.go`
  (`RateLimitPayload`), `internal/tui/dashboard/statusline.go`
  (`activeModelWindow`).

- **Long dark line in the status line.** The transcript modal is now an opaque
  full-width block (`fitModal` in `transcript.go`) so short status-line rows no
  longer let the dark dashboard layer bleed through on the right; `renderInput`
  also sizes the prompt to the available width so it can't overflow the modal.

## Follow-ups / caveats

- The rate-limit fetch depends on an SDK API explicitly marked
  `DO_NOT_RELY_ON_THIS_API_YET`; it is wrapped fail-soft and unverified against a
  live subscription session (no live Claude auth in the dev sandbox). Confirm it
  returns data on a real `max`/`pro` session, then consider pinning the SDK
  version.
- Per-model windows now ride on the `rate_limit.updated` event in full, but the
  status strip only *renders* the one for the attached model (4-row budget). The
  SDK also exposes `seven_day_oauth_apps` and `extra_usage` (overage credits),
  which we drop on the runner side; wire those into the event + a dedicated full
  `/usage` view if we want complete parity.
- The black-line fix is verified by golden tests + static analysis; confirm
  visually in a live TUI attach.
- `/model` offers `opus`/`sonnet`/`haiku` aliases + a free-form gap. If we want
  an exact model list, wire the SDK's `supportedModels()` (a streaming-mode
  control request) instead of hardcoded aliases.

# UX/UI Polish Plan — phased & resumable

Focus: **user-facing UX/UI polish** for the `sandbox` CLI/TUI ahead of the OSS
launch, plus docs cleanup and real demo GIFs. Under-the-hood perf/infra is
explicitly out of scope (see "Deferred" at the bottom).

This plan is the source of truth for a multi-session effort. Each phase is
self-contained and can be implemented one at a time. Every phase carries a
**Restart prompt** — paste it into a fresh session to resume that phase from
any state. Keep the Status checklist current as phases land.

Branch: `feat/ux-polish`. Triage evidence: the verified review behind these
items is in `TODO.md` (file:line anchors) and `docs/review-2026-06-24.md`.

**Sequencing insight (2026-06-28):** phases split cleanly into *cluster-free code*
(Phase 1 ✓, Phase 2 elapsed-timer ✓, Phase 3, Phase 5 docs — all verifiable with
`go test ./internal/tui/dashboard/`) and *cluster-dependent work* (Phase 2's
cold-start pod-phase splash, Phase 4 observer, Phase 6 demos — all want a live
`just dev` kind cluster + the maintainer's creds). Efficient order: finish the
cluster-free code first, then do one focused live-cluster session that brings up
`just dev` once and knocks out the splash live-verify + observer + demos together.

---

## Status

- [x] **Phase 1** — Honesty & feedback bugs (permission errors, cluster-unreachable, error dismiss)
      — landed on `feat/ux-polish`: `approveResultMsg` now handled (`model.go`), chat
      `resolvePermission` returns `permResolveErrMsg` (`transcript.go`), `seedFailedMsg` +
      `m.seedErr` + retry/error state in `renderRowLines`, `esc`-dismiss in `handleKey`.
      7 regression tests in `phase1_ux_test.go`; full dashboard suite green.
- [x] **Phase 2** — Cold-start splash + elapsed timer
      — DONE + LIVE-VERIFIED (`feat/ux-polish`). Elapsed timer (prior) + cold-start
      pod-phase splash: `waitForPodReady` gained an `onPhase(detail)` callback +
      `podPhaseDetail` classifier (scheduling/pulling image/starting); `StartWithProgress`
      keeps the `Start` interface stable; the pod-ready wait moved into
      `sessionConnector.establish` (reports under StageResume) and the pre-TUI
      `backend.Start` was dropped from `claude_remote.go` + `dashboard_connector.go` so the
      connect splash owns the wait. `connectCmd`/`createCmd` aligned (StageCheck init, 300s
      budget). Tests: `backend_phase_test.go` (classifier + callback), tier4 StageResume
      detail. Live: `sandbox claude` shows `Starting pod — scheduling` + `(1s)` timer on a
      cold node instead of a frozen terminal.
- [x] **Phase 3** — Terminal-signal & sync visibility (OSC tab-progress, sync statusline, opencode wheel/click)
      — DONE (`feat/ux-polish`). Items 1–2 (OSC 9;4 tab-progress via `tea.Raw`; chat
      sync-status segment) landed prior. **Item 3 (opencode wheel-scroll + clickable spots)
      now done**: `View()` enables `tea.MouseModeCellMotion` on `ScreenExternal` so the host
      reports wheel/click to the app, where `handleMouse` re-encodes them as SGR mouse for
      opencode's PTY. Verified live (PTY capture) that opencode itself enables mouse tracking
      (DECSET 1000/1002/1003 + SGR 1006), so its NATIVE wheel-scroll + clicks take over once
      the host stops mapping the wheel to arrow keys — no manual scroll-key translation
      needed (simpler than the plan's hypothesis). Tests: `phase3_item3_test.go`
      (wheel→SGR forward + View MouseMode guard).
- [x] **Phase 4** — Runner metrics observer → opencode parity (title, live status, ctx%/cost, cancel/suspend)
      — DONE + LIVE-VERIFIED (`feat/ux-polish`). `runner/src/opencode-observer.ts` is an
      always-on passive subscriber to `opencode serve`'s event stream that frames each
      interactive turn as a synthetic turn — reusing a fresh `createOpencodeTurnMapper` per
      cycle (avoids forking the battle-tested mapper) — emitting
      turn.started/session.started(model)/usage/message/turn.completed and setting
      last_turn_id/model/status. **No schema change** (existing events carry every parity
      surface; `just gen` clean). `server.ts` interrupt route gained an opencode-abort
      fallback so `sandbox cancel` interrupts (was a 404 no-op). Go side: `ApplyRunnerEvent`
      is already backend-agnostic (list-row parity needs NO change); the external pane got a
      live-session handle + `DisplayTitle`/ctx%/cost/status in `statusRow` (`app.go`
      keep-passive-SSE on opencode attach + `external_pane.go`). Tests: 7 observer unit +
      3 Go; `tsc` + 118 runner tests + no gen drift. Live (events.db): a real interactive
      turn produced the full `turn-1` sequence, `last_turn_id`+`model` set, status busy→idle;
      `sandbox cancel` emitted `turn.interrupted`.
- [x] **Phase 5** — Docs & README (accuracy + humanize + launch structure)
      — landed on `feat/ux-polish`: 3 accuracy fixes (each re-verified against code) —
      "start (or reuse)" → "start a **new** session" (`README.md` ×3 + the `claude`
      help string + the misleading opencode comment in `claude_remote.go`); the caps
      "drops all" overstatement now names the re-added default set minus NET_RAW/MKNOD
      (`architecture.md`, from `backend.go:745-752`); the transcript sync path corrected
      to pod `/session/state/claude/{…}` (`CLAUDE_CONFIG_DIR`) → local `~/.claude/{…}`
      in two spots. Humanized the README opening + de-duped the "entry point" phrasing;
      added launch sections (Why-use-this, Try-it-locally/kind, Install path,
      Status/maturity note, Contributing, hero demo-GIF slot). Verified by an
      adversarial multi-agent review (doc-accuracy + voice clean).
- [~] **Phase 6** — Real asciinema demos via local kind, embedded in the README
      — **pipeline proven, recording BLOCKED on the host mutagen daemon.** The harness
      (PTY driver → asciicast v2 → `agg`) works and captured a clean Claude cold-start
      splash→chat (`phase2.cast` → 51KB GIF). But the shared mutagen daemon has ~651
      sync sessions accumulated (mixed with the maintainer's *real* cluster sessions, so
      not safe to mass-prune), and that contention makes every new session connect crawl
      through "Syncing files — scanning" (~75s), which is dead air no `--idle-time-limit`
      can rescue. Clean turn-bearing demos need a healthy daemon first
      (`mutagen sync terminate` the orphans whose pods are gone) — a maintainer call.
      Sync-free shots (bare-`sandbox` dashboard) are still recordable. Demos are
      outward-facing and want maintainer sign-off, so none are committed yet.

Update the box, add a one-line "landed: <commit/summary>" under each phase as it
completes, and move detail to `docs/archive/done-log-2026-06.md` per repo convention.

---

## Constraints (read before any phase)

- **Build/test env:** use the *real* Go env — `GOPATH=~/go`. Do **not** use
  `/tmp/gomodcache` (the CLAUDE.md-suggested path is a corrupted/partial cache
  here: missing `editions_defaults.binpb`, chroma embeds). Go commands need the
  **command-sandbox disabled** (module-cache `.lock` writes are blocked in-sandbox).
- **Gate:** `just check` is the full CI gate. `internal/runner` + `internal/models`
  bind httptest ports the sandbox blocks — run their tests with the sandbox off.
  The `internal/tui/dashboard` package tests run fine and are the main target for
  Phases 1–4; `go test ./internal/tui/dashboard/` is the fast inner loop.
- **Linters not on host:** `golangci-lint` / `eslint` aren't installed; CI enforces
  them. Run locally via `nix run nixpkgs#golangci-lint -- run` if needed.
- **Runner (TS) typecheck:** `cd runner && npm install --ignore-scripts && ./node_modules/.bin/tsc --noEmit`.
- **Event-model changes (Phase 4 only):** never hand-edit `*.gen.*`. Edit
  `schema/events.json`, run `just gen`, commit the regenerated
  `internal/session/eventtypes.gen.go` + `runner/src/events.gen.ts`.
- **Reuse patterns already in the tree** (cited per phase): the `tea.Raw`
  out-of-band escape emit (`model.go` toastMsg handler), the connect stepper +
  per-stage detail (`states.go`), the reconnect elapsed-timer idiom
  (`transcript.go` renderHeader), and `kit.ErrorBlock` detail rendering.
- **Determinism for goldens:** `withDeterministicRender` pins
  `SANDBOX_REDUCE_MOTION=1` + a fixed `nowFunc` + forced gradient
  (`golden_test.go`). Use the injectable `nowFunc` (`states.go`) for any
  time-based assertion.

---

## Phase 1 — Honesty & feedback bugs

**Goal:** No silent failures and no permanent dead-ends in the dashboard. Three
fixes, all contained to `internal/tui/dashboard`, low risk.

**Items (from TODO "Must-fix" + "UX/communication"):**

1. **Permission approve/deny errors are surfaced, not swallowed.**
   - Dashboard: `approveCmd` builds `approveResultMsg{id, err}` (`model.go:1209-1250`)
     but `Model.Update` has **no `case approveResultMsg`** — it falls through to the
     terminal `return m, nil`. Add the case next to `actionResultMsg` (`model.go:806`):
     on `err != nil` set `m.actionErr = fmt.Errorf("resolve permission: %w", err)`,
     clear on success. Reuses the existing `kit.ErrorBlock` render (`model.go:2278-2281`).
   - Chat: `resolvePermission` does `_ = client.ResolvePermission(...)` (`transcript.go:2000`).
     Capture the error, return a new `permResolveErrMsg{err}`, handle it in
     `TranscriptModel.Update` by appending a `blockError` (mirror the
     `interruptFailedMsg`/`turnErrMsg` handlers ~`transcript.go:689/696`). Keep the
     optimistic block, append the error block on failure.
2. **Actionable failure state when the cluster is unreachable.** `seedCmd`
   (`model.go:856-858`), `startWatchCmd` (`model.go:867-870`) and `reconcileListCmd`
   (`model.go:103-105`) all return `nil` on error, so `m.seeded` never flips and
   `renderRowLines` (`model.go:1959-1968`) shows skeleton bars forever. Introduce
   `seedFailedMsg{err}`, store `m.seedErr` (new field by `seeded` ~`model.go:246`),
   add a branch in `renderRowLines` *before* `case !m.seeded` for
   `m.seedErr != nil && len(m.sessions)==0` rendering a `kit.ErrorBlock` + `r retry`
   hint, wire `r` to re-issue seed+watch and clear `m.seedErr`; clear it in
   `applySeed` on success. The reconcile loop self-heals.
3. **Dismiss persistent `connectErr`/`actionErr`.** They render via `kit.ErrorBlock`
   (`model.go:2271-2281`) and only clear on the next attempt. Add a bare-`esc`
   dismiss in `handleKey` (`model.go:1547`, *after* the overlay/confirm/filter/rename
   guards so it doesn't steal esc from overlays) that nils both. Optionally clear on
   cursor nav.

**Verify:**
- `go test ./internal/tui/dashboard/` (sandbox off).
- New/extended tests: extend `actions_test.go` `fakeRunnerClient.ResolvePermission`
  to return a configurable error; assert `m.actionErr` is set after running the
  Cmd. Extend `transcript_test.go`'s resolvePermission test for the chat
  `blockError`. Extend `tier4_test.go` `TestSkeletonBeforeSeeded` (~:269) with a
  `seedFailedMsg` sibling asserting failure copy + `r` hint and NOT skeleton bars
  (use `hasSkeletonBar`). Add a dismiss test (set errs → feed `esc` → assert nil).

**Restart prompt:**
> Resume **Phase 1** of `docs/ux-polish-plan.md` (UX honesty/feedback bugs) on
> branch `feat/ux-polish`, repo `/Users/cullen/git/sandbox`. Three fixes in
> `internal/tui/dashboard`: (1) surface permission approve/deny errors — add the
> missing `case approveResultMsg` in `model.go` Update (set `m.actionErr`) and stop
> `_=`-dropping the error in `transcript.go` `resolvePermission` (return a
> `permResolveErrMsg`, append a `blockError`); (2) add an actionable
> cluster-unreachable state — `seedFailedMsg` + `m.seedErr` + an error/`r`-retry
> branch in `renderRowLines` so it stops showing skeleton bars forever; (3) bare-esc
> dismiss for `connectErr`/`actionErr` in `handleKey`. First re-read the current
> code at the cited lines (some may have shifted) and confirm each bug still exists.
> Use the real Go env (`GOPATH=~/go`, not `/tmp/gomodcache`), run
> `go test ./internal/tui/dashboard/` with the command-sandbox disabled, and add the
> tests listed under Phase 1 "Verify". Don't commit unless asked.

---

## Phase 2 — Cold-start splash + elapsed timer

**Goal:** `sandbox claude` / `opencode` never show a blank, frozen terminal during
pod schedule + image pull. The splash shows per-phase detail (scheduling →
pulling image → starting) and a live elapsed timer. This also makes Phase 6
recordings clean (no dead air).

**Items (from TODO "UX/communication" + "startup speed: surface pod-phase"):**

1. **Elapsed timer on the connect/create splash (do first — small, self-contained).**
   `connectingView` (`app.go:1147-1236`) renders a stepper but no elapsed time. Add
   `connectStartedAt time.Time` to `App` (~`app.go:149`), set it where a connect/create
   begins (attachMsg handler ~`app.go:392`, create/connect Cmds ~`app.go:1020/1062`),
   clear on ready/failed (~`app.go:413-505`), and render it on the title line reusing
   the reconnect idiom (`transcript.go:1313-1317`:
   `if el := nowFunc().Sub(a.connectStartedAt); el >= time.Second { title += fmt.Sprintf(" (%s)", roundDur(el)) }`).
   `connectTickCmd` already re-renders at animFPS, so the clock ticks for free.
2. **Cold-start splash with pod-phase detail (larger).** Root cause: `backend.Start`
   → `waitForPodReady` (`backend.go:516-541`) is a synchronous blocking call that runs
   **before** any TUI: `runStartSession` (`claude_remote.go:166`) for the `claude`/
   `opencode` commands, and `dashboard_connector.go:131` for the dashboard `n` path.
   `waitForPodReady` already classifies pod phase + container-waiting reasons
   (`backend.go:558-579`) but discards them. Thread an `onPhase func(detail string)`
   into the wait that maps state → `"scheduling"`/`"pulling image"`/`"starting"`. In
   `dashboard_connector.go` run `backend.Start` *under* `onStage(StageResume, detail)`
   (replace lines 131-135). For `runStartSession` drop the pre-TUI `backend.Start`
   (`claude_remote.go:166`) and let `RunAttached`'s connect path own the wait so the
   `connectingView` splash is already on screen and `StageResume`'s detail slot
   (`states.go:125-127`) renders the phase — exactly like `StageSync`'s "uploading".

**Verify:**
- `go test ./internal/tui/dashboard/` + a `backend`-level test feeding fake pods in
  Pending/ContainerCreating/Ready phases asserting the `onPhase` callback strings
  (mirror the existing `podStartupError` fatal-reason coverage).
- Extend `tier4_test.go` `TestStepperDetail` (~:89) to assert a `StageResume` detail
  ("pulling image") renders. Extend `connecting_preview_test.go` /
  `TestConnectingViewContent` to set `connectStartedAt = nowFunc().Add(-5*time.Second)`
  and assert an elapsed token (mirror `reconnect_giveup_test.go:53` asserting "1m").
- Manual: `just dev claude "hello"` from a cold node — confirm the splash animates
  with phase + timer instead of a frozen terminal.

**Restart prompt:**
> Resume **Phase 2** of `docs/ux-polish-plan.md` (cold-start splash + elapsed timer)
> on branch `feat/ux-polish`. Two parts: (1) add a live elapsed timer to the
> connect/create splash `connectingView` in `internal/tui/dashboard/app.go` — store
> `connectStartedAt` on `App`, reuse the reconnect idiom from `transcript.go`
> renderHeader (`nowFunc().Sub(...)` gated ≥1s via `roundDur`); (2) stop
> `sandbox claude`/`opencode` from showing a blank terminal during pod schedule +
> image pull — `backend.Start`→`waitForPodReady` (`internal/k8s/backend.go`) blocks
> before any TUI; thread an `onPhase(detail)` callback out of the wait
> (it already inspects pod phase + container-waiting reasons), run the wait under the
> `StageResume` stepper in `internal/cli/dashboard_connector.go`, and drop the
> pre-TUI `backend.Start` in `internal/cli/claude_remote.go` so the connect splash
> owns the wait. Re-read the cited lines first. Verify with the Phase 2 tests and a
> manual `just dev claude` cold start. Real Go env, sandbox off for Go tests.

---

## Phase 3 — Terminal-signal & sync visibility

**Goal:** Out-of-band terminal signals actually reach the terminal, and a stalled
file sync is visible while attached. Three independent fixes.

**Items (from TODO "Must-fix" OSC + "UX/communication" sync + "needs-triage" wheel):**

1. **OSC 9;4 tab-progress out-of-band via `tea.Raw`.** Edge-detection landed
   (`a.progressActive`, `app.go:832-839`) but `withTerminalSignals` still *prepends*
   `terminal.OSCProgress(p)` into `v.Content` (`app.go:855`) — which the v2 cell
   renderer drops (same reason the desktop notification was moved to `tea.Raw`;
   canonical example: the toastMsg handler at `model.go:792-795`). Stop writing OSC
   into `v.Content`; emit `tea.Raw(terminal.OSCProgress(p))` on the aggregate-state
   transition from `App.Update` right after the single dashboard delegation
   (~`app.go:574-585`), comparing against a stored `a.lastProgress`. Keep the Kitty
   image prepend (it legitimately rides `View`). `progressState` already returns
   `ProgressNone` off-Ghostty / under ReduceMotion.
2. **File-sync state in the chat status line.** `renderStatusLine`
   (`statusline.go:378-451`) shows model/cwd/branch/ctx/cost/mode but never sync, so
   a stalled mutagen sync is invisible while attached. The data already exists: the
   dashboard polls warm sessions (`syncPollTickMsg`→`probeSyncCmd`, `model.go:693-704`)
   and stores `Session.SyncStatus` (rendered only in the detail pane,
   `model.go:2189-2197`). Add a `syncStatus string` field to `TranscriptModel`, render
   a trailing row-1 segment using the detail-pane glyphs (✓ synced / ⟳ syncing /
   ⚠ stalled), coral for stalled. Feed it from `App` after the dashboard delegation:
   when `screen==ScreenTranscript`, set
   `a.transcript.syncStatus = a.dashboard.sessionByID(a.transcript.ref.ID).SyncStatus`.
   No new probe.
3. **opencode wheel-scroll hijack + clickable spots.** `app.go` `View()` clears
   `MouseMode` on `ScreenExternal` (~`app.go:748-751`) so opencode owns the terminal,
   but with host mouse reporting off Ghostty maps wheel→arrow keys, which fall through
   to opencode as Up/Down (prompt history). Enable `tea.MouseModeCellMotion` on
   `ScreenExternal` too, then translate `MouseWheelMsg` into opencode's scroll key.
   **First step:** confirm what key opencode's TUI uses to scroll its transcript
   (PageUp/PageDown most likely) before wiring — verify against a live `sandbox opencode`.
   The clickable-spots item is the same root cause (mouse passthrough); enabling
   cell-motion + forwarding click events addresses both.

**Verify:**
- OSC: rewrite `osc_signals_test.go` `TestAppViewPrependsProgressOnGhostty` /
  `...ClearsProgressOnce` to drive `App.Update` with a busy→idle aggregate transition
  and assert via the `rawString` helper (~:126) that the `tea.Raw` payload equals
  `OSCProgress(ProgressBusy)`/`OSCProgress(ProgressNone)`, and that a second idle
  Update emits no raw (one-shot edge). Keep `TestProgressStateMapping`.
- Sync: extend `statusline_test.go` — `syncStatus="stalled"` shows the marker; empty
  leaves row1 byte-identical (avoid golden churn). App-level test that a
  `syncStatusMsg` for the attached id propagates into `a.transcript.syncStatus`.
- Wheel: manual against live opencode + a unit test that a `MouseWheelMsg` on
  `ScreenExternal` forwards the scroll key (not arrow/history).

**Restart prompt:**
> Resume **Phase 3** of `docs/ux-polish-plan.md` (terminal-signal & sync visibility)
> on branch `feat/ux-polish`. Three independent fixes in `internal/tui/dashboard`:
> (1) emit OSC 9;4 tab-progress via `tea.Raw` from `app.go` `App.Update` on the
> busy→idle aggregate transition instead of prepending it into `v.Content` in
> `withTerminalSignals` (follow the desktop-notification `tea.Raw` pattern at
> `model.go` toastMsg handler) — the cell renderer drops in-content escapes;
> (2) add a file-sync segment (✓/⟳/⚠) to the attached chat `renderStatusLine`
> (`statusline.go`) fed from the dashboard's existing `Session.SyncStatus` poll via a
> new `TranscriptModel.syncStatus` field set in `App` after the dashboard delegation;
> (3) fix the opencode wheel-scroll-hijacks-history bug by enabling
> `tea.MouseModeCellMotion` on `ScreenExternal` and translating `MouseWheelMsg` to
> opencode's scroll key — FIRST verify opencode's actual scroll key against a live
> pane. Re-read cited lines, verify with the Phase 3 tests. Real Go env, sandbox off.

---

## Phase 4 — Runner metrics observer → opencode parity

**Goal:** opencode (and later codex) reach UX parity with claude on the metrics we
surface: a live agent-generated **title**, correct live **status** (not permanent
"idle" while working), and **ctx%/cost/recent-tools** in the statusline — plus
working `cancel`/`suspend`-active-turn warnings. The enabler is the
**runner-as-metrics-observer**: the runner subscribes to `opencode serve`'s event
stream and emits normalized usage/tool/turn events over the existing SSE channel,
so the same statusline path that serves claude serves opencode.

This is the one phase with sizable under-the-hood (runner-side) work; the maintainer
opted in because it unblocks several user-visible parity wins. See the "Parity bar"
in `docs/codex-integration-plan.md` and TODO "Agent UX parity".

**Steps:**

1. **Discovery (do first, ~spike):** confirm `opencode serve`'s event-stream API
   shape (endpoint + event types for message/tool/usage/session-title). The runner
   already speaks to `opencode serve`'s API in `runner/src/opencode-turn.ts` — reuse
   that base URL/auth. Record findings in `docs/opencode-turn-adapter-notes.md`.
2. **Observer (runner, TS):** add a passive subscriber (`runner/src/`) that connects
   to the opencode event stream for the session, maps events → the normalized model
   (`runner/src/types.ts` / `events.gen.ts`), and appends to the event log so SSE
   replay/broadcast (`events.ts`) carries them. If a new event field/type is needed,
   edit `schema/events.json` then `just gen` (never hand-edit `*.gen.*`).
3. **Status/title plumbing:** drive `Session` status off observed turn start/stop so
   it leaves `StatusIdle` while working; set the agent-generated title when opencode
   emits one (today `external_pane.go:401-422` shows only `p.sess.Title`).
4. **Statusline rendering:** render ctx%/cost/recent-tools for the external pane the
   same way the claude statusline does (`statusline.go`), reusing the Phase 3 segment
   layout. Keep per-agent in-pane rendering differences; only the *surfaced metrics*
   must match.
5. **cancel/suspend parity:** the inert `cancel` + suspend active-turn warning for
   opencode (`commands.go:120-124`, `cancel.go`) rely on `st.LastTurnID`, which
   in-pane opencode turns don't populate — the observer can populate an
   active-turn marker so these stop being silent no-ops.

**Verify:**
- `cd runner && ./node_modules/.bin/tsc --noEmit`; runner unit tests.
- `just gen` drift clean if `schema/events.json` changed; `just check`.
- k8sit conformance: extend the table-driven suite so opencode shows live
  status/title/metrics (and make `internal/k8sit/cli_smoke_test.go` table-driven over
  backends — TODO "Per-backend CLI smoke").
- Manual against live kind: `just dev opencode`, run a turn, confirm the statusline
  shows ctx%/status and the title updates; confirm `sandbox cancel` interrupts.

**Restart prompt:**
> Resume **Phase 4** of `docs/ux-polish-plan.md` (runner metrics observer → opencode
> parity) on branch `feat/ux-polish`. Goal: opencode gets a live title, correct live
> status (not permanent "idle" while working), and ctx%/cost/recent-tools in the
> statusline, plus working cancel/suspend warnings — by making the runner subscribe
> to `opencode serve`'s event stream and emit normalized events over the existing
> SSE channel. Steps: (1) spike opencode serve's event API shape (reuse the base
> URL/auth already in `runner/src/opencode-turn.ts`); (2) add a passive observer in
> `runner/src/` that maps opencode events → the normalized model and appends to the
> event log; if a new event field is needed, edit `schema/events.json` then
> `just gen` (never hand-edit `*.gen.*`); (3) drive `Session` status/title off the
> observed stream (today `external_pane.go` shows only the static title); (4) render
> the metrics in `statusline.go` for the external pane; (5) populate an active-turn
> marker so `sandbox cancel`/suspend warnings stop being inert for opencode
> (`commands.go`, `cancel.go`). Verify: runner `tsc --noEmit`, `just gen` drift +
> `just check`, and a live `just dev opencode` turn showing live status/title/metrics.
> This phase touches the event model — read the "Event model" section of
> `docs/architecture.md` and CLAUDE.md first.

---

## Phase 5 — Docs & README (accuracy + humanize + structure)

**Goal:** The README reads like a human wrote it, every claim is true, and a visitor
can understand *why* to use this and *how* to actually run it.

**Accuracy fixes (verified against code):**

1. **"start (or reuse)" is wrong — it always mints a new session.** `provisionSession`
   → `newSessionID` always creates a fresh id/pod/PVC; reuse is only via
   `sandbox attach`. Fix `README.md:32` (Quickstart comment), `README.md:108` & `:109`
   (Commands table), and the prose at `README.md:36-38`. Also fix the misleading
   command help string in `internal/cli/claude_remote.go:32` ("Start or attach…").
   → "Start a **new** … session and open the TUI."
2. **architecture.md overstates the capability lockdown.** `architecture.md:206-207`
   says the container "drops all capabilities" but omits the default set re-added at
   `backend.go:745-752` (CHOWN, DAC_OVERRIDE, FOWNER, …, NET_BIND_SERVICE, etc.).
   Match the code comment's honesty: "drops ALL then re-adds the default runtime set
   sshd/the agent need (minus NET_RAW/MKNOD), `allowPrivilegeEscalation:false`."
3. **architecture.md transcript path is wrong.** `architecture.md:163` lists the pod
   transcript source as `~/.claude/{projects,todos,tasks}`; it's actually
   `/session/state/claude/...` (`CLAUDE_CONFIG_DIR`, `backend.go:822`; no `~/.claude`
   symlink). The doc's own State table (`:114`) already uses the right path — fix the
   inconsistency.

**Humanize (replace LLM-tells; concrete rewrites in the audit):**

- README opening sentence is a 5-subsystem noun-pile lifted from CLAUDE.md → lead with
  what the user gets ("Run Claude/OpenCode coding agents on a remote k8s cluster
  instead of your laptop. Each session is a pod that keeps its state on a PVC, so you
  can detach and pick it back up. …").
- The Quickstart prose states "the main entry point" twice in six lines → keep one.
- architecture.md's default rhythm (stacked em-dash + parenthetical + semicolon) →
  vary sentence length, cut abstract filler ("the runner adapter is additive").
- Note: there is no dedicated "humanizer" skill installed; apply the audit's rewrites
  by hand and read each edit aloud for cadence. Don't trade one template for another.

**Launch structure (README gaps):**

- **Why use this** — 2–3 sentences above the fold (isolation, persistence across
  detach, many agents in parallel, keep your laptop free).
- **Install path** — there's only `go build`; add `go install ./cmd/sandbox@latest`
  (or release-binary note) + "put `./sandbox` on PATH".
- **Local trial (kind)** — link `dev/local/README.md` / `just dev` so evaluators have
  a path that doesn't need a real cluster.
- **Maturity note** — surface the `architecture.md` "Status" caveat (Mutagen sync +
  runner image build not yet validated on a live cluster) up front.
- **Contributing/support** pointer + link to architecture.md's rationale.
- **Demo GIF slots** — add placeholders near the top for the Phase 6 GIFs.

**Verify:** re-grep the code for each claim before/after; `just check` (markdown
isn't gated but keep links valid). Have the maintainer eyeball the README voice.

**Restart prompt:**
> Resume **Phase 5** of `docs/ux-polish-plan.md` (docs & README) on branch
> `feat/ux-polish`. Three accuracy fixes: (1) "start (or reuse)" → "start a new
> session" in `README.md:32,36-38,108,109` and the help string in
> `internal/cli/claude_remote.go:32` (the CLI never reuses — `newSessionID` always
> mints fresh; reuse is only `sandbox attach`); (2) `docs/architecture.md:206-207`
> drops-all-caps overstatement — note the default set re-added at
> `internal/k8s/backend.go:745-752`; (3) `docs/architecture.md:163` transcript path
> is `/session/state/claude/...` not `~/.claude/...` (`backend.go:822`). Then
> humanize the README (the opening is a noun-pile from CLAUDE.md; "main entry point"
> is said twice; architecture.md leans on em-dash/parenthetical/semicolon) and add
> the missing launch sections: a "why use this", an install path
> (`go install ./cmd/sandbox@latest` + PATH), a local-kind trial link to
> `dev/local/README.md`/`just dev`, a maturity note (from architecture.md's Status
> admonition), a contributing pointer, and demo-GIF placeholders near the top. Verify
> each claim against code before editing. There is no installed "humanizer" skill —
> apply rewrites by hand.

---

## Phase 6 — Real asciinema demos via local kind

**Goal:** Embed real (not mocked) demo GIFs in the README, recorded against the local
kind dev cluster with asciinema → agg. Depends on Phase 2 (clean cold-start, no dead
air), Phase 4 (opencode live status in-frame), and Phase 5 (README slots).

**Hard blockers that need the maintainer (cannot be automated):**
- Docker/Colima running.
- A real **Claude Code OAuth token** (1Password `op://k8s-secrets/anthropic-credentials/api-key`
  or `$CLAUDE_CODE_OAUTH_TOKEN`) for any shot that runs a real Claude turn (B/C).
- A real **OpenCode API key** for the opencode shot.
- Shots A and D (dashboard / session list) need **no** real auth.

**Bring-up (justfile-grounded):**
```bash
just doctor            # toolchain + Docker + Hall check
just dev               # kind up + controller + creds + images + claude TUI (dashboard)
just dev opencode      # same, opencode backend
# granular: just kind-up; just dev-image; just dev-reset (clean between takes); just dev-nuke (teardown)
```
`just dev claude "prompt"` creates a session and drops straight into chat;
`just dev` (no prompt) opens the dashboard.

**Record + convert (Nix; not pinned in Flox — use `nix run`):**
```bash
nix run nixpkgs#asciinema -- rec demo.cast --cols 120 --rows 30 --idle-time-limit 2
#   …drive the keystrokes, then exit…
nix run nixpkgs#asciinema-agg -- demo.cast demo.gif --theme asciinema --idle-time-limit 2
```

**Shot list:**
- **A — Dashboard / new session** (no auth): `just dev` → `n` → pick backend → watch
  pod go Ready → esc. Shows the command center.
- **B — Cold-start Claude turn** (needs OAuth): `just dev claude "what changed in this repo?"`
  → splash (Phase 2!) → streaming turn + tool cards → `Ctrl+]`. The hero GIF.
- **C — Permission prompt** (needs key): a prompt that triggers a tool → approve → tool
  card renders. opencode triggers tool use more readily.
- **D — Session list / attach** (no auth): `sandbox` → attach → switch sessions. Needs a
  couple of pre-staged sessions.

**Gotchas:**
- Image pull = dead air → pre-run `just dev-image`; `--idle-time-limit 2` collapses it;
  Phase 2 splash makes the wait look intentional.
- **Secret hygiene:** asciinema records everything. Resolve creds via `op`/env *before*
  recording (never echo a token); review the plaintext `.cast` before converting; do
  **not** commit `.cast` files. Commit only the final `.gif` (e.g. under `docs/demos/`).
- `--cols 120 --rows 30` renders well at README scale; `agg --speed 1.5` if pacing drags.
- `just dev-reset` between takes for a clean session list.

**Verify:** GIFs play, no secrets in frame, embedded at a readable size in the README,
file sizes reasonable (consider `--speed`/trimming). Maintainer signs off on content.

**Restart prompt:**
> Resume **Phase 6** of `docs/ux-polish-plan.md` (real asciinema demos via local kind)
> on branch `feat/ux-polish`. Record real demo GIFs of the `sandbox` TUI against the
> local kind cluster and embed them in the README. Bring up with `just doctor` then
> `just dev` / `just dev opencode` (granular: `just kind-up`, `just dev-image`,
> `just dev-reset`, `just dev-nuke`). Record via
> `nix run nixpkgs#asciinema -- rec demo.cast --cols 120 --rows 30 --idle-time-limit 2`
> and convert via `nix run nixpkgs#asciinema-agg -- demo.cast demo.gif --theme
> asciinema --idle-time-limit 2`. Shots: (A) dashboard + new session [no auth],
> (B) cold-start `just dev claude "…"` streaming turn [needs the maintainer's Claude
> OAuth token], (C) permission approve [needs a key], (D) session list/attach [no
> auth]. CRITICAL: this needs the maintainer to supply Docker/Colima running + a real
> Claude OAuth token (1Password or `$CLAUDE_CODE_OAUTH_TOKEN`) — ASK before assuming
> creds. Secret hygiene: resolve creds via `op`/env before recording, review the
> plaintext `.cast` before converting, never commit `.cast`, commit only the final
> `.gif` under `docs/demos/`. Pre-run `just dev-image` to avoid dead air.

---

## Deferred — explicitly NOT in these phases

**Out of scope (under-the-hood / infra / perf — per the UX-first steer):**
- Startup-speed infra: shrink/split per-backend images + Spegel mirror; parallelize
  Secret/PVC/sync/port-forward; tighten `waitForPodReady` poll; ungate the 12s
  first-sync flush; defer reaper/observer connects. (The *felt* half — the splash — is
  Phase 2.)
- Perf micro-opts: mutagen `sync list` poll gating; warm-preview re-layout guard;
  `visibleSessions` memoization; `bodyView`/`fitModal` width scans; SSE
  `JSON.stringify`-per-client; O(N²) streaming-markdown boundary scans.
- opencode in-agent Bash blocklist/audit (security plumbing — worth doing, but not UX).
- KRO custom resource; expose the CLI as a Go library; flox/nix runner support +
  in-cluster nix cache; `:latest`/traefik manifest-cache pinning; codex credential
  manager write-side + transport spike.
- `~/.claude/todos`+`tasks` sync; resumable-transcripts migration release-note.

**In scope by theme but deprioritized (revisit after Phase 1–6):**
- **T10 working-directory picker** (`docs/superpowers/plans/2026-06-22-t10-working-dir-picker.md`)
  — real user affordance but large (path completion + overlay + Creator threading).
- **Tekken-style animated agent picker** with per-agent portraits — delight, low impact.
- **Codex dashboard auth-strip** (read-side `internal/cred` is done; the strip is
  CLI-only today) and **`--check` live pings** — a user-visible status surface.
- **opencode pane as a framed modal** over the dash (design decision needed).

**Already done — verify-only (don't re-implement):** opaque background everywhere;
`esc` forwarded to opencode (not detach); bare `sandbox` drops into an empty dashboard
(no auto-launch); detail-pane "needs you" key hints correct. Confirm live during Phase 6.

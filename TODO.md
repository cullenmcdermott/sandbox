# TODO — backlog

> **How to use this file (agents):** sections are numbered workstreams, ordered
> roughly bugs → strategy → perf → platform. Every item carries `file:line`
> pointers and a fix direction — enough to plan without re-discovery. Pick a
> section (or one bolded cluster), plan the cluster together where the intro
> says so, and when a batch lands: check it off, summarize in one line, move the
> detail to `docs/archive/done-log-2026-07.md` (convention). Provenance docs:
> [`docs/review-2026-06-24.md`](docs/review-2026-06-24.md) (deep review behind
> older items), the 2026-07-01 whole-system review (§1d/§8 intros), and the
> 2026-07-04 multi-agent TUI audit (§1a–1c, §2 — every bug adversarially
> re-verified against source). Done-work history:
> [`docs/archive/done-log-2026-06.md`](docs/archive/done-log-2026-06.md),
> [`done-log-2026-07.md`](docs/archive/done-log-2026-07.md).
>
> **Opus-ready map (2026-07-04 triage; refreshed later the same day after a
> Fable review + pointer re-verification pass — every file:line below was
> spot-checked against the working tree):** §1a–§1c, §1e items 1–5, §2a–§2c,
> §4, §7a (Fable-reviewed — follow its ordering decisions), §10 (except the
> ops/traefik item) and most of §5 carry pointers + fix direction — pick a
> cluster and go; §1a's replay-class item and §1e items 1–2 have full fix
> plans. ADRs/design docs Opus can DRAFT now (implementation gated on
> maintainer sign-off): §7b runner package-manager strategy, §1e item 6
> server-side loop, §10 KRO, §9 worktrees (the maintainer spec in the item is
> enough to design from). Still gated on a maintainer decision: §8
> (deliberate design calls), the §2d yolo-default + first-account items
> (Fable recommendations recorded inline). Needs the
> real cluster or live services, not opus-offline: §5 Spegel deploy, §6 codex
> spike, §7 verify sweeps, parts of §1d.

## 0) Inbox — human notes, needs triage

Raw maintainer notes. Triage = either promote into a numbered section with
pointers, or answer inline and archive. (Resolved investigations moved to the
done log.)

*(empty — 2026-07-04 triage promoted everything into §2d, §5, §7, §7b, §9,
§10 or answered it in the done log's "Inbox investigations" section.)*

## 1) Correctness bugs

### 1a. TUI SSE / state-machine cluster (2026-07-04 audit, verified)

**Plan this cluster together** around a single "stream registration" invariant
(in-flight-connect tracking + per-stream generation tokens) — they interact,
and piecemeal fixes will fight each other. All in `internal/tui/dashboard/`.

- [ ] **Replay-treated-as-live class (HIGH — root cause of the recurring
  "relaunch replay" bugs; diagnosed 2026-07-04).** The replay/live boundary
  (Workstream C) is implemented ONLY in the foreground transcript
  (`transcript.go:2160` consumes `EventStreamLive`; `replaying` flag +
  `attachSeq` watermark + seq dedup + idempotent cache). The dashboard's
  background apply path has NO notion of replay: every event in the
  `after=<seq>` catch-up is applied as if it just happened. Misfiring
  side-effect channels, per replayed event:
  1. **Notification storm / status flapping.** `notifyIfBackgroundAttention`
     runs on every `RunnerEventMsg` (`model.go:940`) and `PodEventMsg`
     (`model.go:788`). A replayed `permission.requested` (resolved later in the
     same history) flips the session to Waiting mid-catch-up → edge-triggered
     toast + OS notification for a long-dead permission on every relaunch, and
     rows visibly flap busy→waiting→needs-input while history streams in.
  2. **`statusChangedAt = time.Now()` on every replayed transition**
     (`session.go:710`) — relative times ("just now") and attention-first
     ordering are corrupted after every relaunch. Events carry `Time` (RFC3339,
     `internal/session/event.go:27`); it is never used.
  3. **No seq dedup in the apply path** — `model.go:1357-1361` advances the
     cursor but applies events at/below it anyway (same hole the
     duplicate-stream item below exploits).
  The resume cursor itself is leaky, which upgrades a small catch-up into a
  full-history replay:
  4. **No snapshot flush on quit, and non-transition events coalesce** to
     `snapshotSaveInterval` (3s, `model.go:1273`); usage/tool events return
     `changed=false` so only status transitions force-save. Persisted `LastSeq`
     is stale at every exit → EVERY relaunch replays a tail of history as
     "live", even when hydration works. Quit paths: `model.go:1862`,
     `app.go:415` — neither flushes.
  5. **Hydration holes** (tracked as separate items below, same class):
     watch-beats-seed skips snapshot hydration → `lastSeq=0` → full-history
     replay; `seenSeq` never restored → phantom unread badges; StreamEnded
     clears PendingPermission* and strands waiting sessions.
  **Fix plan (one pass; the transcript is the reference implementation):**
  a. Add per-session `catchingUp bool` to the dashboard read-model: set true
     when a background stream is installed (`liveSSEReadyMsg`), flip false when
     `RunnerEventMsg` carries `EventStreamLive` — the client already surfaces
     the runner's `: replay-complete` comment
     (`internal/runner/client.go:348-350`); the dashboard currently discards it
     (falls into `ApplyRunnerEvent`'s default no-op). Belt-and-braces: also
     capture lastSeq-at-connect as a watermark like the transcript's
     `attachSeq`, in case the marker is lost.
  b. While `catchingUp`: apply state mutations normally (status, usage,
     pending permission must land) but suppress side effects — skip
     `notifyIfBackgroundAttention` for that session and don't animate/flap.
     On the flip to live, run the notifier once: a session STILL in attention
     after catch-up gets exactly one honest toast.
  c. Set `statusChangedAt` from `ev.Time` instead of `time.Now()` always (not
     just during replay).
  d. Seq dedup at the top of the apply path: drop events with
     `Seq != 0 && Seq <= lastSeq` (also closes half the duplicate-stream item).
  e. Force-flush snapshots for all sessions on quit; also force-save when
     `lastSeq` advanced since the last write even if `changed=false` (bounded
     by the existing interval).
  f. Fold in the hydration items below (watch-beats-seed insert-path
     hydration, seenSeq carry-forward, StreamEnded permission preservation) —
     one invariant: *a relaunch restores the read-model to its pre-quit state,
     then applies only genuinely-new events, silently for history.*
  **Acceptance:** relaunch the dashboard with one busy and one
  permission-waiting session → no toast/OS notification for pre-existing
  state, no status flapping, relative times survive the relaunch, unread
  badges only for genuinely-unseen events; attach to the claude session →
  transcript identical to pre-quit, no duplicate blocks, pending permission
  still approvable.

- [ ] **Duplicate background SSE connects per session (HIGH — two independent
  verifiers confirmed; fires on the NORMAL startup path).** Launch guards check
  `liveSSECancels[id]` but that map is only populated on `liveSSEReadyMsg` —
  connects take seconds (connectSem-throttled), so seed + watch routinely both
  launch. Second ready overwrites the first's cancel WITHOUT cancelling: leaked
  uncancellable stream, double-applied events (`ApplyRunnerEvent` has no seq
  dedup — `model.go:1356/1359` only advances the cursor), duplicate RecentTools
  + re-fired notifications, and the orphan's eventual StreamEnded tears down the
  healthy stream via unconditional `cancelLiveSSE` (`model.go:1306`). Fix: track
  in-flight connects in every guard; on ready with an existing entry, cancel the
  incoming stream; tag msgs with a per-stream generation token. `model.go:888`
  (guards at `:1535`, `:1618`, `:910`).
- [ ] **StreamEnded permanently strands 'waiting' sessions (MED).** On a
  transient drop while a permission is pending, `model.go:1341-1343` clears
  PendingPermission* but keeps StatusWaiting; reconnect replays `after=lastSeq`
  and the permission.requested is below it — approve/deny silently dead, broken
  state force-snapshotted (`:1346`). Same class: the default branch (`:1330`)
  resets NeedsInput→idle, permanently losing attention state. Fix: preserve
  PendingPermission*/DashStatus when the pod is Running and a reconnect is
  scheduled; or re-fetch authoritative state on reconnect.
- [ ] **Watch-beats-seed race skips snapshot hydration (MED).** `applyPodEvent`'s
  insert path (`model.go:1625-1634`) never consults snapStore/titleStore →
  lastSeq=0 → background SSE replays entire history (launch-time notification
  flashes, usage counting from zero); later `applySeed` takes the carry-forward
  branch so hydration is permanently skipped that launch.
  `internal/k8s/watch.go:82-84` documents "seed before watch" — the dashboard
  violates it. Fix: hydrate in the insert path, or defer background SSE until
  first seed applied.
- [ ] **seenSeq never restored → phantom "N new" badges (MED).** Not in
  SessionSnapshot; `applySeed` carry-forward copies lastSeq but drops
  prev.seenSeq (`model.go:1504-1506`) — every relaunch shows
  lifetime-event-count unread badges; `r` re-seed resurrects them mid-run. Fix:
  carry seenSeq forward + init seenSeq=lastSeq on snapshot hydration.
  `session.go:219-224`. Related pre-existing item: **`applySeed` re-seed also
  drops usage tokens, Model, Branch, RecentTools** (`model.go:1452-1483`) — fix
  in the same carry-forward pass.
- [ ] **liveSSEReadyMsg lacks the attachedID guard (LOW)** that
  liveSSEReconnectMsg has (`model.go:916-918`): detach→fast-reattach installs a
  passive stream alongside the transcript's active client (double client, extra
  port-forward). Fix: mirror the guard — if `msg.id == m.attachedID`, cancel.
  `model.go:882`.
- [x] **ggPending has no reset-on-other-keys (LOW).** Fixed 2026-07-04
  (uncommitted claude-pane pass): reset hoisted to the top of `handleKey`
  (`model.go:1748`). Detail in the done log. (The `q`/`g` binding overloads
  item in §2d remains open.)

### 1b. Group view / sort / search / pickers (2026-07-04 audit, verified)

The two group-view items share a root cause with the long-tracked **consolidate
the two row models** refactor — `visibleSessions()` vs `visibleRows()` both
interpret `m.cursor`; one row abstraction with a `sessionAt(cursor)` accessor
for render+nav+actions subsumes them (`groups.go:57`). Prefer that fix.

- [x] **Account picker silently drops pastes (HIGH).** Fixed 2026-07-04
  (`cb0e375`): PasteMsg routed to picker label/console forms via `pickerPaste`.
  Detail in the done log.
- [ ] **Group view ignores filter + attention ordering (MED).**
  `groupedSessions()` iterates `m.sessions` raw; `/` filter is enterable but
  silently inert (no narrowing, no 'no match' state); attention-first float
  inert too. Also filter-mode j/k clamps against `visibleSessions()` while the
  cursor indexes header-inclusive `visibleRows()` (last rows unreachable), and
  j/k interception makes those letters untypeable in queries. Fix: build groups
  from `visibleSessions()`; clamp with `visibleRows()`; arrows-only nav while
  the filter buffer captures text. `groups.go:88-96`, `model.go:2042-2054`.
- [ ] **ctrl+g jump sets a session index into a display-row cursor (MED).** In
  group view (headers occupy rows) the cursor lands wrong — post-detach
  approve/suspend/destroy target the wrongly highlighted session; target may be
  in a collapsed group. Fix: locate in `visibleRows()` skipping headers; expand
  collapsed group. `notify.go:209`.
- [x] **Descending sort comparator is invalid (MED).** Fixed 2026-07-04
  (`cb0e375`): three-way cmp + sign flip + fixed ID tie-break; DisplayTitle.
  Detail in the done log.
- [ ] **Archive is a complete no-op (MED).** `A` writes `Archived=true`; zero
  readers repo-wide (not in visibleSessions/grouped/sort/render); flag is
  in-memory only. Fix: filter + archived section, or remove the binding until
  built. `groups.go:200`.
- [x] **Transcript search drops uppercase + byte-wise backspace (MED/LOW).**
  Fixed 2026-07-04 (`cb0e375`): accept ModShift in `searchKey`; rune-wise
  backspace. Detail in the done log.

### 1c. Rendering / layout bugs (2026-07-04 audit, verified)

- [x] **`hasLinkRefDef` isn't fence-aware (MED, perf-critical).** Fixed
  2026-07-04 (uncommitted claude-pane pass): fenceInfo tracking skips fenced
  lines. `chat/streaming_markdown.go:466-487`. Detail in the done log.
- [x] **`truncate()` not ANSI-aware (LOW).** Fixed 2026-07-04 (uncommitted
  claude-pane pass): delegates to `ansi.Truncate`. `model.go:2635-2645`.
  Detail in the done log.
- [ ] **Spread rows never truncate segments (LOW, three spots).**
  (1) header/hint/status rows (`transcript.go:1352,1422`, `statusline.go:440`):
  long title or queued-chip+workingStatus overflow and fitModal right-clips —
  exactly the status glyph / "esc sends now" affordance disappears at ~100
  cols; (2) `clampLines` pads but never truncates, breaking its "exactly w×h"
  contract — topBar/clusterStrip escape at narrow widths (`zones.go:33`);
  (3) external pane statusRow: long DisplayTitle wraps the bar to 2-3 lines
  (`external_pane.go:459`). Fix pattern for all: measure right segment,
  truncate left to fit, THEN pad.
- [ ] **renderToolCard budgets overflow by construction (LOW).** arg≤width/2 +
  summary≤width/3 + icon/name/separators ≈ (5/6)·width + len(name) + 8, then
  placeIndent adds 3 — clip cuts the result text ("· old_string not found")
  with no ellipsis. Fix: budget summary from measured remaining width.
  `transcript.go:1209`. (The §2c two-line ⏺/⎿ card redesign fixes this by
  construction — prefer doing them together.)
- [ ] **Theme change doesn't invalidate render caches (LOW).** list cache
  (blockFP has no theme input), AssistantItem sections, StreamingMarkdown
  stable prefix all keep old-palette ANSI after `ApplyForBackground` —
  dark-palette lines near-invisible on light terminals until a width change
  flushes. Fix: theme epoch folded into cache keys via `theme.OnChange`.
  `transcript_list.go:78`, `chat/assistant.go:138`, `chat/streaming_markdown.go:34`.
- [ ] **Composer width formula split-brain below width 21 (LOW; latent).**
  layout() uses `max(10, m.width-5)`, renderInput() uses `max(20, m.width-1)-4`.
  Fix: one shared formula. `transcript.go:914` vs `:1373`.

### 1d. System reliability (2026-07-01 whole-system review; HIGHs all fixed — see done log)

- [ ] **O(sessions) laptop cost with no steady-state cap (MED-HIGH).**
  `connectSem` (cap 4) throttles the connect *burst* only. Steady state: N warm
  sessions = N SPDY port-forwards through one kube-apiserver + N SSE streams +
  ~2N goroutines + N heartbeat timers, no LRU eviction. First breakage:
  API-server port-forward pressure (~30 sessions). Cap
  concurrently-*established* observer forwards, evict the coldest.
  `internal/tui/dashboard/model.go:1166`.
- [ ] **SSE consumer backpressure → forced disconnect/replay loop (MED).**
  Events channel buffered at 64; a stalled TUI blocks the scanner, the 90s
  watchdog sees no reads and force-closes → reconnect-with-replay. A slow
  consumer manufactures reconnects. `internal/runner/client.go:286,291`.
- [ ] **Port-forward retries a dead pod forever (MED).** On
  `resolvePodForForward` error the loop keeps the *stale* pod, no terminal
  state — after `sandbox destroy` from another shell, an observer forward
  hammers the vanished pod at ≤10s cadence indefinitely; no "gone forever" vs
  "rescheduling" distinction for the handle owner. `internal/k8s/portforward.go:148`.
- [ ] **Dead-node pods read as Running for minutes (MED).** Both status paths
  trust k8s conditions with no staleness cross-check — node-eviction lag makes
  a crashed session look healthy with a silently-stalled SSE stream.
  `internal/k8s/backend.go:804`, `internal/k8s/watch.go:203`.
- [ ] **Concurrent sessions on one project share one local sync endpoint, no
  dedup (LOW-MED).** Mutagen session name keys on SessionID only; two agents on
  the same repo silently cross-feed edits (same-file race → perpetual
  conflicts). `internal/sync/sync.go:130`.
- [ ] **Mutagen conflicts invisible in the TUI (LOW-MED).** `classify()`
  collapses conflicts into the same `SyncStalled` glyph as transport errors; no
  file/side detail, no resolution hint. `internal/sync/status.go:154`,
  `internal/tui/dashboard/model.go:2431`.
- [ ] **Transcript sync merges pod-agent history into local `~/.claude`
  unscoped (LOW-MED).** By design (subPath bind), but pod conversations become
  locally `--resume`-able with no tag or audit trail back to the sandbox
  session. `internal/k8s/backend.go:1233`, `internal/sync/sync.go:62`.
- [ ] **`destroy` gives no active-turn warning though reversible `suspend` does
  (LOW-MED).** `suspend` dials the runner and warns on `ActiveTurnID`;
  irreversible `destroy` prints a generic prompt. `internal/cli/commands.go:168`
  (vs `:97`).

### 1e. Autopilot (`/loop`/`/goal`) — detach durability + termination (2026-07-04 review of the uncommitted `autopilot.go`)

**Target use case (maintainer):** set an agent loose iterating through `TODO.md`
with Opus, detach to the dashboard, and come back to finished work. What already
works (verified + tested, `autopilot_test.go`): `/loop` survives detach — ticks
carry session+gen (`autopilot.go:75`), `App.autopilotTick` routes them to the
retained warm model and re-points POSTs at the dashboard's background SSE client
(`app.go:867`, `warm.go:49`); model/effort/mode overrides ride every loop turn
(`transcript.go:1916`). The gaps below are what breaks the use case. **Plan
items 1–2 together** — they share one "completion scan on turn end" mechanism.

- [x] **1. `/goal` continuation dies on detach (HIGH — the headline gap).**
  DONE: `handleRunnerEvent` now drives detached goal continuation after ingest
  (`model.go` `driveDetachedAutopilot`) — it rewires the warm model to the live
  background client and returns `autopilotAfterTurn()`'s continuation Cmd; goal
  reached surfaces via `autopilotToast` (reuses the `toastMsg` OS-notification
  plumbing). Foreground path unchanged. Tests: `TestGoalContinuesWhileDetached`,
  `TestDetachedGoalReachedStopsAndToasts`.
  `autopilotAfterTurn` runs only in the foreground `handleEvent` path, gated on
  `m.events != nil` (`transcript.go:2287`) because a background `ingest()`
  discards returned Cmds — so the natural primitive for "work until TODO.md is
  done" stalls after the in-flight turn the moment the user detaches. Fix: route
  goal continuation through the App exactly like `autopilotTickMsg` already is.
  Mechanism: the background apply path (`model.go:1371`, `tr.ingest(...)` inside
  `handleRunnerEvent`) sees every `turn.completed`; after ingest, when
  `tr.autopilot.kind == autopilotGoal`, install the live background client on
  the model (`t.client = liveClient`, the `app.go:888` pattern) and return
  `tr.autopilotAfterTurn()` as the Cmd (`handleRunnerEvent` already returns
  Cmds). Keep the foreground path as-is; guard with gen so a stopped goal can't
  continue. **Acceptance:** start `/goal`, detach mid-turn, stay on the
  dashboard → turns keep chaining until the sentinel; "goal reached" surfaces
  as a toast/OS notification (reuse `notifyIfBackgroundAttention` plumbing),
  not silently in a parked transcript.
- [x] **2. `/loop` never terminates — teach it the sentinel (HIGH for cost).**
  DONE: `autopilotAfterTurn` now handles loop mode — on each loop turn's
  completion it scans `m.lastAssistantText` with `goalReached()` and stops +
  toasts on `GOAL_MET`, foreground (via `handleEvent`) and detached (via the
  same `driveDetachedAutopilot` hook as item 1). The `/loop` usage line documents
  the sentinel tip. (Optional `/loop until <condition>` flourish not done.)
  Tests: `TestLoopStopsOnSentinelForeground`, `TestDetachedLoopStopsOnSentinel`.
- [x] **3. Loop/goal lapse is silent (MED).** DONE (toast half): `App.autopilotTick`
  emits `autopilotToast(sess, "⟳ loop ended — session suspended")` when a tick
  finds the warm model gone (`app.go`), instead of returning nil silently. Test:
  `TestAutopilotLapseToastWhenModelGone`. NOT done: recording the driver spec in
  `internal/index` for a one-key re-arm on re-attach — left as a follow-up.
- [x] **4. Idle-reaper can kill the pod between iterations (MED).** DONE (warn
  path): `TranscriptModel.idleTimeout` is threaded from the dashboard (attach +
  `ensureRetained`); `cmdLoop` warns in the transcript when `interval >=
  idleTimeout`. Chose warn over clamp (the user may keep the session busy another
  way). The bigger "resume, then fire" alternative was NOT taken. Test:
  `TestLoopWarnsWhenIntervalExceedsIdleTimeout`.
- [x] **5. esc contract inconsistent with the chip (LOW).** DONE:
  `escapeConsumes()` returns true when `m.autopilot.active()` (`modes.go`), and
  the esc handler stops the driver in that branch when there is no live turn to
  interrupt (`transcript.go`); detach stays on ctrl+]. Test:
  `TestEscStopsIdleAutopilot`.
- [ ] **6. Server-side loop for true laptop-closed autonomy (ADR
  Opus-draftable; implementation gated on maintainer sign-off — items 1–5 do
  NOT depend on it).** The driver is a `tea.Tick` in the local TUI; quitting
  the TUI or a long sleep kills it. **Fable review (2026-07-04): proceed with
  the ADR; recommended direction = runner-owned driver.** Sketch for the ADR:
  loop/goal spec persisted on the session (new endpoint or a turns-API
  extension); runner self-submits the next turn on turn-completion +
  interval; driver state (armed/tick/stopped/sentinel-met) becomes a new
  event type via `schema/events.json` + `just gen`; the TUI arms/disarms and
  renders from events. Must-answer questions: idle-reaper interaction (an
  armed driver marks the session non-idle until sentinel/stop — cross-ref the
  reaper design), a hard max-iterations/token-budget guard so an unattended
  loop can't run away, and whether the local tea.Tick driver is retired or
  kept as a degraded fallback once this lands.
- Context note for long runs: each iteration is a new turn in one continuous
  SDK session — multi-hour Opus runs lean entirely on server-side compaction,
  and ctx% is silently wrong after it (§2b gap 4). Fix that item before or
  alongside heavy loop usage; no separate work here.

## 2) The "feels like Claude Code" program (2026-07-04 audit)

**Strategy context:** using Claude Code itself as the client is SETTLED — not
happening (see §3). This program is the alternative: close the gap between our
TUI and Claude Code's feel. Three coordinated tracks: **2a structure**
(enablers — do the first two before heavy 2c work), **2b pipeline** (what the
event model can even represent), **2c renderer** (what it looks like). §2d
carries still-open items from earlier UX passes that slot into the same work.
Already at parity: plan mode, interrupt-with-partial-output, file-edit diffs,
resume.

### 2a. Structural enablers (transcript/model decomposition)

- [ ] **Unify the dual block representations (HIGH — the biggest blocker).**
  `blocks []tblock` + parallel `items []*blockItem` reconciled by hand-rolled
  fingerprints (`reconcileItems`/`blockFP`), while `chat/` already has per-role
  item types — and `renderBlockRaw` shadow-renders tool/reasoning/notice blocks
  outside them. Every mutation must remember markBlockDirty/syncBody (the RV9
  trap, `transcript.go:2325`). Expandable tool cards / per-block focus / copy
  need per-block state with nowhere to live. Fix: chat items become THE block
  representation implementing `list.Item`, owning render + version + expanded
  state; delete tblock/blockItem/blockFP; syncBody → SetItems + pin.
  `transcript.go:121`, `transcript_list.go:180-285`. (Also retires the
  "reconcile is O(n) per event" perf item.)
- [ ] **One event reducer, not two drifting switches (HIGH).** `handleEvent`
  (~25 cases, `transcript.go:2118`) and `ApplyRunnerEvent` (`session.go:565`)
  both unmarshal the same payloads into duplicated state (status/usage/git/
  rate-limit), forcing App.Update to mirror across the seam
  (`app.go:521,688`). New event type = editing two switches + SessionSnapshot.
  Fix: shared `sessionReadModel` with one ApplyEvent reducer (event-type
  table), embedded by both; SessionSnapshot becomes its serialization.
  De-risks every §2b event addition.
- [ ] **Declarative vertical layout regions (HIGH).** Stack arithmetic
  hand-counted in 4+ places (layout(), renderTranscript(), previewView
  `h-3-bannerH`, scrollbarDragTo `bodyTop=2`, App.modalRect) — any layout
  change (status-line move, header removal, inline perm prompts) means finding
  every copy; mouse hit-testing silently breaks. Fix: one per-frame
  `[]region{name, height, render}` with body as flex; all consumers walk it.
  `transcript.go:882`.
- [ ] **Mechanical god-file split (MED — do first; makes the rest reviewable).**
  Seams already exist as function clusters: transcript_{stream,reduce,render,
  input}.go + permission_diff.go; model_{sse,reduce,render}.go. Zero behavior
  change. `transcript.go:29`.
- [ ] **App.Update flat dispatch + one detachTranscript() (MED).** 450-line
  screen-router; detach sequence duplicated 4×; recursive
  `a.Update(*msg.ready)` re-entry (`app.go:615,630`); B17 single-delegation
  enforced only by comments. `app.go:368`.
- [ ] **Explicit input contexts + binding tables (MED).** ~180 lines of raw
  string-compare if-chains encode key precedence by code order (esc cascade =
  5 levels); half of Model.handleKey bypasses KeyMap so help/footer can't tell
  the truth and rebinding is impossible. Fix: context enum + per-context
  binding table; esc cascade = ordered action list, unit-testable.
  `transcript.go:1602`, `model.go:1745`.
- [ ] **permissionPrompt component (MED).** Permission feature smeared across 4
  places incl. pre-rendered strings held as model state with asymmetric
  refresh (plan cards read stale cache, perm boxes re-render live) + a second
  independent surface in permqueue.go. Component owns grace-gate/diff/plan
  variant + Height()/Render(w)/HandleKey; perm queue reuses it. Natural vehicle
  for the §2c numbered-options redesign. `transcript.go:135,1433,2817`.
- [ ] **Clock injection sweep (MED, testability).** nowFunc exists but the
  permission grace gate anchors on time.Now() (`transcript.go:2382`) while
  permissionAnswerable compares nowFunc (`:1591`) — the anti-type-ahead
  behavior is untestable; same for turnStart/toast/status fades. Also:
  test-only counters (reconciles/fpComputes/bdBuilds) live as prod struct
  fields → test-observer interface. `rg 'time\.Now\(\)' internal/tui/dashboard`.
- [ ] **Dedup: markdown-renderer closure ×3 (LOW)** (`transcript.go:1029,2331`,
  `transcript_list.go:88` — T1-drift hazard) and **status→label switches ×2,
  already drifted** ('waiting' vs 'awaiting approval'; `session.go:338` vs
  `transcript.go:1304`) → one `SessionStatus.Label()` table + exhaustive test.

### 2b. Event-model parity gaps (schema → mapper → renderer)

Which Claude Code UI capabilities the pipeline can't represent / doesn't map /
doesn't render. These cap how Claude-Code-like ANY client can feel. Schema
changes go through `schema/events.json` + `just gen` (never hand-edit `*.gen.*`).

- [ ] **1. Subagent output flattens into the main transcript (correctness
  bug).** `MessagePayload` has no `parentToolUseId`
  (`schema/events.json:88-95`); `handleStreamEvent` receives it but only
  attaches it to `tool_use`, never text/thinking deltas
  (`runner/src/mapping.ts:119-124,249-253`) — a running Task's narration
  interleaves into the single `assistantBuf` (`transcript.go:2301-2306`),
  corrupting the main streaming reply. Fix: schema field + `just gen`; thread
  in handleAssistantMessage/handleStreamEvent; route parented events into the
  subagentCard (`subagent.go`) → also unlocks per-agent transcripts.
- [ ] **2. "Always allow" built but unreachable.** Runner fully implements
  `scope:'session'` grants + edited input (`claude.ts:38,374-381,401-408`,
  `grants.ts`, `server.ts:247`); TUI hardcodes `Scope:"once"`
  (`transcript.go:2044`, `model.go:1414`) and offers only a/d. SDK `canUseTool`
  suggestions (3rd arg) also dropped (`claude.ts:387` two-arg callback; no
  PermissionPayload field). Mostly renderer work + one schema field. (Folds in
  the earlier "Permission scope is always once" item; lands cleanly inside the
  §2c numbered-options panel.)
- [ ] **3. Thinking invisible until complete.** `reasoning.delta` streams fine;
  TUI buffers silently, flushes only on completed (`transcript.go:2445-2468`)
  — long thinks show a bare spinner where Claude Code streams live.
  Renderer-only: mirror the streamAI live path (`:2284-2306`); make blocks
  expandable. (Folds in the earlier "multi-line reasoning unrecoverable" item;
  target presentation in §2c.)
- [ ] **4. No compaction signal.** `compact_boundary` system msg dropped
  (`mapping.ts:48-60`), no event type — ctx% silently wrong after server-side
  compaction. Fix: `context.compacted` event (pre/post tokens) + one-line TUI
  marker.
- [ ] **5. Background tasks / tool progress dropped.** `tool_progress` ignored
  (`mapping.ts:81-85`), no progress/notification event type — background Bash
  + async completion (signature Claude Code features) unrepresentable. Fix:
  schema event + updating tool-card status.
- [ ] **6. Citations + server-tool results discarded.** Text-block citations
  stripped (`mapping.ts:121-123`); `server_tool_use`/`web_search_tool_result`
  hit default-drop (`:153-156`) — WebSearch shows sourceless flattened text.
  Fix: optional citations field + footnote render.
- [ ] **7. Images unrepresentable end-to-end.** String-only MessagePayload;
  image blocks dropped (`mapping.ts:153-156,183-188`). Kitty plumbing exists
  TUI-side (gauge). Gap starts at schema (attachment payload or fetch-by-ref
  given the SQLite log).
- [ ] **8. Project slash commands / skills / CLAUDE.md absent in-pod.**
  `settingSources: []` (`claude.ts:231`) — user-defined commands don't exist
  for SDK turns. Config-level fix; interacts with config-input sync.
- [ ] **9. Single-slot client-local prompt queue.** `queuedPrompt` is one
  string (`transcript.go:332-334`) — second message overwrites; invisible
  cross-client. Claude Code has a multi-message editable queue.
- [ ] **10. MCP unwired.** No `mcpServers` in buildOptions; `mcp_*` blocks
  dropped. Generic tool.* events would mostly work once configured; non-text
  MCP results flattened.
- [ ] Minor: TUI ignores `MessagePayload.Role` — `message.completed` always
  appends a blockAssistant (`transcript.go:2308-2336`); mapper's `role:'user'`
  emissions would render as assistant markdown.

### 2c. Design/layout changes (renderer)

Deduped against `docs/ux-polish-plan.md` — nothing below is already committed
there. HIGH items are the at-a-glance tells; most are renderer-local.

- [ ] **Tool cards: adopt the ⏺-head + ⎿-elbow two-line idiom (HIGH).** Today
  one packed line (`⏵ Bash  npm test  · exit 0`). Claude Code's most
  recognizable element: line 1 `⏺ Bash(npm test)` (bullet colored by status),
  line 2 indented `⎿  exit 0 · 42 lines` (+ dim `(ctrl+o to expand)` when
  collapsed). Elbow column makes results scannable + anchors expansion. Fixes
  the §1c budget-overflow bug by construction. `transcript.go:1185`. Pairs
  with: **tool-card output expansion** ("slice 5i" — Bash output collapses to
  "N lines" with no way to view it; post-approval diffs vanish from
  scrollback, only the permission box ever renders the diff;
  `transcript.go:1258`).
- [ ] **Kill the full-height `▌` role gutter bars; quiet user prompts (HIGH).**
  Colored bars down every message line + bold-green user text is the largest
  departure from CC's look. Assistant: single `⏺ ` bullet + 2-space hanging
  indent. User: dim `> ` prefix, drop Bold+Guac (user's words are the QUIETEST
  element in CC, not the loudest). Frees role colors to mean status.
  `transcript.go:962`.
- [ ] **Working indicator: left-aligned line above the composer, with "esc to
  interrupt" (HIGH).** Now right-aligned below the box and never mentions esc
  — the most important in-turn action is undiscoverable while a turn runs (esc
  DOES interrupt, `transcript.go:1637`). Target: `✳ Thinking… (12s · ↓820
  tokens · esc to interrupt)` above the input; `· esc to steer` when a prompt
  is queued. Context-aware verb is free: m.reasoning→"Thinking…", running
  tool→"Running Bash…", m.streaming→"Writing…". `transcript.go:1402`, `:1970`.
- [ ] **Statusline: collapse the permanent two-row gauge block (HIGH).**
  Always-on model/cwd/branch/ctx-bar/cost row + rate-limit block-bar row reads
  as a monitoring dashboard; CC's floor is near-zero chrome. Default to one
  quiet row; ctx gauge only ≥60%; cost behind /cost or threshold; rate-limit
  row transient after `rate_limit.updated` or behind /usage.
  `statusline.go:380`. Related open item: **ctx% fallback inconsistency** —
  chat assumes 200k when the model limit is unknown, dashboard hides the gauge
  (`statusline.go:402`, `session.go:197`).
- [ ] **Permission prompt: numbered-option question panel (MED).** `[a]/[d]`
  hotkey hints → CC's signature `Do you want to run this command?` + numbered
  arrow-navigable options with ❯ selection (keep a/d as hidden accelerators).
  Designed so "2. Yes, don't ask again" slots in when §2b gap 2 lands.
  `transcript.go:1433`. Build on the §2a permissionPrompt component.
- [ ] **De-bracket system notices (MED).** `[interrupted]`/`[reconnected]`/
  `[permission approved]` read as debug logs. → `⎿  Interrupted by user`
  (Coral, attached under the cut block), `⎿  Permission approved` under the
  tool card, plain dim sentences for connection state. One helper replaces the
  appendBlock(blockInfo, "[…]") sites.
  `transcript.go:2297,631,650,2050,2467,625`.
- [ ] **Blank line between every top-level block, not just before user turns
  (MED).** CC's perceived calm comes from one blank line per ⏺ entry; keep
  consecutive tool cards tight. Must adjust the streaming tail identically (T1
  height-jump invariant). `transcript_list.go:109`.
- [ ] **Drop the persistent title header + divider (MED).** CC has no top bar;
  ours duplicates the statusline and double-reports working state. Render only
  for exceptional states (reconnecting/terminating); emit title via OSC 0/2
  (tea signal plumbing exists from Phase 3). `transcript.go:851`.
- [ ] **Todo list: checkbox + strikethrough progression under the tool elbow
  (MED).** completed = ✓ strikethrough dim-green, in_progress = ▸ bright,
  pending = ○ dim; attach under the TodoWrite card, drop the standalone
  `▤ todo list` header; updates should mutate one pinned widget, not append
  blocks (§2b pipeline note). `transcript.go:1094`.
- [ ] **Thinking: italic dim body, same shape streaming and completed (LOW).**
  Target render for §2b gap 3: `Thinking…` label + italic TextMuted body,
  ~6-line cap + `… +N lines (ctrl+o)`. `transcript.go:1076`.
- [ ] **Transient scrollbar (LOW).** Permanent bright thumb is constant
  peripheral noise; show only when off-bottom (+ dim `↓ new output · G bottom`
  pill during live turns). `transcript_list.go:297`.

### 2d. Transcript/dashboard UX — still-open items from earlier passes

- [ ] **No prompt history (MED).** No up-arrow recall of previously sent
  prompts in the composer. `transcript.go:1762` (scrollKey owns ↑/↓).
- [ ] **`q`/`g` overloads on the dashboard (LOW-MED).** `q` opens the perm
  queue when any session waits (footer still says quit); lone `g` toggles
  group view, `gg` = top. Surprising vs advertised bindings.
  `model.go:1817,1866`. (Fix alongside the §2a input-context tables + §1a
  ggPending bug.)
- [ ] **ctrl+g/ctrl+k dead in the external pane; no next-attention key on the
  dashboard screen (LOW).** Cross-session nav inconsistent by screen.
  `app.go:752`.
- [ ] **Fresh Claude session renders a blank body (LOW).** No
  welcome/first-hint block, unlike the dashboard's firstRunView.
  `transcript_list.go:290`.
- [x] **Failed sessions aren't floated by attention-first sort (LOW).** Fixed
  2026-07-04 (uncommitted): `needsAttention()` now includes Failed and
  `sortByAttention()` floats via it. `attention.go:17`.
- [ ] **TUI has no path to a FIRST Anthropic account (LOW).** Zero stored
  accounts skips the account picker entirely, so "＋ add account" is only
  reachable once one exists via `sandbox auth login`. Decide: first-run hint,
  or always enter the account stage with cluster-default + add-account rows.
  `account_picker.go:123`. **Fable recommendation (2026-07-04):** always
  enter the account stage with cluster-default + "＋ add account" rows —
  discoverable, costs one keypress in the common case, no CLI detour.
- [ ] **Decide: default permission mode = yolo (`bypassPermissions`)?
  (maintainer ask, 2026-07 triage — needs the decision, then the change is
  small).** Today an empty/unknown mode resolves to `acceptEdits`
  (`runner/src/claude.ts:70-81`); the TUI/CLI send whatever the composer/flag
  holds (`transcript.go:2804`, `internal/cli/turn.go:67`). If yes: flip the
  default (runner-side, or CLI-side per backend), keep the SDK safety gate
  wired (`allowDangerouslySkipPermissions`, `claude.ts:216-218` — needs
  `IS_SANDBOX=1` as root), and surface the active mode in the statusline so
  yolo is visible. Cross-ref §8 "turn model is Claude-SDK-shaped" (the mode
  enum may be abstracted there). **Fable recommendation (2026-07-04, needs
  maintainer confirm before flipping):** yes — default pod sessions to
  `bypassPermissions`. The SDK safety gate is already fully wired (runner
  defaults `IS_SANDBOX=1` and sets `allowDangerouslySkipPermissions` for
  bypass mode, `runner/src/claude.ts:216-229`), pods have default-deny
  egress + Bash guards + audit log, and the headline §1e use case (unattended
  TODO burn-down) assumes it. Ship the statusline mode surface in the same
  change so yolo is never invisible.

## 3) Decision record — Claude Code as the local client (SETTLED 2026-07-04)

Three-track research (official surface, community art, repo feasibility) into
using Claude Code **directly** as the client for a remote sandbox session.
Outcome: **not happening; invest in §2 instead.** Kept here so nobody re-treads.

- **Blocked upstream:** Claude Code has no remote-attach transport — no analog
  of `codex --remote ws://…` / `opencode attach <url>`;
  `--input/--output-format stream-json` is a headless stdio protocol for a
  driving program, not an attach surface, and is undocumented
  (anthropics/claude-code#24594; feature requests #10042, #72448). Anthropic's
  first-party answer is the desktop app's SSH sessions (local GUI, remote
  agent) — a GUI, not the TUI.
- **REJECTED (maintainer): the `CLAUDE_CODE_SHELL` ssh-shim pattern** (local
  claude, Bash proxied over ssh; à la torarnv/claude-remote-shell,
  langwatch/claude-remote). Do not re-propose. Structural costs: rides an
  undocumented env knob; git split-brain with the `--ignore-vcs` project sync;
  bypasses the entire runner control plane (guards/audit/events/metrics/idle)
  — it un-sandboxes the sandbox.
- **Recorded, NOT planned — in-pod official TUI over `ssh -t` as an external
  pane** (codex Option-B shape; violates the "TUI not remote" latency bar).
  Mechanics if ever revisited: `ssh -t sandbox-<id> 'claude --resume
  <claudeSession>'`; binary already in the runner image; `CLAUDE_CONFIG_DIR`
  pod-side (`backend.go:1253`); resume id in `GET /sessions/:id/status`;
  external-pane precedent in `external_pane.go`. Known costs: keystroke RTT,
  CC renderer misbehaves in tmux (claude-code#9935/#4851), permission modal
  replaced by claude's own, guards/audit only via pod-side settings hooks
  (interactive claude reads settings, unlike `settingSources: []` SDK turns —
  `claude.ts:231`), no metrics-observer API, resume forks the session id,
  needs pod tmux for TTY-death survival.
- [ ] **Watch upstream for a real remote transport** (#10042, #72448, #24594).
  If one ships, it slots into the codex Option-B pattern
  (`docs/codex-integration-plan.md`) and obsoletes the custom transcript
  renderer for the claude backend. Cheap periodic check; no code now.
- [ ] **Document the supported escape hatch in README:** `cd <project> &&
  claude --resume <claudeSession>` continues a sandbox conversation locally
  with full history (deliberately designed in: real-host-path subPath mount +
  transcript sync). One-way fork only — local turns never flow back; exits the
  audit envelope.
- Also evaluated and rejected: SSHFS mounts (per-file-op RTT),
  MCP-ssh-tools-with-built-ins-denied (token-expensive file ops, model drifts
  back to native tools), dev containers (local isolation only), web teleport
  (web→local only).

## 4) Performance

- [ ] **Mutagen `sync list` subprocess forked per warm session every 4s** for
  the dashboard's whole life, regardless of focus. Gate on focus/visibility +
  back off. `model.go:73,845` (syncPollInterval / tick handler),
  `sync_support.go:55` (dashboardSyncProber), `status.go:42`.
- [ ] Warm-session detail preview re-renders the retained transcript tail
  every frame (no unchanged-guard). Re-verified 2026-07-04: it renders via
  `tr.tailLines(5, width)` (bounded), so cost is lower than originally
  claimed — measure before optimizing. `model.go:2537`, `transcript.go:2113`.
- [ ] `visibleSessions()` re-filters+re-sorts 4+ times per frame (twice in one
  statement at `groups.go:145`). Memoize per frame. Related: `partition()`
  computed 3× per frame (topBar, clusterStrip, progressState) —
  `zones.go:319`, `model.go:2616`.
- [ ] `bodyView` still ~283µs/frame: `fitModal` does two ANSI `lipgloss.Width`
  scans per visible line every frame. `transcript_list.go:302`.
- [ ] SSE `broadcast()` re-serializes the SSE frame once per client;
  serialize once. `runner/src/events.ts:263-266`.
- [ ] Streaming-markdown safe-boundary predicates rescan the whole growing
  buffer per delta (O(N²) over a turn). `chat/streaming_markdown.go:111-233`.
  (The §1c fence-awareness bug that compounded this was fixed 2026-07-04.)
- [ ] Resize is uncoalesced: width change drops the whole list cache + rebuilds
  a pooled renderer per WindowSizeMsg during a drag. `tui/list/list.go:74`.
- [ ] Glamour pads wrapped lines with per-space SGR runs (bytes; upstream
  glamour style; inflates parse work).
- Reconcile-is-O(n)-per-event: retired by the §2a block unification — don't
  fix separately.

## 5) New-session startup speed (ordered by likely win)

- [ ] **Shrink + split images, and deploy Spegel** — image pull dominates cold
  start and nothing warms it; the default image carries an opencode-only
  `npm i -g opencode-ai` layer the claude path doesn't need (codex will add
  more). Split per-backend images + run Spegel (P2P OCI mirror, via
  Argo/GitOps) so a cold node hits a peer cache. Default image ref:
  `client.DefaultRunnerImage` (`client/client.go:74`, flag wiring
  `internal/cli/claude_remote.go:35`); npm layer `runner/Dockerfile:66`.
  Decide image naming in the same change — the
  "claude-runner" name is a misnomer today (one shared image serves every
  backend; inbox 2026-07).
- [ ] **Stop gating the visible prompt on the 12s blocking first-sync flush** —
  open the transcript as soon as the runner is healthy; background the bounded
  flush (reuse the reconnect pattern). Keep *turn submission* gated on staging.
  (Pointers re-verified 2026-07-04: the connect orchestration lives in
  `client/`, not `internal/cli/connect.go`.) `client/session.go` Connect
  (~:190-320), `internal/sync/sync.go:224` (`FlushAll`).
- [ ] **Parallelize independent serial steps** — Secret+PVC creates (errgroup,
  then Sandbox); the 8 serial `mutagen sync create` execs (only the project
  sync is load-bearing; create the 7 config/transcript syncs lazily); the two
  serial port-forwards (HTTP+SSH). `backend.go:226-260`, `sync.go:85-124`
  (`CreateAll`), `portforward.go:47`.
- [ ] Tighten `waitForPodReady` poll 2s→~500ms-1s (pod-phase detail to the
  stepper already landed, Phase 2).
- [ ] Defer `ensureReaper` + launch-burst observer connects off the foreground
  connect path; drop the redundant connect-time Status Get + re-`ensureSSHKey`
  on the freshly-created path. `client/session.go:256` (`ensureSSHKey`),
  `:290` (`ensureReaper` call; impl `client/sync.go:184`).
- [ ] **Mutagen sync GC follow-ups** (core landed — see done log): **MF3**
  cross-context over-reap (stamp `--label sandbox-context=<ctx>` in CreateAll,
  scope List/gc to current context); **MF5** mid-session sync loss doesn't
  self-heal while SSE is healthy (SyncProber stall → re-run CreateAll+Flush);
  **SF1** auto-GC only runs T+30s into an open TUI (fire `reconcileListCmd` in
  Init; run gc core best-effort at CLI startup after MF3); make `just
  dev-reset`/`kind-down` run `sandbox sync gc` before deleting pods.
  `internal/sync/sync.go`, `internal/tui/dashboard/model.go`,
  `internal/cli/commands.go`.

## 6) Codex backend + credential manager

Plan: [`docs/codex-integration-plan.md`](docs/codex-integration-plan.md) —
remote app-server + local `codex --remote` TUI (Option B), mirroring the
opencode supervisor/external-pane pattern + runner metrics-observer. Backend id
`codex-app-server` reserved (`internal/session/types.go:52`). Auth =
ChatGPT-plan OAuth owned by the credential manager.

- [ ] **CLI-owned credential manager — write side.** Anthropic part DONE
  (multi-account store + Keychain/file backends + `auth
  login/list/logout/default`, public as `client/cred`). Remaining:
  codex/provider-key generalization on `client/cred` — macOS Keychain store
  (optional Secure-Enclave blob + Touch ID; file/env fallback on Linux),
  `sandbox auth {login,sync,logout}` (device-auth / setup-token / paste-key),
  create/connect **reconcile** that seeds the `agent-sessions` Secret +
  prompts for renewal when a cred can't auto-refresh. Generalizes
  `ensureSSHKey`. Egress allowlist must gain OpenAI/ChatGPT hosts.
- [ ] **Unified per-backend credential lifecycle (maintainer ask 2026-07-04;
  Fable-triaged same day — claude's model is the template, opencode/codex
  converge on it).** Target flow: TUI launch → preflight the backend's creds
  → if missing/bad, prompt reauth in-TUI (claude.ai vs console picker) →
  store locally (`client/cred`) → seed the per-session Secret → GC with the
  session. Already true for claude (verified 2026-07-04): secure local store
  with Keychain/file backends; per-session Secret seeding with account
  labels + reconcile on connect (`internal/k8s/backend.go:396`
  `syncSessionCredential`); Secret deleted alongside Sandbox+PVC on destroy
  AND on create-rollback (`backend.go:726,742` `deleteSessionResources`,
  idempotent). The gaps, in order:
  1. **Launch-time preflight + in-TUI reauth (NEW — the headline).** Connect
     today checks runner health only; a bad anthropic credential surfaces as
     a failed turn. Constraint: subscription setup-token expiry is opaque
     (`client/cred/store.go:100`), so "creds are good" needs a cheap
     host-side live probe, not an offline decode — wire it into the §6
     read-side `--check` machinery + dashboard auth strip rather than
     inventing a second checker. On failure at launch/create: enter the
     account picker in a "reauth" stage (picker + claude.ai/console stages
     already exist, `account_picker.go`) instead of failing the launch.
  2. **Device-code flow — investigate, then decide.** Subscription auth
     shells to host `claude setup-token`
     (`internal/cli/auth_accounts.go:30-47`; host-binary dependency, flagged
     in §7b item 4). Codex already chose device-auth for ChatGPT. Determine
     whether an Anthropic device-code flow is supported for claude.ai
     subscription tokens; if not, keep setup-token as the documented
     mechanism and have the reauth stage drive it (wrapped status quo).
     Console accounts stay paste-a-key.
  3. **Secret GC for out-of-band deletion (SMALL).** CLI destroy cleans up,
     but `kubectl delete sandbox` outside the CLI orphans the PVC + Secret.
     Set ownerReferences (Secret+PVC → Sandbox) so cluster-side deletion
     cascades. Cross-ref the §10 KRO ADR, which would subsume this.
  4. **Isolation contract (DECIDED — implement via §7a items 1/3).** No
     shared cross-provider Secret: each backend's key lives in the
     per-session Secret, seeded from `client/cred` for the selected provider
     only, fail-closed. Retires `opencode-credentials` — an
     `ANTHROPIC_API_KEY` must never ride a shared opencode Secret once
     per-account claude creds exist. This item is the cross-backend
     contract; the opencode mechanics live in §7a.
- [ ] **Auth status — remaining read-side scope** (core landed in
  `internal/authstatus`): dashboard strip rendering (CLI-only today);
  `--check` live pings (codex plan/rate-limit via app-server; provider key
  liveness); Claude check should read the credential store, not just env.
- [ ] **Codex transport spike — remaining (off-airplane).** Recorded so far:
  stdio app-server works (newline JSON-RPC, no-auth initialize);
  remote-control/ws needs the STANDALONE managed install (bundle in pod
  image); refresh+approvals delegated to the client; metrics/auth-status are
  client requests. Still TODO live: ws endpoint addressing + a 2nd-client
  thread-observe check.
- [ ] Codex runner-as-metrics-observer (same pattern as opencode's, app-server
  thread notifications).

## 7) Cross-backend parity (operational)

**Parity bar (maintainer 2026-06-24):** startup speed, detach + keybindings,
prompt/affordance UX, and surfaced metrics must be similar across all agents;
per-agent in-pane rendering can differ. The runner is the control plane and
metrics source for every backend. See the codex plan "Parity bar".

### 7a. OpenCode auth persistence / validation (2026-07-04 triage)

**Fable review (2026-07-04): direction approved — Opus-executable in the
order listed.** Current behavior (re-verified): provider keys come from a
shared namespace Secret (`opencode-credentials`) injected as `Optional: true`
env refs for ALL of Anthropic/OpenAI/OpenCode-Zen; `buildOpencodeConfig`
enables whichever env vars are present; OpenCode config/history live on the
session PVC (survive suspend/resume, not new sessions); no create/connect
validation anywhere — the whole chain is fail-open. Decisions: **(1)** do NOT
build an OpenCode-specific store — generalize `client/cred` with a provider
dimension first (that is §6's write-side item; the multi-account store,
Keychain/file backends, and secret/manifest split generalize cleanly — only
the type taxonomy and manifest filename are Anthropic-hardwired) and make
item 1 below consume it. **(2)** The connect preflight bar (item 2) is
Secret-presence + key-shape for the *selected* provider, fail-closed; live
provider/model pings belong behind `sandbox auth status --check`, never on
the connect path. **(3)** Item 3 drops `Optional: true` for the selected
provider's ref and stops mounting unselected providers at all. **(4)** Reaper
RBAC (item 5): replace the cluster-wide ClusterRole `secrets: get` with a
namespaced Role bound in `agent-sessions`. **(5)** Docs item lands last.
The cross-backend contract these decisions implement (preflight → reauth →
local store → per-session Secret → GC, one provider per Secret) is §6's
"Unified per-backend credential lifecycle" item — read that first.

- [ ] **Implement OpenCode local credential store + JIT Secret reconcile.**
  Replace the local-dev-only env/1Password path with a `client/cred`-style store
  and `sandbox opencode` preflight that creates/updates the namespace Secret
  before session creation when absent/stale. Current provisioning is only
  `dev/local/opencode-creds.sh:13-21,59-85` via `justfile:146-153,309-319`;
  session creation only validates generic options in `client/client.go:292-312`.
- [ ] **Validate OpenCode provider auth before/at connect.** `sandbox auth
  status` only reports local env vars (`internal/authstatus/providers.go:119-149`),
  while connect waits for runner health + `opencode serve` readiness only
  (`client/session.go:217-221,301-312`). Add a cluster-aware check for the
  selected provider key and, if feasible, a lightweight model/provider liveness
  probe before launching/attaching.
- [ ] **Stop injecting every provider key into every OpenCode session.** Current
  `opencodeEnv` adds optional refs for Anthropic, OpenAI, and OpenCode Zen from a
  shared Secret (`internal/k8s/backend.go:1342-1378`); generated config enables
  whichever env vars are present (`runner/src/opencode.ts:103-125`
  `buildOpencodeConfig`, `:225` `writeOpencodeConfig`). Move
  to per-session or selected-provider Secrets, reusing the per-session Secret
  creation seam (`internal/k8s/backend.go:292-319`) and make selected refs
  fail-closed.
- [ ] **Add freshness/rotation semantics for OpenCode credentials.** Stamp Secret
  data hash/source/version labels and reconcile on create/connect; document that
  env SecretKeyRefs are pod-start-time state, so rotation requires a pod restart
  or suspend/resume. Current local script preserves stale Secrets when no current
  source resolves (`dev/local/opencode-creds.sh:87-99`); OpenCode env is baked
  into the pod template at `internal/k8s/backend.go:1118-1119,1342-1378`.
- [ ] **Harden OpenCode secret handling and RBAC.** Stop printing secret prefixes
  (`dev/local/opencode-creds.sh:119-126`), enforce/warn on `0600` local overlays
  (`dev/local/secret-template.yaml:18-33`), avoid hardcoded namespace assumptions
  (`dev/local/opencode-creds.sh:29-32`), and narrow the reaper's Secret access
  from broad ClusterRole scope (`dev/local/manifests/agent-reaper.yaml:29-69`).
- [ ] **Document OpenCode auth/persistence for real clusters.** README only says
  OpenCode reads `opencode-credentials` (`README.md:117-123`); add exact keys,
  local-store/JIT reconcile behavior, validation limits, rotation requirements,
  namespace scoping, and what persists across suspend/resume vs new sessions.

### 7b. Flox/Nix-first runner environment (2026-07-04 triage)

**Fable review (2026-07-04): proceed — the ADR is Opus-draftable now;
implementation waits on maintainer sign-off of the ADR.** Context: the repo
has a committed Flox dev/CI environment (`.flox/env/manifest.toml:9-36`,
`.envrc:1-20`) and CI runs `flox activate -- just check`
(`.depot/workflows/ci.yml:42-86`), but runner pods are Debian/apt+npm images
with no `flox`/`nix` in the agent environment. Recommended ADR direction:
keep the Debian `node:24-slim` base (preserves sshd, native `better-sqlite3`,
the tested entrypoint/host-key path) and layer Flox (which vendors Nix) into
the image with the base tool closure baked in — do NOT flip to a fully
Nix-built OCI in the first pass. The §5 per-backend image split composes with
this (shared Flox layer). Per-session PVCs stay out of the `/nix`-store
business; cluster caching = substituters via the env seam (item 2), with the
maintainer's cache requirements (configurable trusted-substituters,
anti-poisoning publish gate, pruning) forming the ADR's cache section — the
scan-then-publish gate is a follow-on design, not runner-image scope. Decided
regardless of ADR outcome: the activation hook's `go get .` (item 6) should
go — it mutates go.mod/module cache as a side effect of `cd`.

- [ ] **Write an ADR for the runner package-manager strategy.** Choose between
  Debian+Nix/Flox, Nix-built OCI, Flox-containerized runner, or split per-backend
  images. Preserve sshd, Node 24, native `better-sqlite3`, `claude`/`opencode`,
  git, sqlite, and diagnostics. Current image uses apt + npm global opencode in
  `runner/Dockerfile:30-67`; entrypoint only starts sshd + Node
  (`runner/entrypoint.sh:34-38`); flake only packages the Go CLI today
  (`flake.nix:20-33`, `nix/package.nix:14-45`).
- [ ] **Add a runtime bootstrap env/mount seam for package managers.** Extend the
  common pod env (`internal/k8s/backend.go:1244-1277`) with package-manager
  preference, cache dirs, binary-cache config, and any optional `/nix`/Flox
  mounts while preserving the existing `/session` PVC + SSH mounts
  (`internal/k8s/backend.go:1185-1241`).
- [ ] **Propagate Flox/Nix preference to agent child processes.** Claude receives
  an explicit env map (`runner/src/claude.ts:213-231`); OpenCode serve inherits
  env and runs at `PROJECT_PATH` (`runner/src/opencode.ts:248-253`). Inject PATH,
  cache/config env, and agent guidance so agents prefer an existing project Flox
  env, create one only when appropriate, otherwise use `flox run` or
  `nix run nixpkgs#…` for one-off tools.
- [ ] **Update runner-image CI triggers and local tool checks.** If the runner
  image depends on `.flox`, `flake.nix`, `flake.lock`, or `nix/**`, add those to
  `.depot/workflows/build-runner-image.yml:12-20`. `opencode attach` requires a
  host `opencode` (`internal/tui/dashboard/external_pane.go:121-155`) and
  `claude setup-token` requires host `claude` (`internal/cli/auth_accounts.go:30-47`);
  package them in Flox if possible or make `just doctor` report the gap.
- [ ] **Plan Kubernetes Nix/Flox cache strategy.** Prefer baked closures when
  possible; otherwise define `NIX_CONFIG` substituters/trusted keys, egress
  allowlist, and a cluster cache/cache-warmer. Current caching is OCI-layer based
  (`internal/k8s/backend.go:194-208,55-62`), per-session PVCs are not a good
  shared `/nix` store, and the §5 Spegel item only covers OCI images.
  Maintainer requirements (inbox 2026-07): trusted-substituters configurable
  in the agent env so it "just works" (home = ceph S3 cache; work needs a
  reasonably generic mechanism); anti-poisoning — agents must not publish to
  the cache directly, e.g. an MCP tool/job that scans a closure and publishes
  it (without re-signing?) only if it passes; and a pruning story for entries
  no longer needed.
- [ ] **Clean up Flox env surprises before making it runtime-canonical.** The
  Flox manifest is now committed, so remove stale notes claiming it is missing;
  also review the activation hook that runs `go get .` on every activation
  (`.flox/env/manifest.toml:54-60`) before using the env as a reproducible
  runner or CI contract.

- [ ] CLI `opencode` still lacks `--model` and an initial-prompt arg
  (cancel/suspend-warning correctness landed — see done log).
  `claude_remote.go:23-71`.
- [ ] Verify detach (Ctrl+]) + surrounding chrome behave identically for every
  backend's external pane.
- [ ] **Live-session verify sweep — opencode (promoted from inbox 2026-07-04;
  needs the real cluster, not opus-offline):** (a) agent-generated title +
  busy/idle status should now stream via the Phase 4 observer — confirm
  against a live session, then archive; (b) clickable spots — real SGR mouse
  forwarding landed in Phase 3 and live capture showed opencode's own clicks
  working — confirm, then archive.
- [x] **Fable review (2026-07-04) — OpenCode idle/status/reaper fix: APPROVED,
  two follow-ups below.** Verified against the working tree: the `/proc`
  socket math is correct (`establishedConnections` counts server-side sockets
  with local port :4096; `runnerOwnedConnections` matches this process's
  client fds by socket inode via `/proc/self/fd`, so
  `externalClientConnections` isolates real attach clients and a runner
  loopback connection nets to zero); terminalizing observer `reset()` with
  `turn.interrupted` kills the stuck-busy-on-stream-drop class; the
  synchronous `/idle` activity probe closes the 20s-poller race; the
  dashboard `EventSessionStatusChanged` mirror matches the transcript's
  mapping, and its `clearPendingPermission()` calls are safe today because
  `setStatus` dedups and busy/idle fire only at turn boundaries
  (`runner/src/session.ts:202`, `claude.ts:345`) — re-verify if status
  emission points ever grow.
- [ ] **Follow-up: bound stuck synthetic-busy so it can't block the reaper
  forever (MED).** `recomputeIdle()` and `idleStatus().turnActive` now treat
  observer-set `status === 'busy'` as activity — right for real turns, but a
  wedged mapper / missed `session.idle` (the residual failure family) keeps
  the pod unreapable indefinitely. Add a staleness bound: synthetic busy with
  no observer events for N minutes AND no external clients → idle-eligible
  (or at minimum emit a warning event). `runner/src/session.ts:218,266`,
  `runner/src/opencode-observer.ts:114`.
- [ ] **Follow-up: GC `interruptedTurns` (LOW).** The module-global set only
  sheds an id when that turn's `session.idle` arrives; a stream drop in
  between leaks the entry. Clear it in `reset()`.
  `runner/src/opencode-observer.ts:49,188`.
- [ ] **Diagnose live: opencode looks stuck after disconnect/reconnect
  (maintainer report 2026-07-04; needs the real cluster — recipe below).**
  Symptom: sometimes, after detaching, a session appears frozen; on reconnect
  the pane shows the same file-read in flight far longer than plausible;
  possibly correlated with opencode-spawned subagents. Same day the
  continuously-attached tab showed the session recover and FINISH — so this
  is a stall or stale display, not a deadlock. Offline review found no defect
  that would stall `opencode serve` itself: the observer correctly gates
  child-session events (`opencode-observer.ts:150-151`) and the reviewed fix
  above already terminalizes observer stream drops. Candidates, most→least
  likely: (1) provider rate-limit/retry backoff during subagent fan-out —
  invisible in our UX because the observer surfaces no retry/rate-limit
  signal for opencode (contrast claude's `rate_limit.updated`); (2) pod CPU
  throttling under parallel subagents (check pod resource limits); (3)
  upstream `opencode attach` rendering a stale in-flight tool after
  reconnect (our PTY path just mirrors its bytes). Next occurrence, capture
  in order: (a) is the stuck tool in opencode's own pane or in our dashboard
  row/status (upstream vs our event model); (b) `sandbox trace` /
  `sqlite3 events.db` — a `tool.started` without matching completion, and
  wall-clock gaps in event `Time` during the window (real stall = no events
  for minutes; display bug = events flowing); (c) `kubectl logs` of the pod
  for provider retries; (d) `kubectl top pod` for throttling. If (1)
  confirms: observer-side retry/backoff surfacing → new event + statusline
  chip (§2b pattern). If (3): file upstream at sst/opencode.
- [ ] **Per-backend CLI smoke.** `internal/k8sit/cli_smoke_test.go`
  `TestCLISmoke` is opencode-only; make it table-driven over `backendCases`
  (gate the non-empty-output assertion on `expectRealReply`) so claude/codex
  fill the column.
- [ ] Should the opencode window feel like a modal over the dash? (design
  decision.)

## 8) Public SDK / client API (deferred design decisions, 2026-07-01 sweep)

Deliberately NOT auto-fixed (maintainer call). Breaking changes OK pre-OSS;
update `sdktest/` pins in the same change.

- [ ] **client: no external test seam / `WithBackend` unusable outside the
  module.** The option takes concrete `internal/k8s.Backend` (importers can't
  name it; only untyped nil compiles), so no fake injection for
  `Create`/`Connect` orchestration tests (zero unit coverage). Narrow public
  backend interface, or drop the option. Deliberately un-pinned in
  `sdktest/surface_test.go`. `client/client.go:141,150`.
- [ ] **The "normalized" turn/state model is Claude-SDK-shaped (MED).**
  `TurnInput.Mode` is the literal SDK permission-mode enum (opencode discards
  it; codex will too); `Connection.Opencode` is backend-specific in the central
  public struct (codex plan pre-announces the break); `State.ClaudeSession` has
  no slot for opencode's resume id. Model execution-policy/state abstractly.
  `internal/session/types.go:175-178` (TurnInput.Mode), `:144-153`
  (State.ClaudeSession), `client/session.go:62`.
- [ ] **tui/theme: closed registry + missing exported tokens.** No
  `Register(Theme)` despite the doc promise; `Denied/Info/Success/Warning`
  tones have no exported active vars. `tui/theme/theme.go:63,107-144`.
- [ ] **tui/kit: unsynchronized global palette.** `SetComponentColors` writes a
  plain map read on every render; `theme.ApplyTheme` off the render goroutine
  is a concurrent-map panic; two tea.Programs share one palette.
  Atomic-pointer swap or documented single-goroutine ownership.
  `tui/kit/style.go:21`, `tui/kit/components.go:32`.
- [ ] **tui/list: `Item.Finished()` is dead API** — never called, every
  implementer must write it. Drop it. `tui/list/list.go:12`.
- [ ] client: `Destroy` stops sync *after* the cluster destroy (library callers
  race EOF errors; TUI's PreDestroyHook covers interactive). `client/client.go`.
- [ ] client: `DialRunner` forwards the unused SSH port. `client/client.go`.
- [ ] `sandbox shell` has no `client/` equivalent — dogfooding gap; external
  consumers can't replicate a shipped command. `internal/cli/shell.go`.
- [ ] kit.FormatTokens caps at "1000M". `tui/kit/style.go`.
- [ ] WithStateDir ssh-dir layout: per-session SSH include lives in a *sibling*
  `ssh/` dir of the state root; containing it is a breaking include-path
  migration — decide pre-OSS. `client/sync.go`.

## 9) Unbuilt features

- [ ] **T10 — working-directory picker** (only unexecuted superpowers plan;
  `docs/superpowers/plans/2026-06-22-t10-working-dir-picker.md`): dirPicker
  overlay end-to-end — `dirpicker_path.go` (~-expansion, child listing,
  longest-common-prefix completion, validation) + overlay struct (open/close,
  prefill, Tab, recents) + wiring before the backend picker + thread
  `projectPath` into the Creator. None exists.
- [ ] **Tekken-style agent-picker modal** — animations + per-agent
  ascii/ansi portrait.
- [ ] **Per-session git worktree lifecycle (maintainer feature ask, promoted
  from inbox 2026-07-04; design first, lands in the public SDK).** New
  sessions should automatically get their own worktree, and on sync-back the
  work must be reachable on the laptop as a sanely-named branch — never a
  cryptic worktree name, never silently lost. Pieces: (a) auto-create a
  worktree at session create (`client/` + CLI/TUI); (b) a "convert to branch"
  affordance (keymap) where an LLM proposes the branch name + commit message
  but the git operations run deterministically CLI-side with human
  confirmation (LLM generates content only); (c) merge/cleanup semantics —
  mutagen syncs from the laptop, so merging into main happens laptop-side and
  the remote shouldn't pull; the agent should name/clean the worktree before
  it becomes a human-pushed branch; (d) reap abandoned worktrees. SDK-first
  rule applies (§8/CLAUDE.md). Cross-ref §1d "concurrent sessions on one
  project share one sync endpoint" — per-session worktrees are plausibly the
  fix for that collision; design them together.

## 10) Harness / tests / docs / ops

- [ ] **`just check` prints green even when gates were skipped.**
  lint/typecheck/runner-tests skip-with-warning when tools are absent, then
  `check` still ends "all gates passed". Track skips: "passed (N gates skipped
  — CI enforces them)". `justfile:24`.
- [ ] **sdktest does not cover the public `tui/` packages.** Add
  `tui_surface_test.go` pinning load-bearing exports of
  `tui/kit`/`anim`/`list`/`theme`/`terminal`.
- [ ] **`client.RunnerClient` widening is not guarded.** Consumers implement it
  for fakes; adding a method is a silent break. Pin in sdktest with a stub
  like `consumerStore` does for `cred.Store`. `client/client.go:47`.
- [ ] `TestAppExternalPaneEscIsForwardedNotDetached` fails in-sandbox (PTY
  spawn blocked; passes unsandboxed) — add to the in-sandbox caveat list in
  CLAUDE.md. `internal/tui/dashboard/actions_test.go:403`.
- [ ] Ops: new CLI-created sessions use `:latest` and can hit the stale traefik
  manifest cache — bust the cache or pin digests CLI-side. (Resume path
  already fixed via digest pinning — see done log.)
- [ ] **End-user host setup recipe / doctor (promoted from inbox 2026-07-04;
  maintainer wrote "AICR recipe" — confirm what the acronym meant; Fable
  2026-07-04: no expansion found anywhere in the repo, maintainer must
  expand — the item is otherwise executable as a `sandbox doctor` design).** A
  first-run check/setup path so the CLI "just works" on a fresh host:
  kubeconfig + context, mutagen binary, ssh, runner/reaper image refs,
  credential store. `just doctor` today only validates the Flox *dev* env
  (`justfile:243-271`), nothing exists for an end user of the `sandbox`
  binary itself (`sandbox doctor`?).
- [ ] **Research/ADR: KRO composite resource (promoted from inbox
  2026-07-04).** Could KRO wrap Sandbox+PVC+Secret(s) into one custom
  resource, replacing CLI-side create orchestration
  (`internal/k8s/backend.go:226-319`)? Key question: custom status/conditions
  support. Short ADR only; no code until decided.
- [ ] **Observability for startup + steady state (promoted from inbox
  2026-07-04; unowned).** Metrics/tracing to analyze cold-start (feeds §5)
  and runtime fan-out cost (feeds §1d). Minimal first cut: timing spans in
  the CLI connect path + runner turn lifecycle.

## Open caveats (carry-forward)

- [ ] Resumable-transcripts migration: pre-existing sessions' old
  `-session-workspace-…` transcripts may break in-session resume-by-id across
  the host-path migration → call out in release notes.
- [ ] rate-limit/usage: unverified against a live max/pro session; consider
  pinning the Agent SDK version; `seven_day_oauth_apps` + `extra_usage`
  (overage) are dropped runner-side; black-line/opacity fixes unverified in a
  live attach.
- [ ] `~/.claude/todos` + `~/.claude/tasks` sync is ancillary (not required for
  resume) — keep but low priority.

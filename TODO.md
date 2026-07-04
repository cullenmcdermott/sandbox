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

## 0) Inbox — human notes, needs triage

Raw maintainer notes. Triage = either promote into a numbered section with
pointers, or answer inline and archive. (Resolved investigations moved to the
done log.)

* Do we need a "claude-runner" specific image or is that name a misnomer at
  this point?
* Automatically create worktrees? How to handle ensuring they are merged in and
  not accidentally "lost"? Since mutagen syncs from the laptop it can merge into
  "main" from the laptop, it just shouldn't try to pull from remote? TUI creates
  a worktree as part of creating a container; agent should clean it up / name it
  properly before it becomes a pushable branch pushed by a human.
* Match claude code UX / feel familiar to claude code users while staying
  distinct → **this is now §2, the "feels like Claude Code" program.**
* Consider KRO to wrap resources into our own "custom resource"? Does it
  support custom status/conditions?
* Expose CLI as a go library → largely exists as the public `client/` SDK (CLI
  dogfoods it); remaining API-shape work tracked in §8.
* opencode doesn't get an agent-generated title and reports "idle" even when
  working? → may be fixed by the Phase 4 opencode-observer (title/status now
  stream live); **verify against a live session before closing.**
* Should the opencode window feel like a modal on top of the dash like the
  claude code tui?
* opencode wants clickable spots → likely works since Phase 3 item 3 (real SGR
  mouse forwarding; live capture showed opencode's own clicks working) —
  **verify, then archive.**
* Speed up startup — pre-cache agent images? Metrics/tracing/observability to
  analyze this and other parts later? → overlaps §5; the observability ask is
  unowned, keep here.
* Add flox/nix support: install by default + agent guidance to use flox/nix;
  nix binary cache inside the k8s cluster (how to publish/update the cache?).
* Why does `just dev` start a claude session automatically?

## 1) Correctness bugs

### 1a. TUI SSE / state-machine cluster (2026-07-04 audit, verified)

**Plan these six together** around a single "stream registration" invariant
(in-flight-connect tracking + per-stream generation tokens) — they interact,
and piecemeal fixes will fight each other. All in `internal/tui/dashboard/`.

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
- [ ] **ggPending has no reset-on-other-keys and no timeout (LOW).** Early-return
  branches (R/A/space/ctrl+k/?/q/esc/y/n + overlays) skip the `:1891` reset — a
  lone `g` minutes later is misread as the gg chord. Fix: reset at top of
  handleKey for non-g keys, or 500ms expiry. `model.go:1871`. (See also the
  `q`/`g` binding overloads item in §2d.)

### 1b. Group view / sort / search / pickers (2026-07-04 audit, verified)

The two group-view items share a root cause with the long-tracked **consolidate
the two row models** refactor — `visibleSessions()` vs `visibleRows()` both
interpret `m.cursor`; one row abstraction with a `sessionAt(cursor)` accessor
for render+nav+actions subsumes them (`groups.go:57`). Prefer that fix.

- [x] **Account picker silently drops pastes (HIGH).** FIXED 2026-07-04
  (`cb0e375`): PasteMsg routed to picker label/console forms via `pickerPaste`.
  Original finding: Picker inputs only receive `tea.KeyPressMsg`; bracketed paste
  arrives as `tea.PasteMsg`, which has NO route to the picker (only handler is
  the external pane's, ScreenExternal-gated) — the field whose placeholder says
  "paste your Anthropic Console API key" gets nothing; 100+ char key must be
  hand-typed masked. Same gap: label form. Fix: forward PasteMsg to
  `a.picker.input.Update` when open (bubbles textinput handles it natively).
  `account_picker.go:340`, `app.go:422` vs `:788`.
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
- [x] **Descending sort comparator is invalid (MED).** FIXED 2026-07-04
  (`cb0e375`): three-way cmp + sign flip + fixed ID tie-break; DisplayTitle.
  Original finding: SortDesc = `!less`; equal
  keys return true both ways → sort.SliceStable swaps equal-title rows on EVERY
  re-sort (runs per cluster/runner event) — rows visibly ping-pong and the
  row-indexed cursor retargets actions. SortByTitle also has no ID tie-break
  and compares Title not DisplayTitle. Fix: three-way cmp with sign flip, ID
  tie-break in fixed direction. `sort.go:116`, `sort.go:101`.
- [ ] **Archive is a complete no-op (MED).** `A` writes `Archived=true`; zero
  readers repo-wide (not in visibleSessions/grouped/sort/render); flag is
  in-memory only. Fix: filter + archived section, or remove the binding until
  built. `groups.go:200`.
- [x] **Transcript search drops every uppercase letter (MED).** FIXED
  2026-07-04 (`cb0e375`). Original finding: `searchKey`
  requires `key.Mod == 0`, but bubbletea v2's decoder sets ModShift on plain
  typed uppercase (verified against the pinned ultraviolet decoder) — typing
  "TODO" adds nothing, "Readme" yields "eadme". Fix: accept when
  `key.Mod &^ tea.ModShift == 0`, as rename/filter do. `search.go:72`.
- [x] **Search backspace is byte-wise (LOW)** — FIXED 2026-07-04 (`cb0e375`). — corrupts multibyte queries
  (é → dangling 0xC3 → U+FFFD → fuzzy-matching garbage). Fix:
  `utf8.DecodeLastRuneInString` like `model.go:2036/2076`. `search.go:66`.

### 1c. Rendering / layout bugs (2026-07-04 audit, verified)

- [ ] **`hasLinkRefDef` isn't fence-aware → streaming cache permanently disabled
  (MED, perf-critical).** A fenced code line shaped like `[key: string]:
  string` (any TS index signature) anywhere in the message forces resetCache +
  full glamour re-render of the whole buffer on EVERY subsequent delta —
  reinstating the O(deltas²) T1 keystroke-starvation this type exists to kill.
  Fix: reuse prefixHasOpenHazard's fenceInfo tracking (`:233-259`) to skip
  fenced lines. `chat/streaming_markdown.go:443`.
- [ ] **`truncate()` not ANSI-aware (LOW; verifier downgraded from high — the
  dangling-CSI corruption needs specific input, but styled input provably
  reaches it).** Measures with lipgloss.Width, trims raw runes — eats trailing
  SGR reset first, can stop mid-escape. Styled callers: tool-card summaries
  (RemapANSI'd, `transcript.go:1218`), overflow band + boxWithTitle
  (`zones.go:385,122`). Fix: `ansi.Truncate` (already imported in
  transcript.go). `model.go:2636`.
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
  consumer manufactures reconnects. `internal/runner/client.go:234,250`.
- [ ] **Port-forward retries a dead pod forever (MED).** On
  `resolvePodForForward` error the loop keeps the *stale* pod, no terminal
  state — after `sandbox destroy` from another shell, an observer forward
  hammers the vanished pod at ≤10s cadence indefinitely; no "gone forever" vs
  "rescheduling" distinction for the handle owner. `internal/k8s/portforward.go:148`.
- [ ] **Dead-node pods read as Running for minutes (MED).** Both status paths
  trust k8s conditions with no staleness cross-check — node-eviction lag makes
  a crashed session look healthy with a silently-stalled SSE stream.
  `internal/k8s/backend.go:431`, `internal/k8s/watch.go:203`.
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
  session. `internal/k8s/backend.go:873`, `internal/sync/sync.go:62`.
- [ ] **`destroy` gives no active-turn warning though reversible `suspend` does
  (LOW-MED).** `suspend` dials the runner and warns on `ActiveTurnID`;
  irreversible `destroy` prints a generic prompt. `internal/cli/commands.go:168`
  (vs `:97`).

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
  for the §2c numbered-options redesign. `transcript.go:135,1433,2766`.
- [ ] **Clock injection sweep (MED, testability).** nowFunc exists but the
  permission grace gate anchors on time.Now() (`transcript.go:2382`) while
  permissionAnswerable compares nowFunc (`:1591`) — the anti-type-ahead
  behavior is untestable; same for turnStart/toast/status fades. Also:
  test-only counters (reconciles/fpComputes/bdBuilds) live as prod struct
  fields → test-observer interface. `rg 'time\.Now\(\)' internal/tui/dashboard`.
- [ ] **Dedup: markdown-renderer closure ×3 (LOW)** (`transcript.go:1029,2287`,
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
  `transcript.go:2253,631,650,2050,2418,625`.
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
  `app.go:711`.
- [ ] **Fresh Claude session renders a blank body (LOW).** No
  welcome/first-hint block, unlike the dashboard's firstRunView.
  `transcript_list.go:290`.
- [ ] **Failed sessions aren't floated by attention-first sort (LOW).** Only
  Waiting/NeedsInput partition to top. `attention.go:16`.
- [ ] **TUI has no path to a FIRST Anthropic account (LOW).** Zero stored
  accounts skips the account picker entirely, so "＋ add account" is only
  reachable once one exists via `sandbox auth login`. Decide: first-run hint,
  or always enter the account stage with cluster-default + add-account rows.
  `account_picker.go:123`.

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
  back off. `model.go:679`, `sync_support.go:118`, `status.go:42`.
- [ ] Warm-session detail preview re-lays-out + reconciles the *entire*
  retained transcript every frame (no unchanged-guard). `model.go:2253`,
  `transcript.go:2008-2025`.
- [ ] `visibleSessions()` re-filters+re-sorts 4+ times per frame (twice in one
  statement at `groups.go:145`). Memoize per frame. Related: `partition()`
  computed 3× per frame (topBar, clusterStrip, progressState) —
  `zones.go:319`, `model.go:2572`.
- [ ] `bodyView` still ~283µs/frame: `fitModal` does two ANSI `lipgloss.Width`
  scans per visible line every frame. `transcript_list.go:302`.
- [ ] SSE `broadcast()` re-`JSON.stringify`s each event once per client;
  serialize once. `runner/src/events.ts:200`.
- [ ] Streaming-markdown safe-boundary predicates rescan the whole growing
  buffer per delta (O(N²) over a turn). `chat/streaming_markdown.go:111-233`.
  (The §1c fence-awareness bug makes this strictly worse — fix that first.)
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
  Argo/GitOps) so a cold node hits a peer cache. `internal/k8s/backend.go:516`,
  `runner/Dockerfile:59`.
- [ ] **Stop gating the visible prompt on the 12s blocking first-sync flush** —
  open the transcript as soon as the runner is healthy; background the bounded
  flush (reuse the reconnect pattern). Keep *turn submission* gated on staging.
  `internal/cli/connect.go:142-158`, `internal/sync/sync.go:185`.
- [ ] **Parallelize independent serial steps** — Secret+PVC creates (errgroup,
  then Sandbox); the 8 serial `mutagen sync create` execs (only the project
  sync is load-bearing; create the 7 config/transcript syncs lazily); the two
  serial port-forwards (HTTP+SSH). `backend.go:226-260`, `sync.go:131-150`,
  `portforward.go:47`.
- [ ] Tighten `waitForPodReady` poll 2s→~500ms-1s (pod-phase detail to the
  stepper already landed, Phase 2).
- [ ] Defer `ensureReaper` + launch-burst observer connects off the foreground
  connect path; drop the redundant connect-time Status Get + re-`ensureSSHKey`
  on the freshly-created path. `connect.go:93,144,184`.
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

- [ ] CLI `opencode` still lacks `--model` and an initial-prompt arg
  (cancel/suspend-warning correctness landed — see done log).
  `claude_remote.go:23-71`.
- [ ] Verify detach (Ctrl+]) + surrounding chrome behave identically for every
  backend's external pane.
- [ ] **Per-backend CLI smoke.** `internal/k8sit/cli_smoke_test.go`
  `TestCLISmoke` is opencode-only; make it table-driven over `backendCases`
  (gate the non-empty-output assertion on `expectRealReply`) so claude/codex
  fill the column.
- [ ] Should the opencode window feel like a modal over the dash? (design
  decision; inbox echo.)

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
  `internal/session/types.go:132,107`, `client/session.go:62`.
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

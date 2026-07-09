# TODO — backlog

> **How to use this file (agents):** sections are numbered workstreams, ordered
> roughly bugs → strategy → perf → platform. Every item carries `file:line`
> pointers and a fix direction — enough to plan without re-discovery. Pick a
> section (or one bolded cluster), plan the cluster together where the intro
> says so, and when a batch lands: check it off, summarize in one line, move the
> detail to `docs/archive/done-log-2026-07.md` (convention). Provenance docs:
> [`docs/archive/review-2026-06-24.md`](docs/archive/review-2026-06-24.md) (deep review behind
> older items), the 2026-07-01 whole-system review (§1d/§8 intros), and the
> 2026-07-04 multi-agent TUI audit (§1c residuals, §2 — every bug adversarially
> re-verified against source). Done-work history:
> [`docs/archive/done-log-2026-06.md`](docs/archive/done-log-2026-06.md),
> [`done-log-2026-07.md`](docs/archive/done-log-2026-07.md).
>
> **2026-07-06 prune:** everything closed through the 2026-07-06 Fable review
> pass (27/28 approved as-shipped, one `catchingUp` fix; `just check` green,
> zero skipped gates) was removed from this file; residual "STILL OPEN" tails
> were promoted to standalone open items below. Detail lives in the done log.
>
> **2026-07-07 handoff review:** an 8-agent sweep (security ×2, perf ×2,
> test-coverage ×2, runner TS, Go client, event-model, docs, TUI-regression)
> added verified findings across §1d/§1f/§2b/§2c/§4/§10 — all backed by
> [`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) (bracketed ids like
> `[A1]`/`[D1]`/`[H1]` point into it). The 2026-07-07 HIGHs are all closed;
> next by severity: §1f [A2]/[B5-B9], §2b [D3]/[D5]/[D6], §4 [E4].
> *(2026-07-08 batch 1: [A1]+[F1]+[F2]+[C2] landed — done log; A1's `/proc`
> residual is a new §1f item. Batch 2: [D1]+[D2]+[D4]+[B1]+[B2]+[B3]+[B4]
> landed. Batch 3 (2026-07-09): [C1]+[H1]+[H2]/[H3]. Batch 4 (2026-07-09):
> [E1]+[E2]+[E3]+[E5]+[E6] — done log.)*
>
> **Opus-ready map:** §1c–§1d residuals, §2a–§2d, §4, §7a and the §5 GC
> follow-ups carry pointers + fix direction — pick a cluster and go. Drafted,
> awaiting maintainer sign-off: §1e server-side-loop ADR, §7b package-manager
> ADR, §10 KRO ADR, §9 worktree design. Still gated on a maintainer decision:
> §8 (deliberate design calls), the §2d yolo-default + first-account items
> (Fable recommendations recorded inline). Needs the real cluster or live
> services: §5 Spegel deploy, §6 codex spike, §7 verify sweeps, parts of §1d.

## 0) Inbox — human notes, needs triage

Raw maintainer notes. Triage = either promote into a numbered section with
pointers, or answer inline and archive. (Resolved investigations moved to the
done log.)

* Create nix flake (with binary and container outputs?). Is there a place to
  host nix binary cache, maybe tigris? Also consider publishing to FloxHub as
  a public package (via Depot CI). — *note: a flake exists but only packages
  the Go CLI (`flake.nix:20-33`); "container outputs" intersects the §7b
  package-manager ADR (Nix-built OCI is its deferred option 2), the
  binary-cache hosting question is the ADR's §4a substituter decision, and
  FloxHub publishing is the distribution channel on top — proposals for all
  three in
  [`docs/decision-proposals-2026-07-06.md`](docs/archive/decision-proposals-2026-07-06.md)
  §2.3/§2.8. Standing directive recorded 2026-07-06: Flox (preferably) or
  Nix is the preferred install mechanism everywhere in the chain. Triage
  alongside the §7b sign-off.*

## 1) Correctness bugs

§1a (TUI SSE / state-machine cluster) and §1b (group view / sort / search /
pickers) are fully closed — done log. Residuals from §1c live below; the §1b
row-model consolidation moved to §2a where it belongs.

### 1c. Rendering / layout residuals (2026-07-04 audit; parents fixed — done log)

- [ ] **`statusline.go` row-1 segment-join tail can still overflow** — not a
  `spread()`-shaped fix (the shared `spread()` hardening landed); folds into
  the §2c statusline collapse.
- [ ] **Subagent child tool lines still use the old arg≤w/2+summary≤w/3
  budgeting (LOW).** Same latent overflow pattern the §2c two-line card
  redesign fixed by construction for top-level cards. `renderChildTool`,
  `subagent.go`.

### 1d. System reliability (2026-07-01 whole-system review; HIGHs all fixed — see done log)

**2026-07-07 handoff-review additions** — detail in
[`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §C/§H (id in brackets):

- [x] **[H1] observer cap protection fixed — done 2026-07-09** (done log):
  NeedsInput protected only while output is unseen (`lastSeq > seenSeq`);
  Waiting/Failed/attached stay protected.
- [x] **[C1] Close seam for port-forwards — done 2026-07-09** (done log):
  `ConnectResult`/`CreateResult.Close` (→ `sess.Close`) wired through
  `cancelLiveSSE`, ready-msg discards, EventsPassive failure, approve
  fallback, detach (`parkTranscript`), external-pane close, stale-gen ready.
- [x] **[H2/H3] eviction side effects fixed — done 2026-07-09** (done log):
  armed `/loop`/`queuedPrompt` protected; eviction keeps the warm model;
  Busy rows stamped to watch baseline; lapse toast wording cause-agnostic.
- [ ] **Re-create with a different account patches the Secret but not the Sandbox
  env → auth breaks silently (MED) [C3].** `backend.go:391,444-488` vs the
  `AlreadyExists` pod template (`buildEnv:1433-1461`). Patch the pod-template env
  on the AlreadyExists path, or reject a shape-changing re-create.
- [ ] **Assorted client reliability (LOW→MED-LOW) [C4-C11]:** observer connect to
  opencode opens 3 forwards not 1 (switch order, `session.go:286-296`); ssh config
  paths unquoted → spaced state dir breaks all sync (`ssh.go:123,134`); backgrounded
  `CreateInputs`/reaper unbounded → hung mutagen wedges the first turn
  (`session.go:458,506`); rollback can delete a pre-existing PVC (`backend.go:297-307`);
  data race on `Session.projectPath` (`session.go:244-248`, trips `-race`);
  `suspend` probe unbounded ~40s (`commands.go:97`); `models.Limit` sync-fetches
  models.dev inside the reducer, freezes UI ~5s (`models.go:186-227`);
  `IdleTimeout` override no-ops when a reaper already runs (`reaper.go:71-74`).

- [ ] **Concurrent sessions on one project share one local sync endpoint, no
  dedup (LOW-MED).** Mutagen session name keys on SessionID only; two agents on
  the same repo silently cross-feed edits (same-file race → perpetual
  conflicts). `internal/sync/sync.go:202` (`projectName`). Cross-ref §9
  worktrees — per-session worktrees are plausibly the fix; design together.
- [ ] **Mutagen conflict detail in the TUI.** The `SyncConflicted` worst-of
  distinction landed (done log); still open: per-file/side detail + a textual
  resolution hint (needs parsing the mutagen `conflicts[]` JSON shape,
  currently `[]any`).
- [ ] **Transcript sync merges pod-agent history into local `~/.claude`
  unscoped (LOW-MED).** By design (subPath bind), but pod conversations become
  locally `--resume`-able with no tag or audit trail back to the sandbox
  session. `internal/k8s/backend.go:1338-1375` (subPath bind), `internal/sync/sync.go:62`.
- [ ] **Port-forward mid-stream death detection (SMALL, optional).** Terminal
  state + immediate `ErrSessionGone` reconnect-abort landed (done log);
  consuming the literal `ForwardHandle.Done()` channel needs a
  `ConnectResult.ForwardDone` seam through client/cli — only worth it if
  mid-stream (non-reconnect) death detection matters.

### 1e. Autopilot (`/loop`/`/goal`)

The local driver is complete (items 1–5: detach-durable `/goal` continuation,
sentinel termination, lapse toast, idle-reaper interval warn, esc contract —
done log; the item-3 follow-up below is the one loose end).

- [ ] **Record the driver spec in `internal/index` for a one-key re-arm on
  re-attach (SMALL, follow-up to the lapse toast).** A lapsed loop currently
  toasts and is gone; re-arming means retyping the /loop command.
- [ ] **6. Server-side loop — ADR ACCEPTED 2026-07-07, IMPLEMENT.**
  [`docs/server-side-loop-adr.md`](docs/server-side-loop-adr.md) — runner-
  owned driver; all constants settled at sign-off (endpoint
  `PUT/DELETE /sessions/:id/autopilot`; max_iterations default 50;
  token_budget optional, shipped v1; capability bit in `/status`; retry 5×
  backoff max(interval,30s)→5m cap; staleness N=30m; no H4 guard).
  Order: schema (`autopilot.state` + `just gen`) → runner (spec persistence,
  self-submit loop, guards, reaper non-idle, boot re-arm) → TUI (arm/disarm +
  render-from-events, local tea.Tick kept as no-capability fallback) → tests.

### 1f. Security & runner-reliability hardening (2026-07-07 handoff review)

Verified findings from the 8-agent handoff sweep; full detail + exploit/scenario
in [`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §A/§B (id in brackets).

- [ ] **[A1 residual] `RUNNER_TOKEN` still recoverable via `/proc` — uid
  separation needed to truly close self-approval (MED, adversarial review
  2026-07-08).** The A1 env-strip landed (child spawns + the workspace git
  calls all get `sanitizedExecEnv`; done log), but runner and agent child share
  uid 0 (`backend.go:1377`), so `tr '\0' '\n' < /proc/1/environ` recovers the
  bearer token and the runner API is reachable on in-pod localhost
  (`server.ts:77`). Fix: run the agent child as a non-root uid distinct from
  the runner (or mount `/proc` with `hidepid=2`); pod-spec + Dockerfile work,
  coordinate with the §7b base-image spike. Until then A1 is
  raised-bar-not-closed; comments in `claude.ts` say so.
- [ ] **Migrate the PreToolUse block result off the legacy `decision:'block'`
  shape (LOW, forward-compat).** The SDK also exposes
  `hookSpecificOutput.permissionDecision:'deny'`; if a future SDK bump drops the
  legacy path, Bash enforcement silently dies while the F2 tests stay green
  (they pin what we return, not what the SDK honors). `claude.ts:349-354`,
  `runner/test/pretooluse-guard.test.ts`. Consider pinning the SDK version
  (cross-ref the carry-forward caveat below).
- [ ] **Event log + SSE persist secrets verbatim, unlike the redacted audit log
  (LOW-MED) [A2].** `events.ts:191-224` `appendEvent` writes/broadcasts raw
  payloads (prompts, Bash commands, tool inputs) with no `redactSecrets`
  (contrast `audit.ts`). Factor redaction into a shared module; run it in
  `appendEvent` for `turn.started`/`tool.*`/`permission.*`.
- [ ] **SECURITY.md: 0.0.0.0 binds + open-443 egress example (INFO) [A3].**
  Runner/sshd/opencode bind all interfaces (`server.ts:77` etc.);
  `networkpolicy-egress-allow.yaml:48-59` permits 443 to any public host — the
  exfil channel behind A1/A2. Document prominently; point at FQDN-scoped egress.
- [x] **[B1] opencode `serve` spawn-error listener — done 2026-07-08** (done
  log): `'error'` + `'exit'` share one per-child respawn scheduler.
- [x] **[B2] 409 gate covers observer-synthetic opencode busy — done
  2026-07-08** (done log): pure `turnRejectReason` in `server.ts`
  (a first bite of [F4]).
- [x] **[B3] /exec resolves at bash exit + process-group SIGKILL — done
  2026-07-08** (done log): `detached:true`, resolve on `'exit'`,
  `kill(-pid)` on timeout.
- [x] **[B4] persist-failure events delivered live — done 2026-07-08** (done
  log): seq-0 bypasses the `<=afterSeq` filter (`shouldDeliver`).
- [ ] **Assorted runner robustness (LOW) [B5-B9]:** `after > lastSeq` accepted →
  silent live-event swallow (clamp, `server.ts:127-130`); synchronous git in
  `emitWorkspaceStatus` blocks the loop ~9s/turn (`claude.ts:857-895`, async it);
  corrupt `session.json` crash-loops the pod (`session.ts:135-137`, catch + move
  aside); permission-resolve races the deadline and lies `resolved:true`
  (`server.ts:233-251`); oversized/malformed body → 500 not 413/400
  (`httputil.ts:14-26`).

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

Block unification, the one-event-reducer, and the mechanical god-file split
all landed (done log) — the items below are what remains.

- [ ] **Declarative vertical layout regions (HIGH).** Stack arithmetic
  hand-counted in 4+ places (layout(), renderTranscript(), previewView
  `h-3-bannerH`, scrollbarDragTo `bodyTop=2`, App.modalRect) — any layout
  change (status-line move, header removal, inline perm prompts) means finding
  every copy; mouse hit-testing silently breaks. Fix: one per-frame
  `[]region{name, height, render}` with body as flex; all consumers walk it.
  `transcript.go:882`.
- [ ] **Consolidate the two dashboard row models (from §1b).**
  `visibleSessions()` vs `visibleRows()` both interpret `m.cursor`; one row
  abstraction with a `sessionAt(cursor)` accessor for render+nav+actions.
  The S15 archived-section design pointer lives in the `groups.go` header
  comment. `groups.go:57`.
- [ ] **App.Update flat dispatch + one detachTranscript() (MED).** 450-line
  screen-router; detach sequence duplicated 4×; recursive
  `a.Update(*msg.ready)` re-entry (`app.go:676,691`); B17 single-delegation
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
- [~] **Clock injection sweep** — dashboard-package clocks all on `nowFunc`
  (grace gate, turn elapsed, toast lifecycle, motion loop, transitions), with
  clock-swap tests; Fable-approved 2026-07-06. DEFERRED: (a) `statusChangedAt`
  assignments + `lastSnapSave` were parked as §1a territory — §1a is now
  closed, so these are unblocked; (b) `tui/theme.FadeColor` computes elapsed
  internally (`tui/theme/styles.go`) — public §8 surface change; (c) test-only
  counters (`reconciles`/`fpComputes`/`bdBuilds`) → observer interface.

### 2b. Event-model parity gaps (schema → mapper → renderer)

Which Claude Code UI capabilities the pipeline can't represent / doesn't map /
doesn't render. These cap how Claude-Code-like ANY client can feel. Schema
changes go through `schema/events.json` + `just gen` (never hand-edit `*.gen.*`).
(Numbering preserved from the audit; gap 4 — compaction — landed, done log.)

- [ ] **1. Subagent output flattens into the main transcript (correctness
  bug).** `MessagePayload` has no `parentToolUseId`
  (`schema/events.json:88-95`); `handleStreamEvent` receives it but only
  attaches it to `tool_use`, never text/thinking deltas
  (`runner/src/mapping.ts:110-114,249-253`) — a running Task's narration
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

**2026-07-07 parity-audit additions** (detail in
[`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §D; id in brackets):

- [x] **[D1] tool completion id-matched + turn-boundary drain — done
  2026-07-08** (done log): `finishToolCard` closes by `flatTools[toolUseId]`
  first (FIFO only as the id-less fallback); `drainPendingTools` runs in all
  three turn-terminal handlers.
- [x] **[D2] mid-turn crash boot terminal events — done 2026-07-08** (done
  log): boot appends `turn.interrupted` + `status_changed{idle}` for the
  orphaned turn before `session.started` (`orphanedTurnBootEvents`).
- [ ] **All four `turn.*` payloads are off-schema → invisible to the drift gate
  (MED) [D3].** `turn.started/completed/failed/interrupted` carry fields absent
  from `schema/events.json`; the Go TUI decodes `turn.failed` via a coincidental
  shared `message` key. Add Turn payloads to the schema + `just gen` (unblocks D5).
- [x] **[D4] interrupt mid-think reasoning teardown — done 2026-07-08** (done
  log): `finalizeStreaming` resets `m.reasoning`/`reasoningBuf`.
- [ ] **OpenCode transcripts have no user prompts on attach/replay (MED) [D5].**
  Adapter/observer map only assistant parts; the prompt lives only in the
  off-schema `turn.started`. Emit `message.*  role:user`, or render
  `TurnStartedPayload.prompt` once D3 lands.
- [ ] **`tool.delta` carries no `toolUseId`/`parentToolUseId` → live input preview
  attaches to the wrong card (MED) [D6].** `mapping.ts:295-297` drops the
  in-scope `parentToolUseId`; the TUI targets the newest pending flat card. Thread
  the ids and target by id. Distinct from gap 1 (text/thinking deltas).
- [ ] **Event-model LOW residuals [D7-D12]:** hook-blocked tools emit two
  `tool.failed` (double FIFO pop, `claude.ts:302-306`); runner omits the schema's
  `required` `ToolPayload.tool` and never emits schematized `exitCode`
  (§2c exit-code plumbing); `session.State.Status` dual vocabulary in the public
  SDK (`client.go:174-188` vs `types.go:156-163`, cross-ref §8); `TurnInput`
  mirror drift (`Advisor` absent TS-side; `Resume` mistyped as `TurnID`);
  pre-cycle `session.error` + retitle-during-headless-turn invisible
  (`opencode-observer.ts:142-153,210`); usage double-emit + cache-token/ctx% edge
  (`mapping.ts:321-366`, `readmodel.go:145-149`).

### 2c. Design/layout changes (renderer)

Deduped against `docs/archive/ux-polish-plan.md` — nothing below is already committed
there. HIGH items are the at-a-glance tells; most are renderer-local.

- [ ] **Tool-card follow-ups** (the two-line ⏺/⎿ idiom + ctrl+o expansion
  landed — done log): per-card focus/expand for older cards
  (space/toggleSubagents has the same latest-only gap); `⎿ exit 0 · 42 lines`
  combo needs exit-code plumbing (§2b gap 5). **2026-07-07 regression residuals
  from 9e1d03b** (detail [`docs/review-2026-07-07.md`](docs/review-2026-07-07.md)
  §H): expanded output renders unsanitized `\r`/non-SGR control sequences into the
  frame → smears the transcript (MED, new exposure; `transcript_render.go:536-560`,
  strip in `clampOutputLines`/`toolExpandBody`) [H4]; tabs measure width 0 but
  expand to 8 cols → expanded Go diffs overflow the budget
  (`transcript_render.go:552`, `permission_diff.go:52`) [H5]; opencode tool output
  is uncapped (`opencode-turn.ts:296`, cap only on the claude path
  `mapping.ts:222`) [H6]; ctrl+o toggles content-less cards with no feedback +
  strands `expanded=true` (`transcript_reduce.go:98-110`) [H7].
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
  `model.go:1817,1866`. (Fix alongside the §2a input-context tables.)
- [ ] **External-pane nav keys — DECIDED 2026-07-07: leader-chord,
  IMPLEMENT with the §2a binding tables.** Reserve NEITHER ctrl+g nor ctrl+k
  (both keep forwarding to the embedded client, `app.go` ScreenExternal);
  instead extend the already-reserved detach key into a leader inside
  external panes: `ctrl+]` then `g`/`k` = next/prev-attention, `ctrl+]`
  `ctrl+]` (or timeout) = detach as today. One reserved prefix scales to
  codex and any future backend. (Dashboard-side `NextAttention` binding
  already landed 2026-07-06.)
- [ ] **First-account path — DECIDED 2026-07-07: always enter the account
  stage, IMPLEMENT.** Zero stored accounts currently skips the picker
  entirely (`account_picker.go:123`); change it to always show the stage
  with "cluster default" + "＋ add account" rows. Also the natural home for
  the §6 launch-preflight reauth stage — build the stage machinery once.
- [ ] **Yolo default — DECIDED 2026-07-07: yes, IMPLEMENT.** Flip the
  runner's empty/unknown-mode default from `acceptEdits` to
  `bypassPermissions` (`runner/src/claude.ts:70-81`) so every client
  inherits it; the SDK safety gate is already wired (`IS_SANDBOX=1` +
  `allowDangerouslySkipPermissions`, `claude.ts:216-229`). REQUIRED in the
  same change: statusline surfaces the active mode so yolo is never
  invisible (`statusline.go`). Cost brake for unattended runs = the §1e
  autopilot guards (max_iterations 50 / token_budget), signed off same day.
  Cross-ref §8's mode-enum abstraction (6.2) — flip the default now, don't
  wait for the enum.

### 2e. Premium-feel program (2026-07-07 Crush/ecosystem research)

Design detail lives in [`docs/tui-premium-plan.md`](docs/tui-premium-plan.md)
(draft, awaiting sign-off) — five-agent comparative study of Crush
(FSL: **ideas only, never copy code**), ultraviolet, gh-dash, huh (all MIT).
Items below are the plan's workstreams; the doc carries mechanics, license
rules, and sequencing. Complements — does not supersede — §2a/§2c.

- [ ] **A. Dialog stack manager + async grace period + huh for form dialogs**
  — one `Dialog` interface + stack on App replaces ~8 bespoke overlays and 4
  copies of center/shadow math (`model_render.go:122-166`, `app.go:1009`,
  `app.go:1137`, `backend_picker.go:211`); 200ms/1.5s/500ms grace kills the
  async-permission blind-approve class. Plan §A.
- [ ] **A4. Input coalescing** — no `tea.WithFilter` today; 16ms wheel/motion
  throttle with sign-aware delta summation. Plan §A4.
- [ ] **B. Transcript depth** — per-tool body dispatch (grow
  `toolExpandBody`, `transcript_render.go:524`), per-card
  selection/expansion (today global-latest only,
  `transcript_input.go:236-241`; unlocks per-subagent collapse + per-item
  copy), three-state thinking view (slice AFTER glamour render), go-udiff +
  chroma diffview replacing the hand-rolled LCS
  (`permission_diff.go:228-277`), lipgloss/tree subagent cards (also retires
  the §1c child-line budget residual). Plan §B.
- [ ] **C. gh-dash lifts (MIT, same charm v2 stack)** — async action task
  queue (start/finish/error + `[⟳ N]` badge + 2s auto-clear in the
  statusline) and the fixed+Grow table/column engine for the session list.
  Plan §C.
- [ ] **D. Motion & chrome** — scrambled-glyph gradient thinking shimmer
  (deterministic staggered fade-in, frame cache; honor
  `SANDBOX_REDUCE_MOTION`), `v.WindowTitle` + `ReportFocus` + native
  progress bar (ghostty keep-alive quirk), composer micro-UX (prompt
  history ↑/↓ with draft preservation, paste-to-attachment, randomized
  placeholders). Plan §D.
- [ ] **E. Theming: iTerm scheme import + /theme picker** — vendor ~12
  curated schemes from mbadolato/iTerm2-Color-Schemes (MIT) in the ghostty
  `key=value` export format, `just gen-themes` → `schemes.gen.go`,
  `Derive()` maps 22 scheme colors → semantic tokens (perceptual blends +
  contrast-floor CI test; imported themes keep their own ANSI-16 for
  authentic tool output), `/theme` picker with live preview + persisted
  choice (`SANDBOX_THEME` env > saved > auto). `tui/theme/theme.go:290-317`
  already stubs the hooks. Plan §E.
- [ ] **F. Ultraviolet phase 1–2 (ADR first)** — bubbletea v2 already
  renders through uv; composing cells ourselves deletes the
  `withBackground`/`bgSeq`/`clampLines` opacity machinery
  (`zones.go:50-105`) and collapses the dual overlay systems. Does NOT fix
  tea.Raw/Kitty (already correct) or child resize seeding. Plan §F.
- [ ] **G. Capability probing + notification backend selection (LOW)** —
  allowlist-gated DA1/XTVERSION/pixel/Kitty/OSC99 probe burst; notification
  escalation (native/OSC99/OSC777/bell) with focus suppression. Plan §G.

## 3) Decision record — Claude Code as the local client (SETTLED 2026-07-04)

Three-track research (official surface, community art, repo feasibility) into
using Claude Code **directly** as the client for a remote sandbox session.
Outcome: **not happening; invest in §2 instead.** Kept here so nobody re-treads.
(The supported `claude --resume` escape hatch is documented in README —
done log.)

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
- Also evaluated and rejected: SSHFS mounts (per-file-op RTT),
  MCP-ssh-tools-with-built-ins-denied (token-expensive file ops, model drifts
  back to native tools), dev containers (local isolation only), web teleport
  (web→local only).

## 4) Performance

- [ ] Warm-session detail preview re-renders the retained transcript tail
  every frame (no unchanged-guard). Re-verified 2026-07-04: it renders via
  `tr.tailLines(5, width)` (bounded), so cost is lower than originally
  claimed — measure before optimizing. `model.go:2537`, `transcript.go:2113`.
- [ ] **`visibleSessions()` re-filters+re-sorts 4+ times per frame**
  (`groups.go`) — measure-first; memoize only if profiling shows it matters.
  (The `partition()` render-path dedup itself landed — done log.)
- [ ] `bodyView` still ~283µs/frame: `fitModal` does two ANSI `lipgloss.Width`
  scans per visible line every frame. `transcript_list.go:302`.
- [ ] **`lastCompleteBlock` still rescans per block-boundary crossing** —
  O(blocks·N), a smaller term now that the incremental `mdScanner` landed
  (done log); measure before touching.
- [ ] Glamour pads wrapped lines with per-space SGR runs (bytes; upstream
  glamour style; inflates parse work).

**2026-07-07 perf-review additions** (two agents; ✓ = both flagged it; detail in
[`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §E, id in brackets):

- [x] **[E1] tool.delta O(n²) hot path fixed — done 2026-07-09** (done log):
  Builder accumulation, eager-under-2KB-then-every-+2KB parse throttle,
  per-delta `Bump()` instead of `syncItems()`.
- [x] **[E2] SSE replay streams in bounded chunks — done 2026-07-09** (done
  log): 512-row chunks + raw-payload frame splice + drain-aware yields; the
  `replaying` flag + synchronous handoff preserve the in-order/no-dup/
  replay-complete contract.
- [x] **[E3] live-broadcast backpressure cap — done 2026-07-09** (done log):
  4 MiB `writableLength` cap; a wedged client is destroyed and reconnects
  with `after=<seq>`.
- [ ] **Delta events persisted forever; log unbounded by default (MED ✓) [E4].**
  Delta-only compaction on `turn.completed` (delete `*.delta` older than the last
  N turns — seq gaps are fine, remaining events stay contiguous). Not M34's
  rejected all-or-nothing retention.
- [x] **[E5] passive streams batch-drain — done 2026-07-09** (done log):
  `liveSSEBatchCmd` + `RunnerEventBatchMsg` mirror the foreground 512-drain;
  one Update+View per burst.
- [x] **[E6] live reasoning wrap is incremental — done 2026-07-09** (done
  log): complete-lines prefix cache keyed by width+theme epoch; only the
  trailing partial re-wraps per frame.
- [ ] **Streaming tail re-hashes + copies the whole buffer per delta (MED-LOW)
  [E7].** `transcript_list.go:240-254` `fnv(entire buf)` + string→[]byte copy;
  running-FNV over delta bytes, or key on `buf.Len()` (monotonic).
- [ ] **LOW perf [E8-E10]:** Go SSE consumer double-copies each line
  (`client.go:403-422`, use `scanner.Bytes()`); `appendEvent` re-prepares its
  INSERT per event (✓, `events.ts:201-207`, prepare once); host event-cache
  reopens the ndjson file per cached event + no size cap
  (`sync_support.go:252-258`, hold an open handle + cap the tail).

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
  backend; inbox 2026-07). Cross-ref the §7b ADR — its Flox layer is designed
  as the shared base of this split.
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
`codex-app-server` reserved (`internal/session/types.go:63`). Auth =
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
  NOTE (from the landed §7a injection work): `resolveOpencodeProvider`
  silently defaults unrecognized values to Anthropic — the future
  `CreateOptions` selector must validate, not default.
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
     cascades. Cross-ref the §10 KRO ADR, which recommends exactly this over
     adopting kro.
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
- [x] **Codex transport spike — COMPLETE (2026-07-06, containerized).**
  `codex app-server --listen ws://127.0.0.1:PORT` (fixed port, loopback, no
  auth needed, `/readyz`+`/healthz` free) on the PLAIN npm build — standalone
  install NOT needed (managed daemon = cloud-relay pairing, not our
  transport); 2nd-client observer CONFIRMED (notification broadcast +
  `thread/read`; key on notifications, not `thread/list`). Full results in
  the plan's "Spike results (2026-07-06)". Residual for Phase 2: authed live
  turn-observe + refresh-ownership decision.
- [ ] Codex runner-as-metrics-observer (same pattern as opencode's, app-server
  thread notifications).

## 7) Cross-backend parity (operational)

**Parity bar (maintainer 2026-06-24):** startup speed, detach + keybindings,
prompt/affordance UX, and surfaced metrics must be similar across all agents;
per-agent in-pane rendering can differ. The runner is the control plane and
metrics source for every backend. See the codex plan "Parity bar".

### 7a. OpenCode auth persistence / validation (2026-07-04 triage)

*(A full task-by-task implementation plan exists at
`docs/superpowers/plans/2026-07-04-opencode-credential-manager.md` — local-only,
gitignored; the decisions below are self-sufficient without it.)*

**Fable review (2026-07-04): direction approved — Opus-executable in the
order listed.** Landed so far (done log): selected-provider-only fail-closed
key injection, freshness/rotation stamps, secret-handling + reaper-RBAC
hardening, README auth section. Decisions that still govern the open items:
**(1)** do NOT build an OpenCode-specific store — generalize `client/cred`
with a provider dimension first (that is §6's write-side item) and make item
1 below consume it. **(2)** The connect preflight bar (item 2) is
Secret-presence + key-shape for the *selected* provider, fail-closed; live
provider/model pings belong behind `sandbox auth status --check`, never on
the connect path. The cross-backend contract these decisions implement
(preflight → reauth → local store → per-session Secret → GC, one provider per
Secret) is §6's "Unified per-backend credential lifecycle" item — read that
first.

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
- [ ] (SMALL) Namespace the remaining Sandbox/pod ClusterRole grants — the
  reaper `secrets: get` move to a namespaced Role landed; the follow-up is
  noted in the k8s manifest.

### 7b. Flox/Nix-first runner environment (2026-07-04 triage)

**ADR ACCEPTED WITH AMENDMENT 2026-07-07** —
[`docs/runner-package-manager-adr.md`](docs/runner-package-manager-adr.md):
**first spike `ghcr.io/flox/flox` as the base image** (everything above the
OS from one pinned Flox env — node, sshd, sqlite, opencode; flox ≥ 1.13 for
`flox run`; acceptance gate = Depot build + sshd/PVC host keys +
better-sqlite3 compile + kind conformance), falling back to the ADR's
Debian+Flox-layer option only if the spike hits a wall. Env/mount seam,
substituters (home = ceph S3 w/ egress CIDR carve-out; Tigris = public/OSS
cache), re-sign-at-publish-gate, no shared /nix mount in pass 1, age-based
pruning all stand as written. Nix-built OCI = pass 2 via flake container
outputs; FloxHub CLI publish via Depot CI can land independently. Decided
regardless: the activation hook's `go get .`
(`.flox/env/manifest.toml:54-60`) goes — it mutates go.mod/module cache as a
side effect of `cd`.

- [ ] **Spike the flox-base image, then implement the rollout** (items below
  are the ADR's work breakdown, kept for pointers):
  - Runtime bootstrap env/mount seam: extend the common pod env
    (`internal/k8s/backend.go:1244-1277`) with package-manager preference,
    cache dirs, binary-cache config, optional `/nix`/Flox mounts, preserving
    the existing `/session` PVC + SSH mounts (`backend.go:1185-1241`).
  - Propagate Flox/Nix preference to agent child processes: Claude's explicit
    env map (`runner/src/claude.ts:213-231`); OpenCode inherits env
    (`runner/src/opencode.ts:248-253`); inject PATH/cache/config env + agent
    guidance (prefer project Flox env → `flox run` → `nix run nixpkgs#…`).
  - Update runner-image CI triggers (`.depot/workflows/build-runner-image.yml:12-20`
    — build context is `runner/`, root-level `.flox`/`flake.nix` are outside
    it) and host-tool checks (`opencode attach` needs host `opencode`,
    `claude setup-token` needs host `claude`; package in Flox or `just
    doctor` reports the gap).
  - Kubernetes Nix/Flox cache strategy: baked closures first; `NIX_CONFIG`
    substituters/trusted keys via the env seam; egress allowlist opening;
    anti-poisoning publish gate (follow-on design) + pruning story.
  - Remove the `go get .` activation hook (can land first, independent).

### 7c. OpenCode operational items

- [ ] CLI `opencode` still lacks `--model`, an initial-prompt arg, and a
  `--provider` flag (cancel/suspend-warning correctness landed — see done
  log). `claude_remote.go:23-71`. NOTE: the SDK half of provider selection
  landed 2026-07-06 — `CreateOptions.OpencodeProvider`, validated fail-closed
  (`ErrInvalidOpencodeProvider`, `client/client.go`) — the CLI flag just
  threads it through `runStartSession`.
- [ ] Verify detach (Ctrl+]) + surrounding chrome behave identically for every
  backend's external pane.
- [~] **Live-session verify sweep — opencode (2026-07-06 headless pass on
  omni-prod, zen provider, free big-pickle):** (a) **busy/idle status:
  CONFIRMED live** — `session.status_changed busy→idle` streams at turn
  boundaries. **Title: NOT verifiable headless** — the turn adapter creates
  opencode sessions with an explicit placeholder title
  (`opencode-turn.ts:487,649`), and no `session.title` event fired within
  ~60s post-turn; opencode's auto-retitle may be skipped for pre-titled
  sessions. STILL OPEN: verify title via the real TUI path (opencode-created
  session through `opencode attach`) — maintainer eyeball, or investigate
  whether the adapter should create sessions WITHOUT a title so retitle
  fires. (b) clickable spots — still needs interactive TUI eyeball, not
  automatable headless.
- [ ] (LOW, pre-existing) `markObservedTurnInterrupted(id)` for an id that
  never becomes the active turn cycle leaks its entry
  (`runner/src/opencode-observer.ts:120`). (The `reset()` GC half landed —
  done log.)
- [ ] **Reasoning re-emitted as a trailing `message.completed` (opencode turn
  adapter, live 2026-07-06).** Observed on omni-prod (big-pickle, reasoning
  model): the reasoning text streams as `message.delta`s → `reasoning.completed`
  → the REAL answer streams + `message.completed` → then a SECOND
  `message.completed` carrying the reasoning text again (seq 41 vs 38 in the
  capture) — a double-render in any client that appends per
  `message.completed`. Likely the adapter/observer flushes the reasoning part
  as a message at turn end. `runner/src/opencode-turn.ts` (part→event
  mapping), cross-ref §2b gap 3 (thinking presentation).
- [ ] **Diagnose live: opencode looks stuck after disconnect/reconnect
  (maintainer report 2026-07-04; recipe below). 2026-07-06 live probes
  (omni-prod) EXONERATED the event layer:** (i) SSE dropped mid-flight
  during a 45s bash tool → the turn ran to completion server-side and
  reconnect with `after=<seq>` replayed contiguously incl. `tool.completed`,
  `turn.completed`, and the `idle` status flip
  (`firstReplay=dropSeq+1, contiguous=true` — three separate runs); (ii) an
  idle SSE stream survived a 6-min window on 30s heartbeats (90s watchdog
  never fired); (iii) the 409 active-turn gate behaved correctly under
  client churn. So a "stuck" display after reconnect is NOT missing replay —
  remaining candidates narrow to (1) provider rate-limit/retry invisibility
  under real load and (3) upstream `opencode attach` rendering a stale
  in-flight tool (our PTY mirrors its bytes). Capture recipe below still
  applies at next natural occurrence.
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
- [x] **OpenCode window as modal over the dash — DECIDED 2026-07-07: no.**
  Full-screen external pane stays (modal PTY = constant reflow churn on a
  client we don't control + "whose chrome wins" ambiguity); parity
  investment goes to identical detach chrome + status strip instead (the
  verify item above).

## 8) Public SDK / client API — ALL DECIDED 2026-07-07, now an implementation backlog

Decisions from the live proposal review (archive/decision-proposals-2026-07-06.md §6).
Breaking changes OK pre-OSS; update `sdktest/` pins in the same change.
Suggested batching: one tui/* PR (Register + palette + Finished + B-tier);
one client-behavior PR (destroy ordering + DialRunner); the interface,
naming-break, and Shell items each stand alone.

- [ ] **Narrow public `client.Backend` interface** (exactly the methods
  Create/Connect/Destroy orchestration uses; `internal/k8s.Backend`
  satisfies it; `WithBackend` takes the interface) — enables fake-injection
  unit tests for orchestration (zero coverage today) and the seam a future
  non-k8s backend needs. Pin in sdktest. `client/client.go:141,150`.
- [ ] **De-Claude the turn/state model in ONE coordinated break:**
  `TurnInput.Mode` → owned `ApprovalPolicy` enum (mapped per-backend in the
  runner; non-honoring backends documented, not silent);
  `Connection.Opencode` → generic `Connection.External` (codex reuses the
  shape); `State.ClaudeSession` → `State.AgentSessionID` (one backend per
  session ⇒ one resume id). `internal/session/types.go:175-178`, `:144-153`,
  `client/session.go:62`. Cross-ref the §2d yolo flip (mode default changes
  runner-side independently).
- [ ] **`Session.Shell` in the SDK (maintainer call — full interactive
  shell, not just a primitive).** SDK gains the one-call interactive shell
  (PTY handling included); `internal/cli/shell.go` becomes a thin wrapper.
  Build it atop an ssh-target seam so non-interactive consumers can still
  exec. Pin in sdktest.
- [ ] tui/theme: add `Register(Theme)` (doc promise exists) + export the
  missing `Denied/Info/Success/Warning` active tone vars.
  `tui/theme/theme.go:63,107-144`.
- [ ] tui/kit: palette race → `atomic.Pointer` swap (two tea.Programs must
  not share a plain map). `tui/kit/style.go:21`, `tui/kit/components.go:32`.
- [ ] tui/list: drop dead `Item.Finished()`. `tui/list/list.go:12`.
- [ ] client: `Destroy` stops sync BEFORE the cluster destroy (mirror the
  TUI's PreDestroyHook ordering in the SDK). `client/client.go`.
- [ ] client: `DialRunner` stops forwarding the unused SSH port.
  `client/client.go`.
- [ ] kit.FormatTokens gains the `B` tier (caps at "1000M" today).
  `tui/kit/style.go`.
- [x] WithStateDir ssh-dir layout — DECIDED via the §9 worktree sign-off
  (4.10): move `ssh/` INSIDE the state root in the same pre-OSS break that
  adds the worktree root; implement with the worktree Spec-split change.
  `client/sync.go`.

## 9) Unbuilt features

- [ ] **T10 — working-directory picker** (only unexecuted superpowers plan;
  `docs/superpowers/plans/2026-06-22-t10-working-dir-picker.md` — NOTE:
  `docs/superpowers/` is local-only, ignored by the maintainer's global
  gitignore; the item text here is self-sufficient): dirPicker
  overlay end-to-end — `dirpicker_path.go` (~-expansion, child listing,
  longest-common-prefix completion, validation) + overlay struct (open/close,
  prefill, Tab, recents) + wiring before the backend picker + thread
  `projectPath` into the Creator. None exists.
- [ ] **Tekken-style agent-picker modal** — animations + per-agent
  ascii/ansi portrait.
- [ ] **Per-session git worktree lifecycle — design ACCEPTED 2026-07-07,
  IMPLEMENT.** [`docs/worktree-lifecycle-design.md`](docs/worktree-lifecycle-design.md)
  — all 10 open questions resolved in its Status block (headlines:
  `Spec.WorkspacePath` split; worktree-keyed transcripts accepted;
  `WorktreeAuto` default; WIP-commit on dirty destroy; **TUI owns the
  branch-name proposal prompt** — SDK ships only the deterministic
  `ConvertToBranch`; cross-machine = B1 only; worktree root under the state
  dir + `ssh/` moves inside it in the same pre-OSS break, resolving the §8
  WithStateDir item). Implementation order: Spec split → auto-worktree at
  Create → capture-then-remove teardown/reap (`ReapWorktrees`, destroy hook)
  → `ConvertToBranch` → TUI proposal prompt + confirm modal + `sandbox
  worktree gc`. SDK-first; update sdktest pins in-change. Fixes §1d's sync
  collision for git projects (distinct mutagen alphas).

## 10) Harness / tests / docs / ops

**2026-07-07 test-coverage additions** (two agents; detail in
[`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §F, id in brackets):

- [ ] **Client orchestration incl. the planned `Destroy` reorder is 0% covered
  (HIGH) [F3].** All of `client/session.go` runtime + `client/client.go:411-469`
  + `client/sync.go`. §8's `Destroy` sync-before-destroy flip has no regression
  net. Unblocked once §8 narrows `Backend`: fake-backed table test per method +
  a `Destroy` call-order spy.
- [ ] **`server.ts` HTTP layer dark; "e2e" is fake-on-both-sides (HIGH) [F4].**
  Route dispatch, bearer enforcement, 409 gate, SSE `after=` untested runner-side;
  `internal/e2e`'s fake runner faithfulness to `server.ts` is unchecked. Add a
  `node:test` suite booting `createServer` (401/404/409/replay).
- [ ] **Port-forward lifecycle 0% covered (HIGH) [F5].** `portforward.go`
  retry loop + `Done` signaling (site of C1 and the §1d `ForwardDone` seam).
  Extract the retry decision to a pure func + table-test; unit-test `Done` closes
  once.
- [ ] **MED coverage [F6/F7]:** opencode cred-rotation warning
  (`backend.go:1530-1584`) + the §7c opencode double-`message.completed`/observer
  leak have no pinning test; `waitHealthy` (`connect.go:41-66`), `reaperTick`
  (`reap.go`, a regression silently stops fleet idle-suspend), and the dashboard
  `model_sse.go` command closures execute in no test (only their reduced messages
  do). Copy the `reap.go` pure-decision split pattern.

- [ ] **`docs/runner-api.md` shape gaps** (2026-07-07 docs sweep): `/healthz`
  body `{status,protocolVersion}` undocumented (consumed by `client.go:97`);
  `POST /turns` 409s (concurrent-turn + supervise-only) undocumented; interrupt
  empty-turn-segment (`/turns//interrupt` = active turn) undocumented. README
  omits `sandbox auth logout`, `auth status`, `sync … gc`. `LAUNCH-CHECKLIST.md`
  "whole tree is uncommitted WIP (no HEAD)" is false; `HARDENING-BACKLOG.md`
  C*/M*/BR* items live outside the TODO single-backlog protocol (fold in or keep
  as a referenced provenance backlog).
- [ ] Ops: new CLI-created sessions use `:latest` and can hit the stale traefik
  manifest cache — bust the cache or pin digests CLI-side. (Resume path
  already fixed via digest pinning — see done log.)
- [ ] **End-user host setup: `sandbox doctor` (promoted from inbox
  2026-07-04; the "AICR" acronym is resolved — separate item below).** A
  first-run check/setup path so the CLI "just works" on a fresh host:
  kubeconfig + context, mutagen binary, ssh, runner/reaper image refs,
  credential store. `just doctor` today only validates the Flox *dev* env
  (`justfile:243-271`), nothing exists for an end user of the `sandbox`
  binary itself (`sandbox doctor`?).
- [ ] **Research: NVIDIA AICR (github.com/nvidia/aicr) home use cases
  (maintainer, expanded 2026-07-07).** Maintainer works on AICR at work
  (GPU-cluster focus) and wants to find homelab use cases for components
  that require multiple pieces of configuration synced up together.
  Candidate fits worth evaluating: the per-session Secret+PVC+Sandbox trio
  (note the KRO decision — [`docs/archive/kro-composite-adr.md`](docs/archive/kro-composite-adr.md)
  rejected adding a controller dependency for this; the same bar applies),
  the §7b substituter/egress/cache config bundle, and non-sandbox homelab
  components. Research note only; no code until a concrete use case earns
  it.
- [x] **KRO — DECIDED 2026-07-07: not adopting.** ADR archived
  ([`docs/archive/kro-composite-adr.md`](docs/archive/kro-composite-adr.md));
  the §6 item-3 ownerReferences fix (Secret+PVC → Sandbox) is now unblocked
  and immediately executable (~10 lines in `CreateSession`).
- [~] **Observability first cut landed** — dependency-free spans behind
  `SANDBOX_TRACE=1` / `sandbox --trace`: connect/create phases incl. the
  backgrounded flush/inputs/reaper under one correlation id (`client/trace.go`);
  runner turn lifecycle (first message / first delta / settled + msg count,
  `runner/src/trace.ts`). Fable-verified 2026-07-06. NEXT (not done):
  correlate CLI connect id ↔ runner turn id across the HTTP seam; runner
  startup spans (index.ts); pod-ready sub-phases (schedule vs pull vs ready
  — the big §5 unknown); SSE first-event latency. Also: `SANDBOX_TRACE` and
  the §1d observer-cap model are absent from `docs/architecture.md` — update
  the durable ref (doc drift, found in the 2026-07-06 harness audit).

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

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
> `[A1]`/`[D1]`/`[H1]` point into it). The 2026-07-07 sweep is nearly
> burned down — remaining: §1f the A1 `/proc` residual + the hook-shape
> forward-compat item + [A3] SECURITY.md; §2b [D7-D12]; §4 [E7-E10] + the
> older measure-first items; §10 [F3-F7].
> *(Batches 1-6, 2026-07-08→09: [A1] [F1] [F2] [C2] · [D1] [D2] [D4]
> [B1-B4] · [C1] [H1] [H2/H3] · [E1] [E2] [E3] [E5] [E6] · [A2] [B5-B9]
> [D3] [D5] [E4] · [H4-H7] [D6] [C3-C11] — all in the done log.)*
>
> **2026-07-18 audit:** a 16-subsystem audit produced 47 verified findings
> ([`docs/audit-2026-07-18.md`](docs/audit-2026-07-18.md), ids `[V1]`-`[V47]`)
> — ALL burned down same day across seven commits (per-finding verdicts in the
> doc; detail in the done log). Residuals promoted into sections: §2b
> AskUserQuestion answer flow ([V15]), §5 dashboard paused-orphan reap
> ([V35]). STILL UNCOVERED: 6 auditors (tui-public, security, docs, tests-ci,
> tui-render, tui-input) died on a spend limit — re-running them is a
> maintainer call.
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

### 0a. Live-dogfood reports (2026-07-15) — ALL RESOLVED same day (done log)

All five maintainer reports fixed + landed: user-block wrap, wrap-aware
composer growth, ctrl+o/ctrl+e split, PasteMsg routing, and the two
directives (CC-style `/model` picker with Fable in the fallback; composer
↑/↓ own history/cursor, never scroll). Detail in the done log.

## 1) Correctness bugs

§1a (TUI SSE / state-machine cluster) and §1b (group view / sort / search /
pickers) are fully closed — done log. Residuals from §1c live below; the §1b
row-model consolidation moved to §2a where it belongs.

### 1g. Dashboard lifecycle actions bypass client (2026-07-18 SDK-example review) — DONE

- [x] **TUI destroy skips the worktree WIP capture — fixed 2026-07-18**
  (done log): lifecycle actions routed through `client` via the
  `clientLifecycleBackend` adapter; destroy-hook plumbing removed.
- [x] **TUI suspend/resume don't pause/resume file sync — fixed
  2026-07-18** (done log): same adapter change.

### 1c. Rendering / layout residuals (2026-07-04 audit; parents fixed — done log)

- [x] **`statusline.go` row-1 overflow — closed 2026-07-12** with the §2c
  statusline collapse (budgeted `slSeg`/`budgetRow`, required segments kept,
  optional shed tail-first; width-safe by construction).
- [x] **Subagent child tool lines width-safe — done 2026-07-11** (done log):
  budgeted by construction (measured prefix, remaining-width segments,
  ANSI-aware whole-line backstop); pinned at widths 8-80.

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
- [x] **[C3] shape-changing re-create rejected — done 2026-07-09** (done log):
  desired vs baked pod-template env compared (`anthropicEnvShape`) BEFORE any
  Secret mutation; same-shape account swaps still patch in place. Supersedes
  the old strip-on-account-removal behavior (which could brick resume).
- [x] **[C4-C11] assorted client reliability — done 2026-07-09** (done log):
  observer forwards 1 port not 3; ssh config paths quoted; background connect
  phase bounded 60s with a timeout advisory; pre-existing PVC survives
  rollback; `projectPath` race fixed; suspend probe capped 5s; `models.Limit`
  refreshes models.dev async (never blocks the reducer); reaper replaced on
  spec mismatch so `IdleTimeout`/`ReaperImage` overrides apply.

- [x] **Concurrent-session sync collision — CLOSED 2026-07-11** (done log):
  git projects isolated by §9 worktrees; non-git same-path sessions now get
  a warn-only `Connection.Warning` at Connect (`sameDirSyncWarning`, index-
  resolved alphas, silent without mutagen).
- [x] **Mutagen conflict detail in the TUI — done 2026-07-11** (done log):
  `conflicts[]` parsed typed (alpha/beta per path, defensive on shape drift);
  `StatusDetail` + per-file lines + resolution hint in the detail pane
  (capped at 5 + "+N more"). Shape unverified against a live conflicted
  mutagen — falls back to count-only on drift.
- [x] **Transcript provenance audit trail — done 2026-07-11** (done log):
  the sandbox-session → claude-session-id mapping (already in the index but
  deleted on destroy) now also appends to `transcript-audit.jsonl` in the
  state dir, deduped, surviving destroy. The unscoped `~/.claude` merge
  itself stays by design (subPath bind, resumability contract).
- [ ] **Port-forward mid-stream death detection (SMALL, optional).** Terminal
  state + immediate `ErrSessionGone` reconnect-abort landed (done log);
  consuming the literal `ForwardHandle.Done()` channel needs a
  `ConnectResult.ForwardDone` seam through client/cli — only worth it if
  mid-stream (non-reconnect) death detection matters.

### 1e. Autopilot (`/loop`/`/goal`)

The local driver is complete (items 1–5: detach-durable `/goal` continuation,
sentinel termination, lapse toast, idle-reaper interval warn, esc contract —
done log; the item-3 follow-up below is the one loose end).

- [x] **Driver-spec re-arm — done 2026-07-11** (done log): last-armed spec
  persisted via a `DriverStore` seam (`index.Entry.Driver`, survives
  detach); bare `/loop` / `/goal` re-arms it without retyping.
- [x] **6. Server-side loop — IMPLEMENTED 2026-07-11** (done log; ADR
  archived to
  [`docs/archive/server-side-loop-adr.md`](docs/archive/server-side-loop-adr.md)):
  `autopilot.state` schema event; runner spec persistence + driver
  (sentinel/budget/lapse/error stops, 409-defer to manual turns, 5× retry
  ladder, boot re-arm anchored on `last_completed_at`,
  persist-stopped-before-emit, armed ⇒ non-idle) +
  `PUT/DELETE /sessions/:id/autopilot` + `/status` capability bit; SDK
  `ArmAutopilot`/`DisarmAutopilot` + sdktest pins; TUI arms the runner
  driver when capable, renders purely from `autopilot.state` (replay never
  re-notifies), local tea.Tick kept as the no-capability fallback. NOT yet
  live-verified on a real cluster (the laptop-closed overnight run —
  maintainer eyeball).

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
- [x] **PreToolUse block result modernized — done 2026-07-11** (done log):
  returns `hookSpecificOutput.permissionDecision:'deny'` AND keeps the
  legacy `decision:'block'` alongside (both shapes verified against the
  pinned SDK's `sdk.d.ts`); guard tests pin the combined shape. SDK version
  unchanged (the pin question stays in the carry-forward caveat below).
- [x] **[A2] event log + SSE redact secrets — done 2026-07-09** (done log):
  shared `redact.ts`; `appendEvent` masks `turn.started`/`tool.*`/
  `permission.*` + role-user `message.*` (the D5 echo) before persist AND
  broadcast.
- [x] **[A3] SECURITY.md posture rewrite — done 2026-07-11** (done log):
  0.0.0.0-binds table + what the ingress policy does/doesn't contain, open-443
  egress named plainly as the exfil channel + `toFQDNs` hardening path, the A1
  `/proc/1/environ` residual with exact guarantees, verified controls list
  (every claim carries file:line), corrected the stale "drop-ALL caps" claim
  (12 caps re-added incl. SETUID/DAC_OVERRIDE).
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
- [x] **[B5-B9] runner robustness LOWs — done 2026-07-09** (done log): after
  clamp; async git (A1 sanitization preserved); corrupt session.json moved
  aside + reseed; permission resolve first-write-wins with honest 409;
  typed 413/400 body errors.

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

- [x] **Declarative vertical layout regions — done 2026-07-12** (done log):
  per-frame `[]region` band stack (body = flex) behind `liveLayout`/
  `previewLayout`; render, list sizing, and scrollbar hit-test all walk it.
  `App.modalRect` deliberately excluded (popup margin geometry, not a band).
  Goldens byte-identical; §2c layout changes are now one-line band edits.
- [x] **Row models consolidated — done 2026-07-12** (done log): one typed
  `[]listRow` from `visibleRows()`; cursor indexes it; `sessionAt(cursor)`
  is the single accessor for render/nav/actions/attention routing.
  `visibleSessions()` remains the flat data source only.
- [x] **App.Update flat dispatch + one detachTranscript() — done 2026-07-15**
  (done log): Update is a ~158-line flat router of named `handle*` methods;
  the 4 detach sites share `detachTranscript()`; the recursive
  `a.Update(*msg.ready/failed)` re-entry replaced by direct
  `handleAttachReady/Failed` calls (verified equivalent); `delegateDashboard`
  is the single B17 call site; per-screen tails extracted.
- [x] **Explicit input contexts + binding tables — done 2026-07-15** (done
  log): derived context enums both layers + ordered `boundAction` tables
  (`inputctx.go` — precedence is data, help text rides the binding); esc
  cascade = one `escStep` list read by BOTH handleKey and escapeConsumes
  (closed a real queued-prompt divergence); overlay internals stay as
  delegate fallbacks. Landed WITH the §2d q/g footer fix + leader chord.
- [x] **permissionPrompt component — CONSOLIDATED 2026-07-15** (done log):
  one `permPrompt` component (Render/Height/HandleKey) owns the tool panel
  AND plan card — static bodies cached, fade chrome live (kills the permBox
  asymmetric refresh); plan grammar joined permPromptKey; queue shares the
  wants-summary vocabulary (full panel reuse deliberately NOT done — the
  queue is a cross-session list, allow-once by design). §2a structural work
  is now fully closed (clock-sweep deferrals remain below).
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

- [x] **1. Subagent output flattening FIXED — done 2026-07-13** (done log):
  `MessagePayload.parentToolUseId` (schema + gen, additive — no protocol
  bump); the mapper stamps it on every message.*/reasoning.* emit; the
  reducer routes parented events to the Task card (live bounded narration
  line) and never the main buffers; `sandbox turn` stdout guarded too.
  Follow-up still open: per-agent full transcripts (narration renders as one
  live line; subagent thinking deliberately dropped — the event log retains
  everything). Live pod verify wanted at next natural Task fan-out.
- [x] **2. "Always allow" reachable — done 2026-07-13** (done log): the §2c
  numbered panel's option 2 sends `Scope:"session"` (tool-name grant,
  `grants.ts`); scrollback names the grant's tool-level breadth; the perm
  queue and plan card stay allow-once by design. STILL OPEN residuals (small):
  `editedInput` (runner validates it; TUI never sends an edited input) and
  SDK `canUseTool` suggestions (3rd arg still dropped — `claude.ts` two-arg
  callback; no PermissionPayload field; would inform richer option rows).
- [x] **3. Thinking streaming + recoverable blocks — CLOSED 2026-07-15**
  (done log): the live-streaming half had already landed with the §1a/§2a
  cluster (live "∴ Thinking" tail; this item was stale — doc drift); the
  remaining half (multi-line thinks unrecoverable after commit) closed with
  the §2c thinking render: capped italic body + ctrl+o expansion, same
  6-line shape live and committed.
- [~] **5. Background tasks / tool progress — pipeline half DONE 2026-07-15**
  (done log): `tool.progress` schema event + `elapsedSeconds` (additive,
  protocol v2), mapper emit with parent attribution, E4-class compaction,
  runner-api.md. REMAINING: the TUI render half (elapsed on running tool
  cards; unlocks the §2c `⎿ exit 0 · 42 lines` combo) and background-task
  correlation (SDK task_id + SDKToolUseSummaryMessage, noted in mapping.ts).
- [x] **6. Citations + server-tool results — done 2026-07-18** (done log):
  `Citation` schema object + `MessagePayload.citations` (message.completed
  only, additive); mapper flattens/dedups/caps; web_search/web_fetch
  server_tool_use + result blocks map to normal tool.* (total, no orphan
  cards); TUI "Sources:" footnote (sanitized, width-safe) + headless
  `sandbox turn` Sources print. Audit [V6]/[V24]/[V25]/[V29] folded in.
  Live verify wanted at next natural WebSearch turn.
- [ ] **7. Images unrepresentable end-to-end.** String-only MessagePayload;
  image blocks dropped (`mapping.ts:153-156,183-188`). Kitty plumbing exists
  TUI-side (gauge). Gap starts at schema (attachment payload or fetch-by-ref
  given the SQLite log).
- [x] **8. Project slash commands / skills / CLAUDE.md in-pod — done
  2026-07-13** (done log): SDK turns load the user/project/local settings
  tiers by default (was pinned `[]`); `SANDBOX_SETTING_SOURCES` overrides
  (`none` = old isolation); title summarizer stays isolated; A1 not
  reopened (settings-defined hooks inherit the stripped agent env —
  SECURITY.md updated). Live in-pod verify (a synced `.claude/` command
  actually firing) still wanted.
- [ ] **AskUserQuestion answer flow (2026-07-18 audit [V15] residual).** The
  tool was in `DEFAULT_ALLOWED_TOOLS` with no answer path in ANY mode (yolo
  omits `canUseTool` entirely; prompting modes degraded to a useless generic
  allow) — removed 2026-07-18 (done log). To re-enable: wire `canUseTool`
  unconditionally, map AskUserQuestion's questions/options into
  `permission.requested` as a distinct question kind, return selections via
  `updatedInput`, and render a picker in the TUI (`runner/src/claude.ts`,
  the §2c permPrompt component).
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
- [x] **[D3] turn.* payloads on-schema — done 2026-07-09** (done log): four
  payload defs (field union across all emitters) + `just gen` + hand-written
  Go structs; TUI decodes `turn.failed` via the real `TurnFailedPayload`.
- [x] **[D4] interrupt mid-think reasoning teardown — done 2026-07-08** (done
  log): `finalizeStreaming` resets `m.reasoning`/`reasoningBuf`.
- [x] **[D5] opencode replay shows user prompts — done 2026-07-09** (done
  log): the turn adapter echoes the prompt as `message.* role:user`
  (Claude-parity path; existing reducer dedup prevents double-print).
- [x] **[D6] tool.delta attributed by id — done 2026-07-09** (done log): the
  mapper tracks `(parent, blockIndex) → tool_use id` per turn and stamps both
  ids on every `tool.delta`; the TUI targets by `flatTools` and drops parented
  deltas with no flat card. Distinct from gap 1 (text/thinking deltas), which
  stays open.
- [x] **[D7/D8/D10/D11/D12] event-model LOW sweep — done 2026-07-11** (done
  log): single id-carrying `tool.failed` (SDK tool_result is the terminal);
  `ToolPayload.tool` recovered via the id→name map on
  completed/failed/delta; `TurnRequestBody` mirrors `Advisor` + documents
  `Resume`'s real semantics; observer title passthrough exempt from the
  headless guard + pre-cycle `session.error` → synthetic failed turn;
  exactly one `usage.updated` per result with real cost, cache-only turns
  move ctx%. Deliberately NOT done here: `exitCode` (§2c plumbing, the
  hook/mapping seam correlation is nontrivial).
- [x] **[D9] Status dual vocabulary — done 2026-07-12** with the §8
  De-Claude break (done log): `State.Status` = k8s lifecycle only;
  new `State.Activity` (idle/busy/error) carries runner turn-activity.

### 2c. Design/layout changes (renderer)

Deduped against `docs/archive/ux-polish-plan.md` — nothing below is already committed
there. HIGH items are the at-a-glance tells; most are renderer-local.

- [ ] **Tool-card follow-ups** (the two-line ⏺/⎿ idiom + ctrl+o expansion
  landed — done log): per-card focus/expand for older cards
  (space/toggleSubagents has the same latest-only gap); `⎿ exit 0 · 42 lines`
  combo needs exit-code plumbing (§2b gap 5). *(The 2026-07-07 [H4-H7]
  regression residuals — control-sequence smearing, tab overflow, uncapped
  opencode output, ctrl+o stranding on content-less cards — all fixed
  2026-07-09, done log.)*
- [x] **Gutter bars killed; quiet user prompts — done 2026-07-12** (done
  log): assistant `⏺ ` + hanging indent, user dim `> `, goldens
  regenerated deliberately.
- [x] **Working indicator above the composer — done 2026-07-12** (done log):
  `✳ <verb>… (elapsed · ↓tokens · esc to interrupt)`, `esc to steer` when
  queued; context-aware verb; a new liveLayout band.
- [x] **Statusline collapsed to one row — done 2026-07-12** (done log): one
  budgeted width-safe row (model + mode chip never shed — closes the §1c
  row-1 overflow residual); ctx gauge only ≥60% AND known limit (200k
  fallback removed both places); cost ≥$0.10; rate-limit row transient 8s
  after `rate_limit.updated` with fade.
- [x] **Permission prompt: numbered-option question panel — done 2026-07-13**
  (done log): per-tool question + ❯-selected numbered options
  (`permprompt.go`); a/d hidden accelerators; ↵ confirms, diff reveal moved
  to ctrl+o; grace gate covers all resolving keys; goldens regenerated.
  "2. Yes, allow <tool> this session" landed WITH §2b gap 2.
- [x] **De-bracket system notices — done 2026-07-15** (done log): connection
  lifecycle = plain dim sentences; interrupted/interrupt-failed = Coral
  `⎿  Interrupted by user` elbow via `appendElbowNotice` (blockShell so the
  Coral survives blockInfo's dim restyle).
- [x] **Blank line per top-level entry — done 2026-07-15** (done log):
  `turnGap` → `entryGap` with one `startsEntry(prev,cur)` predicate shared by
  committed blocks AND the streaming tail (T1 held); consecutive tool cards
  tight, info/footer attach gapless. Goldens regenerated, diffs
  blank-lines-only.
- [x] **Persistent title header dropped — done 2026-07-15** (done log):
  `headerBands()` returns bands only while reconnecting/session-gone; body
  starts at row 0 normally; session title now rides the terminal tab via
  `tea.View.WindowTitle` (`App.windowTitle`); bodyTop/scrollbar/preview
  followed via the §2a band walk.
- [x] **Todo pinned widget + checkbox progression — done 2026-07-15** (done
  log): `todo.updated` mutates ONE `blockTodos` block (payload on the model);
  ✓ strikethrough dim-green / ▸ bright / ○ dim, no `▤ todo list` header;
  empty list → dim "todos cleared".
- [x] **Thinking render — done 2026-07-15** (done log): committed multi-line
  thinks show `∴ Thought` + italic TextMuted body capped at 6 wrapped lines
  + `… +N lines (ctrl+o)`; live tail shows the same 6-line window
  (tail-following, `… +N earlier lines`); ctrl+o generalized to
  `toggleLatestExpandable` (tool cards + capped thinks). Single-line thinks
  unchanged; goldens unchanged (none carry multi-line thinks).
- [x] **Transient scrollbar — done 2026-07-15** (done log): thumb renders
  only when off-bottom (`offset < total-h` gates kit.Scrollbar); blank gutter
  at bottom, width math unchanged. STILL OPEN residual (LOW): the dim
  `↓ new output · G bottom` pill during live turns — needs body-overlay
  machinery (§2e-A/F territory).

### 2d. Transcript/dashboard UX — still-open items from earlier passes

- [x] **Prompt history — done 2026-07-15** (done log): ↑/↓ recall in the
  compose context, gated on empty-composer-or-navigating so drafts keep
  their scroll meaning; draft preserved on entry, restored on ↓ past newest;
  edit exits nav; recorded only at user-origin `submit()` (driver ticks +
  initialPrompt excluded), consecutive dedupe. Session-local by design
  (parked-state survival = documented residual).
- [x] **`q`/`g` overloads — advertising fixed 2026-07-15** (done log): the
  footer now derives from the dispatching dctxList table (`shortHelp` →
  `footerBindings`), so `q` truthfully reads "perm queue" when sessions
  wait and "quit" otherwise; `g` help reads "group view · gg top" (footer +
  `?` overlay). Behavior unchanged by design.
- [x] **External-pane leader chord — done 2026-07-15** (done log): `ctrl+]`
  arms a leader inside external panes — `g`/`k` = next/prev-attention
  (minimize pane + attach target), double-tap or 500ms timeout = detach,
  other keys disarm + forward; pure `leaderStep` classifier + gen-guarded
  tick; `jumpToPrevNeedingAttention` added. Lone-ctrl+] detach now resolves
  at the timeout (accepted trade). Live-verify wanted: chord + jumps in a
  real cluster `opencode attach` pane.
- [x] **First-account path — done 2026-07-15** (done log): zero stored
  accounts now always shows the account stage ("cluster default" + "＋ add
  account"); cluster-default selection is byte-identical to the old silent
  skip (same `beginCreate` params, pinned); §6 reauth-stage signpost left
  where the row set is decided.
- [x] **Yolo default — done 2026-07-12** (done log): runner default flipped
  to `bypassPermissions` (SDK gate verified to cover it); statusline renders
  bypass as an inverted coral `⚠ bypass` chip (never invisible); the TUI
  already pinned the mode per turn, so no status plumbing was needed.

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
  (the SDK's programmatic guard/audit hooks attach only to SDK turns —
  `claude.ts:429`; since the §2b gap-8 fix both paths load on-disk settings),
  no metrics-observer API, resume forks the session id,
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
- [x] **[E4] delta-only compaction — done 2026-07-09** (done log): one
  bounded DELETE on `turn.completed` keeps the last N turns' deltas
  (`DELTA_COMPACT_KEEP_TURNS`, default 2); never fails the append.
- [x] **[E5] passive streams batch-drain — done 2026-07-09** (done log):
  `liveSSEBatchCmd` + `RunnerEventBatchMsg` mirror the foreground 512-drain;
  one Update+View per burst.
- [x] **[E6] live reasoning wrap is incremental — done 2026-07-09** (done
  log): complete-lines prefix cache keyed by width+theme epoch; only the
  trailing partial re-wraps per frame.
- [x] **[E7] streaming-tail O(1) change key — done 2026-07-11** (done log):
  buffer LENGTH (+ mode + theme epoch) replaces the full-buffer hash+copy;
  safe because the live buffer is append-only within a tail's life (audited
  every reset site). Bench: ~89ns constant vs O(L) per delta.
- [x] **[E8-E10] LOW perf trio — done 2026-07-11** (done log): SSE scan loop
  zero-copy via `scanner.Bytes()`+`CutPrefix`; `events.ts` per-connection
  prepared-statement cache; host event-cache holds one open O_APPEND handle
  per session + 8 MiB tail cap with atomic compaction.

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
- [~] **Mutagen sync GC follow-ups — MF3/MF5 + SF1-CLI done 2026-07-12**
  (done log): context labels + scoped GC (legacy label-less syncs keep
  pre-MF3 reapability); prober-layer debounced Reconcile self-heal on
  stall; startup GC on bare-`sandbox`/`attach`; dev-reset/kind-down gc
  first. The two SF1 residuals CLOSED 2026-07-12 in batch 4 (dashboard
  `Init` fires `reconcileListCmd`; create commands run `startupSyncGC`).
  Unverified live: real-daemon heal of a genuinely wedged transport;
  `kind-down`-after-gc leaves orphans if sessions were live at teardown
  (pre-existing, noted). **2026-07-18 audit follow-ups landed** (done log):
  safety-halt vs stall split ([V2]), label sanitization ([V3]), Paused
  classification + heal ([V14]), namespace GC scoping ([V28]), paused-orphan
  reap CLI-side ([V35]). STILL OPEN ([V35] residual): the DASHBOARD reaper
  deliberately does not list paused syncs — its grace logic
  (`internal/tui/dashboard/model_sse.go` `reapOrphans`/`gcRunningSet`)
  protects only Running/Creating, so it can't yet distinguish a suspended
  session's paused syncs from a kubectl-deleted one's; teach it
  suspended-vs-gone before extending the reap.

## 6) Codex backend + credential manager

Plan: [`docs/codex-integration-plan.md`](docs/codex-integration-plan.md) —
remote app-server + local `codex --remote` TUI (Option B), mirroring the
opencode supervisor/external-pane pattern + runner metrics-observer. Backend id
`codex-app-server` reserved (`internal/session/types.go:63`). Auth =
ChatGPT-plan OAuth owned by the credential manager.

- [ ] **Codex C3 parity: extend the shape-changing-re-create guard to codex**
  (from the Phase 1 landing, 2026-07-17): `anthropicEnvShape`
  (`internal/k8s/backend.go`) only detects the anthropic env vars, so a codex
  account→accountless re-create is NOT rejected — `syncSessionCredential`
  strips `codex-auth-json` while a resumed pod's baked NOT-Optional
  `CODEX_AUTH_JSON` SecretKeyRef still points at the stripped key (pod would
  fail env resolution on restart). Covered + documented by
  `TestCreateSessionStripsCodexCredentialOnRecreate`; generalize the shape
  guard over both credential families like `reconcileSecretCredential` did
  for the sync side.
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
  3. ~~Secret GC for out-of-band deletion~~ — **done 2026-07-12** (done
     log): ownerReferences (Secret+PVC → Sandbox) set on create-owned
     resources only, best-effort, reconcile-safe; `kubectl delete sandbox`
     now cascades.
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
  - ~~Remove the `go get .` activation hook~~ — **done 2026-07-12** (done log).

### 7c. OpenCode operational items

- [x] CLI `opencode` `--model`/`--provider`/`[prompt]` — done 2026-07-12
  (done log): provider threads to `CreateOptions.OpencodeProvider`
  (fail-closed); the prompt positional is delivered as a headless first
  turn via the turn adapter pre-attach (hard error, never silently
  dropped). NOT yet live-verified on a cluster: create → headless turn →
  `opencode attach --continue` picking up the in-flight turn.
- [ ] Verify detach (Ctrl+]) + surrounding chrome behave identically for every
  backend's external pane.
- [~] **Live-session verify sweep — opencode (2026-07-06 headless pass on
  my-cluster, zen provider, free big-pickle):** (a) **busy/idle status:
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
- [x] Observer `interruptedTurns` leak bounded — done 2026-07-12 (done log):
  cap 8, oldest-first eviction; regression test.
- [x] **Reasoning double-`message.completed` fixed — done 2026-07-12** (done
  log): root cause = opencode `ReasoningPart` streams content in the same
  `.text` field as `TextPart`, so its deltas were mis-registered as
  assistant text and the idle flush re-emitted them; reasoning part ids now
  tracked, deltas routed to `reasoning.delta`, flush guarded (both
  orderings pinned). Live re-verify at next natural occurrence.
- [ ] **Diagnose live: opencode looks stuck after disconnect/reconnect
  (maintainer report 2026-07-04; recipe below). 2026-07-06 live probes
  (my-cluster) EXONERATED the event layer:** (i) SSE dropped mid-flight
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

- [x] **Narrow public `client.Backend` interface — done 2026-07-11** (done
  log): 12-method interface (exactly the orchestration call sites),
  `WithBackend` takes it, concrete backend pinned by assertion + sdktest.
  NOT yet externally implementable — `EnsureReaper` names
  `internal/k8s.ReaperOptions`; export/replace that type when a third-party
  backend is real (documented in the interface comment).
- [x] **De-Claude coordinated break — done 2026-07-12** (done log):
  `ApprovalPolicy` enum (wire strings unchanged), `Connection.External`/
  `ExternalCreds`, `State.AgentSessionID` (+ index Load migration),
  D9 folded in (`State.Activity` for runner turn-activity; `Status` is
  k8s-only). Wire break ⇒ protocol v2 + actionable mismatch advisory
  (pinned). Live pod round-trip unverified.
- [x] **`Session.Shell` + `SSHTarget` seam — done 2026-07-12** (done log):
  SSH-only forward + per-session key as the reusable primitive; one-call
  PTY shell atop it (raw mode, SIGWINCH, remote exit code); CLI shell is a
  thin wrapper (transport moved k8s-exec → in-pod sshd); sdktest pins.
  Live interactive path unverified against a real pod.
- [x] tui/theme `Register(Theme)` + `Denied`/`*Subtle` tone vars — done
  2026-07-12 (done log).
- [x] tui/kit palette race → whole-palette `atomic.Pointer` snapshot — done
  2026-07-12 (done log; -race hammer test).
- [x] tui/list dead `Item.Finished()` dropped (+ sdktest pin) — done
  2026-07-12 (done log).
- [x] client: `Destroy` sync-before-destroy reorder — done 2026-07-11 (done
  log); pinned by the F3 call-order spy.
- [x] client: `DialRunner` forwards the runner port only — done 2026-07-11
  (done log); pinned by TestDialRunner.
- [x] kit.FormatTokens `B` tier with boundary promotion — done 2026-07-12
  (done log).
- [x] WithStateDir ssh-dir layout — DECIDED via the §9 worktree sign-off
  (4.10): move `ssh/` INSIDE the state root in the same pre-OSS break that
  adds the worktree root; implement with the worktree Spec-split change.
  `client/sync.go`.
- [ ] **Pod bootstrap files + generic env/secret injection** (upstream
  integration request, 2026-07-17): `CreateOptions.ExtraEnv`/`ExtraSecretEnv`
  (per-session-Secret-backed env escape hatch) then
  `CreateOptions.BootstrapFiles` (operator files materialized in pod `$HOME`
  before the agent starts — NOT in the synced workspace); operator binaries
  stay a derived-`--runner-image` pattern, not SDK surface. Full design +
  validation rules: `docs/design-pod-bootstrap-and-tool-injection.md`
  (Status: draft — needs maintainer sign-off). Touch points:
  `client/client.go` (CreateOptions/validate), `internal/k8s/backend.go`
  (Secret + buildEnv), runner boot materialize step; sdktest pins in the
  same change.

**2026-07-18 SDK-example review additions** (from auditing
`client/example_test.go` against the full surface):

- [x] **`OpencodeProvider*` constants aliased into `client` — done
  2026-07-18** (done log): three re-exports + sdktest pin; CreateOptions
  doc reworded to the public spellings.
- [x] **`Example_chat` full chat-loop example — done 2026-07-18** (done
  log): compile-only example covering permissions, deltas, tools,
  steering, reattach/replay, account selection, detach-vs-destroy.
- [ ] **SDK capability gaps for an external chat/dashboard consumer**
  (overlaps `docs/public-api-importability-plan.md`, which is TUI/auth
  focused — these are client-level): no cluster watch (`k8s.Backend.Watch` →
  `StateEvent`, `internal/tui/dashboard/actions.go:20` — SDK has only
  `List`); no model context-window/pricing resolver (`internal/models.Limit`,
  `internal/tui/dashboard/readmodel.go:29` — consumers can't build the ctx%
  gauge); session titles/rename are `internal/index`-only
  (`internal/cli/rename.go:24`); `Client.SyncStatus` returns raw bytes with
  the conflict/orphan classification stuck in `internal/sync`
  (`internal/cli/sync_support.go:90`).

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
- [x] **Per-session git worktree lifecycle — IMPLEMENTED 2026-07-11** (done
  log; design archived to
  [`docs/archive/worktree-lifecycle-design.md`](docs/archive/worktree-lifecycle-design.md)
  with a layout amendment in its Status block): waves 1-4
  (`b84f696`..`d59690c`) — `Spec.WorkspacePath` split + `SANDBOX_PROJECT_ROOT`
  discovery; `WorktreeAuto` default worktree at Create with rollback;
  capture-then-remove Destroy + `ReapWorktrees`; `WorktreeStatus`/
  `ConvertToBranch`; dashboard `b` convert modal (title-derived prefill) +
  `sandbox worktree gc` + `--worktree` flag; `ssh/` nested into the state
  dir with one-time migration (closed the §8 WithStateDir item). Fixes
  §1d's sync collision for git projects. Residuals: non-git same-path
  collision warning still open (§1d, code TODO in Connect); B2
  move-session-to-machine not built (B1 shipped); WIP/convert commits are
  `--no-gpg-sign` by design.

## 10) Harness / tests / docs / ops

**2026-07-07 test-coverage additions** (two agents; detail in
[`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §F, id in brackets):

- [x] **[F3] client orchestration covered — done 2026-07-11** (done log):
  fake `Backend` + fake mutagen `Runner` sharing one call-order log;
  table tests over Create/Status/List/Suspend/Resume/Destroy/DialRunner;
  the Destroy spy pins sync-terminate → destroy → local-state-removal (and
  index preservation on backend failure). Residual: `Session.Connect`'s
  runtime path itself still has no fake-backed test (needs a fake
  RunnerClient/health seam) — fold into [F6]'s `waitHealthy` item.
- [x] **[F4] `server.ts` HTTP layer covered — done 2026-07-11** (done log):
  `createRunnerServer` extracted (listen-free seam); 17-test `node:test` suite
  boots the real router + real sqlite event log — bearer auth, 404s, every
  409 `turnRejectReason` path, SSE `after=` replay incl. the B5 clamp, B9
  typed 400s. Residual promoted below.
- [x] **e2e fake-runner faithfulness — done 2026-07-12** (done log): fake
  mirrors server.ts auth ordering, 409 set, 400/413 bodies, after=
  validation + B5 clamp; unmodeled routes 501 loudly;
  TestE2EFakeRunnerFaithfulness pins 16 shapes.
- [x] **Oversized-body 413 now reaches clients — done 2026-07-12** (done
  log): `readBody` stops destroying the socket; rejects once, drains,
  route's catch flushes the mapped 413. Pinning test flipped to assert the
  413 body.
- [x] **[F5] port-forward lifecycle covered — done 2026-07-11** (done log):
  retry decision extracted pure (`classifyForwardReconnect` +
  `nextForwardBackoff`) + table tests over every branch and the full backoff
  ceiling; C1 Close-seam invariants (Done-after-Close, done-closes-once,
  error-churn vs concurrent Close) pinned under `-race`.
- [x] **[F6/F7] MED coverage — done 2026-07-12** (done log): cred-rotation
  warning, `waitHealthy` (healthChecker seam), `Session.Connect` pre-dial
  branches, `evaluateIdle` full branch table; §7c double-emit/leak pins
  landed with their fixes. STILL OPEN residuals: `Session.Connect`'s happy
  path + `reaperTick`'s wrapper glue need a runner-factory injection seam
  on `Client` (documented in `client/health_connect_test.go`); the
  dashboard `model_sse.go` command closures remain untested (excluded from
  the batch — dashboard was under the §2a refactor).

- [x] **docs shape gaps — done 2026-07-12** (done log): runner-api.md
  healthz body / 409 table / interrupt empty-segment; README auth+sync-gc+
  opencode flags; LAUNCH-CHECKLIST HEAD claim fixed; HARDENING-BACKLOG
  marked provenance-only (verified zero true TODO overlaps).
- [ ] Ops: new CLI-created sessions use `:latest` and can hit the stale traefik
  manifest cache — bust the cache or pin digests CLI-side. (Resume path
  already fixed via digest pinning — see done log.)
- [x] **`sandbox doctor` — done 2026-07-12** (done log): 10-check host
  readiness table (cluster checks can FAIL and short-circuit; binaries/
  creds/images advisory WARN/INFO with remediation); PASS paths of the
  cluster checks unverified against a live cluster.
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
  `runner/src/trace.ts`). Fable-verified 2026-07-06. 2026-07-13 (done log):
  connect id ↔ turn id bridged across the HTTP seam (`X-Sandbox-Trace-Id` →
  `turn.link` in the pod log); runner boot spans (`index.ts`, socket-accept
  anchored); `SANDBOX_TRACE` now documented in `docs/architecture.md`
  (Observability section). STILL OPEN: pod-ready sub-phases (schedule vs
  pull vs ready — the big §5 unknown); SSE first-event latency; the §1d
  observer-cap model remains absent from `docs/architecture.md` (doc drift,
  2026-07-06 harness audit).

- [ ] **Visual-testing gaps (2026-07-13 review) — static goldens are strong,
  motion/theme/size axes are not.** The golden harness
  (`internal/tui/dashboard/golden_test.go:30`) deliberately pins
  `SANDBOX_REDUCE_MOTION=1`, so every committed frame is the settled
  end-state; the transition catalog (`internal/tui/dashboard/transitions.go:16`)
  is only value-tested (`tui/anim/transition_test.go`,
  `internal/tui/dashboard/permission_clock_test.go:119` — and the latter only
  at past-window end states). Sub-items:
  - [ ] *Mid-motion golden frames:* with motion forced ON and the injected
    `nowFunc` stepped through fixed offsets (0, ½-window, past-end of
    `rowEnterDur`/`statusFlashDur`/`permissionAppearDur`), golden the rendered
    frame at each step — pins the row fade (`model_render.go:347`), status
    flash (`model_render.go:342`), and permission-box appear
    (`permission_diff.go:64`) as reviewable frame sequences. All inputs are
    already injectable, so this is byte-deterministic; it also gives agents a
    static way to "see" the animation (the golden files ARE the frames).
  - [ ] *Theme axis:* goldens render only the default theme
    (`tui/theme/theme.go:161` — themes[0] Midnight); Daylight is never
    snapshotted anywhere. Parameterize at least one dashboard + one transcript
    golden per registered theme.
  - [ ] *Size axis:* every golden is 100×30 (`golden_test.go:53`,
    `golden_multiturn_test.go:37`). `tui/kit` covers narrow degradation
    per-component (`components_test.go` TestCardDegradesNarrow) but no
    narrow-terminal golden exists for the composed dashboard/transcript frame.
  - [ ] *Animation eyeball harness:* no repeatable way to watch dashboard
    motion without a live cluster — `cmd/tuikit-demo` exercises only the
    public `tui/` packages, and `just dev-tui` (justfile:363) needs the kind
    cluster. Options: a fixture-replay dev mode for the dashboard (the
    `testdata/transcript-multiturn.jsonl` stream already drives the real
    handleEvent→render path in tests), and/or a VHS tape
    (`nix run nixpkgs#vhs`) recording tuikit-demo → gif as a non-gating CI
    artifact (vhs already noted as a nice-to-have in
    `docs/archive/local-dev-turn-parity-plan.md:159`).

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

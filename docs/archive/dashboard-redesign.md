# Dashboard redesign — Triage Console (list + detail)

Status: **implemented** (Phases 1–4 complete; P1–P13 each have an implementation
+ passing test; `renderDetailLines` wired into `renderZoned`). Direction chosen
from a four-way design exploration of the FleetView dashboard
(`internal/tui/dashboard`). This doc remains the source of truth; the convergence
loop below re-derives status from the code each run. `TODO.md` points here.

## Why

The current dashboard (`renderZoned`, `zones.go:86`) is a 2-column "command
center": a left SESSIONS box (~58% width) + a right stack of three boxes
(NEEDS YOU / USAGE / CLUSTER). A screenshot review surfaced a set of confirmed
problems (P1–P13 below). The biggest: nothing paints an opaque background, so on
a transparent terminal the desktop bleeds through everything; and the three
session rows are indistinguishable (all render the project basename `sandbox`).

## Chosen direction: Triage Console

Collapse the three right-hand info boxes into a **one-line cluster strip**, and
spend the reclaimed width on a real **detail pane** for the selected session.
The list answers "which session needs me"; the detail answers "what is it doing
and what do I press" — **without having to attach**.

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│ sandbox   6 sessions · 2 busy · 1 waiting · 1 needs input          ⚑attn  sort:attn ↓     │
├────────────────────────────────────────────────────────────────────────────────────────┤
│ cluster  ● 4 running · 1 suspended · ✕ 1 failed     claude 4 · opencode 1                 │
├──────────────────────────────────────┬───────────────────────────────────────────────────┤
│ SESSIONS                             │ DETAIL ─ refactor sync manager                    │
│ ▌●❯ refactor sync manager      8m    │ status   ❯ needs-input                            │
│     opus-4.8·claude  a3f1  62%       │ model    opus-4.8   ctx 62%                        │
│   ◆ fix portforward leak      just   │ agent    claude                                   │
│     opus-4.8·claude  7c20  41%  ⚠    │ project  ~/git/sandbox                             │
│   ◐ add reaper job spec       1m     │ session  a3f1c9   pod  sbx-a3f1-0                  │
│     sonnet·claude  bd92  18%         │ created  1h ago   active  8m ago                   │
│   ○ docs polish               19m    │ ─ recent ──────────────────────────────────────  │
│     opus-4.8·claude  19bd  7%        │  Edit  internal/sync/mutagen.go                   │
│   ◌ stale spike (suspended)   3h     │  Bash  go test ./internal/sync/...                │
│   ✕ build runner (failed)     5h     │  Read  internal/sync/ssh.go                       │
│ ─ legend ───────────────────────     │ ─ needs you ───────────────────────────────────  │
│ ○idle ◐busy ◆wait ❯input ◌susp ✕fail│  ❯ waiting for your input                          │
│                                      │  [↵ attach] [r rename] [s suspend] [x destroy]    │
├──────────────────────────────────────┴───────────────────────────────────────────────────┤
│ ↑↓ move   ↵ attach   a approve   d deny   / filter   o sort   ? help   q quit              │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

## Layout anatomy

- **Cluster strip** (new, 1 line, replaces the CLUSTER box): `● N running ·
  M suspended · ✕ K failed` + real backend mix derived from the live sessions
  (`claude N · opencode M`). Fixes the hardcoded-backend bug (P9). Counts come
  from a single shared partition helper (P13).
- **Left list** (~58%): the SESSIONS box, rows two physical lines each (primary +
  dim sub-line) so identical titles disambiguate and model/ctx fit without
  crowding. Pinned `─ legend ─` footer row (P5).
- **Right detail** (~40%): wires in the **already-written** `renderDetailLines`
  (`model.go:1507`) — currently dead (called only by tests). Adds a `─ recent ─`
  tool list and a `─ needs you ─` action block.
- Surfaces: list fills `colorSurface`, detail fills `colorRaised`, strips/bars
  fill `colorSurface`; the root view sets `colorPage`. No transparent cells (P1).

### Session row spec

Two lines per session (fixed columns so the layout never shifts):

- **Line 1:** `selection-bar(2) · attention-dot(2) · status-glyph(2) · title(flex)
  · right-aligned relativeTime`. relativeTime falls back to created-age, never a
  bare `—` (P3). Attention dot extended to fire on `StatusFailed` too (P6).
- **Line 2** (indented under title, `colorTextMuted`): `AgentLabel ("<model>·<client>")
  · short session id (first 4 hex of State.ID) · ctx% · ⚠ when failed`. The
  short id is the disambiguator since there is **no git-branch field** in the
  data model (P2); model + ctx% address P4.

### Detail pane spec

`renderDetailLines` already renders, for the selected session: title, status
glyph+label, agent (client), model, project path, session id, pod, created,
active, the inline gold permission box (`a approve · d deny · ↵ view diff`),
and connector/action error blocks. Additions:

- **`ctx` line:** `model    opus-4.8   ctx 62%` — computed from new per-session
  token fields ÷ `models.Limit(model).ContextLimit` (`models.go:26`).
- **`─ recent ─` section:** the last ≈3 tool calls, newest first, each rendered
  as the **real tool name + its primary arg**, e.g. `Edit internal/sync/mutagen.go`,
  `Bash go test ./...`, `WebSearch claude pricing`. Reuse `toolArg(tool, input)`
  (`transcript.go:798`) for arg extraction — no friendly-verb remap. Main-thread
  tools only (skip subagent-child tools, i.e. `ParentToolUseID != ""`).
- **`─ needs you ─` block:** action key hints (`↵ attach · r rename · s suspend ·
  x destroy`) shown when DashStatus is Waiting / NeedsInput / Failed (P13 — items
  become actionable).

## Key feasibility findings (why this is cheaper than it looks)

1. **Live data for background sessions already streams.** The dashboard opens a
   passive SSE stream per *running* session (`model.go:872`, `EventsPassive`
   `client.go:191`) and routes events by id (`handleRunnerEvent`, `model.go:738`).
   So a selected-but-not-attached session's events already arrive — **no
   connector rework**, does not touch the RV8 heavyweight-connector concern.
2. **The detail renderer already exists** (`renderDetailLines`, `model.go:1507`),
   just unwired. We call it, not build it.
3. **The tool-arg extractor already exists** (`toolArg`, `transcript.go:798`),
   same package — directly reusable for `─ recent ─`.

The only genuinely new state: token/cost fields and a small recent-tools ring on
the dashboard `Session` struct, plus two new cases in `ApplyRunnerEvent` (usage +
tool.started, both currently no-op defaults at `session.go:290`).

## Phased implementation plan

### Phase 1 — Layout (low risk)
- `renderZoned` (`zones.go:86`): replace the right 3-box `JoinVertical` with
  (a) a 1-line cluster strip between header and mid, and (b) a right DETAIL pane
  = `boxWithTitle("DETAIL", m.renderDetailLines(rightW-4, midH-2), …)`.
- Delete `usageBody` (`zones.go:255`) and its `blockBar` use (removes P7/P10
  entirely); replace `clusterBody` (`zones.go:281`) with a strip func counting
  running/suspended/failed **and** backend mix via `ClientLabel(s.State.Backend)`
  (fixes hardcoded `claude-sdk`, P9).
- Opaque surfaces (P1): set `Background(colorPage)` on the composed root view;
  give `boxWithTitle` body rows + borders a `Background` (`colorSurface`/
  `colorRaised`); fill `clampLines` padding.
- Borders neutral (P12): list/detail use `colorBorderMedium`; reserve gold/green
  for actual status semantics (attention dot, permission box).
- Footer (P11): `bottomBar` (`zones.go:191`) `colorTextDim → colorTextMuted`.

### Phase 2 — Rows (low risk)
- `renderSessionRow` (`model.go:1443`): two-line layout per the row spec; add the
  4-hex short id + always-present model slot; relativeTime fallback so it's never
  a bare `—` (P3). Update row-height math so the viewport advances by 2/session.
- `attentionDot` (`attention.go:90`): include `StatusFailed` (P6).
- Append a `renderLegend()` row to `sessionListBody` (`zones.go:195`) (P5).
- One shared `statusCounts()` helper consumed by header + cluster strip (P13).

### Phase 3 — Live metrics (low–med)
- `Session` struct (`session.go:74`): add `InputTokens, OutputTokens,
  CacheReadTokens, CacheWriteTokens int`, `TotalCostUSD float64`, and a cached
  `CtxLimit int`.
- `ApplyRunnerEvent` (`session.go:232`): add `case session.EventUsageUpdated`
  unmarshalling `UsagePayload` (`event.go:53`) into those fields (return false —
  no status change). Set `CtxLimit` from `models.Limit(sess.Model).ContextLimit`
  on `session.started`. ctx% = (inTok + cacheRead + cacheWrite) / CtxLimit.
- Render ctx% in the row sub-line and the detail `model`/`ctx` line; cost in the
  detail (and optionally a cluster-strip aggregate).

### Phase 4 — Recent tool activity (med)
- `Session` struct: add `RecentTools []ToolRef` (a small ring, cap ~5) where
  `type ToolRef struct{ Tool, Arg string }`.
- `ApplyRunnerEvent`: add `case session.EventToolStarted` — unmarshal
  `ToolPayload` (`event.go:32`), skip subagent-child tools (`ParentToolUseID != ""`),
  push `{Tool: p.Tool, Arg: toolArg(p.Tool, p.Input)}`, cap to the ring size
  (drop oldest). Return false (no status change).
- `renderDetailLines`: after the kv block, render a `─ recent ─` rule + the last
  ≈3 entries newest-first as `<Tool>  <Arg>` (Arg truncated to width). Reuse
  `toolArg` (`transcript.go:798`).

### Decisions baked in
- **Recent tools: real tool names** (`Read`/`Write`/`Edit`/`Bash`/`WebSearch`…),
  not friendly verbs — matches the transcript tool cards.
- Last **3** shown, **newest first**, **main-thread only**, ring capacity 5.
- No git-branch row (no such field exists); short session id is the disambiguator.

## Problem coverage (P1–P13)

| # | Problem | Addressed by |
|---|---|---|
| P1 | Transparent bleed-through (no opaque bg) | Phase 1 surfaces |
| P2 | Three identical `sandbox` rows | Phase 2 short id + 2-line rows |
| P3 | Last-active renders `—` for all | Phase 2 created-age fallback + seed LastActivity |
| P4 | Model + ctx% absent from rows | Phase 2/3 model slot + ctx% |
| P5 | No glyph legend | Phase 2 `─ legend ─` row |
| P6 | Failed sessions absent from attention | Phase 2 attentionDot += Failed |
| P7 | "USAGE" shows status, not tokens/cost | Phase 1 deletes the box; real cost in detail (Phase 3) |
| P8 | NEEDS YOU box can't collapse | Replaced by in-list dots + detail block |
| P9 | CLUSTER backend hardcoded `claude-sdk` | Phase 1 cluster strip derives from sessions |
| P10 | Zero-value bars look like noise | Phase 1 removes the bars |
| P11 | Footer low contrast (`colorTextDim`) | Phase 1 `colorTextMuted` |
| P12 | Border colors reuse status accents | Phase 1 neutral borders |
| P13 | Counts triple-computed; attention inert | Phase 2 shared `statusCounts`; detail action block |

## Open follow-ups
- **Seed `LastActivity`** from the index / last event at session build so P3's
  fallback isn't always created-age. Check the seed path (sort already reads
  `State.LastActivity`, `sort.go:91`).
- Two-line rows roughly halve sessions-per-screen before the overflow band; fine
  for a handful of sessions, revisit if fleets grow.
- Optional: cluster-strip aggregate cost once Phase 3 lands (sum `TotalCostUSD`).

## Definition of done & convergence loop

Built so a `/goal` run can be re-run until runs converge. Each run re-derives
status from the **actual code** (not a stored ledger), advances the next gap,
verifies, then reports a fixed verdict.

**Each run:** (1) gap-analyze the code against this doc — every phase (1–4) and
every row in the P1–P13 table needs an implementation *and* a test; anything
missing either is a gap. (2) Fix the highest-priority gaps in phase order,
matching existing `kit`/lipgloss patterns and adding/updating tests + golden
snapshots. (3) Run `just check` until green (sandbox caveat: run
`internal/runner` + `internal/models` with the command-sandbox disabled, per
`docs/verification-protocol.md`). (4) Adversarially audit — prove each item with
a `file:line` + passing test; preferably fan out 2–3 independent reviewer
subagents and only pass an item if they agree. (5) Update this doc's status line
+ the `TODO.md` pointer.

**Done when ALL hold:** `just check` green; every P1–P13 row and every phase
deliverable has a `file:line` + passing test; `renderDetailLines` is reachable
from `View()`/`renderZoned` (grep proof — not dead/test-only); no `*.gen.*`
hand-edits; no pre-existing dashboard test regressed.

**Convergence rule:** a run is **converged** iff it made *no code changes*, gates
are green, and the audit found no *meaningful* gap — where meaningful =
missing/incorrect behavior, a failing gate, dead code, or a deliverable with no
test. Cosmetic/subjective nits (pixel nudges, wording, alternate colors not
contradicting this doc) are **not** meaningful: list them as non-blocking and do
not act on them (prevents thrashing). Two consecutive runs both reporting
`CONVERGED: YES` with identical status = done.

**Report (verbatim each run):**
```
CONVERGENCE REPORT — dashboard Triage Console
Phase status: 1:<done|partial|todo> 2:… 3:… 4:…
P1–P13: <each PASS|FAIL with file:line + test, or a one-line summary of gaps>
renderDetailLines wired: <yes|no, grep proof>
Changes this run: <none | files>
just check: <PASS|FAIL>
Non-blocking nits: <none | list>
CONVERGED: <YES|NO>   Remaining gaps: <ordered list if NO>
```

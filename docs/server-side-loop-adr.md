# ADR: Server-side autopilot loop (runner-owned driver)

- **Status:** Proposed (Opus draft, 2026-07-05). Implementation gated on
  maintainer sign-off.
- **Scope:** TODO.md §1e item 6. Items 1–5 (the local `tea.Tick` driver:
  detach-durable `/goal` continuation, sentinel termination, lapse toast,
  idle-reaper warn, esc contract) are **already implemented** and do **not**
  depend on this ADR. This is the evolution to true laptop-closed autonomy.
- **Fable review (2026-07-04):** proceed with the ADR; recommended direction is
  a **runner-owned driver**. This document commits to that direction and answers
  the three must-answer questions the TODO flagged.

## Context

The headline autopilot use case (maintainer): set an agent loose iterating
through `TODO.md` with Opus, detach, and come back to finished work. Today the
driver is a `tea.Tick` in the local Bubble Tea TUI (`internal/tui/dashboard/`,
`autopilot.go` + `App.autopilotTick`). Detach is survived (ticks carry
session+gen and route to the retained warm model over the background SSE
client), so a loop keeps running **as long as the TUI process is alive**.

The remaining gap is structural: the driver lives in the laptop's TUI process.
Quitting the TUI, closing the laptop, or a long inter-iteration sleep all kill
the driver. "Work until `TODO.md` is done, overnight, laptop closed" is not
achievable while the clock that submits the next turn is client-side.

The runner already owns the control plane for a session: it holds the Claude
Agent SDK `query()` loop, the SQLite event log with SSE replay, the turns API
(`POST /sessions/:id/turns`), the audit log, and the idle clock the reaper reads.
It is the natural owner of a durable driver.

## Decision

Move the autopilot driver into the **runner**. The loop/goal spec is persisted
on the session; the runner self-submits the next turn on turn-completion plus an
interval; driver state transitions become a new normalized event type; the TUI
arms/disarms the driver and renders its state purely from events. The local
`tea.Tick` driver is **retired** as the primary mechanism but **kept as a
degraded fallback** for backends without a runner-side driver (see Q3).

### Considered options

1. **Keep the local `tea.Tick` driver only (status quo).** Rejected: cannot
   survive TUI exit / laptop-closed, which is the entire point of item 6. Items
   1–5 already wrung the maximum out of this approach.
2. **Detached headless CLI process on the laptop** (`sandbox loop --detach`
   spawning a background local process). Rejected: still laptop-bound (dies with
   the machine / sleep), duplicates the driver logic outside the runner, and
   leaves the audit/idle/event envelope only half-aware of the loop.
3. **Runner-owned driver (chosen).** The only option that is genuinely
   laptop-independent and keeps the loop inside the existing control-plane
   envelope (audit, events, idle, guards). Higher up-front cost (schema + runner
   + reaper interaction) but it is the correct home.

## Design

### 1. Driver spec persistence

Add an autopilot spec to session state, persisted on the PVC alongside
`session.json` (runner-side; `runner/src/session.ts`):

```jsonc
// autopilot spec (persisted per session)
{
  "kind": "loop" | "goal" | null,      // null = disarmed
  "prompt": "<the /loop or /goal prompt>",
  "sentinel": "GOAL_MET",              // completion marker scanned in assistant text
  "interval_ms": 0,                    // delay between iterations (0 = immediate)
  "overrides": { "model": "...", "effort": "...", "mode": "..." },
  "max_iterations": 200,               // hard guard (see Q2)
  "token_budget": null,                // optional hard token ceiling (see Q2)
  "iterations": 0,                     // progress counter
  "armed_at": "<RFC3339>",
  "gen": 7                             // monotonic; disarm/rearm bumps it, kills stale ticks
}
```

Arming/disarming is a small API surface. Two viable shapes:

- **(a) New endpoint:** `PUT /sessions/:id/autopilot` (arm/replace),
  `DELETE /sessions/:id/autopilot` (disarm). Clean separation; preferred.
- **(b) Turns-API extension:** a flag on `POST /sessions/:id/turns`
  (`{autopilot: {...}}`) that arms the driver as a side effect of the first
  turn. Fewer endpoints but conflates "submit a turn" with "arm a loop."

**Recommendation: (a).** Arming is a distinct lifecycle action from submitting a
turn; a dedicated resource keeps the turns API single-purpose and makes disarm a
first-class operation (needed for the esc/stop contract and reaper interaction).

### 2. Self-submitting loop

The runner drives the loop off its own turn-completion signal, not a wall clock
it has to poll:

1. On `arm`, if no turn is in flight, the runner submits the first turn
   (`prompt` + `overrides`), else it waits for the in-flight turn to complete.
2. On **turn completion**, the runner:
   - increments `iterations`;
   - scans the just-completed assistant text for `sentinel` → if met, transition
     to `stopped(reason: sentinel)` and emit the terminal event;
   - checks the guards (Q2): `iterations >= max_iterations` or token budget
     exhausted → `stopped(reason: budget)`;
   - otherwise schedules the next turn after `interval_ms` (a runner-side timer,
     which survives because the runner process is the pod).
3. Every self-submitted turn is a normal turn in the **same continuous SDK
   session** — identical to the local driver's semantics, so multi-hour runs
   lean on server-side compaction exactly as they do today (cross-ref §2b gap 4:
   fix the compaction ctx% signal before/alongside heavy loop usage).

`gen` guards against races: a disarm/rearm bumps `gen`; a scheduled tick that
fires with a stale `gen` is dropped. This mirrors the local driver's existing
session+gen tick guard, so the mental model carries over.

### 3. Driver state as a normalized event

Driver state transitions become a new event type through the canonical path:
**edit `schema/events.json`, run `just gen`**, commit the regenerated
`internal/session/eventtypes.gen.go` + `runner/src/events.gen.ts` (never
hand-edit `*.gen.*`). Proposed event:

```jsonc
// autopilot.state — payload
{
  "state": "armed" | "ticked" | "stopped",
  "kind": "loop" | "goal",
  "reason": "sentinel" | "budget" | "user" | "lapsed" | null, // set on stopped
  "iteration": 12,
  "gen": 7
}
```

- The runner emits `armed` on arm, `ticked` at each iteration boundary (carrying
  the iteration count for the TUI's progress chip), and `stopped` with a
  `reason` on termination.
- The TUI **renders the driver purely from these events** — the armed chip, the
  iteration counter, and the terminal toast/OS-notification (reusing the
  existing `autopilotToast`/`notifyIfBackgroundAttention` plumbing) all derive
  from `autopilot.state`, not local `tea.Tick` bookkeeping. This removes the
  read-model split the local driver has.
- Replay-safe: because it is a normalized event in the SSE log, a fresh
  `sandbox attach` catches up the driver's current state from `after=<seq>`
  exactly like any other state — no special hydration. (Respect the §1a
  replay-vs-live boundary: a replayed `stopped(sentinel)` must **not** re-fire
  the OS notification; only the flip-to-live one does.)

## Must-answer questions

### Q1 — Idle-reaper interaction

The per-session reaper Job suspends sessions the runner reports as idle (idle
clock in the runner; see the idle-reaper design). An armed driver between
iterations (especially with a non-zero `interval_ms`) would otherwise look idle
and get reaped mid-loop — killing the very autonomy this ADR enables.

**Decision:** an **armed driver marks the session non-idle** until it reaches
`stopped`. Concretely, `recomputeIdle()`/`idleStatus().turnActive` in
`runner/src/session.ts` treat "autopilot armed (kind != null and not stopped)"
as activity, the same way an in-flight turn does. When the driver transitions to
`stopped` (any reason), the session becomes idle-eligible again and the reaper
resumes its normal countdown.

**Guard against a wedged driver (cross-ref the §7a "bound stuck synthetic-busy"
follow-up):** a driver that is armed but has produced no turn-completion for N
minutes (e.g. a hung SDK call) must not keep the pod unreapable forever. Bound
it: armed + no iteration progress for N minutes → emit a warning event and allow
idle-eligibility (or auto-disarm with `reason: lapsed`). This reuses the
staleness-bound pattern already proposed for the opencode synthetic-busy case.

### Q2 — Runaway guard (hard max-iterations / token budget)

An unattended loop can run away (cost, or a sentinel that never fires). The spec
carries **both** a `max_iterations` (default e.g. 200) and an optional
`token_budget` hard ceiling. The runner enforces them at each turn-completion
check **before** scheduling the next turn; hitting either transitions to
`stopped(reason: budget)` and emits the terminal event (toast + OS notification
on the live client). These are hard stops, not advisories — the loop cannot
exceed them. The TUI surfaces the budget in the armed chip so the ceiling is
visible while running.

### Q3 — Retire or keep the local `tea.Tick` driver?

**Keep it as a degraded fallback, do not delete it.** Rationale:

- The runner-owned driver is Claude-backend-first (it lives in the Claude SDK
  turn loop). Backends driven through an external pane (opencode today, codex
  planned) may not have a runner-side self-submit loop on day one; the local
  driver remains their path until each backend grows a runner driver.
- The local driver is the natural fallback if the runner driver is unavailable
  (older runner image, feature-flag off).

**Precedence:** when a session has a runner-side driver available, the TUI arms
**that** and does **not** start a local `tea.Tick`; it renders from
`autopilot.state` events. The local driver is entered only when the runner
reports no autopilot capability. A capability bit in `GET /sessions/:id/status`
(or the health/capabilities response) tells the TUI which path to take. This
avoids two drivers double-submitting turns.

## Consequences

- **Positive:** laptop-closed autonomy (the item's whole purpose); the loop
  lives inside the audit/event/idle envelope; the TUI read-model for autopilot
  collapses to "render from events" (removes the local driver's bespoke
  bookkeeping); `sandbox attach` from any device shows live driver state via
  replay.
- **Negative / cost:** a new event type (schema + `just gen` + both generated
  files) and a new endpoint; the runner gains a scheduler + guards + reaper
  coupling; two drivers coexist during the transition (mitigated by the Q3
  capability bit). The compaction ctx% signal (§2b gap 4) should land before
  heavy multi-hour use so ctx% isn't silently wrong.
- **Security/safety:** self-submitting turns run under the same permission mode,
  Bash guards, egress allowlist, and audit log as user-submitted turns — the
  driver does not bypass any gate. If the default mode moves to
  `bypassPermissions` (§2d open decision), the runaway guards in Q2 become the
  primary cost control for unattended runs.

## Rollout

1. Schema: add `autopilot.state` to `schema/events.json`; `just gen`; commit
   generated files.
2. Runner: spec persistence + `PUT/DELETE /sessions/:id/autopilot` + self-submit
   loop + guards + reaper `recomputeIdle` change + capability bit.
3. TUI: arm/disarm via the new endpoint when the capability bit is set; render
   the armed chip/iteration/terminal toast from `autopilot.state`; keep the
   local `tea.Tick` path for no-capability sessions.
4. Tests: runner unit tests for sentinel/budget/gen/reaper-non-idle; a
   conformance test that an armed session is not reaped between iterations; TUI
   test that the armed chip and terminal toast derive from `autopilot.state`.
5. Docs: document the endpoint in `docs/runner-api.md` and the event in the
   architecture doc's event-model section.

## Open items for maintainer sign-off

- Endpoint shape (a) vs (b) — recommendation (a).
- Default `max_iterations` / whether `token_budget` is required or optional.
- Whether the capability bit lives in `/status` or a dedicated
  `/capabilities` response.
- Interaction with the §2d yolo-default decision (guards become the main cost
  control if bypass is the default).

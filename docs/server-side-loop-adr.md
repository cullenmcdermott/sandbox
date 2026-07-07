# ADR: Server-side autopilot loop (runner-owned driver)

- **Status:** ACCEPTED (maintainer sign-off 2026-07-07) — all open items
  resolved as proposed in `docs/decision-proposals-2026-07-06.md` §1:
  endpoint shape (a) `PUT/DELETE /sessions/:id/autopilot`; `max_iterations`
  default **50** (always enforced), `token_budget` optional but shipped in
  v1; capability bit lives in `GET /sessions/:id/status`; H2 retry = 5
  attempts, backoff `max(interval_ms, 30s)` doubling capped at 5 min;
  Q1/H1 staleness bound N = 30 min; H4 anti-double-submit guard skipped in
  v1 (accepted skew risk). Implementation is unblocked (schema → runner →
  TUI → tests).
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
  "kind": "loop" | "goal",             // the driver flavour (never null; see "state")
  "state": "armed" | "stopped",        // explicit lifecycle field (see H3)
  "stopped_reason": null,              // set when state == "stopped": sentinel | budget | user | lapsed | error
  "prompt": "<the /loop or /goal prompt>",
  "sentinel": "GOAL_MET",              // completion marker scanned in assistant text
  "interval_ms": 0,                    // delay between iterations (0 = immediate)
  "overrides": { "model": "...", "effort": "...", "mode": "..." },
  "max_iterations": 200,               // hard guard (see Q2)
  "token_budget": null,                // optional hard token ceiling (see Q2)
  "iterations": 0,                     // progress counter
  "armed_at": "<RFC3339>",             // when the driver was armed (boot re-arm anchor; see H1)
  "last_completed_at": null,           // RFC3339 of the last completed self-submitted turn (H1 anchor)
  "gen": 7                             // monotonic; disarm/rearm bumps it, kills stale ticks
}
```

**Stop is an explicit `state` field, not the absence of a spec (H3).** Earlier
drafts implied "disarmed" meant `kind == null`, but the idle rule below phrases
activity as "kind != null and not stopped" — two different notions of stop. We
resolve it in favour of one explicit field: the spec always carries a
non-null `kind` once armed, and `state` alone distinguishes a live driver
(`armed`) from a terminated one (`stopped`, with `stopped_reason`). The spec is
**not** deleted on stop — it is retained with `state: "stopped"` so that
`sandbox attach` and reaper-side idle logic can read a stable terminal record
instead of inferring stop from a missing key. `DELETE /sessions/:id/autopilot`
therefore sets `state: "stopped"` (reason `user`) rather than erasing the spec;
a subsequent arm overwrites it wholesale and bumps `gen`. Every downstream rule
(idle computation §Q1, boot re-arm H1) keys off `state == "armed"`, never off
`kind`.

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

#### Self-submit failure path (H2)

Steps 1–2 above assume the self-submitted turn is accepted and runs to
completion. It may not, and silent death is not acceptable for an overnight run.
Three failure classes, each with a defined policy:

- **409 — a manual user turn is in flight.** The turns route already rejects a
  concurrent start with 409 (`runner/src/server.ts:66`). When the driver's
  self-submit loses that race, it is **not** an error: the user is actively
  driving. The driver does not increment `iterations` or disarm; it defers,
  hooking the same turn-completion signal it uses normally, and schedules its
  next attempt after the user's turn completes (plus `interval_ms`). A manual
  turn thus transparently counts as "the loop got its iteration for free" —
  the driver re-evaluates sentinel/guards against that turn's output on
  completion like any other.
- **Transient StartTurn / SDK errors** (SDK stream error, model 429/5xx, a
  dropped `query()`): bounded retry with exponential backoff — e.g. up to 5
  attempts, backoff `interval_ms` growing 2× per try capped at a few minutes.
  Each retry is `gen`-guarded so a disarm cancels the ladder. Retries do **not**
  count against `max_iterations` (they produced no completed turn).
- **Retry ladder exhausted, or a non-retriable error:** the driver transitions
  to `stopped(reason: error)`, persists `state: "stopped"` + `stopped_reason:
  "error"`, and emits the terminal `autopilot.state` event (toast +
  OS-notification on the live client, same as any terminal reason). The failure
  detail is written to the audit log. The loop never dies without leaving this
  record.

#### Durability across a runner restart (H1)

The spec is PVC-persisted, but the inter-iteration timer and the
turn-completion signal are in-memory. A pod eviction, node drain, or runner
process crash with an armed spec would otherwise **silently kill the loop** —
directly defeating the "overnight, laptop closed" goal — and, worse, the Q1
staleness bound (no iteration progress for N minutes) could then convert the
armed-but-unserviced spec to auto-disarm/reap. The runner must re-arm itself on
boot:

1. **On startup**, after loading session state, if the persisted spec has
   `state == "armed"`, the runner treats the driver as live again. It emits an
   `autopilot.state` event with `state: "armed"` (a *resumed-armed* signal —
   same shape, so replay clients and a fresh `sandbox attach` re-render the
   armed chip without special-casing) and schedules the next turn.
2. **Interval anchoring:** the next turn is scheduled from
   `last_completed_at + interval_ms`, **not** from boot. Anchoring on the last
   completed turn preserves the user's intended cadence across a restart (a
   crash 30s into a 10-minute interval resumes ~9.5 minutes later, not a fresh
   10 minutes) and prevents a crash-loop from hammering the model with
   back-to-back turns. If `last_completed_at + interval_ms` is already in the
   past (the common case for `interval_ms: 0` or a long outage), the turn fires
   immediately. If no turn has completed yet (`last_completed_at == null`, crash
   between arm and first completion), anchor on `armed_at` instead.
3. **The Q1 staleness clock resets at boot** so a restart is never itself
   counted as "no progress for N minutes" — otherwise every restart would race
   the lapse bound.

The reciprocal hazard is a crash **before** emitting `stopped`. Because the TUI
renders the driver purely from `autopilot.state` events (§3), a client that saw
`armed`/`ticked` but never the terminal event would render "armed" forever after
a crash. Two mechanisms close this: (a) the boot re-arm above re-emits `armed`
only when the persisted `state` is still `armed`, so a driver that *did* reach a
terminal state before crashing (its `state: "stopped"` is on the PVC) re-emits
nothing and stays stopped; (b) the terminal transition **persists `state:
"stopped"` before (or atomically with) emitting the `stopped` event**, so the
durable record and the event stream cannot disagree — a crash in the window
leaves `state: "armed"`, and boot re-arm correctly resumes rather than
half-stopping. Replay clients therefore never strand on a phantom "armed": they
either see the real terminal event, or the session genuinely is still armed and
gets a fresh resumed-`armed` on boot.

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
  "reason": "sentinel" | "budget" | "user" | "lapsed" | "error" | null, // set on stopped (mirrors stopped_reason, incl. H2's error)
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
`runner/src/session.ts` treat "autopilot `state == "armed"`" as activity
(keying off the explicit lifecycle field from §1 / H3, never off `kind`), the
same way an in-flight turn does. When the driver transitions to `stopped` (any
reason), the persisted `state` flips to `"stopped"` and the session becomes
idle-eligible again — the reaper resumes its normal countdown. Because this
reads the PVC-persisted `state`, it is correct across a runner restart: the boot
re-arm (H1) keeps a genuinely-armed spec non-idle, while a spec that already
reached `stopped` before the crash stays idle-eligible.

**Guard against a wedged driver (cross-ref the §7a "bound stuck synthetic-busy"
follow-up):** a driver that is armed but has produced no turn-completion for N
minutes (e.g. a hung SDK call) must not keep the pod unreapable forever. Bound
it: armed + no iteration progress for N minutes → emit a warning event and allow
idle-eligibility (or auto-disarm with `reason: lapsed`). This reuses the
staleness-bound pattern already proposed for the opencode synthetic-busy case.
The staleness anchor is `max(last_completed_at, runner boot time)` (with
`armed_at` standing in before the first completion) — the boot-time floor is
what "resets on a runner restart" (H1 step 3) means concretely, so a pod bounce
after a long outage is never itself mistaken for a wedged driver and lapsed
prematurely.

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

**Version skew — the capability bit only protects capability-aware clients
(H4).** The anti-double-submit precedence above assumes the client *reads* the
capability bit. An older CLI that predates this ADR knows nothing about it: it
would arm a server-side driver (if it hits the endpoint) or, more likely, just
start its own local `tea.Tick` loop against a session whose runner is also
armed — two drivers, double submits. For a single-maintainer tool this is an
**accepted risk**: the maintainer controls both ends and upgrades the CLI
alongside the runner, so a skewed pair is transient. If cheap insurance is
wanted, the server side can defend unilaterally: while `state == "armed"`, the
runner rejects an *externally* submitted turn that is not tagged with the
driver's current `gen` (or simply treats the armed driver as the sole
submitter and 409s a foreign self-submit), so a legacy local loop's turns
bounce off the same 409 machinery a manual turn does rather than interleaving.
We note the mitigation but do not require it for v1.

## Consequences

- **Positive:** laptop-closed autonomy (the item's whole purpose); the loop
  lives inside the audit/event/idle envelope; the TUI read-model for autopilot
  collapses to "render from events" (removes the local driver's bespoke
  bookkeeping); `sandbox attach` from any device shows live driver state via
  replay. The boot re-arm (H1) makes the loop survive a pod eviction / node
  drain / runner crash, so durability now matches the "overnight" promise
  end-to-end (spec, timer, and completion signal all recovered), not just the
  spec.
- **Negative / cost:** a new event type (schema + `just gen` + both generated
  files) and a new endpoint; the runner gains a scheduler + guards + reaper
  coupling + boot re-arm + a failure/retry ladder; two drivers coexist during
  the transition (mitigated by the Q3 capability bit, with the accepted
  version-skew residual in H4). The compaction ctx% signal (§2b gap 4) should
  land before heavy multi-hour use so ctx% isn't silently wrong.
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

The design commitments made above are **not** open questions: stop is an
explicit `state` field (H3); the runner re-arms on boot and anchors the next
turn on `last_completed_at` (H1); a self-submitted turn that 409s against a
manual turn defers, transient errors get bounded-backoff retry, and exhaustion
disarms with `reason: error` (H2); and the capability-bit skew is an accepted
single-maintainer risk (H4). What remains open is only the tuning of those
committed mechanisms plus the pre-existing choices:

- Endpoint shape (a) vs (b) — recommendation (a).
- Default `max_iterations` / whether `token_budget` is required or optional.
- Whether the capability bit lives in `/status` or a dedicated
  `/capabilities` response.
- Interaction with the §2d yolo-default decision (guards become the main cost
  control if bypass is the default).
- Concrete constants for the committed policies: the H2 retry ceiling / backoff
  cap, and the Q1/H1 staleness bound `N` (minutes) after which an armed driver
  with no completion is lapsed.
- Whether to ship the optional H4 server-side anti-double-submit guard in v1 or
  accept the skew risk bare.

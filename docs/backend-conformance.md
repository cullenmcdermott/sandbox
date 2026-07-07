# Backend conformance checklist

The canonical, testable definition of "a backend is at parity." Every agent
backend (claude-sdk, opencode, Codex) must satisfy **both** surfaces below. This is
the spec the shared harnesses enforce; see `docs/archive/testing-parity-plan.md` for the
rollout plan and the current status matrix.

A backend is onboarded by **filling its column**, not inventing tests: add it to
each shared harness's table and make the rows green.

## Shared harnesses (where each row is checked)

| Harness | Surface | Location | Add a backend by |
|---|---|---|---|
| `assertMapperInvariants(events)` | backend (unit) | `runner/test/backend-contract.ts` | feeding the backend's captured mapper output through it |
| `backendCases` table | backend (live) | `internal/k8sit/local_test.go` | appending a `backendCase` row |
| `renderBackendTranscript` / `backendTranscriptCases` | frontend (golden) | `internal/tui/dashboard/golden_multiturn_test.go` | appending a row → a per-backend golden |
| `driveBackendTurn` (interactive) | frontend | _Phase F (planned)_ | — |

## Backend surface (runner adapter)

The adapter implements `Agent.runTurn` (`runner/src/agent.ts`) and maps the agent's
native events into the normalized model (`schema/events.json`).

- [ ] **Event mapping (unit)** — native events → normalized events, per type, with
      correct payloads. (claude `mapping.test.ts`, opencode `opencode-turn.test.ts`)
- [ ] **Mapper invariants (unit)** — routes its captured output through
      `assertMapperInvariants`: ≤1 terminal (turn.completed XOR turn.failed); no
      content event after a terminal; every `message.*` role ∈ {user, assistant};
      `turn.failed` carries a non-empty `message`.
- [ ] **Turn lifecycle (unit)** — `turn.started` once at the top; `finishTurn`
      ALWAYS runs (every return/throw/abort path); settle-once (no double terminal).
- [ ] **Abort / interrupt (unit)** — abort stops the turn and does NOT emit a second
      `turn.interrupted` (the `/interrupt` route owns it); `finishTurn` still runs.
- [ ] **Hang / deadline safety (unit)** — an out-of-band settle (failed prompt,
      stuck server) cannot block the loop forever; an absolute deadline backstops.
- [ ] **Model selection** — per-turn model id parsed/resolved; default when empty.
- [ ] **Permission flow** — a tool requiring approval surfaces `permission.requested`
      and resolves (or auto-responds) — bounded, never hangs.
- [ ] **Resume / continuity** — conversation continuity across turns; survives pod
      restart (persisted session id); honors the `resume` arg.
- [ ] **Error surface** — auth failure / bad model → `turn.failed` with a reason,
      no wedge.
- [ ] **Live turn (k8sit)** — real reply when keyed/free; plumbing-only fallback
      (assert the runner accepted + began the turn) when no key.
- [ ] **Live interrupt + lifecycle ops (k8sit)** — interrupt returns the session to
      idle; suspend→resume→turn; destroy idempotent; reconnect/`after=seq` replay.
- [ ] **Schema conformance** — every emitted event is a valid `EventType` with a
      schema-valid payload (replay check).

## Frontend surface (TUI)

How the dashboard presents the backend. Because all backends emit normalized
events, the goal is to render them through the **one** transcript renderer.

- [ ] **Transcript render (golden)** — message/tool/reasoning/usage/turn-footer
      blocks render correctly from the backend's events (`renderBackendTranscript`).
- [ ] **Interactive turn** — input → submit → `StartTurn` → streamed events →
      render updates → reply shown (Model/`teatest` drive).
- [ ] **Adverse-state frames** — permission modal, error block, plan/tool cards,
      streaming — all golden-guarded.
- [ ] **Status line / metrics parity** — ctx% · usage · rate_limit · model ·
      workspace render the same for every backend (fed by the runner observer).
- [ ] **Startup / connecting UX** — connecting preview / attach progress.
- [ ] **Reconnect / SSE replay UI** — stage updates, give-up handling.
- [ ] **Detach / keybindings / interrupt UX (the parity bar)** — Ctrl-] detach, key
      bindings, and interrupt behave identically across backends.
- [ ] **External-pane lifecycle** (if the backend also offers a PTY pane) —
      attach/detach/resize/close-reap + the chrome around it.

## The gate

Codex (or any new backend) is "tested to parity" only when **both** lists are
green for its column. A red row is a tracked gap, never a silent omission.

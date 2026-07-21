# Backend conformance checklist

The canonical, testable definition of "a backend is at parity." Every agent
backend (claude-pane, opencode, Codex) must satisfy **both** surfaces below.
This is the spec the shared harnesses enforce; see
`docs/archive/testing-parity-plan.md` for the original rollout plan.

Since claude-pane-first every backend is **supervised + observed**: the agent's
own interactive client is the product surface, the runner is the supervisor and
the single metrics source (the agent parity bar), and normalized events come
from an observer, not a turn adapter. `opencode-server` additionally keeps a
turn adapter (`Agent.runTurn`, `runner/src/agent.ts`) for its headless first
turn — the only backend that accepts `POST /turns`.

A backend is onboarded by **filling its column**, not inventing tests: add it to
each shared harness's table and make the rows green.

## Shared harnesses (where each row is checked)

| Harness | Surface | Location | Add a backend by |
|---|---|---|---|
| `assertMapperInvariants(events)` | backend (unit) | `runner/test/backend-contract.ts` | feeding the backend's captured observer/mapper output through it |
| `backendCases` table | backend (live) | `internal/k8sit/local_test.go` | appending a `backendCase` row |
| feed goldens + App nav tests | frontend | `internal/tui/dashboard/feed_test.go` (TestGoldenFeed, TestAppViewFeedNavigation) | extending the backend-parameterized cases |
| pane e2e smoke | seam | `internal/e2e/pane_e2e_test.go` (`//go:build e2e`) | — (transport-level; backend-agnostic) |

## Backend surface (runner supervisor + observer)

The supervisor owns the agent process; the observer maps its native signals
into the normalized model (`schema/events.json`).

### Provisioning (pane backends)

- [ ] **Auth/state materialization** — boot materializes the agent's full
      credential/state documents from the per-session Secret, idempotently and
      **only-if-absent** (an agent-refreshed credential survives a runner
      restart); fail-closed when material is missing (claude-pane:
      `.credentials.json` + `.claude.json` seed + settings merge,
      `runner/src/claude-config.ts`).
- [ ] **Env hygiene** — the agent child sees an allowlisted env: no runner
      token, no credential env vars (hooks inherit the child env — this is the
      secret-hygiene boundary).
- [ ] **Zero-dialog start** — a fresh session reaches the agent's composer
      with no onboarding/trust dialogs (live validation).

### Supervisor lifecycle

- [ ] **Lazy spawn + resume chain** — first pane attach spawns with a pinned
      session id; child exit / pod resume respawns with the agent's own resume
      flag on the persisted id (conversation continuity).
- [ ] **Detach survival** — client disconnect never kills the agent process;
      reattach replays bounded scrollback; single attacher with preemption.
- [ ] **Exit recording** — child exit records the reason, emits status, and
      synthesizes a turn-abort for an open turn (graceful-only hooks cannot
      report a crash).

### Observer events

- [ ] **Event mapping (unit)** — native signals → normalized events, per type,
      with correct payloads (claude-pane `claude-pane-observer.test.ts`,
      opencode `opencode-turn.test.ts` / observer tests, codex
      `codex-observer` tests).
- [ ] **Mapper invariants (unit)** — routes its captured output through
      `assertMapperInvariants`: ≤1 terminal (turn.completed XOR turn.failed);
      no content event after a terminal; every `message.*` role ∈
      {user, assistant}; `turn.failed` carries a non-empty `message`.
- [ ] **Permission lifecycle** — a real permission prompt surfaces
      `permission.requested` (→ dashboard waiting/attention) and clears on
      in-agent resolution or turn end; no field fabrication.
- [ ] **Metrics** — model (re-emitted on change, deduped), usage/ctx%, rate
      limits, session title flow as normalized events; the runner is the
      metrics source for every backend.
- [ ] **Schema conformance** — every emitted event is a valid `EventType` with
      a schema-valid payload (replay check).

### Turn adapter (opencode headless only)

- [ ] **Turn lifecycle / abort / deadline (unit)** — `turn.started` once;
      `finishTurn` always runs; settle-once; abort does not double-emit
      `turn.interrupted`; an absolute deadline backstops hangs.
- [ ] **409 contract (every other backend)** — `POST /turns` and any
      programmatic-control route answer 409 for supervise-only backends.

### Auth seed (opencode)

The opencode auth store is a map of independent per-provider entries with **no**
`last_refresh` timestamp (unlike codex's whole-file store), so a conformant
runner must:

- [ ] **Materialize the store** — write the injected `OPENCODE_AUTH_JSON`
      document (the host-harvested `auth.json` from the per-session Secret) to the
      on-disk store at `$XDG_DATA_HOME/opencode/auth.json` (mode `0600`); an
      absent env var touches nothing (the provider-API-key fallback applies)
      (`materializeOpencodeAuth`, `runner/src/agent-auth.ts`).
- [ ] **Per-entry refresh-preserving merge** — reconcile PER PROVIDER ENTRY
      against an `auth.json.seed-hashes` sidecar (the sha256 of each seed entry,
      since opencode entries carry no timestamp to compare): an **unchanged** seed
      entry keeps the on-disk entry (a pod-side token refresh survives), a
      **changed** seed entry (operator reseed, or no recorded hash) wins, and
      entries present only on disk are preserved.
- [ ] **Fail-closed gate before serve** — refuse to start `opencode serve`
      unless the selected provider (`SANDBOX_OPENCODE_PROVIDER`) has an entry in
      the store **or** its fallback API-key env var is set, surfacing the reason
      (never the credential) (`assertOpencodeAuthUsable`).
- [ ] **Child env scrub** — the serve child's env drops both
      `OPENCODE_AUTH_JSON` and `OPENCODE_AUTH_CONTENT` (opencode's `Auth.all()`
      prefers `OPENCODE_AUTH_CONTENT` over the on-disk store, so a leak would
      shadow the materialized store and break refresh persistence).
- [ ] **Version-pinned opencode** — the runner image pins the opencode CLI
      (`OPENCODE_VERSION`, `runner/Dockerfile`) to match the host client.

This mirrors codex's `codex-auth-json` → `CODEX_AUTH_JSON` → `$CODEX_HOME/auth.json`
seed; both backends' materializers consume the shared `AuthFs` +
`writeAuthFile0600` helpers in `runner/src/agent-auth.ts` (codex keeps its
whole-file materializer in `codex.ts`; opencode's per-entry `materializeOpencodeAuth`
lives in `agent-auth.ts`).

### Idle probe

- [ ] **Pane attach counts as activity** — attached pane clients hold the
      session non-idle.
- [ ] **Synthetic busy** — observer sets busy on turn-start, idle on
      Stop/exit/shutdown; observer-event staleness clock refreshes on every
      ingested event (a detached-but-working agent is never reaped mid-turn).

### Live (k8sit)

- [ ] **Live lifecycle ops** — suspend→resume→continuity; destroy idempotent;
      reconnect/`after=seq` replay.

## Frontend surface (TUI)

How the dashboard presents the backend. All backends render through the same
backend-agnostic surfaces: the session list/status rows, the read-only
activity feed, and the external pane.

- [ ] **Feed render (golden)** — prompts, streamed assistant text, one-line
      tool entries, calm notices render from the backend's events
      (TestGoldenFeed; backend-parameterized nav in
      TestAppViewFeedNavigation).
- [ ] **Status row / metrics parity** — title · model · status · ctx% · cost
      render the same for every backend (fed by the runner observer).
- [ ] **Attention routing** — observer-emitted `permission.requested` floats
      the row / fires the toast; clears on activity.
- [ ] **Startup / connecting UX** — connecting splash / attach progress.
- [ ] **Reconnect / SSE replay UI** — stage updates, give-up handling.
- [ ] **Detach / keybindings / interrupt UX (the parity bar)** — Ctrl-]
      detach, leader chords, and minimize behave identically across backends.
- [ ] **External-pane lifecycle** — attach/detach/resize/close-reap over the
      backend's transport (child process or WebSocket) + the chrome around it.

## Codex — Phase 1 status

The `codex-app-server` backend is being onboarded in waves. **Phase 1 is Go-side
plumbing only** and deliberately leaves most rows above red:

- **Delivered (Phase 1):** backend id (`codex-app-server`), the ChatGPT-OAuth
  auth.json credential contract (per-session Secret `codex-auth-json` +
  `sandbox.cullen.dev/codex-account` label, **fail-closed** — the pod refuses to
  start if neither the account auth.json nor the shared `OPENAI_API_KEY` is
  present), `CODEX_HOME` PVC-persisted state, the `codex app-server` websocket
  port-forward (`ForwardSpecsWithCodex`, port 8788), and the `sandbox codex`
  command. The pod supervises `codex app-server` (runner-side, landing
  separately); metrics come from a runner observer connection, and activity/idle
  is the runner's signal — same source every backend uses.
- **Deferred (later waves):** the interactive/external codex pane (it rides
  the same PaneTransport seam claude-pane landed) and the frontend feed/golden
  rows. Programmatic turns are no longer a goal for pane-class backends —
  `POST /turns` answers 409 for codex, matching claude-pane.

Every deferred item stays a tracked red row below — never a silent omission.

## The gate

Codex (or any new backend) is "tested to parity" only when **both** lists are
green for its column. A red row is a tracked gap, never a silent omission.

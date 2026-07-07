# Phase 3 spike: opencode 1.17.7 server API → runner turn adapter

Empirical findings from running `opencode serve` (1.17.7, from the `sandbox-runner:dev`
image) and introspecting its API. Source for the adapter in Phase 4. Regenerate the
OpenAPI any time with: `curl -fsS http://<serve-host>:4096/doc`.

## Headline results

- **A turn completes with NO provider key, on a free model, at $0.** `POST
  /session/{id}/message {"parts":[{"type":"text","text":"say hi"}]}` returned a
  finished assistant message: `"providerID":"opencode","modelID":"big-pickle",
  "finish":"stop","cost":0`. So **`opencode/big-pickle` is the free default** (the
  "big pickle" model). The opencode turn test needs **no secret** and costs nothing
  (works on KIND because kindnet doesn't restrict egress to the opencode endpoint).
- The OpenAPI spec is served at `GET /doc`; operationIds map 1:1 to the official
  `@opencode-ai/sdk` client method namespaces.
- `session.prompt` (the prompt POST) is **synchronous** — it returns the completed
  assistant message — while incremental events stream over `GET /event`.

## Key operations (operationId → SDK method)

| HTTP | operationId | SDK method | use |
|---|---|---|---|
| `POST /session` | `session.create` | `client.session.create()` | create the opencode session (once per runner session) |
| `POST /session/{id}/message` | `session.prompt` | `client.session.prompt()` | send the turn prompt (sync result) |
| `POST /session/{id}/prompt_async` | `session.prompt_async` | `client.session.promptAsync()` | async variant if we don't want to block |
| `GET /event` | `event.subscribe` | `client.event.subscribe()` | SSE event stream (async iterable) |
| `POST /session/{id}/abort` | `session.abort` | `client.session.abort()` | interrupt / abort the turn |
| `POST /session/{id}/permissions/{permissionID}` | `permission.respond` | `client.permission.respond()` | answer a tool-permission prompt |
| `GET /session/{id}/message` | `session.messages` | `client.session.messages()` | list messages (fallback) |

## Prompt request body (`session.prompt`)

```jsonc
{
  "parts": [{ "type": "text", "text": "<prompt>" }],   // required (TextPartInput)
  "model": { "providerID": "opencode", "modelID": "big-pickle" }, // optional; omit → free default
  "agent": "build",        // optional
  "tools": { "<tool>": true|false }, // optional enable/disable
  "system": "<system text>",         // optional
  "messageID": "msg..."              // optional (let server generate)
}
```
`TurnInput.Model` maps to `model` by parsing `"providerID/modelID"`; empty → omit
(free `opencode/big-pickle`).

## Event stream (`GET /event`) — SSE `data: {id,type,properties}`

Turn-relevant event types (observed during a real prompt):

| opencode event | properties | → normalized event |
|---|---|---|
| `session.created` / `session.updated` | `{info, sessionID}` | (context; session.title from info.title) |
| `session.status` | `{sessionID, status}` | session.status_changed (busy/idle) |
| `message.updated` | `{info, sessionID}` | usage.updated (info.tokens/cost); message role/finish |
| `message.part.updated` | `{part, sessionID, time}` | by `part.type`: `text`→message.completed; `reasoning`→reasoning.*; `tool`→tool.started/completed; `step-start`/`step-finish`→(ignore) |
| `message.part.delta` | `{delta, field, messageID, partID, sessionID}` | message.delta (field=`text`) / tool.delta |
| **`session.idle`** | `{sessionID}` | **turn.completed** (turn-done signal) |
| (error in message.info / stream) | — | turn.failed |

Noise to ignore: `server.connected`, `catalog.updated`, `plugin.added`,
`integration.updated`, `reference.updated`, `session.diff`.

## Adapter sketch (Phase 4)

`runner/src/opencode-turn.ts` implementing the `Agent` interface:

1. `createOpencodeClient({ baseUrl: http://127.0.0.1:<OPENCODE_PORT> })`.
2. Resolve an opencode session id for the runner session (create once, cache;
   reuse on subsequent turns / `resume`).
3. `emit turn.started`.
4. Subscribe to `client.event.subscribe()` (filter to our `sessionID`).
5. `client.session.prompt({ path:{ id }, body:{ parts:[{type:'text',text:prompt}], model }})`.
6. Map events → `appendEvent(...)` per the table; on `session.idle` for our session →
   `turn.completed`, then `finishTurn`. On error → `turn.failed`.
7. `abort` → `client.session.abort(...)` + `finishTurn` (never throw on abort).

**Gaps to document honestly:** permission flow (`permission.updated`/`permission.respond`)
— for headless turns default to auto-allow or rely on the free model not invoking
tools; token/cost come from `message.updated.info` (map to usage.updated). Wire
`selectAgent('opencode-server') → opencodeAgent` and drop the 409 in `server.ts`.

## Implementation notes (landed)

- **Role filtering (important):** opencode streams the *user prompt itself* as a
  text part on the *user* message, so a naive mapper echoes the prompt back as an
  assistant reply. `message.updated(role)` for a message always precedes that
  message's `message.part.updated` events, so the adapter records messageID→role
  and maps **only** parts of an assistant message.
- **Auth:** in the pod `opencode serve` requires HTTP Basic auth — user
  `opencode`, password = `OPENCODE_SERVER_PASSWORD` (verified: only
  `opencode:<pw>` returns 200). The adapter sends that header; unset → no auth
  (unsecured local serve, e.g. tests).
- **Turn completion** is driven off `session.idle` (not the synchronous prompt
  result), with any open text part flushed to `message.completed` on idle.
- **Streaming deltas (DONE, Phase G):** opencode emits a separate
  `message.part.delta` event type (not in the SDK's typed `Event` union). The
  adapter now casts and handles it before the typed switch: a text-field delta for
  an assistant part of our session emits `message.started` (once) + `message.delta`.
  The full-text `message.part.updated` still drives `message.completed`; a
  deltas-only part flushes its accumulated text on `session.idle`. Live-verified:
  a real big-pickle turn emitted 35 `message.delta` events.

## Hardening (post adversarial review)

The happy path hid several latent defects; all fixed and unit-tested:
- **No-wedge on out-of-band settle (was a blocker):** the turn is driven by
  `for await (stream)`, which only wakes on a new SSE event. A synchronous prompt
  failure (bad model, server error) sets `settled` but would never wake the loop →
  `finishTurn` never runs → session wedged 'busy' forever (all future turns 409,
  reaper can't suspend). Fix: any out-of-band settle (prompt failure, deadline,
  abort) closes the SSE stream (`stream.return()`), which ends the loop so the
  `finally` always runs `finishTurn`.
- **Absolute deadline backstop:** `OPENCODE_TURN_DEADLINE_MS` (default 600s) aborts
  + fails a turn that never settles — covers a hung server and the
  permission-gated-tool case below.
- **No duplicate `turn.interrupted`:** `server.ts`'s `/interrupt` route already
  emits it; the adapter emits nothing terminal on abort (matches claude.ts).
- **Stale session recovery:** on a missing-session prompt error (404 / "session not
  found") the module-cached opencode session id is invalidated so the next turn
  recreates it instead of failing forever (e.g. after an independent
  `opencode serve` restart).
- **Idempotent tool terminal events; strict `session.error` session match; defensive
  token reads** (a partial `message.updated` can't throw a turn into failure).

**Closed in Phase G (was "still open"):**
- **Permission flow (DONE):** the pure mapper now maps `permission.updated` →
  normalized `permission.requested` (queued via `takePendingPermissions()`) and
  `permission.replied` → `permission.resolved`; `runTurn` drains the queue each
  loop iteration and auto-responds via `client.postSessionIdPermissionsPermissionId`
  (default `"once"` = auto-allow, `OPENCODE_AUTO_PERMISSION`-tunable; justified: the
  pod is the isolation boundary + matches the turn's bypassPermissions default).
  Bounded — the deadline still backstops a never-replied prompt. Unit-tested; not
  exercised live because big-pickle invokes no tools.
- **Continuity across pod restart (DONE):** the opencode session id is persisted as
  `opencode_session_id` in `session.json` (the opencode analogue of
  `claude_session_id`) via `reg.setOpencodeSession` / `clearOpencodeSession`.
  `ensureSession` resolves it through `effectiveOpencodeSession(resume, persisted)`
  (client `resume` wins, else the persisted head, else create). A missing-session
  prompt error clears the persisted head so the next turn recreates it. The
  module-scope `cachedSessionId` + `__resetOpencodeSessionForTest` were removed.
  Live-verified: a second sequential turn completed on the persisted session.

## Phase G adversarial review (verdicts)

A 4-lens adversarial review + verify pass over the Phase G diff surfaced these:
- **FIXED — `message.completed` could drop the reply (real).** `completeTextPart`
  preferred `textOf` via `textOf.get(id) ?? streamedText.get(id)`. When a text part
  is announced with an empty/partial `message.part.updated` (textOf=`''`, which is
  NOT nullish) and then filled only by trailing deltas, the `??` returned `''` and
  dropped the whole reply. Fix: take the LONGER of `textOf`/`streamedText` (both are
  cumulative toward the same string; concatenating — the reviewer's suggested fix —
  would double-count). Unit-tested (empty-first-update + trailing deltas).
- **FIXED — model-selection parity (`??` vs `||`).** `parseOpencodeModel(model ??
  cfg.model)` skipped the session default for an empty-string per-turn model; claude
  uses `||`. Extracted `effectiveOpencodeModel(turn, session)` (|| precedence) +
  test, mirroring claude's `resolveModel`.
- **DEFERRED to Phase B — no in-turn missing-session retry.** claude retries once
  in-turn on a stale resume id; opencode fails the turn and recovers next turn.
  NOT a G regression (the prior module-cache code behaved identically) and rare
  (sessions are PVC-durable via `XDG_DATA_HOME`), and the error-surface parity bar
  (clean `turn.failed`, no wedge) is met — so tracked as a Phase B enhancement, not
  a blocker. See TODO.
- **REFUTED — deltas dropped before `message.updated(role)`.** Precluded by the
  opencode ordering invariant the whole mapper relies on (`message.updated(role)`
  always precedes a message's part events), and the final content is `textOf`-
  authoritative regardless; not a real data-loss path.
- **REFUTED — `partID ?? messageID` merges distinct parts.** opencode always sends
  `partID` on a delta; the fallback is defensive, not a real collision.

## Phase C found: resume-after-restart hardening (fixed)

The k8sit lifecycle conformance test (suspend→resume→turn) caught two real bugs:
- **`ensureSession` resume path raced `opencode serve` boot.** It returned the
  persisted/resume id blind, so the FIRST turn after every suspend/resume failed with
  `opencode prompt error: fetch failed` (opencode serve, a child process, had not
  finished booting when the prompt fired — the runner was healthy but opencode was
  not). Fix: the resume path now probes `client.session.get({path:{id}})` with the
  SAME 20×500ms retry budget as the create path — absorbing the boot delay; a 404
  means the session is genuinely gone (clear + create fresh), a connection error
  means still booting (retry).
- **`Backend.Resume` returned ~10-15s early** (Go side, `internal/k8s/backend.go`) —
  `waitForPodReady` matched the OLD terminating-but-Ready pod, so even the readiness
  probe above couldn't help on the first try. See that file + `parity-RESUME.md`.

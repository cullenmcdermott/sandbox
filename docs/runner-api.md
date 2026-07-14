# Runner API Contract

The runner pod exposes an HTTP API on port 8787. All requests except
`GET /healthz` require a bearer token (`Authorization: Bearer <token>`). The
runner reads the expected token from the `RUNNER_TOKEN` env var (sourced from
the per-session Kubernetes Secret `<session-id>-runner`); if it is unset, every
non-`/healthz` request is rejected with 401. The CLI generates the token at
session-create time and reads it back from the Secret. SSE streams use
`text/event-stream` with `data: <json>\n\n` framing.

The runner serves a single session (its `SANDBOX_SESSION_ID`); requests whose
`:id` does not match that session return 404.

Request bodies are capped at 1 MiB: an oversized body returns **413** and a
body that is not valid JSON returns **400** (both `{"error": "..."}`); other
unexpected failures return 500.

## Endpoints

### `GET /healthz`
Returns 200 if the runner process is alive. No auth required. Body:
```json
{ "status": "ok", "protocolVersion": 2 }
```
`protocolVersion` is the runner's `PROTOCOL_VERSION`. Because this route needs no
bearer token, a client can hit it before it has one to distinguish "no runner
listening" from "runner listening but protocol-skewed" (the CLI reads it in
`internal/runner/client.go`).

### `GET /sessions`
Returns a JSON array of session state objects. Because the runner serves exactly
one session per pod, this is always a single-element array whose element is the
same body as `/status` (below).

### `GET /sessions/:id`
Returns the session state for a single session — the **same body** as
`/sessions/:id/status` (one session per pod, so the two are interchangeable).

### `GET /sessions/:id/status`
Returns the runner's `session.json` state:
```json
{
  "id": "claude-sdk-7f3a",
  "backend": "claude-sdk",
  "projectPath": "/Users/you/git/my-project",
  "activity": "idle",
  "agentSession": "sdk-session-id",
  "lastTurnId": "turn-12",
  "activeTurnId": "",
  "lastActivity": "2026-06-18T22:30:00Z",
  "model": "claude-sonnet-4-5-20250929",
  "protocolVersion": 2,
  "capabilities": { "autopilot": true }
}
```
`activity` is the runner's turn-activity signal — `idle` | `busy` | `error`. It
is **distinct** from the k8s lifecycle status (`CREATING`/`RUNNING`/`SUSPENDED`/…),
which the runner does not report; the Go side keeps the two on separate fields
(`State.Activity` vs `State.Status`, the D9 one-vocabulary-per-field split).
`agentSession` is the backend's own resume id — the Claude SDK session UUID for a
`claude-sdk` session, or the opencode session id — one backend per session ⇒ one
resume id. (Both fields were renamed from `status`/`claudeSession` in the §8
De-Claude break; the CLI and runner ship together, so the wire rename lands on
both sides at once.)

`model` is **optional**: it is omitted until the runner has seen the model id in
the SDK's init message (i.e. before the first turn it is absent).

`capabilities` is the backend capability map the CLI reads to pick a code path.
`capabilities.autopilot` is **true** when this backend has a runner-side
autopilot driver (the server-side `/loop`-`/goal` loop — see
`PUT/DELETE /sessions/:id/autopilot` below): the TUI then arms **that** driver
and renders from `autopilot.state` events instead of running its local
`tea.Tick` loop, avoiding two drivers double-submitting turns. It is **false**
for backends without a runner driver (`opencode-server`, supervise-only), where
the TUI keeps its local driver. Today only `claude-sdk` reports `true`.

`lastTurnId` vs `activeTurnId`: `lastTurnId` is the most recently *started* turn
and persists after it finishes (it seeds the next turn id). `activeTurnId` is
the turn currently running — `""` when the session is idle — and is the signal
clients must use to decide whether there is a turn to interrupt.

### `GET /sessions/:id/idle`
Returns the session's idle state, consumed by the per-session reaper to decide
when to suspend. A session is idle when no turn is running **and** no SSE client
is attached; `idleSince` is the RFC3339 time it became idle, or empty while
active.
```json
{
  "turnActive": false,
  "attachedClients": 0,
  "idleSince": "2026-06-18T22:30:00Z"
}
```

### `POST /sessions/:id/turns`
Starts a new turn. Request body:
```json
{
  "prompt": "Fix the bug",
  "resume": "turn-11",
  "allowedTools": ["Read", "Edit", "Bash"],
  "mode": "acceptEdits",
  "model": "opus",
  "effort": "high"
}
```
`prompt` (non-empty string) is required; the rest are optional. `mode` is the
turn's tool-approval policy (Go: the owned `TurnInput.ApprovalPolicy` enum) — one
of `default`, `acceptEdits`, `plan`, or `bypassPermissions`; an empty or
unrecognized value defaults to `bypassPermissions` (§2d yolo default — the
sandbox pod is the isolation boundary, so an unpinned turn runs without per-tool
permission prompts). The runner maps this **per-backend**: the `claude-sdk`
backend applies it 1:1 as the SDK `permissionMode`; the `opencode-server` backend
does **not** honor it (its interactive client owns its own permission modal) and
ignores the field — a documented no-op, not a silent drop. The TUI pins the mode
explicitly and surfaces `bypassPermissions` as a distinct warning chip in its
status line. (`bypassPermissions` additionally requires the SDK's
`allowDangerouslySkipPermissions` gate, which the runner sets only for that
mode.) `model` is the per-turn model override (the in-session `/model` switch) —
an id or alias like `opus`, `sonnet`, `haiku`, or a full id; it wins over the
session default (`SANDBOX_MODEL`, set from `claude --model`), and an empty value
falls back to that default and then the account default. `effort` is the per-turn
reasoning-effort override (the in-session `/effort` switch) — one of `low`,
`medium`, `high`, `xhigh`, or `max`; it maps to the SDK's `options.effort`. An
empty or unrecognized value leaves effort unset (SDK adaptive-thinking default).
Effort is supported only on Fable 5 / Opus 4.6+ / Sonnet 4.6 and is silently
ignored (or downgraded) on other models. The TUI displays the `max` tier under
the label "ultracode", but the wire value sent here is always the real SDK enum.

Optional request header: `X-Sandbox-Trace-Id: <id>` — a client-side trace
correlation id (the Go client stamps its connect-flow tracer id on requests
when CLI-side tracing is on; see `client/trace.go`). If the runner itself has
`SANDBOX_TRACE` set and the id is well-formed (`[\w.-]{1,64}`), it logs
`trace: <id> turn.link turn=<turnId>` to the pod log, bridging the CLI's
connect spans and the runner's per-turn spans (`runner/src/trace.ts`).
Otherwise the header is ignored. It never affects the response.
Returns 200 with:
```json
{
  "turnId": "turn-13"
}
```
A missing/empty `prompt` (or a non-string prompt) returns **400**
`{"error": "prompt is required"}`. The runner rejects a turn with **409**
(`{"error": "..."}`) in three cases — the single-active-turn invariant (R4) plus
the backend gate — so a client must interrupt the active turn before starting a
new one:

| Condition | 409 `error` body |
|---|---|
| A registered runner turn is already active (any backend) | `a turn is already active; interrupt it before starting a new one` |
| An interactive `opencode-server` turn is in flight (surfaces only as `activity: busy` via the observer, no registered turn) | `the opencode session is busy; interrupt the active turn before starting a new one` |
| A supervise-only backend that has no `Agent` (accepts no runner turns) | `backend <name> does not accept runner turns` |

The first two are the authoritative `turnRejectReason` set (`runner/src/turns.ts`),
shared verbatim by the autopilot driver's self-submit gate; the third is the
no-Agent short-circuit in the route itself.

### `POST /sessions/:id/turns/:turn_id/interrupt`
Cancels an active turn. Returns 200 with `{"turnId": "turn-13"}`. Returns 404
(`{"error": "turn not found or not active"}`) if the turn is not found or no
longer active.

**Empty turn segment.** The `:turn_id` segment may be **empty**
(`…/turns//interrupt`): the client doesn't always know the live turn id when the
user hits esc (it can fire before `POST /turns` returns or the first SSE event
lands), so the TUI sends an empty segment. When the id is empty — or non-empty
but not a live turn — the runner falls back to the session's **sole** active turn.
The R4 single-active-turn invariant guarantees at most one, so "interrupt the
active turn" is unambiguous without an id. (For an `opencode-server` session whose
interactive turn runs inside `opencode serve` and thus never registers as a runner
turn, a `busy` session is aborted directly and returns 200 rather than 404.)

### `PUT /sessions/:id/autopilot`
Arms (or replaces) the runner-owned autopilot driver — the server-side
`/loop`-`/goal` loop that self-submits the next turn on turn-completion plus an
interval, so a loop keeps running with the laptop closed (see
[`server-side-loop-adr.md`](server-side-loop-adr.md)). Only backends that report
`capabilities.autopilot: true` (claude-sdk today) accept it; the rest return
**409** so the CLI falls back to its local `tea.Tick` driver. Request body:
```json
{
  "kind": "loop",
  "prompt": "keep working through TODO.md",
  "sentinel": "ALL_DONE",
  "intervalMs": 0,
  "overrides": { "model": "opus", "effort": "high", "mode": "acceptEdits" },
  "maxIterations": 50,
  "tokenBudget": null
}
```
`kind` (`loop` | `goal`) and `prompt` (non-empty string) are required; the rest
are optional. `sentinel` is a completion marker scanned in each turn's completed
assistant text — a match stops the loop (`''`/omitted disables it). `intervalMs`
is the delay between iterations (default `0` = immediate). `overrides` are
per-turn `model`/`effort`/`mode` applied to every self-submitted turn.
`maxIterations` is a hard iteration ceiling — **always enforced**, default `50`.
`tokenBudget` is an optional hard token ceiling (input+output summed across the
loop; `null`/omitted = no cap). Hitting either ceiling stops the loop
(`reason: "budget"`). Arming **overwrites** any prior spec wholesale and bumps
the driver `gen`. Invalid fields return **400** with a typed message (e.g.
`kind must be 'loop' or 'goal'`, `maxIterations must be a positive integer`).
Returns **200** with the `/status` body (its `capabilities`/state reflect the
now-armed driver). The driver submits the first turn immediately unless a turn
is already in flight, in which case it defers (a manual turn counts as a free
iteration). Driver transitions are emitted as `autopilot.state` events (see
[Event Types](#event-types)); the loop lives inside the same permission mode,
Bash guards, egress allowlist, and audit log as user-submitted turns.

### `DELETE /sessions/:id/autopilot`
Disarms the driver: sets the persisted spec to `state: "stopped"` with
`reason: "user"` (the spec is **retained**, never deleted, so attach + reaper
idle logic read a stable terminal record) and bumps `gen` so any pending tick is
dropped. Returns **200** with the `/status` body, or **404**
(`no autopilot spec to disarm`) when the driver was never armed. Backends without
a runner-side driver return **409** as for `PUT`.

While the driver is armed the session is reported **non-idle** on
`GET /sessions/:id/idle` (so the reaper leaves the pod alone between iterations);
it becomes idle-eligible again once the driver stops for any reason. A driver
armed with no turn-completion for 30 minutes is auto-stopped (`reason: "lapsed"`)
so a wedged loop can't keep the pod unreapable forever. On a runner restart, a
persisted `state: "armed"` spec is re-armed automatically (re-emitting `armed`
and re-scheduling the next turn anchored on the last completed turn).

### `POST /sessions/:id/permissions/:permission_id`
Resolves a permission request. The session and permission ids come from the URL,
so the body carries only the decision:
```json
{
  "allow": true,
  "scope": "once",
  "editedInput": ""
}
```
`allow` (boolean) is required; `scope` and `editedInput` are optional. `scope`
is one of `once` | `session` (defaults to `once`). When `allow` is true and
`editedInput` is non-empty it must be valid JSON (validated at the boundary; a
malformed edit returns 400 and leaves the permission pending so the client can
retry). Returns 200 with `{"permissionId": "...", "resolved": true}`.

Resolution is first-write-wins: the runner auto-denies a pending permission on
its internal deadline, an interrupt, or client detach. A POST that loses that
race returns **409** with
`{"error": "...", "permissionId": "...", "resolved": false, "reason": "expired"}`
instead of falsely claiming success.

> **`session` scope** is a **tool-name-level** grant: when an allow resolves with
> `scope: "session"`, the runner records a grant for that tool name and
> auto-allows every subsequent use of the same tool for the rest of the session
> (no further prompt). Grants are in-memory for the pod's lifetime — a pod
> restart re-prompts. The resulting `permission.resolved` event carries
> `decision: "allow-session"` (vs `"allow-once"` for `scope: "once"`).

### `POST /sessions/:id/exec`
Runs a one-shot shell command in the session's project cwd and returns its
captured output. Each call is independent — there is no persisted `cd`/env
between calls, and interactive programs are unsupported (stdin is closed).
Output is bounded (64 KiB per stream; overflow appends `…[output truncated]`)
and the command is killed after a 30s timeout (`exitCode` 124). Request body:
```json
{
  "command": "git status --porcelain"
}
```
`command` (non-empty string) is required. Returns 200 with:
```json
{
  "stdout": "…",
  "stderr": "…",
  "exitCode": 0
}
```
Every `/exec` call writes an entry to the audit log (`audit.jsonl`, tool
`Exec`), mirroring the PostToolUse(Bash) audit so the `!cmd` shell passthrough is
never an unaudited escape. Commands matching the runner's blocked-Bash patterns
are **refused pre-spawn** (never executed): the response carries
`"exitCode": 126` (constant `EXEC_BLOCKED_EXIT`) with the audit row tagged
`blocked: true`. Exit code `127` indicates a spawn / wrapper failure (the
process never ran). Successful commands report their real child exit code.

### `GET /sessions/:id/events?after=<seq>&passive=<0|1>`
SSE stream. Emits one `data: <json>` line per event. The stream stays open
until the client disconnects. Events strictly after the given sequence number
are sent; `after=0` replays the full log. If `after` is omitted, the stream
resumes from the latest sequence (new events only). A non-integer or negative
`after` returns 400. An `after` beyond the log's current head is clamped to the
head (the client tails live from there) rather than silently swallowing every
live event.

Replay notes: sequence numbers may have gaps — the runner compacts `*.delta`
events of turns older than the most recent N (`DELTA_COMPACT_KEEP_TURNS`,
default 2) once a turn completes; the non-delta events always survive, so a
full replay still reconstructs the transcript. Payloads of `turn.started`,
`tool.*`, `permission.*`, and role-`user` `message.*` events are
secret-redacted before they are persisted or broadcast (same masking as
`audit.jsonl`), so log, live stream, and replay all carry the masked form.

Each event is a JSON object:
```json
{
  "seq": 1842,
  "time": "2026-06-18T22:30:00Z",
  "sessionId": "claude-sdk-7f3a",
  "turnId": "turn-12",
  "type": "tool.completed",
  "payload": {}
}
```

The runner also writes a periodic SSE comment line, `: heartbeat\n\n`, to keep
the connection (and any port-forward) alive across idle gaps. Comment lines have
no `data:` field; clients ignore them.

**`passive=1`** marks a *status observer* — a stream that watches events without
holding the session open. A passive stream does **not** count toward the
attached-client total used for idle detection, so the reaper can still suspend an
otherwise-idle session that has only passive watchers attached (the dashboard's
background list stream and `sandbox trace` use this). It still occupies a
connection slot for the cap below.

**Stream cap (429).** The runner bounds the number of *concurrent* SSE clients
(passive and active alike). When the cap is reached, a new `/events` request is
rejected with `429` and `{"error":"too many concurrent event streams"}` rather
than fanning out unbounded.

## Planned endpoints (not yet implemented)

These are part of the intended contract but are **not** served by the current
runner (`runner/src/server.ts`) and have no client in `internal/runner`. Do not
depend on them yet.

- `GET /sessions/:id/diff` — current dirty diff for the session workspace.
- `GET /outputs` — generated output files.

## Event Types

`schema/events.json` is the canonical source of truth for the event-type strings
and payload shapes. The Go consts (`internal/session/eventtypes.gen.go`) and the
entire TS event model (`runner/src/events.gen.ts`) are generated from it; edit the
schema and run `just gen` (see "Event model" in `docs/architecture.md`). The
runner maps Claude Agent SDK messages into these normalized types before
persisting to `events.db` and streaming to clients.

**Subagent (Task) attribution:** every `message.*`, `reasoning.*`, and `tool.*`
event emitted from a dispatched subagent's stream carries
`parentToolUseId: <Task tool_use id>`; the field is absent on the main thread.
Clients must route parented events to that Task's presentation (the CLI nests
child tools and the live narration under the Task card) — never into the main
streaming transcript, where they would interleave with the main reply.

The `todo.updated` event surfaces the agent's plan/checklist. Payload:
`{ todos: [{ content, status, activeForm }] }`, where `status` is one of
`pending` | `in_progress` | `completed`. The runner emits it when the SDK uses
the `TodoWrite` tool; the CLI renders the list with a per-item status glyph.

The `autopilot.state` event reports a transition of the runner-owned autopilot
driver (armed via `PUT /sessions/:id/autopilot`). Payload (see
`schema/events.json` `AutopilotStatePayload`):
`{ state, kind, reason, iteration, gen }`. `state` is `armed` (arm — also
re-emitted on a boot re-arm so a fresh attach re-renders the armed chip via
replay), `ticked` (each iteration boundary, carrying the `iteration` count), or
`stopped` (termination, with `reason` one of `sentinel` | `budget` | `user` |
`lapsed` | `error`). `kind` mirrors the spec (`loop` | `goal`); `gen` is the
driver generation. The CLI renders the driver **purely** from these events (the
armed chip, iteration counter, and terminal toast/OS-notification), so a
*replayed* `stopped` must not re-fire the OS notification — only a flip-to-live
one does.

The `rate_limit.updated` event carries the claude.ai plan usage windows for the
status line. Payload (see `schema/events.json` `RateLimitPayload` for the
authoritative list): `{ available, subscriptionType, fiveHourUtil,
fiveHourResetsAt, sevenDayUtil, sevenDayResetsAt, sevenDayOpusUtil,
sevenDayOpusResetsAt, sevenDaySonnetUtil, sevenDaySonnetResetsAt }` —
utilizations are 0–100 and resets are RFC3339 (and may be empty when the SDK
reports null). The runner fetches this from the SDK's structured `/usage` data
(`Query.usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET`) once per
turn, fired on the SDK init message (the control channel is only open until the
turn's result closes stdin). `available` is `false` for API-key/Bedrock/Vertex
sessions where plan limits do not apply; the CLI then hides the windows rather
than fabricating values. `subscriptionType` (`pro`/`max`/`team`/`enterprise`)
lets the status line distinguish headless (empty), API-key, and subscription
sessions. The per-model `sevenDayOpus*` / `sevenDaySonnet*` fields are absent
(nil / undefined) unless the plan has a separate weekly cap for that model; the
status line surfaces the one matching the attached model. Best-effort: if the
experimental call fails, no event is emitted.

## Persistence

- `/session/state/sandbox/session.json` — session state (mutable, updated on
  each turn). Stamped with a `state_version` shape version on every save; a
  file written by a NEWER runner loads best-effort with a loud warning
  (unknown fields are preserved across the round-trip).
- `/session/state/sandbox/events.db` — SQLite append-only event log. One
  writer (the runner). Used for replay via `after=<seq>`. The schema version
  lives in SQLite's `user_version` and is read-compare-migrated on every open:
  an older database is upgraded step-by-step, and a database written by a
  NEWER runner is refused (the boot crashes into a visible CrashLoopBackOff)
  so a stale runner image cannot reinterpret state it does not understand.
- `/session/state/sandbox/audit.jsonl` — audit entries from PostToolUse hooks.

Inspect a running session's event log as a timeline with `sandbox trace <id>`
(`--json` for the raw events, `--since <seq>`/`--tool <name>` to filter) — it
streams the same `/events` data this contract serves.

## Debug logging

The CLI can emit a **structured JSON-line debug log** to stderr — one JSON object
per line, so it is greppable and `jq`-pipeable. There is no tracing dependency;
this is deliberately lightweight.

Enable it:

- **CLI:** pass `--debug` (e.g. `sandbox --debug trace <id>`) or set
  `SANDBOX_DEBUG=1`.

> The runner (`runner/src/**`) does **not** emit structured debug logs today —
> only ad-hoc `console.log`/`console.error`. Structured runner logging is
> tracked as **C10** in `docs/oss-launch/HARDENING-BACKLOG.md`; do not rely on
> `SANDBOX_DEBUG` taking effect inside the pod.

Each line is a single JSON object with this schema:

| field | type | notes |
|---|---|---|
| `time` | string (RFC3339) | when the record was emitted |
| `level` | string | `DEBUG` \| `INFO` \| `WARN` \| `ERROR` |
| `msg` | string | short, stable, human-readable event label |
| `component` | string | `cli` — so interleaved logs are attributable (runner emits no structured logs today; see C10) |
| *(extra)* | any | arbitrary structured key/values (e.g. `session`, `seq`, `count`) |

Example:

```json
{"time":"2026-06-22T14:30:05Z","level":"DEBUG","msg":"trace collected events","component":"cli","session":"alpha","count":7,"since":0}
```

Keep `msg` a fixed label and put the variable detail in structured fields (not
string-interpolated into `msg`) so logs stay greppable across runs.

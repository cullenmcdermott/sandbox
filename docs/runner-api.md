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
{ "status": "ok", "protocolVersion": 3 }
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
  "id": "claude-pane-7f3a",
  "backend": "claude-pane",
  "projectPath": "/Users/you/git/my-project",
  "activity": "idle",
  "agentSession": "9a1b2c3d-…-uuid",
  "lastTurnId": "turn-12",
  "activeTurnId": "",
  "lastActivity": "2026-06-18T22:30:00Z",
  "model": "claude-sonnet-4-5-20250929",
  "protocolVersion": 3,
  "capabilities": { "autopilot": false }
}
```
`activity` is the runner's turn-activity signal — `idle` | `busy` | `error`. It
is **distinct** from the k8s lifecycle status (`CREATING`/`RUNNING`/`SUSPENDED`/…),
which the runner does not report; the Go side keeps the two on separate fields
(`State.Activity` vs `State.Status`, the D9 one-vocabulary-per-field split).
`agentSession` is the backend's own resume id — the Claude Code session UUID for
a `claude-pane` session (what the supervisor passes to `claude --resume`), or the
opencode session id — one backend per session ⇒ one
resume id. (Both fields were renamed from `status`/`claudeSession` in the §8
De-Claude break; the CLI and runner ship together, so the wire rename lands on
both sides at once.)

`model` is **optional**: it is omitted until the backend observer has reported
a model id (i.e. before the first turn it is absent).

`capabilities` is the backend capability map. `capabilities.autopilot` is
**always false** since claude-pane-first removed the server-side autopilot
driver (revival path: headless `claude -p --resume`); the key is retained so
old clients still decode `/status`.

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
`prompt` (non-empty string) is required; the rest are optional. Since
claude-pane-first the only backend that accepts runner turns is
`opencode-server` (its headless first turn); every other backend answers 409
(see the table below). `mode` is the turn's tool-approval policy (Go: the owned
`TurnInput.ApprovalPolicy` enum) — one of `default`, `acceptEdits`, `plan`, or
`bypassPermissions`. The `opencode-server` backend does **not** honor it (its
interactive client owns its own permission modal) and ignores the field — a
documented no-op, not a silent drop. `model` is the per-turn model override —
an id or alias like `opus`, `sonnet`, `haiku`, or a full id; it wins over the
session default (`SANDBOX_MODEL`), and an empty value falls back to that
default and then the account default. `effort` is the per-turn reasoning-effort
override — one of `low`, `medium`, `high`, `xhigh`, or `max`; ignored by
backends that don't support it.

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
| A supervise-only backend that has no `Agent` (accepts no runner turns: `codex-app-server`, `claude-pane`) | `backend <name> does not accept runner turns` |

The first two are the authoritative `turnRejectReason` set (`runner/src/turns.ts`);
the third is the no-Agent short-circuit in the route itself.

> **Retired backend id.** `claude-sdk` is a retired backend id (claude-pane-first
> removed the SDK turn engine; see `selectAgent` in `runner/src/agent.ts`): a
> lingering old `claude-sdk` pod still boots and serves `/status` and `/idle`,
> but it has no `Agent` either, so `POST /turns` answers the same no-Agent 409
> rather than crashing.

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

> **Removed endpoints (claude-pane-first).** `PUT/DELETE /sessions/:id/autopilot`
> (the server-side `/loop`-`/goal` driver) and
> `POST /sessions/:id/permissions/:permission_id` (programmatic permission
> resolution) no longer exist — permission decisions happen inside the agent's
> own interactive UI, observed as `permission.requested`/`permission.resolved`
> events. Old clients calling them get a plain 404.

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
events (and legacy `tool.progress` rows from pre-pane logs) of turns older
than the most recent N (`DELTA_COMPACT_KEEP_TURNS`, default 2) once a turn
completes; the non-ephemeral
events always survive, so a full replay still reconstructs the transcript. Payloads of `turn.started`,
`tool.*`, `permission.*`, and role-`user` `message.*` events are
secret-redacted before they are persisted or broadcast (same masking as
`audit.jsonl`), so log, live stream, and replay all carry the masked form.

Each event is a JSON object:
```json
{
  "seq": 1842,
  "time": "2026-06-18T22:30:00Z",
  "sessionId": "claude-pane-7f3a",
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

### `GET /sessions/:id/pane` (WebSocket)

claude-pane sessions only (409 for every other backend; 404 for a wrong id;
401 — decided **before** the id match so a bad token cannot probe which id is
live — for a bad bearer token, all rejected pre-upgrade as plain HTTP). On
upgrade the socket carries the interactive `claude` child's PTY:

- **Binary frames** are raw PTY bytes in both directions: server→client is
  terminal output, client→server is keyboard/paste input written to the PTY
  master verbatim.
- **Text frames** are JSON control messages. Currently only
  `{"type":"resize","cols":N,"rows":N}` (positive integers; malformed frames
  are ignored). Resize is applied to the PTY (SIGWINCH), so the child reflows.
- **On attach** the server first sends the retained scrollback (a bounded
  ~256 KiB ring) as one binary frame, then live output. The interactive child
  is spawned lazily on the first attach ever (`--session-id <uuid>`) and
  resumed (`--resume <uuid>`) on every later spawn; the uuid is persisted in
  session.json.
- **Single attacher.** A new attach preempts the current one: the old socket
  is closed with code **4001**. When the interactive child exits, the attached
  socket is closed with code **4002** (the next attach respawns via
  `--resume`).
- **Backpressure.** A client that stops reading (its send buffer exceeds a
  4 MiB cap — the WebSocket analog of the SSE stream's buffered-bytes
  eviction) is closed with code **4003**. The child keeps running; the client
  reattaches and catches up from the scrollback replay.
- Detaching (closing the socket) leaves the child running; an attached pane
  counts as external activity for `GET /sessions/:id/idle`.

### `POST /observer/claude/hook`, `POST /observer/claude/statusline`

claude-pane telemetry ingestion (absent for other backends). The pod-side
provisioning installs Claude Code settings hooks and a statusline command
whose helper scripts POST their stdin JSON here; the runner's observer
translates them into normalized events on the same log/SSE channel as every
other backend (turn boundaries, streaming assistant text, tool one-liners,
`permission.requested`/`permission.resolved`, `usage.updated`,
`rate_limit.updated`, `session.title`).

Auth is the **pane-observer token**, not the runner token: the helper scripts
execute inside the interactive child's environment, which is deliberately
scrubbed of the runner bearer token, so provisioning mints a scoped token
(persisted at `$CLAUDE_CONFIG_DIR/pane-observer/token`, 0600) accepted only
for these two routes. The runner token is also accepted. Bodies are the raw
hook / statusline stdin JSON; the response is always `200 {}` for an
authenticated request (ingestion is best-effort telemetry — the hook scripts
must never block the interactive session on a runner error).

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
backend observers (claude-pane hooks/statusline, opencode SSE, codex) map
their native signals into these normalized types before persisting to
`events.db` and streaming to clients.

> **Pruned vocabulary (claude-pane-first, protocolVersion 3).** The SDK-engine
> event types `tool.delta`, `tool.progress`, `todo.updated`,
> `models.available`, and `autopilot.state` were removed from the schema —
> nothing produces or consumes them anymore. `workspace.status` and
> `context.compacted` remain in the vocabulary with live Go consumers but no
> current producer (observers can re-emit them). Old event logs may still hold
> pruned rows; consumers ignore unknown types.

**Subagent (Task) attribution:** a `message.*`, `reasoning.*`, or `tool.*`
event may carry `parentToolUseId: <Task tool_use id>` marking a dispatched
subagent's stream; the field is absent on the main thread. Clients route
parented events out of the main transcript. (No current backend sets it —
retained vocabulary from the SDK engine era.)

The `tool.completed` / `tool.failed` events carry an optional `exitCode`
(`ToolPayload.exitCode`), populated for Bash tool completions from the exit code
the PostToolUse hook observes; it is absent for non-Bash tools (and whenever no
code was recorded).

A `message.completed` event may carry an optional `citations` array (see
`schema/events.json` `MessagePayload.citations` / the `Citation` object):
sources the assistant cited, flattened to `{ url?, title?, citedText? }`.
Citations never appear on `message.started`/`message.delta`. (No current
backend emits them — retained vocabulary.)

The `rate_limit.updated` event carries the claude.ai plan usage windows for the
status line. Payload (see `schema/events.json` `RateLimitPayload` for the
authoritative list): `{ available, subscriptionType, fiveHourUtil,
fiveHourResetsAt, sevenDayUtil, sevenDayResetsAt, sevenDayOpusUtil,
sevenDayOpusResetsAt, sevenDaySonnetUtil, sevenDaySonnetResetsAt }` —
utilizations are 0–100 and resets are RFC3339 (and may be empty when the source
reports null). The claude-pane observer derives it from the provisioned
statusline's stdin JSON, which is event-driven while a turn runs (min ~0.3 s
spacing, silent when idle) — see the observer ingestion endpoints below.
`available` is `false` for API-key/Bedrock/Vertex sessions where plan limits do
not apply; the CLI then hides the windows rather than fabricating values.
`subscriptionType` (`pro`/`max`/`team`/`enterprise`) lets the status line
distinguish headless (empty), API-key, and subscription sessions. The per-model
`sevenDayOpus*` / `sevenDaySonnet*` fields are absent (nil / undefined) unless
the plan has a separate weekly cap for that model; the status line surfaces the
one matching the attached model. Best-effort: when the source carries no
rate-limit data, no event is emitted.

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

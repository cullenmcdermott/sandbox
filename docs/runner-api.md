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

## Endpoints

### `GET /healthz`
Returns 200 if the runner process is alive. No auth required.

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
  "status": "idle",
  "claudeSession": "sdk-session-id",
  "lastTurnId": "turn-12",
  "activeTurnId": "",
  "lastActivity": "2026-06-18T22:30:00Z",
  "model": "claude-sonnet-4-5-20250929"
}
```
`model` is **optional**: it is omitted until the runner has seen the model id in
the SDK's init message (i.e. before the first turn it is absent).

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
SDK permission mode — one of `default`, `acceptEdits`, `plan`, or
`bypassPermissions`; an empty or unrecognized value defaults to `acceptEdits`.
(`bypassPermissions` additionally requires the SDK's
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
Returns 200 with:
```json
{
  "turnId": "turn-13"
}
```

### `POST /sessions/:id/turns/:turn_id/interrupt`
Cancels an active turn. Returns 200 with `{"turnId": "turn-13"}`. Returns 404
if the turn is not found or no longer active.

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
`after` returns 400.

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

The `todo.updated` event surfaces the agent's plan/checklist. Payload:
`{ todos: [{ content, status, activeForm }] }`, where `status` is one of
`pending` | `in_progress` | `completed`. The runner emits it when the SDK uses
the `TodoWrite` tool; the CLI renders the list with a per-item status glyph.

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

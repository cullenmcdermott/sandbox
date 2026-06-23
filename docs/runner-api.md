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
Returns a JSON array of session state objects.

### `GET /sessions/:id`
Returns the session state for a single session.

### `GET /sessions/:id/status`
Returns the runner's `session.json` state:
```json
{
  "id": "claude-sdk-7f3a",
  "backend": "claude-sdk",
  "projectPath": "/Users/cullen/git/homelab",
  "status": "idle",
  "claudeSession": "sdk-session-id",
  "lastTurnId": "turn-12",
  "lastActivity": "2026-06-18T22:30:00Z"
}
```

### `POST /sessions/:id/turns`
Starts a new turn. Request body:
```json
{
  "prompt": "Fix the bug",
  "resume": "turn-11",
  "allowedTools": ["Read", "Edit", "Bash"]
}
```
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
Resolves a permission request. Request body:
```json
{
  "session": "claude-sdk-7f3a",
  "permission": "perm-abc",
  "allow": true,
  "scope": "once",
  "editedInput": ""
}
```
`allow` (boolean) is required. Returns 200 with `{"permissionId": "...",
"resolved": true}`.

### `GET /sessions/:id/events?after=<seq>`
SSE stream. Emits one `data: <json>` line per event. The stream stays open
until the client disconnects. Events strictly after the given sequence number
are sent; `after=0` replays the full log. If `after` is omitted, the stream
resumes from the latest sequence (new events only).

Each event is a JSON object:
```json
{
  "seq": 1842,
  "time": "2026-06-18T22:30:00Z",
  "session_id": "claude-sdk-7f3a",
  "turn_id": "turn-12",
  "type": "tool.completed",
  "payload": {}
}
```

## Planned endpoints (not yet implemented)

These are part of the intended contract but are **not** served by the current
runner (`runner/src/server.ts`) and have no client in `internal/runner`. Do not
depend on them yet.

- `GET /sessions/:id/diff` — current dirty diff for the session workspace.
- `GET /outputs` — generated output files.

## Event Types

See `internal/session/event.go` for the canonical list. The runner maps
Claude Agent SDK messages into these normalized types before persisting to
`events.db` and streaming to clients.

## Persistence

- `/session/state/sandbox/session.json` — session state (mutable, updated on
  each turn).
- `/session/state/sandbox/events.db` — SQLite append-only event log. One
  writer (the runner). Used for replay via `after=<seq>`.
- `/session/state/sandbox/audit.jsonl` — audit entries from PostToolUse hooks.

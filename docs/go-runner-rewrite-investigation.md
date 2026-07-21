# Go runner rewrite — feasibility investigation

Status: investigation (2026-07-20) — recommendation accepted in principle;
implementation deliberately gated on the claude-pane-first live gates
(openspec tasks 2.5/8.2) plus a soak period. No openspec change yet.

**Verdict: worth it — staged per-backend (claude-pane first), not big-bang.
~8–12 focused sessions for a claude-pane-only Go runner (incl. test ports +
conformance harness); ~18–25 for full three-backend parity.**

Trigger: post pane-first, the TS runner is mostly a process supervisor + PTY
holder + event log behind a documented HTTP/SSE/WS contract whose types
already live in Go (`internal/session`, generated from `schema/events.json`).

## 1. Inventory

`runner/src`: 24 files, **4,048 code lines** (6,915 raw; one-third
comments). Largest: opencode-turn.ts 513 (via `@opencode-ai/sdk` — biggest
port risk), events.ts 433 (better-sqlite3 + SSE invariants E2/E3/E4/B4),
claude-pane-observer.ts 383, session.ts 311, server.ts 311, claude-pane.ts
306. The injected seams everywhere (PaneSpawner, ClaudeConfigFs,
`__setEventLogForTest`) translate directly to Go interfaces — ~70% of the
port is mechanical. Tests: 34 files / 4,690 code lines / **244 cases** (the
"392" figure was pre-deletion).

**Found: `@anthropic-ai/sdk` ^0.93.0 is declared but never imported** — dead
~7MB prod dependency, droppable today independent of any rewrite.

## 2. Node-necessity audit (image)

- Build stage exists solely to node-gyp better-sqlite3/node-pty — dies with
  Go (`CGO_ENABLED=0` + modernc sqlite).
- `claude` is a per-arch sha256-pinned single-file download
  (Dockerfile:69-82) — almost certainly self-contained (Bun-compiled).
  **Verify before committing:** `docker run --rm --entrypoint sh <image> -c
  'mv /usr/local/bin/node /tmp/ && claude --version'`.
- opencode/codex are `npm install -g` (Dockerfile:90,:98) → node launcher
  shims over native binaries; replace with direct checksummed release
  downloads. **Verify:** `head -1 "$(readlink -f "$(which opencode)")"`
  in-image.
- **The real Node runtime dependency is the observer helper scripts**
  (`claude-pane-observer.ts:353-403`, `#!/usr/bin/env node` on the PVC) —
  in Go, replace with a runner-binary subcommand (also fixes review finding
  [P6], per-hook Node cold-start on the tool-call critical path).
- Size: ~784MB → ~560–620MB (node+node_modules+toolchain ≈150–200MB out, Go
  binary ~15–25MB in). Bigger lever is orthogonal per-backend images
  (claude-pane pod never invokes opencode/codex) → ~350–400MB.

## 3. Go equivalents

| TS | Go | Note |
|---|---|---|
| node-pty | creack/pty (already a dep) | edge cases §5 |
| ws | gorilla/websocket (already a dep; e2e fake pane runner already implements the server side) | auth-before-upgrade ordering preserved |
| better-sqlite3 | **modernc.org/sqlite (pure Go)** over mattn/CGO | all used features supported; write rate low; static cross-arch builds; darwin `go test` |
| SSE | net/http + Flusher | preserve byte-level frame contract: single-line `data:` verbatim splice (`events.ts:632-639`), `: stream-open`/`: heartbeat`/`: replay-complete` comments — the Go client scanner depends on it |
| sshd | keep entrypoint.sh | zero risk |
| `@opencode-ai/sdk` | **no drop-in** — verify `sst/opencode-sdk-go` exists at a 1.x compatible with serve 1.17.7, else hand-roll the ~6 endpoints (session create/prompt/abort, permission respond, event SSE) | the strongest argument for per-backend staging |

## 4. Architecture wins

- TS half of the schema pipeline dies: `events.gen.ts` + drift gate; the Go
  runner imports `internal/session` directly, so `schema_test.go` validates
  the actual producer.
- Duplicated wire constants collapse (pane close codes 4001/4002 exist in
  claude-pane.ts:48-50 AND internal/runner/pane.go:32-34; PROTOCOL_VERSION,
  backend ids, status enums).
- `just check` currently soft-skips runner tests/lint/typecheck without
  node_modules (justfile:31-33,87-91) — in Go the runner is always-on
  `go test ./...`, race-detected.
- `internal/e2e/pane_e2e_test.go` (552 lines) already implements the pane
  protocol slice in Go — seed for the real server package.
- **One module** (`internal/runnerd` + `cmd/sandbox-runner`), not a separate
  module — a second module would need the event model across a module
  boundary, recreating the disease being cured. Consequence: the image
  workflow needs repo-root build context + wider path filters
  (`.depot/workflows/build-runner-image.yml:12-19` filters `runner/**`,
  `context: runner`).

## 5. Costs / risks

- ~4,050 TS → realistically 5–6k Go; 244 test cases to port. Load-bearing
  suites in order: events-replay (E2 chunked-replay handoff — subtlest
  concurrency contract), events-delta-compaction, events-redaction,
  server-http, claude-pane + observer, the session-skew family (D2/B7/V41
  PVC recovery), opencode-turn (644 lines), turn-gate, token-accounting.
- SQLite file format: non-issue (implementation-independent; leftover
  -wal/-shm readable; `user_version` logic ports verbatim). PVC persists
  `/session/state/sandbox/{session.json,events.db,audit.jsonl,ssh/}` +
  `/session/state/claude`.
- **The real PVC-compat landmine: provisioned settings.json hook entries**
  contain the literal command `node /session/state/claude/pane-observer/hook.js`
  and the idempotent upsert identifies "our" entries **by command string**
  (`claude-pane-observer.ts:488-515`). A Go provisioner must
  remove/replace old-path entries, not just upsert — else on a node-less
  image every hook fire fails in-pane.
- node-pty→creack/pty edges, each needing an explicit test: EIO-on-master-
  read after child exit = normal exit; node-pty default `kill()` is SIGHUP
  (stop() relies on it — pick the signal explicitly); derive
  {exitCode,signal} from WaitStatus for the crash-reason string
  (observer:297-299); resize equivalent; flow control unused (Go raw []byte
  is strictly simpler — no UTF-8 hop).
- **Oracle gap:** runner-api.md is current, but `pane_e2e_test.go` pins the
  Go *client* against a *fake* runner, and `server-http.test.ts` dies with
  the TS code. Build a language-agnostic black-box conformance suite
  (HTTP/SSE/WS) first, against the TS runner — it becomes the rewrite's
  safety net and permanently hardens the contract (`just kind-test` is the
  natural home).

## 6. Sequencing (recommended)

1. Pass gates 2.5/8.2 on the TS runner; soak a few weeks. Meanwhile: drop
   the dead `@anthropic-ai/sdk` dep; optionally build the conformance suite.
2. Port claude-pane + control plane only (server, events, session,
   claude-pane, observer incl. helper-script replacement + settings.json
   migration, claude-config, exec, guards/redact/auth/audit/trace/turns).
   Excludes the entire `@opencode-ai/sdk` surface and codex.
3. Ship as a second image selected only for `backend == claude-pane` (small
   `internal/k8s` change; per-session image pinning isolates existing
   sessions; rollback = tag flip).
4. Port opencode (pending Go-SDK verification) + codex observers; retire the
   TS runner and the npm leg of `just check`.

No strangler/proxy hybrid — per-backend staging de-risks equally without
two-runtime complexity.

## 7. Kill criteria

Don't do it if: (1) 8.2 keeps producing pane-stack changes (porting a moving
target ≈ doubles cost); (2) the agent binaries turn out to need the image's
node (30-minute verification, §2 — halves the payoff); (3) no appetite for
per-backend staging (forcing opencode/codex day-one puts the SDK-equivalent
work on the critical path and the profile stops being attractive).

Payoff, honestly weighted: maintenance/coherence primary (one language, one
type system, dead codegen, always-on gates, no node-gyp); image size real
but secondary; cold-start negligible (pull + PVC attach dominate).

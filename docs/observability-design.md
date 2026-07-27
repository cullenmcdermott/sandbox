# Observability implementation plan

<!-- markdownlint-disable MD013 -->

Status: **planning** (revised 2026-07-26) - architecture corrected after review;
the decisions in [Decisions requiring sign-off](#decisions-requiring-sign-off) remain
open. Nothing in this plan should be described as implemented unless its TODO
work package is checked off.

> **Partly overtaken by work landed 2026-07-27.** This revision was written on
> 2026-07-26, before the `[T*]` burndown batch merged. Several of its work
> packages have since been implemented in whole or in part, so read the WP list
> against `TODO.md` (which carries the authoritative marks) rather than assuming
> everything below is outstanding:
>
> | Work package | Overlapping TODO item | State on main |
> |---|---|---|
> | WP1 - bounded runner logs and audit | `[T2]` structured logging, `[T8]` audit cap | `[T2]` done; `[T8]` capped, reader half still open |
> | WP3 - CLI debug sink and instrumentation | `[T1]` | file sink done; the wider instrumentation list deliberately not done |
> | WP4 - trace context and latency measurements | `[T4]` | pane trace correlation done, both ends |
> | WP5 - capacity visibility and runner metrics | `[T6]` | part (a) - event-log and PVC storage gauges - done; part (b) open |
>
> The seven sign-off decisions below are unaffected and remain open. WP0, WP2,
> WP6, WP7 and WP8 were not touched by that batch.

This plan replaces the 2026-07-24 draft. The original audit remains valid at a
high level: the system has useful normalized events and a few hand-written
timers, but weak operational logging, incomplete correlation, no durable metrics,
and no supported way to inspect runner diagnostics without Kubernetes access.
The old implementation proposal made four incorrect assumptions that this
revision removes:

1. `events.db` is not a complete trace tree and cannot explain cold start.
2. The current short `X-Sandbox-Trace-Id` is not an OTLP parent context.
3. `TranscriptSubs` cannot sync `/session/state/sandbox/telemetry` into a
   configurable client state directory.
4. Usage events do not have one uniform counter semantic across backends.

The work is split into independently useful packages. Operational diagnostics do
not depend on running Grafana, optional telemetry does not sit on the pane frame
hot path, and the public client gets every new read/export capability before the
CLI consumes it.

## Outcomes

When the plan is complete, an operator can answer:

- Why did create, resume, connect, pane attach, or runner boot take so long?
- Why did a runner restart, reject a request, or lose a client connection?
- Is the event log or PVC approaching an unsafe size?
- How often are pane, SSE, observer, SQLite, port-forward, and sync paths slow?
- What usage and cost can be stated honestly for each backend and session?
- What happened during a turn, with incomplete source data clearly identified?

The default experience remains lightweight. Basic bounded operational logs and
capacity status are always available. Detailed traces, metric samples, and local
LGTM ingestion are explicitly armed.

## Non-goals

- Cluster-side Prometheus, Grafana, ServiceMonitors, or always-on collectors.
- Alerting, SLOs, or long-term telemetry retention.
- Replacing the normalized event protocol in `schema/events.json`.
- Recording prompts, tool inputs, file contents, pane bytes, authorization
  headers, credentials, or environment values as telemetry attributes.
- Claiming that agent-visible audit data is tamper-evident while runner and agent
  share a UID.
- Emitting a span for every pane frame, message delta, or SSE event.
- Syncing a live SQLite database with Mutagen.

## Current baseline

### Logs

- `internal/cli/debug.go` emits structured JSONL to stderr under `--debug` or
  `SANDBOX_DEBUG`, but production has only three `dbg()` call sites.
- The runner has roughly 30 operational `console.*` calls and no common fields,
  request logging, level policy, file rotation, or supported reader.
- `/session/state/sandbox/audit.jsonl` is structured and PVC-backed, but is
  unbounded, has no purpose-built reader, and is agent-influenceable under the
  current same-UID model.

### Traces

- `client/trace.go` times create/connect phases with an 8-hex-character flow ID.
- `runner/src/trace.ts` times runner boot; `startTurnTrace` is dead code.
- `internal/runner/pane_rtt.go` reports pane ping/pong percentiles locally.
- `X-Sandbox-Trace-Id` reaches ordinary requests made through `Client.do`, but
  not the manually built SSE or pane WebSocket requests. The runner consumes it
  only on the OpenCode headless `POST /turns` path.
- `--trace` changes only the host process. It does not arm runner-wide tracing in
  an existing pod.

### Metrics and events

- No metrics endpoint or telemetry emitter exists.
- `events.db` stores append time, session ID, optional turn ID, type, and payload.
  It is sufficient for honest event-derived timelines, not universal span
  reconstruction: `session.started` is repeatable, tool IDs are optional,
  permission IDs can collide after restart, and some terminal events are absent.
- `usage.updated` has backend-specific snapshot semantics. The latest display
  state is already persisted in the local index; summing every event would
  double-count usage.
- `RETENTION_MAX_EVENTS` is disabled by default. Delta compaction limits old
  streaming deltas, but durable terminal events continue to grow and SQLite does
  not release its high-water allocation without compaction.

## Architecture

Observability has three distinct planes. They share field names and redaction,
but not retention or enablement policy.

### 1. Operational diagnostics

Small, bounded, always available records used to diagnose failure without a
collector:

- Runner control-plane log on the PVC, also rendered as human text to stdout.
- Hardened operational audit file, explicitly documented as agent-influenceable.
- CLI per-session debug file while `--debug` is enabled.
- Capacity snapshot exposed in runner status.
- Authenticated client/CLI readers for logs and audit.

Operational runner logs are always written because an unexpected restart cannot
be diagnosed by telemetry that had to be armed before the incident. Their budget
is intentionally much smaller than optional telemetry.

### 2. Correlated diagnostics

Request and operation context that follows one action across process boundaries:

- W3C `traceparent` for machine correlation.
- A short `flow.id` attribute for human grep compatibility.
- Spans for bounded, low-frequency operations such as connect, pane attach,
  request handling, port-forward establishment, and runner boot.
- Histograms and counters for high-frequency paths such as frames, observer
  ingestion, event append, and reconnects.

A trace may be recorded locally without a collector. Cross-process child
relationships are created only when a valid parent span context was propagated;
otherwise records use links or independent roots rather than invented parentage.

### 3. Optional analysis export

OTLP/JSON artifacts and a disposable local LGTM stack used for investigations:

- A host-side exporter converts normalized events into honest turn timelines.
- Armed CLI and runner emit spans/metrics/logs as immutable OTLP/JSONL segments.
- A dedicated one-way artifact sync maps the runner telemetry directory to the
  configured client state directory.
- A pinned Collector reads completed segments and forwards them to a pinned LGTM
  image.

## Shared contracts

### Enablement

Use separate switches because the storage and privacy effects differ:

| switch | effect | default |
| --- | --- | --- |
| none | bounded runner operational log, audit, capacity status | on |
| `--debug` / `SANDBOX_DEBUG` | detailed CLI JSONL, including a per-session file | off |
| `--trace` / `SANDBOX_TRACE` | host timing lines and OTLP trace/metric artifacts | off |
| runner telemetry setting | runner OTLP artifacts for a session | off, fixed at create time |

An incoming valid `traceparent` correlates that request even when runner-wide
telemetry is off, but does not silently enable broad sampling or persistent
metrics. Existing sessions need an explicit recreate or future runtime control
to arm runner-wide telemetry; this plan does not smuggle `SANDBOX_TRACE` through
the reserved `ExtraEnv` path.

### Trace context

- Propagate standard `traceparent` and optional `tracestate` on HTTP, SSE, and
  pane WebSocket handshakes.
- Generate a 128-bit trace ID and 64-bit span ID with cryptographic randomness.
- Keep the current short flow ID as `sandbox.flow.id` and in human trace lines.
- Validate all inbound context before logging or using it.
- Never mutate a shared runner client's context after it becomes concurrent.
- `Session.Connect` is not a pane attach and may return before background sync
  ends. Pane attach therefore links to the connect trace unless a longer-lived
  caller-owned attach root explicitly parents both operations.

### Common resource attributes

These identify the emitting resource and are not metric dimensions by default:

| attribute | notes |
| --- | --- |
| `service.name` | `sandbox-cli` or `sandbox-runner` |
| `service.version` | build version when available |
| `sandbox.session.id` | omitted for commands not attributable to one session |
| `sandbox.backend` | `claude-pane`, `opencode`, or `codex` |
| `sandbox.runner.boot_id` | random per runner process; disambiguates restarts |
| `sandbox.flow.id` | short human correlation ID |

Turn ID, event sequence, file path, prompt, and tool input are not metric labels.
Tool name, route template, outcome, close code, and hook type are allowed only
where their value sets are bounded.

### Log record

Operational JSONL uses one stable shape on both sides:

```json
{"time":"2026-07-26T12:00:00Z","level":"INFO","msg":"pane attached","component":"pane","session":"claude-pane-abc","trace_id":"...","span_id":"...","fields":{"cols":120,"rows":40}}
```

Requirements:

- `msg` is a stable label; variable data belongs under structured fields.
- Health-check successes, request bodies, auth headers, observer payloads, pane
  bytes, prompts, and tool input/output are excluded.
- Every runner record has time, level, component, session, and boot ID.
- Human stdout remains concise and readable under `kubectl logs`.
- File permissions are `0700` for directories and `0600` for files.
- File-write failure falls back to stderr/stdout and does not crash an observer
  or agent operation.

### Disk and rotation

Proposed defaults, subject to the soak in the final work package:

| artifact | policy | initial budget |
| --- | --- | --- |
| runner operational log | always-on rotating JSONL | 4 x 2 MiB = 8 MiB |
| audit | always-on rotating JSONL | 4 x 4 MiB = 16 MiB |
| each armed OTLP signal | immutable completed segments | 4 x 16 MiB = 64 MiB |
| CLI per-session debug | command-scoped rotating JSONL | 4 x 2 MiB = 8 MiB |

Writers append to an `.active` file. Rotation closes and atomically renames it to
a monotonically unique segment name; readers and Mutagen ignore `.active` files.
Names are never reused, preventing Collector fingerprint/offset confusion. The
oldest completed segment is removed only after the newest is durable.

### Redaction and trust

- Structured values pass through a common recursive redactor before either sink.
- Operational logs and telemetry exclude prompt text, file content, pane bytes,
  observer bodies, tool input/output, env values, and credentials by construction,
  not merely regex masking. Audit is the deliberate exception: its existing
  contract retains tool input and therefore remains a possible secret-bearing
  artifact even after recursive redaction.
- Tests pass known bearer, API, OAuth, private-key, and secret-shaped canary
  values through every sink. Unknown secrets cannot be recognized reliably;
  field minimization is the primary control.
- Audit remains operational provenance, not tamper-evident security evidence,
  until the runner and agent use distinct UIDs or an external sink.
- Log and audit routes use existing bearer authentication and preserve the
  auth-before-session-probe ordering.

## Event-derived tracing contract

The exporter must not describe `events.db` as already being a trace tree.
Its source is normalized events obtained from the authenticated
`GET /sessions/:id/events?after=0` API for a running session. A future offline
export may use a runner-created consistent SQLite snapshot, never a live
Mutagen copy of `events.db`, `-wal`, and `-shm`.

### Reconstruction rules

- Session identity is an OTLP resource, not a session-lifetime span.
- Each `turn.started` creates a root trace. Its matching terminal event closes
  the turn span. A missing start or terminal produces an explicitly incomplete
  span/event.
- A tool becomes a child span only when start and terminal records share a
  non-empty tool ID and turn ID. Otherwise it is an orphan diagnostic event or
  an incomplete span with `sandbox.incomplete=true`.
- Permission events become child spans only when start and resolution share a
  non-empty permission ID and the same non-empty turn ID. Records without that
  compound key remain orphans; permission ID alone is process-local and unsafe
  across historical restarts.
- Messages and reasoning are span events. Deltas are aggregated; they do not
  become spans.
- `session.started` updates resource metadata. It does not open a span.
- `session.terminating` marks a runner lifecycle event, not permanent session
  completion.
- SQLite append timestamps are ingestion timestamps. Derived durations are
  labeled approximations, negative wall-clock durations are clamped/rejected,
  and sequence remains the ordering authority.
- Deterministic valid OTLP IDs are derived from a namespace plus session ID,
  turn ID, event kind/native ID, and start sequence. Re-exporting the same event
  range therefore does not create different traces.

### Usage semantics

Usage export is a separate reducer, not a generic sum over `usage.updated`:

| backend/source | semantic | export |
| --- | --- | --- |
| Claude context tokens | current context snapshot | gauge |
| Claude `totalCostUsd` | cumulative session snapshot | latest-value gauge; reset-aware deltas only after persistence exists |
| OpenCode assistant usage | replaceable per-message/turn snapshot | final per-turn delta |
| Codex `tokenUsage.last` | per-turn snapshot | final per-turn delta |
| rate-limit utilization | account/plan snapshot | gauge, deduplicated by non-secret account identity |

Weekly fleet cost is not promised until normalized timestamped deltas are
persisted. Plan utilization is never summed across sessions sharing one account.

## Metrics catalog

Metric names and semantics are fixed before emission:

| instrument | type | source |
| --- | --- | --- |
| `sandbox.runner.pane.frame.bytes` | histogram | output frame size |
| `sandbox.runner.pane.enqueue.duration` | histogram | PTY callback to WS enqueue completion; not network latency |
| `sandbox.runner.pane.ws.buffered_bytes` | gauge | WS buffered amount |
| `sandbox.runner.pane.closes` | counter | bounded close-code label |
| `sandbox.runner.observer.requests` | counter | bounded hook/event type |
| `sandbox.runner.observer.server_duration` | histogram | runner request handling only |
| `sandbox.runner.observer.end_to_end_duration` | histogram | helper start to response, when helper timestamp is present |
| `sandbox.runner.event.append_duration` | histogram | SQLite append critical path |
| `sandbox.runner.eventlog.rows` | gauge | maintained row count |
| `sandbox.runner.eventlog.bytes` | gauge | DB + WAL + SHM bytes |
| `sandbox.runner.pvc.available_bytes` | gauge | `fs.statfs('/session', {bigint:true})` |
| `sandbox.runner.sse.clients` | gauge | active and total variants |
| `sandbox.runner.sse.evictions` | counter | backpressure disconnects, not “dropped” events |
| `sandbox.runner.process.cpu_seconds` | counter | process/cgroup accounting |
| `sandbox.runner.process.rss_bytes` | gauge | runner and child where PID is available |
| `sandbox.cli.sse.connect_duration` | histogram | request to response headers |
| `sandbox.cli.sse.replay_duration` | histogram | request to replay-complete |
| `sandbox.cli.sse.first_data_duration` | histogram | request to first persisted event, if one arrives |
| `sandbox.cli.portforward.reconnects` | counter | reconnect attempts/outcomes |
| `sandbox.cli.connect.duration` | histogram | completed connect operations |

Metric export uses explicit histogram boundaries, cumulative monotonic counters,
start times, and reset semantics. A 15-second armed export interval is sufficient.
Capacity status is sampled for the status endpoint even when OTLP export is off.

## Public surfaces

Capabilities land in `client` before Cobra commands:

- A public diagnostic reader for finite and follow-mode runner logs.
- A public audit reader with the same cursor model.
- A public normalized-event-to-OTLP exporter accepting `io.Writer` and explicit
  event input, so external consumers do not need the CLI.
- Additive status fields for event-log bytes/rows and PVC available bytes,
  protocol-versioned because status bodies are hand-written on both sides.
- A telemetry/artifact option that does not expose Kubernetes or Mutagen types.

CLI consumers:

```text
sandbox logs <session> [--follow] [--since <cursor>]
sandbox audit <session> [--follow] [--since <cursor>]
sandbox telemetry export <session> --format=otlp-json --output <path>
sandbox telemetry up|down|reset
```

`logs` and `audit` do not silently resume suspended sessions. They return an
explicit suspended error with a `--resume` opt-in. Follow mode reconnects after
port-forward or pod replacement using a generation/segment/offset cursor and a
streaming HTTP client without the ordinary 30-second timeout.

## Work packages

### WP0 - Correct the baseline

Scope:

- Delete dead `startTurnTrace`, its interface/options that no remaining caller
  needs, and its three self-referential tests.
- Retain `traceTurnLink`, trace-header validation, and runner boot timing.
- Correct `docs/architecture.md` so it no longer claims live runner turn
  milestones exist.
- Correct stale TODO pointers and terminology: pane buffer cap, SSE timing seam,
  event append location, and “discarded after render.”

Oracle: repository search finds no production or test reference to
`startTurnTrace`; durable architecture matches the two remaining trace paths.

Behavioral counter: runner tests and typecheck remain green after deletion.

### WP1 - Bounded runner logs and audit

Scope:

- Add one dependency-free runner logger initialized before event-log/session
  loading. It writes concise human stdout plus bounded JSONL on the PVC.
- Migrate all runner `console.*` calls; add a lint/search gate allowing console
  use only inside the logger sink.
- Add filtered HTTP and WebSocket lifecycle logs with route templates, status,
  duration, outcome, session, boot ID, and trace context. Skip successful
  `/healthz` requests.
- Install top-level uncaught exception/rejection logging early enough to capture
  boot failures where possible.
- Reuse the rotating writer for audit; redact the full row, set explicit modes,
  catch write failures, and document pre-execution semantics and missing Codex
  coverage honestly.

Oracle: parse every produced line and assert required fields, permissions,
redaction, rotation order, and recovery after a simulated write failure.

Behavioral counter: a saturation test proves each artifact plateaus below its
configured byte ceiling while `kubectl logs`-style output remains readable.

### WP2 - Diagnostic API, SDK, and CLI readers

Depends on WP1.

Scope:

- Add authenticated finite/follow routes for runner logs and audit. Requests
  select only known artifacts; no caller-supplied filesystem path is accepted.
- Define an opaque cursor containing boot generation, segment, and offset.
- Add streaming client methods and CLI commands, including reconnect/dedup logic
  and explicit suspended-session behavior.
- Preserve auth-before-session-ID probing and redact errors returned to clients.

Oracle: route tests cover auth, cursor validation, rotation, replacement boot,
deduplication, and forbidden traversal. Client tests replay known fixtures in
exact order.

Behavioral counter: kill and replace a runner after writing a marker; once the
new runner is healthy, `sandbox logs` returns both the pre-restart marker and new
records. A permanent pre-listen CrashLoop is documented as requiring Kubernetes
or direct PVC access.

### WP3 - CLI debug sink and instrumentation

Scope:

- Replace the mutable global stderr-only logger with a concurrency-safe routing
  handler keyed by session ID.
- Pre-mint create IDs with `client.NewID` and pass `CreateOptions.ID` so credential
  and create diagnostics can be attributed before cluster calls begin.
- Write `<StateDir>/<id>/debug.jsonl` while the TUI owns the alt-screen; do not tee
  to stderr during full-screen operation.
- Add dependency-neutral diagnostic callbacks/logger seams through client,
  backend, port-forward, and sync code. Instrument create/suspend/resume/destroy,
  health attempts, forward establish/reconnect, credential resolution, and sync
  create/flush.
- Keep public APIs additive and avoid exposing `slog` as a required SDK concept;
  prefer a small record/callback interface if the seam must be public.

Oracle: a dashboard-backed test leaves parseable per-session JSONL with no
cross-session records and mode `0600`.

Behavioral counter: an alt-screen integration test observes zero debug writes to
terminal stderr while the file receives expected lifecycle records.

### WP4 - Trace context and latency measurements

Depends on WP1 for structured runner request and pane records. The context and
client-side measurements can be developed independently, but the package is not
complete until the runner sink carries them.

Scope:

- Introduce W3C trace context while preserving human flow IDs.
- Propagate context through ordinary HTTP, manually constructed SSE, and pane WS
  handshakes. Add runner request/pane records carrying the validated IDs.
- Define pane attach as its own operation linked to connect unless a caller owns
  a still-live parent span.
- Measure SSE connect, replay, and first-data at `internal/runner/client.go`, not
  `pane_rtt.go`.
- Add bounded in-memory aggregations for pane enqueue/frame size, observer
  request count and server duration, end-to-end hook duration, SQLite append,
  SSE evictions, and port-forward reconnects. WP7 adds OTLP file export.
- Keep pane network RTT in the existing ping/pong probe. Do not label `ws.send`
  enqueue time as network latency.

Oracle: a generated `traceparent` on attach is visible unchanged in a runner pane
record; malformed/repeated values are rejected. Histogram tests use injected
clocks and exact buckets.

Behavioral counter: benchmarks show the disarmed frame path has no allocation or
clock regression beyond the agreed threshold; P6’s hook overhead question is
answered with helper-to-response measurements, not server time alone.

### WP5 - Capacity visibility and runner metrics

Scope:

- Add an always-available capacity snapshot: event rows; DB/WAL/SHM bytes; PVC
  available bytes from Node `fs.statfs`; runner RSS; optional child RSS.
- Maintain row counts from inserts/deletes after one startup count rather than
  polling `COUNT(*)` on the synchronous SQLite connection.
- Collect the metrics catalog in bounded in-memory instruments. WP7 exports a
  snapshot every 15 seconds when telemetry is armed. Define buckets, temporality,
  and process-restart behavior in tests now so the exporter cannot reinterpret
  the data later.
- Add warning thresholds to status presentation, but do not silently change
  retention yet.
- After a measured soak, decide continuous retention policy separately. Deleting
  rows does not itself reclaim SQLite high-water allocation.

Oracle: fixture parser tests plus a load test comparing reported DB/WAL/SHM and
available bytes to filesystem truth within an explicit sampling tolerance.

Behavioral counter: status polling and armed export stay responsive during event
append load, and the capacity sample performs no full-table scan per interval.

### WP6 - Honest event-derived OTLP export and local stack

Scope:

- Implement a public pure exporter following the reconstruction contract above.
- Stage 1 reads normalized events through the existing API and writes one
  immutable trace-only OTLP/JSONL artifact on the host. Running sessions only;
  no Mutagen or live SQLite copy.
- Generate deterministic valid OTLP IDs and protobuf-JSON fields, including
  nanosecond timestamps encoded as decimal strings.
- Add backend fixtures for Claude, OpenCode, and Codex, plus missing IDs,
  unmatched starts/terminals, permission-ID collisions, interrupted turns, duplicate
  export, equal timestamps, and backward wall clock.
- Add `dev/observability/compose.yaml` with pinned image versions. Configure a
  trace-specific `otlp_json_file` receiver, `start_at: beginning`, persistent
  file offsets, and a trace-only glob. Add `just obs-up`, `obs-down`, and
  `obs-reset`.

Oracle: a golden OTLP export independently recomputes turn/tool pairing and IDs;
the Collector config validates against the pinned image.

Behavioral counter: loading the fixture renders exactly one honest turn trace in
Tempo, with unmatched records visibly marked rather than silently dropped or
falsely parented.

### WP7 - Armed telemetry files and artifact sync

Depends on WP1, WP4-WP6.

Scope:

- Add narrow Go and TypeScript emitters for span/log/record operations with
  nil/no-op disarmed implementations.
- Emit immutable completed OTLP segments using the shared rotation contract.
- Add a generic one-way artifact-sync mapping with explicit remote and local
  roots. Remote is `/session/state/sandbox/telemetry`; local is
  `<Client.StateDir()>/<id>/telemetry`. Do not add telemetry to
  `TranscriptSubs` or sync all of `/session/state/sandbox`.
- Create artifact sync in the existing non-load-bearing background phase and
  preserve pause/resume/terminate/GC labels.
- Ignore `.active` files and verify atomic segment completion before ingestion.

Oracle: a custom `WithStateDir` test proves the exact local destination and a
Mutagen argument fixture proves remote-to-local direction and exclusions.

Behavioral counter: sustained armed output plus rotation and sync plateaus at the
configured budget and Collector ingestion sees each completed segment once.

### WP8 - Fleet usage and cost

Depends on explicit usage semantics; independent of runner hot-path metrics.

Scope:

- Add a backend-aware reducer producing normalized timestamped snapshots/deltas.
- Persist enough local history to answer bounded period queries without replaying
  destroyed sessions.
- Export per-session tokens/cost and account-deduplicated rate-limit gauges.
- Add a CLI summary before considering a dashboard surface.

Oracle: table tests cover repeated snapshots, reset/restart, duplicate event
delivery, missing cost, shared accounts, and final-per-turn selection.

Behavioral counter: one live session’s exported current cost matches its agent
status exactly, while replaying the same events leaves totals unchanged.

## Dependency order and shipping strategy

```text
WP0
WP1 -> WP2
WP3
WP1 -> WP4 -> WP5
WP6
WP1 + WP4 + WP5 + WP6 -> WP7
usage semantics -> WP8
```

Recommended batches:

1. **Truth and diagnostics:** WP0-WP2. This closes dead tracing, C10, bounded
   audit/log storage, and the no-`kubectl` reader gap.
2. **Host diagnostics:** WP3-WP4. This makes create/connect failures and
   cross-process actions correlatable without adopting OTLP everywhere.
3. **Capacity and visualization:** WP5-WP6. This answers PVC risk and proves the
   local stack with honest event-derived traces.
4. **Armed telemetry transport:** WP7 after the format and stack are proven.
5. **Fleet accounting:** WP8 after semantics are settled with live fixtures.

Each batch updates `docs/architecture.md`, `docs/runner-api.md`, `SECURITY.md`,
and the public SDK conformance pins where its behavior changes. Each batch runs
its named package tests during development and `just check` before completion.

## Failure behavior

- Operational log/audit write failure is reported to stdout/stderr and counted;
  it never blocks observer ingestion or agent execution.
- Telemetry write, rotation, sync, or Collector failure does not fail create,
  connect, pane I/O, SSE, or event persistence.
- The event log remains append-before-stream and is never made dependent on an
  exporter.
- Metrics sampling skips a cycle rather than blocking the Node event loop.
- Reader follow mode reconnects from a cursor and tolerates a rotated-away
  segment with an explicit gap marker.
- Disk-full behavior is tested. Rotation may fail when no space remains, so the
  status endpoint and stdout warning are the last-resort diagnostic surfaces.

## Decisions requiring sign-off

Recommended answers are shown first.

1. **Runner operational logs:** always-on bounded 8 MiB per session
   (recommended) versus armed-only. Armed-only cannot explain surprise restarts.
2. **Runner telemetry arming:** immutable create-time session option
   (recommended first cut) versus a runtime authenticated control. Create-time is
   simpler and honest for existing pods; runtime control can follow if needed.
3. **Stack packaging:** pinned Docker Compose with LGTM + Collector
   (recommended) versus Flox-native services. The repo already requires a Docker
   daemon for KIND; Compose is the smaller disposable implementation.
4. **Reader behavior for suspended sessions:** fail with `--resume` opt-in
   (recommended) versus automatic resume. Diagnostics should not mutate lifecycle
   unexpectedly.
5. **Initial disk caps:** accept the proposed 8 MiB log, 16 MiB audit, and 64 MiB
   per armed signal for one soak, then revise from measured output.
6. **Event retention:** leave `RETENTION_MAX_EVENTS` disabled until WP5 measures
   growth (recommended), then decide a continuous policy separately.
7. **Audit positioning:** explicitly call it agent-influenceable operational
   provenance (recommended) until UID separation lands; do not market it as a
   security boundary.

## Completion criteria

The observability program is complete when:

- Runner and CLI operational records are structured, bounded, redacted, and
  readable without Kubernetes access.
- One standard trace context crosses HTTP, SSE, and pane WebSocket boundaries.
- High-volume paths use bounded aggregations and the disarmed frame path remains
  effectively unchanged.
- Capacity status exposes event-log and PVC risk even when telemetry is off.
- Event-derived traces visibly preserve uncertainty and load into the pinned
  local stack.
- Optional telemetry reaches a custom client state directory through a dedicated
  artifact sync and is ingested exactly once.
- Fleet totals have tested backend-specific semantics and are replay-idempotent.
- Saturation tests prove every file class remains within its configured budget.
- `just check` passes with no skipped observability gates.

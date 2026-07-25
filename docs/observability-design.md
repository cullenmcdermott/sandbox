# Observability: logs, traces, metrics, and a disposable local stack

Status: **draft** (2026-07-24) — awaiting maintainer sign-off. Nothing here is
implemented. The audit in [Problem](#problem) is verified against the tree at
`a0ed573`; the corresponding defects are filed as TODO §10 `[T#]` items and are
actionable independently of whether this design is accepted.

## Problem

The system has a lot of moving parts — a CLI, a Bubble Tea dashboard, a
port-forward, a WebSocket pane, an SSE event stream, a Node runner supervising
a PTY child, three backend observers, and Mutagen — and almost no way to see
across them. Concretely:

**Logs.** Two disjoint systems, and the good one is nearly unused. The CLI's
structured slog JSON-lines logger (`internal/cli/debug.go`, schema documented at
`docs/runner-api.md:376-401`) has **three production call sites**:
`internal/cli/trace.go:74`, `:88`, and `internal/cli/k8slog.go:37`. Only the
last earns its keep. The runner has no structured logging at all — ~30 ad-hoc
`console.log/warn/error` calls with no level, timestamp, session id, or
correlation id, and `runner/src/server.ts` logs exactly one line ever (`:280`),
so the HTTP surface has zero request logging. Neither sink writes to disk
host-side, which means the dashboard — the primary workflow, and the one that
owns the alt-screen — is precisely the case where stderr output is unreadable.
Pod stdout is ephemeral and is not on the PVC, so a restart destroys the only
record of why it restarted. There is no `sandbox logs <session>`; seeing runner
output requires `kubectl` against a cluster the tool otherwise hides.
`runner/src/audit.ts` is the lone structured on-disk log — and it is never
surfaced host-side, never synced, and never capped.

**Traces.** Three hand-rolled tracers, all `SANDBOX_TRACE`-gated, all emitting
`trace: <id> <name> <dur>` text: `client/trace.go` (14 spans on create/connect),
`runner/src/trace.ts` (boot phases, turn milestones, the `X-Sandbox-Trace-Id`
bridge), and `internal/runner/pane_rtt.go` (pane WS ping/pong percentiles). The
line format is consistent, which is worth preserving. The coverage is not:

- `startTurnTrace` (`runner/src/trace.ts:39`) has **zero production callers** —
  the turn engine that used it was deleted by `claude-pane-first`.
  `runner/test/trace.test.ts` still tests it in three cases, so the suite stays
  green while nothing calls it.
- `X-Sandbox-Trace-Id` is consumed in exactly one place — `runner/src/server.ts:418`,
  on `POST /turns`, the *opencode headless* path. The claude-pane WebSocket
  upgrade never reads or propagates it, so **end-to-end correlation does not
  exist for the primary backend**.
- Spans are flat: no parent/child, no nesting, no sampling. Adequate for a
  five-span cold start; useless for "why was that turn slow."
- Untimed entirely: the pane frame path (`runner/src/claude-pane.ts` contains no
  clock read at all), observer ingestion (TODO §4 `[P6]` proposes
  `kubectl logs | rg -c observer` as the measurement recipe — the absence of a
  counter *is* the finding), `appendEvent` → SQLite (`runner/src/events.ts:353`,
  on the critical path of every event via the append-before-stream invariant),
  SSE first-event latency (wanted at `internal/runner/pane_rtt.go:32`),
  steady-state Mutagen sync, and port-forward reconnect frequency
  (`classifyForwardReconnect`/`nextForwardBackoff` back off with no counter).

**Metrics.** None. No `/metrics` route; no otel or prometheus dependency in
`go.mod` or `runner/package.json`; `internal/k8s` never touches `metrics.k8s.io`.
Data that is already collected and then discarded after render: `usage.updated`
(input/output/cache tokens + `totalCostUsd` per session,
`schema/events.json:134-143`), `rate_limit.updated` (5h/7d/Opus/Sonnet plan
utilization + resets, `:144-156`), and `IdleStatus` (`turnActive`,
`attachedClients`, `idleSince` — the reaper polls it and throws it away). That is
most of a fleet cost dashboard already flowing through SSE.

Nothing tracks the operational risk that matters most: **PVC fill**.
`runner/src/events.ts:396` deliberately never `VACUUM`s, and
`RETENTION_MAX_EVENTS` (`:153-155`) is opt-in and **off by default**. The event
log grows without bound and no gauge watches it.

The structural summary: three trace formats, two log formats, zero metrics, and
every byte of it line-oriented text on stderr/stdout that no tool can ingest.

## Goals and non-goals

**Goals.**

1. One emitter per process, one wire format, ingestible by off-the-shelf tooling.
2. Telemetry from the pod reaches the laptop without cluster-side infrastructure.
3. A local Grafana-shaped stack that is **off by default** and starts in one
   command when a question needs answering.
4. Bounded disk, by construction rather than by discipline.
5. Answer, at minimum: *why is cold start N seconds*, *where does a turn spend
   its time*, *is this session about to fill its PVC*, *what has the fleet cost
   me this week*.

**Non-goals.**

- Cluster-side Prometheus/Grafana, ServiceMonitors, or any always-on collector.
  Explicit maintainer constraint: this is local.
- Always-on telemetry. The default posture is off; armed telemetry is a
  debugging mode, not steady state.
- Replacing the normalized event model. `schema/events.json` is a user-facing
  contract at protocolVersion 3 and telemetry must not touch it.
- Alerting, SLOs, long-term retention.

## Design

### D1. Two observations that shape everything

**`events.db` is already a trace.** `session.started` / `turn.started` /
`tool.started` / `tool.completed` carry timestamps and ids — that is a span tree
in all but name, persisted on the PVC, replayable via `after=`, and already
drained by `internal/runner/client.go`. A converter from events to OTLP spans
yields full turn-level tracing for every session **retroactively, with zero
pod-side code and zero hot-path risk**. This is the highest value-per-line move
available and it is stage 1.

**The runner can measure its own pod.** `/proc/self/status`, `/proc/<child>/stat`,
and `statvfs('/session')` give CPU, RSS, and PVC free bytes from inside the
container. No metrics-server, no cluster Prometheus, no RBAC change — which is
exactly what the local-only constraint requires. "Pod resource usage over time"
is a self-reported gauge, not a cluster query.

### D2. Wire format: OTLP/JSON, newline-delimited

Telemetry is written as files where **each line is one complete OTLP/JSON export
request** — `{"resourceSpans":[…]}`, `{"resourceLogs":[…]}`, or
`{"resourceMetrics":[…]}`.

This is the exact input format of the OpenTelemetry Collector's
`otlpjsonfilereceiver` (contrib). The consequence is the point of the decision:
**writing needs no SDK and no collector** — only correct JSON — while reading is
a nine-line collector config. Neither process takes an OTel dependency in stage
1-3; the shape is small and stable enough to emit by hand, and the existing
`trace: <id> <name> <dur>` lines map onto it directly.

Rejected alternatives: the Go SDK's `stdouttrace`/`stdoutmetric` exporters emit
their own JSON shape, not OTLP, and would need a translation step anyway; a
live OTLP push from the pod would require either an always-on local receiver or
cluster egress, both excluded above.

Adopting `go.opentelemetry.io/otel` properly is a reasonable stage-6 follow-up
once the shape has proven itself, and the emitter API (D4) is designed so that
swapping the backend is a file-local change.

### D3. Where telemetry lives, and how it gets home

Pod side, alongside the existing state root (`runner/src/types.ts:200`):

```
/session/state/sandbox/telemetry/{traces,logs,metrics}-NNN.jsonl
```

Host side, under the existing per-session state dir:

```
~/.local/share/sandbox/remote-sessions/<id>/telemetry/*.jsonl
```

**Transport is the existing Mutagen transcript sync.** `internal/sync/sync.go:332-334`
already creates a one-way remote→host session per entry in `TranscriptSubs`
(`:241`). Telemetry is a fourth such group — same mechanism, same lifecycle,
same GC, no new protocol and no new port. It is created in the background
alongside the other non-load-bearing syncs (`client/sync.go:264`) so it cannot
delay connect.

The CLI writes its own telemetry to the same per-session directory when it can
attribute it to a session, and to `~/.local/share/sandbox/telemetry/` otherwise
(create/list/doctor paths). One glob covers both for ingestion.

### D4. The emitter

One narrow module per language — `internal/telemetry` (Go) and
`runner/src/telemetry.ts` — exposing three verbs and nothing else:

```
span(name, attrs) -> handle with End()   // nested; parent from context
log(level, msg, attrs)                   // structured, trace-correlated
record(instrument, value, attrs)         // counter / histogram / gauge
```

Requirements:

- **Nil-safe and near-free when disarmed**, matching the existing tracer idiom
  (`client/trace.go:63` — a nil `*tracer` makes every call a no-op). No
  allocation, no clock read, no branch cost that matters on the frame path.
- **Armed by the existing `SANDBOX_TRACE` / `--trace` gate.** A third switch is
  not warranted; the flag already means "I am debugging performance." A separate
  `SANDBOX_TELEMETRY_DIR` overrides the output path.
- **Trace context propagates across every seam**, including the pane WebSocket
  upgrade — the current one-route gap (`server.ts:418`) is closed here.
- **All attribute values pass through the existing redactor**
  (`runner/src/redact.ts`, re-exported from `audit.ts`). Prompts, tool inputs,
  and file contents are never span attributes; sizes and hashes are.

The three existing tracers collapse into this. `client/trace.go`'s 14 spans
become a nested tree under one `sandbox.connect` root; `runner/src/trace.ts`'s
boot phases become children of `runner.boot`; `pane_rtt.go`'s percentiles become
a histogram instrument. The human-readable `trace: …` stderr line is **kept** as
a second sink — it is genuinely useful without a stack running, and losing it
would make the change a downgrade for the common case.

### D5. Traces: what gets a span

| span | parent | source |
|---|---|---|
| `sandbox.connect` / `sandbox.create` | root | `client/session.go:287`, `client/client.go:533` — existing spans, nested |
| `runner.boot` + phase children | root | `runner/src/index.ts:124` — existing |
| `session.turn` | root | reconstructed from `turn.started`/`turn.completed` |
| `tool.<name>` | `session.turn` | reconstructed from `tool.started`/`tool.completed`/`tool.failed` |
| `permission.request` | `session.turn` | `permission.requested` → `permission.resolved` |
| `pane.attach` | `sandbox.connect` | upgrade → auth → spawn/resume → first frame |
| `observer.ingest` | `session.turn` | one per `POST /observer/claude/*` |
| `event.append` | varies | `appendEvent` → SQLite write |
| `portforward.establish` / `.reconnect` | `sandbox.connect` | one per reconnect |

The first four rows require **no new instrumentation** — they are derived from
the existing event log (D1). Rows five onward are new emitter calls.

**Never a span per pane frame.** Frames are the highest-volume thing in the
system by orders of magnitude; they are covered by histograms (D6), not spans.
This is the single rule that keeps the design affordable.

### D6. Metrics catalog

Runner-side, per session (`session.id` as a *resource* attribute, not a label):

| instrument | type | why |
|---|---|---|
| `runner.pane.frame.bytes` | histogram | frame-size distribution; input to the slow-link decision (TODO §4) |
| `runner.pane.frame.latency` | histogram | PTY read → WS write; the untimed frame path |
| `runner.pane.ws.buffered_bytes` | gauge | headroom against the 4 MiB 4003 cap (`events.ts:150`) |
| `runner.pane.close` | counter | by code: 4001 preempt / 4002 child exit / 4003 backpressure |
| `runner.observer.ingest.duration` | histogram | closes TODO §4 `[P6]` with a number instead of a recipe |
| `runner.observer.ingest` | counter | by hook event; the per-turn POST cadence `[P6]` asks for |
| `runner.event.append.duration` | histogram | the append-before-stream critical path |
| `runner.eventlog.rows` / `.bytes` | gauge | **the PVC-fill early warning** |
| `runner.pvc.free_bytes` | gauge | `statvfs('/session')` |
| `runner.sse.clients` | gauge | against `MAX_SSE_CLIENTS = 16` (`events.ts:131`) |
| `runner.sse.dropped` | counter | `events.ts:609` logs these and counts nothing |
| `runner.process.cpu` / `.rss` | gauge | runner and PTY child, from `/proc` |

CLI/fleet-side, derived from data already on the wire:

| instrument | type | source |
|---|---|---|
| `fleet.sessions` | gauge | by status — live / suspended / gone |
| `fleet.session.age` | gauge | from the local index |
| `fleet.tokens` | counter | by kind (input/output/cache-read/cache-write), from `usage.updated` |
| `fleet.cost_usd` | counter | `totalCostUsd`, per session and summed |
| `fleet.plan.utilization` | gauge | 5h / 7d / Opus / Sonnet, from `rate_limit.updated` |
| `fleet.reaper.suspends` | counter | reaper actions |
| `fleet.portforward.reconnects` | counter | session flapping |
| `cli.connect.duration` | histogram | cold-start distribution over time, not one run |

**Cardinality rule, enforced in review:** turn id, event seq, tool input, and
file path are never metric labels. Session id is a resource attribute. Tool name
and outcome are acceptable labels — bounded and useful.

### D7. Logs

Runner: a `runner/src/log.ts` replacing all ~30 `console.*` calls with
level/timestamp/component/session-id/trace-id records. It emits **both** human
text to stdout (so `kubectl logs` stays readable) and OTLP/JSON to the file sink
when armed. This closes `C10` in `docs/oss-launch/HARDENING-BACKLOG.md` and the
caveat at `docs/runner-api.md:387-390`.

CLI: keep `dbg()`'s shape — it is already the right schema — and (a) actually
call it, at the ~40 places that currently log nothing, and (b) add a file sink,
because stderr under the dashboard's alt-screen is unusable and that is the
main workflow.

New command `sandbox logs <session> [-f]`, served over the existing port-forward
from a new runner route, so seeing runner output never requires `kubectl`.

### D8. Disk budget

The constraint the maintainer named, and the one most likely to make this
unpleasant if handled loosely.

- **Off by default.** Disarmed, zero bytes are written.
- **Size-capped rotation**, not time-based: N files × M MB per stream per
  session, oldest unlinked on roll. Defaults `4 × 16 MB` per stream ⇒ a hard
  ceiling of ~192 MB per session across all three, on the PVC and mirrored
  host-side. A ~60-line writer in each language.
- **Aggregation over enumeration** on hot paths (D5's frame rule). A histogram
  is a fixed number of bytes per export interval regardless of traffic; a span
  per frame is unbounded.
- **Metric export interval** of 15s while armed. Nothing is emitted between.
- **GC** folds into the existing `sandbox gc` (`internal/cli/commands.go:501`),
  which already prunes per-session local state, plus the Mutagen sync GC path.
- The local stack's own storage is a Docker named volume, so `docker volume rm`
  is a complete reset.

Rotation caps are per-stream constants in one place in each language so a
maintainer can raise them for a long soak without hunting.

### D9. The local stack

`dev/observability/compose.yaml`, two services:

1. `grafana/otel-lgtm` — Grafana + Prometheus + Tempo + Loki, pre-wired
   datasources, OTLP on 4317/4318, UI on 3000.
2. `otel/opentelemetry-collector-contrib` — reads our files, pushes OTLP:

   ```yaml
   receivers:
     otlpjsonfile:
       include: [/telemetry/**/*.jsonl]
   exporters:
     otlp: {endpoint: lgtm:4317, tls: {insecure: true}}
   service:
     pipelines:
       traces:  {receivers: [otlpjsonfile], exporters: [otlp]}
       metrics: {receivers: [otlpjsonfile], exporters: [otlp]}
       logs:    {receivers: [otlpjsonfile], exporters: [otlp]}
   ```

   with `~/.local/share/sandbox` bind-mounted read-only at `/telemetry`.

Driven by `just obs-up` / `just obs-down` / `just obs-reset`.

**Why Docker rather than flox services**, given this repo's flox-first posture:
the LGTM backends need four hand-written config files plus Grafana datasource
provisioning to stand up natively, and `grafana/otel-lgtm` ships all of that
pre-wired. The Docker daemon is already a documented prereq for `just dev` (the
KIND cluster; `.flox/env/manifest.toml` pins `docker-client` and notes the
daemon is host Colima), so this adds no new dependency class — and compose
up/down is a cleaner on/off switch than service supervision for something meant
to be off most of the time.

A flox-native variant pinning `opentelemetry-collector-contrib`, `tempo`,
`loki`, `prometheus`, and `grafana` as `[services]` is viable and is the right
fallback if the maintainer prefers zero Docker; it costs roughly a day of config
plumbing and needs Prometheus 3.x with `--web.enable-otlp-receiver` for native
OTLP ingest. **Maintainer decision — see [Open decisions](#open-decisions).**

### D10. Security

Telemetry files are a new place for secrets to leak: they land on the PVC, cross
the Mutagen sync, and sit unencrypted in the host state dir.

- Every attribute value passes `redactSecrets` (`runner/src/redact.ts`) — the
  same masking `audit.jsonl` and the event log already use. Go gets the
  equivalent.
- Prompt text, tool inputs, file contents, and env values are **never**
  attributes. Lengths, hashes, and counts are.
- Files are written `0600`, in a directory created `0700`, matching the
  credential-materialization convention (`runner/src/agent-auth.ts:38`).
- The telemetry sync is one-way remote→host, matching transcripts — nothing on
  the laptop is pushed into the pod by this feature.
- `SECURITY.md` gains a paragraph, and the redaction path gets a test that feeds
  a known token through a span attribute.

## Sequencing

Each stage is independently useful and independently shippable.

1. **`events.db` → OTLP spans + the stack.** A converter (`sandbox trace
   --otlp`, or a `telemetry export` subcommand) plus `dev/observability/`. No
   pod-side change, no hot-path risk, and it works on sessions that have already
   run. Proves the whole pipeline end to end. **~1 day.**
2. **Runner self-metrics.** `/proc` + `statvfs` gauges, event-log size, SSE and
   pane counters. Answers the pod-resource question and puts a number on the
   unbounded-growth risk. **~1 day.**
3. **Unified logging.** `runner/src/log.ts` (closes C10), CLI file sink + real
   `dbg()` call sites, `sandbox logs`. **~1-2 days.**
4. **Hot-path spans.** Pane attach, observer ingest, `appendEvent`; trace-id
   propagation through the pane upgrade so claude-pane correlates end to end.
   Delete `startTurnTrace` and its three tests. **~1-2 days.**
5. **Fleet metrics.** Cost/tokens from `usage.updated`, session counts, reaper
   actions, plan utilization. **~1 day.**

Stage 1 alone answers "why is cold start 4 seconds" and "where does a turn spend
its time" for every session already on disk.

## Open decisions

1. **Stack packaging** — Docker compose (recommended, D9) vs flox-native
   services. Affects stage 1 only; the file format is identical either way.
2. **Arming switch** — reuse `SANDBOX_TRACE` (recommended: one concept, already
   documented, already means "I'm debugging performance") vs a distinct
   `SANDBOX_TELEMETRY`. Reuse means arming tracing also writes files, which is a
   behavior change for anyone scripting against the current stderr-only output.
3. **Default rotation caps** — `4 × 16 MB` per stream per session is a guess.
   Worth one real soak before it hardens.
4. **Retention default** — this design surfaces PVC fill but does not fix it.
   Flipping `RETENTION_MAX_EVENTS` (`runner/src/events.ts:153-155`) to a
   non-zero default is a separate, arguably overdue decision; the gauge from
   stage 2 should inform it rather than the reverse.

## Verification

Per `docs/verification-protocol.md`, each stage needs a correctness oracle and a
behavioral counter:

- **Stage 1:** a golden test converting a fixture `events.db` to OTLP JSON and
  asserting span parentage and timing; the counter is loading the output into a
  live collector and seeing the trace render in Tempo.
- **Stage 2:** unit tests over the `/proc` and `statvfs` parsers with fixture
  input; the counter is a live session whose `runner.eventlog.bytes` gauge
  tracks actual file growth under load.
- **Stage 3:** a test that every log record carries level/component/session; the
  counter is `kubectl logs` remaining human-readable after the migration.
- **Stage 4:** a test that a trace id set on connect appears on a pane-attach
  span; the counter is a benchmark showing the disarmed frame path is unchanged.
- **Stage 5:** a test that cost accumulates correctly across `usage.updated`
  events; the counter is fleet cost matching a known session's real spend.

Disk is verified once, directly: arm telemetry, run a session to saturation,
confirm the on-disk total plateaus at the configured ceiling rather than growing.

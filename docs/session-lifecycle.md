# Session lifecycle: ephemeral pods, idle reaping, and reconnect

Status: **implemented and live-validated** (design approved 2026-06-18;
end-to-end path verified on a real cluster 2026-06-23, see
`docs/archive/done-log-2026-06.md`). The CLI, runner, and reaper code paths are
implemented and unit-tested; runner + reaper images publish to GHCR via
`.depot/workflows/`. The cluster GitOps wiring (RBAC, namespaces, network
policy) ships as example manifests under `k8s/`. This document is both the
design and the implementation checklist for making sandbox session pods
ephemeral and resilient.

## Goals

1. **One pod per session.** Two sessions never share a pod/PVC, even in the same
   project directory.
2. **Auto-suspend on idle.** When a session is idle, terminate its pod (keep the
   PVC). Reconnecting spins up a fresh pod with the same storage.
3. **Graceful reschedule.** On a planned pod termination (node drain/reboot,
   suspend, eviction) the user gets a warning and the client reconnects
   automatically once the pod is back.
4. **Best effort on abrupt loss.** Hard node failure can't be signalled; we rely
   on PVC durability + controller reschedule + client reconnect, and surface
   guidance to the user.

## Definitions

- **Idle** = *turn-done AND detached*: no turn is running **and** no SSE client
  is attached. Background processes are intentionally **not** considered. (Chosen
  for simplicity; revisit if leftover dev servers/watchers become a problem.)
- **Grace period** = **15 minutes** of continuous idle before suspend.

## Key design decisions

### Unique session IDs (done)
`sandbox claude` mints a fresh ID per invocation: `claude-sdk-<pathhash6>-<rand>`
(minted in `client/client.go`, `newSessionID`). The path hash keeps sessions grouped by
project at a glance; the random suffix guarantees distinct pods. Reconnecting is
done by **explicit ID** via `attach`, `status`, etc. — not by re-deriving from
the path.

### Idle clock lives in the runner, not the reaper
The runner tracks `idleSince`: set the moment the session becomes idle
(turn-done AND attachedClients==0), cleared when a turn starts or a client
attaches. Exposed at `GET /sessions/:id/idle` →
`{ turnActive, attachedClients, idleSince }`.

This makes the reaper **stateless**: a freshly (re)scheduled reaper just reads
`idleSince` and suspends if `now - idleSince >= 15m`. Reaper restarts never miss
or double-count the window, and the grace is correctly measured from when the
user detached, not from the last turn.

### Reaper = per-session Kubernetes Job
When a session starts (and on `attach`/`resume`), the CLI ensures a Job
`reap-<sid>` exists that watches only that session:

- Polls the runner `/idle` every ~30s.
- When `now - idleSince >= 15m`, patches the Sandbox `replicas: 0` (the existing
  suspend mechanism — pod gone, PVC retained) and exits 0.
- `ttlSecondsAfterFinished` then deletes the Job ("self-deletes").
- Resilient infinite loop; only ever exits 0 after suspending. High
  `backoffLimit` + a `podFailurePolicy` that ignores infra disruptions, so the
  Job keeps watching across pod death rather than giving up.

Implemented as a hidden `reap` subcommand on the existing `sandbox` Go binary
(reuses `internal/k8s` + `internal/runner`), shipped in a small image.

**Why a Job, not a Deployment:** a Deployment (`replicas:1`) restarts its pod on
exit and so can't cleanly "finish"; a Job completes on success and self-cleans
via TTL with less RBAC. The idle-clock-in-runner design removes the only
reliability concern (missed windows on restart).

**Namespace constraint:** the reaper cannot run in `agent-sessions` — that
namespace's egress NetworkPolicy blocks the k8s API, so it could not issue the
suspend. It runs in `agent-reaper` (API egress allowed) and reaches the session
pod cross-namespace on :8787. Because the runner-token Secret (`<sid>-runner`)
lives in `agent-sessions` and can't be cross-namespace mounted, the reaper reads
it via the k8s API (RBAC `get secrets` in agent-sessions).

### Graceful reschedule (SIGTERM)
On SIGTERM (drain/suspend/eviction) the runner:
1. Emits `session.terminating` `{ reason, graceSeconds, turnsAborted }` so the
   TUI shows a banner.
2. Aborts in-flight turns (existing `turn.abort`).
3. Flushes (events.db is append-before-stream durable; checkpoint WAL) and exits.

The pod spec sets `terminationGracePeriodSeconds` (~60–120s) to give this room.

### Client auto-reconnect (shared infra)
Reaper-suspend, node drain, and a transient port-forward drop all look the same
to the client: the pod went away but the session persists on the PVC. So the CLI
has one reconnect loop used by `claude` and `attach`:

- On SSE/stream end, re-resolve the session: if suspended, resume (replicas
  0→1) and wait ready; re-establish the port-forward; rebuild the runner client;
  re-attach SSE from `after=<lastSeq>` (replaying anything missed).
- The TUI shows a "reconnecting…" banner and renders `session.terminating`.

**Resume runs the same binary it suspended with.** Once a session's pod first
goes Ready, the backend stamps the kubelet-resolved digest of the running
runner image onto the Sandbox (`sandbox.cullen.dev/pinned-runner-image`).
Resume rewrites the pod template's image from that annotation (relaxing an
auto-resolved `PullAlways` to `IfNotPresent` — the digest is immutable) before
scaling 0→1, so a moving tag (`:latest`) advancing while the session was
suspended cannot swap the runner under the session's persisted
`events.db`/`session.json`. Covers every suspend path (CLI and reaper); when no
digest could be captured (e.g. a locally-loaded dev image), resume falls back
to the tag as before.

For **abrupt** node loss there is no warning; recovery waits on RWO ceph volume
force-detach (~minutes). The TUI surfaces guidance to the user in that case.

## Components / checklist

Runner (`runner/`):
- [x] `idleSince` tracking + `sseClientCount` hook (`events.ts`, `session.ts`)
- [x] `GET /sessions/:id/idle` (`server.ts`)
- [x] `session.terminating` event type (`types.ts`) + SIGTERM handler (`index.ts`)

Go shared (`internal/session`):
- [x] `session.terminating` event + `TerminatingPayload`
- [x] `IdleStatus` type

Go client (`internal/runner`):
- [x] `Idle(ctx, ref)` method (`client.go`)

Reaper:
- [x] hidden `reap` subcommand (poll → suspend → exit) (`internal/cli/reap.go`)
- [x] `internal/k8s` helpers: pod IP, read runner token via API, ensure reaper Job
      (`PodIP`, `RunnerToken` in `backend.go`; `EnsureReaper` in `internal/k8s/reaper.go`)

CLI/TUI:
- [x] spawn reaper Job on `claude` create + `attach`/`resume`
      (`ensureReaperForSession`, called from `sessionConnector.connect`)
- [x] auto-reconnect loop + reconnecting banner + `session.terminating` render
      (`internal/cli/connect.go`, `internal/tui/dashboard/model.go` + `app.go`)
- [x] `terminationGracePeriodSeconds` in pod spec (`internal/k8s/backend.go`)

Images:
- [x] build/push runner image (`runner/Dockerfile`) to GHCR (`.depot/workflows/build-runner-image.yml`)
- [x] build/push reaper image (`Dockerfile.reaper`) to GHCR (`.depot/workflows/build-reaper-image.yml`)

Cluster (GitOps — example manifests under `k8s/`):
- [ ] `agent-reaper` namespace + ServiceAccount
- [ ] Role in agent-sessions (get/patch sandboxes, get pods, get secrets) +
      RoleBinding to the reaper SA
- [ ] NetworkPolicy ingress exception on agent-sessions pods for the reaper

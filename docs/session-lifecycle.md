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
  is attached. For external-pane backends (claude-pane, opencode) the same
  probe counts **attached pane clients** as activity and the observer drives a
  **synthetic busy** between turn-start and Stop, so a detached-but-working
  agent is never reaped mid-turn. Background processes are intentionally
  **not** considered. (Chosen for simplicity; revisit if leftover dev
  servers/watchers become a problem.)
- **Grace period** = **15 minutes** of continuous idle before suspend.

## Key design decisions

### Unique session IDs (done)
`sandbox claude` mints a fresh ID per invocation: `<backend>-<pathhash6>-<rand>`
(e.g. `claude-pane-ab12cd-x7`; minted in `client/client.go`, `NewID`). The path
hash keeps sessions grouped by project at a glance; the random suffix
guarantees distinct pods. Reconnecting is done by **explicit ID** via `attach`,
`status`, etc. — not by re-deriving from the path.

### claude-pane process lifecycle (claude-pane-first)
The runner owns the interactive `claude` child for the pod lifetime (design
D1/D3, `runner/src/claude-pane.ts`):

- **Lazy spawn on first attach.** The first `GET /sessions/:id/pane` WebSocket
  attach spawns `claude --session-id <runner-generated-uuid>` under node-pty;
  the uuid is persisted in `session.json` as `claude_pane_session_id`.
- **The attach is gated on first-sync staging.** Because the spawn is lazy, the
  attach — not a turn — is what starts an agent for this backend, so
  `client.Session.AttachPane` waits on `AwaitSync` and refuses with
  `ErrInitialSyncFailed` when the session's first-ever project sync never
  staged. Without the gate a broken sync transport boots claude into an EMPTY
  workspace. A slow-but-healthy first upload is only an advisory and does not
  block the attach; a reconnect is never gated (the workspace already staged
  once).
- **Detach keeps it alive.** Ctrl+] closes the WS; the child keeps running
  (core product behavior). Reattach replays a bounded scrollback ring
  (~256 KiB) so the screen repaints instantly. One concurrent attacher; a new
  attach preempts the old (WS close 4001).
- **Child exit → `--resume` chain.** On child exit the supervisor records the
  reason, emits status (+ a synthetic turn-abort for an open turn), closes any
  attached WS (4002), and respawns with `claude --resume <uuid>` on the next
  attach — the conversation continues.
- **Suspend/resume rides the same chain.** Pod suspend kills the child; the
  PVC keeps `CLAUDE_CONFIG_DIR` (transcript, credentials); resume boots a
  fresh runner that spawns `--resume <uuid>` on the next attach.
- **Env hygiene.** The child sees an allowlisted env (PATH/HOME/TERM/LANG/
  CLAUDE_CONFIG_DIR + workspace cwd) — never the runner token or credential
  env vars, because provisioned hooks inherit the child's entire env.

### opencode credential provisioning (host harvest → per-session Secret)
Like claude-pane, an opencode session boots from a **per-session Secret**, not a
shared cluster credential. At create, `sandbox opencode` harvests the host's own
`opencode auth login` store (`auth.json`) and seeds it into the session's own
Secret under key `opencode-auth-json`, injected to the pod as env
`OPENCODE_AUTH_JSON`; the runner materializes it back to
`$XDG_DATA_HOME/opencode/auth.json` (mode `0600`, PVC-persisted, so a pod-side
token refresh survives suspend/resume). A host with **no** local opencode login
falls back to the shared `opencode-credentials` Secret. This mirrors codex's
`codex-auth-json` → `$CODEX_HOME/auth.json` seed; see the credentials section of
[`../README.md`](../README.md) and [`backend-conformance.md`](backend-conformance.md).

### Per-session git worktrees (create / convert / destroy)
For a git project, `sandbox claude` creates the session on its own git worktree
at `~/.local/share/sandbox/remote-sessions/worktrees/<id>`
(`<stateDir>/worktrees/<id>`), on an auto-branch `sandbox/<id>` cut
from `HEAD` — so two sessions on one repo never cross-feed edits (the worktree,
not the repo root, is the Mutagen endpoint and pod cwd). `--worktree auto` (the
default) makes this conditional on the project being a git work tree; `on`
requires it, `off` disables it. **Destroy** captures any dirty worktree with a
WIP commit to its branch *before* `git worktree remove` (work is never silently
discarded — I2), leaving the branch behind for the user. Idle **suspend/resume**
never touch the worktree — it is a laptop artifact that persists across the pod
going away. In the dashboard, `b` on a selected session opens the
**convert-to-branch** modal: it renames `sandbox/<id>` onto a human-approved,
title-derived branch name (e.g. `feat/fix-login-flow`) and commits the pending
work, so the session's edits land on a named, mergeable ref (`sandbox worktree
convert` is the headless form). Full design:
`docs/archive/worktree-lifecycle-design.md`.

**Reaping is retention-gated, not "session is gone".** A worktree outlives its
session by design, so `sandbox worktree gc` removes one only when *all* of these
hold: the session is not live in the cluster, the local index proves the current
namespace owns it ([V1]), it has not been touched within `--min-age` (default
7d — the most recent of index activity, branch tip commit time and directory
mtime wins), and its branch has no commits missing from the base branch
(`--reap-unlanded` opts out of that last one). Eligible worktrees are listed
with a per-row reason and confirmed before deletion (`-y` skips the prompt,
`--dry-run` previews). The branch is never deleted — gc removes checkouts, not
work.

### Finishing a session's work (merge back)

The durable unit of work is **the branch, not the session**. A session is a
place an agent was working; the branch outlives it, including destruction. This
is the recipe for getting that work into your main branch.

**1. Look at it.** The worktree is a normal checkout on your laptop — open it in
an editor, run the tests, use any git tool:

```bash
cd $(sandbox worktree path auth-refactor)   # fuzzy: id, branch, or title
git log --oneline
```

`sandbox worktree path` resolves offline against the local session index, so it
works with no cluster reachable. With no argument it offers every session; when
a query matches several, a picker appears **on stderr** so that `cd $(…)` still
receives only the path.

**2. Name the branch.** While the work is still a session, its branch is
`sandbox/<id>` — accurate but unmergeable-looking. Convert it:

```bash
sandbox worktree convert auth-refactor --branch feat/auth --message "auth: rework token refresh"
```

This commits any uncommitted work under your message, then renames the branch.
Both strings are taken verbatim — the command never invents a branch name or a
commit message. In the dashboard, `b` does the same thing with a title-derived
name prefilled. Converting is optional: an unconverted `sandbox/<id>` branch
merges exactly as well, it just reads worse in a PR.

**3. Merge it.** Here is the part that trips everyone up:

> **The branch is checked out by the session's worktree, so `git checkout` in
> the main repo will refuse:**
>
> ```
> fatal: 'feat/auth' is already used by worktree at '…/worktrees/claude-pane-df80e6-…'
> ```
>
> **This is not an error you need to fix.** Git is preventing two checkouts of
> one branch, which is exactly what worktrees are for. Every operation that does
> not require checking the branch out works normally from the main repo:

```bash
git diff main...feat/auth     # review it
git log main..feat/auth       # what's on it
git merge feat/auth           # merge it (from main, without checking it out)
git push origin feat/auth     # push it / open a PR
git rebase main feat/auth     # rebase it
```

If you genuinely need the branch checked out in the main repo — say, to keep
working on it after the agent is done — release it first by destroying the
session (`sandbox destroy <id>`, which captures any dirty state to the branch
before removing the worktree) or by removing just the worktree
(`git worktree remove <path>`). Then `git checkout feat/auth` succeeds.

**4. Clean up.** Destroying a session captures dirty work to its branch, then
removes the worktree; the branch stays. `sandbox worktree gc` does the same
sweep for worktrees whose session is already gone. Neither ever deletes a
branch — deleting work is left to you (`git branch -d`).

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

Cluster (example manifests under `k8s/` — apply per `k8s/README.md`; your real
cluster wiring, e.g. GitOps, is up to you):
- [x] `agent-reaper` namespace + ServiceAccount
      (`k8s/reaper-namespace.yaml`, `k8s/reaper-rbac.yaml`)
- [x] Role in agent-sessions (`sandboxes: get,update` — suspend is
      `sandboxes.Update`, not patch — plus `pods: list` and `secrets: get`) +
      RoleBinding to the reaper SA (`k8s/reaper-rbac.yaml`)
- [x] NetworkPolicy ingress exception on agent-sessions pods for the reaper
      (`k8s/networkpolicy-reaper-ingress.yaml`)

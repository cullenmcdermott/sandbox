# Hardening backlog (pre-prod)

> **Status: referenced provenance backlog — NOT a second active backlog.** This
> file is a point-in-time record (like the `docs/review-*.md` docs), preserved so
> the C*/M*/BR* ids from the archived `PRODUCTION-REVIEW.md` stay resolvable. It is
> **not** part of the single-backlog protocol: `TODO.md`'s "How to use this file"
> preamble is the one canonical, actively-worked backlog. Do not schedule work
> from this file directly — if an item here becomes active, add it to `TODO.md`
> (with a `file:line` pointer) and let this entry remain as its provenance. As of
> 2026-07-12 none of the items below are duplicated as open `TODO.md` entries;
> `docs/runner-api.md` cross-references C10 (structured runner logging) here rather
> than tracking it twice. At OSS-launch these become GitHub Issues (see
> `LAUNCH-CHECKLIST.md`).

Still-open production-readiness / hardening items, extracted from the internal
`docs/design/PRODUCTION-REVIEW.md` before that working doc was archived out of the
public tree. Each item below was **re-verified against the current code** during the
OSS-launch cleanup (see "verified" notes); items that were since fixed are recorded
under "Already fixed (for context)".

Threat-model context: session pods run in the `agent-sessions` namespace with
default-deny ingress + an egress allowlist; the runner API is reached via a local
`kubectl port-forward` and gated by a per-session bearer token. The deployment is
single-operator. Many "attacker" findings therefore drop to low severity — they
mostly matter once this grows to multi-user / per-namespace isolation.

File:line pointers were accurate at extraction time; line numbers drift, so treat
them as anchors, not exact addresses.

---

## Build / runtime artifacts

- **BR2 [low-med] Image pushed as `:latest` (and `:sha`); deploys non-reproducible if the
  manifest references `:latest`.** Pin the Sandbox/Deployment to the `:sha` tag for
  deterministic rollback. *(Verified: `build-runner-image.yml` pushes
  `${IMAGE}:latest` + `${IMAGE}:${{ github.sha }}`; cluster manifest lives in the
  homelab repo, out of this tree.)*
  → `.github/workflows/build-runner-image.yml:48-50`; `spec.RunnerImage` in `internal/k8s/backend.go`

- **BR3 [low] No image vulnerability scan / SBOM / provenance attestation in the build
  workflow.** `docker/build-push-action@v6` is used with neither `provenance:` nor
  `sbom:`. *(Verified: no `sbom`/`provenance` keys present.)*
  → `.github/workflows/build-runner-image.yml:43-50`

- **BR5 [low] `sshd` started unsupervised then `exec node`.** If sshd dies, file sync
  silently stops while the container stays healthy (runner still serving). No
  restart/monitor. *(Verified: entrypoint still does `/usr/sbin/sshd` then
  `exec node dist/index.js`.)*
  → `runner/entrypoint.sh:35,38`

  (BR4 — stable per-PVC SSH host keys — was fixed; see "Already fixed".)

## Kubernetes / reliability

- **M20 [med] Pod runs as root — `runAsNonRoot`/`fsGroup` still not enforced.** BR1
  already landed `allowPrivilegeEscalation: false` + `capabilities.drop:[ALL]` (adding
  back only the sshd-privsep / agent defaults, which removes NET_RAW + MKNOD), and the
  pod has `RuntimeDefault` seccomp. Full non-root is deferred because sshd privsep
  needs root — it requires rearchitecting the sshd/sync transport (unprivileged sshd on
  a high port, or a different transport) + `fsGroup` for PVC ownership + a live
  validation deploy. *(Verified: the deferral comment is still in backend.go at the
  container `securityContext`.)*
  → `internal/k8s/backend.go:644,683-688`

- **M19 [med] Reaper TOCTOU: a turn can start between the idle check and suspend.**
  Mitigated this session (the reaper re-checks `/idle` immediately before suspend,
  narrowing the window) but not fully closed — a pod can still be suspended with a
  freshly-started turn in flight. Graceful-shutdown mitigates data loss; the UX (turn
  aborted) is still degraded.
  → `internal/cli/reap.go` (idle-check → suspend); `runner/src/server.ts` (turn registration)

- **C11-residual [HIGH] Graceful shutdown / `session.terminating` implemented but
  not covered by e2e tests.** SIGTERM/SIGINT handlers abort in-flight turns, emit
  `session.terminating`, and flush the event log — good design, never validated on a
  live cluster or in tests. Suggested e2e: start a turn with a pending permission,
  simulate client disconnect, assert the permission auto-denies and the turn finishes;
  create→detach→assert reaper suspends within idle-timeout + poll + slop; on suspend
  verify `events.db` readable + `session.json` consistent; resume and verify replay.
  → `runner/src/index.ts` (signal handlers); `internal/cli/reap.go`

## Observability

- **C10 [HIGH] Minimal observability: no structured logging or metrics in the runner.**
  Only `console.log`/`console.error`. No structured (JSON, session/turn/event/latency
  fields) logging, no metrics. Suggested: a small structured logger (pino/winston) keyed
  by session ID / turn ID / event type / latency; `--debug`/`LOG_LEVEL` on the CLI.
  *(Verified: no pino/winston/bunyan in `runner/package.json` or `runner/src`.)*
  NOTE: the Go side of `--debug` (CLI JSON-line logging) shipped via the harness plan;
  the TS runner side of that schema is documented in `docs/runner-api.md` but not yet
  emitting. See TODO-ARCHIVE HE-3.
  → `runner/src/index.ts`, `runner/src/server.ts`, `runner/src/session.ts`

- **M35 [low-med] No `/metrics` / Prometheus endpoint.** Turn latency (`durationMs`),
  errors, and usage are captured in the SQLite event log per-event, but there is no
  scrape endpoint, so no alerting / capacity planning / billing data. *(Verified: no
  `/metrics`, no `prom-client`/`prometheus`/OpenTelemetry deps.)*
  → `runner/src/server.ts`

## Security (token-/cluster-gated; mostly matter at multi-user)

- **M29 [med] Bearer token has no rotation / renewal / expiration.** Generated once
  per session, stored in a k8s Secret, valid for the pod's lifetime. Live rotation needs
  the runner to read the token from a *mounted* Secret file + watch it, plus a CLI
  scheduler that rewrites the Secret — architectural; tie to the per-namespace work.
  → `internal/cli/root.go` (token read); `internal/k8s/backend.go` (Secret)

- **M12 [low] Permission IDs use only the first UUID segment (32 bits) — weak entropy.**
  `shortId()` takes `randomUUID().split('-')[0]`. Low risk under the trusted-token model
  (permissions also auto-delete on resolve), but front-running / brute-forcing a pending
  permission ID becomes relevant under multi-user. *(Verified: still
  `randomUUID().split('-')[0]`.)*
  → `runner/src/events.ts:279-280`

- **M28 [low] Mutagen SSH uses `StrictHostKeyChecking no` + `UserKnownHostsFile
  /dev/null` (no host-key verification).** Intentional for `127.0.0.1` ephemeral
  port-forwards (auth boundary = per-session ed25519 key over a local forward). BR4
  (PVC-persistent host keys) is now in place as the prerequisite for enabling host-key
  pinning later (`accept-new` + `HostKeyAlias`). *(Verified: ssh config still emits
  `StrictHostKeyChecking no` + `/dev/null`.)*
  → `internal/sync/ssh.go:109-120`

- **M13 [med] Tool inputs / commands written to the audit log were unredacted** —
  shipped a redaction pass this session (secret-named fields + known tokens centrally in
  `appendAudit`); listed here for completeness in case redaction coverage needs review.
  → `runner/src/audit.ts`, `runner/src/claude.ts`

- **M14 [med] `/exec` inherited full `process.env` including infra secrets** — fixed
  this session (`/exec` child env strips the runner's own secrets, keeps user vars).
  Re-verify if the exec path changes.
  → `runner/src/exec.ts`, `runner/src/claude.ts`

- **M15 [med] `/exec` + SDK cwd from `PROJECT_PATH` was unvalidated (traversal)** —
  fixed this session via a `resolveWorkspaceDir` traversal guard. Re-verify on changes.
  → `runner/src/exec.ts`, `internal/cli/claude_remote.go`

- **C17 / M27-adjacent [med] `~/.ssh/config` rewrite is TOCTOU-racy** (no file
  locking around read-check-write). Concurrent session creation or manual edits during
  setup can lose user changes. Use flock around the sequence, or append-only, or
  document that concurrent creation is unsupported.
  → `internal/sync/ssh.go` (`ensureInclude`)

- **M24 [low] SSH public key written 0644 (world-readable);** **M25 [low] no secure
  deletion of SSH private keys** (RemoveAll only). Low impact under single-operator.
  → `internal/cli/sync_support.go`

- **M21 [low] `allowedTools` array not runtime-validated** before passing to the SDK
  (TS type is compile-time only). Unknown/non-string entries could cause odd SDK
  behavior.
  → `runner/src/server.ts`

- **M23 [low] Prompt field has no length cap beyond the global 1 MiB body limit.**
  → `runner/src/server.ts`, `runner/src/httputil.ts`

- **M30 [low] No explicit `maxHeaderSize` on `createServer()`** (relies on Node's 16 KiB
  default). Body is capped at 1 MiB.
  → `runner/src/server.ts`

- **M17 [low] OpenCode basic-auth enforcement assumed, not verified at runtime.** The
  runner ensures `OPENCODE_SERVER_PASSWORD` is present but can't verify the binary
  enforces it.
  → `runner/src/opencode.ts`

## Correctness / reliability (low)

- **M2 [low] `turn.interrupted` payload (`{reason}`) has no Go struct** — handler
  hardcodes the reason text.
  → `runner/src/server.ts`; `internal/session/event.go`

- **M5 [low] Turn ID collision** possible only under a narrow rapid-restart edge.
  → `runner/src/session.ts`

- **M6 [low] `idleSince` is null on boot until the first recompute.** No call to
  `recomputeIdle()` in `initRegistry()`; grace-period clock effectively starts at the
  first reaper poll, not at boot.
  → `runner/src/session.ts`

- **M7 [low] Shell `SIGWINCH` handler never `signal.Stop`'d** — leaks one handler per
  shell command invocation.
  → `internal/cli/shell.go:76-80`

- **M10 [low] SSE `streamClient` transport not explicitly closed** — `resp.Body.Close()`
  releases the TCP conn, single-stream-per-session bounds the leak; cosmetic.
  → `internal/runner/client.go`

- **M11 [low] `lastRead` channel close semantics subtle/fragile** — partially addressed
  (closed once via `defer`); pattern still error-prone on refactor.
  → `internal/runner/client.go`

- **M16 [low] 120s abandon window may be too long on disconnect** — `checkAbandoned`
  fires every 120s, delaying permission auto-deny after SSE drop.
  → `runner/src/claude.ts`

- **M34-residual [low] Event log retention is opt-in** (`RETENTION_MAX_EVENTS`, default
  off) + a schema `user_version` were added; default-on retention / compaction is still
  open if long-lived PVCs grow.
  → `runner/src/events.ts`

## UX (low)

- **M31 [low] Output truncation not signaled in the TUI.** The `…[output truncated]`
  marker renders as plain text with no distinctive warning.
  → `runner/src/exec.ts`; `internal/tui/dashboard/transcript.go`, `internal/cli/commands.go`

- **M32 [low] No copy/paste of transcript blocks** (no keybinding / selection UI).
  → `internal/tui/dashboard/transcript.go`

- **M36 [low] Permission-resolve Cmd is fire-and-forget; can be lost if SSE drops
  in-flight.** UI clears pending before the POST; on drop the runner never gets it and
  the turn blocks.
  → `internal/tui/dashboard/transcript.go`

- **M38 [low] Detach/reattach restores the search query but leaves scroll at the
  bottom** (no `scrollToMatch()` after replay).
  → `internal/tui/dashboard/transcript.go`

- **M37-residual [med] Permission grace period across reconnect** — re-anchored this
  session (closes the held-key bypass); revisit if the reconnect path changes.
  → `internal/tui/dashboard/transcript.go`

### UX / design judgment calls (for the maintainer)

- Suspend with an in-flight turn warns but doesn't prevent suspend — confirm or improve
  the message. → `internal/cli/commands.go`
- `claude` accepts a variable arg count without validation
  (`cobra.MaximumNArgs(1)`?). → `internal/cli/commands.go`
- Search overlay help text vs actual bindings (n/N vs enter/^n/^p) — align help to
  reality. → `internal/tui/dashboard` search overlay
- Long-error detail pane / expansion (treat `blockError` like `blockSubagent`).
  → `internal/tui/dashboard/transcript.go`
- Wide-character (emoji/CJK) rendering not thoroughly tested — add a grapheme-width
  render test. → `internal/tui/dashboard`

---

## Already fixed (for context — do not re-do)

These were confirmed fixed during the review/hardening sessions; recorded so the
backlog above isn't misread as a list of regressions.

- **C2** tool.delta wire mismatch + duplicate tool cards (flat-tool dedup by `toolUseId`;
  `PartialJSON` added to `ToolPayload`).
- **C3** stale `busy` status after restart (`reconcileLoadedStatus`: busy→idle on load).
- **C4** SSE oversized-event silent loop (raised scanner ceiling to 16 MB; visible
  `EventError` on `ErrTooLong`).
- **C5** destroy orphaned cross-namespace resources + early-return (Create pinned to
  `b.namespace`; Destroy attempts all deletions via `errors.Join`).
- **C7** watch backpressure stalling the informer (replaced with a non-blocking
  `coalescingBuffer`).
- **C8** unguarded `JSON.parse(editedInput)` turn hang (+ auto-deny-after-timeout,
  `PERMISSION_MAX_WAIT_MS`, idempotent `denyAndResolve`).
- **C12** stale permission badge on reconnect (`applySeed` mirrors `applyPodEvent`;
  cluster status authoritative when suspended/failed).
- **C13** stuck-busy symptom (subsumed by C3); **C14** double-resolve (subsumed by C8's
  settled-guard).
- **C9** readiness + liveness probes on the running pod (`GET /healthz`;
  readinessProbe initialDelay=5/period=10, livenessProbe initialDelay=10/period=30,
  same timeout/failureThreshold). Covered by `TestCreateSessionProbes`.
- **BR1** container `securityContext`: `allowPrivilegeEscalation:false` +
  `capabilities.drop:[ALL]` (drops NET_RAW/MKNOD). *(runAsNonRoot still open — M20.)*
- **BR4** stable per-PVC SSH host keys (`/session/state/sandbox/ssh`) instead of
  regenerating on every boot.
- **M1/M3/M9/M11/M13/M14/M15/M19/M27/M33/M34/M37** — shipped this session with tests
  (see PRODUCTION-REVIEW history at extraction time). M20 + M29 staged with design.
</content>
</invoke>

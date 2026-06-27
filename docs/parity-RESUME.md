# RESUME: frontend + backend testing parity

Kickoff prompt for a fresh session. Paste the block below (or just point the new
session at this file).

---

You're continuing the **frontend + backend testing-parity** effort in
`~/git/sandbox`. Read these first, in order:
1. `docs/testing-parity-plan.md` — the plan + the two-surface matrix (the source of truth).
2. `docs/backend-conformance.md` — the conformance checklist the shared harnesses enforce.
3. `docs/opencode-turn-adapter-notes.md` — the opencode `serve` API + event mapping + hardening notes.
4. `docs/local-dev-turn-parity-plan.md` — how the local KIND dev env was built.

## State of the world

- **Local dev env is built and live.** KIND cluster `sandbox-local` (context
  `kind-sandbox-local`, `KUBECONFIG=dev/local/.kubeconfig`) is **currently up**
  with the agent-sandbox v0.4.6 controller + the `sandbox-runner:dev` image (which
  includes the opencode turn adapter). Tools (kind/tilt/kubectl/helm) are in the
  Flox env; `.envrc` auto-activates. Recipes: `just dev-up` (cluster+images),
  `just dev-tui [opencode|claude]`, `just kind-test`, `just kind-down`, `just check`.
- **Nothing is committed.** All work is uncommitted in the working tree. Don't
  commit unless asked.
- **`just check` is green** (92 runner tests, Go build/vet/test, e2e, verify).
- **Backends today:** `claude-sdk` (native) and `opencode-server` — opencode now
  drives real one-shot turns through `runner/src/opencode-turn.ts` (an
  `@opencode-ai/sdk` adapter) on the **free** model `opencode/big-pickle` ($0, no
  key). claude live turns are **plumbing-only** until an API key is wired (deferred).

## Done

- **Local dev env + opencode one-shot turn adapter** (live-verified, hardened
  after an adversarial review: deadline backstop, stream-close-on-settle, no
  double-interrupt, stale-session recovery, idempotent tools, leak-safe errors).
- **Parity Phase G (opencode functional gaps)** — all three landed in
  `runner/src/opencode-turn.ts` + `session.ts`/`types.ts`, unit-tested
  (`opencode-turn.test.ts`, 25 cases) AND live-verified on KIND (`just check`
  green; a live probe saw 35 `message.delta` events + a clean second turn on the
  persisted session):
  - **Streaming deltas** — `message.part.delta`(text) → `message.started`/
    `message.delta`; deltas-only parts flush accumulated text on idle.
  - **Permission flow** — mapper emits `permission.requested`/`permission.resolved`;
    `runTurn` auto-responds via `postSessionIdPermissionsPermissionId` (default
    `"once"`, `OPENCODE_AUTO_PERMISSION`-tunable). `mapPermissionDecision`:
    once→allow-once, always→allow-session, reject→deny.
  - **Resume / continuity** — `opencode_session_id` persisted in `session.json`;
    `effectiveOpencodeSession(resume, persisted)` precedence; cleared on
    missing-session error. Removed the module `cachedSessionId` +
    `__resetOpencodeSessionForTest`.
- **Parity Phase A (keystone harnesses)** — a new backend joins by appending a row:
  - `runner/test/backend-contract.ts` `assertMapperInvariants(events)` — the shared
    cross-backend event contract; applied in BOTH `test/mapping.test.ts` (claude)
    and `test/opencode-turn.test.ts` (opencode).
  - `internal/k8sit/local_test.go` `backendCases` table → `backend_test.go`
    `TestBackendTurn` (table-driven, both backends as subtests, live-verified).
  - `internal/tui/dashboard/golden_multiturn_test.go` `renderBackendTranscript` /
    `TestGoldenTranscriptByBackend` (per-backend transcript goldens).
- **Parity Phase D** — schema/known-event-type conformance folded into
  `assertMapperInvariants` (validates every emitted type ∈ `ALL_EVENT_TYPES`).
- **Parity Phase B (partial — model-selection)** — `effectiveOpencodeModel(turn,
  session)` (|| precedence, mirrors claude `resolveModel`; fixes a `??` bug that
  skipped the session default on an empty per-turn model) + unit test. Permission/
  resume/streaming unit parity already landed with Phase G. Remaining B: error-
  surface mapping breadth + the in-turn missing-session retry (TODO, see below).
- **Parity Phase C (k8sit conformance suite)** — `internal/k8sit/conformance_test.go`,
  all table-driven over `backendCases`, all live-verified green:
  - `TestBackendErrorSurface` — bogus model → the turn SETTLES + **no wedge** (session
    idles, accepts a follow-up). Live finding: opencode rejects an unknown model
    (`turn.failed`), claude tolerates it (`turn.completed`) — so no-wedge-on-settle
    is the parity assertion, not "bad model → failed".
  - `TestBackendInterrupt` — long turn → `InterruptTurn` → `turn.interrupted` + idle
    (skips plumbing-only backends).
  - `TestBackendReconnectReplay` — `after=<seq>` replays only newer events, never the
    cut event, re-delivers the terminal.
  - `TestBackendLifecycle` — suspend→resume→turn (asserted SSE-independently via
    `/idle` poll + post-settle log replay) + Destroy idempotency.
  - **TWO real bugs found by the suite + fixed (both live-verified):**
    1. **`Backend.Resume` returned ~10-15s too early** — `waitForPodReady` matched the
       OLD terminating-but-Ready pod (DeletionTimestamp set; getPodForSandbox's R7
       fallback), so a turn started right after resume was orphaned on the dying pod.
       Fix: `waitForPodReady` now ignores a pod with a `DeletionTimestamp`
       (`backend.go`) + unit tests (`backend_resume_ready_test.go`).
    2. **opencode resume raced `opencode serve` boot** — `ensureSession`'s persisted/
       resume path returned the id blind, so the first turn after a resume failed
       with "fetch failed". Fix: it now probes `client.session.get` with the same
       retry budget as the create path (404 → clear+recreate; connection error →
       retry) (`opencode-turn.ts`).

- **Parity Phase F (cross-backend UX-parity)** — `internal/tui/dashboard/parity_ux_test.go`:
  `TestUXParityStatusLineMetrics` (status line byte-identical across backends given
  identical metrics), `TestUXParityInterrupt`, `TestUXParityVimModeSwitch`. Caught a
  **third real bug**: `models.Limit` ranged a provider map and returned a RANDOM
  match, so a model listed by multiple models.dev providers (claude-opus-4-8 is under
  8, e.g. anthropic=1M vs azure=200k, venice prices 6/30) resolved nondeterministically
  → the status-line ctx%/cost flickered. Fixed: deterministic canonical-provider pick
  (`internal/models/models.go` + `TestMultiProviderDeterministicCanonical`).
- **Parity Phase E (decision + goldens)** — DECISION (maintainer): **keep the external
  PTY pane** as opencode's interactive UI; make its surrounding chrome first-class via
  the runner observer (the forward path, tracked in TODO "Runner-as-metrics-observer").
  The transcript stays the headless/test path. Adverse-state goldens added for the
  shared renderer: `golden_adverse_test.go` (permission box, ExitPlanMode plan card,
  error block, mid-stream message).

- **Parity Phase H (dev-env hardening)** — DONE (plan: docs archived; approved plan
  ran low-risk-first). `just check` is green AND now runs golangci-lint locally
  (Flox-pinned 2.12.2). Highlights:
  - **Flox-pinned the whole dev/CI toolchain** (ctlptl, docker-client, golangci-lint,
    jq, mutagen added) so nothing leaks from the laptop; `just doctor` fails if a
    tool resolves outside `$FLOX_ENV` (caught Homebrew `jq`).
  - **ctlptl owns the cluster lifecycle** (`dev/local/ctlptl.yaml`; `kind-up`→apply,
    `dev-nuke`→delete); **Hall** image delivery wired into `dev-image` with a
    `kind load` fallback (host setup documented; auto-detected). `just dev [backend]`
    = doctor+cluster+images+TUI; `just dev-reset` wipes rogue sandboxes (keeps the
    cluster). All live-verified (ctlptl kind-up + dev-image + TestBackendTurn + reset).
  - **golangci-lint ratcheted** to staticcheck + goimports (goimports was already
    clean); fixed ~8 staticcheck findings incl. a real `SA4000` test bug; scoped
    SA1019 carve-outs for two un-migratable deprecations.
  - **CI moved off GHA to Depot** (`.depot/workflows/`; `.github/workflows/` removed —
    you run `depot ci migrate` to connect).
  - **Removed a dead theme duplicate** (the "wire-in" turned out wrong): the whole
    `internal/tui/dashboard/theme.go` local palette was an orphaned duplicate of the
    shared `tui/theme` package (render code uses `tui/theme` exclusively; the local
    `colorXxx` were referenced only by the file + its test). Deleted it + the dead
    `modeLine`/`styleAgent` → lint green, zero render impact.

## Done — all unblocked phases complete

Phases G, D, A, B, C, F, E, H done. `just check` green; the live KIND suite green.
Remaining is BLOCKED only:
- **I** Claude/OpenAI real turns — needs the API key (you provide). Then wire
  `dev/local/secret.local.yaml` + flip `TestBackendTurn/claude` to assert a real
  haiku reply (the harness gate `expectRealReply` already flips automatically).
- **J** Codex onboarding — needs the Codex backend to exist (`docs/codex-integration-plan.md`).

Account/host follow-ups you own: `depot ci migrate` (connect Depot), Hall host setup
(NRI socket on Colima + `hall daemon`; arm64/Colima still UNVERIFIED).

## Gotchas / conventions

- Run go cmds in-sandbox with `GOPATH=/tmp/gopath GOMODCACHE=/tmp/gomodcache
  GOFLAGS=-mod=mod GOCACHE=$TMPDIR/go-build-cache`. Runner unit tests + docker/kind
  need the command-sandbox **disabled** (httptest ports / docker socket).
- After ANY `runner/src` change, `just dev-image` (rebuild + `kind load`) before
  `just kind-test`, or the pod runs stale code.
- opencode auth = HTTP Basic, user `opencode`, pw `OPENCODE_SERVER_PASSWORD`.
- opencode role filter: the user prompt streams as a text part on the USER message;
  the mapper maps only assistant parts (`message.updated(role)` precedes its parts).
- `t.Skip` must carry `// gate-ok: <reason>` (anti-cheat gate in scripts/verify.sh).
- ultracode mode is ON this session (use Workflow for substantive multi-agent work);
  the new session may differ.

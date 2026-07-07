# OSS-readiness implementation plan (execution)

Durable plan for the pre-open-source pass. Companion to `ASSESSMENT.md` (findings).
Working/internal doc — remove or archive before the public commit.

## Decisions (final)
- Autonomous execution; aggressive (break/rename/remove freely for clean v1).
- Internal docs: keep CLAUDE.md + AGENTS.md (scrubbed); **drop TODO.md** (preserve open items to `docs/oss-launch/` first).
- K8s manifests: author an example `k8s/` dir.
- Half-built features: **implement both** `todo.updated` and permission **`session` scope**.
- cmd/mockup: **remove** (`git rm`), reword provenance comments.
- Build/CI/registry: **build hygiene only** — commit `runner/package-lock.json`, add `/dist/` + `/mockup` to `.gitignore`, document image prerequisite. **Do NOT** change image defaults (`registry.cullen.rocks` stays), the publish workflow, or add a reaper build. GHCR publishing is deferred by the maintainer.

## Shared contracts (all agents must agree)

### `todo.updated` event payload (`TodoPayload`)
JSON: `{ "todos": [ { "content": string, "status": "pending"|"in_progress"|"completed", "activeForm": string } ] }`
Go (`internal/session/event.go`):
```go
type TodoItem struct {
    Content    string `json:"content"`
    Status     string `json:"status"`
    ActiveForm string `json:"activeForm,omitempty"`
}
type TodoUpdatedPayload struct {
    Todos []TodoItem `json:"todos"`
}
```
- Schema source of truth: add the payload to `schema/events.json`, then regenerate via `go run ./cmd/gen-eventschema` (updates `eventtypes.gen.go` + `events.gen.ts`).
- Runner emits on the SDK `TodoWrite` tool use. TUI renders the list at `transcript.go:1702`.

### Permission `session` scope (v1 semantics)
- When a permission is resolved with `scope:"session"`, the runner records a **tool-name-level** grant for that session. Subsequent `canUseTool` calls for the same tool auto-allow without prompting, for the session lifetime (in-memory; OK to persist to session state).
- Documented as such in `docs/runner-api.md` (clearly: a session grant allows all future uses of that tool for the session).

### Conventions
- Go: run `gofmt -w` on changed files; build/vet your own package with `GOCACHE=$TMPDIR/go-build-cache GOFLAGS=-mod=mod go build ./internal/<pkg>/... && go vet ./internal/<pkg>/...`. Do NOT run port-binding tests (command sandbox blocks `internal/runner`, `internal/models`, and any `httptest`/SSH-listening test) — read them instead. The full gate is run centrally.
- Runner: `cd runner && ./node_modules/.bin/tsc --noEmit` to typecheck; `npm test` for unit tests (pure tests run in-sandbox; do not add port-binding tests).
- Re-verify every "dead code" claim with `rg` reference counts before removing.
- Do NOT `git add`/`git commit` (the maintainer commits). Working-tree edits + creating files only. Exception: archival removes files from the tree (see its task).
- **Stay in your lane**: edit only your owned files; never touch another agent's files.

## Phases

### Phase 0 — git index landmine (done directly by orchestrator)
- `git rm --cached internal/tui/model.go` (dead old TUI, staged-AD; worktree already deleted).

### Round A — Foundation + Core (parallel, disjoint file ownership)
1. **oss-files** — NEW files only: `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `.github/ISSUE_TEMPLATE/*`, `.github/PULL_REQUEST_TEMPLATE.md`, and `k8s/` example dir (namespace, default-deny + egress-allowlist NetworkPolicy, agent-sandbox install pointer, PVC/StorageClass notes, README). Security model: runner runs as root, network is the boundary, REQUIRES default-deny+egress NetworkPolicy, Mutagen skips host-key check, bring/override your own runner+reaper image. Open-hardening items (C9 probes, C10/M35 metrics+logging, BR2 :latest pin, BR3 SBOM, BR5 sshd supervision, M20 runAsNonRoot, M29 token rotation, M12 perm-id entropy, M28 StrictHostKeyChecking) listed as Known Limitations.
2. **ci-build** — `.gitignore` (remove `runner/package-lock.json` line; add `/dist/` + `/mockup`); ensure `runner/package-lock.json` is in sync (`cd runner && npm install`); confirm `runner/Dockerfile` `npm ci` is correct with the committed lockfile. Do NOT touch `build-runner-image.yml`, `ci.yml`, or image defaults.
3. **archival** — Extract still-open items from `docs/design/PRODUCTION-REVIEW.md` → `docs/oss-launch/HARDENING-BACKLOG.md`; extract still-open items from `TODO.md` → `docs/oss-launch/TODO-ARCHIVE.md`. Move `docs/design/impl/verification-protocol.md` → `docs/verification-protocol.md`. Then remove from the tree (`git rm -r --cached --ignore-unmatch <p>; rm -rf <p>`): all of `docs/design/**` (except the moved verification-protocol.md), `TODO.md`, `cmd/mockup/`. (Provenance comments reworded by `tui` in Round B.)
4. **event-model** — `schema/events.json` add `TodoPayload`; `go run ./cmd/gen-eventschema`; add `TodoItem`/`TodoUpdatedPayload` to `event.go`; remove `State.ForwardPort` + `State.SSHForwardPort` (`types.go`, verified 0 refs); add clarifying comments on emitted-but-unconsumed `SessionStartedPayload.{Tools,PermissionMode,ClaudeSessionID}` + `State.ClaudeSession`; keep `schema_test.go` green. Owns `schema/events.json`, `internal/session/*`, `runner/src/events.gen.ts` (via gen). ONLY agent that runs the generator.
5. **k8s** — remove `NewForConfig` (0 refs); port-forward: observe `Done()`/reconnect or surface a visible error (`portforward.go`); `retry.RetryOnConflict` (or Patch) for `setReplicas`; fix `watch_test.go:161` stale comment; make `defaultStorageClass` empty→cluster-default (overridable); add `restURLForPodPortForward` trailing-slash test; add `reaper_test.go` for `EnsureReaper` 3 branches. Keep `DefaultReaperImage` as-is. Owns `internal/k8s/*`.
6. **cli** — factor shared `provisionSession` + thread `runnerImage` through `newDashboardCreator`; call `idx.Delete` on destroy (both `commands.go` destroy + `sync_support.go` local-destroy hook); add `sessionKeyDir` traversal test; extract `reaperTick` over an interface + test (not-yet-idle / idle-past-timeout / M19 re-check); add `sanitizeLabel` + destroy-confirm tests; scrub `root.go:3-6` package doc + `cmd/sandbox/main.go:6` docs/superpowers link. Keep `defaultRunnerImage` as-is. Owns `internal/cli/*` + `cmd/sandbox/main.go`.
7. **index** — make `Save` load-merge + per-file lock (same signature, fixes lost-update); `List` surfaces skipped corrupt entries; remove `SaveFromState` (0 refs); add tests (atomic write + 0600 perms, corrupt-file skip, traversal via public API, lost-update). Owns `internal/index/*`.
8. **runner-client** — surface server `{"error":...}` body in every non-2xx path + SSE non-200 (`client.go`); fix `RunerClient` typo; comment the single-line-SSE-frame assumption; add `dashboard.RunnerClient` compile assertion + `Idle` comment; add error-path tests. Owns `internal/runner/*`.
9. **sync** — assert sync direction (alpha/beta) in tests; fix `Spec.SSHHost` doc + a fixture to alias form; swallow not-found in `PauseAll`/`ResumeAll`; validate sessionID in `SSHConfig.Upsert`/`Remove`; add `ExecRunner` test; add `isMutagenNotFound` third-branch test. Owns `internal/sync/*`.
10. **docs** — README (add `trace` row, fix `opencode` no-prompt signature, add Quickstart leading with the dashboard, image prerequisite, reframe homelab refs); CLAUDE.md (`23`→`24`, scrub `~/src/system-config` + Nix store paths, fix `internal/tui`→`internal/tui/dashboard` row, add `internal/models`+`internal/e2e` rows); AGENTS.md (scrub, `verify-tui.sh`→`scripts/verify.sh`, verification-protocol path → `docs/verification-protocol.md`); architecture.md (add models+e2e, fix tui row, reframe homelab); runner-api.md (add `mode` on /turns, `model` on /status, `passive=1`+429+heartbeat on /events, `/exec` audit note, trim permission body, generic projectPath, GET /sessions note, document `session` scope as implemented + `todo.updated`); session-lifecycle.md (fix `internal/tui/model.go` pointer→dashboard, reframe homelab). Owns those .md files. Document `session`-scope + `todo.updated` as the TARGET behavior.

### Round B — Consumers (parallel; depend on event-model)
11. **tui** — remove no-op Tab/focus (Focus type/field/handler + keymap binding); remove `minDetailWidth`; remove local `func max`; fix `UpdateExtra` comment; remove `/compact` + `/undo` from `commandGroups()`; render real `TodoUpdatedPayload` at `transcript.go:1702`; reword "Ported from cmd/mockup" provenance comments to past tense; add `applyEditorResult` test; clean the ~23 dead theme/style vars. Owns `internal/tui/dashboard/*`.
12. **runner-ts** — emit `todo.updated` (map SDK `TodoWrite` → `TodoPayload`) in `claude.ts`; implement permission `session` scope (track tool-level grants per session; `canUseTool` checks grants; `server.ts` permission-resolve stores `scope:"session"`); reconcile reboot `session.started` emit (`index.ts:73` add model/cwd, drop off-schema fields); wire or delete the 5 unused TS interfaces (`types.ts`); add `runner/test/auth.test.ts` + `runner/test/events.test.ts`; make `claude.ts` runTurn mapping testable + table-test. Owns `runner/src/*.ts` (NOT `events.gen.ts`) + `runner/test/*`.

### Phase 3 — Gate + fix (orchestrator)
Run the full gate with sandbox disabled: `go build ./...`, `go vet ./...`, `gofmt -l`, `go test ./...`, `cd runner && tsc --noEmit && npm test`, regen drift check. Dispatch targeted fix agents for failures; iterate to green.

### Phase 4 — Review + report
Adversarial review workflow over the diff; produce `docs/oss-launch/LAUNCH-CHECKLIST.md` (manual maintainer steps: publish/visibility of images, create public repo, etc.); report.

## Status
- [x] Phase 0 — git rm --cached internal/tui/model.go
- [x] Round A — 10 agents landed; build/vet/gofmt green, all Go tests pass, 37 runner tests pass, tsc clean
- [x] Reconcile (orchestrator): moved dashboard.RunnerClient assertion out of internal/runner → internal/cli/connect.go (decoupled runner from TUI tree); CLAUDE.md Task-backlog section repointed off deleted TODO.md → GitHub Issues; scrubbed dangling docs/design/harness-engineering-plan.md refs in docs/verification-protocol.md + docs/architecture.md
- [x] Round B — tui (dead Tab/focus + 11 dead vars + /compact,/undo removed; real todo rendering; provenance reworded) + runner-ts (todo.updated emit, session-scope grants w/ allow-session, reboot emit reconcile, 5 HTTP-body interfaces wired, auth.ts/mapping.ts/grants.ts extracted + tests). Build/vet/gofmt/gen-drift green; go test all pass; runner tsc clean + 63 pass/1 skip.
- [x] Phase 3 — full gate green; docs flipped to "implemented" for session-scope + todo.updated; fixed latent colorBusy nil-color bug; cleaned final 9 unused symbols and ENABLED golangci `unused` (now 0 issues); decoupled runner from TUI tree.
- [x] Phase 4 — LAUNCH-CHECKLIST.md written; 5-way adversarial review done. Verdicts: session-scope SOUND, mapping-refactor SOUND, k8s-misc SOUND, index SOUND (1 edge case), portforward BUG-FOUND. Fixes applied + re-gated:
  - **HIGH** portforward: caller `ready` only signaled on attempt 1 → a transient first-attempt failure that recovered stranded the caller until the 5s timeout AND tore down the recovered forward. Fixed: per-attempt bounded watcher signals caller `ready` exactly once on first success (added `forwardOnceFn` seam + regression test `TestForwardPortRecoversWhenFirstAttemptFailsBeforeReady`).
  - **LOW** portforward doc comment corrected (no terminal give-up; h.err internal-only).
  - **LOW** TUI rename-clear: empty rename now a no-op (matches CLI) so it can't diverge from the merge-on-Save persistence.
  - Carry-forward (verified-correct, low): session-scope grant *integration* wiring in claude.ts makeCanUseTool is covered only at the pure-helper level (grants.test.ts) — logic traced sound by review; an integration test is a nice-to-have, not added to avoid a fragile seam in the SDK permission path pre-launch.

## FINAL GATE (all green)
- go build/vet ./... clean; gofmt clean; `go test ./...` all pass; e2e build-tagged compiles.
- golangci-lint (errcheck, govet, ineffassign, **unused**) → 0 issues.
- gen-drift: eventtypes.gen.go / events.gen.ts unchanged after regen.
- runner: tsc --noEmit clean; npm test 63 pass / 1 skip (guarded sqlite event-log test).
- k8s package passes `-race`.

### Round A outcomes of note
- event-model extended cmd/gen-eventschema to support nested object arrays (TodoItem); schema_test drift gate extended accordingly.
- k8s: NewForConfig removed; setReplicas now RetryOnConflict; port-forward reconnect loop added; defaultStorageClass "" (cluster default).
- cli: provisionSession factored + runnerImage threaded through dashboard creator; idx.Delete on both destroy paths; reaperTick made testable.
- index: Save now locked load-merge (lost-update fix); List surfaces corrupt entries; SaveFromState removed.
- Carry-forward flagged by agents (Phase 3/4): wire ForwardHandle.Done() consumer (optional UX); docs/superpowers is gitignored (won't ship — OK); enable golangci `unused` after dead-var cleanup.

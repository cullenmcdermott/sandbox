# Pre-open-source assessment — findings & roadmap

> Working doc for the OSS-readiness effort (2026-06-22). Internal/scratch —
> decide whether to delete or archive before the public commit. Raw per-finder
> output (15 finders, ~70 findings) is preserved at the workflow task output:
> `/private/tmp/claude-501/-Users-cullen-git-sandbox/7c0f1ec4-aace-4b46-89ff-ac8b80dbabe6/tasks/wq9l818gp.output`

## Mandate (maintainer decisions, 2026-06-22)
- Execution: autonomous (write plan, execute via subagents, report at end).
- Priorities: all four — streamlining/archival, tests, bugs/correctness, docs.
- Stale design docs: `git rm` (delete; carry forward live gaps to a fresh doc).
- Aggression: aggressive — break/rename/remove freely for a clean v1.

## Biggest launch risks (from synthesis)
1. **Broken build from a fresh clone.** `runner/package-lock.json` is gitignored but `runner/Dockerfile` runs `npm ci` (fails without lockfile). CI masks it via `npm install --ignore-scripts`. `build-runner-image.yml` needs `runs-on: kubevirt-runners` + Tailscale secrets → can't run on a fork.
2. **Broken out-of-box.** Default runner/reaper images point at private `registry.cullen.rocks` → ImagePullBackOff for any external user.
3. **Shipping dead/private content.** Pre-initial-commit: a careless `git add -A` would commit dead `internal/tui/model.go` (staged AD), a 47MB `dist/` binary, a 5.8MB `mockup` binary, and the 58KB internal `TODO.md` ledger.
4. **Security optics.** `docs/design/PRODUCTION-REVIEW.md` reads as a disclosure map of unmitigated weaknesses — and is the only record of real open prod gaps (no probes/metrics, `:latest` image), so it can't just be deleted.
5. **Untested security boundary.** `runner/src/server.ts` auth (constant-time compare w/ a documented prior timing-leak fix) + cross-session `:id` routing have zero tests; `events.ts` seq/replay (the reconnect backbone) is untested.
6. **Dangling doc links.** `main.go`/`architecture.md` link into gitignored `docs/superpowers/`; `docs/design/` is untracked but referenced by `AGENTS.md`/`CLAUDE.md`.

## Roadmap

### P0 — launch-blocking

**A. Fix broken-from-fresh-clone build**
- Commit `runner/package-lock.json` (remove `.gitignore:4`); regenerate cleanly via `npm install` in `runner/` first.
- `build-runner-image.yml`: `runs-on: ubuntu-latest`, drop/gate Tailscale step behind `if: github.repository == 'cullenmcdermott/sandbox'`; keep GHCR push gated to main.
- Ensure `schema/events.json`, `cmd/gen-eventschema`, both `*.gen.*` are in the initial commit so the `just gen` drift gate is real.

**B. Scrub private infra from defaults/docs/CI**
- Default both images to public GHCR: `internal/cli/claude_remote.go:20`, `internal/k8s/reaper.go:28`. Reframe `registry.cullen.rocks` as optional mirror in README.
- `docs/runner-api.md:31` `/Users/cullen/git/homelab` → generic.
- Drop private-repo refs: `CLAUDE.md:9` (`~/src/system-config/...`), `internal/cli/root.go:3-6`, `cmd/sandbox/main.go:6` (gitignored `docs/superpowers/` link).
- Reframe/include cluster manifests: `README.md:30`, `architecture.md:193`, `session-lifecycle.md:5,128`.

**C. Stage repo cleanly for initial commit**
- `git rm internal/tui/model.go` (dead 407-line old TUI, staged AD).
- `.gitignore`: add `/dist/`, `/mockup`.
- Decide cmd/mockup + cmd/gen-eventschema source (source yes, binaries never).

### P1 — quality bar for a credible release

**D. OSS community-health + security docs** — SECURITY.md (threat model: root pod, network-is-boundary, REQUIRES default-deny+egress-allowlist NetworkPolicy), CONTRIBUTING.md (`just check`, in-sandbox test caveat, `just gen` codegen contract), CODE_OF_CONDUCT.md, issue/PR templates, README Quickstart leading with the dashboard (RV50).

**E. Cross-language API/event-model contract drift** — `23`→`24` event types (CLAUDE.md:72,98); resolve `todo.updated` (no producer); reconcile reboot `session.started` emit (`index.ts:73-78`) with schema; document `mode` on POST /turns, `model` on /status, `passive=1`/429/heartbeat on /events; trim permission-resolve body doc/types.

**F. Highest-risk test gaps** — `runner/test/auth.test.ts` (authOk + cross-session 404), `runner/test/events.test.ts` (append-before-stream, monotonic seq, replay), make `claude.ts` runTurn mapping testable + table-test, Go runner client error-path tests.

**G. Error surfacing** — read+surface server `{"error":...}` bodies in Go client (client.go all status-error sites); observe `ForwardHandle.Done()` / reconnect port-forward; `retry.RetryOnConflict` (or Patch) for `setReplicas`.

**H. Index + CLI session-management** — call `idx.Delete` on destroy (both paths); load-merge/locked `Save` or `Update(id, fn)`; thread `runnerImage` through dashboard creator + factor shared `provisionSession`; test `sessionKeyDir` traversal guard; surface corrupt entries in `List`.

**I. Remove dead code + stale comments** — `NewForConfig`, `SaveFromState`, `State.ForwardPort/SSHForwardPort`, 5 unused runner TS body interfaces, no-op Tab/focus, `minDetailWidth`, local `func max`; fix `RunerClient`/`UpdateExtra`/1024-buffer/`Status: proposal` comments; hide/label `/compact`+`/undo`; enable golangci `unused`.

**J. Archive internal design/review history** — **extract** PRODUCTION-REVIEW open items first (C9 probes, C10/M35 metrics+logging, BR2 `:latest` pin, BR3 SBOM, BR5 sshd supervision, M20 runAsNonRoot, M29 token rotation, M12 entropy, M28 host-key) into a fresh hardening doc/issues, THEN `git rm` the impl/ ledgers, implemented specs, command-center mockup (134KB), PRODUCTION-REVIEW.md. Keep `verification-protocol.md`. Decide TODO.md fate.

### P2 — post-launch / polish
**K. Production hardening** (probes, /metrics+structured logging, image digest pin, runAsNonRoot, token rotation/entropy). **L. Lower-priority tests/consistency** (reaper suspend incl. M19 TOCTOU + EnsureReaper branches, sync direction assertion + SSHHost doc, restURL trailing-slash, sanitizeLabel/destroy-confirm, applyEditorResult, SSE single-line-frame comment, de-flake watch_test 100ms sleep, configurable storageclass/namespaces, CI dep caching + single-source Go version).

## Decisions (locked 2026-06-22)
- **Internal docs:** keep CLAUDE.md + AGENTS.md (scrubbed of private paths); **drop TODO.md** from the public tree (preserve still-open items to `docs/oss-launch/` internal, then `git rm`).
- **K8s manifests:** I author an example `k8s/` dir (default-deny NetworkPolicy + egress allowlist, agent-sandbox install pointer, PVC/StorageClass notes), marked as a starting point.
- **Half-built features:** **implement BOTH** — emit `todo.updated` (map SDK TodoWrite → new `TodoPayload`); implement real permission **`session` scope** (runner tracks granted perms for the session).
- **cmd/mockup:** **remove** from the public tree (`git rm cmd/mockup`); reword `Ported from cmd/mockup` provenance comments to past tense.
- Tab/focus mechanism — remove (no-op binding).
- GET /sessions + bare /sessions/:id — keep, document as /status-equivalent.
- GHCR image anonymous-pullable? — verify during execution (gates default-image fix).

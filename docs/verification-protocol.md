# Verification Protocol (shared)

This file defines the verification philosophy and the shared gates used across the
repo (the `just check` / `just verify` gate that CI runs). It is the durable
reference an implementer uses to prove a package correct; it is not tied to any
specific spec file.

## The principle: hard to fake, not "trust me"

Every exit criterion pairs two checks that pull in opposite directions:

- a **correctness oracle** — an independent reference the implementer does not control: a full
  `glamour` render, an exact expected golden string, a recomputed-from-scratch value. You
  cannot satisfy it by stubbing; the output must actually be right.
- a **behavioral counter** — proof the optimization engaged: render-call counts, a
  sub-quadratic byte budget, a count of how many items were rendered. You cannot satisfy it
  with the naive "do all the work every time" implementation.

Satisfying one alone is easy; satisfying **both** forces a real implementation. Example: an
"always full render" streaming markdown passes correctness but blows the byte budget; an
"always return cached" version passes the budget but fails correctness on the first new
content. Only a correct + incremental implementation passes both.

This is not foolproof — a determined implementer can still game any test — but it raises the
cost of faking well above the cost of just doing it.

## Canonical tests are provided and immutable

Each spec ships its exit-criteria tests as **canonical Go** under a `// CANONICAL TEST —
do not weaken` banner. The implementer copies them verbatim into the package's `_test.go`
files and makes the production code satisfy them. They may:

- add the production code and any *additional* tests,
- adjust the canonical tests only where the spec marks `// IMPL: wire to your API` (e.g. a
  constructor name), keeping every assertion intact.

They may **not** delete assertions, loosen thresholds, add `t.Skip`, or `//nolint`. The gate
(`just verify` / `scripts/verify.sh`) scans for those.

## Shared gates (`just verify` / `scripts/verify.sh`)

> **History:** the gate was once `scripts/verify-tui.sh`, over-fitted to an
> early TUI overhaul — it also checked goal-coupled invariants like the
> existence of specific packages. Those stages were retired; the durable,
> goal-independent parts below were salvaged into `scripts/verify.sh`, run by
> `just verify` and as part of `just check` (which is what CI runs).

`just verify` enforces the two checks that are valuable regardless of the goal:

1. **Anti-cheat scan** — no `panic(`/`not implemented`/stub in non-test source; no
   `t.Skip` or bare `//nolint` in any Go file under `internal/`, `cmd/`, the public
   `client/` + `tui/` packages, and `sdktest/`. A line carrying an explicit
   `// gate-ok: <reason>` is exempt (a visible, reviewable acknowledgement).
   `TODO`/`FIXME` are *not* forbidden — this repo uses `TODO.md` as its backlog.
2. **Race + determinism** — `go test -race ./...` over the whole module, run twice
   (catches global-state leakage and nondeterministic output). `./...` stops at
   nested modules, so the `sdktest/` conformance module is covered by
   `just sdk-conformance` instead (its tests have no concurrency).

The wider gate is `just check`: gen drift, `gofmt`, lint, build, vet, the full test
suite, runner typecheck, SDK conformance (`just sdk-conformance` — the external
`sdktest/` module compiling + testing the public SDK as a consumer), then
`just verify`, then the e2e smoke test. Per-package during development, run
e.g. `go test -race -count=1 ./tui/list/...`.

## Determinism requirements (so the gates are stable)

- No wall-clock in rendered output under test. Time-derived strings (relative times, elapsed,
  spinner frames) must be injectable. Tests pass a fixed clock / frame.
- `SANDBOX_REDUCE_MOTION=1` collapses animation to end-state. The gate runs with it set.
- Fixed color profile + theme in tests (don't depend on terminal detection).

## Module facts

- Module path: `github.com/cullenmcdermott/sandbox`; the SDK-conformance
  harness is a separate nested module, `sdktest/`.
- Go toolchain + env per `CLAUDE.md` (writable `GOPATH`/`GOMODCACHE`/`GOCACHE`
  inside the agent command-sandbox).

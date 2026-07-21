# Contributing

Thanks for your interest in `sandbox`. This guide covers the development setup,
the test/verify workflow, and the one codegen contract you need to know about.

By participating you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).
For security issues, see [SECURITY.md](SECURITY.md) — do not open a public issue.

## Project layout

`sandbox` is two components that talk over one HTTP+SSE API:

- a **Go CLI** (`cmd/sandbox`, `internal/*`) that runs on your machine, and
- a **TypeScript runner** (`runner/`) that runs one session per Kubernetes pod.

Start with [`docs/architecture.md`](docs/architecture.md) for the component map and
[`docs/runner-api.md`](docs/runner-api.md) for the API contract between the two
halves.

## Prerequisites

- **Go 1.26+**
- **Node 24+** (for the TypeScript runner)
- **[`just`](https://github.com/casey/just)** — the canonical command surface.

Run `just` with no arguments to list every recipe.

## The gate: `just check`

`just check` is the full gate, and it is exactly what CI runs. A green
`just check` locally means a green CI:

```bash
just check
```

It runs, in order (the `check` recipe in the `Justfile`): `gen` (event-model
codegen + drift gate), `fmt-check` (gofmt, no writes), `lint` (golangci-lint +
eslint), `build`, `vet`, `test` (Go + runner unit tests), `typecheck` (runner
`tsc --noEmit`), `sdk-conformance` (compiles + tests the public SDK from the
separate `sdktest` module, exactly as an external consumer imports it),
`verify` (the anti-cheat + race-twice gate, `scripts/verify.sh`), and `e2e`
(build-tagged CLI↔runner end-to-end tests).

Individual pieces are also addressable as their own recipes:

```bash
just build            # build the whole Go module
just test             # Go + runner unit tests
just lint             # golangci-lint + eslint (CI-enforced; see caveats below)
just fmt              # gofmt
just typecheck        # runner tsc --noEmit
just gen              # regenerate event-model code (see below)
just sdk-conformance  # public-SDK conformance via the sdktest module
just verify           # anti-cheat scan + go test -race, twice (scripts/verify.sh)
just e2e              # CLI↔runner e2e tests (build tag: e2e)
```

### Test caveat: sandboxed environments bind ports

Several suites bind local ports or spawn PTYs that sandboxed dev environments
block. The canonical, kept-current list lives in **CLAUDE.md → "Before you call
it done"**; the short version is run `just test` / `just check` with the command
sandbox disabled, or those tests fail spuriously on port binding rather than on
real defects.

### Linters aren't required locally

`golangci-lint` and `eslint` may not be on every contributor's machine; the local
recipes skip them with a warning when absent. **CI is the hard enforcement point.**
Don't install them globally just to run them — invoke on demand, e.g.:

```bash
nix run nixpkgs#golangci-lint -- run
```

## Event-model codegen contract

`schema/events.json` is the **single source of truth** for the normalized event
model (event-type strings and payload field shapes). The Go and TypeScript sides
are kept in sync from it.

To change an event type or payload:

1. Edit `schema/events.json`.
2. Run `just gen`.
3. Commit the regenerated files alongside your change:
   - `internal/session/eventtypes.gen.go`
   - `runner/src/events.gen.ts`

**Never hand-edit a `*.gen.*` file** — it will be overwritten and CI's
generated-file diff gate will fail. The Go payload structs in
`internal/session/event.go` stay hand-written, but `internal/session/schema_test.go`
validates them against the schema and fails on drift. Scope is event payloads only
(not HTTP request/response bodies or `IdleStatus`).

See the "Event model" section of [`docs/architecture.md`](docs/architecture.md) for
the rationale.

## The `openspec/` references

Some tracked files (`TODO.md`, `docs/architecture.md`) point at paths like
`openspec/changes/<name>/…`. That directory is the maintainer's local OpenSpec
planning workspace and is **not part of the repository** — a fresh clone does
not contain it, and that is expected. In that workflow, work large enough to
need a written plan gets a change directory `openspec/changes/<name>/` holding
`proposal.md` (why + what changes), `design.md` (decisions), `specs/` (delta
specs per affected capability), and `tasks.md` (the implementation checklist);
smaller fixes skip it and land as direct commits tracked in `TODO.md`. When a
change is implemented, its deltas are folded into the main specs
(`openspec/specs/`) and the change directory moves to
`openspec/changes/archive/`. Anything from those artifacts that matters
durably is reflected into `docs/` (see the docs map in `CLAUDE.md`), so you
never need `openspec/` to build, review, or contribute.

## Pull requests

- Branch off `main`; keep PRs focused.
- Run `just check` and make sure it is green before opening the PR.
- Include tests for behavior changes and bug fixes.
- Update the relevant docs when you change behavior or the API contract — the
  list of durable references (and the plan-doc archive convention) is in
  **CLAUDE.md → "Docs map & lifecycle"**.

## License

By contributing, you agree that your contributions are licensed under the same
license as this project (see [`LICENSE`](LICENSE)).

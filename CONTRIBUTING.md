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

It runs, in order: codegen drift check, `gofmt`, lint, build, `go vet`, the Go and
runner test suites, the runner typecheck, and the anti-cheat / race checks.

Individual pieces are also addressable as their own recipes:

```bash
just build       # build the sandbox binary
just test        # Go + runner unit tests
just lint        # golangci-lint + eslint (CI-enforced; see caveats below)
just fmt         # gofmt
just typecheck   # runner tsc --noEmit
just gen         # regenerate event-model code (see below)
```

### Test caveat: sandboxed environments bind ports

`internal/runner` and `internal/models` bind local ports (`httptest`, listeners)
that some sandboxed dev environments block. If you develop inside such a sandbox,
run those suites — and `just test` / `just check` overall — **with the command
sandbox disabled**, or those tests will fail spuriously on port binding rather than
on real defects.

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

## Pull requests

- Branch off `main`; keep PRs focused.
- Run `just check` and make sure it is green before opening the PR.
- Include tests for behavior changes and bug fixes.
- Update the relevant docs (`docs/architecture.md`, `docs/runner-api.md`, or the
  READMEs) when you change behavior or the API contract.

## License

By contributing, you agree that your contributions are licensed under the same
license as this project (see [`LICENSE`](LICENSE)).

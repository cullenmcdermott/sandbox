# justfile — the canonical command surface for this repo.
#
# `just check` is the full gate an agent runs before declaring work done; CI
# (.github/workflows/ci.yml) calls the same targets so local and CI cannot drift.
#
# Environment notes:
#   - Inside the agent command-sandbox the Go toolchain wants writable caches.
#     Export these before invoking just there (they are NOT baked in, so CI and a
#     normal Nix/macOS dev shell use their own defaults):
#         GOPATH=/tmp/gopath GOMODCACHE=/tmp/gomodcache GOFLAGS=-mod=mod \
#         GOCACHE=$TMPDIR/go-build-cache
#   - In-sandbox httptest caveat: `internal/runner` and `internal/models` bind
#     localhost ports that the agent command-sandbox blocks. Run `just test` /
#     `just verify` with the sandbox disabled (CI and normal dev are unrestricted).
#   - Linters (golangci-lint, eslint) are not installed on the Nix host and must
#     not be installed imperatively. Recipes skip them with a warning when absent;
#     CI is the hard enforcement point.

# Show the available recipes.
default:
    @just --list

# The full gate: everything CI runs. Mirrors the CI job graph.
check: gen fmt-check lint build vet test typecheck verify e2e
    @printf '\033[32m%s\033[0m\n' "just check: all gates passed"

# Regenerate the event model from schema/events.json and fail on any drift
# (schema edited without regeneration, or a *.gen.* file hand-edited).
gen:
    go run ./cmd/gen-eventschema
    @git diff --exit-code -- internal/session/eventtypes.gen.go runner/src/events.gen.ts \
        || { printf '\033[31m%s\033[0m\n' "generated files are stale — schema/events.json changed without regen, or a *.gen.* file was hand-edited. Commit the regenerated output."; exit 1; }

# Format Go source in place. Scoped to cmd/ + internal/ (all our Go) so it never
# touches the vendored Go inside runner/node_modules.
fmt:
    gofmt -w cmd internal
    @command -v goimports >/dev/null 2>&1 && goimports -w cmd internal || echo "note: goimports absent, skipping (gofmt applied)"

# Fail if any Go file is not gofmt-clean (no writes). Same scope as `fmt`.
fmt-check:
    @unformatted="$(gofmt -l cmd internal || true)"; \
    if [ -n "$unformatted" ]; then \
        printf '\033[31m%s\033[0m\n' "gofmt: these files need formatting (run 'just fmt'):"; \
        echo "$unformatted"; exit 1; \
    else echo "gofmt: clean"; fi

# Lint Go + TS. Linters skip-with-warning when not installed locally.
lint:
    @if command -v golangci-lint >/dev/null 2>&1; then \
        golangci-lint run ./...; \
    else \
        printf '\033[33m%s\033[0m\n' "warning: golangci-lint not installed — skipping (CI enforces it). Add to Flox or run: nix run nixpkgs#golangci-lint -- run"; \
    fi
    @if [ -x runner/node_modules/.bin/eslint ]; then \
        cd runner && npm run --silent lint; \
    else \
        printf '\033[33m%s\033[0m\n' "warning: runner eslint not installed — skipping (CI enforces it). Run: cd runner && npm install --ignore-scripts"; \
    fi

# Build the whole Go module.
build:
    go build ./...

# Vet the whole Go module.
vet:
    go vet ./...

# Run Go + runner unit tests. See the in-sandbox httptest caveat above.
test:
    go test ./...
    @if [ -d runner/node_modules ]; then \
        cd runner && npm test; \
    else \
        printf '\033[33m%s\033[0m\n' "warning: runner deps not installed — skipping runner tests. Run: cd runner && npm install --ignore-scripts"; \
    fi

# Typecheck the TS runner.
typecheck:
    @if [ -x runner/node_modules/.bin/tsc ]; then \
        cd runner && ./node_modules/.bin/tsc --noEmit; \
    else \
        printf '\033[33m%s\033[0m\n' "warning: runner deps not installed — skipping typecheck. Run: cd runner && npm install --ignore-scripts"; \
    fi

# Durable anti-cheat + race-twice gate (scripts/verify.sh).
verify:
    bash scripts/verify.sh

# End-to-end tests across the CLI↔runner seam (build-tagged `e2e`, kept out of
# the default `go test ./...`). Binds httptest ports — run with the sandbox off.
e2e:
    go test -tags e2e ./internal/e2e/...

# justfile — the canonical command surface for this repo.
#
# `just check` is the full gate an agent runs before declaring work done; CI
# (.depot/workflows/ci.yml) calls the same targets so local and CI cannot drift.
#
# Environment notes:
#   - Inside the agent command-sandbox the Go toolchain wants writable caches.
#     Export these before invoking just there (they are NOT baked in, so CI and a
#     normal Nix/macOS dev shell use their own defaults):
#         GOPATH=/tmp/gopath GOMODCACHE=/tmp/gomodcache GOFLAGS=-mod=mod \
#         GOCACHE=$TMPDIR/go-build-cache
#   - In-sandbox httptest caveat: `client`, `internal/runner`, `internal/models`,
#     and `internal/k8s` bind localhost ports the agent command-sandbox blocks
#     (and `sdk-conformance` may need the network for `go mod tidy -diff`). Run
#     `just test` / `just verify` / `just check` with the sandbox disabled
#     (CI and normal dev are unrestricted).
#   - Linters (golangci-lint, eslint) are not installed on the Nix host and must
#     not be installed imperatively. Recipes skip them with a warning when absent;
#     CI is the hard enforcement point.

# Show the available recipes.
default:
    @just --list

# The full gate: everything CI runs. Mirrors the CI job graph. Ordered
# cheapest-first within reason so failures surface fast (typecheck is seconds;
# sdk-conformance compiles the whole SDK; verify/e2e are the heaviest).
check: gen fmt-check lint build vet test typecheck sdk-conformance verify e2e
    @skipped=""; \
    command -v golangci-lint >/dev/null 2>&1 || skipped="$skipped golangci-lint"; \
    [ -x runner/node_modules/.bin/eslint ] || skipped="$skipped runner-eslint"; \
    [ -d runner/node_modules ] || skipped="$skipped runner-tests runner-typecheck"; \
    if [ -n "$skipped" ]; then \
        n=$(printf '%s' "$skipped" | wc -w | tr -d ' '); \
        printf '\033[33m%s\033[0m\n' "just check: passed ($n gate(s) skipped — CI enforces them:$skipped)"; \
    else \
        printf '\033[32m%s\033[0m\n' "just check: all gates passed"; \
    fi

# Regenerate the event model from schema/events.json and fail on any drift
# (schema edited without regeneration, or a *.gen.* file hand-edited).
gen:
    go run ./cmd/gen-eventschema
    @git diff --exit-code -- internal/session/eventtypes.gen.go runner/src/events.gen.ts \
        || { printf '\033[31m%s\033[0m\n' "generated files are stale — schema/events.json changed without regen, or a *.gen.* file was hand-edited. Commit the regenerated output."; exit 1; }

# Format Go source in place. Scoped to our Go trees (cmd/, internal/, the public
# client/ + tui/ SDK packages, sdktest/) so it never touches the vendored Go
# inside runner/node_modules.
fmt:
    gofmt -w cmd internal client tui sdktest
    @command -v goimports >/dev/null 2>&1 && goimports -w cmd internal client tui sdktest || echo "note: goimports absent, skipping (gofmt applied)"

# Fail if any Go file is not gofmt-clean (no writes). Same scope as `fmt`.
fmt-check:
    @unformatted="$(gofmt -l cmd internal client tui sdktest || true)"; \
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

# SDK-conformance gate: compile + test the public SDK (client, client/cred) from
# the SEPARATE sdktest module, exactly as an external consumer imports it. This
# is what catches a breaking API change (or an internal/... type leaking into an
# exported signature) the moment it lands — the main module cannot check either.
# `go mod tidy -diff` is the drift gate: the child module tracks the parent's
# deps via the replace directive, so a parent dep bump must be propagated with
# `cd sdktest && go mod tidy` and committed. -diff fails WITHOUT writing, so the
# gate never mutates the tree (mirrors the `gen` drift gate's spirit).
sdk-conformance:
    @cd sdktest && go mod tidy -diff \
        || { printf '\033[31m%s\033[0m\n' "sdktest/go.mod|go.sum are stale (parent dep change not propagated) — run 'cd sdktest && go mod tidy' and commit the result."; exit 1; }
    cd sdktest && go vet ./... && go test ./...

# Durable anti-cheat + race-twice gate (scripts/verify.sh).
verify:
    bash scripts/verify.sh

# End-to-end tests across the CLI↔runner seam (build-tagged `e2e`, kept out of
# the default `go test ./...`). Binds httptest ports — run with the sandbox off.
e2e:
    go test -tags e2e ./internal/e2e/...

# ── Local KIND integration environment (dev/local) ──────────────────────────────────────
# A disposable cluster + the agent-sandbox controller for the two-layer
# integration tests in internal/k8sit (CLI → controller → Sandbox CRD → runner
# pod → HTTP+SSE turn loop). These are DELIBERATELY out of `just check`: they
# need Docker + KIND and are run on demand, so CI never depends on a cluster.
#
# kind/kubectl/helm live in the Flox env (.flox/env/manifest.toml) and are only
# on PATH inside `flox activate`. Each dev recipe self-activates that env on its
# first line, so a bare `just kind-up` works whether or not you are in the env.
# All dev recipes point KUBECONFIG at the gitignored dev/local/.kubeconfig (kube
# context kind-sandbox-local).

# Create (or reuse) the local KIND cluster, install the agent-sandbox controller +
# local namespaces, and wait for the CRD + controller to be Ready.
kind-up:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just kind-up
    set -euo pipefail
    export KUBECONFIG="dev/local/.kubeconfig"
    # ctlptl owns the cluster lifecycle: `apply` creates it if absent and REUSES it
    # if present (idempotent), writing the context into our scoped KUBECONFIG.
    ctlptl apply -f dev/local/ctlptl.yaml
    kind export kubeconfig --name sandbox-local --kubeconfig "$KUBECONFIG"
    # Server-side apply: the sandboxes CRD is large; SSA sidesteps the
    # client-side last-applied-annotation size limit and is re-apply safe.
    kubectl apply --server-side -f dev/local/agent-sandbox/manifest.yaml
    kubectl apply -f dev/local/manifests/
    kubectl wait --for=condition=Established crd/sandboxes.agents.x-k8s.io --timeout=120s
    kubectl -n agent-sandbox-system rollout status deployment/agent-sandbox-controller --timeout=180s
    if [ -f dev/local/secret.local.yaml ]; then
        kubectl -n agent-sessions apply -f dev/local/secret.local.yaml
        echo "applied dev/local/secret.local.yaml into agent-sessions"
    fi
    # Provision the Claude OAuth token Secret (anthropic-credentials) from 1Password
    # (op) or $CLAUDE_CODE_OAUTH_TOKEN — mirrors the ESO-provisioned Secret on a real
    # cluster so `just dev` (claude) works without hand-maintaining secret.local.yaml.
    # Non-fatal: a missing token just leaves the claude backend plumbing-only.
    bash dev/local/claude-creds.sh ensure-secret || true
    # Provision the OpenCode Zen API key Secret (opencode-credentials) from 1Password
    # (op) or $OPENCODE_API_KEY — same pattern as above. Non-fatal.
    bash dev/local/opencode-creds.sh ensure-secret || true
    printf '\033[32m%s\033[0m\n' "kind up: context kind-sandbox-local (KUBECONFIG=dev/local/.kubeconfig)"

# Tear the local dev env down: delete the cluster (via ctlptl) and drop its local
# kubeconfig. `dev-nuke` is the friendlier alias (full node reset).
kind-down:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just kind-down
    set -euo pipefail
    ctlptl delete -f dev/local/ctlptl.yaml || kind delete cluster --name sandbox-local
    rm -f dev/local/.kubeconfig
    printf '\033[32m%s\033[0m\n' "kind down: cluster 'sandbox-local' deleted"

# Build the runner + reaper images and load them into the KIND node. The runner
# image is REQUIRED by the integration tests (hard failure). The reaper image is
# best-effort — it is not on the turn-test path, and its Dockerfile is arch-aware
# (TARGETARCH) so it cross-builds for the arm64 KIND node.
dev-image:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev-image
    set -euo pipefail
    # Image delivery: if Hall is active (a host daemon that mirrors the local Docker
    # store to the KIND node; see dev/local/README.md) the node pulls the freshly
    # built tag directly — no `kind load`. Otherwise fall back to `kind load` so the
    # recipe still works without Hall. Force either path with SANDBOX_USE_HALL=1/0.
    deliver() { # $1 = image tag
        if [ "${SANDBOX_USE_HALL:-}" = "1" ] || { [ "${SANDBOX_USE_HALL:-}" != "0" ] && command -v hall >/dev/null 2>&1 && hall status >/dev/null 2>&1; }; then
            echo "  (Hall active — node pulls $1 from the host Docker store; no kind load)"
        else
            kind load docker-image "$1" --name sandbox-local
        fi
    }
    # Runner image — required; any failure here is fatal.
    docker build -t sandbox-runner:dev -f runner/Dockerfile runner
    deliver sandbox-runner:dev
    echo "runner image loaded (sandbox-runner:dev)"
    # Reaper image — best-effort; cross-build the Go CLI for the KIND node arch
    # and pass it through as TARGETARCH (matches Dockerfile.reaper's COPY).
    arch="$(go env GOARCH)"
    mkdir -p dist   # `go build -o` won't create a missing parent dir; dist/ is gitignored
    if CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "dist/sandbox-linux-$arch" ./cmd/sandbox \
        && docker build -t sandbox-reaper:dev --build-arg "TARGETARCH=$arch" -f Dockerfile.reaper . \
        && deliver sandbox-reaper:dev; then
        echo "reaper image loaded (sandbox-reaper:dev)"
    else
        printf '\033[33m%s\033[0m\n' "warning: reaper image build/load failed — continuing (integration tests do not need it)"
    fi

# Run the two-layer integration tests against the running local cluster. Requires
# `just kind-up` + `just dev-image` first. Build-tagged `integration`, kept out
# of the default `go test ./...` and out of `just check`.
#
# Cost model (the suite drives REAL turns): the opencode turn uses opencode's
# default free model (opencode/big-pickle) → $0, no key. The claude turn asserts a
# real reply ONLY when the anthropic-credentials Secret is present (apply your own
# dev/local/secret.local.yaml); without it, claude degrades to plumbing-only and
# makes no model call. So PAID calls happen only when you have deliberately wired a
# key. Each run makes ~2 free opencode calls — run it on demand, not in a loop.
kind-test:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just kind-test
    set -euo pipefail
    # Absolute KUBECONFIG: `go test` runs each test binary with its cwd set to
    # the package dir (internal/k8sit), so a relative path would not resolve.
    # 1200s outer wall clock exceeds the worst-case sum of the suite's inner
    # per-op context ceilings (CreateSession + Start + turn) across the tests.
    KUBECONFIG="$PWD/dev/local/.kubeconfig" go test -tags integration -count=1 -timeout 1200s ./internal/k8sit/...

# Spin up the local cluster AND load the dev images in one shot — the "from a
# clean machine to ready" recipe. Activates flox once, then reuses it.
dev-up:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev-up
    set -euo pipefail
    just doctor
    just kind-up
    just dev-image
    printf '\033[32m%s\033[0m\n' "local dev cluster ready — try: just dev            (claude TUI)"
    printf '\033[32m%s\033[0m\n' "                          or: just dev opencode"

# The all-in-one: provision (doctor + ctlptl cluster + controller + images) and
# launch the TUI. `just dev` (claude) / `just dev opencode`.
dev backend="claude":
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev {{backend}}
    set -euo pipefail
    just dev-up
    just dev-tui {{backend}}

# Verify the dev/CI toolchain all resolves from the Flox env (no laptop leakage),
# the Docker daemon (Colima) is reachable, and report Hall (image delivery) status.
doctor:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just doctor
    set -uo pipefail
    fail=0
    echo "Flox env: ${FLOX_ENV}"
    for t in go node gcc git kind ctlptl docker tilt kubectl helm crictl mutagen golangci-lint jq; do
        p="$(command -v "$t" 2>/dev/null || true)"
        if [ -z "$p" ]; then
            printf '\033[31m✗ %-14s not found in the Flox env\033[0m\n' "$t"; fail=1
        elif [ "${p#"$FLOX_ENV"}" = "$p" ]; then
            printf '\033[31m✗ %-14s LEAKS from the host: %s\033[0m\n' "$t" "$p"; fail=1
        else
            printf '\033[32m✓ %-14s %s\033[0m\n' "$t" "$p"
        fi
    done
    if docker info >/dev/null 2>&1; then
        printf '\033[32m✓ docker daemon  reachable (%s)\033[0m\n' "$(docker context show 2>/dev/null || echo ok)"
    else
        printf '\033[31m✗ docker daemon  unreachable — start Colima:  colima start\033[0m\n'; fail=1
    fi
    if command -v hall >/dev/null 2>&1 && hall status >/dev/null 2>&1; then
        printf '\033[32m✓ hall           active (images delivered without kind load)\033[0m\n'
    else
        printf '\033[33m• hall           not active — dev-image falls back to `kind load` (see dev/local/README.md)\033[0m\n'
    fi
    if [ "$fail" -eq 0 ]; then printf '\033[32mdoctor: all good\033[0m\n'; else printf '\033[31mdoctor: issues above (tools must come from the Flox env)\033[0m\n'; exit 1; fi

# Wipe ROGUE SANDBOXES (and their PVCs + reaper Jobs) but KEEP the cluster — the
# fast "clean slate" between dev sessions without a full node rebuild.
dev-reset:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev-reset
    set -euo pipefail
    export KUBECONFIG="$PWD/dev/local/.kubeconfig"
    if ! kind get clusters 2>/dev/null | grep -qx sandbox-local; then
        echo "no 'sandbox-local' cluster — nothing to reset (run 'just dev-up')" >&2; exit 0
    fi
    # Sandboxes own their pods; delete them, then the PVCs and any reaper Jobs that
    # the controller/reaper left behind in agent-sessions.
    kubectl delete sandboxes.agents.x-k8s.io --all -n agent-sessions --ignore-not-found --wait=false || true
    kubectl delete pvc --all -n agent-sessions --ignore-not-found --wait=false || true
    kubectl delete jobs --all -n agent-sessions --ignore-not-found --wait=false || true
    printf '\033[32m%s\033[0m\n' "dev-reset: rogue sandboxes/PVCs/reaper jobs cleared (cluster kept)"

# (Re)provision the Claude OAuth token Secret (anthropic-credentials) in the local
# cluster from 1Password (op) or $CLAUDE_CODE_OAUTH_TOKEN. Idempotent — safe to
# re-run after rotating the token, without a full `kind-up`. `just dev-claude-secret`.
dev-claude-secret:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev-claude-secret
    set -euo pipefail
    export KUBECONFIG="$PWD/dev/local/.kubeconfig"
    if ! kind get clusters 2>/dev/null | grep -qx sandbox-local; then
        echo "no 'sandbox-local' cluster — run 'just dev-up' first" >&2; exit 1
    fi
    bash dev/local/claude-creds.sh ensure-secret

# Report where the Claude OAuth token resolves from (1Password / env), redacted.
# A debugging aid — does NOT touch the cluster. `just dev-claude-creds`.
dev-claude-creds:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev-claude-creds
    bash dev/local/claude-creds.sh status

# (Re)provision the OpenCode Zen API key Secret (opencode-credentials) in the local
# cluster from 1Password (op) or $OPENCODE_API_KEY. Idempotent. `just dev-opencode-secret`.
dev-opencode-secret:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev-opencode-secret
    set -euo pipefail
    export KUBECONFIG="$PWD/dev/local/.kubeconfig"
    if ! kind get clusters 2>/dev/null | grep -qx sandbox-local; then
        echo "no 'sandbox-local' cluster — run 'just dev-up' first" >&2; exit 1
    fi
    bash dev/local/opencode-creds.sh ensure-secret

# Report where the OpenCode Zen API key resolves from (1Password / env), redacted.
# A debugging aid — does NOT touch the cluster. `just dev-opencode-creds`.
dev-opencode-creds:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev-opencode-creds
    bash dev/local/opencode-creds.sh status

# Full node reset: delete the KIND cluster (via ctlptl). Alias of kind-down.
dev-nuke: kind-down

# Nuke and rebuild from scratch (cluster + controller + images).
dev-recreate:
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev-recreate
    set -euo pipefail
    just dev-nuke
    just dev-up

# Launch a dev build of the CLI/TUI (compiled fresh via `go run`, no install)
# attached to the LOCAL cluster, pinned to the :dev images. KUBECONFIG is scoped
# to dev/local/.kubeconfig so this can only ever talk to the local cluster.
#   just dev-tui            # claude backend (default)
#   just dev-tui opencode   # opencode backend
dev-tui backend="claude":
    #!/usr/bin/env bash
    [ -n "${FLOX_ENV:-}" ] || exec flox activate -- just dev-tui {{backend}}
    set -euo pipefail
    case "{{backend}}" in claude|opencode) ;; *) echo "backend must be 'claude' or 'opencode', got '{{backend}}'" >&2; exit 2;; esac
    export KUBECONFIG="$PWD/dev/local/.kubeconfig"
    if ! kind get clusters 2>/dev/null | grep -qx sandbox-local; then
        echo "no 'sandbox-local' cluster — run 'just dev-up' first" >&2; exit 1
    fi
    exec go run ./cmd/sandbox {{backend}} \
        --runner-image sandbox-runner:dev \
        --reaper-image sandbox-reaper:dev

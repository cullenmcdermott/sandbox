# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go CLI (`sandbox`) and TypeScript runner pod that together run AI coding agents (Claude Agent SDK) in Kubernetes Sandbox pods with PVC persistence, port-forwarded HTTP+SSE runner API, a local Bubble Tea TUI, and Mutagen file sync.

This is the k8s-native successor to a Lima-based local sandbox CLI. The old Lima backend was for local macOS development; this repo targets a remote Kubernetes cluster (agent-sandbox controller).

## Task backlog

`TODO.md` is the backlog, and its **"How to use this file" preamble is the
canonical protocol** — numbered workstreams, every item carries `file:line`
pointers + a fix direction, and finished items get checked off with a one-line
summary while the detail moves to `docs/archive/done-log-YYYY-MM.md`. Follow it.
When you discover or are told about a new issue mid-task, add it to `TODO.md`
(§0 inbox, or the matching workstream) with a `file:line` pointer.

## Docs map & lifecycle

`docs/` holds two kinds of files. Know which you're touching:

**Durable references** — when a change alters what one of these describes,
update the doc *in the same change* (stale references are bugs):

- `docs/architecture.md` — component map, event-model rationale
- `docs/runner-api.md` — the HTTP+SSE contract between CLI and runner
- `docs/session-lifecycle.md` — create/suspend/resume/destroy flow
- `docs/verification-protocol.md` — the `just check`/`just verify` philosophy
- `docs/backend-conformance.md` — the per-backend test contract

**Plans / ADRs / reviews** — everything else is point-in-time. Rules:

- Every plan/ADR carries a `Status:` line near the top (planning / draft /
  implemented / superseded).
- When your work completes or supersedes one, set its status and `git mv` it
  to `docs/archive/` in the same change (update inbound references — `rg` for
  the filename). `docs/archive/` is the **only** archive; done-logs live there
  too. Links *inside* archived docs are as-of-archive-time and stay unfixed.
- `docs/superpowers/` is the maintainer's local-only plan scratch (globally
  gitignored — other clones don't have it). Never rely on it from tracked
  files without saying so.

**Repo hygiene:** if you notice tracked cruft — empty files, stale build
outputs, docs your change just obsoleted — remove or archive it in a dedicated
commit rather than leaving it for the next agent.

## Commands

`just` is the canonical command surface; `just check` is the full gate CI runs.

```bash
just check        # the full gate (== CI): gen drift, gofmt, lint, build, vet,
                  # test (Go + runner), runner typecheck, SDK conformance
                  # (sdktest module), anti-cheat + race-twice, e2e
just              # list all recipes (test, lint, fmt, gen, typecheck, verify, …)

# Individual pieces (all also reachable as `just <target>`):
go build ./cmd/sandbox/           # builds ./sandbox binary
go test ./...                     # Go unit tests
cd runner && npm install --ignore-scripts && ./node_modules/.bin/tsc --noEmit
```

Toolchain: Go 1.26+ and Node 24+. In a constrained/sandboxed environment set `GOPATH=/tmp/gopath GOMODCACHE=/tmp/gomodcache GOFLAGS=-mod=mod GOCACHE=$TMPDIR/go-build-cache`.

### Before you call it done

Run `just check` — it is what CI runs, so a green `just check` means a green CI.
See `docs/verification-protocol.md` for the verification philosophy
(hard-to-fake gates: correctness oracle + behavioral counter). Notes:

- **In-sandbox caveat:** `client`, `internal/runner`, `models`, and
  `internal/k8s` bind httptest/local ports the command-sandbox blocks; run
  `just test` / `just verify` (and `just check`) with the sandbox disabled.
  Other packages test fine in-sandbox, with one exception:
  `internal/tui/dashboard`'s `TestAppExternalPaneEscIsForwardedNotDetached`
  spawns a real `opencode attach` child in a PTY, so it SELF-SKIPS (visibly)
  when the `opencode` binary or a PTY is unavailable — in practice only inside
  the command sandbox. The `opencode` CLI is pinned in the flox env
  (`.flox/env/manifest.toml`), so unsandboxed local runs and Depot CI both
  exercise it for real.
- **Linters** (`golangci-lint`, `eslint`) aren't on the Nix host — local recipes
  skip them with a warning; CI is the hard enforcement point. Don't install them
  imperatively (run `golangci-lint` via `nix run nixpkgs#golangci-lint -- run`).

### Event-model contract

`schema/events.json` is the source of truth for the normalized event model. To
change an event type or payload: **edit `schema/events.json`, then run `just gen`**,
then commit the regenerated `internal/session/eventtypes.gen.go` +
`runner/src/events.gen.ts`. Never hand-edit a `*.gen.*` file. Go payload structs in
`internal/session/event.go` stay hand-written but are validated against the schema
by `internal/session/schema_test.go`; CI also runs a generated-file diff gate.
Scope is event payloads only (not HTTP bodies or `IdleStatus`). See the "Event
model" section of `docs/architecture.md`.

## Architecture

Two components that talk over a shared HTTP+SSE API (see `docs/runner-api.md`):

### Go CLI (`cmd/sandbox/`)

Module: `github.com/cullenmcdermott/sandbox`

| Package | Role |
|---|---|
| `cmd/sandbox` | Binary entry point |
| `internal/session` | Core types: `Spec`, `State`, `Event` (the event types enumerated in `schema/events.json`), `Backend` and `RunnerClient` interfaces |
| `internal/k8s` | `Backend` impl using agent-sandbox v1alpha1 clientset: Sandbox/PVC create, suspend (replicas→0), resume, port-forward via client-go SPDY; reaper Job spec |
| `internal/runner` | HTTP client implementing `RunnerClient`: health, start/interrupt turn, resolve permission, SSE event streaming with `after=<seq>` replay |
| `models` (public) | Resolves a model's context-window limit + per-million-token pricing; drives the TUI ctx% indicator (`models.Limit(model)`) |
| `internal/cli` | Cobra command tree: `claude`, `opencode`, `attach`, `status`, `suspend`, `resume`, `cancel`, `destroy`, `shell`, `sync`, `trace`, `rename` |
| `internal/authstatus` | Offline per-agent auth status report (Claude/Codex/OpenCode providers) behind `sandbox auth status` — deliberately internal presentation machinery, not SDK surface |
| `internal/tui/dashboard` | Bubble Tea v2 command-center: session list, attention routing, transcript, tool cards, permission modal, external (opencode) PTY pane, detach/interrupt keys. App-specific styles/glyphs read `tui/theme` tokens and re-skin via `theme.OnChange` |
| `tui/` (public) | Reusable, importable TUI building blocks split out of the dashboard: `tui/kit` (widgets), `tui/anim` (transitions/spinner), `tui/list` (scrolling list), `tui/theme` (palette registry, exported semantic color tokens, gradient/spinner/fade helpers, status glyphs). No app coupling — other projects can import these directly |
| `client/` (public) | The Go SDK: create/connect/suspend/destroy sessions, turns, SSE events, sync — the CLI and TUI dogfood it. New capability goes HERE first, not in `internal/cli` |
| `client/cred` (public) | Multi-account Anthropic credential store (Keychain/file backends, manifest, token parsing, account→CreateOptions selection) |
| `sdktest/` | SDK-conformance harness: a SEPARATE Go module that imports `client`/`client/cred` as an external consumer, with compile-time signature pins + behavioral contract tests. Run via `just sdk-conformance` (in `just check`). Breaking the public SDK fails here first — when a break is intentional, update the pins in the same change |
| `internal/sync` | Mutagen sync manager: project (two-way-safe), config inputs (one-way), transcripts (one-way) |
| `internal/index` | Local session index at `~/.local/share/sandbox/remote-sessions/<id>/session.json` |
| `internal/e2e` | Build-tagged (`//go:build e2e`) CLI↔runner smoke test driving a full turn across the HTTP+SSE seam |

### TypeScript runner (`runner/`)

Runs inside the Sandbox pod — one session per pod. Serves HTTP on port 8787, SSH on port 22 (for Mutagen sync).

| File | Role |
|---|---|
| `src/index.ts` | Entrypoint: open event log, load session state, init registry, start server |
| `src/server.ts` | node:http server with bearer token auth, routes per API contract |
| `src/claude.ts` | Claude Agent SDK integration: `query()`, hooks (PreToolUse Bash blocking, PostToolUse audit, SessionEnd), `canUseTool` permission flow, SDK message → normalized event mapping |
| `src/events.ts` | SQLite event log via better-sqlite3; append-before-stream invariant; SSE replay |
| `src/session.ts` | Session registry, env config, session.json persistence, turn ID generation |
| `src/audit.ts` | Append-only audit.jsonl log |
| `src/types.ts` | Normalized event/session types mirroring `internal/session/event.go` |
| `src/httputil.ts` | Request body reader helper |
| `Dockerfile` | Multi-stage node:24-slim: build TS, runtime with sshd + sqlite3 |

### Event model

The Go `internal/session/event.go` and TypeScript `runner/src/types.ts` define identical event types (the set enumerated in `schema/events.json`). The runner maps Claude SDK messages into these normalized events, persists to SQLite, then streams via SSE. The Go CLI's `internal/runner/client.go` consumes the SSE stream and feeds events to the TUI.

### Session lifecycle

1. `sandbox claude` → CLI creates Sandbox CRD + PVC in `agent-sessions` namespace
2. CLI port-forwards to the pod's port 8787
3. CLI health-checks the runner, then launches the TUI
4. User types a prompt → CLI POSTs to `/sessions/:id/turns` → runner calls Claude SDK `query()`
5. Events stream via SSE → TUI renders transcript, tool cards, permission prompts
6. User can detach (Ctrl+]) — pod keeps running, PVC persists state
7. `sandbox attach` → port-forward + SSE replay from last seen seq → TUI catches up

### Sync

Mutagen syncs the project directory (two-way-safe), config inputs (one-way host→remote), and transcripts (one-way remote→host). SSH keys per session are managed via Kubernetes secrets.

## Cluster-side

This repo contains the CLI, runner, runner image CI, and **example** cluster
manifests under `k8s/` (namespaces, RBAC, network policy). The maintainer's real
cluster wiring (GitOps apps, secret provisioning) is a separate private
deployment and is not in this repo.

- agent-sandbox controller: v0.4.6
- Session pods run in the `agent-sessions` namespace with default-deny ingress +
  egress allowlist
- Runner image built via `.depot/workflows/build-runner-image.yml` (Depot CI);
  external users build + push their own and pass `--runner-image` /
  `--reaper-image` (see `README.md`)

## Key decisions

- **agent-sandbox v1alpha1** (v0.4.6): uses `replicas` (0/1) for suspend/resume, not `operatingMode` from v1beta1
- **Direct client-go clientset** instead of agent-sandbox Go SDK — the SDK is built around SandboxClaim/WarmPool which doesn't match the one-Sandbox-per-session model
- **Bubble Tea v2** at `charm.land/bubbletea/v2` — v2 moved from `github.com/charmbracelet/`, API changed (`View()` returns `tea.View`, `v.AltScreen = true`, `KeyPressMsg`)
- **One session per pod**: `SANDBOX_SESSION_ID` from env, workspace at `/session/workspace/<projectPath>`, state at `/session/state/sandbox/`

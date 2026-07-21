# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go CLI (`sandbox`) and TypeScript runner pod that together run AI coding agents — the real Claude Code TUI in a runner-owned PTY pane (claude-pane), OpenCode (`opencode serve`), and Codex (app-server, phase 1) — in Kubernetes Sandbox pods with PVC persistence, a port-forwarded HTTP+SSE(+WebSocket pane) runner API, a local Bubble Tea dashboard TUI, and Mutagen file sync. The runner supervises each agent process and *observes* its activity into a normalized event log; it does not drive claude turns (the Claude Agent SDK turn engine was deleted by `claude-pane-first`, 2026-07-20 — `POST /turns` survives only as the opencode headless first-turn adapter).

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

- **In-sandbox caveat:** `client`, `client/models`, `internal/runner`, and
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
| `internal/runner` | HTTP client implementing `RunnerClient`: health, start/interrupt turn (opencode), SSE event streaming with `after=<seq>` replay, pane WebSocket dial (`pane.go` — 4001 preempt / 4002 child-exit / 4003 backpressure close codes) |
| `client/models` (public) | Resolves a model's context-window limit + per-million-token pricing; drives the TUI ctx% indicator (`models.Limit(model)`) and is importable by external SDK consumers |
| `internal/cli` | Cobra command tree: `claude`, `opencode`, `codex`, `attach`, `status`, `suspend`, `resume`, `cancel`, `destroy`, `shell`, `sync`, `trace`, `rename`, `auth`, `doctor`, `worktree` |
| `internal/authstatus` | Offline per-agent auth status report (Claude/Codex/OpenCode providers) behind `sandbox auth status` — deliberately internal presentation machinery, not SDK surface |
| `internal/tui/dashboard` | Bubble Tea v2 command-center: session list, attention routing, the external agent pane (vt emulator behind the `PaneTransport` seam — child-process PTY for opencode, WebSocket for claude-pane), read-only detached activity feed (`feed.go`), create overlay (backend + account pickers), detach/interrupt keys. App-specific styles/glyphs read `tui/theme` tokens and re-skin via `theme.OnChange` |
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
| `src/index.ts` | Entrypoint: open event log, load session state, init registry per `SANDBOX_BACKEND`, start server; SIGTERM shutdown ordering |
| `src/server.ts` | node:http server with bearer token auth (constant-time), routes per API contract incl. the pane WebSocket upgrade (`GET /sessions/:id/pane` — auth before session-id probe) and the observer ingestion routes |
| `src/claude-pane.ts` | claude-pane supervisor: lazy spawn of interactive `claude` under node-pty with a strict env allowlist, `--session-id`/`--resume` chain, 256 KiB scrollback ring, single-attacher preemption (4001), child-exit (4002), 4 MiB send-buffer backpressure close (4003) |
| `src/claude-pane-observer.ts` | Hook/statusline observer: provisions settings + helper scripts + a scoped observer token into `CLAUDE_CONFIG_DIR`, ingests POSTs → normalized events (turn/message/tool/permission/usage/rate-limit), synthetic busy/idle |
| `src/claude-config.ts` | Boot materialization of `.credentials.json` + `.claude.json` seed (only-if-absent, refresh-preserving, fail-closed) |
| `src/opencode.ts` / `src/opencode-observer.ts` / `src/opencode-turn.ts` | `opencode serve` supervisor + SSE observer (via `@opencode-ai/sdk`) + the headless first-turn adapter (the only remaining `POST /turns` consumer) |
| `src/codex.ts` / `src/codex-observer.ts` | codex app-server supervisor (auth.json seeding) + passive JSON-RPC-over-WS observer |
| `src/events.ts` | SQLite event log via better-sqlite3; append-before-stream invariant; chunked SSE replay; backpressure cap; redaction via `redact.ts` |
| `src/session.ts` | Session registry, env config, session.json persistence, turn ID generation, idle clock |
| `src/audit.ts` | Append-only audit.jsonl log |
| `src/exec.ts` / `src/guards.ts` | `/exec` one-shot shell with sanitized env, process-group kill + the shared bash guard |
| `src/types.ts` | Normalized event/session types mirroring `internal/session/event.go` (`events.gen.ts` is generated — never hand-edit) |
| `Dockerfile` | Multi-stage node:24-slim: build TS, runtime with sshd + sqlite3 + the pinned, sha256-verified `claude` binary (both glibc arches) + opencode/codex CLIs |

### Event model

The Go `internal/session/event.go` and TypeScript `runner/src/types.ts` define identical event types (the set enumerated in `schema/events.json`, protocolVersion 3). The runner's per-backend observers (claude hooks/statusline, opencode SSE, codex notifications) map agent activity into these normalized events, persist to SQLite, then stream via SSE. The Go CLI's `internal/runner/client.go` consumes the SSE stream and feeds events to the dashboard read-model and feed.

### Session lifecycle

1. `sandbox claude` → CLI harvests the host's Claude Code login (`cred.SystemMaterial`, Max mode — fail-closed; stored setup-token accounts are rejected for the pane) and creates Sandbox CRD + PVC + per-session Secret in `agent-sessions`
2. CLI port-forwards to the pod's port 8787; runner materializes credentials/config on boot
3. CLI health-checks the runner, then launches the dashboard TUI
4. The dashboard attaches the pane (`GET /sessions/:id/pane` WebSocket over the same forward) → runner lazily spawns interactive `claude` under node-pty → keystrokes/frames stream both ways; the user talks to the real Claude Code TUI
5. In parallel, provisioned hooks + statusline POST observer events → SSE → session list status/metrics, attention routing, and the detached feed
6. Detach (Ctrl+]) — the pod-side child keeps running; PVC persists state
7. `sandbox attach` → port-forward + pane reattach (scrollback-ring replay) + SSE replay from last seen seq

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

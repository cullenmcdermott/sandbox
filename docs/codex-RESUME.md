# Codex backend + credential manager — resume pointer

A paste-able prompt to resume this work in a fresh session. **Canonical state lives
in the docs/memory below — this file is just the on-ramp; if it disagrees with them,
they win.**

- `docs/codex-integration-plan.md` — full design, spike results, auth decisions
- `TODO.md` — current backlog ("Codex backend" + credential-manager sections)
- `docs/review-2026-06-24.md` — the deep-review findings
- Memory: `sandbox-codex-backend`, `sandbox-agent-parity-bar`

---

## Resume prompt (paste this)

```
Resume the Codex backend + credential-manager work on the `sandbox` repo (Go CLI +
TS runner that run AI coding agents in k8s pods). Read docs/codex-integration-plan.md,
TODO.md, and docs/codex-RESUME.md first; recall memory sandbox-codex-backend and
sandbox-agent-parity-bar. Then continue.

DECISIONS LOCKED
- Codex = 3rd agent backend (id `codex-app-server`). Option B: pod runs `codex
  app-server` daemon (remote-control); local `codex --remote` TUI is an external
  pane, mirroring OpenCode. The runner ALSO opens a passive observer connection to
  the app-server to surface metrics — because the parity bar requires startup,
  detach/keybindings, prompt UX, and metrics to be similar across all agents, with
  the runner as the metrics source for every backend.
- Auth = a CLI-owned credential manager (the `sandbox` CLI is the authority): macOS
  Keychain store (optional Secure-Enclave-wrapped blob + Touch ID; file/env fallback
  on Linux), reconciles the agent-sessions Secret on create/connect, prompts for
  renewal. Agent-agnostic (codex first; will also own Claude OAuth + provider keys).
- Auth-mode default = OPENAI_API_KEY (API credits), because the ChatGPT account reads
  free/rate-limited for Codex (live-verified). OAuth-subscription needs a
  Codex-eligible PAID plan; the credential manager supports both.

ALREADY BUILT (uncommitted, on main — branch before committing):
- internal/cred/ — Provider abstraction + Claude/Codex/OpenCode providers (offline,
  secret-free; JWT exp decode for codex) + tests
- internal/k8s/health.go — Ping (/healthz), Host, Namespace
- internal/cli/auth.go — `sandbox auth status` red/green surface + test; registered
  in root.go. All green: go build ./..., go vet, go test, gofmt, race.

SPIKE RESULTS (offline, codex 0.139.0 Homebrew):
- bare `codex app-server` works over stdio (newline JSON-RPC; no-auth `initialize`)
- remote-control/managed daemon needs the STANDALONE install (curl|sh) → bundle in
  the POD image; native channel is a unix socket
- refresh + approvals are DELEGATED to the client (ServerRequest); metrics/auth are
  client requests: account/rateLimits/read, account/usage/read, getAuthStatus,
  account/read (PlanType), model/list

DO NEXT (priority order):
1. ON TAILSCALE/HOMELAB — validate `sandbox auth status` against the live k8s API:
   the kubernetes line should go GREEN (reachable) with ns + host; confirm Ping
   succeeds when up and fails gracefully (timeout, red) when down. This is the main
   k8s-API validation available until the codex backend exists.
2. Write-side credential manager: macOS Keychain store, `sandbox auth login/sync/
   logout`, and the create/connect reconcile that seeds the agent-sessions Secret +
   prompts for renewal. Start with codex's OPENAI_API_KEY path (testable now).
3. `--check` live mode: codex plan/rate-limit via the app-server (catches the
   free-tier issue), provider-key liveness pings.
4. Codex backend plumbing (plan Phase 1): session.BackendCodex, newCodexCmd, k8s
   env/Secret + port-forward spec, runner codex.ts supervisor + metrics-observer,
   image with the standalone codex install. Egress allowlist needs OpenAI/ChatGPT
   auth+API hosts.
5. Resolve the transport fork (B-ws vs B-ssh): needs a standalone daemon (throwaway
   env or the pod image) + a live turn to confirm a 2nd client can observe a thread,
   and whether `codex --remote unix://` works over an SSH-tunneled socket.

ENV/TEST NOTES: `just check` is the full gate. internal/runner + internal/models need
the command-sandbox disabled. Don't `curl|sh`-install codex's standalone build on the
Nix host. Don't commit unless asked; branch off main first.
```

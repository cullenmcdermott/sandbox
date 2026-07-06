# sandbox

Run **Claude** and **OpenCode** coding agents on a remote Kubernetes cluster
instead of on your laptop. Each session is its own pod that keeps its state on a
PVC, so you can detach, close the lid, and pick the conversation back up later —
or run several agents in parallel without tying up your machine.

You drive it all from a local terminal UI: a command-center dashboard that lists
your sessions, flags the ones waiting on you, and drops you into a live chat (or
the agent's own TUI) on demand. The agents run on the cluster; your keystrokes
and files stay local. It targets a cluster running the
[agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) controller
(v0.4.6).

<p align="center">
  <img src="docs/demos/claude-cold-start.gif" alt="Cold-starting a Claude session on Kubernetes from a local terminal: the pod schedules and the workspace syncs, then a streaming chat turn" width="820">
  <br>
  <sub><em>Cold-start a Claude session: the pod schedules + syncs (fast-forwarded), then you chat — all from your terminal.</em></sub>
</p>

> **Status — early.** The component boundaries and the auth/env wiring are
> implemented and unit-tested, but the end-to-end file-sync path (Mutagen over
> SSH) and the runner image build have **not** been validated on a live cluster
> yet. Treat this as a working prototype, not a turnkey product — see the
> [unvalidated paths](docs/architecture.md#unvalidated-paths).

## Why use this

- **Isolation** — each agent runs in its own pod under a default-deny network
  policy, not against your real shell, filesystem, or cluster credentials.
- **Persistence** — session state lives on a PVC, so it survives detach,
  suspend/resume, and CLI restarts. Reconnect and the event log replays.
- **Parallelism** — start many sessions at once and let the dashboard route your
  attention to whichever one needs input next.
- **A free laptop** — the cluster does the work; your machine just renders the UI
  and syncs files.

Persistence in practice: suspend a session (the pod is torn down, the PVC kept),
then re-attach — the pod cold-starts and the conversation picks up exactly where
you left it, follow-up and all.

<p align="center">
  <img src="docs/demos/opencode-resume.gif" alt="Resuming a suspended OpenCode session: the pod cold-starts from the PVC and the prior conversation is restored, then a follow-up prompt continues it" width="820">
  <br>
  <sub><em>Resume a suspended OpenCode session: the cold pod restarts, the conversation is restored, and a follow-up just continues.</em></sub>
</p>

## How it works

1. `sandbox claude` creates a Sandbox CRD + PVC in the `agent-sessions`
   namespace and waits for the runner pod to be ready.
2. The CLI port-forwards to the pod's runner API (port 8787), health-checks it,
   and opens a TUI.
3. You type a prompt; the runner invokes the Claude Agent SDK and streams
   normalized events back over SSE to the TUI (transcript, tool cards,
   permission prompts).
4. Detach with `Ctrl+]` — the pod keeps running and the PVC persists state.
   `sandbox attach <id>` reconnects and replays the event log.

See `docs/architecture.md` for the component design, lifecycle, and security
model (with diagrams), and `docs/runner-api.md` for the HTTP+SSE contract.

### Continue a session locally with `claude --resume`

A sandbox Claude session can be picked up on your laptop with full history. This
is deliberate: the runner mounts the workspace at the session's **real host path**
(e.g. `/Users/you/git/project`, via a PVC `subPath` bind-mount), so the Claude SDK
keys its transcript directory by that path — exactly the path a local `claude`
would use. Transcripts sync one-way remote→host (into `~/.claude/projects/…`), so
once a session has synced you can:

```bash
cd <project>            # the same directory you launched the session from
claude --resume         # pick the session from claude's list, or:
claude --resume <claudeSession>
```

`<claudeSession>` is the Claude SDK session UUID, surfaced by the runner status
API (`GET /sessions/:id/status`, field `claudeSession`) and recorded in the local
session index; `claude --resume` with no id also lists it for that project.

This is a **one-way fork**: local turns run entirely on your laptop and never flow
back to the sandbox — the pod, its guards, and the audit log see none of them. Use
it to keep working offline or hand off to the local CLI, not as a two-way bridge.

## Quickstart

```bash
sandbox                    # open the command-center dashboard:
                           # session list, attention routing, attach, create
sandbox claude "fix the flaky test"   # shortcut: start a NEW Claude session for
                                      # the current project and open its TUI
```

`sandbox` with no args is the way in: it opens the dashboard, where you create,
attach to, and route between sessions. `sandbox claude [prompt]` skips straight to
a new Claude session for the current directory. It always starts a fresh session —
to return to an existing one, run `sandbox attach <id>` or pick it from the
dashboard.

For development, `just` is the canonical command surface (`just` lists recipes,
`just check` is the full CI gate). See `CLAUDE.md` for the toolchain notes.

## Try it locally (no remote cluster)

You don't need a real cluster to kick the tires. A disposable local
[KIND](https://kind.sigs.k8s.io/) environment brings up the agent-sandbox
controller and a runner image, then drops you into the TUI:

```bash
just doctor          # check the toolchain + Docker daemon
just dev             # KIND up + controller + images + the Claude TUI
just dev opencode    # …same, with the OpenCode backend
```

See [`dev/local/README.md`](dev/local/README.md) for the full local-dev guide —
prerequisites, image delivery, and resetting between runs. (Live Claude/OpenCode
turns still need credentials; the dashboard and session-list views don't.)

## Prerequisites

- A Kubernetes cluster with the [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox)
  controller installed. Example manifests for the namespaces, RBAC, and network
  policy live under `k8s/` in this repo; the maintainer's real cluster wiring is
  a separate private deployment.
- A kubeconfig with access to the `agent-sessions` namespace.
- [Mutagen](https://mutagen.io/) on your local machine for file sync.
- **Claude credentials in the cluster.** The default `claude-sdk` backend reads a
  **Claude Code OAuth token** (subscription auth) from a Secret named
  `anthropic-credentials` (key `api-key`) in the `agent-sessions` namespace and
  injects it into session pods as `CLAUDE_CODE_OAUTH_TOKEN`. Generate the token
  with `claude setup-token`, then provision the Secret once:

  ```bash
  kubectl create secret generic anthropic-credentials \
    --namespace agent-sessions \
    --from-literal=api-key="$(claude setup-token)"
  ```

  > This must be a Claude Code OAuth token, **not** an `ANTHROPIC_API_KEY` —
  > Claude Code rejects a raw API key in this slot and every turn would fail to
  > authenticate. (The separate `opencode` backend instead reads provider API
  > keys from an `opencode-credentials` Secret.)

  The reference is optional, so pods start without it, but turns will fail to
  authenticate until it exists.

  **Alternative: per-session accounts (no shared Secret needed).** Instead of
  the cluster-wide Secret you can store one or more Anthropic accounts locally
  and pick one per session — each session pod then receives only that account's
  credential, provisioned into its own per-session Secret:

  ```bash
  sandbox auth login --subscription   # claude.ai login via `claude setup-token`
  sandbox auth login --console        # paste an Anthropic Console API key
  sandbox auth list                   # enumerate stored accounts
  sandbox auth default <id>           # pick the default account
  sandbox claude --account work       # run a session on a specific account
  ```

  In the TUI, `n` → claude opens an account picker (with an add-account flow)
  once at least one account is stored. Credentials live in the macOS Keychain
  (per-account `0600` files elsewhere); with no stored accounts, behavior is
  unchanged — sessions use the shared Secret above.

The per-session runner bearer token is generated by the CLI and stored in a
per-session Secret (`<session-id>-runner`); you do not manage it manually.

- **OpenCode credentials in the cluster (the `opencode` backend).** OpenCode
  sessions read a **provider API key** (a real API key, not a Claude subscription
  OAuth token) from a Secret named `opencode-credentials` in the `agent-sessions`
  namespace. The recognized keys and the env vars they map to are:

  | Secret key         | Env var             | Provider         |
  | ------------------ | ------------------- | ---------------- |
  | `anthropic-api-key`| `ANTHROPIC_API_KEY` | Anthropic (default) |
  | `openai-api-key`   | `OPENAI_API_KEY`    | OpenAI           |
  | `opencode-api-key` | `OPENCODE_API_KEY`  | OpenCode Zen     |

  ```bash
  kubectl create secret generic opencode-credentials \
    --namespace agent-sessions \
    --from-literal=anthropic-api-key="sk-ant-..."
  ```

  Each session is provisioned with **exactly one** provider — the one selected by
  the session (defaulting to Anthropic) — and only that provider's key is mounted;
  the others are not injected at all. That reference is **fail-closed**: if the
  selected key is absent from `opencode-credentials`, the pod does **not** start —
  it stalls in `CreateContainerConfigError` (`kubectl describe pod` shows
  `couldn't find key <key> in Secret agent-sessions/opencode-credentials`) rather
  than starting an agent with no credential. (The credential is validated only for
  *presence* at pod start; there is no key-validity/JIT check — an invalid key
  surfaces as per-turn auth failures inside OpenCode.)

  **Rotation requires a pod restart.** Provider keys are resolved from the Secret
  once, at pod start, via a `SecretKeyRef` env var. Updating `opencode-credentials`
  does **not** reach a running pod; adopt a rotated key by restarting the pod
  (`sandbox suspend <id> && sandbox resume <id>`, or destroy + recreate). The CLI
  stamps a short, non-reversible fingerprint of the provider key each pod started
  against onto its Sandbox and prints a warning on the create/resume reconcile
  paths when the live Secret has drifted from that stamp. The selected key
  otherwise **persists across suspend/resume** unchanged (resume refreshes the
  stamp to whatever the resumed pod actually starts against).

  All of the above is scoped to the `agent-sessions` namespace. For the throwaway
  local KIND cluster, `dev/local/opencode-creds.sh` provisions this Secret from
  1Password or `$OPENCODE_API_KEY` (namespace overridable via
  `$SANDBOX_NAMESPACE`).

- **Container images reachable from the cluster.** The default runner image
  (`registry.cullen.rocks/sandbox-claude-runner:latest`) and reaper image
  (`registry.cullen.rocks/sandbox-reaper:latest`) point at the maintainer's
  private registry, so **external users must build their own**:

  ```bash
  # Runner (the per-session pod that runs the Claude Agent SDK):
  docker build -t <your-registry>/sandbox-claude-runner:latest -f runner/Dockerfile runner/
  # Reaper (the idle-suspend Job):
  docker build -t <your-registry>/sandbox-reaper:latest      -f Dockerfile.reaper .
  ```

  Push both to a registry your cluster can pull from, then point sessions at
  them with `--runner-image <ref>` and `--reaper-image <ref>`. (A wrong or
  unreachable image leaves the pod in `ImagePullBackOff`, which `sandbox claude`
  reports instead of hanging.) The runner image is also built in CI via
  `.depot/workflows/build-runner-image.yml` if you prefer to wire up your own
  registry there.

## Install

```bash
# From source — produces ./sandbox in the repo root:
go build ./cmd/sandbox/

# Or, once the module is published, install the CLI directly:
go install github.com/cullenmcdermott/sandbox/cmd/sandbox@latest
```

Put the resulting `sandbox` binary somewhere on your `PATH` (e.g. `~/bin` or
`/usr/local/bin`). To typecheck the TypeScript runner:

```bash
cd runner && npm install --ignore-scripts && ./node_modules/.bin/tsc --noEmit
```

## Commands

| Command | Description |
|---|---|
| `sandbox` | Open the command-center dashboard (session list, attention routing, attach) |
| `sandbox claude [prompt]` | Start a **new** Claude Agent SDK session for the current project and open the TUI (`--model <id\|alias>` sets the session model; switch in-session with `/model`). To resume an existing one, use `sandbox attach` |
| `sandbox opencode` | Start a **new** OpenCode-backend session (external `opencode serve` + attach) |
| `sandbox attach <id>` | Reconnect to a running/suspended session and replay history |
| `sandbox trace <id>` | Replay a session's normalized event timeline (`--json`, `--since`, `--tool` filters) |
| `sandbox status` | List sessions and their status |
| `sandbox sync <id>` | Manage Mutagen file sync for a session |
| `sandbox suspend <id>` | Terminate the pod, keep the PVC |
| `sandbox resume <id>` | Resume a suspended session |
| `sandbox cancel <id>` | Interrupt the active turn |
| `sandbox rename <id> <name>` | Set a persistent display title for a session |
| `sandbox shell <id>` | Open a debug shell into the session pod |
| `sandbox destroy <id>` | Delete the session and its PVC (irreversible) |

## Testing

```bash
go test ./...
go vet ./...
```

## Contributing & support

Contributions are welcome — start with [`CONTRIBUTING.md`](CONTRIBUTING.md) for the
dev setup and the one codegen contract, and [`docs/architecture.md`](docs/architecture.md)
for the design rationale behind the two-component split. For security issues,
follow [`SECURITY.md`](SECURITY.md) instead of opening a public issue.

## License

[MIT](LICENSE)

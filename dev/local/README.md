# Local KIND dev environment

A disposable, local Kubernetes dev cluster that proves the full path:

```
sandbox CLI -> agent-sandbox controller -> Sandbox CRD -> runner pod -> HTTP+SSE turn loop
```

for the **opencode** backend, without touching any remote cluster.

## Cluster constants

| Thing | Value |
|---|---|
| KIND cluster name | `sandbox-local` |
| kube context | `kind-sandbox-local` |
| kubeconfig (gitignored) | `dev/local/.kubeconfig` |
| runner image | `sandbox-runner:dev` |
| reaper image | `sandbox-reaper:dev` |
| namespaces | `agent-sessions`, `agent-sandbox-system`, `agent-reaper` |
| controller | agent-sandbox v0.4.6 (vendored manifest) |

## Tooling (Flox)

The **entire** dev/CI toolchain is pinned in the Flox env
(`.flox/env/manifest.toml`) so nothing leaks from the laptop: `go, node, gcc, git;
ctlptl, kind, docker (CLI), tilt, kubectl, helm, crictl, mutagen; golangci-lint
2.12.2, jq`. They are ONLY on `PATH` inside `flox activate`; `.envrc` auto-activates
via direnv, and the just recipes self-activate, so a direct `just kind-up` works in
or out of the env.

Run **`just doctor`** to verify every required tool resolves from `$FLOX_ENV` (it
fails if e.g. `jq` resolves from Homebrew), the Docker daemon is reachable, and to
report Hall status.

The **Docker daemon is Colima** (arm64; the KIND node is arm64) — a host prereq,
not pinnable in Flox (only the `docker` CLI is). This host is Nix-managed: do not
install tools imperatively.

## Quick start

```bash
# From the repo root (recipes self-activate the flox env):
just dev              # doctor + ctlptl cluster + controller + images + launch the claude TUI
just dev opencode     # …same, opencode backend

# Or step by step:
just doctor           # verify the Flox toolchain (no host leakage) + daemon
just kind-up          # ctlptl applies the sandbox-local cluster + installs controller/manifests
just dev-image        # build sandbox-runner:dev (+ reaper); deliver via Hall or `kind load`
just kind-test        # run the two-layer integration tests (plumbing + full turn)

# …or drive the live-reload dev loop with Tilt from this directory:
cd dev/local && tilt up
```

## Image delivery: Hall (optional) or `kind load`

`just dev-image` delivers freshly-built images to the KIND node one of two ways:

- **Hall** (`https://hall.kvick.dev`) — a host daemon (built on Spegel) that mirrors
  your local Docker store to the cluster, so the node pulls `sandbox-runner:dev`
  directly with **no `kind load`**. When active, `dev-image` skips the load step.
- **`kind load`** (fallback) — used automatically when Hall isn't detected. Always
  works; just slower per build.

`dev-image` auto-detects Hall (`hall status`); force either path with
`SANDBOX_USE_HALL=1` / `SANDBOX_USE_HALL=0`.

**Host setup for Hall** (one-time; arm64/Colima is UNVERIFIED — verify on your box):

```bash
# 1. Install the hall binary (not in nixpkgs):  see https://hall.kvick.dev
# 2. Enable the NRI socket in Colima's Docker daemon, then restart Colima:
colima ssh -- sudo sh -c 'mkdir -p /etc/docker && \
  jq ". + {\"features\": {\"nri\": true}}" /etc/docker/daemon.json 2>/dev/null \
  > /tmp/d.json || echo "{\"features\":{\"nri\":true}}" > /tmp/d.json; \
  mv /tmp/d.json /etc/docker/daemon.json'
colima restart
# 3. Start the daemon (configures new/existing clusters automatically):
hall daemon
# 4. Confirm:  just doctor   # should report "hall active"
```

## (a) NetworkPolicy is NOT enforced here

kindnet (the default KIND CNI) does **not** enforce `NetworkPolicy`. The egress
boundary in `k8s/networkpolicy-default-deny.yaml` and
`k8s/networkpolicy-egress-allow.yaml` would therefore be a **silent no-op**, so
those manifests are **intentionally not applied** in the local dev env. Do not rely on
egress-deny here — a session pod in this local dev env can reach the API server, cluster
services, and the internet. Network isolation is validated only on a real
cluster with an enforcing CNI.

## (b) Provider keys

Provider credentials are optional and supplied via gitignored Secrets:

```bash
cp dev/local/secret-template.yaml dev/local/secret.local.yaml
# edit secret.local.yaml — fill in the keys you have (all keys optional)
KUBECONFIG=dev/local/.kubeconfig kubectl apply -f dev/local/secret.local.yaml
```

The two Secrets (both in `agent-sessions`) and their env mappings:

- `opencode-credentials`: `anthropic-api-key`→`ANTHROPIC_API_KEY`,
  `openai-api-key`→`OPENAI_API_KEY`, `opencode-api-key`→`OPENCODE_API_KEY`
- `anthropic-credentials`: `api-key`→`CLAUDE_CODE_OAUTH_TOKEN`

The session pod starts before these exist; missing keys just leave the env var
unset.

## (c) Plumbing-only vs full-turn

The integration tests come in two layers:

- **Plumbing layer** — needs no provider key. Proves CLI→controller→Sandbox
  CRD→runner pod→port-forward→`/healthz` and the SSE seam are wired correctly
  (the Sandbox reconciles, the pod goes Ready, the runner answers).
- **Full-turn layer** — requires a valid provider key in `secret.local.yaml`.
  Drives a real turn through `sandbox turn <id> --prompt …` and asserts an
  assistant `message.completed` reply and a `turn.completed` event.

Without keys the plumbing layer still passes; the full-turn layer is skipped (or
fails fast with a clear "no provider credentials" reason).

## (d) Its own kubeconfig + context guard

The local dev env always uses `dev/local/.kubeconfig` (gitignored) as its `KUBECONFIG`, and
recipes/tests guard on the context being exactly `kind-sandbox-local` before doing
anything destructive. The Tiltfile pins `allow_k8s_contexts('kind-sandbox-local')`.
This makes it impossible for the local dev env to act against a remote cluster, even if
your ambient `~/.kube/config` points elsewhere.

## Regenerating the vendored controller manifest

`dev/local/agent-sandbox/manifest.yaml` is the helm-rendered agent-sandbox v0.4.6
install (leader election disabled). It is vendored — do not hand-edit. See
`dev/local/agent-sandbox/VERSION` for the exact `helm template …` command to
regenerate it.

## Reset / teardown

```bash
just dev-reset        # wipe ROGUE SANDBOXES (+PVCs +reaper Jobs) but KEEP the cluster — fast
just dev-nuke         # full node reset: delete the sandbox-local cluster (ctlptl) + kubeconfig
just dev-recreate     # dev-nuke + dev-up (cluster + controller + images from scratch)
just kind-down        # alias of dev-nuke
```

ctlptl owns the cluster lifecycle (`dev/local/ctlptl.yaml`): `kind-up` runs
`ctlptl apply` (idempotent — creates if absent, reuses if present) and `dev-nuke`
runs `ctlptl delete`.

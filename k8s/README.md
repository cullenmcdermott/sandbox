# Example Kubernetes manifests

> **These are example manifests — a starting point, not an authoritative or
> turnkey deployment.** The maintainer's real cluster setup lives in a separate,
> private GitOps repository and is not published here. Read every file, adapt it
> to your cluster (CNI, storage, network ranges, namespaces, PodSecurity policy),
> and validate it before trusting it.

These manifests cover the **network security boundary** that `sandbox` depends
on, plus the namespace/RBAC/NetworkPolicy pieces the idle reaper needs. They do
**not** install the controller or storage — see
[What's not here](#whats-not-here).

## Prerequisites

- A Kubernetes cluster with the
  [agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox) controller
  (`v1alpha1`, e.g. v0.4.6) installed.
- A CNI that enforces `NetworkPolicy` (e.g. Cilium, Calico). **If your CNI does not
  enforce NetworkPolicy, the security boundary described below does not exist.**
- A `StorageClass` for the per-session PVCs.

## Container images

The CLI's **default** runner and reaper images are **public GHCR packages** built
by Depot CI:

- `ghcr.io/cullenmcdermott/sandbox-claude-runner:latest`
- `ghcr.io/cullenmcdermott/sandbox-reaper:latest`

They pull without extra setup as long as your cluster can reach `ghcr.io`. Build
and push your own only for a private fork or an air-gapped cluster, then pass them
explicitly:

```bash
# Build & push the runner image (see runner/Dockerfile)
docker build -t <your-registry>/sandbox-claude-runner:latest runner/
docker push   <your-registry>/sandbox-claude-runner:latest

# Build & push the reaper image (the `sandbox reap` subcommand; see Dockerfile.reaper)
docker build -f Dockerfile.reaper -t <your-registry>/sandbox-reaper:latest .
docker push   <your-registry>/sandbox-reaper:latest

# Point the CLI at your images on every session-creating command
# (`sandbox claude` is interactive-only — it takes no positional prompt)
sandbox claude \
  --runner-image <your-registry>/sandbox-claude-runner:latest \
  --reaper-image <your-registry>/sandbox-reaper:latest
```

## Apply order

```bash
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/reaper-namespace.yaml
kubectl apply -f k8s/reaper-rbac.yaml
kubectl apply -f k8s/networkpolicy-default-deny.yaml
kubectl apply -f k8s/networkpolicy-egress-allow.yaml
kubectl apply -f k8s/networkpolicy-reaper-ingress.yaml
```

`networkpolicy-default-deny.yaml` denies all ingress and egress for the namespace;
`networkpolicy-egress-allow.yaml` then adds back the minimum egress (DNS + HTTPS to
the model API and package registries), and `networkpolicy-reaper-ingress.yaml` adds
back the one ingress path the idle reaper needs (reaper pods → session pods on
`:8787`). Apply all three — the deny policy alone leaves sessions with no network
at all.

The reaper pieces (`reaper-namespace.yaml`, `reaper-rbac.yaml`,
`networkpolicy-reaper-ingress.yaml`) are what let the per-session reaper Jobs the
CLI creates actually run. **Without them, sessions never auto-suspend**: the CLI
still creates sessions (it surfaces an "idle reaper not started" warning), but
idle pods keep running — and consuming resources — until you `sandbox suspend`
or `sandbox destroy` them yourself.

### Tighter egress: FQDN allowlist (Cilium)

[`networkpolicy-egress-fqdn.yaml.example`](networkpolicy-egress-fqdn.yaml.example)
is a tightened **replacement** for `networkpolicy-egress-allow.yaml`: a Cilium
`CiliumNetworkPolicy` using `toFQDNs` to pin egress to the documented Claude Code
endpoints (plus commented per-backend and registry blocks) instead of all public
443 — closing the credential-exfiltration channel described in
[`../SECURITY.md`](../SECURITY.md). It requires an FQDN-aware CNI (e.g. Cilium);
keep `networkpolicy-default-deny.yaml` applied, and do not apply both egress
examples at once. Re-verify the host set against a live session
(`hubble observe`) before enforcing — see the file's header comments.

## Facts these manifests match

These values are taken from the CLI/runner source (`internal/k8s/backend.go`); keep
them in sync if you change defaults:

| Thing | Value |
|---|---|
| Session namespace | `agent-sessions` |
| Session-id label | `sandbox.cullen.dev/session-id: <session-id>` |
| App label | `app.kubernetes.io/name: sandbox-<backend>` (e.g. `sandbox-claude-pane`) |
| Runner HTTP API port | `8787` |
| `sshd` port (Mutagen sync) | `22` |
| OpenCode port | `4096` |

The runner pod sets `automountServiceAccountToken: false`, a `RuntimeDefault`
seccomp profile, `allowPrivilegeEscalation: false`, and a dropped-capability set —
but it still **runs as root** and exposes `sshd`. The NetworkPolicy is what keeps a
session from reaching the API server, in-cluster services, the cloud metadata
endpoint, and your internal network. See [`../SECURITY.md`](../SECURITY.md).

## What's not here

This example intentionally omits parts of a full deployment, because they depend on
your cluster or live in the maintainer's private repo:

- **agent-sandbox controller install** — see the upstream project.
- **StorageClass / PVC tuning.** The CLI defaults to a 50Gi PVC; the StorageClass is
  overridable per session. Provide one suited to your cluster (RWO is sufficient).
- **The shared `opencode-credentials` Secret** is a **fallback** for
  `sandbox opencode` sessions — used only when the CLI host has no local opencode
  login (e.g. CI/headless). The primary path harvests the host's own `opencode
  auth login` and seeds it into the session's own per-session Secret, so **no**
  shared Secret is needed; see the credentials section of the main
  [`README.md`](../README.md). Provision this fallback Secret out-of-band only for
  machines with no local opencode login (see
  [`../docs/architecture.md`](../docs/architecture.md)). Likewise the `claude`
  backend needs **no** shared Secret: `sandbox claude` copies your local Claude
  Code login into a per-session Secret at create time. The shared
  `anthropic-credentials` Secret is legacy — its only remaining consumer is the
  retired `claude-sdk` backend branch (`buildEnv` in `internal/k8s/backend.go`),
  so new deployments can skip it.

(The idle reaper's cluster pieces — the `agent-reaper` namespace, the
cross-namespace `Role`/`RoleBinding`, and the `:8787` ingress exception — used to
be on this list; they are now shipped as [`reaper-namespace.yaml`](reaper-namespace.yaml),
[`reaper-rbac.yaml`](reaper-rbac.yaml), and
[`networkpolicy-reaper-ingress.yaml`](networkpolicy-reaper-ingress.yaml). See
[`../docs/session-lifecycle.md`](../docs/session-lifecycle.md) for how the reaper
works.)

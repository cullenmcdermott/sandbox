# Example Kubernetes manifests

> **These are example manifests — a starting point, not an authoritative or
> turnkey deployment.** The maintainer's real cluster setup lives in a separate,
> private GitOps repository and is not published here. Read every file, adapt it
> to your cluster (CNI, storage, network ranges, namespaces, PodSecurity policy),
> and validate it before trusting it.

These manifests cover the **network security boundary** that `sandbox` depends on.
They do **not** install the controller, RBAC, storage, or the reaper — see
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
sandbox claude "..." \
  --runner-image <your-registry>/sandbox-claude-runner:latest \
  --reaper-image <your-registry>/sandbox-reaper:latest
```

## Apply order

```bash
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/networkpolicy-default-deny.yaml
kubectl apply -f k8s/networkpolicy-egress-allow.yaml
```

`networkpolicy-default-deny.yaml` denies all ingress and egress for the namespace;
`networkpolicy-egress-allow.yaml` then adds back the minimum egress (DNS + HTTPS to
the model API and package registries). Apply both — the deny policy alone leaves
sessions with no network at all.

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
| App label | `app.kubernetes.io/name: sandbox-<backend>` (e.g. `sandbox-claude-sdk`) |
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
- **The `agent-reaper` namespace + RBAC.** The idle reaper runs in a *separate*
  namespace (`agent-reaper`) because the session namespace's egress policy blocks
  the Kubernetes API; the reaper needs API egress plus a `Role` in `agent-sessions`
  granting `sandboxes: get,update` (suspend is `sandboxes.Update`, not patch),
  `pods: list` (to resolve the runner pod IP), and `secrets: get` (the runner
  bearer token). An example `Role` + `RoleBinding` is in
  [`reaper-rbac.yaml`](reaper-rbac.yaml); see also
  [`../docs/session-lifecycle.md`](../docs/session-lifecycle.md).
- **A NetworkPolicy ingress exception** so the reaper (in `agent-reaper`) can reach
  the session pod on `:8787` cross-namespace.
- **StorageClass / PVC tuning.** The CLI defaults to a 50Gi PVC; the StorageClass is
  overridable per session. Provide one suited to your cluster (RWO is sufficient).
- **Shared credential Secrets** (`anthropic-credentials`, `opencode-credentials`)
  for the model API token — provision these out-of-band; see
  [`../docs/architecture.md`](../docs/architecture.md).

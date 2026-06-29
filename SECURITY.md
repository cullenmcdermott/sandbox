# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities **privately** to the maintainer via
[GitHub Security Advisories](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
(the "Report a vulnerability" button under the repository's **Security** tab).
Do not open a public issue for a suspected vulnerability.

Please include enough detail to reproduce: affected component (CLI, runner, or
deployment manifests), version/commit, and a proof of concept if you have one.

## Threat model

`sandbox` runs an AI coding agent (the Claude Agent SDK) inside a Kubernetes pod
with broad write access to a project workspace. **The agent is treated as
untrusted code** — the design assumes it may attempt to read, write, or exfiltrate
anything it can reach over the network or filesystem inside its pod.

The **security boundary is the network**, not the process. The runner pod runs as
**root** and exposes an `sshd` (port 22, for Mutagen file sync) alongside the
HTTP+SSE runner API (port 8787). Confining what a session can reach is therefore
the deployer's responsibility:

> **Deployment REQUIRES a Kubernetes `NetworkPolicy` that default-denies ingress
> and egress for the session namespace, plus an explicit egress allowlist.**
> Without it, a session pod can reach the Kubernetes API server, in-cluster
> services, the cloud metadata endpoint (`169.254.169.254`), and your internal
> network. Example manifests are in [`k8s/`](k8s/) — they are a starting point you
> must review and adapt, not a turnkey configuration.

### Controls in place

- **No cluster credentials in the pod.** Session pods set
  `automountServiceAccountToken: false`, so a compromised agent has no in-cluster
  identity.
- **Per-session bearer token.** The CLI mints a 256-bit token at create time,
  stores it in a per-session Kubernetes Secret, and injects it into the pod as
  `RUNNER_TOKEN`. The runner rejects every non-`/healthz` request that does not
  present it (constant-time comparison).
- **Per-session SSH key.** File sync authenticates with a per-session ed25519
  keypair: the public key is installed as the pod's `authorized_keys`, the private
  key never leaves the local machine. Root login is key-only.
- **Local-only transport.** Both the runner API and `sshd` are reached over an
  ephemeral `kubectl` port-forward to `127.0.0.1`, not a cluster-exposed Service.
- **Pod hardening.** `seccompProfile: RuntimeDefault`,
  `allowPrivilegeEscalation: false`, and a dropped-capability set (no `NET_RAW`,
  no `MKNOD`). The namespace is intended to run under PodSecurity Admission
  (`baseline` enforce, `restricted` warn).
- **Tool guardrails (defense-in-depth, not a boundary).** A runner `PreToolUse`
  hook blocks Bash patterns that reach for host/cluster/credential surfaces, and a
  `PostToolUse` hook writes an append-only audit log.

### Known trade-offs

- The **Mutagen transport intentionally skips SSH host-key verification**
  (`StrictHostKeyChecking no`). The remote host is always a fresh, local
  port-forward to `127.0.0.1:<ephemeral-port>`; the per-session key — not the host
  key — is the authentication boundary.

## Known limitations / hardening backlog

These are known gaps for a v1 launch. They are tracked and welcome as
contributions; none should be assumed to be mitigated:

- **No `/metrics` endpoint and no structured logging** in the runner.
- **Runner image is tagged `:latest`**, not digest-pinned.
- **No SBOM, image scan, or build provenance** for published images.
- **`sshd` is not supervised** (not run under a process supervisor; a crash is not
  automatically restarted).
- **Runner runs as root.** Moving to `runAsNonRoot` + `fsGroup` + further
  capability drops is tracked — it requires live validation because `sshd`
  privilege separation depends on the current layout.
- **No permission-token rotation.** The per-session `RUNNER_TOKEN` is fixed for
  the session lifetime.
- **Permission-id entropy** of the in-flight permission-prompt identifiers has not
  been hardened/reviewed.
- **`StrictHostKeyChecking=no` on the Mutagen transport** (see trade-off above).

## Supported versions

This project is pre-1.0 and ships from `main`. Security fixes land on `main`;
there is no separate long-term support branch yet.

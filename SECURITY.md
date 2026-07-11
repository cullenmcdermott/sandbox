# Security Policy

Aimed at operators deploying `sandbox` to their own cluster. It documents the
posture **as built today**, including the raised-but-not-closed gaps — it is not
a claim of completeness. Provenance for the verified findings is
[`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §A; the open hardening
items are tracked in [`TODO.md`](TODO.md) §1f.

## Reporting a vulnerability

Please report security vulnerabilities **privately** via
[GitHub Security Advisories](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
(the "Report a vulnerability" button under the repository's **Security** tab).
Do not open a public issue for a suspected vulnerability. The project is pre-1.0
and has no dedicated security contact yet; the advisory flow reaches the
maintainer.

Please include enough detail to reproduce: affected component (CLI, runner, or
deployment manifests), version/commit, and a proof of concept if you have one.

## Threat model

`sandbox` runs an AI coding agent (the Claude Agent SDK, or an external
`opencode`/Codex backend) inside a Kubernetes pod with broad write access to a
project workspace. **The agent is treated as untrusted code** — the design
assumes a prompt-injected or misbehaving agent may attempt to read, write, or
exfiltrate anything it can reach over the network or filesystem inside its pod.

The **security boundary is the network, not the process.** Runner, sshd, and the
agent's own shell all run as **root (uid 0)** in the same pod (no `runAsUser` in
the pod spec — `internal/k8s/backend.go`; no `USER` in `runner/Dockerfile`), so
in-pod isolation between the control plane and the agent it supervises is weak.
Confining what a session can reach is therefore the deployer's responsibility:

> **Deployment REQUIRES a Kubernetes `NetworkPolicy` that default-denies ingress
> and egress for the session namespace, plus an explicit egress allowlist.**
> Without an enforcing CNI (Cilium, Calico, …) the policy is silently ignored and
> there is no boundary. Without it, a session pod can reach the Kubernetes API
> server, in-cluster services, the cloud metadata endpoint (`169.254.169.254`),
> and your internal network. Example manifests are in [`k8s/`](k8s/) — a starting
> point you must review and adapt, not a turnkey configuration.

### In-pod network binds — all listen on `0.0.0.0`

Every in-pod listener binds all interfaces, not loopback:

| Port | Service | Evidence |
|---|---|---|
| 8787 | runner HTTP + SSE API | `runner/src/server.ts:170` (`server.listen(PORT)`, no host arg → all interfaces) |
| 22 | `sshd` (Mutagen sync transport) | `runner/Dockerfile:86` (`EXPOSE 8787 22`); key-only auth |
| 4096 (`OPENCODE_PORT`) | `opencode serve` (opencode backend only) | `runner/src/opencode.ts:313` (`--hostname 0.0.0.0`), default `4096` at `opencode.ts:22` |

**What contains this:** the default-deny **ingress** NetworkPolicy
(`k8s/networkpolicy-default-deny.yaml`) means nothing off-pod can dial these
ports, and the runner API additionally requires a bearer token
(`runner/src/auth.ts`). The CLI reaches all three over an ephemeral `kubectl`
port-forward to `127.0.0.1`, never a cluster-exposed Service.

**What it does NOT contain:** processes *inside the pod*. The agent's own Bash
tool runs in the same network namespace and can dial `127.0.0.1:8787`,
`:22`, and `:4096` directly — the bind address is not a boundary against the
agent. This is the mechanism behind the A1 self-approval class of finding
(below): the ingress policy and bearer token stop off-pod callers, not the
in-pod agent that already holds (or can recover) the token.

### Egress: the example allows 443 to any public host

`k8s/networkpolicy-egress-allow.yaml:48-59` permits **TCP 443 to `0.0.0.0/0`**
with only RFC1918 / CGNAT / link-local (incl. the metadata endpoint) carved out.
Stated plainly: **this is an open exfiltration channel.** It is what turns an
in-pod secret disclosure (A1) or a logged-secret (A2, now redacted) into remote
exfiltration — a compromised agent can POST anything it reads to any public
HTTPS host. The example is deliberately broad so the agent can reach
`api.anthropic.com` and public package registries out of the box.

**Hardening path:** replace the broad `ipBlock` with the specific CIDRs — or,
better, an FQDN-scoped egress policy — for only the provider/registry hosts your
agents actually need. If your CNI is Cilium, a `CiliumNetworkPolicy` with
`toFQDNs` (DNS-aware egress) pins egress to named hosts (e.g.
`api.anthropic.com`, `registry.npmjs.org`) rather than all of public 443. This
is the single highest-leverage hardening step for a real deployment.

### Controls in place

Each verified against code at the cited location:

- **Bearer-token auth on every non-`/healthz` route**, including SSE. The CLI
  mints a **256-bit** token (`generateToken`, `backend.go`), stores it in a
  per-session Kubernetes Secret, and injects it as `RUNNER_TOKEN`. The runner
  compares it **constant-time** and **rejects all requests when no token is
  configured** (`runner/src/auth.ts` `bearerTokenOk` / `constantTimeEqual`;
  wired at `runner/src/server.ts:27`).
- **Per-session Secrets + reconcile.** Bearer token, opencode server password,
  and SSH keys live in a per-session Secret, not a shared one; create/resume
  paths hash the live cluster Secret and warn on drift (`backend.go`).
- **Event-log + SSE secret redaction (A2 fix, landed 2026-07-09).** A shared
  `runner/src/redact.ts` masks secret-named fields, known runner secret values,
  `sk-…` tokens, and `Authorization: Bearer …` headers. `appendEvent`
  (`runner/src/events.ts:303`) redacts `turn.started`, `tool.*`, `permission.*`,
  and role-user `message.*` payloads **before both SQLite persist and SSE
  broadcast** — so secrets echoed in commands do not land cleartext on the PVC or
  replay to `attach` clients. The same `redactSecrets` gates `audit.jsonl`
  (`runner/src/audit.ts:34`).
- **PreToolUse Bash blocking.** A runner `PreToolUse` hook
  (`runner/src/claude.ts:282`, `makePreToolUseBashHook`) runs a shared blocklist
  (`runner/src/guards.ts` `bashCommandBlocked`) that denies Bash commands
  reaching for host/cluster/credential surfaces. **Defense-in-depth, not a
  boundary** — a regex blocklist is bypassable; treat it as noise reduction.
- **Append-only audit log.** A `PostToolUse` hook writes `audit.jsonl`
  (`runner/src/audit.ts`), redacted.
- **Runner-infra env strip for spawned children.** `sanitizedExecEnv`
  (`runner/src/exec.ts:36`) drops `RUNNER_TOKEN` and the other runner-infra
  secrets from the env of `/exec`, the SDK `claude` child, workspace git calls,
  and the `opencode serve` child (`runner/src/claude.ts:93`,
  `runner/src/opencode.ts:47`). See the A1 residual below for what this does and
  does not guarantee.
- **Default-deny ingress + egress allowlist** (`k8s/networkpolicy-default-deny.yaml`
  + `networkpolicy-egress-allow.yaml`) — the network boundary, subject to the
  enforcing-CNI caveat above.
- **No cluster credentials in the pod.** `automountServiceAccountToken: false`
  (`backend.go:1283`) — a compromised agent has no in-cluster identity.
- **Pod hardening.** `seccompProfile: RuntimeDefault` (`backend.go:1293`),
  `allowPrivilegeEscalation: false` (`backend.go:1362`), and capabilities
  **drop `ALL`** then re-add a specific set — `CHOWN, DAC_OVERRIDE, FOWNER,
  FSETID, SETGID, SETUID, SETPCAP, SETFCAP, NET_BIND_SERVICE, SYS_CHROOT, KILL,
  AUDIT_WRITE` (`backend.go:1363`). Note this is not a pure drop-all: `SETUID`
  and `DAC_OVERRIDE` remain, which is why the uid-separation fix (A1 residual)
  needs live validation.
- **Scoped reaper RBAC.** The idle reaper runs as a distinct ServiceAccount
  (`sandbox-reaper`) in a **separate** namespace (`agent-reaper`), bound to a
  narrow `Role` in `agent-sessions` — get/patch sandboxes, get pods/secrets — not
  cluster-admin (`internal/k8s/reaper.go:20`). It lives outside the session
  namespace precisely because that namespace's egress policy blocks the k8s API.
- **Per-session SSH key.** File sync authenticates with a per-session ed25519
  keypair (public key installed as the pod's `authorized_keys`; private key never
  leaves the local machine). `sshd` is password-auth-off, pubkey-only, root login
  key-only (`runner/Dockerfile:54`).

### A1 residual — `RUNNER_TOKEN` is recoverable in-pod despite the env strip

The A1 env strip (above) removes `RUNNER_TOKEN` from every **child** env, so an
agent that runs `env`/`printenv` in its own Bash tool no longer sees the token
directly. **It does not close the hole.** Because the runner (PID 1) and the
agent child share **uid 0** in the same pod, the agent can read the runner's
environment out of `/proc`:

```
tr '\0' '\n' < /proc/1/environ   # recovers RUNNER_TOKEN from the runner process
```

and then reach the runner API on in-pod localhost (`127.0.0.1:8787`) to
**self-approve its own permission prompts**, start/interrupt turns, or read the
event log — defeating the approval flow for an agent already running in
default/acceptEdits mode.

**So the env strip guarantees:** the token is not handed to children directly,
raising the bar past a trivial `printenv`. **It does not guarantee:** the token
is unrecoverable — a same-uid process reads it from `/proc`. This is
**raised-bar, not closed.** The tracked fix is uid separation — run the agent
child as a non-root uid distinct from the runner, or mount `/proc` with
`hidepid=2` (pod-spec + Dockerfile work, coordinated with the base-image spike).
See [`TODO.md`](TODO.md) §1f (`[A1 residual]`); the comments in
`runner/src/claude.ts` say so at the call site.

### Known trade-offs

- The **Mutagen transport intentionally skips SSH host-key verification**
  (`StrictHostKeyChecking no`). The remote host is always a fresh, local
  port-forward to `127.0.0.1:<ephemeral-port>`; the per-session key — not the host
  key — is the authentication boundary.

## Known limitations / hardening backlog

Known gaps; none should be assumed mitigated. Cross-referenced to
[`TODO.md`](TODO.md) §1f where tracked.

- **Runner + agent share uid 0** — the A1 residual above; the top open item.
- **Open 443-to-any example egress** — the exfil channel above; FQDN-scoped
  egress is the fix.
- **Runner runs as root.** Moving to a non-root uid requires live validation
  because `sshd` privilege separation depends on the current layout.
- **`PreToolUse` block uses the legacy SDK `decision:'block'` shape.** If a
  future SDK bump drops it, Bash enforcement silently dies while tests stay green
  (they pin what we return, not what the SDK honors). Tracked in §1f.
- **Runner image is tagged `:latest`**, not digest-pinned; no SBOM, image scan,
  or build provenance for published images.
- **No `/metrics` endpoint and no structured logging** in the runner.
- **No `RUNNER_TOKEN` rotation.** The per-session token is fixed for the session
  lifetime.
- **`sshd` is not run under a process supervisor** — a crash is not
  auto-restarted.
- **Permission-prompt ids carry only 32 bits of entropy** (`shortId` =
  the first UUID segment, `runner/src/events.ts:661`) and have not been
  hardened/reviewed. Resolving a permission still requires the bearer token,
  so id guessability is a second factor, not a standalone hole.

## Supported versions

This project is pre-1.0 and ships from `main`. Security fixes land on `main`;
there is no separate long-term support branch yet.

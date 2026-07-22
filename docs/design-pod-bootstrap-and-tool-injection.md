# Pod bootstrap files, generic env/secret injection, and operator tool binaries

Status: **parts A + B implemented** (part B `d6e55fa`, 2026-07-21; part A
2026-07-22 — `CreateOptions.BootstrapFiles`, per-session-Secret transport,
runner boot materialize before any agent starts). The docs closeout is done bar
the operator-prose README section for env/file injection (TODO §8); part C stays
documentation. One decision was resolved during
implementation and **needs maintainer eyes** (see the callout in part B
below): `ExtraSecretEnv` was made **agent-visible** rather than
stripped-from-agent, because the maintainer's stated use case (inject a
GitLab/GitHub PAT / Jira key for the agent's own `git`/`gh` to use) requires
it, and rider (b)'s "each injected secret widens the *agent's* exfil blast
radius" framing only holds if the agent can read it. If you disagree, the
strip-by-default alternative is a one-line change to the runner allowlist +
`sanitizedExecEnv`.

## Problem

Sandbox sessions currently receive exactly the configuration this repo knows
about: agent credentials, sync endpoints, and backend selection. An external
orchestrator that wants to extend the *agent environment inside the pod* — for
example an approval-gated egress system whose CLI, agent skill, and CLAUDE.md
guidance must exist in the pod's `$HOME`, plus a control-endpoint address and
token the CLI needs — has no supported hook. Today it would have to fork the
runner image and hand-patch pod specs. Three gaps:

1. **No way to place operator-supplied files into the pod's `$HOME` at
   create-time.** The synced workspace is the wrong place: it is a git worktree
   of the user's repo, and writing tool config there pollutes it (and syncs it
   back to the laptop).
2. **No generic env/secret escape hatch on the Create surface.** Every
   credential so far (Anthropic, opencode providers, codex) got a bespoke
   `Spec` field. That is right for credentials this repo owns end-to-end, but
   an operator passing "my tool's endpoint + token" should not need a Spec
   field per tool.
3. **No way to get an operator's binary into the pod** short of forking the
   runner image build.

## Proposal

Three mechanisms, smallest-first. A and B are SDK surface (new `CreateOptions`
/ `Spec` fields, per-session-Secret transport, runner materialization); C is a
documented pattern plus an optional later SDK feature.

### A. Bootstrap files (`CreateOptions.BootstrapFiles`)

```go
// BootstrapFile is an operator-supplied file materialized in the pod before
// the agent backend starts. Content rides the per-session Secret (never
// logged, never serialized into the local index) and persists on the PVC.
type BootstrapFile struct {
    // Path is where the file lands in the pod. Absolute, or "~/"-relative to
    // the pod HOME. Must resolve OUTSIDE the synced workspace (the workspace
    // is the user's repo — bootstrap files must never sync back) and inside
    // $HOME or /session/state. Validated at Create, fail-closed.
    Path string
    // Content is the file body. Create-time-only material (json:"-" on Spec).
    Content []byte
    // Mode is the unix file mode; 0 means 0644 (use 0600 for secrets).
    Mode uint32
}
```

- Transport: one per-session-Secret key per file (`bootstrap-<n>` plus a small
  JSON manifest key carrying path/mode metadata). Kubernetes Secrets cap at
  ~1 MiB total — enforce a summed-size limit at Create with a clear error.
- Materialization: the runner writes the files at boot *before* starting any
  agent backend, exactly where codex's `auth.json` materialization already
  hooks (a shared "materialize" step). Write-if-changed so a pod restart
  doesn't clobber a file the agent legitimately edited, unless the seed
  changed (operator rotated it) — same precedence rule as the codex seed.
- Covers: `CLAUDE.md` guidance, `~/.claude/skills/<tool>/SKILL.md`, tool
  config files. Same lifecycle as credentials: injected at Create, persists
  across suspend/resume, reconciled on re-create.

### B. Generic env injection (`CreateOptions.ExtraEnv` / `ExtraSecretEnv`)

```go
// ExtraEnv adds plain environment variables to the runner pod.
ExtraEnv map[string]string
// ExtraSecretEnv adds environment variables whose values ride the
// per-session Secret and reach the pod only as SecretKeyRefs. Values are
// create-time-only material (json:"-").
ExtraSecretEnv map[string][]byte
```

- Validation, fail-closed at Create: reject names colliding with the reserved
  runner/backend namespace (`SANDBOX_*`, `RUNNER_TOKEN`, `PROJECT_PATH`, the
  credential vars — one exported denylist, kept beside `buildEnv`).
- **Runner side — AS IMPLEMENTED (d6e55fa), a change from this draft's
  original position; NEEDS MAINTAINER SIGN-OFF.** The draft said
  `ExtraSecretEnv` is *stripped* from agent children. It is instead
  **agent-visible**: it is NOT added to `sanitizedExecEnv`'s strip set, so
  opencode/codex children see it via passthrough, and the claude-pane strict
  allowlist explicitly admits it (both via the `SANDBOX_EXTRA_ENV_NAMES` /
  `SANDBOX_EXTRA_SECRET_ENV_NAMES` marker vars `buildEnv` emits). Rationale:
  the maintainer's use case is injecting a GitLab/GitHub PAT / Jira key for
  the agent's *own* `git`/`gh`/`glab`, which requires the agent to read it;
  and rider (b)'s exfil-blast-radius framing only makes sense if the agent
  can read the secret. The runner's *own* infra secrets (`RUNNER_TOKEN`,
  provider keys) remain stripped — only operator-declared, marker-named vars
  cross the boundary. Compensating controls: values are masked from the event
  log/audit (`runner/src/redact.ts` reads the secret-names marker), and the
  exfil widening is documented in `SECURITY.md`. If strip-by-default is
  preferred, revert to withholding the marker-named vars in
  `buildClaudePaneEnv` + adding them to `RUNNER_SECRET_ENV_KEYS`.
- Covers: a tool's control-endpoint URL + bearer token without a bespoke Spec
  field per tool; and injected PAT/API keys the agent's tooling consumes.

### C. Operator tool binaries

Recommendation: **do not bake operator-specific binaries into the shared
runner image.** The runner image is this repo's published artifact; an
operator's egress CLI is not this repo's dependency, and coupling its release
cadence to runner releases is the wrong ownership direction. Two supported
paths instead:

1. **Derived runner image (available today, document it).** The CLI already
   takes `--runner-image`; a two-line Dockerfile (`FROM <runner-image>` +
   `COPY tool /usr/local/bin/`) gives the operator a versioned bundle of
   runner+tool that they control. This is the recommended v1: zero SDK work,
   already supported end-to-end, and the operator's CI owns the pairing.
2. **Tool-image init containers (later SDK feature, if demanded).**
   `CreateOptions.ToolImages []ToolImage{Image, Bin string}`: each becomes an
   initContainer that copies its binary into a shared volume mounted on the
   runner container's PATH. Decouples tool versioning from the runner image
   at the cost of pod-spec surface and pull latency. Only build this if the
   derived-image pattern proves painful in practice.

### Out of scope (cluster-side, for the requester)

- The tool's control endpoint must be reachable from the pod network: the
  `agent-sessions` default-deny egress NetworkPolicy needs an allowlist entry
  for it, and the endpoint itself must not bind loopback-on-the-node only.
  That wiring lives in the operator's cluster config, not this repo.

## Sequencing

1. B (`ExtraEnv`/`ExtraSecretEnv`) — smallest, unblocks endpoint+token.
2. A (`BootstrapFiles`) — reuses B's Secret plumbing plus the codex
   materialize hook.
3. C stays documentation unless the derived-image pattern fails in practice.

All three are additive SDK surface → new `sdktest` pins in the same change.

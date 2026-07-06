# ADR: Runner package-manager strategy (Debian base + Flox layer)

- **Status:** Proposed (Opus draft, 2026-07-05). **Docs only — implementation is
  gated on maintainer sign-off of this ADR.** Nothing in this document has been
  built; the code references below are the *seams* an implementation would touch,
  not changes already made.
- **Scope:** TODO.md §7b (Flox/Nix-first runner environment). This ADR decides
  the runner image's package-manager strategy and the runtime env/mount seam; it
  does **not** decide the cache-publish gate (a follow-on design) and does **not**
  itself add `flox`/`nix` to the image — it commits to the shape that a follow-up
  PR implements.
- **Fable review (2026-07-04):** proceed — the ADR is Opus-draftable now;
  implementation waits on maintainer sign-off. Recommended direction: keep the
  Debian `node:24-slim` base and **layer Flox** (which vendors Nix) with the base
  tool closure baked in; do **not** flip to a fully Nix-built OCI in the first
  pass. This document commits to that direction.

## Context

The repo already runs on Flox everywhere *except the runner pod*. The dev/CI
toolchain is pinned in `.flox/env/manifest.toml:14-36` (go, nodejs, gcc, git,
kubectl, mutagen, golangci-lint, just, …), `.envrc` auto-activates it, and CI
runs `flox activate -- just check` (`.depot/workflows/ci.yml`). `just doctor`
(`justfile:253-279`) even *fails* if a required dev tool resolves outside
`$FLOX_ENV`, so the host laptop can't leak a drifting version into a build.

The runner pod is the exception. Its image (`runner/Dockerfile`) is a Debian
`node:24-slim` multi-stage build:

- **build stage** (`runner/Dockerfile:8-27`): installs `python3 make g++` via
  apt so `npm ci` can compile `better-sqlite3` from source (no Node 24 prebuilt),
  then `npm prune --omit=dev` keeps the compiled native addon without shipping a
  toolchain.
- **runtime stage** (`runner/Dockerfile:30-88`): apt-installs `openssh-server`,
  `sqlite3`, `git`, `ca-certificates`, `wget`, `netcat-openbsd`, `dnsutils`;
  patches `sshd_config` to pubkey-only; `npm install -g opencode-ai@1.17.7`
  (`runner/Dockerfile:65-66`); copies the pruned `node_modules` + `dist`; and
  sets `entrypoint.sh` as `CMD`.

The entrypoint (`runner/entrypoint.sh:12-38`) persists SSH host keys on the PVC
(`/session/state/sandbox/ssh`), installs the per-session `authorized_keys` from
the projected Secret, starts `sshd`, then `exec node dist/index.js`. The Nix
flake today packages **only the Go host CLI** (`flake.nix:20-33`,
`nix/package.nix:14-45`, `subPackages = ["cmd/sandbox"]`) — it does not build the
runner or a runner image.

The consequence: an agent running inside a session pod has `apt`, `npm`, `git`,
and whatever the image baked in — but **no `flox` and no `nix`**. It cannot
reproducibly fetch a one-off tool, cannot enter a project's committed Flox env,
and cannot benefit from the substituter/cache story the rest of the repo relies
on. The maintainer wants the pod agent to have the same "just works" reproducible
package surface the laptop has, backed by a cluster cache, without letting agents
poison that cache.

This ADR is the parent decision. The four dependent §7b items (env/mount seam,
child-process propagation, CI triggers, cache strategy) are designed here and
implemented in follow-up PRs once the direction is signed off.

## Decision

**Keep the Debian `node:24-slim` base and layer Flox into the runtime image, with
the base tool closure baked in at build time.** Concretely:

1. The runtime stage installs `flox` (which vendors Nix) and bakes a base Flox
   environment / Nix closure so the tools the runner and agents rely on
   (`git`, `sqlite3`, diagnostics, and — where licensing/packaging allows —
   `opencode`) resolve from the Flox layer instead of ad-hoc apt/npm-global
   installs. sshd, Node 24, and the compiled `better-sqlite3` addon stay exactly
   as they are today.
2. A runtime bootstrap env/mount seam (see Design §1) tells the pod which package
   manager is preferred, where its caches live, and which substituters/binary
   caches to trust — so `flox`/`nix` inside the pod "just works" without baking
   cluster-specific config into the image.
3. The runner propagates the Flox/Nix preference and cache/config env to agent
   child processes (Design §2) and guides agents to prefer an existing project
   Flox env, else `flox activate`/`flox run`, else `nix run nixpkgs#…` for
   one-off tools — rather than mutating the image with apt/npm-global.

We do **not** rebuild the runner as a fully Nix-built OCI image in this pass (see
Rejected options), and we keep per-session PVCs **out** of the `/nix`-store
business (Design §3).

### Considered options

1. **Debian `node:24-slim` base + layered Flox/Nix (chosen).** Preserves every
   load-bearing property of the current image that has already been debugged into
   place: the pubkey-only `sshd` config and PVC-persisted host-key path
   (`runner/entrypoint.sh:12-21`, BR4), the from-source `better-sqlite3` native
   compile with the toolchain pruned out of the runtime
   (`runner/Dockerfile:11-27,68-72`), and the tested entrypoint→`exec node`
   handoff (`runner/entrypoint.sh:34-38`). Flox is *additive* — a new layer plus
   an env seam — so the blast radius is small and reversible. Cost: image grows
   by the Flox/Nix runtime + base closure; two package worlds (apt-provided base
   OS + Flox-provided tools) coexist, which must be documented so nobody
   apt-installs a tool Flox already owns.

2. **Fully Nix-built OCI image (`dockerTools.buildLayeredImage` / the flake builds
   the runner).** Rejected *for the first pass*. It is the most reproducible end
   state, but it forces re-solving three things that already work under Debian, at
   once: (a) `sshd` + privilege separation + the PVC host-key persistence path
   under a Nix-composed rootfs; (b) `better-sqlite3`'s native addon under a Nix
   Node rather than the current npm-from-source compile
   (`runner/Dockerfile:11-27`) — the exact failure mode the current comment warns
   about; (c) the entrypoint/CMD contract. The flake today builds only
   `cmd/sandbox` (`nix/package.nix:26`, `flake.nix:20-23`) — there is no runner
   derivation to extend, so this is a from-scratch image build, not an
   incremental change. Deferred, not forbidden: once the Flox layer is proven in
   the pod, a Nix-built OCI is a reasonable *second* pass.

3. **Flox-containerized runner (`flox containerize` produces the image).**
   Rejected for the first pass for the same reason as (2), plus it inverts the
   ownership of the base: sshd/Node/entrypoint would have to be expressed as a
   Flox environment's manifest/services rather than a Dockerfile, throwing away
   the working multi-stage build and the `npm prune` trick that keeps the runtime
   toolchain-free. The Flox *skill* set (flox-containers) makes this attractive
   later, but it is a bigger rewrite than layering Flox onto the existing image.

4. **Split per-backend images with no shared package manager (status-quo shape,
   just more images).** Rejected as the *package-manager* answer — it is
   orthogonal. §5 (`TODO.md:870-879`) already wants to split the image so the
   claude path doesn't carry the opencode-only `npm i -g` layer
   (`runner/Dockerfile:66`). That split is worth doing, but on its own it gives
   agents no reproducible package manager and no cache story. This ADR's Flox
   layer becomes the **shared base layer** the per-backend split sits on top of
   (see Composition with §5), so the two decisions compose rather than compete.

## Design

### 1. Runtime bootstrap env/mount seam

The pod env is built once in `internal/k8s/backend.go:buildEnv` (common vars at
`backend.go:1316-1342`, then backend-specific credential vars). Volume mounts are
built in `runnerVolumeMounts` (`backend.go:1292-1307`): the session PVC at
`/session` plus the read-only SSH-key projection at `sshAuthorizedKeyMountPath`;
the pod template's `Volumes` (`backend.go:1250-1272`) are the PVC and the
per-session Secret only. This ADR extends the **common** env block (so every
backend gets it) with a package-manager seam:

```
# added to the common env in buildEnv (backend.go:1316-1342), values below are illustrative
SANDBOX_PKG_MANAGER   = "flox"                       # flox | nix | none  (agent guidance + PATH setup)
FLOX_CACHE_DIR        = "/session/state/flox-cache"  # per-session, on the PVC (see §3)
NIX_CONFIG            = "substituters = ...\ntrusted-public-keys = ..."   # cluster cache wiring (see §4)
# optional, only if a shared read-only /nix store is mounted (see §3):
# NIX_REMOTE / store path env for the mounted store
```

Design rules for the seam:

- **Preserve the existing mounts unchanged.** The `/session` PVC mount and the
  SSH-key projection (`backend.go:1292-1307`, `1250-1272`) stay exactly as they
  are; the entrypoint's host-key + authorized_keys handling
  (`entrypoint.sh:12-32`) is untouched. Any Flox/Nix mount is *additive*.
- **Cache dirs live on the PVC by default** (`FLOX_CACHE_DIR` under
  `/session/state/...`), so a per-session download cache survives suspend/resume
  the way `session.json`/`events.db` do — but a per-session PVC is explicitly
  **not** a shared `/nix` store (see §3).
- **An optional shared read-only `/nix` (or Flox) store mount** is left as a seam
  the pod template *can* add (a cluster-provided volume, not the session PVC),
  gated behind the cache decision in §4. It is not required for the first pass —
  baked closures + substituters cover the common case.
- The values are **cluster config, not image config** — that is the whole point
  of putting them in `buildEnv` rather than the Dockerfile. Home and work
  clusters set different substituters (see §4) without rebuilding the image.

### 2. Propagating the Flox/Nix preference to agent child processes

Two backends spawn the agent, and they take env differently:

- **Claude** builds an **explicit** env map for the spawned `claude` binary
  (`runner/src/claude.ts:221-230`): it spreads `process.env` then overlays
  `CLAUDE_CONFIG_DIR`, `CLAUDE_CODE_DISABLE_AUTO_MEMORY`, `IS_SANDBOX`. Because it
  spreads `process.env`, the pod-level seam vars from §1 already flow through —
  but `PATH` and any Flox/Nix cache/config env that must take a *specific* value
  for the child should be set explicitly here alongside the existing overlays, so
  the agent's `flox`/`nix` resolve deterministically regardless of what the base
  image PATH happens to be.
- **OpenCode** inherits the env wholesale: `startOpencodeSupervisor` defaults to
  `process.env` (`runner/src/opencode.ts:275-287`) and the supervisor spawns
  `opencode serve` under it. So the §1 seam vars flow through unless something
  strips them; no explicit overlay is needed, but the same PATH/cache vars must
  be present in the pod env for the inherited path to work.

**Agent guidance (prompt/system-level, not just env):** the agent should be told
to, in order of preference: (1) use an existing project Flox env if the workspace
has a committed `.flox/`; (2) otherwise `flox activate` / `flox run <pkg>` for a
tool it needs repeatedly, creating a project env only when that is the right
scope; (3) otherwise `nix run nixpkgs#<pkg>` for a genuine one-off — and **avoid**
`apt-get`/`npm i -g`/`pip install`, which mutate the image's world and don't
persist or reproduce. This mirrors the maintainer's own global instruction and
keeps the pod's package surface reproducible and cache-backed.

### 3. Per-session PVCs stay out of the `/nix`-store business

A per-session PVC is the wrong substrate for a Nix store: it is single-writer,
not shared, and re-downloading/rebuilding a closure per session defeats the
point. The rule this ADR commits to:

- **Per-session PVC** (`/session`, `backend.go:1250-1258,1294`) holds session
  state, workspace, and at most a *download/eval cache* (`FLOX_CACHE_DIR`) — never
  the canonical shared store.
- **Shared store** (if any) is a **read-only, cluster-provided** mount (§4),
  independent of the session PVC lifecycle, so it is never coupled to
  suspend/resume/destroy of a single session.

This keeps the PVC's role identical to today (state + workspace) and pushes all
sharing into the cache layer, where it belongs.

### 4. Cluster cache strategy (substituters, anti-poisoning, pruning)

This section captures the maintainer's requirements verbatim in spirit; the
**scan-then-publish gate is a follow-on design, explicitly out of runner-image
scope** (it is called out as such in TODO §7b).

**a. Configurable trusted-substituters so it "just works."** The trusted
substituters/binary caches are set through the §1 env seam (`NIX_CONFIG` /
Flox equivalent), **configurable per cluster** so the same image works in both
environments:

- **Home:** a ceph-backed S3 Nix cache. Note: the example egress allowlist
  (`k8s/networkpolicy-egress-allow.yaml:48-59`) allows public 443 but **carves
  out RFC1918 / CGNAT / link-local** (lines 52-56). A home cache on a private IP
  is therefore **blocked by default** — reaching it requires adding its CIDR to
  the egress policy (or fronting it with an allowed host). This coupling between
  the cache location and the network policy must be part of the cache rollout,
  not an afterthought.
- **Work:** needs a "reasonably generic mechanism" — a substituter URL + trusted
  public key supplied through the same env seam, with the egress allowlist opened
  to exactly that host. No image rebuild between clusters; only the injected
  `NIX_CONFIG` and the egress policy differ.
- **Baked closures first.** Where a tool is known ahead of time (the base
  closure), bake it into the image layer so the common path needs *no* substituter
  round-trip at all — the substituter is for the long tail of agent-requested
  tools.

**b. Anti-poisoning: agents must not publish to the cache directly.** Agents get
**read (substitute) access only.** Publishing is mediated by a separate,
privileged path — the sketch is an MCP tool or a Job that **scans a built closure
and publishes it only if it passes** a policy check, rather than letting the
agent `nix copy` arbitrary paths into the shared cache. **Open question flagged by
the maintainer:** whether the publish step **re-signs** the closure with a
cache-owned key (so trust derives from the gate, not from whatever key the agent
produced) or preserves upstream signatures. **This scan-then-publish gate is a
follow-on design and is out of scope for the runner image** — the runner-image
work only needs to guarantee agents have *substitute* (read) access and *no*
direct publish credential.

**c. Pruning.** A shared cache needs a garbage-collection/retention story for
entries no longer referenced (age-based and/or GC-root-based eviction on the
cache backend). Also a seam, owned by the cache backend, not the runner image;
noted here so it is not forgotten when the cache is stood up.

The current caching in this repo is **OCI-layer only**: image pull policy is
digest-vs-tag based (`backend.go:194-208`), the pinned-image annotation keeps a
session on one runner binary across resume (`backend.go:55-62`), and the §5 Spegel
item (`TODO.md:870-879`) is a P2P mirror for *OCI images*, not a Nix store. None
of that helps a `nix`/`flox` fetch inside the pod — hence this separate cache
layer.

### 5. CI triggers and host-tool gaps

**Runner-image CI triggers.** The runner image is built by
`.depot/workflows/build-runner-image.yml`. Two things matter if the image starts
depending on Flox/Nix files:

- Its `paths:` triggers are `runner/**`, the workflow file, and `depot.json`
  (`build-runner-image.yml:12-15,17-20`). If the Dockerfile bakes a base env from
  `.flox/`, `flake.nix`, `flake.lock`, or `nix/**` (all at the **repo root**,
  outside `runner/`), those paths **must** be added to both the `push` and
  `pull_request` trigger lists, or a cache-definition change won't rebuild the
  image.
- The build **context is `runner`** (`build-runner-image.yml:55`). Root-level
  `.flox`/`flake.nix`/`nix/**` are **outside that context** and cannot be `COPY`ed
  today. Consuming them requires either moving the context to the repo root (and
  re-rooting the Dockerfile's `COPY` paths) or vendoring the needed manifest into
  `runner/`. This is a real constraint the implementation must resolve, not a
  one-line `paths:` add.

**Host-tool gaps.** Two host-side flows shell out to tools the laptop must have,
independent of the pod:

- `opencode attach` requires a host `opencode` on PATH
  (`internal/tui/dashboard/external_pane.go:121-125` — `exec.LookPath("opencode")`,
  with the actionable "install it locally (Nix)" error).
- `claude setup-token` requires a host `claude` on PATH
  (`internal/cli/auth_accounts.go:37-40` — `exec.LookPath("claude")`).

Neither is in the Flox dev env today, and `just doctor` (`justfile:259`) checks
`go node gcc git kind ctlptl docker tilt kubectl helm crictl mutagen
golangci-lint jq` — **not** `claude`/`opencode`. Two options, in preference
order: (a) **add `opencode` (and `claude` if packaged) to
`.flox/env/manifest.toml`** so they resolve from the pinned env like every other
tool; (b) where a tool can't be packaged, **add it to the `just doctor` loop** so
the gap is reported rather than surfacing as a bare `ENOENT`/LookPath error at
use time. `opencode` is already Nix-managed on the host (`runner/Dockerfile:64`
notes the host client is Nix-managed), so (a) is realistic for it.

## Composition with the §5 per-backend image split

§5 (`TODO.md:870-879`) splits the single "claude-runner" image (a misnomer — one
image serves every backend) into per-backend images so the claude path drops the
opencode-only `npm i -g opencode-ai` layer (`runner/Dockerfile:65-66`) and codex
can add its own. This ADR's Flox layer is designed to be the **shared base layer**
underneath that split: base OS + sshd + Node + `better-sqlite3` + the baked Flox
closure live in one shared layer, and each per-backend image adds only its
agent-specific bits on top. The two decisions are complementary — do the Flox
layer as the shared base, then split backends above it — and both compose with the
§5 Spegel OCI mirror (which speeds the *image* pull, orthogonal to the in-pod Nix
cache in §4).

## Decided regardless of ADR outcome

The Flox activation hook runs `go get .` on **every** `flox activate`
(`.flox/env/manifest.toml:54-60`). This mutates `go.mod`/`go.sum` and the module
cache as a side effect of merely entering the env (or `cd`-ing in, under direnv),
which is a surprising, non-reproducible side effect for a dev/CI/runtime contract.
**Remove it** (item 6) independent of which package-manager direction is chosen —
it should not be part of the env that becomes runtime-canonical.

## Consequences

- **Positive:** the pod agent gets the same reproducible, cache-backed package
  surface the laptop has; one-off tools stop mutating the image; the seam is
  cluster-configurable so home/work differ by injected config, not image builds;
  the change is additive and reversible; it composes cleanly with the §5 split.
- **Negative / cost:** image size grows by the Flox/Nix runtime + base closure;
  two package worlds (apt base OS + Flox tools) coexist and must be documented so
  nobody apt-installs what Flox owns; the CI build context/trigger constraint
  (§5) is real work, not a config tweak; the cache backend (substituter,
  publish-gate, pruning) is net-new infrastructure, most of it follow-on.
- **Security/safety:** agents get **substitute (read) access only** — no direct
  publish credential — so a compromised/hallucinating agent cannot poison the
  shared cache; the publish gate (re-signing TBD) is the only write path and is
  designed separately. sshd's pubkey-only boundary and the PVC host-key path are
  untouched. The egress allowlist stays default-deny; opening it to a substituter
  host is an explicit, reviewable policy change (§4a).

## Rollout (once signed off)

1. Remove the `go get .` activation hook (`.flox/env/manifest.toml:54-60`) —
   independent, can land first.
2. Add the base Flox layer to the runtime stage of `runner/Dockerfile`; resolve
   the CI build-context/trigger constraint (§5) in the same change.
3. Extend `buildEnv` common env (`backend.go:1316-1342`) with the package-manager
   seam (§1); wire the explicit Claude child env overlay
   (`claude.ts:221-230`) and confirm the OpenCode inherited path
   (`opencode.ts:275-287`) carries it.
4. Add agent guidance (prefer project Flox env / `flox run` / `nix run`) to the
   agent's system prompt/config.
5. Package `opencode`/`claude` in `.flox/env/manifest.toml` where possible, else
   extend the `just doctor` check (`justfile:259`) to report the host-tool gap.
6. Stand up the cluster cache (substituter + egress opening) as its own change;
   the scan-then-publish gate and pruning follow separately.
7. Compose with §5: make the Flox layer the shared base of the per-backend split.

## Open questions for maintainer sign-off

1. **Base closure contents.** Exactly which tools get baked into the Flox layer
   vs left to on-demand `flox run`/`nix run`? Minimum viable set: `git`, `sqlite3`,
   diagnostics. Does `opencode` move from `npm i -g` (`runner/Dockerfile:66`) into
   the Flox closure, or stay npm-global until the §5 split?
2. **CI build context.** Move the runner image's Depot build context from `runner`
   to the repo root (re-rooting `COPY` paths) so it can consume `.flox`/`flake.nix`/
   `nix/**`, or vendor a runner-local manifest into `runner/`? (§5)
3. **Substituter mechanism for work.** What is the "reasonably generic" work-cluster
   substituter — a plain S3/HTTP binary cache URL + trusted key injected via
   `NIX_CONFIG`, or something else? And confirm the home ceph-S3 cache's CIDR gets
   added to the egress allowlist (`networkpolicy-egress-allow.yaml`) rather than
   fronted by a public host. (§4a)
4. **Publish-gate re-signing.** Does the scan-then-publish gate **re-sign** closures
   with a cache-owned key (trust from the gate), or preserve upstream signatures?
   (Confirming this is out of runner-image scope, but the answer shapes the cache
   design.) (§4b)
5. **Shared `/nix` store mount — yes or no for pass 1?** Rely on baked closures +
   substituters only (simpler), or also mount a cluster-provided read-only store?
   (§3, §4)
6. **Pruning owner/policy.** Age-based, GC-root-based, or manual? Which component
   owns it? (§4c)
7. **Timing vs §5.** Land the Flox base layer first and split per-backend on top,
   or do both in one change? (Composition section)
8. **When (if ever) to revisit the fully Nix-built OCI** (rejected option 2) as a
   second pass once the Flox layer is proven in-pod?

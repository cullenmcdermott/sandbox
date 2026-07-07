# Consolidated decision proposals — 2026-07-06

**Status:** IMPLEMENTED/RESOLVED — every proposal below was decided in the
2026-07-07 live review and the outcome migrated into the source ADRs/designs and
TODO.md (§1e, §7b, §9, §8, §2d). This doc is the point-in-time decision record;
archived 2026-07-07. Nothing here is open.

**Original status line (kept for provenance):** partially decided (live review
with maintainer, 2026-07-07):
- **§1 server-side loop — SIGNED OFF as proposed** (ADR status updated).
- **§2 package manager — SIGNED OFF with amendment:** spike
  `ghcr.io/flox/flox` as the base (one package world, flox ≥ 1.13 for
  `flox run`) before falling back to the Debian+layer shape; the rest of
  §2's answers stand (ADR status updated).
- **§3 KRO — DECIDED: not adopting;** ownerReferences directly. ADR archived.
- **§4 worktrees — SIGNED OFF** with one flip: **4.8 = the TUI owns the
  proposal prompt** (no `Session.ProposeBranch` in v1); 4.5 WIP-commit and
  4.2 transcript re-keying confirmed; 4.7 = B1 only (design status updated).
- **§5 — ALL DECIDED as proposed:** yolo default = yes (runner-side flip +
  statusline mode surface); first-account = always show the stage;
  external-pane keys = ctrl+] leader-chord, reserve nothing else; opencode
  modal = no, full-screen stays.
- **§7.1 AICR — RESOLVED:** it's [github.com/nvidia/aicr](https://github.com/nvidia/aicr)
  (maintainer works on it; wants homelab use cases for multi-piece config
  sync) — split into its own §10 research item; `sandbox doctor` stands
  alone.
- **§6 SDK sweep — ALL DECIDED:** 6.1 narrow interface, 6.2 one
  naming break, 6.3–6.7/6.9 approved as proposed, **6.8 overridden — full
  `Session.Shell` in the SDK** (maintainer wants the one-call interactive
  shell), 6.10 resolved via worktrees 4.10. TODO §8 is now an implementation
  backlog.

Nothing in this document remains undecided.

One proposal per open maintainer decision in TODO.md.
Accepting a proposal = mark the decision in the source ADR/design (or inline
in TODO.md), then the item becomes Opus-executable. Rejections just need a
one-line reason so the alternative is recorded.

Format per decision: **Proposal** (what to do) + **Why** (grounded in the
architecture/goals: runner-is-control-plane, SDK-first, cross-backend parity
bar, pre-OSS breaking changes OK, unattended-autopilot headline use case,
real-world cost sensitivity).

---

## 1. Server-side loop ADR (§1e) — [docs/server-side-loop-adr.md](server-side-loop-adr.md)

The ADR's committed design (runner-owned driver, explicit `state` field, boot
re-arm, 409-defer/retry ladder) is sound; these settle its open items.

**1.1 Endpoint shape** — **Adopt (a): `PUT/DELETE /sessions/:id/autopilot`.**
Arming is a lifecycle action, not a turn; disarm must be first-class for the
esc/stop contract and the reaper interaction. The turns API stays
single-purpose, which also keeps the codex/opencode turn adapters unpolluted.

**1.2 Guard defaults** — **`max_iterations` default 50 (not 200), always
enforced; `token_budget` optional (null) but surfaced in the armed chip.**
50 Opus iterations against a real backlog is already a very long unattended
run; 200 as a *default* is a footgun given real monthly spend limits. Raising
it is a deliberate per-arm act (`/loop --max 200`). Ship `token_budget` in v1
(the field is cheap; the runner already receives usage events per turn) —
with yolo-by-default (decision 5.1) these guards are the primary cost control,
per the ADR's own security note.

**1.3 Capability-bit home** — **`GET /sessions/:id/status`.** The TUI already
fetches status on attach; a dedicated `/capabilities` endpoint is a second
round-trip and a second version-skew surface for exactly one bit today. If
capabilities multiply (codex/opencode feature flags), promote to a
`capabilities: []string` *field on status* — still not a new endpoint.

**1.4 Retry/staleness constants** — **H2: 5 attempts, backoff
`max(interval_ms, 30s)` doubling, capped at 5 min. Q1/H1 staleness bound N =
30 min.** A single Opus turn regularly runs >10 min, so N must comfortably
exceed a long turn; 30 min lapses a genuinely wedged driver the same night
rather than pinning the pod unreapable until morning. All three are runner
constants, not spec fields — don't grow the API for tuning knobs until
someone actually needs per-session values.

**1.5 H4 anti-double-submit guard** — **Skip in v1; accept the skew risk.**
Single-maintainer tool, CLI and runner upgrade together; the mitigation
(409 foreign submits while armed) is a small, purely-additive server change
that can land later without schema impact if skew ever bites.

**Verdict: sign off the ADR with 1.1–1.5 recorded; implement in the ADR's
rollout order (schema → runner → TUI → tests).**

---

## 2. Runner package-manager ADR (§7b) — [docs/runner-package-manager-adr.md](runner-package-manager-adr.md)

Direction (Debian base + Flox layer, substitute-only cache access) is right.
**Standing maintainer directive (2026-07-06): Flox (preferably) or Nix is the
preferred way to install anything, everywhere in the chain** — host (already
Nix-managed), dev/CI env (already Flox), runner image (this ADR), agents
in-pod (this ADR's guidance section), and ad-hoc tooling containers. Apt/npm
in the runner image shrink to the irreducible base (OS, sshd, Node runtime,
the native `better-sqlite3` compile) and everything above it resolves from
Flox. The ADR should carry this as a stated principle, not just a mechanism.
Answers to its 8 open questions:

**2.1 Base closure contents** — **`git`, `sqlite3`, `curl`, `jq`, `ripgrep`,
`fd` — the diagnostics/QoL set agents reach for constantly — and `opencode`
moves into the Flox closure too** (maintainer directive 2026-07-06: Flox/Nix
is the preferred install mechanism *everywhere in the chain* — host, CI,
containers, pods). The one real constraint is version pinning: the Dockerfile
pins `opencode-ai@1.17.7` deliberately and the Flox catalog can lag
opencode's release cadence — so pin the exact version in the baked env
manifest, and fall back to the npm pin *only* if the catalog can't serve the
required version, documented as a temporary exception, re-checked at the §5
split.

**2.2 CI build context** — **Move the Depot build context to the repo root;
keep `dockerfile: runner/Dockerfile`; add a `.dockerignore` to keep the
context small.** Vendoring a copy of the Flox manifest into `runner/` is a
drift bug waiting to happen — the whole point is *one* pinned env. Add
`.flox/**`, `flake.nix`, `flake.lock`, `nix/**` to both trigger lists in the
same change.

**2.3 Work substituter mechanism (+ the §0 "host it on tigris?" question)** —
**A plain HTTP(S)/S3 binary-cache URL + `trusted-public-keys` injected via
the `NIX_CONFIG` env seam *is* the generic mechanism — don't invent more.**
Home: add the ceph-S3 cache's CIDR:port to the egress NetworkPolicy
explicitly (reviewable, default-deny preserved) rather than fronting it with
a public host. **Tigris: yes, it works** — it's S3-compatible, so
`s3://<bucket>?endpoint=https://fly.storage.tigris.dev` is a valid Nix
substituter/`nix copy` target, and being a public HTTPS host it needs *no*
egress-policy carve-out (public 443 is already allowed). Recommended split:
**home = ceph S3** (free, local, fast), **Tigris = the public/work/OSS-users
cache** — especially attractive later as the cache external users hit when
the project open-sources. Same `NIX_CONFIG` seam serves both; only the
injected URL+key differ per cluster.

**2.4 Publish-gate re-signing** — **Re-sign with a cache-owned key.** Trust
must derive from the gate having scanned the closure, not from whatever key
produced it; pods then trust exactly one key, and key rotation is a
cache-side operation. (Confirming: out of runner-image scope; this just
shapes the follow-on design.)

**2.5 Shared `/nix` store mount in pass 1** — **No.** Baked closures cover
the common path; substituters cover the tail. A shared read-only store is a
cluster-storage dependency (RWX or per-node) with real failure modes — earn
it with evidence that substituter latency actually hurts.

**2.6 Pruning** — **Age-based (e.g. not-fetched-in-90d) via S3 lifecycle
rules / a periodic GC job, owned by the cluster GitOps repo, not this repo.**
Simplest policy that prevents unbounded growth; GC-roots are overkill for a
cache whose worst case is a re-download.

**2.7 Timing vs §5 split** — **Flox base layer first, per-backend split on
top, two changes.** The layer is the shared base the split sits on; doing
both at once doubles the blast radius of the first image restructure.

**2.8 Nix-built OCI revisit + distribution outputs** — **Revisit after (a)
the Flox layer is proven in-pod and (b) the §5 split lands — and make the new
§0 inbox note ("create nix flake with binary and container outputs") the
vehicle:** the flake already packages the CLI; a `packages.runner-image`
(dockerTools/flox containerize) output is exactly the second pass the ADR
deferred. That inbox item is *this* decision, not a separate workstream —
triage it into §7b as the deferred pass-2 marker.

**Distribution channels (maintainer add, 2026-07-06):** alongside the Tigris
binary cache, **publish `sandbox` to FloxHub as a public package** — the CI
(Depot) pipeline that builds + pushes the Tigris cache is the natural place
to also `flox publish`. For OSS users this is the lowest-friction install
(`flox install <owner>/sandbox`) and it dogfoods the exact Flox-first
directive the runner ADR encodes. Sequencing: FloxHub publish only needs the
*existing* CLI package (could land before the OCI pass 2); the runner env
publishing to FloxHub is optional later. Note FloxHub publish requires the
package be built from a flox env build/expression — worth deciding whether
the canonical build stays the Nix flake (Tigris/nix users) with a parallel
`flox build` definition (FloxHub users), or the flake becomes the single
source both consume; lean single-source if FloxHub's Nix-expression build
path supports it cleanly.

**Verdict: sign off with 2.1–2.8; land the `go get .` hook removal first
(already decided).**

---

## 3. KRO composite ADR (§10) — [docs/archive/kro-composite-adr.md](archive/kro-composite-adr.md)

**Proposal: accept the ADR's recommendation — do NOT adopt kro; implement §6
item 3's `ownerReferences` (Secret+PVC → Sandbox) directly in
`CreateSession`; archive the ADR as decided.**

**Why:** the composition is 3 resources whose interesting parts (token/key
minting, runner-API status merge) can't move into CEL anyway, so kro delivers
a partial "one object" story at the price of a pre-1.0 (`v1alpha1`,
breaking-changes-warned) controller that every external user would have to
install next to agent-sandbox — directly against the OSS-prep goal of a low
adoption bar. ownerReferences is ~10 lines, closes the only concrete
correctness gap (out-of-band `kubectl delete sandbox` orphans), and keeps the
existing rollback semantics. Revisit trigger stays as written (kro v1 + the
graph growing past ~5 resources).

---

## 4. Worktree lifecycle design (§9) — [docs/worktree-lifecycle-design.md](worktree-lifecycle-design.md)

The design is solid (auto-branch `sandbox/<id>` from creation is the key move
— I2 holds from the first commit). Answers to its 10 questions:

**4.1 Spec split naming** — **Add `Spec.WorkspacePath`; `ProjectPath` keeps
meaning "repo root".** Less churn for every existing reader, and
grouping/display semantics stay under the familiar name. Default
`WorkspacePath == ProjectPath` makes non-worktree sessions bit-identical to
today.

**4.2 Transcript path change** — **Accept worktree-path keying.** Per-session
transcript isolation is *better* (two sessions on one repo no longer
interleave in one `~/.claude/projects` folder), and `claude --resume` run
from inside the worktree finds its history naturally. Preserving repo-path
grouping would break the pod-cwd == alpha-path identity that makes §2.2 work
— not worth it.

**4.3 Default mode** — **`WorktreeAuto` from day one.** The feature ask was
literally "new sessions should automatically get their own worktree";
shipping opt-in-first just delays finding the edge cases. `WorktreeOff`
remains the escape hatch flag.

**4.4 Non-git §1d collision** — **Warn-only** (Connection.Warning when a
second live session syncs the same path). Hard-refusing breaks legitimate
single-user flows and the failure mode (mutagen conflict) is recoverable,
not data loss.

**4.5 Dirty-destroy policy** — **Default: silent WIP commit to the session
branch, report the branch name in the destroy output. `--discard` for
explicit opt-out.** Never-lose (I2) outranks branch litter; litter is exactly
what `sandbox worktree gc` + a `sandbox/*` prefix make cheap to sweep.

**4.6 Merge helper** — **Skip in v1.** I4 holds structurally; merging is the
user's own git muscle memory. A convenience wrapper adds SDK surface (sdktest
pins, error modes) for something `git -C /repo merge X` already does.

**4.7 Cross-machine attach** — **B1 only** (runner surface works, file sync
warns and stays off). B2 ("move the session here") is a real feature with
real divergence semantics — build it when someone actually needs it.

**4.8 LLM proposal helper** — **Yes, ship `Session.ProposeBranch` in
`client/`.** SDK-first rule: the TUI must dogfood, and an external consumer
building the same convert flow shouldn't have to reinvent the prompt. Keep it
a *separate* method returning strings so `ConvertToBranch` stays LLM-free
(I3 intact).

**4.9 Branch naming policy** — **Any valid ref the human approves
(`git check-ref-format` is the only gate); the *prompt* suggests `feat/`/
`fix/` prefixes.** Enforcing a prefix in the deterministic layer turns a
style preference into an error path.

**4.10 Worktree root** — **`~/.local/share/sandbox/worktrees/<id>`, and
resolve the §8 `WithStateDir` question in the same change: move the ssh
include *inside* the state root (`<stateRoot>/{remote-sessions,ssh,worktrees}`).**
Pre-OSS is the one cheap moment for the breaking include-path migration;
deciding the app-dir layout once beats deciding it twice.

**Verdict: sign off with 4.1–4.10; implementation order: Spec split →
Create/auto-worktree → destroy/reap capture paths → convert-to-branch →
ProposeBranch helper. This also closes §1d's sync-collision item for git
projects.**

---

## 5. §2d policy/UX decisions

**5.1 Default permission mode = `bypassPermissions` — YES** (confirming the
Fable recommendation). The safety envelope is already the pod, not the
prompt: default-deny egress, Bash guards, audit log, `IS_SANDBOX=1` +
`allowDangerouslySkipPermissions` wired. The headline use case (unattended
TODO burn-down) is incoherent with per-tool prompting, and interactive users
can still pick a stricter mode per turn. Two conditions in the same change:
(a) the statusline surfaces the active mode so yolo is never invisible;
(b) land it together with the autopilot guards (1.2) since they become the
cost backstop. Implementation: flip the runner's empty-mode default
(`claude.ts:70-81`) — runner-side, so every client inherits it.

**5.2 First-account path — always enter the account stage** with
`cluster-default` + `＋ add account` rows (Fable rec confirmed). One extra
keypress in the common case vs an undiscoverable CLI detour for the
first-run case; also becomes the natural home of the §6 reauth stage, so the
picker stage machinery gets built once.

**5.3 ctrl+g/ctrl+k in the external pane — reserve NEITHER; add a
leader-chord instead.** Forward both to the embedded client (status quo).
For dashboard nav from inside an external pane, extend the *already-reserved*
detach key into a leader: `ctrl+]` then `g`/`k` = next/prev-attention,
`ctrl+]` then `ctrl+]` (or timeout) = detach as today. Rationale: ctrl+k is
opencode's command palette — stealing it traps users in *our* chrome instead
of theirs, the exact failure the TODO note fears; and codex will bring its
own bindings, so per-key negotiation doesn't scale. One reserved prefix is a
contract every backend can live with, and it slots into the §2a binding-table
work as a proper input context.

**5.4 OpenCode window as modal over the dash — NO; keep the full-screen
takeover.** A modal PTY means constant SIGWINCH/reflow churn on a client we
don't control, wasted columns for an agent UI designed full-width, and a
"whose chrome wins" ambiguity. The parity bar explicitly allows per-agent
in-pane rendering to differ while requiring detach/keybinding/metrics parity
— invest there (identical detach chrome + status strip, item already in §7c)
instead of window dressing.

---

## 6. §8 SDK surface decisions (pre-OSS; break now, update sdktest pins in-change)

**6.1 `WithBackend` seam** — **Define a narrow public `client.Backend`
interface (exactly the methods `Create`/`Connect`/`Destroy` orchestration
calls); `internal/k8s.Backend` satisfies it; `WithBackend` takes the
interface.** Don't drop the option — it's the only path to fake-injection
unit tests for orchestration (currently zero coverage) and the seam a future
non-k8s backend needs anyway. Pin it in sdktest.

**6.2 Claude-SDK-shaped turn/state model** — **One coordinated break,
backend-neutral names:**
- `TurnInput.Mode` → a small owned enum (e.g. `ApprovalPolicy: default |
  acceptEdits | bypass | plan`), mapped per-backend in the runner; backends
  that can't honor it ignore it *documented*, not silently.
- `Connection.Opencode` → `Connection.External` (generic external-pane
  connection info) since codex will need the identical shape.
- `State.ClaudeSession` → `State.AgentSessionID` (one backend per session ⇒
  one resume id; no map needed).
Do all three in one change — the codex plan already pre-announces the break,
and sdktest makes the blast radius visible in-repo.

**6.3 tui/theme registry** — **Add `Register(Theme)` (doc promise exists —
this is a bug, not a decision) + export the missing `Denied/Info/Success/
Warning` active tone vars.**

**6.4 tui/kit palette race** — **`atomic.Pointer[palette]` swap.** Documented
single-goroutine ownership is a footgun for exactly the "two tea.Programs"
case the item names; the atomic swap is ~20 lines and removes the panic
class.

**6.5 `tui/list.Item.Finished()`** — **Drop it.** Dead API, every implementer
pays for it.

**6.6 `Destroy` sync ordering** — **Stop sync *before* cluster destroy**
(mirror the TUI's PreDestroyHook ordering inside the SDK). Fix, not a
decision — library callers shouldn't race EOF errors the TUI already
dodges.

**6.7 `DialRunner` SSH forward** — **Stop forwarding the unused port.**
Nothing consumes it; it's connection latency + a confusing surface.

**6.8 `sandbox shell` SDK gap** — **Expose the primitive, not the terminal:**
`Session.SSHConfig()` (host alias, config path, port) so a consumer can run
`ssh`/`exec` themselves; the interactive wrapper stays in `internal/cli`.
Embedding a PTY shell in the SDK drags terminal handling into a library that
is otherwise transport-shaped.

**6.9 `kit.FormatTokens` 1000M cap** — **Add the `B` tier.** Trivial; no
decision needed, just batch it with 6.3–6.5.

**6.10 `WithStateDir` ssh layout** — **Contain `ssh/` inside the state root;
do the breaking include-path migration now, together with the worktree root
(4.10).** Pre-OSS is the only cheap moment; one app-dir layout decision,
made once.

Suggested batching: 6.3/6.4/6.5/6.9 = one mechanical tui/* PR; 6.6/6.7 = one
client-behavior PR; 6.1, 6.2, 6.8, 6.10 each stand alone (real surface
changes).

---

## 7. Flagged, not proposable

**7.1 "AICR recipe" (§10)** — the acronym appears nowhere in the repo and I
won't guess a meaning into a design. Best candidates: "AI Code Review
recipe" or a tool/hostname. **The `sandbox doctor` item is executable
without it** — propose proceeding on doctor (kubeconfig/context, mutagen,
ssh, image refs, credential store checks) and expanding AICR when you
remember what it meant.

**7.2 §3 upstream watch** — no decision needed; periodic check stands.

---

## Summary table

| # | Decision | Proposal |
|---|---|---|
| 1 | Server-side loop ADR | Sign off: endpoint (a), max_iter 50 + optional token_budget, capability bit in /status, 5×/30s→5m retry, N=30m, skip H4 guard |
| 2 | Package-manager ADR | Sign off: git/sqlite/curl/jq/rg/fd closure, root build context, NIX_CONFIG substituters + egress CIDR, re-sign at gate, no shared store, 90d age prune, layer-then-split, flake container output = pass 2 |
| 3 | KRO | Reject kro; implement ownerReferences; archive ADR |
| 4 | Worktrees | Sign off: WorkspacePath split, worktree transcripts, Auto default, warn-only non-git, WIP-commit destroy, no merge helper, B1 only, ship ProposeBranch, free naming, state-root layout with 6.10 |
| 5.1 | Yolo default | Yes — runner-side flip + statusline mode surface + guards land together |
| 5.2 | First account | Always enter account stage |
| 5.3 | External-pane keys | Reserve neither; ctrl+] leader-chord |
| 5.4 | OpenCode modal | No — keep full-screen |
| 6 | SDK sweep | Narrow Backend iface; one neutral-naming break; Register+tokens; atomic palette; drop Finished(); sync-before-destroy; drop SSH forward; SSHConfig primitive; B tier; contained ssh/ |
| 7.1 | AICR | Needs your expansion; doctor proceeds regardless |

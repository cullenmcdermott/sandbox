# ADR: KRO composite resource for session provisioning

- **Status:** Research / Proposed (Opus draft, 2026-07-05). No code until
  decided.
- **Scope:** TODO.md §10 "Research/ADR: KRO composite resource". Evaluates
  whether [kro](https://kro.run) (Kube Resource Orchestrator) should wrap the
  per-session **Secret + PVC + Sandbox** trio into one composite custom
  resource, replacing the CLI-side create orchestration in
  `internal/k8s/backend.go` (`CreateSession`, lines ~226–319, plus the
  rollback path `deleteSessionResources`, line ~742). Would subsume the §6
  ownerReferences item (`TODO.md` ~line 952: Secret+PVC → Sandbox GC on
  out-of-band `kubectl delete`).

## Context

`CreateSession` today is a hand-rolled three-step orchestration:

1. Create the per-session **Secret** (runner bearer token, opencode password,
   SSH authorized key, optional Anthropic credential).
2. Create the **PVC** (workspace + state).
3. Create the **Sandbox** CRD (agent-sandbox v1alpha1) that references both.

It carries its own transaction semantics: a `defer` rollback deletes everything
created earlier if a later step fails (`deleteSessionResources`), with a
deliberate exception for the idempotent re-create case (a pre-existing Secret
means the resources belong to a prior call and must NOT be rolled back —
`secretPreexisted`). Destroy/List/Status only ever enumerate Sandboxes, so an
orphaned Secret (live bearer token) or PVC is invisible and leaks. Two known
gaps this couples to:

- **§6 ownerReferences:** `kubectl delete sandbox` outside the CLI orphans the
  PVC + Secret. The fix is `ownerReferences` (Secret+PVC → Sandbox) so cluster
  GC cascades. This is exactly the kind of parent/child lifecycle a composite
  resource expresses natively.
- Rollback is best-effort and runs on an independent short-lived context
  because the caller's ctx may already be cancelled when the failure surfaces.

kro's model: a cluster-side controller reads a **ResourceGraphDefinition**
(RGD) that declares a graph of resources + a CEL-templated schema, and
generates a new CRD (e.g. `SandboxSession`) plus a controller that reconciles
instances of it into the underlying Secret/PVC/Sandbox. The CLI would create
one `SandboxSession` object; kro owns creation ordering, dependency resolution,
and (via ownerReferences it sets) cascading deletion.

## What kro would replace vs. add

Replaces:
- The 3-step create sequence and its manual rollback/transaction logic.
- The §6 ownerReferences work (kro sets owner refs across the graph; out-of-band
  delete of the composite cascades to Secret+PVC+Sandbox for free).

Adds:
- A **cluster-side controller dependency** (kro) that must be installed,
  upgraded, and RBAC'd alongside the agent-sandbox controller.
- A new CRD (`SandboxSession`) + RGD to author and version.
- Indirection: the runner token / SSH key generation still happens CLI-side
  (they can't be CEL-derived), so the Secret's *contents* must still be supplied
  by the CLI — kro would compose an existing Secret, or the CLI keeps creating
  the Secret and kro composes only PVC+Sandbox. Either way the "one object"
  story is partial.

## Key question: status / conditions support

The TODO flags custom status/conditions as the deciding factor, because
List/Status/the dashboard need a single readable state per session.

Findings (kro docs, mid-2026):
- **RGD-level status** exposes five conditions (`ResourceGraphAccepted`,
  `KindReady`, `ControllerReady`, `Ready`) describing whether kro is *serving*
  the generated API — infra health, not per-instance session health.
- **Instance-level custom status** is supported: the RGD `status` block uses
  **CEL expressions** to project fields from the composed resources (e.g. surface
  the Sandbox's pod-readiness or a PVC bound phase) and can aggregate across
  collections. This maps reasonably onto what `statusFromSandbox` derives today.
- Caveat: our session state (Creating/Running/Suspended/Gone, pod-ready, active
  turn) is derived by Go from the Sandbox + a live pod-readiness probe and the
  runner's `/status`. CEL over static resource fields can express the k8s-visible
  part but **not** the runner-API-derived parts (active turn, claudeSession).
  Those stay a runner call regardless, so kro's status doesn't remove the
  two-source status merge — it only relocates the k8s half.

## Maturity assessment

- kro is **pre-1.0**: latest release **v0.9.2** (May 2026), API at
  **`v1alpha1`** with explicit "may introduce breaking changes" language.
- Origin AWS (KubeCon NA 2024), now vendor-neutral (Google + Azure joined);
  a **subproject of Kubernetes SIG Cloud Provider**. Not a formally graduated/
  incubating CNCF project; treated as early-stage, dev-environments-encouraged,
  not-yet-production-hardened by its own docs.

## Decision (recommendation)

**Do not adopt kro now.** The composition is only 3 resources with generation
logic (tokens/keys) that can't move into CEL, so kro would not actually collapse
provisioning to "one declarative object" — the CLI still mints the Secret
contents and still merges runner-API status. Against that thin win, adopting a
pre-1.0 (`v1alpha1`, breaking-changes-warned) cluster-side controller as a hard
runtime dependency of every session raises the ops and upgrade surface for
external users who already must install the agent-sandbox controller.

**Instead, take the cheap 80%:** implement §6 `ownerReferences` (Secret+PVC →
Sandbox) directly in `CreateSession`. That closes the out-of-band-delete orphan
gap — the one concrete correctness problem kro would solve — with ~10 lines and
zero new dependency, and keeps the existing rollback semantics (including the
`secretPreexisted` re-create exception) intact.

**Revisit when:** kro reaches a stable (v1) API and/or the composition grows
materially (e.g. per-session NetworkPolicy, Service, multiple Secrets, External
Secret) such that the hand-rolled ordering/rollback becomes a real maintenance
burden. At that point the declarative graph earns its dependency.

## Sources

- kro docs — RGD overview, status/readiness, schema:
  <https://kro.run/docs/concepts/rgd/overview/>,
  <https://kro.run/docs/concepts/rgd/schema/>
- kro repo (version/maturity): <https://github.com/kubernetes-sigs/kro>
- CNCF blog, "Building platforms using kro for composition" (2025-12-15).

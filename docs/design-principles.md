# Design principles

Status: **durable reference** (adopted 2026-07-27). Three principles that
constrain what goes in the public SDK (`client/`, `client/cred`,
`client/models`, `tui/`) and how the internals are shaped to support it. They
originated as a maintainer note in `TODO.md` and were pinned down in
conversation on 2026-07-27; the decisions recorded here are the resolved form.

Conformance findings against these principles live in
[`review-principles-2026-07-27.md`](review-principles-2026-07-27.md);
actionable gaps are tracked in `TODO.md` §8.

---

## Why these three

The project has one external-facing promise: **an outside consumer can build
their own thing on top of this** — their own CLI, their own dashboard, their own
automation — without forking. Everything below is a specific way that promise
gets broken, stated so it can be checked mechanically at review time.

The CLI and dashboard are the first consumers of the public SDK, not a parallel
path. If the shipped app can do something an importer cannot, that is the bug.

---

## 1. Transport is pluggable; topology is not

**A consumer supplies *how to reach* things. They never supply *what the things
are*.**

### Everything rides the kube-api (decided 2026-07-27)

There is **one** transport seam, not two, because the pod-endpoint hop is not
an independent network path — it is a *subresource of the kube-api*. The
port-forward URL is built through the REST client's own request builder
(`internal/k8s/portforward.go:381-386`, `SubResource("portforward")`) and both
dialers are constructed from the same `*rest.Config`
(`spdy.RoundTripperFor(b.config)` `:324`, `NewSPDYOverWebsocketDialer(u, b.config)`
`:330`).

So a consumer configures **one** thing — the kubeconfig / `*rest.Config` — and
all four channels follow it: the runner's HTTP+SSE API, the pane WebSocket, the
in-pod sshd behind `Session.Shell`, and the Mutagen sync endpoint. They are all
tunneled *inside* that forward.

This means an access layer that proxies the API server — Teleport
(`tsh`/`tbot`), an API-server proxy, a bastion — **already works today** through
`WithKubeconfig` / `WithContext` / `WithRESTConfig`, with no seam to build. The
`127.0.0.1` addresses throughout `client/` are the *local ends of an
already-proxied tunnel*, not an assumption about network topology.

That is principle 3 paying a dividend: leaning on the k8s primitive is what made
the transport substitutable for free.

**Scope for now: kube-api only.** Reaching a pod *without* going through the
kube-api — Teleport application/TCP access straight to port 8787, an ingress, a
Tailscale sidecar — is a possible future option, not a current requirement. Two
preconditions are recorded so that work can't start by accident:

1. `Session.Shell` uses `ssh.InsecureIgnoreHostKey()` (`client/shell.go:163`),
   which is safe **only** because the hop is a loopback forward. Any bypass
   transport must make that policy transport-conditional in the same change, or
   it silently converts a safe default into an MITM-able one.
2. The bypass seam must cover all four channels. Mutagen is the one that gets
   forgotten, because its endpoint comes from a *generated ssh config file*
   (`internal/sync/ssh.go:120`), not from Go call sites.

**What is explicitly *not* in scope, ever.** The cluster model stays fixed:
agent-sandbox `Sandbox` + PVC + per-session Secret, one session per pod. A
consumer cannot swap in a Docker, Fly, or bare-SSH backend, and we do not owe
them that.

### `client.Backend` should still be externally implementable

With transport handled by the kubeconfig, this is no longer justified by
transport redirection. It is justified by **consumer testability**: someone
building on this SDK cannot write tests for their own app without faking the
cluster. We use exactly that seam for our own orchestration tests; an external
consumer deserves the same.

*Done 2026-07-27.* Every type in `client.Backend` is now exported from `client`
(`PortName`, `PortSpec`, `ForwardHandle`, `Forwards`, `ReaperOptions`), and
`sdktest/backend_test.go` implements the interface from the external module as
the standing proof. Two things about the shape are load-bearing and should not
be "simplified" back:

- `PortForward` returns a **name-keyed `Forwards` map**, not an ordered slice.
  An ordered slice pushes a correctness obligation onto the implementer that the
  compiler cannot check — return the handles in a different order and opencode
  traffic silently reaches the sshd port. Any future seam handing a consumer a
  *set* of things should be keyed, not indexed, for the same reason.
- A missing endpoint is an **explicit error**, never a fallback to some other
  handle.

### Applying it

- ✅ An option that accepts a kubeconfig, context, or `*rest.Config` — the
  transport seam.
- ✅ An exported type describing *an endpoint* (`PortSpec`, `ForwardHandle`).
- ❌ A method signature naming a type from `internal/…` — that makes the
  interface uninhabitable outside this module. This is the single most common
  way this principle gets broken.
- ❌ Reaching the cluster or a pod by any path that bypasses `*rest.Config` —
  a raw HTTP client, a shelled-out `kubectl`, a hand-built URL. That is what
  would break Teleport and every other proxying access layer.
- ⚠️ A security decision justified by the hop being loopback is *correct today*
  but must be listed as a precondition on any future bypass transport — see
  above.

---

## 2. The TUI is batteries-included *and* dismantlable

**A consumer gets a runnable dashboard out of the box, and can add, remove,
resize, or reorder any region of it without forking.**

Both halves are load-bearing. Publishing only leaf widgets (buttons, gauges,
spinners) is not batteries-included — it hands someone a parts bin and a
month of work. Publishing only a fixed app is not customizable — it hands
someone an ultimatum.

So the public TUI surface owes three layers:

1. **Primitives** — `tui/kit`, `tui/anim`, `tui/theme`, `tui/list`,
   `tui/terminal`. Styling and low-level widgets. *(Exists.)*
2. **Panels** — the session list, the detail pane, the activity feed, the
   agent-pane viewer, the create overlay, each an independently constructible
   and independently renderable component. *(Does not exist publicly.)*
3. **An assembled app + a layout API** — a default dashboard a consumer can run
   in one call, whose region composition is data, not a hardcoded render
   function. *(Does not exist publicly.)*

### Extraction and polish are one pass, not two

*Decided 2026-07-27.* When the panels are extracted, they get made **visually
good at the same time** — the restructuring pass is also the polish pass. Two
reasons this is a principle and not a scheduling preference:

1. The alternative rewrites the same code twice. The premium-feel work
   (`docs/tui-premium-plan.md` §A/§F) targets exactly the overlay and
   compositing layers that panelization dissolves.
2. Polish done *before* extraction has no reason to export its abstractions,
   so it produces another internal-only layer — the exact thing this principle
   exists to stop.

This also gives visual quality a checkable form it otherwise lacks. "Feels
premium" is not testable; **"an external consumer can rebuild this dashboard
minus a panel, plus one of their own, and it still looks right at 80x24,
100x30, and 140x40 in both themes"** is.

### Applying it

- ✅ A panel that takes its data through an interface and renders into a
  caller-chosen width/height.
- ✅ Layout expressed as a value the caller can rebuild (regions, sizes,
  ordering) rather than a `render()` that concatenates strings in a fixed order.
- ❌ A new dashboard feature wired straight into the monolithic renderer with
  no seam. Every such addition raises the cost of principle 2.
- ❌ A public `tui/` package importing `internal/…`. (Currently clean — keep it
  that way; it is checked by the fact that `tui/` compiles from `sdktest/`.)
- ⚠️ **Process-global mutable state is a customization ceiling.** `tui/theme`
  and `tui/kit` hold one active palette per *process*. That is a deliberate
  trade (it makes `theme.Charple` a plain read at render time), but it means two
  differently-themed consumers cannot coexist, and the single-goroutine
  contract must be stated in the package doc, not just in an internal comment.
  *Settled for `tui/theme` on 2026-07-27:* the contract is **stated, not
  enforced** — synchronizing ~40 exported color **vars** would mean turning the
  whole public token vocabulary into accessor funcs and would still not let two
  themed consumers coexist. The ceiling is documented, and the escape hatch for
  an embedder is named: `Theme` is an inert value, so `ByName` /
  `DefaultForBackground` let a library derive styles without calling
  `ApplyTheme` and clobbering its host. A stated contract has no signature to
  pin, so `sdktest` pins the escape hatch instead. Any *new* process-global in
  `tui/` owes the same treatment: state it, and pin whatever lets a consumer
  opt out.

---

## 3. Kubernetes is an implementation detail, not an interface

**Lean on agent-sandbox and Kubernetes primitives wherever they do the job.
Do not make a consumer learn them.**

The test is *what kind* of Kubernetes concept appears in a public signature:

**Resource objects — never exposed.** `Sandbox`, `PVC`, `Job`, `Secret`,
`Pod`, `resource.Quantity`, pod phases, label selectors, owner references. A
consumer must never construct, read, or reason about one. The normalized model
(`Spec`, `State`, `Event`, `Status`) is the entire vocabulary.

**Deployment knobs — legitimately exposed** *(decided 2026-07-27)*. Namespace,
runner/reaper image, image pull policy, `Resources` as plain strings
(`"500m"`, `"4Gi"`), kubeconfig path/context, and `*rest.Config`. These are
operator configuration: someone deploying this genuinely has a namespace and an
image, and abstracting them behind a neutral type would buy nothing but
indirection. `*rest.Config` stays for the same reason principle 1 blesses it —
it *is* the kube-api transport seam.

The line: a consumer configures **where and how** the system is deployed; they
never touch **what** it deploys.

### Applying it

- ✅ `Resources{CPURequest: "500m"}` — a string a human types, not a
  `resource.Quantity`.
- ✅ `Status` as an abstracted lifecycle vocabulary rather than pod phases.
- ❌ Any `k8s.io/api/...` or `sigs.k8s.io/agent-sandbox/...` type in a public
  signature.
- ❌ Any `internal/k8s` type in a public signature — this violates principle 1
  and 3 simultaneously, which is why it is the highest-priority class of fix.

---

## Reviewing against these

When a change adds or alters anything under `client/` or `tui/`, check:

1. Does any new exported signature name an `internal/…` type? → blocks
   principles 1 and 3.
2. Does any new code path hardcode a host, port, or scheme that a custom
   transport would need to control? → blocks principle 1.
3. Does a security choice depend on the transport being loopback? → must be
   made transport-conditional.
4. Does a new dashboard feature land in the monolithic renderer with no
   extractable seam? → raises the cost of principle 2.
5. Does a Kubernetes *resource object* appear anywhere a consumer can see it?
   → blocks principle 3. (Deployment knobs are fine — see above.)

`sdktest/` is the enforcement point: it is a separate module that imports
`client`/`client/cred`/`tui/*` as a genuine external consumer, so anything it
cannot name or construct is exactly what an external consumer cannot name or
construct. **When a principle-relevant seam lands, pin it there in the same
change** — if the pin can't be written, the seam isn't real.

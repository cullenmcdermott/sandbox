# Principle-conformance review — 2026-07-27

Status: **complete** (point-in-time). A whole-repo review of the public surface
against [`design-principles.md`](design-principles.md), run the same day the
principles were pinned down. Every finding below was verified against source at
the cited `file:line`; nothing here is inferred from docs.

Actionable items are carried into `TODO.md` §8, which hosts both the client-API
and the `tui/*` public-surface backlog. IDs `[P1-#]` / `[P2-#]` / `[P3-#]` are
referenced from there.

**Headline.** Principle 3 is essentially met — the normalized model is
genuinely Kubernetes-free, and the exceptions are the ones we decided to keep.
Principle 1 is *mostly met in practice*: everything rides the kube-api, so a
proxying access layer already works today; what remains is that
`client.Backend` is uninhabitable outside this module and carries an unwritten
positional invariant. Principle 2 is the outlier: it is not partially met, it
is unstarted above the widget layer.

> **Revised 2026-07-27, after review.** This document originally treated the
> pod-endpoint hop as a second, unsatisfied transport seam. That was wrong in
> its practical effect: the port-forward is a *subresource of the kube-api*
> (`internal/k8s/portforward.go:381-386`) dialed from the same `*rest.Config`
> (`:324,:330`), so Teleport and every other API-server-proxying access layer
> already work through `WithKubeconfig`/`WithRESTConfig` with no seam to build.
> The maintainer has scoped principle 1 to **kube-api only** for now. Findings
> `[P1-2]`, `[P1-3]`, `[P1-5]`, `[P1-8]` are therefore **not defects** — they
> are reclassified below as preconditions on a possible future bypass
> transport. `[P1-4]` is reclassified from urgent to a recorded tripwire.
> `[P1-1]` and `[P1-6]` survive as real work, on different justifications than
> originally given.

---

## Principle 1 — transport pluggable, topology fixed

### `[P1-0]` **Positive — the transport is already substitutable, via the kube-api**

The port-forward is not a separate network path. `restURLForPodPortForward`
builds its URL through the REST client's request builder
(`internal/k8s/portforward.go:381-386`), and both dialers come from the same
`*rest.Config` — `spdy.RoundTripperFor(b.config)` (`:324`) and
`portforward.NewSPDYOverWebsocketDialer(u, b.config)` (`:330`), behind a
fallback dialer (`:335`, `shouldFallbackToSPDY` `:314`). Nothing anywhere
shells out to `kubectl`.

Consequently **all four channels inherit the kubeconfig's server, TLS, auth,
and proxy settings**: runner HTTP+SSE, pane WS, sshd, and Mutagen sync are all
tunneled inside that forward. An access layer that proxies the API server —
Teleport via `tsh`/`tbot`, a bastion, an API-server proxy — works today with no
code change. The codebase has already met this class of environment: the
comment at `:377-379` explains the request builder is used precisely so a
kubeconfig server URL with a trailing slash, "as some API-server proxies
emit", joins correctly.

Two operational notes for that setup, neither a code defect:

- **Interactive credential plugins are fine at startup; mid-session refresh is
  the case to handle.** Kubeconfig loading is
  `clientcmd.NewNonInteractiveDeferredLoadingClientConfig`
  (`internal/k8s/options.go:63`), which supports `exec:` plugins, and client-go
  runs them lazily at first request rather than at construction. In the CLI's
  flow the cluster work — create, port-forward, health check — all completes
  *before* `tea.NewProgram` (`internal/tui/dashboard/app.go:376,392`), so a
  `tsh` login prompt at startup has the real terminal and behaves normally.
  `tsh` is a human-at-a-terminal tool, so that is the expected and supported
  path.

  The residual is **credential expiry while the dashboard is running**, which
  is reachable: the dashboard makes live cluster calls for its whole lifetime —
  `List`/`Watch`/`Suspend`/`Resume`/`Destroy` (`actions.go:24-30`) plus
  background observer port-forwards and forward re-resolution — and a `tsh`
  cert TTL is easily shorter than a left-open session. There, the plugin fires
  underneath an alt-screen raw-mode program.

  This is ours to solve, not a reason to avoid `tsh`, and **the pattern already
  exists in this codebase**: the subscription login hands the terminal to a
  child process via `tea.Exec` (`internal/tui/dashboard/account_picker.go:301-310`,
  wired at `app.go:477`). The same handover applied to a credential-refresh
  failure would close it. (tbot's background renewal sidesteps the situation
  entirely, but it is one option, not a prerequisite.)
- If WS-tunneled forwarding misbehaves against a proxy, the SPDY fallback
  (`:314-316,:335`) is the first place to look.

*(Teleport-side behavior here is reasoned from how its Kubernetes access works;
the transport mechanism above is verified against source, but this was not
tested against a live Teleport cluster.)*

### `[P1-1]` **Blocker — `client.Backend` is uninhabitable outside this module, in three places, not one**

`client/client.go:109` and `:114`:

```go
PortForward(ctx context.Context, ref Ref, ports []session.PortSpec) ([]session.ForwardHandle, error)
EnsureReaper(ctx context.Context, ref Ref, opts k8s.ReaperOptions) error
```

An external module can name neither `session.PortSpec`, `session.ForwardHandle`,
nor `k8s.ReaperOptions`, so the interface cannot be implemented.

**Justification revised:** originally this was framed as blocking transport
redirection. Under kube-api-only that is no longer the reason — `[P1-0]` shows
transport is already handled by the kubeconfig. It survives because of
**consumer testability**: an external consumer building on this SDK cannot fake
the cluster to test their own application. We rely on exactly this seam for our
own orchestration tests (`client/orchestration_test.go:126,145`); an importer
gets no equivalent. That is a live gap regardless of transport, and the fix is
cheap.

The existing self-documentation **undercounts this**: both the interface doc
comment (`client/client.go:84-87`) and the `sdktest` header
(`sdktest/surface_test.go:15-18`) name only `k8s.ReaperOptions`. Anyone reading
those would budget a one-type fix and then hit two more.

*Fix:* `PortSpec`/`ForwardHandle` are plain declarations
(`internal/session/types.go:507-514`) and need only be added to the existing
alias block at `client/client.go:31-50` — near-zero cost. `ReaperOptions` needs
a real public type. Then delete the caveat from both doc sites and add a
compile-time pin that an external type satisfies `client.Backend`.

### `[P1-2]` `[P1-3]` `[P1-5]` `[P1-8]` — **Not defects. Preconditions on a future bypass transport.**

*Reclassified 2026-07-27 by the kube-api-only scope decision.* These describe
code that is **correct as written** for the supported topology: every one of
them sits on the local end of a tunnel that is already proxied by whatever the
kubeconfig points at (`[P1-0]`). They are recorded here — not deleted — as the
complete work-list for the day someone wants to reach a pod *without* the
kube-api (Teleport app/TCP access, ingress, Tailscale sidecar).

- **`[P1-2]` no transport injection point.** `runner.New(baseURL, token)`
  (`internal/runner/client.go:52`) takes no `*http.Client`/RoundTripper and
  builds its own (`:56`); the streaming path constructs a **second, separate**
  client inline (`:264`, `streamClient := &http.Client{Timeout: 0}`). Anyone
  adding a transport must thread it to **both**, or SSE silently bypasses it.
  This is the non-obvious one.
- **`[P1-3]` loopback assembled at four sites.** `client/client.go:879`
  (DialRunner), `client/session.go:417` (Connect), `:548` (opencode URL, which
  is *handed to consumers* via `ExternalCreds.URL`), `client/shell.go:83`
  (`SSHTarget.Host`, documented "always 127.0.0.1" at `:42`).
- **`[P1-5]` Mutagen's endpoint is a generated file, not Go.**
  `internal/sync/ssh.go:120` writes `HostName 127.0.0.1` into the ssh config
  (assumption stated at `:116`). Easiest of the four to overlook for exactly
  that reason.
- **`[P1-8]` the pane WS URL is a scheme swap.**
  `wsBase := "ws" + strings.TrimPrefix(c.baseURL, "http")`
  (`internal/runner/pane.go:86`). Fine for http/https; breaks for any endpoint
  that isn't the same authority and path.

### `[P1-4]` Tripwire — the SSH host-key policy is safe *because* the hop is loopback

`client/shell.go:163`:

```go
HostKeyCallback: ssh.InsecureIgnoreHostKey(), // localhost forward; ephemeral pod host key (see SSHTarget)
```

*Reclassified 2026-07-27: not urgent, because under kube-api-only the
justification holds — the hop really is loopback.* Recorded as a **precondition**
on the work above: any bypass transport must make this policy
transport-conditional **in the same change**, or a consumer routing
`Session.Shell` over a real network silently inherits an MITM-able connection
with no warning and no opt-in. It is the one item in this section that turns a
feature into a vulnerability if the ordering slips, which is why it is called
out separately rather than folded into the list.

### `[P1-6]` The `Backend` contract has an unwritten positional invariant

`Connect` indexes port-forward handles **by position** — `handles[0]` for the
runner (`client/session.go:417`), `handles[2]` for opencode (`:548`) — relying
on an ordering documented only on the internal helper that happens to produce
them ("The returned handles are ordered HTTP, SSH, opencode",
`internal/k8s/portforward.go:438`).

Nothing in the `Backend` interface communicates this. An external implementer
would satisfy the compiler, return handles in a different order, and get a
silent misconnection — opencode traffic to the sshd port. Exporting the types
from `[P1-1]` without fixing this ships a contract that is *type-safe and
semantically undefined*.

*Fix direction:* the seam should return **named** endpoints (a map or a struct
keyed by channel), not a positional slice. This is the one place where closing
principle 1 should change the shape of the interface rather than just its
visibility.

### `[P1-7]` Even the default forward specs are unnameable

`k8s.ForwardSpecsRunnerOnly` / `ForwardSpecs` / `ForwardSpecsWithOpencode` are
called from *public* code paths (`client/client.go:865`,
`client/session.go:400-404`) but live in `internal/k8s`. A consumer wrapping our
backend cannot express "the standard forwards" and would have to hand-rebuild
them — re-deriving the invariant from `[P1-6]` by hand.

*(`[P1-8]` is folded into the reclassified block above.)*

### `[P1-9]` Minor — `ForwardSpecsWithCodex` is dead code

Defined at `internal/k8s/portforward.go:447`, referenced by no live caller;
`client/session.go:386` explains why (codex phase 2 not wired). Known and
intentional — noted only so the transport rework doesn't preserve it by
accident.

---

## Principle 2 — batteries-included *and* dismantlable

### `[P2-1]` **Blocker — there is no public dashboard at all**

Everything above the leaf-widget layer is `internal/tui/dashboard` (~90 files).
The public `tui/` tree is eight packages of primitives — `kit` (badges,
buttons, cards, gauges), `anim`, `theme`, `list`, `picker`, `composer`,
`chrome`, `terminal`. A consumer today gets a parts bin, not batteries.

### `[P2-2]` **Blocker — layout is a hardcoded string-builder with nothing to hook**

`internal/tui/dashboard/model_render.go:91` — `func (m *Model) render() string`,
in a 729-line file. Regions are produced and concatenated in a fixed order by
methods on `*Model`. There is no region, panel, or box abstraction anywhere
(`column.go` is 125 lines of column-width math, not a layout engine).

"Add or remove boxes, resize them" has no seam to attach to. **This is the
central obstacle to principle 2** — not the internal/public boundary, which is
comparatively mechanical (see `[P2-5]`).

### `[P2-3]` No panel-level components exist, even internally

The would-be panels are all `*Model` methods, not constructible values:
`renderSessionRow` (`:278`), `renderDetailLines` (`:395`), `renderConfirm`
(`:594`), `renderConvertModal` (`:615`), `renderHelp` (`:661`), plus
`feed.go`, `dirpicker.go`, `backend_picker.go`, `account_picker.go`. Promotion
requires extracting each into something that takes its data through an
interface and renders at a caller-chosen size — the same refactor `[P2-2]`
needs, which is why they should be done as one piece of work.

### `[P2-4]` `[L6]` remains open — pane transport is public, pane *viewer* is not

`Session.AttachPane` / `PaneStream` are public and pinned
(`sdktest/surface_test.go:108-128`), but the vt emulator, key/paste/mouse
encoding, and resize handling live in
`internal/tui/dashboard/external_pane.go` + `key_encode.go`. `tui/terminal` is
caps/kitty/OSC helpers, not a screen widget. External consumers get raw bytes
and no way to display them.

Under the resolved principle 2 this is no longer an open question — a pane
viewer is one of the panels `[P2-3]` must produce.

### `[P2-5]` **Positive** — the promotion is more mechanical than it looks

The dashboard's entry points are already seam-shaped:
`Run(backend, connector, creator, opts ...RunOptions)` and `RunAttached`
(`app.go:373,387`), with `Connector`, `Creator`, `SyncProber`, `SyncReaper`,
`TitleStore`, `SnapshotStore` all function types or interfaces
(`connector.go:89,112,214`, `app.go:281-306`). Critically, the types crossing
those seams — `session.Ref`, `session.ID`, `session.State` — are **already
aliased in `client`**, so re-typing them to `client.*` is identity, not
conversion.

The internal/public boundary is therefore not the hard part. `[P2-2]` is.

### `[P2-6]` **Positive** — `tui/` is clean of `internal/` imports

Verified: no file under `tui/` imports `github.com/cullenmcdermott/sandbox/internal`.

### `[P2-7]` The concurrency hardening is inconsistent, and the contract is unstated

`tui/kit` deliberately moved its palette behind an `atomic.Pointer` so that
"multiple tea.Programs sharing this process never race"
(`tui/kit/palette.go:4,35`).

But its **upstream** did not. `theme.ApplyTheme` (`tui/theme/theme.go:199`)
writes ~40 plain package globals (`Charple`, `TextBody`, … `theme.go:107-158`),
plus `activeTheme` (`:161`), `changeHooks` (`:166`), and `themeEpoch` (`:184`),
with no synchronization — justified by an internal comment on `themeEpoch`:
"Single-threaded with the UI render loop, so no atomic is needed". Since
`ApplyTheme` is also what calls `kit.SetANSITable` and
`kit.SetComponentColors`, **kit's hardening is defeated one layer above it**:
an off-UI-goroutine theme swap still races.

Two problems for a package advertised as "reusable across TUI applications"
(`theme.go:7-11`):

1. The single-goroutine contract appears nowhere in the package doc or on any
   exported function — only in a comment on an unexported variable.
2. `OnChange`'s returned unsubscribe writes `changeHooks[idx] = nil`
   (`theme.go:176`) with no guard, so unsubscribing off the UI goroutine races
   the hook slice.

*Fix:* state the contract in the package doc and on `ApplyTheme`/`Register`/
`OnChange`, or finish the hardening to match `kit`. Either is defensible;
the current split is not.

### `[P2-8]` One palette per process is a customization ceiling

Following from `[P2-7]`: `tui/theme` and `tui/kit` hold exactly one active
theme per process. Two consumers with different themes — or our dashboard
embedded inside a differently-themed host app — cannot coexist. This is a
reasonable trade (it is what lets `theme.Charple` be a plain variable read at
render time), but under principle 2 it should be a **documented decision** with
its limit stated, not an undiscovered ceiling a consumer finds at integration
time.

---

## Principle 3 — Kubernetes as an implementation detail

### `[P3-1]` **Positive — the normalized model is genuinely Kubernetes-free**

Verified by scanning every exported signature and struct field in `client`:

- `Resources` carries plain strings (`"500m"`, `"4Gi"`), not
  `resource.Quantity` (`internal/session/types.go:310-319`). `resource.ParseQuantity`
  is used only for *internal validation* (`client/client.go:1220`), and
  `validation.IsValidLabelValue` likewise (`:1004,1033,1057`) — neither type
  reaches a signature.
- `Status` is an abstracted lifecycle vocabulary, explicitly separated from
  runner-reported `Activity` (D9, `types.go:401-410`) rather than mirroring pod
  phases.
- `Spec`, `State`, `Ref`, `Event`, `BootstrapFile` contain no Kubernetes types.
- **No `k8s.io/api/…`, `metav1`, `corev1`, or `sigs.k8s.io/agent-sandbox/…`
  type appears in any exported signature or field.**

This principle was being followed before it was written down.

### `[P3-2]` The only true violation is `[P1-1]`'s

`k8s.ReaperOptions` in `Backend.EnsureReaper` (`client/client.go:114`) is the
lone `internal/k8s` type on the public surface. Closing `[P1-1]` closes this.

### `[P3-3]` Accepted exceptions — now decided, not drift

`WithRESTConfig(*rest.Config)` (`client/client.go:209`) is the **only** k8s.io
type in an exported signature. Per the 2026-07-27 decision it stays: it is
operator configuration and simultaneously the kube-api transport seam principle
1 requires. Same for `WithKubeconfig`/`WithContext`/`WithNamespace`,
`RunnerImage`/`ReaperImage`, and `ImagePullPolicy`.

Recorded here so a future reviewer doesn't "fix" them.

---

## Suggested sequencing

*Revised for the kube-api-only scope.* Ordered by dependency, not by size.

1. **`[P1-1]` + `[P1-6]` + `[P1-7]` together.** Export the types *and* replace
   the positional handle slice with named endpoints in the same change —
   exporting alone ships a contract that is type-safe and semantically
   undefined. Add the external-implementer pin to `sdktest` (that pin is the
   proof), and correct the undercounted caveat at `client/client.go:84-87` and
   `sdktest/surface_test.go:15-18`. Small, self-contained, and the only
   principle-1 work the current scope calls for.
2. **`[P2-7]`.** State or finish the `tui/theme` concurrency contract.
   Independent of everything else; can be done any time.
3. **`[P2-2]` + `[P2-3]` + `[P2-4]`, merged with `TODO.md` §2e `[L5]`/A/F.**
   The layout/panel extraction — the large one, and the only thing standing
   between the project and principle 2. *Decided 2026-07-27: the premium-feel
   workstreams that touch the same code land in this pass, not before or
   after it, and the item carries a visual acceptance bar alongside the
   consumability one.* `[P2-5]` says the public-boundary half is cheap once
   this lands; budget the effort in the extraction and the visual pass.

**Not scheduled:** `[P1-2]`, `[P1-3]`, `[P1-5]`, `[P1-8]` are preconditions on
a future bypass transport, and `[P1-4]` is the security tripwire attached to
them. They become a work-list only if that option is ever taken up. `[P1-9]`
and `[P2-8]` fold into the work above rather than standing alone.

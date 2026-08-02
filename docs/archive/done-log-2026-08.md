# Done log — 2026-08

Detail for TODO.md items closed in August 2026. See
[`done-log-2026-07.md`](done-log-2026-07.md) for the previous month.

## 2026-08-02 — stale `:latest` on CLI-created sessions (§10 ops item)

**Closed with no code change in this repo.** The cause was never the CLI, and
the fix landed cluster-side.

**What the item alleged.** `client.DefaultRunnerImage` is a moving tag, so new
sessions could run a days-old runner — observed live 2026-08-01, a session
running the 2026-07-25 build while `:latest` had been the 2026-07-30 build for
two days. User-visible symptom: no 5h/weekly usage in the in-pane statusline,
because that code shipped in the newer image.

**Actual cause.** Spegel (the in-cluster P2P OCI mirror) advertised the
`resolve` capability, so containerd asked a *peer* to turn `:latest` into a
digest and peers answered from their own caches. The pull policy was never
implicated: `resolveImagePullPolicy` (`internal/k8s/backend.go:294`) already
maps a moving tag to `Always`, which is correct — `Always` re-resolves the tag,
but it can only be as fresh as whatever answers the resolve.

**Fix.** `spegel.resolveTags: false` in the homelab repo (`f3628f5`). Tags
resolve upstream at GHCR; layers still come from LAN peers, so the caching win
is kept. Confirmed at the source on all three big nodes — `capabilities =
['pull']`, no `'resolve'`, read out of `/etc/cri/conf.d/hosts/_default/hosts.toml`.

**Verification — the item's own acceptance line, run 2026-08-02.** A live
session created at 18:32Z resolved `sha256:499f33c4…`, matching GHCR's current
`:latest`. That alone is weak evidence (its node already held that digest), so
it was re-run as a discriminating test: `talos-297-4am`'s containerd still maps
`:latest` to the *older* `sha256:841a1960…`, so a Job pinned to that node with
exactly the create path's image ref and policy (`:latest` + `Always`) had every
opportunity to serve the stale digest.

```
GHCR :latest       = sha256:499f33c4…
node-local :latest = sha256:841a1960…   (stale mapping, still present)
probe pod resolved = sha256:499f33c4…   ✅ upstream won
```

**Fix direction not taken.** The item offered "resolve the tag to a digest
CLI-side at Create". Rejected: it buys nothing now that resolution is correct,
and costs a registry round-trip in the create hot path plus a new failure mode
(GHCR auth/network) on a step that currently needs neither. The resume path's
pin (`pinRunnerImageDigest`) stays as-is — it serves a different purpose,
holding a session on one image across its lifetime.

**Residual, deliberately accepted.** Correctness now rests on `Always` being
derived for moving tags. A caller who overrides `--image-pull-policy=IfNotPresent`
with a moving tag gets whatever that node's containerd last mapped — on
`talos-297-4am` today, a digest from five builds ago. That is user-chosen and
the override exists for side-loaded dev images.

Full plan, workstreams, and the six execution surprises:
`~/git/homelab/docs/ci-auth-and-runner-image-plan.md`.

## 2026-08-01 — pane viewport + mouse ownership ([L10], [L11])

Two maintainer reports from a live session, both traced to deliberate rules in
the pane that were doing the wrong thing. Neither was a rendering bug.

### [L10] Sticky scroll — the pane snapped to live on every child output

**Symptom:** impossible to scroll up while the agent is writing.

**Cause:** `apply()` (`internal/tui/dashboard/external_pane.go`) set
`scrollOffset = 0` before feeding *every* chunk, so each burst of output yanked
the view back to the live tail. Scrollback was therefore usable only while the
agent was quiet — exactly when nobody needs it. This shipped with [L7], whose
rule was "key / paste / **new output** snap back to live".

**Fix:** the rule had three triggers and only two of them are user intent. Key
and paste still snap (`snapToLive`) — the user is typing at the child and must
land on what it is painting. New output is the *agent's* activity and no longer
moves the viewport. Instead `holdScrollAnchor` samples `emu.ScrollbackLen()`
either side of the feed and grows the offset by the delta, so the lines under
the user's eye stay put.

**Why anchor-hold and not "freeze the offset":** freezing the offset drifts the
view upward one burst at a time, because the lines an offset points at move as
history grows. The test asserts held *content*, not a held number, and the
mutation run confirms a frozen offset fails it (`l1/l2/l3` → `l3/l4/l5`).

Scoping decisions, both test-pinned:
- **Live tail (offset 0) is untouched** — the common case keeps following
  output. A blanket "never move the view" would have frozen every pane.
- **Alt screen is inert** — `scrollBy` already refuses to build an offset
  there, so the hold must not invent one from the main screen's scrollback.
- **Full-ring eviction cannot be saved.** Parked at the top of a full
  `paneScrollbackLines` ring, history evicts as fast as it grows, the delta is
  0, and the anchored lines are genuinely gone. Documented on the helper.

**New surface:** with the anchor held, output arrives off-screen with no other
sign, so `pendingOutput` drives a "↑ N lines · new output below" status row.
Cleared on any return to live.

**Test pin deliberately reversed:** `TestExternalPaneOutputSnapsBack` asserted
the old behavior and is now `TestExternalPaneOutputHoldsScrollAnchor`, plus
`…LiveTailStillFollowsOutput` and `…AltScreenOutputDoesNotAnchor` for the two
scoping halves.

### [L11] Click-drag selection was impossible in the pane

**Symptom:** cannot drag-highlight to select/copy text inside the Claude Code
TUI.

**Cause:** `App.View()` set `MouseMode = CellMotion` on `ScreenExternal`
unconditionally. DECSET 1002 is a single switch covering click, release, wheel
AND drag, so claiming the mouse takes the terminal's own selection away.
Shift-drag was the only escape — undiscoverable, and several terminals bind it
otherwise.

**Why the obvious fix was rejected:** "only capture when the child enables
tracking" would restore selection for claude (which runs inline and enables
none), but the comment at that call site records the regression it causes — with
capture off, terminals translate alt-screen wheel ticks into Up/Down key presses
that fall through to the child and hijack its prompt history. That trades a
selection bug for a worse input bug.

**Also checked, and it closes the item's open question:** bubbletea v2.0.7
offers exactly `MouseModeNone`, `MouseModeCellMotion` and `MouseModeAllMotion`
— verified by reading the module source, not assumed. There is no button-only
mode that would keep the wheel while leaving drag to the terminal, so no
finer-grained answer exists.

**Fix:** an explicit toggle. `ctrl+] s` flips `selectionMode`; `mouseMode()`
returns `None` while it is on, and `App.View()` asks the pane rather than
hardcoding. The chord reuses the existing leader (a bare `s` is ordinary text
the child must keep). Selection mode also *drops* mouse events in
`handleMouse`, because the mode change reaches the terminal only on the next
render and a frame's worth of events can already be in flight.

**Accepted cost, documented rather than papered over:** while selection mode is
on, wheel ticks reach the child as Up/Down. Suppressing arrows would silently
eat legitimate ones and break handleKey's "keys always reach the child"
invariant. The mode is brief, user-initiated, and has a visible indicator.

**Discoverability:** the help surface already carried "shift+drag — select text
in the agent pane" (the old workaround); it now leads with `^] s` and keeps
shift+drag as the terminal bypass.

**Nil-pane behavior changed:** `ScreenExternal` with no pane no longer asserts
capture — nothing consumes the events, matching the defensive shape
`windowTitle`/`attachedSessionID` already use. Two existing tests
(`TestMouseCaptureOnlyOnScreensThatConsumeIt`,
`TestExternalScreenEnablesMouseCapture`) now attach a real pane; the nil case
is asserted separately.

### Verification

`go test ./internal/tui/dashboard/` green; `-race -count=2` green; full
`go test ./...` green; `gofmt`/`go vet` clean; `go mod tidy -diff` clean (the
[D0] `go.sum` trap avoided). Both fixes mutation-checked: restoring the L7
snap-back fails the anchor test, freezing the offset fails it differently (the
drift), and restoring unconditional `CellMotion` fails the selection test.

**Verified in-pod**, which the §0 item says is impossible: Go was absent as
[D0] records, so go1.26.5 was fetched to `/tmp/go` and `gcc`/`libc6-dev`
installed for `-race`. Still not reproducible policy — see the standing §0
decision item. Not verified live (a real drag, a real streaming response); that
needs the maintainer's terminal.

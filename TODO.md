# TODO — backlog

### inbox
* dashboard shows a session "ready" but the agent is waitin gon subagents. How can we make this clear to the user that its not idle?
* On the detail sidebar it looks like created and last active are the same time? Last active should tell me basically the last time we heard from the agent itself.
* Claude Code TUI seems "sticky" to the bottom of the transcript. Its impossible to scroll up while the agent is actively writing output


> **How to use this file (agents):** sections are numbered workstreams, ordered
> roughly bugs → strategy → perf → platform. Every item carries `file:line`
> pointers and a fix direction — enough to plan without re-discovery. Pick a
> section (or one bolded cluster), plan the cluster together where the intro
> says so, and when a batch lands: check it off, summarize in one line, move the
> detail to `docs/archive/done-log-2026-07.md` (convention). Provenance docs:
> [`docs/archive/review-2026-06-24.md`](docs/archive/review-2026-06-24.md) (deep review behind
> older items), the 2026-07-01 whole-system review (§1d/§8 intros), and the
> 2026-07-04 multi-agent TUI audit (§1c residuals, §2 — every bug adversarially
> re-verified against source). Done-work history:
> [`docs/archive/done-log-2026-06.md`](docs/archive/done-log-2026-06.md),
> [`done-log-2026-07.md`](docs/archive/done-log-2026-07.md).
>
> **2026-07-06 prune:** everything closed through the 2026-07-06 Fable review
> pass (27/28 approved as-shipped, one `catchingUp` fix; `just check` green,
> zero skipped gates) was removed from this file; residual "STILL OPEN" tails
> were promoted to standalone open items below. Detail lives in the done log.
>
> **2026-07-07 handoff review:** an 8-agent sweep (security ×2, perf ×2,
> test-coverage ×2, runner TS, Go client, event-model, docs, TUI-regression)
> added verified findings across §1d/§1f/§2b/§2c/§4/§10 — all backed by
> [`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) (bracketed ids like
> `[A1]`/`[D1]`/`[H1]` point into it). The 2026-07-07 sweep is nearly
> burned down — remaining: §1f the A1 `/proc` residual + the hook-shape
> forward-compat item + [A3] SECURITY.md; §2b [D7-D12]; §4 [E7-E10] + the
> older measure-first items; §10 [F3-F7].
> *(Batches 1-6, 2026-07-08→09: [A1] [F1] [F2] [C2] · [D1] [D2] [D4]
> [B1-B4] · [C1] [H1] [H2/H3] · [E1] [E2] [E3] [E5] [E6] · [A2] [B5-B9]
> [D3] [D5] [E4] · [H4-H7] [D6] [C3-C11] — all in the done log.)*
>
> **2026-07-18 audit:** a 16-subsystem audit produced 47 verified findings
> ([`docs/audit-2026-07-18.md`](docs/audit-2026-07-18.md), ids `[V1]`-`[V47]`)
> — ALL burned down same day across seven commits (per-finding verdicts in the
> doc; detail in the done log). Residuals promoted into sections: §2b
> AskUserQuestion answer flow ([V15]), §5 dashboard paused-orphan reap
> ([V35]). STILL UNCOVERED: 6 auditors (tui-public, security, docs, tests-ci,
> tui-render, tui-input) died on a spend limit — re-running them is a
> maintainer call.
>
> **2026-07-20 pane-first review + first live validation:** three Fable
> reviewers (perf/security/onboarding) swept the merged claude-pane-first
> tree and the first live sessions added verified findings — all in
> [`docs/review-2026-07-20.md`](docs/review-2026-07-20.md) (ids `[P#]`
> `[S#]` `[O#]` `[L#]`), triaged below into §1h/§1f/§2e/§4/§8/§10. Gate 1.4
> closed the same evening (main merged 26c55f9, runner+reaper images
> published). Same-day feasibility study:
> [`docs/go-runner-rewrite-investigation.md`](docs/go-runner-rewrite-investigation.md)
> (gated on 2.5/8.2 + soak; §10 carries the watch item). The opencode
> credential failure spawned
> `openspec/changes/opencode-multi-provider-auth/` (see §7a).
>
> **2026-07-24 observability review:** a logs/traces/metrics audit of the
> whole system landed ten verified findings in §10 (ids `[T1]`-`[T10]`),
> backed by [`docs/observability-design.md`](docs/observability-design.md)
> — which also carries a staged proposal for on-disk OTLP/JSON plus a
> disposable local LGTM stack (draft; four open decisions await maintainer
> sign-off). The §10 `[~]` "Observability first cut" item is the direct
> ancestor: `[T4]` and `[T9]` are its "STILL OPEN" tail with pointers.
>
> **Agent-ready map — rewritten 2026-07-27** (the previous version pointed at
> work that had already shipped; each claim below was re-verified against the
> tree on that date).
>
> **Pick from here — open, unblocked, no decision needed:**
> - §8 `[P1-1]`+`[P1-6]` — make `client.Backend` externally implementable
>   (two type aliases + a public `ReaperOptions` + named endpoints). Small,
>   self-contained, sdktest-pinned.
> - §8 `[P1-0a]` — `tea.Exec` terminal handover for mid-session credential
>   refresh. Precedent already in-tree.
> - §8 `[P2-7]` — state or finish the `tui/theme` concurrency contract.
> - §7c table-driven CLI smoke; §10 `[T4]`/`[T9]` observability tails.
>
> **The one large workstream — plan it whole before writing code:**
> - §8 **the panel workstream** (`[P2-2]`/`[P2-3]`/`[P2-4]` merged with §2e
>   `[L5]`/A/F, decided 2026-07-27). Extraction for external consumability
>   *and* the visual polish pass happen together, with a two-part acceptance
>   bar. §2e A/F/`[L5]` are folded in and must not be started separately —
>   they rewrite the same code. §2e A4/C/D/E remain independent and are free
>   to pick up any time.
>
> **Blocked on a maintainer decision — do not start:**
> - §7b package-manager rollout — direction is settled (Flox/Nix preferred
>   everywhere); what is gated is the Depot spike + kind conformance. The
>   FloxHub-publish half can land independently.
> - §10 `[T10]` local OTel stack — four open decisions in
>   `docs/observability-design.md`.
> - §0 MCP / laptop-endpoint exposure — needs a scope call on reverse
>   tunnels before any design.
>
> **Cannot be closed without the live cluster or the maintainer's eyeball:**
> §0b runner image rebuild (gates ctx% part B *and* the compaction marker
> reaching live), `[L2]` pane-replay repro, claude-pane-first gates 8.2/8.3,
> §5 Spegel, §6 codex live spike, §7c verify sweeps, `[O14]` hero GIF,
> `[S1]` FQDN hubble-verify, `[L7]` trackpad + T10 overlay feel, live reaper
> deploy (`[O5]` manifests), §0 workspace-guide two-target validation.
>
> **Rules of the road:** (1) run the tests named in the item, not the full
> gate, per change; `just check` once per batch (command-sandbox caveats in
> CLAUDE.md); (2) line numbers drift — anchor by symbol name and re-`rg`
> before editing; (3) never touch `openspec/` structure, `*.gen.*` files, or
> memory; (4) anything exported from `client/` or `tui/` is governed by
> [`docs/design-principles.md`](docs/design-principles.md). Every open item
> carries pointers + a fix direction + a verification line; if one doesn't,
> that's a bug in this file.

## 0) Inbox — human notes, needs triage

Raw maintainer notes. Triage = either promote into a numbered section with
pointers, or answer inline and archive. (Resolved investigations moved to the
done log.)

- [ ] **The idle reaper never fires while the dashboard is running: detach
  (ctrl+]) leaves the pane WebSocket attached forever.** Diagnosed live
  2026-07-27 on `claude-pane-df80e6-031396fb` — agent's last real work was
  16:09:08Z, pod still Running at 18:10Z (2h), reaper Job healthy and polling
  the whole time with zero log lines past `watching;`.
  **Chain:** `handleLeaderTimeout` (`internal/tui/dashboard/app.go:915-922`) and
  `leaderJump` (`app.go:1015-1022`) resolve a detach by setting
  `a.screen = ScreenDashboard` and nothing else — `a.external.close()` is only
  reached when a *different* session's pane comes up (`app.go:798`), the child
  exits (`app.go:852`), or the process dies. `leaderJump`'s own comment states
  this ("the child keeps running") so it is deliberate, for fast reattach. But
  the pane socket staying open keeps `sup.attached()` true
  (`runner/src/claude-pane.ts:427`), which is wired straight into
  `setExternalActivityProbe` (`claude-pane.ts:575`); `idleStatus()` calls the
  probe and stamps `setExternalActivity()` on *every* poll
  (`runner/src/session.ts:515`), so `isDetached()` (`session.ts:450-454`) is
  never true, `recomputeIdle()` never sets `idleSince`, and the reaper's
  `evaluateIdle` returns early forever (`internal/cli/reap.go:169-171`).
  Net effect: one attach pins the pod against the reaper for the entire life of
  the dashboard process. RV6 solved this for passive SSE list streams; the pane
  WS is the same hole reopened on a different transport.
  **Proof (both directions):** killing the dashboard at ~18:08:43Z produced
  `idleSince=2026-07-27T18:10:13.516Z` — exactly `EXTERNAL_ACTIVE_WINDOW_MS`
  (90s, `session.ts:84`) after the socket closed; the reaper then suspended the
  session at ~18:25:13Z (idleSince + 15m) and TTL-cleaned its Job. The reaper
  was never broken — it was never told the session was idle.
  **READ THIS BEFORE PICKING A FIX — the current behavior is test-pinned.**
  `TestLeaderTimeoutDetaches` (`internal/tui/dashboard/leader_wiring_test.go:74-76`)
  asserts `app.external != nil` after a detach with the message *"timeout detach
  tore the pane down; it must only minimize"*. Minimize-don't-tear-down is a
  deliberate, defended invariant, not an oversight. So:
  - **(b) preferred — keep the socket, add a client→runner "backgrounded"
    control frame** that makes `attached()` report false for idle purposes while
    the transport stays warm. Preserves the invariant and instant reattach;
    `parsePaneControl` (`runner/src/server.ts:235`) is already the control-frame
    seam and already has tests (`runner/test/pane-control-bounds.test.ts`).
  - **(a) close the socket on detach** is smaller but *fails the test above* —
    taking it means deliberately reversing that invariant and rewriting the test,
    and paying a reattach round-trip each time. Don't do this by accident.
  **Coverage gap that let this ship:** `runner/test/pane-output-idle.test.ts` has
  exactly two tests, both about `notePaneOutput` (the PTY-output window); neither
  touches `externalActivityProbe`/`attached()` — the path that actually fired.
  The dashboard side is worse than uncovered: it pins the wrong half.
  **Cross-refs:** [L8] (§1h, "reaper eligibility unchanged — still
  isDetached-gated") treats isDetached-gating as the sound backstop; this item is
  the proof it isn't while any pane is attached. Also `SECURITY.md:269-271` —
  see the correction item below.
  **Verification:** `cd runner && npm test` (extend `pane-output-idle.test.ts`
  with "backgrounded pane ⇒ `idleSince` advances"); `go test
  ./internal/tui/dashboard/ ./internal/runner/ ./internal/cli/` (unsandboxed —
  `internal/runner` binds httptest ports). Live: attach, detach, then poll
  `/sessions/:id/idle` on the pod and confirm `idleSince` advances without
  reattaching.

- [ ] **Pane WebSocket has no keepalive and no reconnect: after laptop sleep the
  pane freezes silently while the port-forward under it self-heals.** Maintainer
  report 2026-07-27, mechanism traced same day. Same seam as the reaper item
  above — read them together.
  1. **No keepalive, either direction.** The only ping machinery is
     `startRTTProbe`, gated on `paneTraceEnabled()` (`internal/runner/pane.go:119`)
     — i.e. only under `SANDBOX_TRACE` — and even armed it is pure measurement: a
     missing pong records no sample and fails nothing
     (`internal/runner/pane_rtt.go:130-173`). Server side, `ws` auto-pongs but
     never pings (`runner/src/server.ts:203`, no ping interval on the
     `WebSocketServer`).
  2. **No read deadline.** `PaneStream.Read` blocks in `conn.NextReader()` with no
     `SetReadDeadline` (`pane.go:142`). A peer that vanishes without a FIN — what
     sleep does — leaves that read blocked forever. The pane just freezes: no
     bytes, no error, no `externalPaneFinishedMsg`, so nothing reaches
     `handleExternalPaneFinished`. It doesn't fail to reconnect; it never learns
     there is anything to reconnect.
  3. **No reconnect path even if it did learn.** `external_pane.go:320-332` treats
     every read error as terminal — the comment there lists "network drop"
     alongside child exit — and `handleExternalPaneFinished` (`app.go:845`) closes
     the pane and drops to the dashboard. Reattach is manual.
  4. **The asymmetry is the actual bug.** The port-forward underneath *does*
     recover: `runForward` reconnects indefinitely with capped backoff and
     preserves the local port (`internal/k8s/portforward.go:197-217`). On wake the
     tunnel is back and the pane sitting on it is still dead.
  5. **Suspected reaper interaction (mechanism traced, NOT yet observed —
     confirm before fixing on this basis).** A cleanly-killed dashboard releases
     the session 90s later, as measured. A slept laptop may not: the runner's
     `socket !== null` (`claude-pane.ts:427`) stays true until the pod-side TCP
     actually tears down, and with no keepalive nothing forces that promptly. If
     kubelet does not reap the proxied connection, a session you slept on and
     never returned to stays pinned against the reaper indefinitely. Mitigating
     factor: single-attacher preemption (4001) means the *next* attach kicks the
     stale socket. To confirm, sleep with a pane attached, wake, and check
     `attached()`/`/idle` on the pod without reattaching.
  **Fix direction:** add a server-side ping interval + client-side pong-driven
  read deadline so both ends detect a dead peer within ~30s. That alone converts
  the silent freeze into a clean stream error and (5) into a bounded release.
  Then decide whether the pane auto-redials on transport error — it should, since
  the forward below it already does, and the runner's 256 KiB scrollback ring
  makes a redial cheap and near-seamless. Distinguish transport death (redial)
  from the terminal close codes 4001/4002 (don't).
  **Do this WITH the §4 slow-link item, not before it.** That item
  (`perMessageDeflate` + swapping `DefaultDialer` for a configured
  `websocket.Dialer`) edits the exact two call sites a keepalive touches —
  `runner/src/server.ts:203` `new WebSocketServer(...)` and
  `internal/runner/pane.go:101` `DefaultDialer.DialContext`. Two uncoordinated
  passes over the same lines is how the wrap-width mess happened. Note the
  keepalive is NOT gated on RTT numbers the way the compression half is: it is a
  correctness fix, so it can lead.
  **Reuse, don't rebuild:** `startRTTProbe` (`pane_rtt.go:130`) already does
  WriteControl pings on a ticker with a pong handler. A keepalive is that loop
  with (i) the `paneTraceEnabled()` gate removed and (ii) a pong-driven
  `SetReadDeadline`. Keep the trace summary as-is; don't fork a second pinger.
  **Verification:** `go test ./internal/runner/` (unsandboxed) with a new test
  pinning "no pong within N ⇒ Read returns an error" — `panePingInterval` is
  already a package var precisely so tests can shorten it; `cd runner && npm
  test`. Live: attach a pane, sleep the laptop 5+ min, wake, and confirm the pane
  either recovers or reports a clean error rather than sitting frozen.

- [ ] **`SECURITY.md:269-271` overstates the "bounded" idle-reaper stall — a
  durable reference is wrong.** Found while diagnosing the two pane items above
  (2026-07-27). The text says a malicious in-pod agent holding a synthetic
  'busy' is *"bounded: once observer events go quiet, the staleness release
  `SYNTHETIC_BUSY_STALE_MS` frees the session for reaping after 5 minutes."*
  Two verified problems, only the first of which is agent-exploitable:
  1. **The bound is defeatable by the agent, which is the threat this section is
     about.** `syntheticBusyStale()` returns false whenever `paneOutputActive()`
     (`runner/src/session.ts:431`), and pane-output freshness is just "the child
     printed within 90s" (`session.ts:438`) — something the in-pod agent fully
     controls. Going quiet on *observer events* while still writing to stdout
     holds the stall open indefinitely. The bound is on observer silence AND
     output silence, not observer silence alone.
  2. **"Frees the session for reaping" doesn't follow even when the release does
     fire.** The release only flips status to idle; `recomputeIdle` still gates
     `idleSince` on `isDetached()` (`session.ts:406-415`), which is false while
     any pane is attached — see the reaper item above. Not agent-controlled, so
     not a security hole, but it makes the sentence untrue as written.
  **Fix direction:** correct the parenthetical to state both conditions and drop
  the unconditional "frees the session for reaping". Whether the residual is
  still an *accepted* risk is a maintainer call — (1) turns a 5-minute bound into
  an unbounded one, which may or may not change the verdict. Do NOT quietly
  restate the bound without deciding that. Sequencing: fixing the reaper item
  above resolves (2), so land this correction with or after it and describe the
  world as it will then be. Provenance for the original claim:
  `docs/review-2026-07-20.md` §S [S3].
  **Verification:** doc-only; re-read against `session.ts:392-440` after the
  reaper fix lands. No test.

- [ ] **Passing MCPs and skills into sessions — needs a design call.** Skills
  already flow: `ConfigInputsSubs` (`internal/sync/sync.go`) one-way syncs
  `~/.claude/{skills,agents,commands,hooks,statusline}` plus the project
  `.claude/` (README documents it). **MCP servers do not**, and the harder half
  is the maintainer's own framing: *exposing laptop endpoints to agent pods* —
  MCP servers, an ssh-agent socket — is a reverse tunnel out of the pod into
  the developer's machine, which inverts the current trust direction (today
  everything is host→pod push, or pod→cluster egress on an allowlist).
  Prerequisite for any design: decide whether reverse exposure is in scope at
  all, and if so whether it is per-session opt-in with an explicit grant.
  Related surface that already exists: `CreateOptions.ExtraEnv`/`ExtraSecretEnv`
  + `BootstrapFiles` (§8, all shipped) can carry an MCP *config* today — what
  is missing is the *transport* for a host-side server.
  `k8s/networkpolicy-egress-fqdn.yaml.example` carries the egress precedent and
  its "opening egress for a tool also opens its token's exfil path" note.

- [ ] **Pane byte-flow debug log — the last residual of the 2026-07-25 blank-pane
  incident (MITIGATED, not closed).** *Status corrected 2026-07-27: two of the
  three original fix directions shipped in `3e4a2fd` and this note previously
  described them as outstanding.*
  **The incident:** a fresh `sandbox opencode` session rendered nothing in the
  pane from 16:26 until the SPDY forward dropped at ~16:39, then rendered
  immediately once `runForward`'s reconnect respawned the attach child. Pod
  healthy, `opencode serve` answering through the forward (direct curl of
  `/session`, `/config`, `/path`, `/event` SSE), attach child #1 alive, and a
  manual `opencode attach` through the ORIGINAL forward rendered fine. Ruled out
  live: the server side, the x/vt emulator + reply-pump pipeline (headless
  replica of the exact `ExternalPane` plumbing rendered correctly), the
  OSC 66 / CSI 6n width exchange, and a dashboard main-loop freeze. Best
  fit-all-facts candidate that was never excluded: one of child #1's startup
  streams stalling inside the legacy SPDY forward — the upstream-known pathology
  `PortForwardWebsockets` exists to fix.
  **Shipped since (both in `3e4a2fd`, verify by symbol not line):**
  (1) startup-recovery watchdog — `startupRecoveryAttempted` + a first-frame
  deadline in `internal/tui/dashboard/external_pane.go`, one recovery per user
  attach so a legitimately quiet client cannot respawn-loop; (2) the WebSocket
  dialer with SPDY fallback — `portforward.NewSPDYOverWebsocketDialer` +
  `NewFallbackDialer` + `shouldFallbackToSPDY` (`internal/k8s/portforward.go:330-335`),
  replacing the bare `spdy.NewDialer`. Together these address both the trigger
  and the 13-minute detection gap.
  **What remains (this item):** a debug log file for pane byte-flow, so a
  recurrence is root-causeable at all — the TUI hides stderr, which is why child
  #1's byte stream was unobservable post-mortem. Natural home is the existing
  per-session file sink from §10 `[T1]` (already landed) rather than a new
  mechanism. **Verification:** attach a pane, confirm the byte-flow log records
  first-frame timing and stream lifecycle; check it captures the recovery path
  by forcing the startup deadline low.

- [ ] **LIVE: validate the two new workspace-guide targets** (the fix landed
  2026-07-27, done log; `guideTargetFor(backend, env)` in `runner/src/index.ts`
  + `runner/src/workspace-guide.ts`). Both new targets rest on
  documented-but-unexercised behavior: that codex reads `$CODEX_HOME/AGENTS.md`
  (`CODEX_HOME` being the documented relocation of `~/.codex`), and that
  opencode honors an absolute path in its config's `instructions` array (the
  field is typed in the pinned `@opencode-ai/sdk`, never observed working).
  **Verification:** start a codex session and an opencode session on a
  worktree-backed project and ask each agent what it knows about its git
  situation *before* it runs any git command — it should already say the
  worktree's `.git` points at a host path. **If codex misses it,** the fallback
  is `$CODEX_HOME/config.toml`'s instruction keys.

### 0b. Loose ends from the 2026-07-25 ctx%/sync batch

- [ ] **Rebuild + publish the runner image so ctx% part B reaches live
  sessions.** `contextLimitTokens` is emitted by
  `runner/src/claude-pane-observer.ts` (handleStatusline) but a pod only picks
  it up on a new image; until then live sessions run the part-A clamp
  (`client/models.EffectiveContextLimit`). Trigger
  `.depot/workflows/build-runner-image.yml`, then recreate a session and check
  the dashboard ctx% against the in-pane statusline — they should now agree
  exactly rather than approximately. *Maintainer confirmed the rebuild cost is
  acceptable ("This is fine. As long as it works idc", 2026-07-25).*

- [x] **`go test ./...` walks into `runner/node_modules` — done 2026-07-27**
  (done log): one `ignore runner/node_modules` line in `go.mod` (the Go 1.25
  directive) instead of narrowing each recipe's pattern, so it cannot drift as
  top-level dirs are added. `go list ./...` 24 → 23 packages (the npm
  `flatted` one gone, all of ours kept); build/vet green; `tidy` preserves it;
  no-op when the dir is absent, so Go gates that run before `npm install` are
  unaffected.

- [x] **Is auto-compact on in pane sessions? — ANSWERED 2026-07-27: yes, by
  Claude Code's own default; the sandbox touches the setting nowhere.** Verified
  against the env allowlist, the seeded `settings.json`, the `.claude.json` seed,
  and a live pane pod's on-disk config. No default needs seeding. Detail on the
  parent bullet in the top-of-file list.

- [x] **Compaction is INVISIBLE to the dashboard — FIXED 2026-07-27** (done
  log): `PreCompact` added to `PROVISIONED_HOOK_EVENTS` + mapped in the
  observer switch, so auto-compaction lands in the feed instead of ctx%
  dropping unexplained. **Still needs the §0b runner image rebuild to reach
  live sessions.**

### 0a. Live-dogfood reports (2026-07-15) — ALL RESOLVED same day (done log)

All five maintainer reports fixed + landed: user-block wrap, wrap-aware
composer growth, ctrl+o/ctrl+e split, PasteMsg routing, and the two
directives (CC-style `/model` picker with Fable in the fallback; composer
↑/↓ own history/cursor, never scroll). Detail in the done log.

## 1) Correctness bugs

§1a (TUI SSE / state-machine cluster) and §1b (group view / sort / search /
pickers) are fully closed — done log. Residuals from §1c live below; the §1b
row-model consolidation moved to §2a where it belongs.

### 1g. Dashboard lifecycle actions bypass client (2026-07-18 SDK-example review) — DONE

- [x] **TUI destroy skips the worktree WIP capture — fixed 2026-07-18**
  (done log): lifecycle actions routed through `client` via the
  `clientLifecycleBackend` adapter; destroy-hook plumbing removed.
- [x] **TUI suspend/resume don't pause/resume file sync — fixed
  2026-07-18** (done log): same adapter change.

### 1h. claude-pane live-validation bugs (2026-07-20 first sessions; detail in review-2026-07-20.md §L)

- [x] **[L1] fixed 2026-07-20 (same night):** fail-closed fullness gate at
  every layer (`cred.ValidateFullCredential` exported; `UseClaudePaneMaterial`
  + Create both reject setup-token-shaped credentials with remediation), the
  picker leads with a "host claude login" row (default, empty id →
  SystemMaterial) and renders stored accounts inert with the setup-token
  reason, `--account` help + docs honest. Store-account path self-heals when
  the store learns full OAuth docs. Also closed [O13] (sentinel remediation
  text) in the same change.
- [ ] **[L2] Pane replay renders corrupted frames on attach (MED).** Observed
  garbled/overlapping header ("Claude CodClaude Code") on a live 2026-07-20
  session. Likely mechanism: scrollback-ring chunk eviction
  (`ScrollbackRing` in `runner/src/claude-pane.ts`) cuts mid-escape-sequence,
  so replay starts without cursor context. GATED on a live repro (maintainer):
  attach → generate >256 KiB of output → detach → reattach; note whether
  corruption appears on FIRST attach too (would implicate something other
  than ring eviction). Then pick: (a) runner-side — after sending the replay
  snapshot, force the child to repaint via a rows±1→rows resize jiggle on
  the pty (one-liner in the attach path, works regardless of ring cut
  position; slight flicker), or (b) trim the replay snapshot to start at the
  last clear-screen/cursor-home sequence (`ESC[2J`/`ESC[H` scan in the ring
  snapshot — cleaner but misses claude redraws that never clear). Prefer (a)
  unless the repro shows it flickering badly. Tests: unit-test whichever
  helper (jiggle call sequence via the PaneSpawner seam / trim function on
  crafted byte strings) in the runner suite (`cd runner && npm test`);
  final verification is the live reattach.
- [x] **[L7]+[P3] done 2026-07-21** (done log): wheel with child
  mouse-tracking OFF scrolls a local view over the vt scrollback (3
  lines/tick, clamped; alt-screen ignored; "↑ N lines — any key to return"
  indicator replaces the detach hint); key/paste/new-output snap back to
  live BEFORE forwarding; tracking children still get SGR wheel. [P3]:
  scrollback capped at 2000 lines (`SetScrollbackSize`) — the viewer is its
  only reader. Live trackpad feel still wants a maintainer eyeball.
- [x] **[L8] parts (a)+(b) fixed 2026-07-21** (done log): UserPromptSubmit's
  busy is now PROVISIONAL — a timer-driven ~10s confirm window
  (`SANDBOX_BUSY_CONFIRM_WINDOW_MS`, deps-injectable) reverts to idle via
  the standard `setStatus` path (emits `session.status_changed`) unless
  model activity (MessageDisplay/PreToolUse/PostToolUse/PermissionRequest)
  confirms; late activity re-asserts busy; the synthetic turn stays open so
  a late Stop still completes it. Stale synthetic busy is now RELEASED for
  real in `recomputeIdle` — status flip + event, attachment-independent;
  reaper eligibility unchanged (still isDetached-gated; real runner turns
  exempt via the activeTurns guard in `syntheticBusyStale`).
- [ ] **[L8 residual] Esc-interrupt probe + honest "stalled?" rendering.**
  (c) probe whether claude fires ANY hook on Esc-interrupt (E1 hookprobe
  method) and map it to `closeTurn('interrupted')` — needs a live session.
  Dashboard follow-up (separate, needs design): an honest "stalled?"
  rendering for busy-with-quiet-observer beats a false "working".
- [x] **[L9] done 2026-07-25** (done log): in-pane copy reaches the host
  clipboard. The vt emulator swallowed the child's OSC 52 as an "unhandled
  sequence", so Claude Code's "sent N chars via OSC 52" confirmation was a
  lie and shift-drag native selection was the only way out. `Init` now
  registers an OSC 52 handler that queues onto `pendingClip`; `apply()`
  drains it into `tea.SetClipboard` batched with the next read (and
  `handlePtyOutput` batches it with the finished message so a copy in the
  child's final output still lands). Parse lives in the PUBLIC
  `terminal.ParseOSC52` (sdktest-pinned). Applies to every pane backend.
  Residual: clipboard *reads* (`OSC 52 ; c ; ?`) are still dropped — an
  answer needs `tea.ReadClipboard` plus an async reply down the transport.
- [x] **[L3] done 2026-07-21** (done log): feed opens seeded from a
  ONE-SHOT passive SSE replay from seq 0 (`feed_history.go` — attach-gate/
  connect-slot/C1 discipline, 15s read bound, 2000-event tail cap, gen
  guard, live-tap buffering so seq-dedup can't drop the seed); the
  dashboard stream keeps `after=lastSeq`, so the notification-flash guard
  is untouched. The orphaned EventCache surface (dashboard interface +
  cli adapter + `internal/index/cache.go`) deleted in the follow-up
  hygiene commit, per option A.

### 1c. Rendering / layout residuals (2026-07-04 audit; parents fixed — done log)

- [x] **`statusline.go` row-1 overflow — closed 2026-07-12** with the §2c
  statusline collapse (budgeted `slSeg`/`budgetRow`, required segments kept,
  optional shed tail-first; width-safe by construction).
- [x] **Subagent child tool lines width-safe — done 2026-07-11** (done log):
  budgeted by construction (measured prefix, remaining-width segments,
  ANSI-aware whole-line backstop); pinned at widths 8-80.

### 1d. System reliability (2026-07-01 whole-system review; HIGHs all fixed — see done log)

**2026-07-07 handoff-review additions** — detail in
[`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §C/§H (id in brackets):

- [x] **[H1] observer cap protection fixed — done 2026-07-09** (done log):
  NeedsInput protected only while output is unseen (`lastSeq > seenSeq`);
  Waiting/Failed/attached stay protected.
- [x] **[C1] Close seam for port-forwards — done 2026-07-09** (done log):
  `ConnectResult`/`CreateResult.Close` (→ `sess.Close`) wired through
  `cancelLiveSSE`, ready-msg discards, EventsPassive failure, approve
  fallback, detach (`parkTranscript`), external-pane close, stale-gen ready.
- [x] **[H2/H3] eviction side effects fixed — done 2026-07-09** (done log):
  armed `/loop`/`queuedPrompt` protected; eviction keeps the warm model;
  Busy rows stamped to watch baseline; lapse toast wording cause-agnostic.
- [x] **[C3] shape-changing re-create rejected — done 2026-07-09** (done log):
  desired vs baked pod-template env compared (`anthropicEnvShape`, since
  generalized to `credentialEnvShape` — §6 C3-codex, 2026-07-21) BEFORE any
  Secret mutation; same-shape account swaps still patch in place. Supersedes
  the old strip-on-account-removal behavior (which could brick resume).
- [x] **[C4-C11] assorted client reliability — done 2026-07-09** (done log):
  observer forwards 1 port not 3; ssh config paths quoted; background connect
  phase bounded 60s with a timeout advisory; pre-existing PVC survives
  rollback; `projectPath` race fixed; suspend probe capped 5s; `models.Limit`
  refreshes models.dev async (never blocks the reducer); reaper replaced on
  spec mismatch so `IdleTimeout`/`ReaperImage` overrides apply.

- [x] **Concurrent-session sync collision — CLOSED 2026-07-11** (done log):
  git projects isolated by §9 worktrees; non-git same-path sessions now get
  a warn-only `Connection.Warning` at Connect (`sameDirSyncWarning`, index-
  resolved alphas, silent without mutagen).
- [x] **Mutagen conflict detail in the TUI — done 2026-07-11** (done log):
  `conflicts[]` parsed typed (alpha/beta per path, defensive on shape drift);
  `StatusDetail` + per-file lines + resolution hint in the detail pane
  (capped at 5 + "+N more"). Shape unverified against a live conflicted
  mutagen — falls back to count-only on drift.
- [x] **Transcript provenance audit trail — done 2026-07-11** (done log):
  the sandbox-session → claude-session-id mapping (already in the index but
  deleted on destroy) now also appends to `transcript-audit.jsonl` in the
  state dir, deduped, surviving destroy. The unscoped `~/.claude` merge
  itself stays by design (subPath bind, resumability contract).
- [ ] **Port-forward mid-stream death detection (SMALL, optional).** Terminal
  state + immediate `ErrSessionGone` reconnect-abort landed (done log);
  consuming the literal `ForwardHandle.Done()` channel needs a
  `ConnectResult.ForwardDone` seam through client/cli — only worth it if
  mid-stream (non-reconnect) death detection matters.

### 1e. Autopilot (`/loop`/`/goal`) — REMOVED 2026-07-20 (claude-pane-first)

The entire autopilot feature (local tea.Tick driver AND the 2026-07-11
server-side loop) was deleted with the SDK turn engine in the
claude-pane-first change (maintainer decision; see
`openspec/changes/claude-pane-first/design.md` D8 and the archived
[`server-side-loop-adr.md`](docs/archive/server-side-loop-adr.md), now
Status: superseded). Programmatic turns don't exist for pane backends, so
there is nothing for a driver to submit.

- [ ] **Autopilot revival via headless `claude -p --resume` (watch item).**
  The verified revival path: a runner-side loop that appends turns to the
  SAME pane conversation with `claude -p --resume <claude_pane_session_id>`
  between interactive attaches (append semantics verified in the 2026-07-20
  pane research). Needs: serialize against the interactive child (never run
  both at once), map `-p` stream-json output through the observer's event
  path, re-arm UX. No code now — revisit when a laptop-closed loop is
  actually missed.

### 1f. Security & runner-reliability hardening (2026-07-07 handoff review)

Verified findings from the 8-agent handoff sweep; full detail + exploit/scenario
in [`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §A/§B (id in brackets).

**2026-07-20 pane-first security review** (trust model + full scenarios in
[`docs/review-2026-07-20.md`](docs/review-2026-07-20.md) §S; core auth got a
clean bill — constant-time compare, upgrade-auth ordering, env allowlist,
unified redaction):

- [x] **[S1] done 2026-07-21** (done log):
  `k8s/networkpolicy-egress-fqdn.yaml.example` (Cilium `toFQDNs`; host set
  re-verified against the CURRENT Claude Code network-config doc —
  statsig/sentry are legacy there now, `platform.claude.com` added because
  the in-pod claude refreshes its own credential; codex/opencode/registry
  blocks commented per-backend; mandatory DNS-proxy rule with tunneling
  caveat), SECURITY.md exfil paragraph extending [A3], k8s/README
  subsection. Host set NOT live-validated — `hubble observe` a session
  before enforcing. Longer-term scoped credentials + the
  opencode-multi-provider-auth seed filter remain separate tracks.
- [x] **[S2] done 2026-07-21** (done log): 12 credential-filename patterns
  (`.netrc`/`_netrc`/`.npmrc`/`.git-credentials`; `.aws` +
  `service-account*.json`; default SSH private-key names + `.*` derivatives)
  added to the non-overridable `securityIgnores` layer in
  `internal/sync/sync.go`, layering test pins presence + position, README
  Mutagen bullet notes the defensive exclusion.
- [x] **[S3] done 2026-07-21** (done log): SECURITY.md threat-model section
  "Observer events are agent-influenceable (claude-pane)" — same-session
  spoofing + bounded reaper stall documented as accepted; cross-session
  impossible (per-session token). Origin-tagging NOT implemented — still a
  maintainer design call.
- [x] **[S4] done 2026-07-20:** grants.ts + its test deleted; the unused
  `@anthropic-ai/sdk` dep dropped in the same hygiene commit.
- [x] **[S5] pane WS maxPayload + resize bounds — done 2026-07-27** (done log):
  rode the [T2] server.ts work rather than waiting for the still-gated slow-link
  change. `MAX_PANE_FRAME_BYTES` (1 MiB) replaces `ws`'s 100 MiB default on the
  pane `WebSocketServer`; `MAX_PANE_DIMENSION` (2000) bounds a resize in either
  direction — rejected, not clamped, so the two ends can't silently disagree
  about pane size. 5 tests in `runner/test/pane-control-bounds.test.ts` (there
  were none for `parsePaneControl` before). The slow-link compression change is
  still gated on RTT numbers and no longer carries [S5].
- [ ] **[S6] runAsNonRoot deferral stays tracked**
  (`internal/k8s/backend.go:1572-1582,1667-1673`; PSA baseline permits root,
  restricted warns).

- [ ] **[A1 residual] `RUNNER_TOKEN` still recoverable via `/proc` — uid
  separation needed to truly close self-approval (MED, adversarial review
  2026-07-08).** The A1 env-strip landed (child spawns + the workspace git
  calls all get `sanitizedExecEnv`; done log), but runner and agent child share
  uid 0 (`backend.go:1377`), so `tr '\0' '\n' < /proc/1/environ` recovers the
  bearer token and the runner API is reachable on in-pod localhost
  (`server.ts:77`). Fix: run the agent child as a non-root uid distinct from
  the runner (or mount `/proc` with `hidepid=2`); pod-spec + Dockerfile work,
  coordinate with the §7b base-image spike. Until then A1 is
  raised-bar-not-closed; comments in `opencode.ts`/`codex.ts` say so, and the
  claude-pane child's env allowlist (`runner/src/claude-pane.ts`) is scoped
  against the same threat (2026-07-20: applies to the pane child too — it
  gets a scoped observer token by design, but uid-0 `/proc` still exposes
  the full runner token).
- [x] **PreToolUse block result modernized — done 2026-07-11** (done log):
  returns `hookSpecificOutput.permissionDecision:'deny'` AND keeps the
  legacy `decision:'block'` alongside (both shapes verified against the
  pinned SDK's `sdk.d.ts`); guard tests pin the combined shape. SDK version
  unchanged (the pin question stays in the carry-forward caveat below).
- [x] **[A2] event log + SSE redact secrets — done 2026-07-09** (done log):
  shared `redact.ts`; `appendEvent` masks `turn.started`/`tool.*`/
  `permission.*` + role-user `message.*` (the D5 echo) before persist AND
  broadcast.
- [x] **[A3] SECURITY.md posture rewrite — done 2026-07-11** (done log):
  0.0.0.0-binds table + what the ingress policy does/doesn't contain, open-443
  egress named plainly as the exfil channel + `toFQDNs` hardening path, the A1
  `/proc/1/environ` residual with exact guarantees, verified controls list
  (every claim carries file:line), corrected the stale "drop-ALL caps" claim
  (12 caps re-added incl. SETUID/DAC_OVERRIDE).
- [x] **[B1] opencode `serve` spawn-error listener — done 2026-07-08** (done
  log): `'error'` + `'exit'` share one per-child respawn scheduler.
- [x] **[B2] 409 gate covers observer-synthetic opencode busy — done
  2026-07-08** (done log): pure `turnRejectReason` in `server.ts`
  (a first bite of [F4]).
- [x] **[B3] /exec resolves at bash exit + process-group SIGKILL — done
  2026-07-08** (done log): `detached:true`, resolve on `'exit'`,
  `kill(-pid)` on timeout.
- [x] **[B4] persist-failure events delivered live — done 2026-07-08** (done
  log): seq-0 bypasses the `<=afterSeq` filter (`shouldDeliver`).
- [x] **[B5-B9] runner robustness LOWs — done 2026-07-09** (done log): after
  clamp; async git (A1 sanitization preserved); corrupt session.json moved
  aside + reseed; permission resolve first-write-wins with honest 409;
  typed 413/400 body errors.

## 2) The "feels like Claude Code" program — CLOSED 2026-07-20 (claude-pane-first)

**Closeout:** the program's premise died when the 2026-07-04 §3 decision was
reversed — `sandbox claude` now runs the **real Claude Code TUI** in an
external pane (runner-owned PTY + WebSocket attach; see
`openspec/changes/claude-pane-first/`), so there is no custom claude
transcript left to bring to parity. The custom chat renderer, SDK event
pipeline for claude, and their public surfaces were deleted (tasks 6.2–6.7 of
that change). What the program had already shipped is in the done logs
(2026-07: §2a structural enablers, §2b pipeline gaps 1/2/3/6/8 + D1–D12, most
of §2c/§2d); those payoffs live on in the shared reducer, the event model,
and the dashboard.

Disposition of the items that were still open here:

- **Obsolete with the renderer/SDK engine deleted** (do not revive): 2b gap 5
  render-half (tool.progress elapsed — event pruned), gap 7 (images), gap 9
  (prompt queue), gap 10 (MCP wiring), AskUserQuestion answer flow, gap 2's
  editedInput/canUseTool residuals, 2c tool-card follow-ups + the
  `↓ new output` pill residual — Claude Code itself now provides all of these
  in-pane (images, queueing, MCP, AskUserQuestion, tool expansion).
- **Still live, retargeted at the dashboard/feed**: the §2e premium-feel
  items below (renamed — they never depended on the transcript), and the §2a
  clock-injection deferrals (folded into §10 test hygiene territory).

### 2e. Dashboard premium-feel backlog (2026-07-07 Crush/ecosystem research)

Design detail lives in [`docs/tui-premium-plan.md`](docs/tui-premium-plan.md)
— five-agent comparative study of Crush (FSL: **ideas only, never copy
code**), ultraviolet, gh-dash, huh (all MIT). Retargeted at the dashboard/feed
after claude-pane-first deleted the transcript: workstream **B (transcript
depth) is OBSOLETE** — skip it when reading the plan doc.

> **SPLIT 2026-07-27 (maintainer decision).** The layout-touching workstreams
> — **[L5]**, **A**, and **F** — are **folded into §8's panel workstream** and
> must NOT be started here; they rewrite the same code the panelization
> dissolves, and doing them separately would produce an internal-only
> abstraction that then has to be redone. The premium-feel intent travels with
> them: the merged item carries a visual acceptance bar alongside the
> consumability one.
>
> **A4, C, D, E stay here** — they are independent of layout and can proceed
> any time. They no longer need the plan doc signed off as a whole; each is
> self-contained against its own `Plan §` section.

- [x] **"needs input" relabel — done 2026-07-21** (done log): display label
  → "ready" (row label, attention summaries "%d ready"/"%d ready below",
  detail note "ready for your next prompt"); wire string "needs-input",
  Status constant, and the already-calm ❯ `GlyphNeedsInput` untouched;
  goldens unaffected (no fixture renders NeedsInput) — attention tests pin
  the new strings; StatusWaiting semantics unchanged.
- **[L5] — FOLDED into §8's panel workstream (2026-07-27).** External panes
  as floating modals over the dashboard (fleet context stays visible instead
  of a full-screen takeover). It needs both a modal system (workstream A's
  output) and a pane viewer (`[P2-4]`'s output), so it cannot lead. Sizing/
  focus semantics, sub-region rendering through the vt emulator, and input
  coalescing are the open design questions; seam exists
  (`internal/tui/dashboard/pane_transport.go`), detail in
  `docs/review-2026-07-20.md` §L5.

- **A. Dialog stack manager — FOLDED into §8's panel workstream
  (2026-07-27).** One `Dialog` interface + stack replacing ~8 bespoke overlays
  and 4 copies of center/shadow math (`model_render.go:122-166`,
  `app.go:1009`, `app.go:1137`, `backend_picker.go:211`) — the cited range is
  *inside* the `render()` the panelization dissolves, which is why it moved.
  Carry the 200ms/1.5s/500ms grace period with it: it kills the
  async-permission blind-approve class and is not purely cosmetic. Plan §A.
- [ ] **A4. Input coalescing** — no `tea.WithFilter` today; 16ms wheel/motion
  throttle with sign-aware delta summation. Plan §A4.
- [x] **B. Transcript depth — OBSOLETE 2026-07-20** (claude-pane-first
  deleted the custom transcript renderer; Claude Code renders its own
  transcript in-pane). Plan §B stays as-of-time in the doc — skip it.
- [ ] **C. gh-dash lifts (MIT, same charm v2 stack)** — async action task
  queue (start/finish/error + `[⟳ N]` badge + 2s auto-clear in the
  statusline) and the fixed+Grow table/column engine for the session list.
  Plan §C.
- [ ] **D. Motion & chrome** — scrambled-glyph gradient thinking shimmer
  (deterministic staggered fade-in, frame cache; honor
  `SANDBOX_REDUCE_MOTION`), `v.WindowTitle` + `ReportFocus` + native
  progress bar (ghostty keep-alive quirk), composer micro-UX (prompt
  history ↑/↓ with draft preservation, paste-to-attachment, randomized
  placeholders). Plan §D.
- [ ] **E. Theming: iTerm scheme import + /theme picker** — vendor ~12
  curated schemes from mbadolato/iTerm2-Color-Schemes (MIT) in the ghostty
  `key=value` export format, `just gen-themes` → `schemes.gen.go`,
  `Derive()` maps 22 scheme colors → semantic tokens (perceptual blends +
  contrast-floor CI test; imported themes keep their own ANSI-16 for
  authentic tool output), `/theme` picker with live preview + persisted
  choice (`SANDBOX_THEME` env > saved > auto). `tui/theme/theme.go:290-317`
  already stubs the hooks. Plan §E.
- **F. Ultraviolet phase 1–2 — FOLDED into §8's panel workstream
  (2026-07-27).** Composing cells ourselves deletes the
  `withBackground`/`bgSeq`/`clampLines` opacity machinery (`zones.go:50-105`)
  and collapses the dual overlay systems — that compositing layer is what any
  panel abstraction sits on, so it belongs in the same pass. Still ADR-first.
  Does NOT fix tea.Raw/Kitty (already correct) or child resize seeding.
  Plan §F.
- [ ] **G. Capability probing + notification backend selection (LOW)** —
  allowlist-gated DA1/XTVERSION/pixel/Kitty/OSC99 probe burst; notification
  escalation (native/OSC99/OSC777/bell) with focus suppression. Plan §G.

## 3) Decision record — Claude Code as the client (REVERSED 2026-07-20)

**2026-07-20 amendment: the 2026-07-04 rejection is SUPERSEDED by
claude-pane-first.** The "Recorded, NOT planned" option below shipped — in a
materially better shape than the one evaluated: a **runner-owned node-pty**
child (not `ssh -t`; survives disconnects, no tmux rendering bugs), a
**WebSocket pane** on the existing 8787 forward (no keystroke-over-ssh
stack), provisioned **observer hooks + statusline** as the metrics tap (the
"no metrics-observer API" cost turned out solvable), `--session-id` pinning
(the "resume forks the session id" cost was wrong for interactive
`--resume`), and full credential materialization for Max mode. The
empirical groundwork that flipped each cost is in the 2026-07-20 pane
research (maintainer-local); the shipped design is
`openspec/changes/claude-pane-first/design.md`. The latency bar the 2026-07-04
decision assumed was re-tested and judged excellent by the maintainer on the
target network. The ssh-shim rejection below STANDS (it bypasses the runner
control plane; pane-first does not). The upstream-transport watch item is
obsolete — the pane transport ships in this repo.

Original record (kept so the reasoning trail survives):

Three-track research (official surface, community art, repo feasibility) into
using Claude Code **directly** as the client for a remote sandbox session.
Outcome at the time: **not happening; invest in §2 instead.**

- **Blocked upstream:** Claude Code has no remote-attach transport — no analog
  of `codex --remote ws://…` / `opencode attach <url>`;
  `--input/--output-format stream-json` is a headless stdio protocol for a
  driving program, not an attach surface, and is undocumented
  (anthropics/claude-code#24594; feature requests #10042, #72448). Anthropic's
  first-party answer is the desktop app's SSH sessions (local GUI, remote
  agent) — a GUI, not the TUI.
- **REJECTED (maintainer): the `CLAUDE_CODE_SHELL` ssh-shim pattern** (local
  claude, Bash proxied over ssh; à la torarnv/claude-remote-shell,
  langwatch/claude-remote). Do not re-propose. Structural costs: rides an
  undocumented env knob; git split-brain with the `--ignore-vcs` project sync;
  bypasses the entire runner control plane (guards/audit/events/metrics/idle)
  — it un-sandboxes the sandbox.
- **Recorded, NOT planned — in-pod official TUI over `ssh -t` as an external
  pane** (codex Option-B shape; violates the "TUI not remote" latency bar).
  Mechanics if ever revisited: `ssh -t sandbox-<id> 'claude --resume
  <claudeSession>'`; binary already in the runner image; `CLAUDE_CONFIG_DIR`
  pod-side (`backend.go:1253`); resume id in `GET /sessions/:id/status`;
  external-pane precedent in `external_pane.go`. Known costs: keystroke RTT,
  CC renderer misbehaves in tmux (claude-code#9935/#4851), permission modal
  replaced by claude's own, guards/audit only via pod-side settings hooks
  (the SDK's programmatic guard/audit hooks attach only to SDK turns —
  `claude.ts:429`; since the §2b gap-8 fix both paths load on-disk settings),
  no metrics-observer API, resume forks the session id,
  needs pod tmux for TTY-death survival.
- [x] **Watch upstream for a real remote transport — OBSOLETE 2026-07-20:**
  claude-pane-first ships our own pane transport (runner PTY + WS), so an
  upstream remote-attach no longer gates anything.
- Also evaluated and rejected: SSHFS mounts (per-file-op RTT),
  MCP-ssh-tools-with-built-ins-denied (token-expensive file ops, model drifts
  back to native tools), dev containers (local isolation only), web teleport
  (web→local only).

## 4) Performance

**2026-07-20 pane byte-path review** (detail in
[`docs/review-2026-07-20.md`](docs/review-2026-07-20.md) §P; all prior
E-series SSE fixes verified intact — do-not-regress list in the doc):

- [x] **[P1] done 2026-07-20:** bounded non-blocking drain in `apply`
  (256 chunks / 1 MiB caps), one emulator Write per burst; reader goroutine
  + O7 grapheme boundary untouched. With [P5] in the same commit.
- [x] **[P2] done 2026-07-20:** pane WS closes with code 4003 over a 4 MiB
  `bufferedAmount` cap (E3 parity); client reconnects into the scrollback
  ring.
- [x] **[P3] done 2026-07-21 with §1h [L7]** (done log): scrollback kept
  but capped at 2000 lines; the [L7] wheel viewer is its reader.
- [x] **[P4] done 2026-07-21** (done log): input-writer goroutine is the
  sole UI-side transport writer — 64-entry tagged queue (`paneInput{data,
  size}`) carries keys/paste/mouse AND resize in UI order (geometry can't
  overtake type-ahead); non-blocking enqueue, drop-on-full records a
  pane-level error on the existing `p.err` surface; writer exits via the
  P5 done channel, close()'s transport.Close() unblocks a parked Write.
  Emulator reply pump keeps direct writes (capability replies must never
  drop). Stalled-transport detach + ordering + drop tests; race suite
  green.
- [x] **[P5] done 2026-07-20** with [P1]: done-channel select in the reader,
  closeOnce-guarded close.
- [ ] **[P6] One fresh Node process per observer hook event — PreToolUse on
  every tool call's critical path (MED, measure live cadence first).**
  Helper scripts + PROVISIONED_HOOK_EVENTS in
  `runner/src/claude-pane-observer.ts`. Measurement recipe (live session,
  maintainer or cluster access): count observer POSTs per turn — `kubectl
  logs <pod> | rg -c observer` or add a temporary counter — and time one
  PreToolUse hook (`time node hook.js < sample.json` in-pod). Only if
  PreToolUse adds >~100ms per tool call: MINIMAL fix = hook.js fires the
  POST and exits WITHOUT awaiting the response (drop the `finally(exit)`
  await chain; best-effort telemetry already) — keep the 3s abort for the
  statusline which must print. Do NOT build the FIFO/persistent-forwarder
  variants: the Go-runner rewrite (§10 watch item) replaces the scripts
  with a runner subcommand and would strand that work.
- [x] **[P7] Feed streaming O(n²) per message — MEASURED 2026-07-27, declined**
  (done log; `perf_bench_test.go`). 3-5µs per delta (~0.03% of a 60fps frame) at
  work that happens at network cadence, not per frame. Holding the message at
  32 KB and cutting deltas 8× gave 4.8×, while holding deltas and cutting bytes
  8× gave only 1.8× — so per-delta constants dominate and the quadratic rebuild
  is sub-dominant at every realistic size. Benchmark kept as a tripwire.
- [x] **Pane transport RTT probe — done 2026-07-21** (done log):
  SANDBOX_TRACE-gated pinger in `internal/runner/pane_rtt.go` (5s
  WriteControl pings w/ nanotime payload, pong-handler sampling into a
  256-slot ring, additive `PaneStream.RTTStats()`, ONE stderr line on
  Close: `trace: <id> pane.rtt n= p50= p95= max=` — the format the §10
  SSE-latency probe should reuse). Zero runner changes (node `ws`
  auto-pongs). No live-link numbers yet — the slow-link item below stays
  gated on what this measures on a real slow link.
- [ ] **Slow-link mode: pane WS compression + runner output coalescing
  (2026-07-21 transport review; GATED on the RTT probe above showing it
  matters, or a real slow-link use case — on LAN it buys nothing
  perceptible). [S5] no longer rides this — it landed 2026-07-27 with the
  [T2] server.ts work.** ANSI redraw streams compress 5-10x, but both
  ends ship compression OFF: the pane `WebSocketServer` in
  `runner/src/server.ts` sets `maxPayload` but no `perMessageDeflate`,
  and `internal/runner/pane.go:77` dials with gorilla `DefaultDialer`
  (EnableCompression false). Separately, every `pty.onData` chunk becomes
  its own WS frame (`runner/src/claude-pane.ts` onData → `ring.push` +
  `safeSend`). Three parts, one change: (1) server.ts: enable
  `perMessageDeflate` conservatively (`threshold: 512`,
  `zlibDeflateOptions: { level: 1 }`) — the [S5] `maxPayload`/resize
  bounds are already in place. (2) pane.go: `DefaultDialer` → a
  `websocket.Dialer{EnableCompression: true}`; CAUTION — permessage-
  deflate is historically gorilla's flakiest feature; if frames corrupt
  under load, swap the client lib to `coder/websocket` INSIDE pane.go
  only (PaneStream is the seam; sdktest pins the PaneStream API, not the
  wire lib). (3) claude-pane.ts: coalesce output — buffer onData chunks,
  flush on a ~5ms timer OR ≥32 KiB, and flush synchronously on child
  exit, on detach/close, and before the attach replay snapshot;
  `ring.push` stays per-chunk (replay fidelity unaffected); safeSend's
  P2 backpressure check unchanged (runs per flush). Flush interval
  injectable for tests (existing runner deps-seam conventions). Tests:
  runner suite — two rapid onData within the window → one send; size
  cutoff flushes immediately; exit flushes the tail. Run
  `cd runner && npm test` + `go test ./internal/runner/` (unsandboxed).
  NOT worth doing regardless of measurements: semantic/diff protocols
  (pane-first deliberately ships bytes-of-truth) and mosh-style
  prediction (machinery >> benefit at current latencies); the
  data-plane-off-apiserver move (direct Service/tailnet to 8787) is a
  separate future call with [S1] security tradeoffs — don't fold it in
  here.

- [x] **Transcript-renderer perf items — OBSOLETE 2026-07-20**
  (claude-pane-first deleted the custom renderer): warm-preview tail
  re-render, `lastCompleteBlock` block rescans, glamour per-space SGR
  padding all left with it (`glamour` dropped from go.mod in the same
  change).
- [x] **`visibleSessions()` re-filters+re-sorts 4+ times per frame — MEASURED
  2026-07-27, declined** (done log; `perf_bench_test.go`). The default view is
  free and flat in n (5.7ns at n=8 and n=200 — `FilterSessions` returns the
  input on an empty query, `sortByAttention` is a passthrough when
  `attentionFirst` is off). Worst realistic case — 50 sessions, filter active,
  attention sorting on, ×4 per frame — is 0.36ms, ~2% of a 60fps budget.
  Memoizing would add an invalidation surface over every session mutation and
  filter keystroke to buy that back. Not doing it.
- [x] **`fitModal` two ANSI `lipgloss.Width` scans per line — FIXED 2026-07-27**
  (done log): one scan per line, padding inline. 1.35ms → 1.00ms on a tall feed
  (h=120,w=200, was ~8% of a frame), 89µs → 65µs at 20×80. ~26% rather than the
  50% the scan count suggests — the rest is Split/Join/Repeat allocation, noted
  in the code so nobody re-runs the experiment expecting a halving. Goldens
  byte-identical.

**2026-07-07 perf-review additions** (two agents; ✓ = both flagged it; detail in
[`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §E, id in brackets):

- [x] **[E1] tool.delta O(n²) hot path fixed — done 2026-07-09** (done log):
  Builder accumulation, eager-under-2KB-then-every-+2KB parse throttle,
  per-delta `Bump()` instead of `syncItems()`.
- [x] **[E2] SSE replay streams in bounded chunks — done 2026-07-09** (done
  log): 512-row chunks + raw-payload frame splice + drain-aware yields; the
  `replaying` flag + synchronous handoff preserve the in-order/no-dup/
  replay-complete contract.
- [x] **[E3] live-broadcast backpressure cap — done 2026-07-09** (done log):
  4 MiB `writableLength` cap; a wedged client is destroyed and reconnects
  with `after=<seq>`.
- [x] **[E4] delta-only compaction — done 2026-07-09** (done log): one
  bounded DELETE on `turn.completed` keeps the last N turns' deltas
  (`DELTA_COMPACT_KEEP_TURNS`, default 2); never fails the append.
- [x] **[E5] passive streams batch-drain — done 2026-07-09** (done log):
  `liveSSEBatchCmd` + `RunnerEventBatchMsg` mirror the foreground 512-drain;
  one Update+View per burst.
- [x] **[E6] live reasoning wrap is incremental — done 2026-07-09** (done
  log): complete-lines prefix cache keyed by width+theme epoch; only the
  trailing partial re-wraps per frame.
- [x] **[E7] streaming-tail O(1) change key — done 2026-07-11** (done log):
  buffer LENGTH (+ mode + theme epoch) replaces the full-buffer hash+copy;
  safe because the live buffer is append-only within a tail's life (audited
  every reset site). Bench: ~89ns constant vs O(L) per delta.
- [x] **[E8-E10] LOW perf trio — done 2026-07-11** (done log): SSE scan loop
  zero-copy via `scanner.Bytes()`+`CutPrefix`; `events.ts` per-connection
  prepared-statement cache; host event-cache holds one open O_APPEND handle
  per session + 8 MiB tail cap with atomic compaction.

## 5) New-session startup speed (ordered by likely win)

- [ ] **Shrink + split images, and deploy Spegel** — image pull dominates cold
  start and nothing warms it; the default image carries an opencode-only
  `npm i -g opencode-ai` layer the claude path doesn't need (codex will add
  more). Split per-backend images + run Spegel (P2P OCI mirror, via
  Argo/GitOps) so a cold node hits a peer cache. Default image ref:
  `client.DefaultRunnerImage` (`client/client.go:74`, flag wiring
  `internal/cli/claude_remote.go:35`); npm layer `runner/Dockerfile:66`.
  Decide image naming in the same change — the
  "claude-runner" name is a misnomer today (one shared image serves every
  backend; inbox 2026-07). Cross-ref the §7b ADR — its Flox layer is designed
  as the shared base of this split.
- [~] **Mutagen sync GC follow-ups — MF3/MF5 + SF1-CLI done 2026-07-12**
  (done log): context labels + scoped GC (legacy label-less syncs keep
  pre-MF3 reapability); prober-layer debounced Reconcile self-heal on
  stall; startup GC on bare-`sandbox`/`attach`; dev-reset/kind-down gc
  first. The two SF1 residuals CLOSED 2026-07-12 in batch 4 (dashboard
  `Init` fires `reconcileListCmd`; create commands run `startupSyncGC`).
  Unverified live: real-daemon heal of a genuinely wedged transport;
  `kind-down`-after-gc leaves orphans if sessions were live at teardown
  (pre-existing, noted). **2026-07-18 audit follow-ups landed** (done log):
  safety-halt vs stall split ([V2]), label sanitization ([V3]), Paused
  classification + heal ([V14]), namespace GC scoping ([V28]), paused-orphan
  reap CLI-side ([V35]). **[V35] residual CLOSED 2026-07-27** (done log): the
  dashboard now lists paused syncs too, tagged `OrphanSync.Paused`, and applies
  the rule the tag makes possible — a transport-down sync is judged on POD
  LIVENESS (`gcRunningSet`, unchanged), a paused one on SESSION EXISTENCE (new
  `gcKnownSet`: everything but `StatusGone`). So a suspended session's paused
  syncs are protected (resume unpauses them) while the same session's
  transport-down syncs are still reaped, and a deleted session's paused syncs —
  previously immortal to the dashboard, since they are not transport-down — are
  finally collected. Counter-checked: dropping the paused branch reaps a
  suspended session's sync, which would silently lose its file sync.

## 6) Codex backend + credential manager

Plan: [`docs/codex-integration-plan.md`](docs/codex-integration-plan.md) —
remote app-server + local `codex --remote` TUI (Option B), mirroring the
opencode supervisor/external-pane pattern + runner metrics-observer. Backend id
`codex-app-server` reserved (`internal/session/types.go:63`). Auth =
ChatGPT-plan OAuth owned by the credential manager.

- [x] **Codex C3 parity — done 2026-07-21** (done log): `anthropicEnvShape`
  → family-neutral `credentialEnvShape` (anthropic
  CLAUDE_CODE_OAUTH_TOKEN/ANTHROPIC_API_KEY + codex
  CODEX_AUTH_JSON/OPENAI_API_KEY; opencode still exempt — reconciles via
  `warnIfOpencodeCredsRotated`); codex shape-changing re-creates now
  rejected before any Secret mutation (closes the stripped-key resume
  brick), same-shape account swaps still patch in place — both pinned
  (`TestCreateSessionRejectsCodexAuthShapeChange`,
  `TestCreateSessionSameShapeCodexAccountSwapPatchesSecret`).
- [ ] **CLI-owned credential manager — write side.** Anthropic part DONE
  (multi-account store + Keychain/file backends + `auth
  login/list/logout/default`, public as `client/cred`). Remaining:
  codex/provider-key generalization on `client/cred` — macOS Keychain store
  (optional Secure-Enclave blob + Touch ID; file/env fallback on Linux),
  `sandbox auth {login,sync,logout}` (device-auth / setup-token / paste-key),
  create/connect **reconcile** that seeds the `agent-sessions` Secret +
  prompts for renewal when a cred can't auto-refresh. Generalizes
  `ensureSSHKey`. Egress allowlist must gain OpenAI/ChatGPT hosts.
  NOTE (from the landed §7a injection work): `resolveOpencodeProvider`
  silently defaults unrecognized values to Anthropic — the future
  `CreateOptions` selector must validate, not default.
- [ ] **Unified per-backend credential lifecycle (maintainer ask 2026-07-04;
  Fable-triaged same day — claude's model is the template, opencode/codex
  converge on it).** Target flow: TUI launch → preflight the backend's creds
  → if missing/bad, prompt reauth in-TUI (claude.ai vs console picker) →
  store locally (`client/cred`) → seed the per-session Secret → GC with the
  session. Already true for claude (verified 2026-07-04): secure local store
  with Keychain/file backends; per-session Secret seeding with account
  labels + reconcile on connect (`internal/k8s/backend.go:396`
  `syncSessionCredential`); Secret deleted alongside Sandbox+PVC on destroy
  AND on create-rollback (`backend.go:726,742` `deleteSessionResources`,
  idempotent). The gaps, in order:
  1. **Launch-time preflight + in-TUI reauth (NEW — the headline).** Connect
     today checks runner health only; a bad anthropic credential surfaces as
     a failed turn. Constraint: subscription setup-token expiry is opaque
     (`client/cred/store.go:100`), so "creds are good" needs a cheap
     host-side live probe, not an offline decode — wire it into the §6
     read-side `--check` machinery + dashboard auth strip rather than
     inventing a second checker. On failure at launch/create: enter the
     account picker in a "reauth" stage (picker + claude.ai/console stages
     already exist, `account_picker.go`) instead of failing the launch.
  2. **Device-code flow — investigate, then decide.** Subscription auth
     shells to host `claude setup-token`
     (`internal/cli/auth_accounts.go:30-47`; host-binary dependency, flagged
     in §7b item 4). Codex already chose device-auth for ChatGPT. Determine
     whether an Anthropic device-code flow is supported for claude.ai
     subscription tokens; if not, keep setup-token as the documented
     mechanism and have the reauth stage drive it (wrapped status quo).
     Console accounts stay paste-a-key.
  3. ~~Secret GC for out-of-band deletion~~ — **done 2026-07-12** (done
     log): ownerReferences (Secret+PVC → Sandbox) set on create-owned
     resources only, best-effort, reconcile-safe; `kubectl delete sandbox`
     now cascades.
  4. **Isolation contract (DECIDED — implement via §7a items 1/3).** No
     shared cross-provider Secret: each backend's key lives in the
     per-session Secret, seeded from `client/cred` for the selected provider
     only, fail-closed. Retires `opencode-credentials` — an
     `ANTHROPIC_API_KEY` must never ride a shared opencode Secret once
     per-account claude creds exist. This item is the cross-backend
     contract; the opencode mechanics live in §7a.
- [ ] **Auth status — remaining read-side scope.** Core landed in
  `internal/authstatus` (offline per-agent report behind `sandbox auth
  status`). Three gaps, independently shippable:
  (a) **dashboard strip rendering** — CLI-only today, nothing in
  `internal/tui/dashboard` consumes the report;
  (b) **`--check` live pings** — codex plan/rate-limit via app-server, and
  provider-key liveness; today every check is offline;
  (c) **the Claude check reads env only, not the credential store** —
  `internal/authstatus/providers.go:119-149` inspects env vars while the real
  source of truth is `client/cred` (multi-account store), so a
  Keychain-stored account reports as unconfigured.
  (c) is the smallest and the most wrong; do it first. **Verification:**
  `internal/authstatus` tests are hermetic via `noEnv()` (done log 2026-07-27)
  — add cases with a populated `cred.Store` and assert the report flips.
- [x] **Codex transport spike — COMPLETE (2026-07-06, containerized).**
  `codex app-server --listen ws://127.0.0.1:PORT` (fixed port, loopback, no
  auth needed, `/readyz`+`/healthz` free) on the PLAIN npm build — standalone
  install NOT needed (managed daemon = cloud-relay pairing, not our
  transport); 2nd-client observer CONFIRMED (notification broadcast +
  `thread/read`; key on notifications, not `thread/list`). Full results in
  the plan's "Spike results (2026-07-06)". Residual for Phase 2: authed live
  turn-observe + refresh-ownership decision.
- [ ] **Close the observer event-coverage gap — both directions.** *(Rewritten
  2026-07-27: the old text said "codex runner-as-metrics-observer, same pattern
  as opencode's", which is stale — codex already observes more than opencode in
  two dimensions.)* Measured coverage of the normalized event set:
  - `runner/src/codex-observer.ts` emits `turn.{started,completed,failed,interrupted}`,
    `tool.{started,completed,failed}`, `usage.updated` — **missing** `message.*`
    and `session.title`.
  - `runner/src/opencode-observer.ts` emits `session.{created,started,updated,idle,error,title}`,
    `message.updated`, `turn.{started,failed,interrupted}` — **missing**
    `tool.*` and `usage.updated`.

  Per the §7 parity bar the runner is the metrics source for every backend, so
  both gaps matter. Codex's usage already maps `thread/tokenUsage/updated`
  (`codex-observer.ts:209`) — mirror that shape for opencode, and add codex
  message/title mapping from its app-server thread notifications.
  **Verification:** `docs/backend-conformance.md` is the per-backend contract —
  extend its table and assert each backend's emitted set there.

## 7) Cross-backend parity (operational)

**Parity bar (maintainer 2026-06-24):** startup speed, detach + keybindings,
prompt/affordance UX, and surfaced metrics must be similar across all agents;
per-agent in-pane rendering can differ. The runner is the control plane and
metrics source for every backend. See the codex plan "Parity bar".

### 7a. OpenCode auth persistence / validation (2026-07-04 triage)

> **2026-07-20 direction change — IMPLEMENTED 2026-07-21 (see the checked
> item below + done log):** provider credentials moved to
> harvest-from-local-opencode + per-session Secret seeding
> (`openspec/changes/opencode-multi-provider-auth/`). The shared
> `opencode-credentials` Secret is now the explicit fallback; items below
> that harden the shared-Secret path still apply to that fallback. Original
> trigger (now resolved pending live verify): `CreateContainerConfigError`
> (cluster Secret has only the Zen key; default provider wanted
> `anthropic-api-key`).

*(A full task-by-task implementation plan exists at
`docs/superpowers/plans/2026-07-04-opencode-credential-manager.md` — local-only,
gitignored; the decisions below are self-sufficient without it.)*

**Fable review (2026-07-04): direction approved — Opus-executable in the
order listed.** Landed so far (done log): selected-provider-only fail-closed
key injection, freshness/rotation stamps, secret-handling + reaper-RBAC
hardening, README auth section. Decisions that still govern the open items:
**(1)** do NOT build an OpenCode-specific store — generalize `client/cred`
with a provider dimension first (that is §6's write-side item) and make item
1 below consume it. **(2)** The connect preflight bar (item 2) is
Secret-presence + key-shape for the *selected* provider, fail-closed; live
provider/model pings belong behind `sandbox auth status --check`, never on
the connect path. The cross-backend contract these decisions implement
(preflight → reauth → local store → per-session Secret → GC, one provider per
Secret) is §6's "Unified per-backend credential lifecycle" item — read that
first.

- [x] **Implement OpenCode credentials — DONE 2026-07-21** — harvest local
  `auth.json` + per-session Secret seeding; detail in the done log.

- [ ] **Validate OpenCode provider auth before/at connect.** `sandbox auth
  status` only reports local env vars (`internal/authstatus/providers.go:119-149`),
  while connect waits for runner health + `opencode serve` readiness only
  (`client/session.go:217-221,301-312`). Add a cluster-aware check for the
  selected provider key and, if feasible, a lightweight model/provider liveness
  probe before launching/attaching.
- [ ] (SMALL) Namespace the remaining Sandbox/pod ClusterRole grants — the
  reaper `secrets: get` move to a namespaced Role landed; the follow-up is
  noted in the k8s manifest.

### 7b. Flox/Nix-first runner environment (2026-07-04 triage)

> **Standing direction (maintainer, reaffirmed 2026-07-27): Flox — or Nix
> beneath it — is the preferred primary package manager for this project going
> forward, everywhere in the chain: host toolchain, CI, containers, and session
> pods.** Everything in this section is the *how*, not the *whether*. When a
> new tool needs to exist somewhere, the default answer is a pinned Flox env,
> not an `apt-get`/`npm -g` line in a Dockerfile. The open questions are
> sequencing, image size, and cache hosting — not direction.

**ADR ACCEPTED WITH AMENDMENT 2026-07-07** —
[`docs/runner-package-manager-adr.md`](docs/runner-package-manager-adr.md):
**first spike `ghcr.io/flox/flox` as the base image** (everything above the
OS from one pinned Flox env — node, sshd, sqlite, opencode; flox ≥ 1.13 for
`flox run`; acceptance gate = Depot build + sshd/PVC host keys +
better-sqlite3 compile + kind conformance), falling back to the ADR's
Debian+Flox-layer option only if the spike hits a wall. Env/mount seam,
substituters (home = ceph S3 w/ egress CIDR carve-out; Tigris = public/OSS
cache), re-sign-at-publish-gate, no shared /nix mount in pass 1, age-based
pruning all stand as written. Nix-built OCI = pass 2 via flake container
outputs; FloxHub CLI publish via Depot CI can land independently. Decided
regardless: the activation hook's `go get .`
(`.flox/env/manifest.toml:54-60`) goes — it mutates go.mod/module cache as a
side effect of `cd`.

**2026-07-22 assessment — SUPERSEDED 2026-07-27; its central claim was wrong.**
It stated that "session pods run default-deny egress, so `flox activate` / `nix`
cannot fetch from a substituter at activation", and concluded from that a
pre-seeded `/nix` must be BAKED into the image and that "no low-risk partial
exists". **Measured from inside a live claude-pane session pod on 2026-07-27:
`downloads.flox.dev`, `cache.nixos.org`, `proxy.golang.org`, and the Debian
mirrors all answer 200.** `apt-get install ./flox.deb` (flox 1.13.2, the ADR's
`>= 1.13` bar) then `flox activate` pulled the full pinned toolchain — go
1.26.3, just 1.51.0, git 2.54.0, golangci-lint 2.12.2 — into a ~2.6 GB `/nix`,
after which `just fmt-check|vet|build|typecheck|lint|test|sdk-conformance|
verify|e2e` ALL passed in-pod (0 failures, including the httptest packages
CLAUDE.md flags).

What that changes and what it doesn't:

- **Changes:** baked closures are now a *startup-latency and image-size*
  requirement, not a connectivity one — and a low-risk partial DOES exist, since
  flox can be installed and activated in-pod without touching the base image.
- **Does not change:** the acceptance gate is still a Depot build + kind
  conformance (unverifiable from a laptop), a 2.6 GB activation-time pull is not
  a cold-start story, and the runner image (`runner/Dockerfile`) remains
  `node:24-slim` with no flox/nix.
- **Caveat:** one pod, one cluster, one point in time. The egress allowlist is
  exactly the kind of thing that changes — re-measure before relying on it.

One concrete bug found while measuring — tracked as its own item in §10
("`just gen` drift check reports a false positive in a session pod").

- [ ] **Spike the flox-base image, then implement the rollout** (items below
  are the ADR's work breakdown, kept for pointers):
  - Runtime bootstrap env/mount seam: extend the common pod env
    (`internal/k8s/backend.go:1244-1277`) with package-manager preference,
    cache dirs, binary-cache config, optional `/nix`/Flox mounts, preserving
    the existing `/session` PVC + SSH mounts (`backend.go:1185-1241`).
  - Propagate Flox/Nix preference to agent child processes: the claude-pane
    child's strict env allowlist (`runner/src/claude-pane.ts:138` — new vars
    must be added there explicitly); OpenCode inherits env
    (`runner/src/opencode.ts:248-253`); inject PATH/cache/config env + agent
    guidance (prefer project Flox env → `flox run` → `nix run nixpkgs#…`).
  - Update runner-image CI triggers (`.depot/workflows/build-runner-image.yml:12-20`
    — build context is `runner/`, root-level `.flox`/`flake.nix` are outside
    it) and host-tool checks (`opencode attach` needs host `opencode`,
    `claude setup-token` needs host `claude`; package in Flox or `just
    doctor` reports the gap).
  - Kubernetes Nix/Flox cache strategy: baked closures first; `NIX_CONFIG`
    substituters/trusted keys via the env seam; egress allowlist opening;
    anti-poisoning publish gate (follow-on design) + pruning story.
  - ~~Remove the `go get .` activation hook~~ — **done 2026-07-12** (done log).

- [ ] **Distribution side: flake outputs, binary-cache hosting, FloxHub
  publish** (folded in from the inbox 2026-07-27 — same decision as the ADR,
  not a separate one). Today `flake.nix:20-33` packages **only the Go CLI**.
  Three pieces, each already has a home in the accepted ADR:
  - **Container outputs from the flake** = the ADR's deferred option 2
    (Nix-built OCI, pass 2).
  - **Binary-cache hosting** = the ADR's §4a substituter decision — home is
    ceph S3 with an egress CIDR carve-out; Tigris was the candidate for the
    public/OSS cache.
  - **FloxHub publish via Depot CI** = the distribution channel on top; the
    ADR notes this **can land independently** of the base-image spike, so it
    is the cheapest first move here if the spike stays gated.
  Proposals for all three:
  [`docs/archive/decision-proposals-2026-07-06.md`](docs/archive/decision-proposals-2026-07-06.md)
  §2.3/§2.8.

### 7c. OpenCode operational items

- [x] CLI `opencode` `--model`/`--provider`/`[prompt]` — done 2026-07-12
  (done log): provider threads to `CreateOptions.OpencodeProvider`
  (fail-closed); the prompt positional is delivered as a headless first
  turn via the turn adapter pre-attach (hard error, never silently
  dropped). NOT yet live-verified on a cluster: create → headless turn →
  `opencode attach --continue` picking up the in-flight turn.
- [ ] **Verify detach + surrounding chrome behave identically for every
  backend's external pane.** The two pane transports differ by construction —
  claude-pane is a WebSocket to the runner, opencode/codex are child-process
  PTYs (`internal/tui/dashboard/pane_transport.go`) — so detach has two code
  paths that can drift. Bindings live in
  `internal/tui/dashboard/keymap.go` (`Detach`, `esc`) with the pane-scoped
  escape cascade in `external_pane.go`; the existing regression pin is
  `TestAppExternalPaneEscIsForwardedNotDetached` (self-skips without a PTY —
  see CLAUDE.md). **Verification:** for each of the three backends, attach,
  confirm esc reaches the child rather than detaching, confirm the documented
  detach key returns to the list with the pod still running, and confirm the
  status/footer chrome renders the same fields.
- [~] **Live-session verify sweep — opencode (2026-07-06 headless pass on
  my-cluster, zen provider, free big-pickle):** (a) **busy/idle status:
  CONFIRMED live** — `session.status_changed busy→idle` streams at turn
  boundaries. **Title: NOT verifiable headless** — the turn adapter creates
  opencode sessions with an explicit placeholder title
  (`opencode-turn.ts:487,649`), and no `session.title` event fired within
  ~60s post-turn; opencode's auto-retitle may be skipped for pre-titled
  sessions. STILL OPEN: verify title via the real TUI path (opencode-created
  session through `opencode attach`) — maintainer eyeball, or investigate
  whether the adapter should create sessions WITHOUT a title so retitle
  fires. (b) clickable spots — still needs interactive TUI eyeball, not
  automatable headless.
- [x] Observer `interruptedTurns` leak bounded — done 2026-07-12 (done log):
  cap 8, oldest-first eviction; regression test.
- [x] **Reasoning double-`message.completed` fixed — done 2026-07-12** (done
  log): root cause = opencode `ReasoningPart` streams content in the same
  `.text` field as `TextPart`, so its deltas were mis-registered as
  assistant text and the idle flush re-emitted them; reasoning part ids now
  tracked, deltas routed to `reasoning.delta`, flush guarded (both
  orderings pinned). Live re-verify at next natural occurrence.
- [ ] **Diagnose live: opencode looks stuck after disconnect/reconnect
  (maintainer report 2026-07-04; recipe below). 2026-07-06 live probes
  (my-cluster) EXONERATED the event layer:** (i) SSE dropped mid-flight
  during a 45s bash tool → the turn ran to completion server-side and
  reconnect with `after=<seq>` replayed contiguously incl. `tool.completed`,
  `turn.completed`, and the `idle` status flip
  (`firstReplay=dropSeq+1, contiguous=true` — three separate runs); (ii) an
  idle SSE stream survived a 6-min window on 30s heartbeats (90s watchdog
  never fired); (iii) the 409 active-turn gate behaved correctly under
  client churn. So a "stuck" display after reconnect is NOT missing replay —
  remaining candidates narrow to (1) provider rate-limit/retry invisibility
  under real load and (3) upstream `opencode attach` rendering a stale
  in-flight tool (our PTY mirrors its bytes). Capture recipe below still
  applies at next natural occurrence.
  Symptom: sometimes, after detaching, a session appears frozen; on reconnect
  the pane shows the same file-read in flight far longer than plausible;
  possibly correlated with opencode-spawned subagents. Same day the
  continuously-attached tab showed the session recover and FINISH — so this
  is a stall or stale display, not a deadlock. Offline review found no defect
  that would stall `opencode serve` itself: the observer correctly gates
  child-session events (`opencode-observer.ts:150-151`) and the reviewed fix
  above already terminalizes observer stream drops. Candidates, most→least
  likely: (1) provider rate-limit/retry backoff during subagent fan-out —
  invisible in our UX because the observer surfaces no retry/rate-limit
  signal for opencode (contrast claude's `rate_limit.updated`); (2) pod CPU
  throttling under parallel subagents (check pod resource limits); (3)
  upstream `opencode attach` rendering a stale in-flight tool after
  reconnect (our PTY path just mirrors its bytes). Next occurrence, capture
  in order: (a) is the stuck tool in opencode's own pane or in our dashboard
  row/status (upstream vs our event model); (b) `sandbox trace` /
  `sqlite3 events.db` — a `tool.started` without matching completion, and
  wall-clock gaps in event `Time` during the window (real stall = no events
  for minutes; display bug = events flowing); (c) `kubectl logs` of the pod
  for provider retries; (d) `kubectl top pod` for throttling. If (1)
  confirms: observer-side retry/backoff surfacing → new event + statusline
  chip (§2b pattern). If (3): file upstream at sst/opencode.
- [ ] **Per-backend CLI smoke.** `internal/k8sit/cli_smoke_test.go`
  `TestCLISmoke` is opencode-only; make it table-driven over `backendCases`
  (gate the non-empty-output assertion on `expectRealReply`) so claude/codex
  fill the column.
- [x] **OpenCode window as modal over the dash — DECIDED 2026-07-07: no.**
  Full-screen external pane stays (modal PTY = constant reflow churn on a
  client we don't control + "whose chrome wins" ambiguity); parity
  investment goes to identical detach chrome + status strip instead (the
  verify item above).

## 8) Public SDK / client API — ALL DECIDED 2026-07-07, now an implementation backlog

Decisions from the live proposal review (archive/decision-proposals-2026-07-06.md §6).
Breaking changes OK pre-OSS; update `sdktest/` pins in the same change.
Suggested batching: one tui/* PR (Register + palette + Finished + B-tier);
one client-behavior PR (destroy ordering + DialRunner); the interface,
naming-break, and Shell items each stand alone.

**2026-07-27 principle conformance.** The maintainer's three principles are now
a durable reference ([`docs/design-principles.md`](docs/design-principles.md))
and the whole public surface was reviewed against them
([`docs/review-principles-2026-07-27.md`](docs/review-principles-2026-07-27.md),
ids `[P1-#]`/`[P2-#]`/`[P3-#]`). Principle 3 came back clean — the normalized
model carries no k8s type and the exceptions (`rest.Config`, namespace/image
knobs) are decided keeps, so there is no §3 work. The items below are the
principle-1 (transport) and principle-2 (TUI) gaps, in dependency order.

**Scope narrowed 2026-07-27 (maintainer): consumers reach the cluster through
the kube-api.** This retired most of the principle-1 list before it started.
The port-forward is a *subresource of the kube-api*
(`internal/k8s/portforward.go:381-386`) dialed from the same `*rest.Config`
(`:324,:330`), so all four channels — runner HTTP+SSE, pane WS, sshd, Mutagen —
inherit whatever the kubeconfig points at. Teleport (`tsh`/`tbot`), bastions,
and API-server proxies therefore **work today** via
`WithKubeconfig`/`WithContext`/`WithRESTConfig`, with nothing to build.
`[P1-2]` `[P1-3]` `[P1-5]` `[P1-8]` are consequently **not defects** — they are
the work-list for a possible future *bypass* transport (Teleport app/TCP
straight to 8787, ingress, Tailscale sidecar) and live in the review doc, not
here. `[P1-4]` (`ssh.InsecureIgnoreHostKey`, `client/shell.go:163`) is the
security tripwire on that future work: correct today because the hop really is
loopback, but any bypass transport must make it transport-conditional **in the
same change**. Do not start any of it without the maintainer taking up the
bypass option.

- [ ] **[P1-1] + [P1-6] Make `client.Backend` externally implementable — one
  change, not two.** *(Justification revised 2026-07-27: not transport — that's
  handled by the kubeconfig — but **consumer testability**. An external consumer
  building on this SDK cannot fake the cluster to test their own app; we use
  exactly this seam for ours, `client/orchestration_test.go:126,145`.)*
  Three types block it, not the one the docs claim:
  `session.PortSpec` + `session.ForwardHandle` (`client/client.go:109`) and
  `k8s.ReaperOptions` (`:114`). The first two are plain declarations
  (`internal/session/types.go:507-514`) and only need adding to the alias block
  at `client/client.go:31-50`; `ReaperOptions` needs a real public type.
  **But do not export alone:** `Connect` indexes handles *positionally*
  (`handles[0]` `client/session.go:417`, `handles[2]` `:548`) against an
  ordering documented only on the internal helper
  (`internal/k8s/portforward.go:438`). An external implementer would compile
  fine and misroute opencode traffic to the sshd port. Change the seam to
  **named** endpoints in the same change, then correct the undercounted caveat
  at `client/client.go:84-87` and `sdktest/surface_test.go:15-18`, and add an
  sdktest pin where an *external* type satisfies `client.Backend` (that pin is
  the proof the principle holds). Folds in **[P1-7]**: export the standard
  forward specs too, since public paths already call the internal helpers
  (`client/client.go:865`, `client/session.go:400-404`).

- [ ] **[P1-0a] Hand the terminal back for a mid-session credential refresh.**
  Found while validating the Teleport flow. Interactive kubeconfig `exec:`
  plugins (`tsh kube credentials`) are fine at *startup* — client-go runs them
  lazily at first request, and the CLI finishes its cluster work before
  `tea.NewProgram` (`internal/tui/dashboard/app.go:376,392`), so the prompt gets
  a real terminal. The gap is **expiry while the dashboard is running**: it
  makes live cluster calls for its whole lifetime (`List`/`Watch`/`Suspend`/
  `Resume`/`Destroy`, `internal/tui/dashboard/actions.go:24-30`, plus background
  observer port-forwards and forward re-resolve), and a `tsh` cert TTL is easily
  shorter than a left-open session — so the plugin prompts underneath an
  alt-screen raw-mode program and the auth silently fails or corrupts the
  display. **The pattern already exists:** subscription login hands the terminal
  to a child via `tea.Exec`
  (`internal/tui/dashboard/account_picker.go:301-310`, wired `app.go:477`).
  Apply the same handover to a credential-refresh failure. Small, self-contained,
  and it makes `tsh` a first-class path rather than one needing tbot to work
  around.

- [ ] **THE PANEL WORKSTREAM — `[P2-2]`+`[P2-3]`+`[P2-4]` merged with §2e
  `[L5]`/A/F. The big one.**

  > **Decided 2026-07-27 (maintainer):** the public-consumability extraction
  > and the premium-feel polish happen **as one pass**, not sequentially. "As
  > we extract/reconfigure the way we build our panels and UI components so
  > they can be more easily consumed by an API consumer, we should also take
  > that time to make sure they are visually and overall up to snuff."
  > This resolves the collision recorded in the preamble: §2e workstreams
  > **A** (dialog stack), **F** (ultraviolet/overlay collapse), and **`[L5]`**
  > (panes as floating modals) touch the *same* code as this item and are
  > folded in here. §2e **C/D/E/A4** are independent of layout and stay put.

  **Why they had to merge.** §2e A rewrites the overlay dispatch at
  `model_render.go:122-166` — which is *inside* the `render()` this item
  dissolves (`model_render.go:91`, 729-line file). §2e F collapses the dual
  overlay systems at `zones.go:50-105`, the compositing layer any panel
  abstraction sits on. `[L5]` needs both a modal system (A's output) and a
  pane widget (`[P2-4]`'s output). Landing them separately means rewriting the
  same file two or three times, and — the real risk — §2e alone has no reason
  to *export* its new abstraction, so it would produce another internal-only
  layer that this item then has to redo.

  **The work.** The would-be panels are `*Model` methods, not constructible
  components: `renderSessionRow:278`, `renderDetailLines:395`,
  `renderConfirm:594`, `renderConvertModal:615`, `renderHelp:661`, plus
  `feed.go`, `dirpicker.go`, `backend_picker.go`, `account_picker.go`. Extract
  each into something that takes its data through an interface and renders at
  a caller-chosen size; express composition as a value the caller can rebuild
  rather than a fixed concatenation order; build the dialog/modal stack and
  the overlay collapse **as part of that shape**, exported from the start; then
  publish. **This closes `[L6]`** — a pane viewer (vt emulator +
  key/paste/mouse encoding, today `external_pane.go`/`key_encode.go`) is simply
  one of the panels, and `[L5]`'s floating modal is that panel composed rather
  than fullscreened.

  **Acceptance — both halves, or it isn't done:**
  1. *Consumability:* an external consumer can build this dashboard minus one
     panel, plus a panel of their own, at a size they choose — pinned in
     `sdktest/` (if the pin can't be written, the seam isn't real). No `tui/`
     package imports `internal/`.
     2. *Visual:* the result is at least as good as today's dashboard at
     80x24 / 100x30 / 140x40, in both light and dark themes, with
     `anim.ReduceMotion` honored — goldens updated deliberately, never
     rubber-stamped. This is the checkable form of "premium feel", which §2e
     never had.

  *Encouraging:* `[P2-5]` — the entry seams are already interface-shaped
  (`Run(backend, connector, creator, opts...)`, `app.go:373`) and the types
  crossing them (`session.Ref`/`ID`/`State`) are already aliased in `client`,
  so re-typing to `client.*` is identity. Budget the effort in the extraction
  and the visual pass, not the promotion.

  **Read first:** [`docs/design-principles.md`](docs/design-principles.md)
  §2 (the bar), [`docs/tui-premium-plan.md`](docs/tui-premium-plan.md) §A/§F
  (the design detail for the folded-in pieces — note **§B is OBSOLETE**, the
  transcript it describes was deleted), and
  [`docs/review-2026-07-20.md`](docs/review-2026-07-20.md) §L5.
  **Plan the whole cluster before writing code** — this is the one item in the
  file where a piecemeal start is actively harmful.

- [ ] **[P2-7] + [P2-8] State (or finish) the `tui/theme` concurrency
  contract.** `tui/kit` moved its palette behind an `atomic.Pointer`
  specifically so "multiple tea.Programs sharing this process never race"
  (`tui/kit/palette.go:4,35`) — but its upstream `theme.ApplyTheme`
  (`tui/theme/theme.go:199`) writes ~40 plain package globals (`:107-158`) plus
  `activeTheme`/`changeHooks`/`themeEpoch` (`:161,166,184`) unsynchronized, and
  is what calls `kit.SetANSITable`/`SetComponentColors` — so kit's hardening is
  defeated one layer up. The single-goroutine contract lives only in a comment
  on an unexported var, while the package doc advertises "reusable across TUI
  applications" (`theme.go:7-11`). Also `OnChange`'s unsubscribe writes
  `changeHooks[idx] = nil` (`:176`) unguarded. Either state the contract in the
  package doc and on `ApplyTheme`/`Register`/`OnChange`, or finish the
  hardening to match `kit` — the current split is the thing that's wrong.
  Same edit should document `[P2-8]`: one palette per *process* is a real
  customization ceiling (two differently-themed consumers can't coexist) and
  should be a stated decision, not a surprise found at integration time.
  Independent of every other item here.

- [ ] **[L6] Decide: public pane-viewer widget? (2026-07-20).** *(2026-07-27:
  the principle-2 decision answers this — a pane viewer IS in scope; fold this
  into the `[P2-2]` panel work above rather than deciding it separately.)* The pane
  *transport* is fully public + pinned (`Session.AttachPane`, `PaneStream`
  w/ Resize, sentinels — `sdktest/surface_test.go:108-128`), but the
  *presentation* layer (vt emulator wiring, key/paste/mouse encoding,
  `internal/tui/dashboard/external_pane.go`) is internal; `tui/terminal` is
  caps/kitty/osc helpers, not a screen widget. External consumers get raw
  bytes with no viewer. Decide against the parity/importability bar whether
  to promote a pane-viewer building block or document as deliberately out.

- [x] **Narrow public `client.Backend` interface — done 2026-07-11** (done
  log): 12-method interface (exactly the orchestration call sites),
  `WithBackend` takes it, concrete backend pinned by assertion + sdktest.
  NOT yet externally implementable — `EnsureReaper` names
  `internal/k8s.ReaperOptions`; export/replace that type when a third-party
  backend is real (documented in the interface comment).
- [x] **De-Claude coordinated break — done 2026-07-12** (done log):
  `ApprovalPolicy` enum (wire strings unchanged), `Connection.External`/
  `ExternalCreds`, `State.AgentSessionID` (+ index Load migration),
  D9 folded in (`State.Activity` for runner turn-activity; `Status` is
  k8s-only). Wire break ⇒ protocol v2 + actionable mismatch advisory
  (pinned). Live pod round-trip unverified.
- [x] **`Session.Shell` + `SSHTarget` seam — done 2026-07-12** (done log):
  SSH-only forward + per-session key as the reusable primitive; one-call
  PTY shell atop it (raw mode, SIGWINCH, remote exit code); CLI shell is a
  thin wrapper (transport moved k8s-exec → in-pod sshd); sdktest pins.
  Live interactive path unverified against a real pod.
- [x] tui/theme `Register(Theme)` + `Denied`/`*Subtle` tone vars — done
  2026-07-12 (done log).
- [x] tui/kit palette race → whole-palette `atomic.Pointer` snapshot — done
  2026-07-12 (done log; -race hammer test).
- [x] tui/list dead `Item.Finished()` dropped (+ sdktest pin) — done
  2026-07-12 (done log).
- [x] client: `Destroy` sync-before-destroy reorder — done 2026-07-11 (done
  log); pinned by the F3 call-order spy.
- [x] client: `DialRunner` forwards the runner port only — done 2026-07-11
  (done log); pinned by TestDialRunner.
- [x] kit.FormatTokens `B` tier with boundary promotion — done 2026-07-12
  (done log).
- [x] WithStateDir ssh-dir layout — DECIDED via the §9 worktree sign-off
  (4.10): move `ssh/` INSIDE the state root in the same pre-OSS break that
  adds the worktree root; implement with the worktree Spec-split change.
  `client/sync.go`.
- [~] **Pod bootstrap files + generic env/secret injection** — PARTS A+B DONE
  (B `d6e55fa` 2026-07-21; A 2026-07-22, done log); only the operator-prose
  README section for env/file injection remains of the docs closeout.
  - [x] **Part B — `CreateOptions.ExtraEnv`/`ExtraSecretEnv` (d6e55fa):**
    plain pod env + per-session-Secret-backed secret env; fail-closed
    validation against the exported `k8s.IsReservedEnvName` denylist
    (SANDBOX_* prefix + RUNNER_TOKEN/PROJECT_PATH/credential vars),
    invalid-name/cross-map-dup/512KiB-cap sentinels; Optional SecretKeyRefs
    (non-bricking) + re-create reconcile; sdktest pins. Riders (a) redaction
    and (c) pane-allowlist admit done via the `SANDBOX_EXTRA_ENV_NAMES`/
    `SANDBOX_EXTRA_SECRET_ENV_NAMES` markers. `just check` green.
    **DECISION FLAGGED FOR MAINTAINER:** ExtraSecretEnv is **agent-visible**
    (not stripped) — required for the PAT-for-git use case; rationale +
    revert path in the design doc Status block + SECURITY.md.
  - [x] **Part A — `CreateOptions.BootstrapFiles` (2026-07-22):** operator
    files materialized in pod `$HOME`/`/session/state` before the agent starts
    (NOT the synced workspace); reuses part B's per-session-Secret plumbing +
    the codex materialize hook (write-if-changed, per-file seed-hash sidecar,
    same seed-precedence as codex). One Secret key per file (`bootstrap-<n>`) +
    a `bootstrap-manifest` JSON key for path/mode, projected read-only+Optional
    as a Secret volume (`SANDBOX_BOOTSTRAP_DIR`); summed-size cap 256 KiB at
    Create. Fail-closed path validation (absolute/`~/`, `path.Clean`, strictly
    inside $HOME/`/session/state`, unique, four exported sentinels), re-checked
    runner-side. `client/client.go`, `internal/k8s/backend.go`,
    `runner/src/bootstrap.ts` + boot wiring, `sdktest` pins; `just check` green.
  - [~] **Docs closeout (rider b + skills README):** rider (b) FQDN-egress
    example DONE (2026-07-22) — commented operator-endpoint block (gitlab.com)
    with the "opening egress for a tool also opens its token's exfil path" note
    in `k8s/networkpolicy-egress-fqdn.yaml.example` (the SECURITY.md prose half
    landed with d6e55fa). Skills half DONE (2026-07-22) — README section for the
    ALREADY-BUILT `ConfigInputsSubs` (`internal/sync/sync.go`) one-way sync of
    `~/.claude/{skills,agents,commands,hooks,statusline}` + project `.claude/`
    on the project sync. Operator-injection README section **DONE 2026-07-27**
    (done log): new "Injecting your own config into sessions (Go SDK)" section
    covering all three fields with a compile-checked example, the two things an
    operator gets wrong (ExtraSecretEnv is deliberately agent-visible;
    BootstrapFiles are rejected inside the synced workspace), the sentinel list,
    and the egress cross-reference. **Residual:** a live verification of the
    config sync, not code.
  - Part C (operator binaries) stays documentation (derived-`--runner-image`
    pattern; tool-image initContainers only if that proves painful).

**2026-07-18 SDK-example review additions** (from auditing
`client/example_test.go` against the full surface):

- [x] **`OpencodeProvider*` constants aliased into `client` — done
  2026-07-18** (done log): three re-exports + sdktest pin; CreateOptions
  doc reworded to the public spellings.
- [x] **`Example_chat` full chat-loop example — done 2026-07-18** (done
  log): compile-only example covering permissions, deltas, tools,
  steering, reattach/replay, account selection, detach-vs-destroy.
- [x] **Cluster watch in the SDK — done 2026-07-18** (done log):
  `Client.Watch` + public `StateEvent` (type moved to `internal/session`);
  dashboard consumes `session.StateEvent`, dropping four `internal/k8s`
  imports.
- [x] **Model limits/pricing public — done 2026-07-18** (done log):
  `internal/models` → `client/models` (git mv + doc.go + sdktest pins).
- [x] **tui/chat completed + sdktest pin added — done 2026-07-18**: the public
  transcript-item vocabulary is now production-complete — the empty renderers
  (tool/user/notice/shell/subagent) were replaced with the derived-from-dashboard
  cards, and `ReasoningItem`, `TodosItem`, `PermissionItem`, `Citation`, the
  `Bullet`/`Quote` chrome, and `ToolArg`/`ToolSummary` helpers were added. Each
  item is width/ANSI/grapheme-safe, focus-aware, expansion/collapse-aware,
  version-cached with theme-epoch invalidation, and free of `internal/` types.
  `sdktest/chat_surface_test.go` pins the exported vocabulary + a
  compile-and-render conformance test (80x24/100x30/140x40, scroll/follow/expand/
  resize/theme-swap); golden frames live in `tui/chat/testdata/golden/`;
  `cmd/chatdemo` is the public-package-only runnable example. The higher-level
  interactive layer is now DONE too — see the next item.
- [x] **Public TUI importability goal COMPLETE — done 2026-07-18**: drove
  `docs/archive/public-tui-importability-goals.md` (T1–T8) to green. Added the
  `tui/chat` turn-footer item (`FooterItem`/`TurnFooter`) + item-level goldens
  (streaming/fatal/empty) + list scenario tests (resize/append-while-scrolled),
  then the higher-level public packages: `tui/transcript` (the `Apply(client.Event)`
  event→transcript reducer — tool pairing, subagent routing, streaming
  coalescing, todos, permissions, replay dedup, unknown-event degradation, follow/
  focus/expansion, host callbacks for submit/approve/deny/interrupt/steer/detach),
  `tui/composer` (multi-line input, queue-while-busy steering, escape cascade,
  grace-gated permission answering), `tui/picker` (model/backend/account selection),
  and `tui/chrome` (status line, context/token gauge, working indicator honoring
  `anim.ReduceMotion`, calm notices). `cmd/chatdemo` now drives the transcript +
  composer from a scripted `[]client.Event` (no hand-assembled items); `sdktest`
  pins every surface + `TestTranscriptFromPublicEvents` (event-sourced conformance,
  six goldens). No `tui/` package imports `internal/`; `just check` green.
  *(Historical record — claude-pane-first then deleted `tui/chat`,
  `tui/transcript`, and `cmd/chatdemo` on 2026-07-20 with the custom claude
  renderer they served (see §2/§3); the surviving public `tui/` surface is
  kit / list / picker / anim / theme / composer / chrome / terminal.)*
- [ ] **Remaining client-level capability gaps** (overlaps
  `docs/public-api-importability-plan.md`, which is TUI/auth focused):
  session titles/rename are `internal/index`-only
  (`internal/cli/rename.go:24`); `Client.SyncStatus` returns raw bytes with
  the conflict/orphan classification stuck in `internal/sync`
  (`internal/cli/sync_support.go:90`). Both need API design (index
  promotion vs a title field on the runner; a typed SyncStatus) before
  dispatch.

## 9) Unbuilt features

- [x] **T10 — working-directory picker — done 2026-07-21** (done log):
  directory stage FIRST in the create overlay (cwd preselected — enter-enter
  keeps the old muscle memory; ≤5 recents via new
  `Index.RecentProjects(limit)` injected through `RunOptions.RecentProjects`;
  free-text row w/ ~-expansion + Tab completion), `CreateParams.ProjectPath`
  threaded through `beginCreate` so every create path inherits it, CLI-side
  Creator re-validates fail-closed (`creatorProjectPath`); shared
  normalization extracted to `internal/projpath` (resolveProjectPath
  delegates, behavior-identical). CLI commands keep pure-cwd semantics.
  Live overlay feel wants a maintainer eyeball.
- [x] **Host statusline in the pane — done 2026-07-21** (done log): the
  provisioned statusline script chains, first hit wins:
  `pane-observer/user-statusline` (pod drop-in) →
  `../statusline/user-statusline` (host-synced) → `sandbox-user-statusline`
  on PATH (future flox bin); ~1s timeout
  (`SANDBOX_STATUSLINE_TIMEOUT_MS`), metrics POST initiated FIRST and exit
  still gated on it; missing/non-exec falls through, ran-but-failed/empty
  → builtin. Host side: ConfigInputsSubs gains `statusline/` — a SIBLING
  of runner-owned `pane-observer/`, so host sync can never touch the
  observer token (pinned by test). Runner tests execute the real script.
  - [x] **PATH branch lit up — done 2026-07-24:** `claude-statusline`
    vendored to `runner/statusline/` (nested Go module, stdlib-only,
    byte-identical to nix-config bar a provenance header) and built by a
    `golang:1.26-bookworm` stage in `runner/Dockerfile` into
    `/usr/local/bin/sandbox-user-statusline` — the chain's 3rd candidate, so
    a host-synced override still wins. Went to the image rather than the flox
    env: the binary must exist in the pod, which is what the image builds.
    Verified by running the REAL extracted `STATUSLINE_SCRIPT` against the
    REAL binary — chained (not builtin) in 0.34 s vs the 1 s budget.
    - [ ] **Live gate (unverified):** no local Docker daemon, so the image
      build is untested until CI; confirm in a live session that the pane
      shows the full line.
    - [ ] **Usage rows (lines 2–3) are inert in-pod** and render nowhere:
      `accessToken()` reads `$HOME/.claude` (HOME=/root) but creds live at
      `CLAUDE_CONFIG_DIR=/session/state/claude`, so it bails before any HTTP
      call — load-bearing, since the pane credential is inference-scoped and
      a located token would only buy a doomed 3 s request against the 1 s
      chain budget. The seam if we want them: the binary already reads
      `/tmp/claude-statusline-usage-cache.json` (60 s TTL), so the host —
      which *can* read usage — could push JSON there. Needs a runner route +
      CLI push; not started.
- [ ] **Worktree/branch terminal ergonomics — T1-T5 (maintainer ask,
  2026-07-25).** Five items designed in conversation and never written down;
  recovered 2026-07-26 from the session transcript and recorded here so no one
  has to read it again. **The driving complaint:** a converted branch cannot be
  checked out in the main repo, because the session's own worktree already
  holds it — `fatal: 'feat/pick-small-task-from-todo-list' is already used by
  worktree at '…/remote-sessions/worktrees/claude-pane-df80e6-3e1d6e81'` — and
  nothing in the CLI lists worktrees, finds one by a name a human remembers, or
  prints a path you can `cd` to. `sandbox worktree` hosts only `gc`
  (`internal/cli/worktree.go:26-34`, whose own doc comment says "Today it hosts
  `gc`").

  **The keystone already landed:** `client.ResolveSessions` /
  `ResolveSession` (`6d3f42f`, `client/resolve.go:169,:213`) fuzzy-resolve a
  human-typed query to a session. `SessionMatch` (`:91`) carries
  `ID/Title/Backend/ProjectPath/WorktreePath/Branch/LastActivity/MatchedBy`;
  match kinds rank id → worktree-path → branch → id-prefix → title →
  project-path → any (`:77`), tie-broken by `LastActivity`. `ResolveSessions`
  never errors on no-match (empty slice — completion must stay silent);
  `ResolveSession` returns `*AmbiguousSessionError` with candidates (`:137`)
  when the top kind is tied. T1/T2 are thin consumers of it — do not rebuild
  resolution.

  **Decisions already settled — implement, don't relitigate:**
  - **ID is identity, title is a mutable label.** Already the design
    (`sandbox rename <session-id> <name>`, `internal/cli/rename.go`). Fuzzy
    match over both, always resolve to the ID.
  - **Never rename a worktree directory.** Its name is a live cross-machine
    contract, not cosmetics: the pod bind-mounts the workspace at the *same
    absolute host path* (`client/sync.go:284-289`). A rename would
    simultaneously break mutagen's alpha path (baked in at create), the pod's
    bind-mount match, transcript path-keying for resume, git's
    `.git/worktrees/<name>/gitdir` admin entry, `index.Entry.WorktreePath`, and
    the `sandbox/<id>` branch. Titles are LLM-generated and non-unique anyway —
    **never put a title in a path.**
  - **The TTY rule shapes the CLI.** `cd $(sandbox worktree path …)` runs
    stdout through a pipe, so any interactive picker MUST render to **stderr**,
    read keys from **/dev/tty**, and put *only* the resolved path on
    **stdout**. Get this wrong and `cd $(…)` either eats escape codes or can't
    draw at all.
  - **Picker is `tui/picker`, not gum.** gum is bubbletea underneath, so
    `tui/picker` is the same machinery without the subprocess hop — and it is
    already public and themed.
  - **Continuity is deprioritized** (maintainer, 2026-07-26): gap (1) of the
    2026-07-21 item below is explicitly *not* wanted right now.

  **STATUS 2026-07-26: all five implemented** (this batch). Summary per item
  below; what shipped, in one place: `tui/picker` gained opt-in
  `WithFilter()`/`Query`/`SetQuery`/`Filtered` (T3); `internal/cli/
  worktree_path.go` is the new `worktree path` (T1); `internal/cli/
  completion.go` carries `completeSessionArg` + `sandbox completion` (T2);
  `internal/cli/worktree_convert.go` is the headless convert and
  `docs/session-lifecycle.md` "Finishing a session's work (merge back)" is the
  merge-back doc incl. the checkout gotcha (T4); `ReapOptions` gained
  `MinAge`/`ReapUnlanded`/`BaseBranch` and `ReapedWorktree` a `Reason`, with
  the classify→list→confirm→act flow in `runWorktreeGC` (T5). Unverified:
  the interactive `/dev/tty` paths (picker + gc confirm) have no automated
  coverage — they need a human at a terminal.

  - [x] **T1 — `sandbox worktree path [query]` — done 2026-07-26.** Prints a session's worktree
    dir so `cd $(sandbox worktree path pick-small)` works. Thin consumer of
    `ResolveSessions`. Rules, per the TTY decision above: `--json` is always
    structured and never interactive; 0 matches → error; 1 → print the path; N
    + `/dev/tty` available → picker on stderr; N + no tty → error listing the
    candidates. The per-session path is already exposed via
    `Session.WorktreeStatus` (`client/worktree.go:380,:393` — `Path`, live
    `Branch`, `Dirty`, `Changed`), and `SessionMatch.WorktreePath` carries it
    without a git call. Empty `WorktreePath` = non-git or `WorktreeOff`
    session; say so rather than printing the repo root.
  - [x] **T2 — shell completion — done 2026-07-26.** No `ValidArgsFunction` existed anywhere in
    `internal/cli` today. Wiring one over `ResolveSessions(ctx, "")` gives zsh
    completion to **every** session-taking command — `attach`, `destroy`,
    `rename`, `suspend`, `resume`, `cancel`, plus T1 — not just the new one.
    Add a `sandbox completion` subcommand (cobra generates the script) for the
    maintainer's home-manager config. No new dependencies. Completion must
    never block or prompt: `ResolveSessions` is TTY-free and returns an empty
    slice rather than an error, which is exactly why it was built that way.
  - [x] **T3 — type-to-filter in `tui/picker` — done 2026-07-26.** `Item{ID,Name,Desc,Current}`
    (`tui/picker/picker.go:29-34`) and `New`/`WithChoose`/`WithCancel` exist,
    but there is **no filter or query field** (`:48-54`), so typing `pick-small`
    to narrow is impossible today. Add a filter matching over ID+Name, threaded
    through `Update` (`:106`) and `View` (`:145`). Public-package improvement:
    the dashboard's own overlays inherit it, and it is what lets T1 use
    in-process bubbletea. Pin any new exported surface in `sdktest/`.
  - [x] **T4 — `sandbox worktree convert` + the merge-back docs — done 2026-07-26.** Convert was
    TUI-only today (`b` modal, `internal/tui/dashboard/worktree.go:120-254`),
    so nothing about the flow is scriptable or headless.
    `Session.ConvertToBranch` is already public
    (`client/worktree.go:450`, `ConvertOptions` `:417`, `BranchResult` `:425`),
    so this is a thin Cobra wrapper taking `--branch`/`--message`; map the
    existing sentinels (`ErrInvalidBranchName`, `ErrBranchNameTaken`,
    `ErrWorktreeDirty`, `ErrNoWorktree`) to useful exit messages. Pair it with
    the missing "finishing a session's work" section covering: live view via
    the worktree dir, snapshot via convert, then merge/rebase/push — this is
    also gap (2) of the 2026-07-21 item below, so writing it closes both. Homes:
    `README.md:329` Commands table + a subsection under
    `docs/session-lifecycle.md:87` ("Per-session git worktrees").
    **Document the checkout gotcha explicitly** — the branch is held by the
    worktree, so `git checkout` fails, but `git diff`, `git merge`, and
    `git push` against that branch all work fine from the main repo. That
    misunderstanding is what started this whole thread.
  - [x] **T5 — `worktree gc` retention + interactive confirm — done 2026-07-26.** Wanted:
    prune old worktrees on age / last-commit / session-existence, with a
    confirmation listing exactly what will be deleted. **This is a semantics
    change, not just new flags:** `ReapOptions` carries only `DryRun`
    (`client/worktree.go:556`), and the sole reap trigger is "session no longer
    live in the cluster" — which directly conflicts with the maintainer's
    requirement that **worktrees should persist past their agent session**.
    Needed: (1) age/last-commit criteria — `index.Entry` already has
    `CreatedAt`/`LastActivity` (`internal/index/index.go:96-97`), plus
    `git log -1 --format=%ct <branch>` and dir mtime; (2) a **retain-if-unlanded
    rule** — never auto-reap a branch holding commits unreachable from the base
    branch (a better signal than age alone); (3) an interactive confirmation
    listing the victims before mutating, reusing the `ReapedWorktree` report
    shape (`:563`). Note the live-session gate is also load-bearing for safety
    (`docs/audit-2026-07-18.md` :76-77 — a cross-cluster `gc` can reap a running
    session's worktree); do not weaken it while adding retention.

  Tests: `client/resolve_test.go` (extend), `worktree_test.go`,
  `tui/picker` unit tests, CLI flag/TTY-branch parsing. Run
  `go test ./client/ ./internal/cli/ ./tui/...` — `client` and `internal/cli`
  need the command sandbox disabled (httptest ports).
- [ ] **Worktree continuity + merge-back UX (maintainer ask, 2026-07-21).**
  *(2026-07-26: gap (1) continuity is deprioritized by the maintainer; gap (2)
  merge-back docs is now owned by T4 above. Gap (3) is the live remainder.)*
  What EXISTS (verify-then-build-on, don't rebuild): destroy is
  capture-then-remove — dirty WIP is committed to the session's
  `sandbox/<id>` branch before the worktree is removed
  (`client/worktree.go` teardownWorktree; `BranchResult.CommitSHA`), so
  destroying a session never loses work — it survives as a branch in the
  main repo; `ConvertToBranch` (dashboard `b` modal, title-derived
  prefill) renames it humanly. The GAPS the maintainer feels: (1) **no
  continuity** — a new session always gets a fresh worktree from the base
  branch; there is no "start a session ON branch X" to pick up prior work
  (incl. a destroyed session's `sandbox/<id>` branch). Fix sketch: extend
  the `--worktree` flag vocabulary with `continue:<branch>` (and a
  creator-overlay branch picker fed by `git for-each-ref
  'refs/heads/sandbox/*'` + converted branches) → `client/worktree.go`
  creates the worktree from that ref instead of the base; validate the
  branch isn't checked out elsewhere. (2) **merge-back is undocumented** —
  the recipe (detach/destroy → `b` convert or `sandbox worktree` →
  `git merge`/PR from the converted branch) exists but appears nowhere in
  README/session-lifecycle.md; write it as a short "finishing a session's
  work" section. (3) **destroy should offer the convert** — when Destroy
  captures dirty WIP, surface "converted-name?" (the modal exists; wire it
  into the destroy path) instead of silently leaving `sandbox/<id>`.
  Tests: worktree_test.go continue-from-branch cases; CLI flag parsing.
  Run `go test ./client/ ./internal/cli/` (client needs the sandbox
  disabled). The session↔worktree tie itself is sound — the branch, not
  the session, is the durable unit; these three make that visible.
- [x] **Per-session git worktree lifecycle — IMPLEMENTED 2026-07-11** (done
  log; design archived to
  [`docs/archive/worktree-lifecycle-design.md`](docs/archive/worktree-lifecycle-design.md)
  with a layout amendment in its Status block): waves 1-4
  (`b84f696`..`d59690c`) — `Spec.WorkspacePath` split + `SANDBOX_PROJECT_ROOT`
  discovery; `WorktreeAuto` default worktree at Create with rollback;
  capture-then-remove Destroy + `ReapWorktrees`; `WorktreeStatus`/
  `ConvertToBranch`; dashboard `b` convert modal (title-derived prefill) +
  `sandbox worktree gc` + `--worktree` flag; `ssh/` nested into the state
  dir with one-time migration (closed the §8 WithStateDir item). Fixes
  §1d's sync collision for git projects. Residuals: non-git same-path
  collision warning still open (§1d, code TODO in Connect); B2
  move-session-to-machine not built (B1 shipped); WIP/convert commits are
  `--no-gpg-sign` by design.

## 10) Harness / tests / docs / ops

- [ ] **`just gen`'s drift check reports a false positive inside a session
  pod** (found 2026-07-27 while measuring in-pod egress for §7b). The recipe
  regenerates correctly, then gates on
  `git diff --exit-code -- internal/session/eventtypes.gen.go runner/src/events.gen.ts`
  (`justfile:45-46`). In a per-session worktree the `.git` file points at a
  **host** path that does not exist in the pod — git fails by design (this is
  exactly what `runner/src/workspace-guide.ts` tells the agent) — so `git diff`
  exits non-zero and the `||` branch prints "generated files are stale" even
  when nothing drifted. **This is the one stage of `just check` that cannot run
  in a session pod**, which matters now that everything else can. Fix
  direction: probe git usability first (e.g. `git rev-parse --git-dir`) and
  distinguish "git unavailable → skip the drift gate with a visible note" from
  "git works and output drifted → fail". Do not silently skip — a gate that
  quietly passes is worse than one that noisily fails.
  **Verification:** run `just gen` in a session pod (expect a skip note, exit
  0) and on the host with a hand-edited `*.gen.*` file (expect the stale error,
  exit 1).

**2026-07-20 onboarding/newcomer review** (full walkthrough narrative in
[`docs/review-2026-07-20.md`](docs/review-2026-07-20.md) §O — what the docs
do well is recorded there too; fix in roughly this order):

> *(The two 2026-07-20 parked part-done worktrees — [O3] and the
> [O1]/[O7]/[O8]/[O9] docs draft — were harvested, re-verified against
> current main, finished, and removed in the 2026-07-21 seven-agent batch;
> the out-of-scope `client/sync.go` diff turned out to be a legitimate
> 2-line comment fix of the same [O9] hedge and was kept.)*

- [x] **[O1] done 2026-07-21** (done log): root `--help` example, Long
  text, and package doc all pane-first (no positional prompt; real backend
  set named).
- [x] **[O2] done 2026-07-20 (1dbf495):** CLAUDE.md rewritten for
  pane-first — intro, runner file table, event-model wording, session
  lifecycle, and command tree all match architecture.md and the code.
- [x] **[O3] done 2026-07-21** (done log): presence-only host-login probe
  (darwin keychain exit-code, no `-w`; keychain answer FINAL when
  `security` exists, mirroring `cred.SystemMaterial` exactly; else
  credentials-file stat, `CLAUDE_CONFIG_DIR` exclusive) is the PRIMARY
  claude source in `auth status`; env vars demoted to headless-only and
  render Degraded/yellow; doctor headline + remedy rewritten ("log in with
  `claude` on this machine (Max mode)"), shared-Secret wording deleted.
  Injected exec/stat seams; no real `security` calls in tests.
- [x] **[O4] done 2026-07-21** (done log): `sandbox doctor` leads the
  Quickstart + first Commands row; the two doctors (host readiness vs
  `just doctor` dev-env toolchain) explicitly disambiguated in both spots.
- [x] **[O5] done 2026-07-21** (done log): `k8s/reaper-namespace.yaml`
  (restricted PSS) + `k8s/networkpolicy-reaper-ingress.yaml` (agent-reaper
  → session pods :8787 only) shipped, apply order fixed, "sessions never
  auto-suspend" consequence stated. NOT applied to a live cluster —
  restricted-PSS admission + netpol semantics reasoned from source.
- [x] **[O6] done 2026-07-21** (done log): README Commands row marked
  **experimental**, documenting BOTH halves of the credential contract
  honestly — per-session ChatGPT-OAuth auth.json is SDK-only today, the
  CLI always uses the shared `openai-api-key` fallback (verified: nothing
  in internal/cli|tui populates CodexAccountID/CodexAuthJSON) — plus the
  degraded-attach caveat. Command stays visible.
- [x] **[O7] done 2026-07-21** (done log): k8s/README positional prompt,
  `sandbox-claude-sdk` label, and shared anthropic-credentials guidance
  all corrected (verified against `labelAppName`/`buildEnv`).
- [x] **[O8] done 2026-07-21** (done log): runner-api examples claude-sdk
  → claude-pane; retired-id note extended (what a lingering claude-sdk pod
  still serves, per `selectAgent`).
- [x] **[O9] done 2026-07-21** (done log): architecture.md worktree hedges
  now state shipped behavior; matching 2-line `client/sync.go`
  worktreesRoot comment fix; session-lifecycle path gained the missing
  `remote-sessions/` segment.
- [x] **[O10] done 2026-07-21** (done log): dev/local README claude
  section pane-first (host-login harvest; env-token flow relabeled legacy
  claude-sdk-only; hidden `turn` noted).
- [x] **[O11] done 2026-07-21** (done log): CONTRIBUTING "The `openspec/`
  references" section — states plainly that openspec/ is the maintainer's
  LOCAL planning workspace, untracked (`.git/info/exclude`), absent from
  clones by design, and that durable outcomes land in `docs/`.
- [x] **[O12] done 2026-07-21** (done log): one story everywhere — README
  Testing + CONTRIBUTING both describe the Justfile `check` recipe's actual
  ten stages and name CI (`.depot/workflows/ci.yml` runs `just check`
  verbatim); CONTRIBUTING's recipe list gained sdk-conformance/verify/e2e
  and `just build`'s description was corrected (whole module).
- [x] **[O13] done 2026-07-20 with [L1]:** the sentinel and the store-account
  error both carry "log in with `claude` on this machine" remediation.
- [ ] **[O14] README hero GIF predates the pane UI** — re-record after the
  live pass (was already noted in 7.4; now has an id).
- [x] **[O15] Retired-backend residue sweep — done 2026-07-25** — detail in
  the done log.

- [ ] **Go-runner rewrite watch item** — investigation complete
  ([`docs/go-runner-rewrite-investigation.md`](docs/go-runner-rewrite-investigation.md)):
  gated on live gates 2.5/8.2 + soak; pre-work available now: build the
  language-agnostic runner conformance suite, run the two 30-minute
  node-necessity verifications (§2 of the doc). *(The third pre-work bullet,
  dropping the dead `@anthropic-ai/sdk` dep, was already done by §1f [S4] on
  2026-07-20 — re-verified 2026-07-27: `runner/package.json` has four runtime
  deps, none of them that one, and zero imports remain.)*

**2026-07-07 test-coverage additions** (two agents; detail in
[`docs/review-2026-07-07.md`](docs/review-2026-07-07.md) §F, id in brackets):

- [x] **[F3] client orchestration covered — done 2026-07-11** (done log):
  fake `Backend` + fake mutagen `Runner` sharing one call-order log;
  table tests over Create/Status/List/Suspend/Resume/Destroy/DialRunner;
  the Destroy spy pins sync-terminate → destroy → local-state-removal (and
  index preservation on backend failure). Residual: `Session.Connect`'s
  runtime path itself still has no fake-backed test (needs a fake
  RunnerClient/health seam) — fold into [F6]'s `waitHealthy` item.
- [x] **[F4] `server.ts` HTTP layer covered — done 2026-07-11** (done log):
  `createRunnerServer` extracted (listen-free seam); 17-test `node:test` suite
  boots the real router + real sqlite event log — bearer auth, 404s, every
  409 `turnRejectReason` path, SSE `after=` replay incl. the B5 clamp, B9
  typed 400s. Residual promoted below.
- [x] **e2e fake-runner faithfulness — done 2026-07-12** (done log): fake
  mirrors server.ts auth ordering, 409 set, 400/413 bodies, after=
  validation + B5 clamp; unmodeled routes 501 loudly;
  TestE2EFakeRunnerFaithfulness pins 16 shapes.
- [x] **Oversized-body 413 now reaches clients — done 2026-07-12** (done
  log): `readBody` stops destroying the socket; rejects once, drains,
  route's catch flushes the mapped 413. Pinning test flipped to assert the
  413 body.
- [x] **[F5] port-forward lifecycle covered — done 2026-07-11** (done log):
  retry decision extracted pure (`classifyForwardReconnect` +
  `nextForwardBackoff`) + table tests over every branch and the full backoff
  ceiling; C1 Close-seam invariants (Done-after-Close, done-closes-once,
  error-churn vs concurrent Close) pinned under `-race`.
- [x] **[F6/F7] MED coverage — done 2026-07-12** (done log): cred-rotation
  warning, `waitHealthy` (healthChecker seam), `Session.Connect` pre-dial
  branches, `evaluateIdle` full branch table; §7c double-emit/leak pins
  landed with their fixes. STILL OPEN residuals: `Session.Connect`'s happy
  path + `reaperTick`'s wrapper glue need a runner-factory injection seam
  on `Client` (documented in `client/health_connect_test.go`); the
  dashboard `model_sse.go` command closures remain untested (excluded from
  the batch — dashboard was under the §2a refactor).

- [x] **docs shape gaps — done 2026-07-12** (done log): runner-api.md
  healthz body / 409 table / interrupt empty-segment; README auth+sync-gc+
  opencode flags; LAUNCH-CHECKLIST HEAD claim fixed; HARDENING-BACKLOG
  marked provenance-only (verified zero true TODO overlaps).
- [ ] **Ops: new CLI-created sessions use `:latest` and can hit the stale
  traefik manifest cache.** `client.DefaultRunnerImage`
  (`client/client.go:172`) is a moving tag, and the pull-policy derivation
  (`internal/k8s/backend.go:292`) correctly maps a moving tag to `Always` — so
  a stale registry-proxy manifest, not the policy, is what serves an old
  image. The **resume** path was already fixed by digest pinning
  (`internal/k8s/backend.go:73`, done log); create was not. Fix direction:
  either resolve the tag to a digest CLI-side at Create (same mechanism as
  resume) or bust the traefik cache. **Verification:** create a session
  immediately after publishing a new runner image and confirm the pod's
  `image:` is the new digest.
- [x] **`sandbox doctor` — done 2026-07-12** (done log): 10-check host
  readiness table (cluster checks can FAIL and short-circuit; binaries/
  creds/images advisory WARN/INFO with remediation); PASS paths of the
  cluster checks unverified against a live cluster.
- [ ] **Research: NVIDIA AICR (github.com/nvidia/aicr) home use cases
  (maintainer, expanded 2026-07-07).** Maintainer works on AICR at work
  (GPU-cluster focus) and wants to find homelab use cases for components
  that require multiple pieces of configuration synced up together.
  Candidate fits worth evaluating: the per-session Secret+PVC+Sandbox trio
  (note the KRO decision — [`docs/archive/kro-composite-adr.md`](docs/archive/kro-composite-adr.md)
  rejected adding a controller dependency for this; the same bar applies),
  the §7b substituter/egress/cache config bundle, and non-sandbox homelab
  components. Research note only; no code until a concrete use case earns
  it.
- [x] **KRO — DECIDED 2026-07-07: not adopting.** ADR archived
  ([`docs/archive/kro-composite-adr.md`](docs/archive/kro-composite-adr.md));
  the §6 item-3 ownerReferences fix (Secret+PVC → Sandbox) is now unblocked
  and immediately executable (~10 lines in `CreateSession`).
- [~] **Observability first cut landed** — dependency-free spans behind
  `SANDBOX_TRACE=1` / `sandbox --trace`: connect/create phases incl. the
  backgrounded flush/inputs/reaper under one correlation id (`client/trace.go`);
  runner turn lifecycle (first message / first delta / settled + msg count,
  `runner/src/trace.ts`). Fable-verified 2026-07-06. 2026-07-13 (done log):
  connect id ↔ turn id bridged across the HTTP seam (`X-Sandbox-Trace-Id` →
  `turn.link` in the pod log); runner boot spans (`index.ts`, socket-accept
  anchored); `SANDBOX_TRACE` now documented in `docs/architecture.md`
  (Observability section). STILL OPEN: pod-ready sub-phases (schedule vs
  pull vs ready — the big §5 unknown); SSE first-event latency; pane WS
  RTT (§4 "Pane transport RTT probe", 2026-07-21 — same family, keep
  output formats consistent). ~~the §1d observer-cap model remains absent
  from `docs/architecture.md`~~ — **FIXED 2026-07-27:** new "Observer streams
  and the fleet cap" subsection under Event model covering the ~30-forward
  apiserver limit that motivates the cap, the admission-vs-eviction split, the
  coldest-by-recency victim rule, and why `NeedsInput` protection is
  unseen-output-gated (the [H1] no-op).

**2026-07-24 observability review** (ids `[T#]`; full audit + the staged
proposal behind them in
[`docs/observability-design.md`](docs/observability-design.md), status
draft/awaiting sign-off). The findings below are verified against `a0ed573`
and are each actionable on their own — none of them is gated on accepting
that design. Headline: three trace formats, two log formats, zero metrics,
all of it line-oriented text nothing can ingest.

- [~] **[T1] CLI debug log — FILE SINK DONE 2026-07-27 (done log); deep
  instrumentation still open.** `attachDebugFileSink`
  (`internal/cli/debug.go`) writes `--debug` records to
  `~/.local/share/sandbox/remote-sessions/<id>/debug.jsonl`, appended across
  commands and stamped with the session id; `afterTUIForSession` installs it for
  the TUI entry points as the ONLY sink (file, not tee — a stderr write under
  the alt-screen is what corrupts the UI). Failures are advisory, printed before
  the TUI starts. Lifecycle instrumentation added at the CLI's own
  suspend/resume/destroy call sites. 4 tests in `debug_test.go`; runner-api.md
  updated. **STILL OPEN:** the rest of the instrumentation list — port-forward
  establish + each reconnect, health-check attempts, sync create/flush,
  credential resolution — lives in `client/`, `internal/k8s`, and
  `internal/sync`, none of which can reach `internal/cli`'s unexported `dbg`.
  That needs a **logging seam on the public client package** (a `slog.Logger` or
  handler option on `client.New`), which is a public-API design call, not a
  mechanical edit — decide it alongside §8's SDK surface items. **Also
  unverified:** the item's own verification is a live `--debug` dashboard run
  leaving a parseable file with no terminal corruption; the unit tests pin the
  file contents and the no-stderr property, but the end-to-end run needs a
  cluster.
- [x] **[T2] runner structured logging — done 2026-07-27** (done log; closes
  C10): new `runner/src/log.ts` (level/ts/component/sessionId, `child()` for
  traceId), all 32 `console.*` sites across 12 files migrated, `no-console`
  added to the runner ESLint config as the durable guard. Text is the DEFAULT
  format (`kubectl logs` is the primary reader) with structure as trailing
  `key=value`; `SANDBOX_LOG_FORMAT=json` switches to ndjson and
  `SANDBOX_LOG_LEVEL` gates both. The HTTP surface — which logged exactly one
  line ever — now logs every request on finish with method/path/status/duration,
  level-graded (debug 2xx / warn 4xx / error 5xx) so a busy pod stays readable,
  and carries `X-Sandbox-Trace-Id` as `traceId` when the caller stamped one.
  `trace.ts`'s spans keep their documented `trace: …` envelope via the single
  `logRaw` exemption. 9 tests in `runner/test/log.test.ts`; runner-api.md
  rewritten. **Enables [T7]** (`sandbox logs`), which needs a log to read.
- [x] **[T3] dead `startTurnTrace` deleted — done 2026-07-27** (done log):
  the function, its `TurnTrace` interface, the `NOOP` const, and the three
  tests that were its only callers. `traceTurnLink`/`traceIDFromHeader`/
  `startBootTrace` stay (all wired); shared options type renamed
  `TurnTraceOptions` → `TraceOptions`, and the module header + `traceTurnLink`
  doc corrected — both still described `turn.*` milestone lines that no longer
  exist. `just typecheck` clean, trace suite 6 pass.
- [x] **[T4] pane trace correlation — done 2026-07-27** (done log): both ends.
  Go `AttachPane` (`internal/runner/pane.go`) now sets `X-Sandbox-Trace-Id` on
  the WebSocket handshake, like every other runner request; the runner's pane
  upgrade reads it (query-param `traceId` fallback for clients that cannot set
  handshake headers) through the same `traceIDFromHeader` validation and stamps
  it on `pane attached` / `pane detached` records via the [T2] logger's
  `child()`. 2 tests pin the header and that an unset id sends nothing rather
  than an empty value. **Live verification still owed:** the item's check is one
  `sandbox --trace attach` followed by grepping the connect id onto pane lines —
  needs a cluster, and the runner half needs an image rebuild (batch with §0b).
- [ ] **[T5] No metrics exist anywhere, including data already on the
  wire.** No `/metrics` route (`runner/src/server.ts` serves healthz /
  observer / sessions / status / idle / events / turns / interrupt /
  exec); no otel or prometheus dep in `go.mod` or `runner/package.json`;
  `internal/k8s` never touches `metrics.k8s.io`. Meanwhile
  `usage.updated` (`schema/events.json:134-143` — token counts +
  `totalCostUsd`), `rate_limit.updated` (`:144-156` — 5h/7d/Opus/Sonnet
  utilization), and `IdleStatus` (`turnActive`/`attachedClients`/
  `idleSince`, polled by the reaper) are all collected and then discarded
  after render. Fix: the D6 catalog in the design doc — fleet cost/tokens
  are derivable CLI-side today with no pod change. Verify: fleet cost for
  one session matches its real spend.
- [~] **[T6] PVC fill — PART (a) DONE 2026-07-27 (done log); part (b) awaits its
  data.** `sampleStorageStats()` (`runner/src/events.ts`) reports
  `eventLogBytes` (main + `-wal` + `-shm` — the WAL dwarfs the main file between
  checkpoints, so counting only the latter under-reports exactly when growth is
  fastest), `eventLogRows`, and `pvcFreeBytes`/`pvcTotalBytes` from
  `statfsSync(STATE_DIR)` using `bavail` (not `bfree` — reserved blocks are not
  writable by the runner). `startStorageGauge()` in `index.ts` logs it once at
  boot and every `SANDBOX_STORAGE_GAUGE_MS` (default 15m, 0 disables) through the
  [T2] logger; every failure degrades to 0 rather than throwing. 4 tests in
  `runner/test/storage-gauge.test.ts`. **Part (b) unchanged and still open** —
  whether `RETENTION_MAX_EVENTS` should default non-zero — and it should be
  decided from a real session's numbers, which now exist to be read. Needs a
  runner image rebuild to reach live sessions (batch with §0b).
- [ ] **[T7] No way to see runner logs without `kubectl`.** The command
  tree (`internal/cli/root.go:96-113`) has no `logs`, and pod stdout is
  ephemeral — it is not on the PVC, so a restart destroys the record of
  why it restarted. Fix: a runner log route + `sandbox logs <session>
  [-f]` over the existing port-forward, and write the runner's log to
  `/session/state/sandbox/` so it survives restarts. Depends on [T2].
  Verify: kill a pod mid-session, resume, read the pre-restart logs.
- [~] **[T8] `audit.jsonl` — CAPPED 2026-07-27 (done log); the reader half is
  still open.** The unbounded-growth half is fixed: `runner/src/audit.ts`
  rotates to `audit.jsonl.1` at `SANDBOX_AUDIT_MAX_BYTES` (default 8 MiB, `0`
  disables), one generation retained, so worst case is 2× the cap. Rotation
  happens after the write so the triggering row is never split or dropped, and
  the byte counter is seeded from the file at boot so a frequently-restarting
  pod still rotates. New `createAuditWriter` seam + `runner/test/
  audit-rotation.test.ts` (7 tests); architecture.md + SECURITY.md updated.
  **STILL OPEN — the reader:** the log is still never surfaced host-side and
  never synced, so it remains a file nobody can read. Fix direction unchanged:
  sync it home with the transcript group (`internal/sync/sync.go:332-334`) and
  give it a reader (a `sandbox audit <session>` subcommand, or fold it into
  [T7]'s `sandbox logs`). Verify: a host-side command prints a live session's
  audit rows.
- [ ] **[T9] Latency blind spots — the paths most likely to be slow are
  the ones with no clock in them.** `runner/src/claude-pane.ts` contains
  no clock read at all (the whole frame path is untimed); observer
  ingestion is unmeasured to the point that §4 `[P6]` proposes `kubectl
  logs | rg -c observer` as its measurement recipe; `appendEvent`
  (`runner/src/events.ts:353`) sits on the critical path of every event
  via the append-before-stream invariant and is untimed; SSE first-event
  latency is wanted at `internal/runner/pane_rtt.go:32`; steady-state
  Mutagen sync and port-forward reconnect frequency
  (`classifyForwardReconnect`/`nextForwardBackoff` back off with no
  counter) are both uncounted. Fix: histograms, **not** spans, on the
  frame path (a span per frame is unbounded; see the design doc's D5
  rule). Verify: `[P6]`'s open question gets answered with a number.
- [ ] **[T10] Local OTel stack (the design doc's deliverable).** Write
  telemetry as newline-delimited OTLP/JSON so the Collector's
  `otlpjsonfilereceiver` can ingest it, sync it home on the existing
  one-way transcript rail (`internal/sync/sync.go:332-334`,
  `TranscriptSubs` at `:241`), and stand up a `dev/observability/`
  compose stack that is off unless asked. **Start with stage 1** —
  `events.db` → OTLP spans — because `session.started`/`turn.started`/
  `tool.*` with timestamps is already a span tree, so it yields full
  turn-level tracing retroactively with zero pod-side code and zero
  hot-path risk. Open decisions (docker-compose vs flox-native services;
  reuse `SANDBOX_TRACE` vs a new switch; rotation caps) are listed in the
  design doc and need maintainer sign-off first. Verify: a fixture
  `events.db` renders as a correctly-parented trace in Tempo.

- [~] **Visual-testing gaps (2026-07-13 review; re-scoped 2026-07-20 after
  claude-pane-first deleted the transcript surfaces) — motion/theme/size
  axes CLOSED 2026-07-21 (done log); eyeball harness still open.**
  - [x] *Mid-motion golden frames — done 2026-07-21* (done log):
    `withMotionRender` (motion ON, `nowFunc = goldenFixedNow+offset`);
    `TestGoldenRowEnter` {0,90,200ms} pins the row fade,
    `TestGoldenStatusFlash` {0,150,350ms} the status pulse — frames
    verified genuinely distinct + deterministic (`-count=3`).
  - [x] *Theme axis — done 2026-07-21* (done log): `TestGoldenDashboard`/
    `TestGoldenFeed` fan out over every registered theme (Midnight/
    Daylight/Ember) as subtests; Midnight goldens are pure renames of the
    old un-suffixed files (byte-identical).
  - [x] *Size axis — done 2026-07-21* (done log):
    `TestGoldenDashboardNarrow`/`TestGoldenFeedNarrow` pin the degraded
    60×20 layout (clean degradation, no panic).
  - [ ] *Animation eyeball harness:* no repeatable way to watch dashboard
    motion without a live cluster — `cmd/tuikit-demo` exercises only the
    public `tui/` packages, and `just dev-tui` (justfile:363) needs the kind
    cluster. Options: a fixture-replay dev mode for the dashboard (needs a
    new event fixture — the transcript-multiturn stream left with the
    renderer; the feed's `[]client.Event` cases in `feed_test.go` are the
    seed), and/or a VHS tape (`nix run nixpkgs#vhs`) recording tuikit-demo →
    gif as a non-gating CI artifact (vhs already noted as a nice-to-have in
    `docs/archive/local-dev-turn-parity-plan.md:159`).

## Open caveats (carry-forward)

- [ ] **Release-notes item: resumable-transcripts migration.** Sessions created
  before the workspace host-path migration have transcripts under the old
  `-session-workspace-…` path, so in-session resume-by-id may fail for them.
  No code fix intended — this is a note to write when release notes are
  drafted. **Done when:** the caveat appears in the release notes; then delete
  this item.

- [ ] **LIVE: rate-limit/usage against a real max/pro session.** The read path
  landed (`rate_limit.updated` → dashboard, done log 2026-07-27) but has never
  been exercised against an account that actually reports limits. Known
  deliberate drops runner-side: `seven_day_oauth_apps` and `extra_usage`
  (overage) — confirm those are still the right calls once real payloads are
  visible. **Verification:** attach a max/pro session, confirm the pane status
  row shows the `5h …% ⟳… · wk …%` segment with plausible values, and confirm
  a never-reported session still shows nothing rather than a fabricated 0%.

- [ ] **`~/.claude/todos` + `~/.claude/tasks` sync — decide keep or drop.**
  Ancillary: not required for resume. `ConfigInputsSubs`
  (`internal/sync/sync.go`) is where it would be added alongside the existing
  `skills`/`agents`/`commands`/`hooks`/`statusline` entries. Low priority;
  needs a call on whether agent-local task state should round-trip to the host
  at all before anyone implements it.

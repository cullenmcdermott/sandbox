# TODO — backlog

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
> **Opus-ready map (updated 2026-07-21 after the seven-agent Fable fan-out
> batch — [S1]/[S3], [L7]+[P3], the pane RTT probe, [O1]/[O3]/[O5]/
> [O7]-[O10] incl. both parked-worktree harvests, §9 T10 dirPicker, and the
> §9 statusline chain all landed; done log):** best first picks, in order —
> §7a = execute `openspec/changes/opencode-multi-provider-auth/tasks.md`,
> §8 pod-bootstrap (maintainer re-raise 2026-07-21 = the sign-off signal;
> confirm the design doc then execute), §10 [O15] residue sweep + §7c
> table-driven CLI smoke (small, related), §9 worktree continuity +
> merge-back UX, §2e workstreams A/C/D/E. Every open item carries pointers
> + a fix direction + a verification line; if one doesn't, that's a bug in
> this file. Rules of the road for Opus: (1) run the tests named in the
> item, not the full gate, per change; run `just check` once per batch
> (command-sandbox caveats in CLAUDE.md); (2) line numbers drift — anchor
> by symbol name and re-`rg` before editing; (3) never touch `openspec/`
> structure, `*.gen.*` files, or memory. Drafted, awaiting maintainer
> sign-off: §7b package-manager ADR, §2e plan-doc workstreams (A-G).
> Maintainer-decision gated: §8 [L6], §2e [L5] floating-modal design.
> Needs the live cluster or the maintainer's eyeball: [L2] repro,
> claude-pane-first gates 8.2/8.3, §5 Spegel, §6 codex live spike, §7c
> verify sweeps, [O14] hero GIF, the [S1] FQDN set hubble-verify, [L7]
> trackpad feel + T10 overlay feel, live reaper deploy ([O5] manifests).

## 0) Inbox — human notes, needs triage

Raw maintainer notes. Triage = either promote into a numbered section with
pointers, or answer inline and archive. (Resolved investigations moved to the
done log.)

* Create nix flake (with binary and container outputs?). Is there a place to
  host nix binary cache, maybe tigris? Also consider publishing to FloxHub as
  a public package (via Depot CI). — *note: a flake exists but only packages
  the Go CLI (`flake.nix:20-33`); "container outputs" intersects the §7b
  package-manager ADR (Nix-built OCI is its deferred option 2), the
  binary-cache hosting question is the ADR's §4a substituter decision, and
  FloxHub publishing is the distribution channel on top — proposals for all
  three in
  [`docs/decision-proposals-2026-07-06.md`](docs/archive/decision-proposals-2026-07-06.md)
  §2.3/§2.8. Standing directive recorded 2026-07-06: Flox (preferably) or
  Nix is the preferred install mechanism everywhere in the chain. Triage
  alongside the §7b sign-off.*

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
- [ ] **[S5] Set pane WS maxPayload + resize bounds when next touching
  `server.ts:174,119-134` (INFO).** Slated to ride the §4 slow-link
  compression change (2026-07-21), which touches exactly that surface.
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
(draft, awaiting sign-off) — five-agent comparative study of Crush
(FSL: **ideas only, never copy code**), ultraviolet, gh-dash, huh (all MIT).
Retargeted at the dashboard/feed after claude-pane-first deleted the
transcript: workstream **B (transcript depth) is OBSOLETE** — skip it when
reading the plan doc; the rest apply to surfaces that still exist (dashboard
modals, session list, feed, theming).

- [x] **"needs input" relabel — done 2026-07-21** (done log): display label
  → "ready" (row label, attention summaries "%d ready"/"%d ready below",
  detail note "ready for your next prompt"); wire string "needs-input",
  Status constant, and the already-calm ❯ `GlyphNeedsInput` untouched;
  goldens unaffected (no fixture renders NeedsInput) — attention tests pin
  the new strings; StatusWaiting semantics unchanged.
- [ ] **[L5] Design pass: external panes as floating modals over the
  dashboard (maintainer idea, 2026-07-20).** Render in-pane clients
  (claude/opencode) as floating modals instead of full-screen takeover —
  fleet context stays visible around the agent surface. Needs: sizing/focus
  semantics, sub-region rendering through the vt emulator, and [P1]
  coalescing first (dashboard renders around a busy pane). Seam already
  exists (`pane_transport.go`); detail in review-2026-07-20.md §L5.

- [ ] **A. Dialog stack manager + async grace period + huh for form dialogs**
  — one `Dialog` interface + stack on App replaces ~8 bespoke overlays and 4
  copies of center/shadow math (`model_render.go:122-166`, `app.go:1009`,
  `app.go:1137`, `backend_picker.go:211`); 200ms/1.5s/500ms grace kills the
  async-permission blind-approve class. Plan §A.
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
- [ ] **F. Ultraviolet phase 1–2 (ADR first)** — bubbletea v2 already
  renders through uv; composing cells ourselves deletes the
  `withBackground`/`bgSeq`/`clampLines` opacity machinery
  (`zones.go:50-105`) and collapses the dual overlay systems. Does NOT fix
  tea.Raw/Kitty (already correct) or child resize seeding. Plan §F.
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
- [ ] **[P7] Feed streaming O(n²) per message (LOW, only if felt).**
  `internal/tui/dashboard/feed.go:198-201`.
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
  perceptible). Execute §1f [S5] in the same change (it's "when next
  touching server.ts").** ANSI redraw streams compress 5-10x, but both
  ends ship compression OFF: `runner/src/server.ts:179`
  `new WebSocketServer({ noServer: true })` has no `perMessageDeflate`,
  and `internal/runner/pane.go:77` dials with gorilla `DefaultDialer`
  (EnableCompression false). Separately, every `pty.onData` chunk becomes
  its own WS frame (`runner/src/claude-pane.ts` onData → `ring.push` +
  `safeSend`). Three parts, one change: (1) server.ts: enable
  `perMessageDeflate` conservatively (`threshold: 512`,
  `zlibDeflateOptions: { level: 1 }`) + [S5]'s `maxPayload` and resize
  bounds (`server.ts:174,119-134`). (2) pane.go: `DefaultDialer` → a
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
- [ ] **`visibleSessions()` re-filters+re-sorts 4+ times per frame**
  (`groups.go`) — measure-first; memoize only if profiling shows it matters.
  (The `partition()` render-path dedup itself landed — done log.)
- [ ] `fitModal` does two ANSI `lipgloss.Width` scans per visible line every
  frame (`render_util.go`; feeds modal fitting + the feed view) — measure
  before optimizing.

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
  reap CLI-side ([V35]). STILL OPEN ([V35] residual): the DASHBOARD reaper
  deliberately does not list paused syncs — its grace logic
  (`internal/tui/dashboard/model_sse.go` `reapOrphans`/`gcRunningSet`)
  protects only Running/Creating, so it can't yet distinguish a suspended
  session's paused syncs from a kubectl-deleted one's; teach it
  suspended-vs-gone before extending the reap.

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
- [ ] **Auth status — remaining read-side scope** (core landed in
  `internal/authstatus`): dashboard strip rendering (CLI-only today);
  `--check` live pings (codex plan/rate-limit via app-server; provider key
  liveness); Claude check should read the credential store, not just env.
- [x] **Codex transport spike — COMPLETE (2026-07-06, containerized).**
  `codex app-server --listen ws://127.0.0.1:PORT` (fixed port, loopback, no
  auth needed, `/readyz`+`/healthz` free) on the PLAIN npm build — standalone
  install NOT needed (managed daemon = cloud-relay pairing, not our
  transport); 2nd-client observer CONFIRMED (notification broadcast +
  `thread/read`; key on notifications, not `thread/list`). Full results in
  the plan's "Spike results (2026-07-06)". Residual for Phase 2: authed live
  turn-observe + refresh-ownership decision.
- [ ] Codex runner-as-metrics-observer (same pattern as opencode's, app-server
  thread notifications).

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

- [x] **Implement OpenCode credentials — DONE 2026-07-21** (done log):
  executed `openspec/changes/opencode-multi-provider-auth/tasks.md` §1-5
  (docs §6 in the same batch) across seven-agent fan-out —
  `f5a6eb0`/`93ce61f`/`4a7f631`/`521839e`/`683c13a`. `sandbox opencode`
  harvests the host's local `opencode auth.json` and seeds it JIT into the
  per-session Secret (`opencode-auth-json` → `OPENCODE_AUTH_JSON` +
  `SANDBOX_OPENCODE_PROVIDER`, codex transport pattern); runner materializes
  it with a per-entry seed-hash refresh-preserving merge + fail-closed gate;
  seeded↔fallback re-create rejected; shared `opencode-credentials` Secret
  demoted to fallback. `--seed-providers` narrows the seed; `opencode auth
  login` passthrough for a missing provider. `just check` green. **Live
  verify on omni-prod still pending (task 6.4):** multi-provider seed
  session + fallback session — the original `CreateContainerConfigError`
  repro. Deferred follow-ups (filed, not blocking):
  - Dashboard opencode creator seeds ALL local providers but collects no
    `--provider` (defaults anthropic) and has no per-provider picker — a
    user whose local login lacks anthropic hits `ErrOpencodeProviderNotSeeded`
    fail-closed at Create instead of the CLI's login prompt. Add a provider
    picker to the create overlay (parity with the account picker) OR default
    the dashboard's opencode provider to the sole/first harvested entry.
  - `stampOpencodeCredsFreshness` still stamps a shared-Secret provider-key
    fingerprint for SEEDED sessions too (`internal/k8s/backend.go`), so
    `warnIfOpencodeCredsRotated` could emit a spurious rotation warning for
    a seeded session if the shared Secret's key changes — gate stamping to
    the fallback path.
  - Multi-*account* per opencode provider (design non-goal) stays deferred.
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

### 7c. OpenCode operational items

- [x] CLI `opencode` `--model`/`--provider`/`[prompt]` — done 2026-07-12
  (done log): provider threads to `CreateOptions.OpencodeProvider`
  (fail-closed); the prompt positional is delivered as a headless first
  turn via the turn adapter pre-attach (hard error, never silently
  dropped). NOT yet live-verified on a cluster: create → headless turn →
  `opencode attach --continue` picking up the in-flight turn.
- [ ] Verify detach (Ctrl+]) + surrounding chrome behave identically for every
  backend's external pane.
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

- [ ] **[L6] Decide: public pane-viewer widget? (2026-07-20).** The pane
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
    on the project sync. **Residual:** a README section for the operator
    ExtraEnv/ExtraSecretEnv + BootstrapFiles injection surface (SDK/operator
    audience) — architecture.md + the design doc cover the mechanics; the
    user-facing README prose is still to write. Plus a live verification of the
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
  - [ ] **Maintainer follow-up (not blocking):** package
    `claude-statusline` into the runner flox env so the PATH branch lights
    up; then a live session shows the host statusline in-pane.
- [ ] **Worktree continuity + merge-back UX (maintainer ask, 2026-07-21).**
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
- [ ] **[O15] Retired-backend residue sweep** — `backend.go:1716`,
  `internal/session/types.go:53`, `runner/src/session.ts:37`,
  `client/account.go:69`, `internal/k8sit/local_test.go:44`; apply the
  `agent.ts:66` retired-id comment pattern or delete dead branches. (The
  stale `--pane` comment in dashboard_connector.go was rewritten with [L1].)
  **2026-07-21 batch additions (discovered during the O-docs harvest):**
  `internal/k8sit/local_test.go:66` still drives `BackendClaudeSDK`
  expecting a real turn (409s post-pane-first when the legacy Secret is
  present — stale conformance row; fold into the §7c table-driven smoke
  rework); justfile ~:158-160/:220-224 comments still claim
  `anthropic-credentials` makes `just dev` claude work; `client.Create`
  defaults empty `Backend` to the retired `BackendClaudeSDK`
  (`client/client.go:459` — also mirrored by the dashboard creator's
  default; decide the new default, likely claude-pane);
  `internal/session/types.go:53` example id `claude-sdk-7f3a`; SECURITY.md
  A1-residual section cites the deleted `runner/src/claude.ts` (should
  point at `opencode.ts`/`codex.ts`).
- [ ] **Go-runner rewrite watch item** — investigation complete
  ([`docs/go-runner-rewrite-investigation.md`](docs/go-runner-rewrite-investigation.md)):
  gated on live gates 2.5/8.2 + soak; pre-work available now: drop the dead
  `@anthropic-ai/sdk` dep from `runner/package.json` (zero imports), build
  the language-agnostic runner conformance suite, run the two 30-minute
  node-necessity verifications (§2 of the doc).

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
- [ ] Ops: new CLI-created sessions use `:latest` and can hit the stale traefik
  manifest cache — bust the cache or pin digests CLI-side. (Resume path
  already fixed via digest pinning — see done log.)
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
  output formats consistent); the §1d observer-cap model remains absent
  from `docs/architecture.md` (doc drift, 2026-07-06 harness audit).

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

- [ ] Resumable-transcripts migration: pre-existing sessions' old
  `-session-workspace-…` transcripts may break in-session resume-by-id across
  the host-path migration → call out in release notes.
- [ ] rate-limit/usage: unverified against a live max/pro session; consider
  pinning the Agent SDK version; `seven_day_oauth_apps` + `extra_usage`
  (overage) are dropped runner-side; black-line/opacity fixes unverified in a
  live attach.
- [ ] `~/.claude/todos` + `~/.claude/tasks` sync is ancillary (not required for
  resume) — keep but low priority.

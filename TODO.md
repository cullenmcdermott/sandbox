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
> **Opus-ready map (updated 2026-07-20 late):** best first picks, in order —
> §10 [O1]-[O12]/[O15] docs+probe cluster (two PARKED part-done worktrees to
> finish, see the §10 intro), §1h [L3] feed history + [L7] wheel-scroll
> (implement WITH §4 [P3] as one change), §4 [P4] input writer, §1f
> [S1]/[S2]/[S3] security follow-ups, §9 T10 dirPicker + statusline chain
> (execution contracts in the items), §7a = execute
> `openspec/changes/opencode-multi-provider-auth/tasks.md`, §2e needs-input
> relabel. Every open item carries pointers + a fix direction + a
> verification line; if one doesn't, that's a bug in this file. Rules of the
> road for Opus: (1) run the tests named in the item, not the full gate, per
> change; run `just check` once per batch (command-sandbox caveats in
> CLAUDE.md); (2) line numbers drift — anchor by symbol name and re-`rg`
> before editing; (3) never touch `openspec/` structure, `*.gen.*` files, or
> memory. Drafted, awaiting maintainer sign-off: §7b package-manager ADR, §8
> pod-bootstrap design, §2e plan-doc workstreams (A-G). Maintainer-decision
> gated: §8 [L6], §2e [L5] floating-modal design. Needs the live cluster or
> the maintainer's eyeball: [L2] repro, claude-pane-first gates 8.2/8.3, §5
> Spegel, §6 codex live spike, §7c verify sweeps.

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
- [ ] **[L7] Trackpad/wheel scroll does nothing in the claude pane
  (2026-07-20 live session). Implement WITH §4 [P3] as one change.** Local
  claude "scrolling" is the TERMINAL scrolling its own history (claude runs
  inline, no alt-screen) — but the pane renders only the vt emulator's live
  screen, and the emulator's scrollback (10k lines, `vt.NewEmulator` call in
  `external_pane.go`) is retained yet never exposed. Execution sketch:
  (1) in `ExternalPane.handleMouse`, when the event is wheel up/down AND the
  child has not enabled mouse tracking (check what the vt emulator exposes
  for mode state; if it exposes nothing, gate on "wheel while not forwarded"
  by asking the emulator — worst case forward-when-tracking-else-local via
  the modes the key encoder already consults), adjust a new `scrollOffset
  int` field instead of forwarding; (2) `View()`: when scrollOffset > 0,
  compose the visible rows from `emu.Scrollback()` tail + top of the live
  screen (the vt package's Scrollback returns lines — clamp offset to its
  length), and render a small "↑ N lines — any key to return" indicator in
  the status row; (3) snap back: any KeyPressMsg, paste, or new output
  (apply feeding bytes) resets scrollOffset to 0 BEFORE forwarding/writing;
  (4) [P3]: cap retention via `SetScrollbackSize(2000)` at emulator
  construction. Tests (`external_pane_test.go` seams exist): wheel-up sets
  offset and renders scrollback content; keypress snaps to live and IS
  forwarded; offset clamps at history length; wheel with mouse-tracking
  enabled still forwards to the child. Run
  `go test ./internal/tui/dashboard/` + `-race -run 'Pane|External'`.
- [ ] **[L3] Detached feed opens empty — host event cache has a reader but no
  writer (MED).** `app.go` `openFeed` seeds from `eventCache.LoadEvents` but
  `AppendEvent` has zero production callers (orphaned by a935541);
  `EventCache`/`WithEventCache` (`internal/tui/dashboard/model.go:425-441`)
  and `indexEventCache`/`CacheWriter` (`internal/cli/sync_support.go:427`)
  are dead weight. RECOMMENDED fix (option A): on feed open, request SSE
  replay from seq 0 instead of `sess.lastSeq` (the runner's SQLite log holds
  full history and replay is already chunked/bounded — E2 in
  `runner/src/events.ts`), seed the feed from that replay, THEN delete the
  whole EventCache surface (interface, WithEventCache, indexEventCache,
  CacheWriter, and their tests) as a follow-up hygiene commit. Watch out:
  the `after=lastSeq` behavior exists to prevent launch-time notification
  flashing/usage double-count on the DASHBOARD path (`model_sse.go` comment
  near `afterSeq :=`) — scope the from-zero replay to the feed's stream
  only, not the dashboard read-model stream. Option B (reconnect the cache
  write path) is NOT recommended: it re-adds a second source of truth.
  Tests: extend `feed_test.go` seeding cases; a reducer-level test that a
  feed opened mid-session renders prior prompts/tool lines. Run
  `go test ./internal/tui/dashboard/`.

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
  desired vs baked pod-template env compared (`anthropicEnvShape`) BEFORE any
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

- [ ] **[S1] In-pane agent can exfiltrate the full refresh-capable Max
  credential over permissive 443 egress (HIGH).** The agent must be able to
  read `$CLAUDE_CONFIG_DIR/.credentials.json`
  (`runner/src/claude-config.ts:98-119`, allowlisted dir in
  `claude-pane.ts`) and the example egress
  (`k8s/networkpolicy-egress-allow.yaml:55-66`) allows all public 443 →
  prompt injection exfils a credential that outlives the session.
  Deliverables (docs+manifests only, no Go/TS): (1) new
  `k8s/networkpolicy-egress-fqdn.yaml.example` — Cilium
  `CiliumNetworkPolicy` with `toFQDNs` allowlisting `api.anthropic.com`,
  `statsig.anthropic.com`, `sentry.io` (verify the set: run a live session
  and check `hubble observe`/pod connects, or start from Claude Code's
  documented endpoints), plus the opencode/codex provider hosts commented
  per-backend; header comment stating it REQUIRES an FQDN-aware CNI and
  replaces the broad-443 example. (2) SECURITY.md: a plainly-worded
  paragraph in the existing egress section that the broad-443 example does
  NOT contain credential exfiltration and that a claude-pane session hands
  the agent a refresh-capable credential (cross-ref [A3]'s existing
  open-443 wording — extend, don't duplicate). (3) k8s/README.md row for
  the new example. Longer-term scoped credentials + the
  opencode-multi-provider-auth seed filter are separate tracks.
- [ ] **[S2] Project-sync secret ignores miss credential *filenames* (MED,
  untouched by the parked worktrees — clean start).**
  `securityIgnores` in `internal/sync/sync.go` blocks by extension only.
  Add mutagen ignore patterns (gitignore-like syntax — match how the
  existing dir entries in that list are written) for: `.netrc`, `_netrc`,
  `.npmrc`, `.git-credentials`, `.aws/` (whole dir),
  `service-account*.json`, `id_rsa`, `id_rsa.*`, `id_ed25519`,
  `id_ed25519.*`, `id_ecdsa`, `id_ecdsa.*` — grouped with a comment per
  group naming what it protects (existing list's style). Extend the
  existing securityIgnores test the same way, and add one sentence to the
  README sync section: the project dir should not contain secret files;
  these names are excluded defensively. Run `go test ./internal/sync/`.
- [ ] **[S3] Observer-token telemetry spoofing by the in-pane agent (LOW,
  accept+document).** Token readable under CLAUDE_CONFIG_DIR
  (`claude-pane-observer.ts` token write; routes in `server.ts` before the
  global auth gate) → same-session fake events / bounded reaper stall
  (SYNTHETIC_BUSY_STALE_MS releases after 5 min). Scope: DOCUMENT ONLY —
  add a short "observer events are agent-influenceable" paragraph to
  SECURITY.md's threat model (don't over-trust a claude-pane live
  transcript; cross-session spoofing impossible — token is per-session).
  The optional origin-tagging of observer events is a maintainer design
  call — do not implement without sign-off.
- [x] **[S4] done 2026-07-20:** grants.ts + its test deleted; the unused
  `@anthropic-ai/sdk` dep dropped in the same hygiene commit.
- [ ] **[S5] Set pane WS maxPayload + resize bounds when next touching
  `server.ts:174,119-134` (INFO).**
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

- [ ] **"needs input" label reads as blocked when the agent simply finished
  (maintainer feedback, first live pane session 2026-07-20).**
  `internal/tui/dashboard/session.go:41-42` — StatusNeedsInput means "turn
  finished, awaiting next prompt", but the label (:330) + glyph read like
  the session is stuck at a picker. Rename the HUMAN-facing label to
  something calm ("ready" / "your turn"), keeping StatusWaiting as the
  genuinely-blocked state and its attention float. Touch points: the
  human label fn (`session.go` ~:330 "needs input"), the glyph
  (`theme.GlyphNeedsInput` — check whether the glyph itself also needs a
  calmer form), and any status-row/list renderings that embed the word.
  CAREFUL: the WIRE string "needs-input" (String(), :57) round-trips
  through snapshot serialization (parse at :470) — do NOT rename it; only
  the display label. Regenerate goldens
  (`go test ./internal/tui/dashboard/ -run TestGolden -update`) and eyeball
  the diff. Run `go test ./internal/tui/dashboard/`.
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
- [ ] **[P3] vt emulator retains a 10k-line scrollback the pane never reads
  (MED).** `vt.NewEmulator` call in `external_pane.go`. DIRECTION CHANGED
  2026-07-20: claude runs inline (no alt-screen) and [L7] (§1h) wants
  wheel-scroll over this exact buffer — so KEEP the scrollback but cap it
  (`SetScrollbackSize(2000)`) and build the [L7] viewer; do NOT
  SetScrollbackSize(1). Execute as one change with [L7] (full sketch there).
- [ ] **[P4] Pane input is a blocking network write on the UI goroutine
  (MED).** `handleKey`/`handlePaste`/`handleMouse` in `external_pane.go` →
  `PaneStream.Write` under `writeMu` (`internal/runner/pane.go`): every
  keystroke blocks the Bubble Tea loop on `conn.WriteMessage`; a stalled
  forward freezes the whole dashboard INCLUDING ctrl+] detach. Fix sketch:
  give the pane an input-writer goroutine owning transport writes — a
  buffered `chan []byte` (~64 entries), handleKey/etc. do a non-blocking
  send; on full channel, drop the input and record a pane-level stream
  error (surfaced like other pane errors) rather than blocking; writer
  exits on the P5 done channel (already exists post-ea27ab9); close() must
  drain-or-abandon cleanly. CAUTION: preserve write ordering with resize
  (`PaneStream.Resize` shares writeMu — route resize through the same
  channel or document why racing is safe). Tests: fake transport whose
  Write blocks — UI Update returns promptly and detach works; ordering
  test (keys arrive in send order); race run green. Run
  `go test ./internal/tui/dashboard/` + `-race -run 'Pane|External'`.
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

- [ ] **Codex C3 parity: extend the shape-changing-re-create guard to codex**
  (from the Phase 1 landing, 2026-07-17): `anthropicEnvShape`
  (`internal/k8s/backend.go`) only detects the anthropic env vars, so a codex
  account→accountless re-create is NOT rejected — `syncSessionCredential`
  strips `codex-auth-json` while a resumed pod's baked NOT-Optional
  `CODEX_AUTH_JSON` SecretKeyRef still points at the stripped key (pod would
  fail env resolution on restart). Covered + documented by
  `TestCreateSessionStripsCodexCredentialOnRecreate`; generalize the shape
  guard over both credential families like `reconcileSecretCredential` did
  for the sync side.
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

> **2026-07-20 direction change:** provider credentials move to
> harvest-from-local-opencode + per-session Secret seeding —
> `openspec/changes/opencode-multi-provider-auth/` (proposal/design/specs/
> tasks complete, validated). The shared `opencode-credentials` Secret
> demotes to explicit fallback; items below that harden the shared-Secret
> path still apply to that fallback. Trigger: live
> `CreateContainerConfigError` (cluster Secret has only the Zen key;
> default provider wants `anthropic-api-key`, backend.go:182).

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

- [ ] **Implement OpenCode credentials = EXECUTE
  `openspec/changes/opencode-multi-provider-auth/tasks.md` (supersedes this
  item's original shape, 2026-07-20).** Do NOT build a `client/cred`-style
  opencode store or shared-Secret preflight — the accepted design harvests
  the local opencode auth.json JIT at create into the per-session Secret
  (codex `codex-auth-json` transport pattern, `backend.go` reconcile at the
  `secretKeyCodexAuthJSON` branch) with the shared Secret demoted to
  fallback. The change's proposal/design/specs/tasks are complete and
  validated; start at tasks.md §1 (two verification tasks gate the merge
  semantics). Original pointers kept for the fallback path only:
  `dev/local/opencode-creds.sh`, `client/client.go` create validation.
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
- [ ] **Pod bootstrap files + generic env/secret injection** (upstream
  integration request, 2026-07-17): `CreateOptions.ExtraEnv`/`ExtraSecretEnv`
  (per-session-Secret-backed env escape hatch) then
  `CreateOptions.BootstrapFiles` (operator files materialized in pod `$HOME`
  before the agent starts — NOT in the synced workspace); operator binaries
  stay a derived-`--runner-image` pattern, not SDK surface. Full design +
  validation rules: `docs/design-pod-bootstrap-and-tool-injection.md`
  (Status: draft — needs maintainer sign-off). Touch points:
  `client/client.go` (CreateOptions/validate), `internal/k8s/backend.go`
  (Secret + buildEnv), runner boot materialize step; sdktest pins in the
  same change.

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

- [ ] **T10 — working-directory picker** (only unexecuted superpowers plan;
  `docs/superpowers/plans/2026-06-22-t10-working-dir-picker.md` — NOTE:
  `docs/superpowers/` is local-only, ignored by the maintainer's global
  gitignore; the item text here is self-sufficient): dirPicker
  overlay end-to-end — `dirpicker_path.go` (~-expansion, child listing,
  longest-common-prefix completion, validation) + overlay struct (open/close,
  prefill, Tab, recents) + wiring before the backend picker + thread
  `projectPath` into the Creator. None exists.
  **Re-raised by the maintainer 2026-07-20 during the first live pane
  session** — the dashboard creator still hard-cwd's every new session
  (`internal/cli/claude_remote.go:142` resolveProjectPath ← cwd; used by
  `dashboard_connector.go` newDashboardCreator). Priority up. Execution
  sketch (if the local plan file is unavailable, this suffices): (1) new
  picker stage BEFORE the backend stage in the create overlay
  (`backend_picker.go` + `account_picker.go` show the stage pattern:
  stage enum + key handler + render fn), prefilled with cwd; rows = cwd,
  recent project paths (dedup'd, most-recent-first from the local index —
  `internal/index/index.go:65` ProjectPath; expose a listing via the
  existing index API or a small `RecentProjects()` helper), and a free-text
  path row (textinput, ~-expansion, EvalSymlinks, must-exist validation;
  reuse resolveProjectPath's normalization); (2) thread the choice as
  `CreateParams.ProjectPath` (add the field to
  `internal/tui/dashboard/connector.go` CreateParams) and have
  `newDashboardCreator` use it when non-empty, else cwd — the CLI commands
  keep pure-cwd semantics; (3) validation errors render inline like the
  console-key form's formErr. Tests: picker-stage navigation/threading in
  the account_picker_test.go style; a connector test that ProjectPath
  flows to CreateOptions. Run `go test ./internal/tui/dashboard/
  ./internal/cli/`.
- [ ] **Host statusline in the pane (maintainer ask, 2026-07-20).** The
  runner's `mergeSettings` unconditionally overwrites `statusLine` with its
  own metrics-tap script (`runner/src/claude-pane-observer.ts:494`), so the
  user's own Claude Code statusline never shows in-pane. Wrinkle: the host
  statusline command is a Nix store path
  (`~/.claude/settings.json` → `/nix/store/.../claude-statusline`) that
  cannot be copied into the pod — its closure isn't there. Fix direction:
  the provisioned statusline.js CHAINS a user statusline when one is
  available in the pod (print its stdout, still POST the metrics; fall back
  to the built-in minimal string on any failure), delivered either via a
  designated synced script dir (ConfigInputsSubs pattern,
  `internal/sync/sync.go:218`) for plain scripts, or flox-installed into the
  runner env for packaged statuslines (the maintainer's case — see the
  flox-first policy). EXECUTABLE CONTRACT (decided default — implement
  this; the flox-install route needs no code beyond it): the provisioned
  statusline script checks for an executable at
  `$CLAUDE_CONFIG_DIR/pane-observer/user-statusline` OR a
  `sandbox-user-statusline` on PATH (covers a future flox-provided bin);
  if found, pipe the SAME stdin JSON to it with a ~1s timeout and print
  ITS stdout as the in-pane line (still POST the metrics first,
  fire-and-forget-safe); on missing/failure/timeout, print the built-in
  minimal string exactly as today. Host side: add `pane-observer/` (or a
  dedicated `statusline` entry) to ConfigInputsSubs
  (`internal/sync/sync.go:218`) so a plain script syncs in — but EXCLUDE
  the runner-owned token file from any host→remote overwrite (check how
  ConfigInputsSubs paths interact with the runner-written
  `pane-observer/token` before choosing the dir). Tests: runner suite —
  user script present→chained output, absent→builtin, failing→builtin;
  sync sub addition pinned in sync tests. Run `cd runner && npm test` +
  `go test ./internal/sync/`. Maintainer follow-up (not blocking): package
  `claude-statusline` into the runner flox env so the PATH branch lights
  up. Closes the design doc's open "statusline display string" question.
- [ ] **Tekken-style agent-picker modal** — animations + per-agent
  ascii/ansi portrait.
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

> **PARKED PART-DONE WORKTREES (2026-07-20, agents stopped at the spend
> limit — finish these rather than restarting):**
> `.claude/worktrees/agent-a0080936a970d42b6` has an [O3] start
> (`internal/authstatus/{authstatus,providers}.go` modified; doctor.go and
> the [S2] task NOT started — diff it, keep what's right, finish per the
> item). `.claude/worktrees/agent-aff3123c45acf636a` has [O1]/[O7]/[O8]
> mostly drafted + [O9] in progress (root.go, k8s/README.md, runner-api.md,
> session-lifecycle.md, architecture.md modified; the [O5]
> reaper-namespace.yaml was NOT yet created) — **WARNING: it also modified
> `client/sync.go`, which was out of scope; review that diff skeptically
> and drop it unless clearly justified.** Both worktrees are based on
> 683bffc; integrate via cherry-pick/apply onto current main, re-verify
> every doc claim against code, and remove the worktrees when harvested.

- [ ] **[O1] `sandbox --help` example broken by pane-first** —
  `internal/cli/root.go:41` positional prompt vs `cobra.NoArgs`; also stale
  Long text :37 + package doc :2.
- [x] **[O2] done 2026-07-20 (1dbf495):** CLAUDE.md rewritten for
  pane-first — intro, runner file table, event-model wording, session
  lifecycle, and command tree all match architecture.md and the code.
- [ ] **[O3] `auth status` + `doctor` report pre-pane claude auth** —
  `internal/authstatus/providers.go:31-36` env-only probe;
  `internal/cli/doctor.go:287` names a shared Secret that doesn't exist for
  claude-pane (client/account.go:146 says so). Probe design: presence-only,
  NEVER secret bytes — darwin: `security find-generic-password -s "Claude
  Code-credentials"` WITHOUT `-w` (exit code is the signal); else stat
  `$CLAUDE_CONFIG_DIR/.credentials.json` → `~/.claude/.credentials.json`
  (mirror `client/cred/system.go` systemPaths — import if it doesn't drag
  secret-reading in, else copy with a pointer comment). Report as the
  PRIMARY claude source ("host Claude Code login"); demote the env checks
  to secondary/headless wording. Rewrite the doctor remedy to "log in with
  `claude` on this machine (Max mode)". Tests: injected exec/stat seams in
  the existing authstatus test style — no real `security` calls. A start
  exists in the parked worktree (see the block above). Run
  `go test ./internal/authstatus/ ./internal/cli/`.
- [ ] **[O4] `sandbox doctor` missing from README** (`doctor.go:122-131`;
  README's `just doctor` is the KIND-env tool) — Quickstart + Commands row.
- [ ] **[O5] Reaper undeployable from shipped k8s/ + silent consequence** —
  apply order omits reaper-rbac (`k8s/README.md:51-55`), `agent-reaper`
  namespace YAML missing (`reaper-rbac.yaml:22`), netpol exception not
  shipped; add the "sessions never auto-suspend" sentence.
- [ ] **[O6] `sandbox codex` in --help but undocumented** (`root.go:96`).
  DEFAULT (execute unless the maintainer overrides): add a README Commands
  row marked **experimental** with the ChatGPT-OAuth `auth.json` credential
  prereq (contract lives in code comments, `claude_remote.go:89-96`) and a
  one-line "attach UX is degraded until the codex pane lands" caveat — do
  NOT hide the command (it works; hiding it regresses codex dogfooding).
- [ ] **[O7] k8s/README pre-pane facts** — :44-46 positional prompt, :71
  `sandbox-claude-sdk` label, :100-102 shared anthropic-credentials guidance
  (only consumer is the retired-backend branch, backend.go:1716-1766).
- [ ] **[O8] runner-api.md examples use retired `claude-sdk`** (:43-44, :61,
  :218) — switch to claude-pane + extend the retired-id note.
- [ ] **[O9] architecture.md "later wave" worktree hedges contradict
  session-lifecycle.md** (:153-156, :174-176, :224) — worktrees shipped.
- [ ] **[O10] dev/local README claude section pre-pane** (:99-121 env token,
  :156 hidden `turn`; no host-login note).
- [ ] **[O11] openspec/ workflow unexplained** — one CONTRIBUTING paragraph
  or 5-line openspec/README.md.
- [ ] **[O12] Gate described three ways** — CONTRIBUTING.md:37-39 vs
  Justfile:28; README Testing points at neither.
- [x] **[O13] done 2026-07-20 with [L1]:** the sentinel and the store-account
  error both carry "log in with `claude` on this machine" remediation.
- [ ] **[O14] README hero GIF predates the pane UI** — re-record after the
  live pass (was already noted in 7.4; now has an id).
- [ ] **[O15] Retired-backend residue sweep** — `backend.go:1716`,
  `internal/session/types.go:53`, `runner/src/session.ts:37`,
  `client/account.go:69`, `internal/k8sit/local_test.go:44`; apply the
  `agent.ts:66` retired-id comment pattern or delete dead branches. (The
  stale `--pane` comment in dashboard_connector.go was rewritten with [L1].)
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
  pull vs ready — the big §5 unknown); SSE first-event latency; the §1d
  observer-cap model remains absent from `docs/architecture.md` (doc drift,
  2026-07-06 harness audit).

- [ ] **Visual-testing gaps (2026-07-13 review; re-scoped 2026-07-20 after
  claude-pane-first deleted the transcript surfaces) — static goldens are
  strong, motion/theme/size axes are not.** The golden harness
  (`internal/tui/dashboard/golden_test.go:30`) deliberately pins
  `SANDBOX_REDUCE_MOTION=1`, so every committed frame is the settled
  end-state; the transition catalog (`internal/tui/dashboard/transitions.go`)
  is only value-tested (`tui/anim/transition_test.go`). Sub-items:
  - [ ] *Mid-motion golden frames:* with motion forced ON and the injected
    `nowFunc` stepped through fixed offsets (0, ½-window, past-end of
    `rowEnterDur`/`statusFlashDur`), golden the rendered frame at each step —
    pins the row fade and status flash (`transitions.go`) as reviewable frame
    sequences. All inputs are already injectable, so this is
    byte-deterministic; it also gives agents a static way to "see" the
    animation (the golden files ARE the frames).
  - [ ] *Theme axis:* goldens render only the default theme
    (`tui/theme/theme.go:161` — themes[0] Midnight); Daylight is never
    snapshotted anywhere. Parameterize at least one dashboard + one feed
    golden per registered theme.
  - [ ] *Size axis:* every golden is 100×30 (`golden_test.go:53`). `tui/kit`
    covers narrow degradation per-component (`components_test.go`
    TestCardDegradesNarrow) but no narrow-terminal golden exists for the
    composed dashboard/feed frame.
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

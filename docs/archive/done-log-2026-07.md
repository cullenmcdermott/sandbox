# Done log — July 2026

Detail for completed TODO.md items, pruned out of the live backlog on
2026-07-04 (per the running-notes convention: one-line summary stays in git
history, detail lands here). Predecessor: [`done-log-2026-06.md`](done-log-2026-06.md).

## Must-fix correctness (all fixed on `feat/ux-polish` phases 1–3)

- **Detail-pane "needs you" key hints were wrong on 3 of 4 actions.** Fixed
  2026-06-28: row intent unchanged (attach / rename / suspend / destroy for a
  waiting/needs-input/failed session) but each key char is now correct —
  `↵ attach / R rename / x suspend / ! destroy` — matching `keymap.go` (rename is
  capital `R`; lowercase `r` is resume). `internal/tui/dashboard/model.go:2268`.
- **Permission approve/deny errors were silently swallowed** (chat + dashboard).
  Fixed (Phase 1): `case approveResultMsg` → `m.actionErr` in `model.go` Update;
  chat `resolvePermission` returns `permResolveErrMsg` and appends a
  `blockError`. Tests in `phase1_ux_test.go`.
- **Dashboard sat on skeleton bars forever when the cluster was unreachable.**
  Fixed (Phase 1): `seedCmd` returns `seedFailedMsg`, `m.seedErr` drives an
  error+`r`-retry branch in `renderRowLines`, self-heals on next seed/watch.
  Tests in `phase1_ux_test.go`.
- **OSC 9;4 tab-progress was dropped by the v2 cell renderer.** Fixed (Phase 3):
  progress signal rides `tea.Raw` from `App.Update`, edge-triggered against
  `App.lastProgress` so it emits once per aggregate-state transition and goes
  quiet when steady; `withTerminalSignals` keeps only the Kitty prepend;
  `lastProgress` resets under `ScreenExternal`. Tests in `osc_signals_test.go`.

## UX / communication (all fixed on `feat/ux-polish`)

- **Cold start showed a blank, frozen, TUI-less terminal** during pod schedule +
  image pull. Fixed + live-verified (Phase 2): pod-ready wait moved out of
  pre-TUI `backend.Start` into the connect path (`establish`), so the animated
  splash (per-phase detail + elapsed timer) is on screen during schedule + pull.
- **Connect/create splash had no elapsed timer.** Fixed (Phase 2):
  `App.connectStartedAt` rendered on the `connectingView` title via the
  `roundDur` reconnect idiom. Test in `phase2_ux_test.go`.
- **Chat status line never surfaced file-sync state.** Fixed (Phase 3): trailing
  row-1 sync segment (`syncSegment()`, ✓ synced / ⟳ syncing / ⚠ stalled) driven
  by `TranscriptModel.syncStatus` fed from the dashboard's warm-session poll.
  Gated on non-empty so the default status line stays byte-identical. Tests in
  `phase3_ux_test.go`. `statusline.go`, `app.go`.
- **connectErr/actionErr persisted in the detail pane with no dismiss.** Fixed
  (Phase 1): bare `esc` dismisses both in `handleKey` (`model.go`). Test in
  `phase1_ux_test.go`.

## Agent parity (opencode)

- **Runner-as-metrics-observer for external-pane backends.** DONE +
  live-verified for opencode (Phase 4): `runner/src/opencode-observer.ts`
  subscribes to `opencode serve`'s event stream always-on and emits normalized
  turn/usage/tool/title events → SSE → list row + external-pane statusline get
  live status/ctx%/cost/title. No schema change. Verified in events.db: a real
  interactive turn produced the full turn-1 sequence; `external_pane.go`
  statusRow reads the live read-model. (Codex same-pattern work remains open.)
- **OpenCode in-agent tool use was neither audited nor gated** by the runner's
  Bash blocklist. FIXED 2026-07-01: gating via a guardrail plugin generated at
  boot from `guards.ts` (`serializeBlockedPatterns` → lossless
  `new RegExp(source, flags)` embed) registered in the opencode config `plugin`
  array (v1.17.7 file-plugin spec, verified against pinned `@opencode-ai/plugin`
  types + sst/opencode source); its `tool.execute.before` throws on a blocked
  `bash` command; fail-open with a loud log (defense-in-depth only). Audit via
  injectable `AuditTool` in `createOpencodeTurnMapper` (headless `/turns`) +
  `ObserverDeps.audit` (interactive) → `audit.jsonl`. Tests:
  `runner/test/opencode-guardrail.test.ts` + mapper/observer audit tests.
- **opencode `cancel` + suspend-warning correctness.** `cancel` WORKS (Phase 4):
  observer sets `last_turn_id`, interrupt route gained an opencode-abort
  fallback (`server.ts`) — live-verified. The monotonic-`last_turn_id`
  false-positive was fixed 2026-07-01: `/status` exposes a live `activeTurnId`
  (registry-derived, opencode busy fallback); cancel/suspend key off it.
  (`--model`/initial-prompt arg remains open.)
- **opencode in-turn missing-session recovery (claude parity).** Subsumed by the
  Phase C `ensureSession` fix: a gone persisted session 404s on `session.get`,
  the id is cleared, and a fresh session is created in the same call, so the
  turn proceeds. Residual: a negligible 404 window between probe and prompt,
  self-heals next turn — the risky `runTurn` restructure to mirror
  `claude.ts:511-573` was judged not warranted. `runner/src/opencode-turn.ts`.
- **opencode turn parity (Phases G/B/C, "done recently" note).** Streaming
  deltas, permission auto-respond flow, resume/continuity in
  `runner/src/opencode-turn.ts`; model-selection `||` precedence fix; k8sit
  conformance suite (interrupt/error-surface/reconnect/lifecycle,
  table-driven). Two real bugs found+fixed by the suite: `Backend.Resume`
  returned early on a terminating pod (`waitForPodReady` now ignores pods with
  a `DeletionTimestamp`; `backend_resume_ready_test.go`), and the opencode
  resume path raced `opencode serve` boot (now probes `session.get` with
  retry). See `docs/parity-RESUME.md`.
- **opencode wheel-scroll hijacked prompt history** (inbox observation). DONE
  (Phase 3, item 3): enabled `tea.MouseModeCellMotion` on `ScreenExternal`; live
  PTY capture confirmed opencode enables mouse tracking itself (DECSET
  1000/1002/1003 + SGR 1006), so forwarding real SGR mouse lets opencode's own
  wheel-scroll + clicks work. Tests in `phase3_item3_test.go`.
- **esc detach conflict with opencode overlays** (inbox). DONE 2026-06-28:
  dropped `esc` from the `ScreenExternal` detach set (`app.go` ~:660); only
  `ctrl+]`/`ctrl+4` detach, `esc` forwards to opencode. Regression test:
  `TestAppExternalPaneEscIsForwardedNotDetached`.

## Stale docs (all corrected)

- Probes ARE implemented (`backend.go:698-721`); SECURITY.md / LAUNCH-CHECKLIST /
  HARDENING-BACKLOG updated (C9 → "Already fixed"; L-CHECK checked off). 2026-06-28.
- `architecture.md` "Security model": capability drops +
  `allowPrivilegeEscalation:false` recorded as landed (BR1); only non-root +
  `fsGroup` left as M20; dead networkpolicy link replaced with the real flat
  `k8s/networkpolicy-*.yaml` files. 2026-06-28.
- `runner-api.md`: `/exec` blocked → "refused pre-spawn, exit 126" (127 spawn
  failures); runner-side `SANDBOX_DEBUG` claim removed (C10 tracks it);
  `rate_limit.updated` payload lists all 10 fields. 2026-06-28.
- README "start (or reuse)" → "start a **new** session" everywhere (Phase 5);
  resume is only `sandbox attach` / the dashboard list.
- ghostty header + verification-protocol dead spec refs — fixed 2026-06-24.

## Whole-system design review 2026-07-01 — fixed HIGHs

- **CLI↔runner protocol version handshake.** FIXED 2026-07-01, schema-driven:
  `schema/events.json` gained top-level `protocolVersion: 1`; `just gen` emits
  `session.ProtocolVersion` (Go) + `PROTOCOL_VERSION` (TS) with a drift test.
  Runner reports it on `/healthz` + `StatusResponse`; `runner.Client.Health`
  caches it; `client.Session.Connect` (→ `Connection.Warning` via
  `appendWarning`) and headless `waitHealthy` warn, never refuse, via shared
  `runner.ProtocolMismatchWarning`. The TUI `ApplyRunnerEvent` `default: return
  false` is the documented skew safety net. Bump the schema field whenever an
  event/payload/SSE change could silently misbehave across versions.
- **`events.db` schema version migration.** FIXED 2026-07-02: `openEventLog`
  read-compare-migrates `user_version` on every open — NEWER-than-binary is
  refused (visible CrashLoopBackOff instead of misread rows); older walks the
  `MIGRATIONS` registry in transactions; pre-versioning db treated as v1.
  `session.json` carries `state_version`; newer file loads best-effort with a
  loud warning, unknown fields preserved round-trip (`reviveSessionState`).
  Tests: `runner/test/schema-version.test.ts`, `state-version.test.ts`.
- **`:latest` + `PullAlways` swapped the binary under old PVC state on resume.**
  FIXED 2026-07-02, cluster-side: once the pod first goes Ready,
  `pinRunnerImageDigest` stamps the kubelet-resolved digest as the
  `sandbox.cullen.dev/pinned-runner-image` annotation; `Resume` rewrites the pod
  template image from it (relaxing `PullAlways` → `IfNotPresent`) in the same
  Update as replicas 0→1. Re-stamps on drift; falls back to tag for
  locally-loaded dev images (no registry digest). Bonus: resume-by-digest
  sidesteps the stale traefik tag→manifest cache on resume (creates still
  affected — see live Ops item). Tests: `internal/k8s/backend_pin_test.go`.
  Docs: `session-lifecycle.md`.
- **Project sync ignored `.gitignore`; secrets flowed laptop→pod.** FIXED
  2026-07-01: `createProjectSync` translates the project root's `.gitignore`
  verbatim into `--ignore` flags (`internal/sync/gitignore.go`). Layering
  (mutagen later-wins): build-tree defaults → `.gitignore` → security set LAST
  (non-overridable). Limits documented: root `.gitignore` only. Unreadable
  `.gitignore` fails the create (fail-closed). Tests: `gitignore_test.go`,
  `TestCreateProjectSyncIgnoreLayering`.
- **Two-way sync propagated pod-authored auto-executing files to the laptop.**
  FIXED 2026-07-01: non-overridable security ignore layer excludes `.envrc`,
  `.direnv`, `.vscode`, `.idea`. Makefile-class files deliberately NOT ignored.
  `internal/sync/sync.go` securityIgnores.
- **Egress allowlist is lateral-movement boundary, not exfil — was
  undocumented.** DONE 2026-07-01 in architecture.md "Security model" +
  "File-sync boundary" bullet.
- **`CreateSession` had no rollback; orphaned a bearer-token Secret + PVC.**
  FIXED 2026-07-01: deferred best-effort `deleteSessionResources` (shared with
  `Destroy`, NotFound-tolerant, sweeps earlier orphans at the same id) on an
  independent 30s `context.WithoutCancel`; rollback failure appended to (never
  masks) the original error. Tests: `TestCreateSessionRollsBack*`
  (`internal/k8s/backend_c5_test.go`).

## Mutagen sync GC (core landed; follow-ups remain in TODO)

Root cause: host mutagen daemon outlives the CLI and is blind to the cluster —
any non-CLI session death (idle reaper, `dev-reset`, eviction, crash) leaked ~8
syncs retrying forever (observed 634 dead syncs). LANDED:
`Manager.List`/`IsOrphanStatus`/`TerminateByIdentifier` (scoped to the
`sandbox-session` label); dashboard GC on the reconcile tick (reaps only when
the pod is NOT Running/Creating per the authoritative snapshot, 90s grace —
MF1); `sandbox sync gc [--dry-run]` with cluster-outage guard; `ResumeAll` on
attach (MF2); partial CreateAll recovery on empty ProjectPath (MF4).
Adversarially reviewed. Follow-ups MF3/MF5/SF1 + dev-reset hook remain in
TODO.md §5.

## Auth / credential decisions

- **Auth + cluster status read-side surface.** DONE 2026-06-24: provider
  abstraction (Claude/Codex/OpenCode; offline, secret-free; JWT-exp decode for
  codex) + `internal/k8s` `Ping`/`Host`/`Namespace` + `sandbox auth status`
  red/green rendering. Lives in `internal/authstatus` since 2026-07-03.
  Tested (`internal/authstatus/authstatus_test.go`, `internal/cli/auth_test.go`).
  Remaining scope tracked live in TODO.md §6.
- **cred read-side status report is NOT public SDK.** DECIDED + DONE 2026-07-03:
  moved back internal as `internal/authstatus` (read side is presentation, not
  capability; roadmap would churn a pinned public surface). Dissolves the
  `cred.Status` vs `client.Status` collision; `client/cred` is write-side only.
  Also fixed in the move: `Status.Level()` derives WARN from a structured
  `Expired bool` instead of a `strings.Contains(Detail, "EXPIRED")` substring
  contract. Re-promote deliberately (hardened + pinned) only if a real external
  consumer appears.

## TUI picker/search/sort fixes (`cb0e375`, 2026-07-04) — detail from TODO §1b

- **Account picker silently dropped pastes.** Picker inputs only received
  `tea.KeyPressMsg`; bracketed paste arrives as `tea.PasteMsg`, which had no
  route to the picker (only handler was the external pane's,
  ScreenExternal-gated) — the field whose placeholder says "paste your
  Anthropic Console API key" got nothing; same gap on the label form. Fixed:
  PasteMsg routed to picker label/console forms via `pickerPaste`.
  `account_picker.go:340`, `app.go:422`.
- **Descending sort comparator was invalid.** SortDesc = `!less`; equal keys
  returned true both ways → `sort.SliceStable` swapped equal-title rows on
  every re-sort (per cluster/runner event) — rows visibly ping-ponged and the
  row-indexed cursor retargeted actions. SortByTitle also had no ID tie-break
  and compared Title not DisplayTitle. Fixed: three-way cmp with sign flip +
  fixed-direction ID tie-break, DisplayTitle. `sort.go:116`, `sort.go:101`.
- **Transcript search dropped every uppercase letter** (`searchKey` required
  `key.Mod == 0`, but bubbletea v2's decoder sets ModShift on plain typed
  uppercase — "Readme" yielded "eadme") **and backspace was byte-wise**
  (é → dangling 0xC3 → U+FFFD → fuzzy-matching garbage). Fixed: accept
  `key.Mod &^ tea.ModShift == 0`; `utf8.DecodeLastRuneInString`.
  `search.go:72`, `search.go:66`.

## Fixed in the 2026-07-04 uncommitted claude-pane pass — detail from TODO §1a/§1c

Verified fixed in the working tree by the 2026-07-04 Fable re-verification
sweep (commit pending at time of writing).

- **`truncate()` not ANSI-aware.** Measured with lipgloss.Width but trimmed
  raw runes — could eat a trailing SGR reset or stop mid-escape on styled
  input (tool-card summaries, overflow band, boxWithTitle). Fixed: `truncate()`
  now delegates to `ansi.Truncate(s, maxW, "…")`. `model.go:2635-2645`.
- **`hasLinkRefDef` wasn't fence-aware.** A fenced code line shaped like a TS
  index signature (`[key: string]: string`) forced resetCache + full glamour
  re-render on every subsequent delta, reinstating O(deltas²) keystroke
  starvation. Fixed: tracks `openerChar`/`fenceInfo`+`closingFenceInfo` and
  skips fenced lines before `isLinkRefDefinition`.
  `chat/streaming_markdown.go:466-487`.
- **`ggPending` had no reset on other keys.** Early-return branches
  (R/A/space/overlays/…) skipped the reset, so a lone `g` minutes later was
  misread as the `gg` chord. Fixed: reset hoisted to the top of `handleKey`
  (`if ks != "g" { m.ggPending = false }`, `model.go:1748`); all key presses
  route through handleKey so no branch can skip it. (The alternative 500ms
  expiry was not added — the reset path is the implemented fix.)

## Inbox investigations (resolved)

- **hall.kvitch.dev "not in use"** — typo; real URL is **hall.kvick.dev**. Hall
  IS wired in (`justfile:156-162` auto-detects, `dev/local/README.md:57-81`,
  `dev/local/Tiltfile:22`), optional via `SANDBOX_USE_HALL=0`.
- **"Make sure background is opaque everywhere"** — already enforced:
  `app.go` `View()` forces `v.BackgroundColor = theme.Page` on every screen
  except `ScreenExternal` (opencode paints its own bg); `modalView` composites
  an opaque page-colored backdrop (Fix E).
- **"Fresh start should drop into empty dashboard"** — already true: bare
  `sandbox` calls `dashboard.Run` with no auto-attach; only `sandbox claude` /
  `sandbox attach` auto-attach (intentional).
- **"Do we need a claude-runner-specific image or is the name a misnomer?"**
  (2026-07-04 triage) — misnomer: one shared runner image serves every backend
  today (opencode is npm-globaled into it, `runner/Dockerfile`). Naming gets
  decided inside the §5 split-per-backend-images item, where the note now
  lives.
- **"Why does `just dev` start a claude session automatically?"** (2026-07-04
  triage) — by design: `dev backend="claude"` = `dev-up` + `dev-tui
  {{backend}}` (`justfile:234-240`). `just dev-up` gives cluster-only; `just
  dev opencode` picks another backend.
- **"Match claude code UX"** → became TODO §2 (the program). **"Expose CLI as
  a go library"** → shipped as the public `client/` SDK; remaining API-shape
  work is TODO §8.

## Fable review pass (2026-07-06) — the 2026-07-05 "PENDING FABLE REVIEW" batch

Every item stamped PENDING FABLE REVIEW (2026-07-05) across §1a–§1e, §2a–§2d,
§4 and §10 was re-verified — three parallel adversarial Opus reviews (per-item
verdicts, mechanisms traced against source, tests checked for vacuity) plus a
direct Fable pass on the docs/harness items. Outcome: **27 of 28 approved
as-shipped; one confirmed defect (fixed same day, below); full `just check`
green with zero skipped gates.** Working-tree hardening that rode along:
§1a `catchingUp` hydrate-arm + re-seed carry, §1c theme-epoch forced
reconcile, the justfile per-gate skip detection, ADR H1–H4, README
`--resume` section.

### §1a SSE / state-machine cluster (6 items, all approved)

- **Replay-treated-as-live 7-step fix**: per-session `catchingUp` armed on
  stream install, cleared ONLY at `EventStreamLive`; notify suppressed during
  catch-up with one honest flip-to-live toast; `statusChangedAt` from
  `ev.Time`; seq dedup (`Seq!=0 && Seq<=lastSeq`); quit flush via `Cancel()`;
  hydration folds. Tests incl. the launch-storm and resolved-in-replay
  scenarios (`sse_catchup_test.go`).
- **Duplicate background connects**: `liveSSEConnecting` in-flight map +
  `hasLiveSSE` guard on all three launch paths, cancel-incoming-on-existing,
  per-stream generation tokens with a stale-gen guard atop
  `handleRunnerEvent`; failed initial connect surfaces as
  `liveSSEConnectFailedMsg`. `connect_side_test.go` (11 cases).
- **StreamEnded**: preserves pending permission + attention on a
  still-Running-pod blip and schedules reconnect; clears + degrades only when
  the cluster says not-running; `degradeUnreachable` only after retry budget.
- **Watch-beats-seed**: `applyPodEvent` insert path hydrates titles +
  snapshot (lastSeq/seenSeq) BEFORE starting the stream, so informer-first
  inserts resume from head, not `after=0`.
- **seenSeq**: carried across re-seeds (with usage/cost/model/branch/tools);
  `applySnapshot` marks restored history seen; detach syncs the dashboard
  cursor from the transcript (`syncCursorFromTranscript`).
- **liveSSEReadyMsg attachedID guard** + `applyPodEvent` skip for the
  attached session (no start-then-cancel churn).
- **Review finding (MEDIUM, fixed 2026-07-06):** the uncommitted hydrate-arm
  of `catchingUp` had exactly ONE clear path (`EventStreamLive`), so a session
  whose background connect never succeeds (cluster says Running, pod
  unreachable — the fe259d6/c191c85 condition family) kept the flag forever
  and permanently suppressed toasts for a genuinely-pending hydrated
  attention state; the comment claimed a degrade/teardown clear that did not
  exist. Fixed: released on ALL no-stream-coming paths —
  `liveSSEConnectFailedMsg` (which now also runs the notify scan),
  `degradeUnreachable`, and the StreamEnded not-running branch. Tests:
  `TestConnectFailedReleasesCatchUpAndToasts`,
  `TestDegradeUnreachableReleasesCatchUp`,
  `TestStreamEndedNotRunningReleasesCatchUp`.

### §1b group view / pickers (3 items, approved)

- Group view builds from `visibleSessions()` (filter + attention-order carry
  through; collapsed badge counts filtered); filter-mode nav arrows-only
  clamped to `visibleRows()`. `group_filter_test.go`.
- ctrl+g jump in group view verified row-cursor-correct + expands the target
  group; fail-closed if the row can't be resolved.
  `TestCtrlGGroupViewLandsOnRowAndExpands`.
- Archive dead binding fully removed (`A`/`archiveSelected`/`Archived`);
  the S15 archived-section design pointer lives in the `groups.go` header
  comment, deferred to the §2a row-model consolidation.

### §1c rendering (3 items, approved)

- `spread()` hardened (right segment always survives, total exactly width);
  `clampLines` clips to its w×h contract; external-pane statusRow +
  attached-header + composer hint all routed through `spread`. Only the
  statusline row-1 segment-join tail remains, folded into the §2c collapse.
- **Theme cache invalidation**: `theme.Epoch()` folded into `blockFP`,
  AssistantItem section keys, and `StreamingMarkdown`; the follow-up forced
  reconcile (epoch-changed ⇒ recompute every fingerprint; epoch folded into
  the streaming-tail fp) closes the "stale palette until a width change"
  window the key-only fold left. Review traced the full chain (fp → version
  bump → tui/list cache miss → glamour pool invalidated via `theme.OnChange`)
  and found no remaining stale-palette path. Force-path test added
  2026-07-06: `TestThemeSwapForcesReconcileRerender`.
- Composer width helpers (`composerBoxWidth`/`composerInnerWidth`) unify
  `layout()` and `renderInput()`; behavior identical at width ≥ 21.

### §1d system reliability (2 items, approved)

- `SyncConflicted` state: conflicts win over transport stalls in `classify` +
  the worst-of reducer; both TUI glyph maps render `⇄ conflicted`; Gold
  (needs-you) vs Coral (transport, may self-heal). Per-file conflict detail
  remains a follow-up (still in §1d).
- `destroy` now runs suspend's best-effort active-turn probe (5s bound)
  before the confirmation gate; warn to stderr, non-fatal.

### §1e.6 server-side loop ADR (approved with fixes)

`docs/server-side-loop-adr.md` reviewed incl. the H1–H4 hardening (explicit
`state` lifecycle field; boot re-arm anchored on `last_completed_at`;
409-defers / bounded-retry / stopped(error) failure ladder; version-skew
accepted-risk note). Fable fixes: the `autopilot.state` event's `reason` enum
was missing H2's `"error"`; the Q1 staleness-clock wording contradicted H1
(now explicitly `max(last_completed_at, boot time)`). Implementation remains
gated on maintainer sign-off of the listed open items.

### §2a structural enablers (3 items, approved)

- Clock-injection sweep: all in-package animation/timing reads on `nowFunc`
  (grace gate, turn elapsed, toast lifecycle, motion loop, transitions);
  deferred halves (statusChangedAt assignments → §1a territory,
  `tui/theme.FadeColor` → §8, test-counter observer) stay in TODO.
- Markdown-renderer dedup: single package-level `renderAssistantMD` feeds
  both the finalized and streaming paths.
- status→label "drift" retriage: divergence is by design (user-seat phrasing
  in chat); locked with exhaustive enum-walk tests instead of merging.

### §2b event-model (2 items, approved)

- **context.compacted** (7270c6c): schema + regen'd files verified no-drift;
  mapping verified against the vendored SDK's `SDKCompactBoundaryMessage`
  (`compact_metadata.pre_tokens` required / `post_tokens` optional ⇒ the
  `?? 0` default and preserve-baseline-when-absent path are both correct);
  both reducers reset the ctx% baseline only when `PostTokens>0`; transcript
  marker is replay-safe under the seq-dedup guard. 5 Go + 2 runner tests.
- `MessagePayload.Role`: user echoes render as `blockUser`, stay out of
  `lastAssistantText` (goal-sentinel safe), dedup the optimistic block,
  strict `p.Content`. `message_role_test.go`.

### §2d UX (2 items, approved)

- ctrl+g `NextAttention` binding on the dashboard, surfaced via `FullHelp`;
  the external-pane key-reservation half stays open in §2d (maintainer
  decision).
- Fresh-session welcome: `transcriptEmpty()` gate, live attached view only,
  `fitModal`-exact at widths 20–80.

### §4 perf (2 items, approved)

- `partition()` computed once per `renderZoned` and passed to both bands.
- Runner SSE `broadcast()` serializes the frame once + zero-client early
  return; verified behavior-preserving (frame is a pure function of the
  event; per-client `afterSeq` filtering untouched).

### §10 harness (4 items, approved)

- `just check` skip report re-derives each optional gate from the SAME
  condition its recipe uses (incl. the separate `tsc` check for typecheck);
  amber summary lists what CI will still enforce.
- `sdktest/tui_surface_test.go` compile-pins all five public tui packages
  (method expressions fail on any signature change; `consumerListItem` locks
  the `Item` interface).
- `consumerRunnerClient` pins `client.RunnerClient` at exactly 9 methods —
  widening breaks the sdktest compile first.
- PTY-test in-sandbox caveat documented in CLAUDE.md.

### Rode along (same pass)

- README gained the supported `claude --resume` escape-hatch section (§3's
  open doc item) — field name `claudeSession` verified against the status
  API + local index.
- `notify.go` staticcheck QF1003 (tagged switch) fixed to keep
  `golangci-lint` green now that it's on the Flox host.

## Fable-coordinated batch (2026-07-06, second pass) — §1d/§4/§7 verification + fix

The five commits that landed after the morning review pass (`c72f0c7`,
`c191c85`, `fe259d6`, `114223d`, `5f96ccd`) were adversarially verified; one
real defect found and fixed in-tree.

### §1d system reliability (3 items)

- **SSE consumer backpressure** (`c72f0c7`): `events()` split into a scanner
  goroutine (feeds watchdog liveness on every wire read) and a forwarder
  goroutine draining an internal growable FIFO — a stalled consumer can no
  longer starve the watchdog into a forced disconnect, and `after=<seq>`
  contiguity/ordering/close semantics are preserved. Tradeoff (documented):
  the internal queue is unbounded if the consumer never drains.
  `TestEventsSlowConsumerDoesNotForceReconnect` +
  `TestEventsSilentStreamStillForceCloses` green under `-race`.
- **Port-forward terminal state** (`c191c85`): `resolvePodForForward`
  distinguishes Sandbox-gone (NotFound propagates, loop stops, `h.err` set,
  `h.done` closes) from reschedule gaps (retry as before). Verified: the
  ≤10s-forever hammering class is dead. Follow-up stays in TODO §1d: no
  caller consumes `ForwardHandle.Done()` yet.
- **Dead-node staleness cross-check** (`fe259d6`): pod path verified correct
  (phase-gated, zero-LastTransitionTime guarded, applied on Status/List/watch).
  **Review found a real watch-path defect:** `sandboxStale` aged the Ready
  condition with no phase gate, and the agent-sandbox controller stamps
  `Ready=False` at first reconcile with `LastTransitionTime` pinned at
  creation (`meta.SetStatusCondition` only bumps on Status change) — so a
  healthy >90s cold-start (slow image pull) read UNKNOWN from the watch.
  Masked today (dashboard folds UNKNOWN→Idle) but wrong on the public
  `StatusUnknown` surface. **Fixed:** `sandboxNeverReady` gate — a not-True
  Ready still stamped within `stalenessThreshold` of CreationTimestamp is a
  slow start (stays CREATING), not a stall. New tests: never-Ready slow start
  (unit + through `sandboxToState`), Ready-lost-long-after-creation still
  stale. Known-narrow tradeoff documented in the helper comment.

### §4 perf (1 item)

- **Focus-gated mutagen polling** (`114223d`): selected-row + attached
  sessions probe at 4s, others back off to 30s, first tick sweeps everyone,
  map pruned to live sessions; regaining focus re-probes on the next 4s tick.
  Conflict-latency concern checked: sync status feeds no attention routing /
  needs-you sort / notify — only the focused detail-pane/header indicators,
  which still probe at 4s. Group-view cursor resolution via
  `selectedRowSession` is nil-safe.

### §7 opencode reaper follow-ups (2 items, `5f96ccd`)

- Synthetic-busy staleness bound: reaper keys solely on `idleSince`;
  `recomputeIdle` ANDs `isDetached()`, so an attached client always pins the
  session non-idle; real runner turns immune via the `activeTurns>0` guard.
  Accepted tradeoff: a fully-detached opencode turn with zero observer events
  for >5min becomes idle-eligible (the conscious replacement for
  unreapable-forever). 152 runner tests green.
- `interruptedTurns` GC in `reset()`: safe — `reset()` clears `activeTurnId`,
  so a late `session.idle` after reconnect can't flip status. Residual
  pre-existing leak for never-active interrupted ids noted in TODO §7.

### §2a mechanical god-file split (Opus build, Fable-verified)

- `transcript.go` 3087 → 745 + `transcript_{stream,reduce,render,input}.go` +
  `permission_diff.go`; `model.go` 3086 → 799 +
  `model_{sse,reduce,render,input}.go`. Pure code motion (whole declarations,
  doc comments attached): verified three ways — AST decl-key accounting
  (105/105 + 109/109, no dups), byte-identical non-comment code lines, and an
  independent sorted-line-multiset diff (coordinator). Only content change: a
  merged straddling comment above `tailLines` split so `handleEvent` gets its
  own doc. goimports grouping on three new files fixed post-hoc (caught by
  `just check` lint).

### §4 perf pair (Opus build, Fable-verified)

- **Streaming-markdown O(N²) → incremental** (`chat/streaming_markdown.go`):
  `mdScanner` processes each completed line exactly once, snapshotting
  follow-independent boundary safety (`scanBound`) at each blank-line
  boundary; only the setext "what follows" check re-evaluates per query.
  Fence-skipping for link-ref-defs preserved (existing test still green).
  Original predicates kept as the reference oracle;
  `TestIncrementalScannerMatchesReference` + `TestStreamingRenderChunkingInvariant`
  assert equality at every prefix under whole/1-byte/random chunkings.
  `BenchmarkStreamingDeltas`: 119ms → 14.7ms/op, 180MB → 59MB, 57k → 20k
  allocs. staticcheck QF1001 De Morgan applied post-hoc.
- **tui/list resize coalescing**: `SetSize` records size + sets `needReflow`
  (following only); `applyReflow` runs at the head of `normalize()` so every
  anchor read settles the deferred re-pin — a drag's burst collapses to one
  `GotoBottom` at final size. Eager cache drop removed (entries refresh
  lazily on width mismatch; oscillation re-hits). `GotoTop`/`GotoBottom`
  clear the flag; `SetFollow` flushes under the old intent first. API
  unchanged (public §8 surface). New tests: drag coalescing, width
  oscillation zero-rerender, stacked-shrink repin.

## Fable-coordinated batch 2 (2026-07-06) — §2a block unification + §5 startup

### §2a — unify the dual block representations (Opus build, Fable-verified)

- `blockCard` (embeds `*list.Versioned`, implements `list.Item`) replaces the
  `blocks []tblock` + `items []*blockItem` + fingerprint-reconcile triple;
  `m.blocks` is now `[]*blockCard` — the cards ARE the list items. Deleted:
  `tblock`, `blockItem`, `blockFP`, `reconcileItems`, `markBlockDirty`,
  `syncBody` (→ `commitItems` + SetItems/pin), `renderBlockRaw`
  (→ `renderBlockBody(*blockCard)`), `bumpStreamItem`, `fpComputes`.
- Mutations bump versions at the mutation site: tool/subagent cards hold a
  `card *blockCard` back-reference (O(1) status/summary/child updates);
  per-commit display flags (`unread`, `turnGap`) memoized in `setDisplay`;
  the streaming tail refreshes one card gated on `streamFP`; theme epoch
  bumps every card once (seeded at NewTranscript so first commit is quiet).
- Verification: bump-site audit (every `.tool.`/subagent mutation adjacent to
  a `Bump()`), 9 test files ported off deleted internals with assertions
  preserved (incl. `transcript_blockfp_test.go` re-anchored to versions and
  the untouched replay-perf O(N) contract), full suite + `-race` green.
- Retires §4 "reconcile is O(n) per event". Unlocks per-block expanded/focus/
  copy state (a field + a bump now, not a new fingerprint dimension).

### §5 — startup speed cluster (Opus build, Fable-verified + gate wired by Fable)

- **Parallel creates:** Secret+PVC via `errgroup.WithContext`, Sandbox after
  both; rollback still enumerates all three NotFound-tolerantly under partial
  parallel failure (`TestCreateSessionRollsBackPVCOnSecretFailure`).
- **Lazy syncs:** `CreateAll` split into `CreateProject` (foreground,
  load-bearing) + `CreateInputs` (7 config/transcript syncs, backgrounded,
  kept serial for deterministic GC labels); failures surface via the
  advisory, never dropped.
- **Parallel port-forwards:** concurrent establishment, order-preserving
  handle slice, siblings cancelled+closed on any failure.
- **Deferred reaper:** `ensureReaperWithRetry` (3 attempts, exponential
  backoff) inside the background task; persistent failure surfaces via
  AwaitSync. Fresh-path skips the redundant Status Get + `ensureSSHKey`
  (Create stamps them onto the Session; consumed by first Connect).
- **Prompt un-gated from the flush:** Connect returns at runner-health +
  project-sync-create; the bounded 12s first flush (or detached reconnect
  flush) + CreateInputs + reaper run in `startBackgroundSync`, rooted at a
  ctx `closeHandles` cancels; `task.finish` always runs so `AwaitSync` never
  deadlocks. `waitForPodReady` poll 2s→1s.
- **Turn-staging gate (Fable):** `stagedRunner` wraps both `Connection.Runner`
  and `Session.Runner()` — `StartTurn` awaits `AwaitSync` (instant once
  settled; ctx-cancellable; other methods pass through), restoring the
  "no turn before the workspace is staged" invariant for every consumer.
  `sandbox turn` (DialRunner, no sync lifecycle) correctly ungated.
- **Late advisory surfacing (Fable):** `ConnectResult/CreateResult.AwaitWarning`
  → connector populates with `sess.AwaitSync` → App polls once per attach and
  appends `⚠ …` to the session's transcript (attached or retained-warm) via
  `syncAdvisoryMsg`; opencode external-pane drops it, matching the existing
  Warning behavior on that path.
- Public SDK impact: one added method (`Session.AwaitSync`); sdktest green.

## Fable-coordinated batch 3 (2026-07-06) — §2a one reducer + §7a cred hardening

### §2a — one event reducer (Opus build, Fable-verified)

- `sessionReadModel` (new `readmodel.go`) embedded by BOTH the dashboard
  `Session` and `TranscriptModel`; field names match Session's old exported
  fields so ~26 reader files needed zero churn (promotion). One `ApplyEvent`
  reducer; the 6 doubly-parsed payloads (session.started, usage,
  context.compacted, workspace.status, permission.requested, session.status)
  now unmarshal in exactly one place (independently grep-verified).
  `handleEvent` keeps presentation only (blocks/streaming/permission UI);
  `ApplyRunnerEvent` keeps dashboard extras (auto-title, RecentTools, glyph
  flash). `ApplyEvent` returns the parsed payloads the transcript needs so
  nothing re-parses.
- SessionSnapshot deliberately kept flat (on-disk JSON shape unchanged;
  encoder ownership outside the dashboard); saveSnapshot/applySnapshot read
  via promoted fields with all preserve-guards intact.
- Two documented divergences unified, both verified safe: workspace
  `Branch==""` preserves (was transcript-zeroing; unobservable);
  `permission.resolved`→busy unconditional — safe because the runner settles
  each permission exactly once, the interrupt path auto-denies BEFORE the
  turn-terminal event, and a post-settle client resolve is a server-side
  no-op, so resolved always precedes turn.completed/interrupted in the log
  and the busy→needs-input correction follows in-order (replay preserves log
  order).
- 90 composite literals rewritten to nest `sessionReadModel{…}`; full suite +
  `-race` + lint green.

### §7a — opencode credential hardening, items 3–5 + docs (Opus build, Fable-verified)

- **One provider key per session, fail-closed:** `Spec.OpencodeProvider` +
  canonical constants; `opencodeEnv(spec, name)` injects exactly the selected
  provider's SecretKeyRef with `Optional` removed — missing key stalls the
  pod in `CreateContainerConfigError` instead of starting uncredentialed.
  Finding: no provider selection reaches CreateSession today (defaults
  Anthropic); the user-facing selector is §6's client/cred item, and it must
  VALIDATE (resolveOpencodeProvider currently defaults unrecognized values).
- **Freshness stamps:** `sandbox.cullen.dev/opencode-creds-hash` (first 8 hex
  of sha256 of the selected key) + provider annotation at create;
  `warnIfOpencodeCredsRotated` on the idempotent re-create path; Resume
  re-stamps to the current Secret. Local script's kept-stale-Secret branch
  warns loudly.
- **Hardening:** secret-prefix printing removed from `cmd_status`; 0600
  overlay enforcement; namespace = `$SANDBOX_NAMESPACE` → kubeconfig context
  → default; reaper `secrets: get` moved from the ClusterRole to a namespaced
  Role/RoleBinding in `agent-sessions` (reaper genuinely needs the read:
  `RunnerToken` for the `/idle` poll auth). k8s/README already consistent.
- **README:** OpenCode credentials section (keys→env table, fail-closed,
  rotation-requires-restart, scoping, persistence).

## Fable-coordinated batch 4 (2026-07-06) — §1d scaling + §2c tool cards + §10 tracing

### §1d — connection-scaling cluster (Opus build, Fable-verified)

- **Steady-state cap:** observer forwards capped at 16 (`WithMaxObserverStreams`;
  below the ~30-forward API-server pressure point, above warmSoftLimit 12).
  Recency on `nowFunc` (stream-ready, every applied live event, focus);
  coldest evicted at stream-ready; attached + needs-attention rows are never
  victims; admission gate stops over-cap launches; eviction tears down
  forward+SSE+reader+idle-timer+warm model. Reconnect-on-focus via cursor
  movement. Recorded tradeoff: evicted rows keep watch-driven lifecycle
  status but SSE-derived attention can go stale — mitigated because active
  turns keep sessions warm; a cluster-side attention signal is the escape
  hatch if caps tighten.
- **Terminal-forward teardown:** reconnect errors thread into
  `liveSSEReconnectFailedMsg.err`; `session.ErrSessionGone` (the c191c85
  terminal condition, surfaced through the reconnect path) aborts retries
  immediately (~2s vs ~14s budget) and drops the warm model. The literal
  `ForwardHandle.Done()` channel remains unexposed through client/cli —
  optional `ConnectResult.ForwardDone` seam noted in TODO.
- **attachGate:** foreground attach/create shuts a gate observers wait on
  before taking a connectSem slot — foreground never blocks, observers yield.
- 11 new tests (cap/evict/protect/admit/focus/gone/gate); suite deterministic
  under -count=3 -race.

### §2c — tool-card two-line redesign + expansion (Opus build, Fable-verified)

- `⏺ Bash(npm test)` head (bullet toned by status: Malibu/Guac/Coral) +
  `  ⎿  exit 0  (ctrl+o to expand)` elbow. ctrl+o (composer empty) toggles
  the latest card: edit tools re-render their +/− diff from retained input
  via the permission_diff machinery (post-approval diffs no longer vanish);
  output tools show captured output (runner `capToolOutput` bounds it at
  64KB head+tail at BOTH tool.completed and tool.failed emits — verified;
  display further clamps 20+6 lines); arg fallback only when head truncated.
  With a draft in the composer ctrl+o keeps its $EDITOR role (slice 5g).
- `ToolPayload.output` already existed in the schema — no schema/gen change;
  gen verified drift-free.
- §1c budget overflow fixed by construction (measured ANSI-aware budgets,
  per-line truncate backstop; width 20/30/40 tests).
- Golden diffs (4, each reviewed): TestGoldenToolCard,
  TestGoldenTranscriptStream, TestGoldenTranscriptByBackend/{claude,opencode}
  — all exactly the head/elbow re-skin. Permission/plan/dashboard goldens
  byte-identical (shared styleDiffLine refactor verified no-op there).
- Follow-ups recorded in TODO: subagent child-tool lines still on the old
  budgeting; per-card focus for older cards; exit-code-in-elbow needs §2b
  gap 5.

### §10 — tracing first cut (Opus build, Fable-verified)

- `client/trace.go` (89 lines, unexported, nil-safe no-op when off):
  `SANDBOX_TRACE=1` or `sandbox --trace` (flag sets the env var in
  PersistentPreRun). Spans: connect.{total,status,pod_ready,port_forward,
  runner_health,project_sync,opencode_ready} + background
  {first_flush,create_inputs,reaper} under one 4-byte correlation id;
  create.{total,ssh_key,session}. Envelope: `trace: <id> <name> <ms>`.
- `runner/src/trace.ts` (56 lines, injectable now/log):
  turn.first_message / turn.first_delta / turn.settled + msgs= count.
- `sandbox trace` (event replay) deliberately untouched. SDK surface
  unchanged. Next-instrumentation list recorded in TODO.

## 2026-07-06 — TODO.md prune (maintainer ask)

All checked-off items were removed from TODO.md outright (previously retained
as one-line summaries). Their detail is in the sections above and in
done-log-2026-06.md. Residual "STILL OPEN" tails were promoted to standalone
open items: §1c statusline row-1 tail + subagent child-tool budgeting; §1d
ForwardDone seam + mutagen conflict detail; §1e index re-arm follow-up; §2a
row-model consolidation (from §1b); §2c tool-card follow-ups; §4
visibleSessions memoize + lastCompleteBlock rescan (both measure-first); §7a
ClusterRole namespacing; §7c observer interrupt-id leak.

Trivial one-liners removed without a matching detail section above, recorded
here for completeness:

- §3: README documents the supported `claude --resume` escape hatch
  (one-way-fork / exits-the-audit-envelope caveat; `claudeSession` field name
  verified against the status API + local index).
- §5: `waitForPodReady` poll tightened 2s→1s (1s not 500ms — gentle on the
  API server).
- §7a: README OpenCode auth section — keys→env table, fail-closed + rotation
  semantics, namespace scoping, suspend/resume persistence.
- §7: Fable review of the OpenCode idle/status/reaper fix — APPROVED; caveat
  worth keeping in mind: the dashboard `clearPendingPermission()` calls are
  safe today because `setStatus` dedups and busy/idle fire only at turn
  boundaries (`runner/src/session.ts:202`, `claude.ts:345`) — re-verify if
  status emission points ever grow.
- §10: PTY-test in-sandbox caveat documented in CLAUDE.md; `just check`
  honest skip report; sdktest tui surface pins; `client.RunnerClient`
  widening pin (all Fable-approved 2026-07-06, detailed in earlier batches).

## 2026-07-08 — batch 1 of the systematic TODO burndown: [A1]+[F2]+[F1]+[C2]

Three parallel Opus implementers + one Opus adversarial reviewer + Fable
review; `just check` green (all gates, race-twice, e2e), runner suite 177
pass / 0 skipped. Provenance: docs/review-2026-07-07.md.

- **§1f [A1] — RUNNER_TOKEN stripped from agent child processes (HIGH).**
  New `buildAgentEnv` (claude.ts) applied at both SDK spawn sites
  (buildOptions + title summarizer) and `buildOpencodeServeEnv` (opencode.ts)
  for the `opencode serve` child — both start from `sanitizedExecEnv` (the
  /exec denylist) and restore only the creds each child needs
  (ANTHROPIC_API_KEY/CLAUDE_CODE_OAUTH_TOKEN for claude;
  OPENCODE_SERVER_PASSWORD + the three provider keys for serve).
  Fable-review addition: `emitWorkspaceStatus`'s git calls run in the
  agent-writable workspace and inherited full env — a repo-local
  `core.fsmonitor` would have executed with RUNNER_TOKEN in scope; those
  calls now get sanitized env + `-c core.fsmonitor= -c core.hooksPath=/dev/null`
  (verified harmless to branch/dirty/ahead-behind output on git 2.54).
  Tests: runner/test/child-env.test.ts (incl. a live supervisor spawn-spy).
  ADVERSARIAL-REVIEW RESIDUAL (tracked as a new §1f item): runner + agent
  share uid 0, so /proc/<pid>/environ still recovers the token —
  raised-bar, not closed; real fix is uid separation/hidepid.
- **§10 [F2] — PreToolUse Bash guard pinned (CRITICAL gap).**
  `makePreToolUseBashHook` exported with an injectable `emit` seam;
  runner/test/pretooluse-guard.test.ts table-tests block/allow over real
  `PreToolUseHookInput` shapes (block ⇒ `decision:'block'`+`continue:false`
  + one tool.failed emit; benign ⇒ `{continue:true}`, no emit; missing/
  non-string command edges). Forward-compat note (legacy `decision:'block'`
  vs `hookSpecificOutput.permissionDecision`) tracked as a new LOW item.
- **§10 [F1] — CI now runs the SQLite event-log suite (CRITICAL gap).**
  ci.yml: `npm rebuild better-sqlite3` after the --ignore-scripts install +
  `RUNNER_REQUIRE_SQLITE=1` on the `just check` step. New shared
  runner/test/sqlite-probe.ts: skips cleanly when the addon is absent,
  THROWS at import when the env var demands it — verified both paths
  empirically (fail 1/nonzero vs skipped-clean). events.test.ts +
  schema-version.test.ts (the only two sqlite-gated suites, verified by
  grep) both consume it. Local macOS/Nix caveat: `npm rebuild` needs
  `CC=clang CXX=clang++` on this host; CI is depot-ubuntu and unaffected.
- **§1d [C2] — non-Claude model lookups resolve (MED).** `lookupKeys` tries
  the RAW lowercase id (dated suffix stripped, dots preserved — models.dev's
  verbatim keying for opencode/openai) before the `claude-` alias;
  `lookupEntry` preserves the deterministic multi-provider pick. Fixes
  opencode sessions reading 200k/$0 (wrong ctx%/cost). Adversarial check:
  raw/alias key forms are structurally disjoint for every Claude id — no
  prior resolution changes; static fallback unchanged.

## 2026-07-08 — handoff-review batch 2 (§2b D1/D2/D4, §1f B1–B4)

- **§2b [D1] — tool completion id-matched; pending cards drained at turn
  boundaries (HIGH).** `finishToolCard` now takes the event's `toolUseId` and
  closes the exact card via the existing `flatTools` id-map (new
  `removePending` keeps the FIFO consistent for out-of-order closes); the FIFO
  pop survives only as the fallback for id-less events (the PreToolUse-hook
  synthetic `tool.failed`, pre-toolUseId runners). New `drainPendingTools`
  runs in all three turn-terminal handlers (completed/interrupted/failed) so
  an interrupted tool can never render "running" into the next turn or poison
  later FIFO matches. Tests: `transcript_d1_d4_test.go` (non-head id close,
  parallel tools, drain on each terminal, FIFO fallback).
- **§2b [D2] — mid-turn pod crash no longer replays as "working forever"
  (HIGH).** `loadSessionState` returns `bootEvents` from new
  `orphanedTurnBootEvents`: when a persisted `busy` is coerced to `idle`,
  boot appends `turn.interrupted {reason:'runner restart'}` (turn id from
  `last_turn_id`, which setLastTurn persists before the status flips) +
  `session.status_changed {idle}` BEFORE the boot `session.started`, so
  replay terminates the orphaned turn. Tests:
  `runner/test/session-boot-events.test.ts`.
- **§2b [D4] — interrupt mid-think tears down the live reasoning tail
  (MED).** `finalizeStreaming` resets `m.reasoning`/`reasoningBuf` (no
  backend emits `reasoning.completed` on abort) and syncs items on the
  empty-assistant path; the "Thinking" tail no longer renders forever nor
  leaks into the next turn. Test in `transcript_d1_d4_test.go`.
- **§1f [B1] — opencode `serve` spawn failure no longer kills the runner
  (MED).** `startOpencodeSupervisor` registers `proc.on('error')`; error and
  exit share one respawn scheduler guarded per-child so error+late-exit
  respawns exactly once. Test: `runner/test/opencode.test.ts`.
- **§1f [B2] — POST /turns 409s on observer-synthetic opencode busy (MED).**
  New pure `turnRejectReason(backend, activeTurnCount, status)` in
  `server.ts` (unit-testable — a first bite of [F4]) mirrors the interrupt
  route's `opencode-server && busy` check. Tests:
  `runner/test/turn-gate.test.ts`.
- **§1f [B3] — /exec resolves at bash exit and SIGKILLs the process group
  (MED).** `runExec` spawns `detached:true` (own pgid), resolves on `'exit'`
  not `'close'` (a backgrounded grandchild holding the stdout pipe no longer
  hangs the call past the timeout), timeout kills `-pid` via
  `killProcessGroup`, and our pipe ends are destroyed post-resolve.
  `timeoutMs` injectable for tests. Trade-off: output written by a surviving
  grandchild after bash exits is dropped. Tests: `runner/test/exec.test.ts`
  (prompt return with `sleep 30 &`; group-kill reports 124 near the deadline).
- **§1f [B4] — persist-failure events reach the live stream (LOW-MED).**
  New pure `shouldDeliver(eventSeq, afterSeq)` in `events.ts`: seq-0 (the
  R11 insert-failure fallback) bypasses the `<= afterSeq` filter — real seqs
  start at 1 so there is no collision, and a reconnect simply never replays
  it (intended best-effort live-only delivery). Test in
  `runner/test/events.test.ts`.
- **Test-infra rider:** the external-pane PTY test
  (`TestAppExternalPaneEscIsForwardedNotDetached`) now SELF-SKIPS visibly
  when `opencode` or a PTY is unavailable instead of failing, and the
  `opencode` CLI is pinned in the flox env (linux + aarch64-darwin — upstream
  has no x86_64-darwin build) so local unsandboxed runs and Depot CI exercise
  it for real. CLAUDE.md caveat updated.

## 2026-07-09 — handoff-review batch 3 (§1d C1/H1/H2/H3, the observer-cap cluster)

- **§1d [C1] — port-forwards get a real close seam; every discard path
  releases its transport (HIGH).** `ConnectResult`/`CreateResult` gain
  `Close func()` (→ `client.Session.Close`, which closes the SPDY forward
  handles + their reconnect loops); the CLI connectors wire it. Dashboard:
  the ready message carries it, `liveSSECloses` registers it, and
  `cancelLiveSSE` — the single stream-teardown choke point (eviction,
  suspend, supersede) — invokes it; ready-msg discard paths (raced
  duplicate / session gone / attached-owns-stream) use a shared `discard()`;
  `EventsPassive` failure and the approveCmd one-shot fallback close after
  use. Foreground too: `parkTranscript` (the single detach hook) closes the
  transcript's transport (`TranscriptModel.transportClose`; the background
  observer + autopilot reroute own the session post-detach), the external
  pane's real teardown (`close()`, never minimize) releases the opencode
  forwards after the child dies, and a stale-generation `attachReadyMsg`
  is closed instead of silently dropped. Tests:
  `observer_cap_test.go` (choke-point close, all three discard paths,
  detach + pane close).
- **§1d [H1] — observer cap protects the right rows (MED-HIGH).**
  `observerProtected` no longer blankets all attention rows: Waiting/Failed
  (+ attached) stay protected; NeedsInput — the steady state of every
  session that ever completed a turn — is protected only while it carries
  UNSEEN output (`lastSeq > seenSeq`; hydrate marks history seen, so a
  relaunch with a fleet of completed sessions evicts down to the cap again
  instead of admitting all of them). Tests: unseen-vs-seen protection + the
  end-to-end needs-input-fleet eviction oracle.
- **§1d [H2] — eviction no longer destroys detached work (MED).**
  `observerProtected` also protects a warm model with an armed
  `/loop`/`/goal` driver or a queued prompt; `evictObserver` keeps the
  retained model (the cap targets API-server forward pressure, not RAM —
  and C1 means eviction now actually releases the forward), preserving the
  O(1) re-focus swap.
- **§1d [H3] — evicted Busy rows unfreeze; lapse toast stops lying (MED).**
  `evictObserver` stamps a runner-derived Busy row back to its
  watch-derived baseline (nothing is left to flip it once the stream is
  gone); the autopilot lapse toast is cause-agnostic ("suspended or
  unreachable") since that path can't distinguish suspend from delete from
  a dead stream.

## 2026-07-09 — handoff-review batch 4 (§4 E1/E2/E3/E5/E6, the perf hot paths)

Implemented by Opus subagents under Fable orchestration; every diff audited
line-by-line against the review's invariants before landing.

- **§4 [E1] — tool.delta handler no longer O(n²) (HIGH).** `toolCard.rawJSON`
  string → `rawBuf strings.Builder` (O(N) accumulation); preview extraction
  throttled off a `lastExtractLen` watermark (parse every delta under 2KB for
  live feel, then once per +2KB of growth — the preview is cosmetic, the
  finalized tool.started overwrites `arg`); the per-delta `syncItems()` list
  rebuild replaced with a lone `Bump()`, mirroring `streamDelta`'s
  refresh-in-place (list cache keyed on (item, width, version)). New test-only
  `argExtracts` counter; `transcript_e1_test.go` pins behavior (small-input
  preview + full accumulation) and cost (200 deltas → 28 parses; 0 reconciles
  with version still advancing).
- **§4 [E2] — runner SSE replay streams in bounded chunks (HIGH).**
  `replayTo` (`.all()` whole log + JSON.parse + re-stringify, one synchronous
  write burst) → `streamReplayThenAttach`: 512-row `LIMIT` chunks (each fully
  materialized before any await — an open better-sqlite3 iterator held across
  a yield would break concurrent appendEvent INSERTs), raw `payload`-column
  frame splice (`rawFrame`, byte-identical to live `JSON.stringify` frames
  incl. omit-turnId-when-NULL), `await drain` on write()===false +
  setImmediate yields between chunks. Ordering contract preserved via a
  `replaying` client flag (broadcast skips a replaying client; its
  during-replay appends are picked up from SQLite by a later chunk — exactly
  once, in order) + a fully synchronous zero-rows handoff (set afterSeq,
  clear replaying, write `: replay-complete` in one tick). Audited deviation
  from the orchestrator's sketch: the client registers in `clients` at attach
  (not after replay) so RV6 idle-count and M33 cap semantics are unchanged.
  Disconnect mid-replay aborts the loop; a thrown replay routes to cleanup
  (no unhandled rejection). seq-0 (B4) live bypass untouched.
- **§4 [E3] — live SSE broadcast gets a backpressure cap (MED ✓✓).**
  `broadcast` destroys + cleans up a client whose `res.writableLength`
  exceeds `MAX_SSE_CLIENT_BUFFER_BYTES` (4 MiB) — a wedged/half-open reader
  can no longer grow runner RSS until the pod OOMs; it reconnects and replays
  from its last seq. The replay path deliberately keeps drain-await as its
  own flow control instead. Tests: `runner/test/events-replay.test.ts` (9
  tests: order/boundary/multi-chunk/backpressure/raw-splice round-trip incl.
  control chars/NULL turn_id/mid-replay disconnect/after=mid/seq-0). Audit
  fix: raw \x00/\x01 bytes in a test literal escaped to \u-escapes.
- **§4 [E5] — passive SSE streams batch-drain (MED ✓✓).** New
  `liveSSEBatchCmd` + `RunnerEventBatchMsg` mirror the foreground
  `waitForEvent` 512-drain: block for the first event, non-blockingly drain
  the burst, ONE Update+View per batch (was one per event — 3-5 busy warm
  sessions ≈ 100-150 render pipelines/s). `handleRunnerEvent` split into
  shared `applyRunnerEvent`/`handleStreamEnded` so single- and batch-paths
  reduce identically (audited move-only); stale-gen guard gates the whole
  batch; close-mid-drain applies drained events then the ended handling;
  per-batch post-handling (anim/notify) runs once. `model_e5_test.go`.
- **§4 [E6] — live reasoning tail wraps incrementally (MED).**
  `wrapLiveReasoning` caches the styled complete-lines prefix (lipgloss wraps
  hard lines independently, so a completed line's wrap never changes) keyed
  by width + theme epoch, re-wrapping only the trailing partial per frame;
  cache resets at all three `reasoningBuf.Reset()` sites incl. the D4
  `finalizeStreaming`. Audit note: TrimSpace-on-a-growing-buffer keeps the
  cached prefix valid (the old trimmed text is always a prefix of the new).
  Oracle tests pin byte-equality with the full wrap incl. blank lines, plus
  width/theme invalidation and a cost bound. `transcript_e6_test.go`.

Still open in §4: E4 (delta compaction — retention design), E7-E10 (LOW),
and the older measure-first items.

## 2026-07-09 — handoff-review batch 5 (§1f A2/B5-B9, §2b D3/D5, §4 E4)

Three Opus subagents in parallel (file-disjoint packages) under Fable
orchestration; diffs audited line-by-line, one cross-agent seam fixed by the
orchestrator.

- **§1f [A2] — event log + SSE redact secrets (LOW-MED).** Redaction factored
  out of audit.ts into shared `redact.ts` (byte-identical logic, re-exported
  for back-compat); `appendEvent` masks `turn.started`/`tool.*`/`permission.*`
  payloads BEFORE persist and broadcast, so log, live frames, and (via E2's
  raw splice) replay all carry the masked form. Orchestrator integration fix:
  role-`user` `message.*` events are masked too — the D5 user echo carries the
  same prompt text `turn.started` does, so masking one but not the other
  leaked anyway; assistant message.* stays untouched and message.delta never
  pays the walk. Tests: events-redaction.test.ts incl. the A2×D5 seam pin.
- **§4 [E4] — delta-only compaction (MED ✓✓).** On `turn.completed`, one
  bounded DELETE removes `*.delta` events older than the last N turns
  (`DELTA_COMPACT_KEEP_TURNS`, default 2 = current + previous, so a
  just-detached client still replays its live tail; invalid env → default).
  Non-delta events always survive (full replay still reconstructs the
  transcript; seq gaps are within the after=<seq> contract). Best-effort:
  compaction failure never fails the append (R11 stance). Distinct from
  M34's rejected all-or-nothing retention; safe against E2's seq-cursor
  chunked replay; no VACUUM (file plateaus, never shrinks — that's the goal).
- **§1f [B5-B9] — runner robustness LOWs.** B5: SSE `after` beyond the head
  clamps to `lastSeq` (pure `clampAfterSeq`) instead of silently swallowing
  every live event. B6: `emitWorkspaceStatus` git calls now async
  (promisified execFile; A1 env sanitization + fsmonitor/hooksPath disarming
  preserved verbatim; `mapMessage` awaited so workspace.status still lands
  after turn.completed) — no more ~9s of blocked event loop per turn worst
  case. B7: corrupt session.json is moved aside
  (`session.json.corrupt-<ts>`) + logged + reseeded instead of crash-looping
  the pod; no bootEvents from an unrecoverable file. B8: permission resolve
  is first-write-wins — a POST that loses to the deadline/abort/detach
  auto-deny returns 409 `{resolved:false, reason:'expired'}` instead of
  lying `resolved:true` (Go client treats non-2xx as a visible error and
  reads `{error}` — verified read-only). B9: `readBody` rejects with typed
  `BodyTooLargeError`/`InvalidJsonError` mapped centrally to 413/400.
  Tests: robustness-b5-b9.test.ts, session-corrupt.test.ts.
  `docs/runner-api.md` updated in-change (409 case, after-clamp, 413/400,
  redaction + compaction replay notes).
- **§2b [D3] — turn.* payloads on-schema (MED).** Four payload definitions
  added to schema/events.json from the field union across ALL emitters
  (claude/mapping/opencode-turn/opencode-observer/server/session):
  `turn.started{prompt?}`, `turn.completed{result?,stopReason?,numTurns?,
  durationMs?}`, `turn.failed{message,subtype?,errors?}`,
  `turn.interrupted{reason}`. `just gen` (idempotent, verified twice) +
  generator payloadOrder + hand-written Go structs registered in
  schema_test's payloadRegistry (the drift gate now covers them) + types.ts
  re-exports. TUI `turn.failed` decode switched from the coincidental
  ErrorPayload to the real TurnFailedPayload.
- **§2b [D5] — opencode replay shows user prompts (MED).** Chose the
  Claude-parity fix: the runner-driven opencode turn adapter echoes the
  driving prompt as `message.started/completed role:"user"`
  (`emitOpencodeUserPrompt`), the exact shape mapping.ts emits for claude —
  the reducer's existing role:user dedup (optimistic-block trimEqual)
  prevents double-printing on live sessions, and /loop-driven turns render
  their prompt too. The prompt-less observer path (external client owns
  input) is untouched. Tests: transcript_d5_test.go (replay order, no
  double-print, turn.failed payload), opencode-turn.test.ts echo pin.

## 2026-07-09 — handoff-review batch 6 (§2c H4-H7, §2b D6, §1d C3-C11)

Fable, inline (no fan-out — token-budget sensitivity); every fix carries a
regression test; `just check` green end-to-end (incl. race-twice + e2e).

- **§2c [H4] — expanded tool output sanitized (MED).** New
  `sanitizeToolOutput` in `clampOutputLines`: CRLF→LF, lone-CR keeps only the
  final line state (progress-bar rewrites), and `stripNonSGR` drops every
  escape except SGR color runs (via a shared `ansiSeqEnd` scanner) — cursor-up
  /erase-line sequences no longer execute inside the composited frame and
  smear the transcript. SGR still flows to `kit.RemapANSI` for theming.
  Test: TestExpandedOutputSanitized.
- **§2c [H5] — tabs expanded before truncation (MED-LOW).** `expandTabs`
  (ANSI-aware 8-column stops) applied in `clampOutputLines` and inside
  `styleDiffLine`; `permission_diff.go` reordered to style-then-truncate so
  the box budget sees post-expansion width. Covers expanded Edit diffs of
  tab-indented (Go) files AND the pre-existing permission-box variant.
  Test: TestExpandedOutputTabsExpanded.
- **§2c [H7] — ctrl+o skips inexpandable cards (LOW).** `toggleLatestToolCard`
  gates on new `toolCardExpandable` (same width math + `toolExpandBody` call
  as the renderer, via the extracted `headArg` helper renderToolCard now
  shares) — no more silently-swallowed ctrl+o or stranded `expanded=true`
  popping a card open when output arrives later; falls through to $EDITOR
  when nothing is expandable; collapse of an open card always works.
  Tests: TestToggleSkipsInexpandableCards, TestToggleNoExpandableCardFallsThrough.
- **§2c [H6] — opencode tool output capped (LOW).** `opencode-turn.ts` wraps
  `st.output` in the same `capToolOutput` the claude path uses (64KB,
  head+tail + truncation marker) at the emit site. Test: opencode-turn cap pin.
- **§2b [D6] — tool.delta attributed by id (MED).** No schema change needed
  (ToolPayload already carries the optional ids): `mapping.ts` tracks
  `(parentToolUseId, blockIndex) → tool_use id` per turn (`StreamToolIndex`,
  fresh per query() attempt in claude.ts; non-tool block starts clear a
  reused index) and stamps `toolUseId`+`parentToolUseId` onto every
  `tool.delta`. TUI targets the exact card via `flatTools`; a parented delta
  with no flat card is DROPPED (subagent input no longer animates onto a
  main-thread card's arg); only id-less (pre-D6 runner) deltas fall back to
  newest-pending. Tests: mapping.test.ts (cross-stream attribution + index
  reuse), TestToolDeltaTargetsCardByID.
- **§1d [C3] — shape-changing re-create rejected (MED).** The pod template
  bakes the credential env SHAPE (oauth vs api-key env var; per-session vs
  shared source Secret) at first create. CreateSession now compares the
  desired shape (`anthropicEnvShape`) against the existing Sandbox BEFORE
  mutating the Secret and rejects mismatches with a destroy-and-recreate
  error (+ belt-and-suspenders check on the Sandbox AlreadyExists branch).
  Consciously supersedes the old strip-on-account-removal behavior — stripping
  a key the baked non-Optional SecretKeyRef still references would brick the
  next resume. Same-shape account swaps still patch bytes+label in place.
  Tests: TestCreateSessionRejectsAuthShapeChange,
  TestCreateSessionSameShapeAccountSwapPatchesSecret.
- **§1d [C4] — observer connect forwards 1 port (MED-LOW).** `case !full`
  moved above `case opencode` in Connect's forward switch: background
  observer streams to opencode sessions no longer carry unused SSH+opencode
  forwards (3→1 SPDY streams per row).
- **§1d [C5] — ssh config paths quoted (LOW-MED).** `IdentityFile %q` +
  `Include %q`; legacy unquoted include lines still recognized so older
  configs don't get a duplicate prepended. A spaced state dir
  (macOS `Application Support`, the documented WithStateDir shape) now
  produces a valid config. Test: TestSSHConfigQuotesSpacedPaths.
- **§1d [C6] — background connect phase bounded (LOW-MED).** One 60s
  `WithTimeoutCause` deadline over flush+CreateInputs+reaper so a wedged
  mutagen daemon can't hang `task.finish` and turn the AwaitSync gate into
  "prompt submitted, nothing happens"; the deadline (vs a closeHandles
  cancel, which stays silent) surfaces as an explicit advisory.
- **§1d [C7] — pre-existing PVC survives rollback (LOW).** Rollback guard now
  keys on `secretPreexisted || pvcPreexisted`; a prior session's workspace
  PVC can no longer be deleted as collateral of a failed re-create whose
  Secret happened to be fresh. Cost: at most an orphaned fresh Secret.
  Test: TestCreateSessionPreexistingPVCSurvivesRollback.
- **§1d [C8] — projectPath race fixed (LOW).** Write + fresh-path read +
  ProjectPath() all under `s.mu`; Connect uses a captured local afterwards.
- **§1d [C9] — suspend probe capped at 5s (LOW).** Same explicit
  `WithTimeout(5s)` destroy already used; a half-dead node no longer stalls
  suspend ~40s.
- **§1d [C10] — models.Limit never blocks on the network (LOW).** `load()`
  serves the fresh disk cache synchronously; on a cold/stale cache it serves
  the stale table / static fallback immediately and refreshes models.dev in a
  background goroutine (`refresh` + `awaitRefresh` test seam) — the first
  session.started/usage event of the day no longer freezes the TUI reducer up
  to 5s. Test: TestColdLimitDoesNotBlockOnNetwork (+ prime() in the fetch
  tests).
- **§1d [C11] — reaper override honored (LOW).** EnsureReaper compares the
  live Job's container image/pull-policy/args against the desired spec
  (`reaperSpecMatches`) and delete+recreates on mismatch — a reconnect with a
  different IdleTimeout/ReaperImage is applied instead of silently
  first-writer-wins; the idle clock lives runner-side so nothing is lost.
  Test: TestEnsureReaperReplacesRunningJobOnSpecMismatch.

## 2026-07-11 — handoff-review batch 7 (§8 SDK narrowing, §10 F3-F5 coverage, small sweep)

Opus build, Fable-verified, landed slice-by-slice as each agent's work passed
review. Detail: docs/review-2026-07-07.md §F.

- **§10 [F5] — port-forward lifecycle covered; retry decision extracted pure
  (HIGH).** The reconnect re-resolve switch in `runForward` became
  `classifyForwardReconnect(pod, err)` (`forwardUseNewPod` / `forwardRetryStale`
  / `forwardTerminal`) and the capped-exponential wait became
  `nextForwardBackoff` — 1:1 behavior-preserving, mirroring the reap.go
  pure-decision split. Tests pin every classifier branch (typed + wrapped
  NotFound terminal; plain error / context.Canceled / nil-err-nil-pod all
  retry-stale; NotFound wins over a stray non-nil pod), the full
  500ms→1s→2s→4s→8s→10s ceiling, and the C1 Close-seam invariants under
  `-race`: Done fires only after Close, `h.done` closes exactly once under
  16×10 concurrent Close() calls, and error-churn racing concurrent Close
  still tears down with a non-nil terminal `h.err`. Tests:
  TestClassifyForwardReconnect, TestNextForwardBackoff,
  TestForwardBackoffProgression, TestRunForwardCloseCausesDone,
  TestRunForwardCloseIsIdempotentAndDoneClosesOnce,
  TestRunForwardConcurrentErrorAndClose.

- **§10 [F4] — runner HTTP layer covered by a real-server suite (HIGH).**
  `startServer` split: exported `createRunnerServer(cfg, agent)` builds the
  router + B9 error-mapping without listening (routing byte-identical);
  `session.ts` gained `__setSessionJsonPathForTest` (mirror of
  `__setEventLogForTest`) so the turn-accept persistence path runs off-pod.
  New `runner/test/server-http.test.ts`: 17 tests booting the real server on
  an ephemeral port with a real better-sqlite3 event log — healthz
  unauth+protocolVersion, 401 missing/wrong bearer, 404 unknown route/wrong
  session (no cross-session leak), the full 409 turn-gate matrix (concurrent
  turn, opencode synthetic-busy, supervise-only null agent), B9 typed 400s
  (malformed JSON, missing prompt), SSE `after=` contiguous replay +
  replay-complete boundary + live flow, the B5 bogus-cursor clamp, R8 400 on
  bad cursors. Runner suite 227→244, 0 skip under RUNNER_REQUIRE_SQLITE=1.
  Found (logged in TODO §10): oversized bodies reset the socket before the
  mapped 413 can be written (`httputil.ts` destroys synchronously); the
  fake-runner-faithfulness half of F4 promoted as a MED residual.

- **§8 — public `client.Backend` interface narrowed (+ two decided client
  behaviors) (HIGH enabler).** 12-method interface = exactly the
  orchestration call sites (Namespace, CreateSession, Status, List, Suspend,
  Resume, Destroy, StartWithProgress, PortForward, RunnerToken,
  OpencodePassword, EnsureReaper); `WithBackend` takes it; `var _ Backend =
  (*k8s.Backend)(nil)` + a new sdktest signature pin. Documented caveat: not
  externally implementable while `EnsureReaper` names
  `internal/k8s.ReaperOptions`. In the same change: `Destroy` stops sync
  BEFORE the cluster destroy (mutagen stream torn down while the pod is
  alive; best-effort, so not gated on destroy success) and `DialRunner`
  forwards the runner HTTP port only (`ForwardSpecsRunnerOnly`), dropping the
  unused SSH SPDY stream. Plus an unexported `Client.syncRunner` seam so
  tests observe mutagen calls without a daemon.
- **§10 [F3] — client orchestration covered (HIGH).** New
  `client/orchestration_test.go`: `fakeBackend` + `fakeSyncRunner` share one
  ordered call log; TestClientCreate (spec propagation, fresh-path shortcuts,
  index save, validation-before-cluster, error propagation), Status/List,
  Suspend/Resume (backend-error short-circuits skip the sync verb; success
  order pinned), TestDestroyStopsSyncBeforeClusterDestroy (the §8 reorder
  regression net: sync-terminate → destroy, index entry removed only on
  success and preserved on failure), TestDialRunner (runner-only forward
  specs; cleanup and token-failure paths close the forward exactly once).

- **§1f [A3] — SECURITY.md posture rewrite (INFO).** Revised in place (the
  file predated A2 and missed the A3 asks): 0.0.0.0-binds table
  (runner 8787 / sshd 22 / opencode 4096) with the containment split —
  default-deny ingress + bearer token stop off-pod callers, nothing stops
  in-pod processes (the A1 mechanism); the example 443-to-any egress named
  plainly as the exfiltration channel with Cilium `toFQDNs` as the hardening
  path; the A1 residual documented with exact guarantees (env-strip raises
  the bar; /proc/1/environ recovery remains until uid separation); verified
  controls list, every claim carrying file:line evidence. Corrections found
  during verification: the review's "drop-ALL caps" was imprecise (12 caps
  re-added incl. SETUID + DAC_OVERRIDE — documented truthfully, and relevant
  to the A1 fix); stale pre-A2 wording replaced. Fable restored the
  permission-id entropy known-gap the draft dropped (shortId = 32 bits,
  `events.ts:661` — still true, bearer token is the containing factor).

- **§2b [D7/D8/D10/D11/D12] + §1f hook-shape — event-model LOW sweep.**
  D7: `makePreToolUseBashHook` emits nothing — the SDK's
  `tool_result(is_error)` is the single terminal (id-carrying; the old
  synthetic second `tool.failed` FIFO-corrupted card matching); guard test
  pins hook silence. Hook-shape: block result returns
  `hookSpecificOutput.permissionDecision:'deny'` + legacy `decision:'block'`
  together (both verified in the pinned SDK's sdk.d.ts). D8:
  `StreamToolIndex` → `{byIndex, names}`; id→name captured at both
  content_block_start and full-message tool_use; tool.completed/failed/delta
  now carry the schema-required `tool` (names deliberately NOT cleared on
  index reuse — a late tool_result still needs its name). exitCode deferred
  to §2c (hook-seam correlation). D10: `TurnRequestBody.advisor` added;
  `resume` documented as the AGENT session id (Go rename waits for §8
  AgentSessionID); compile-time mirror test added. D11: title passthrough
  hoisted above the headless-turn guard; pre-cycle `session.error` →
  synthetic turn.failed + error + status error, foreign sessions ignored
  (3 new observer tests). D12: `emitResultUsage` — exactly one usage.updated
  per result, real cost on success AND failure (failure previously dropped
  cost as 0); readmodel refreshes input/cache counters when ANY of the three
  is >0 so cache-only turns move ctx%, all-zero still can't clobber. Runner
  suite 244→251.
- **§4 [E7] — streaming-tail O(1) change key (MED-LOW).** `ensureStreamTail`
  keys on buffer LENGTH + mode + theme epoch instead of hashing (and
  copying) the whole live buffer per delta. Safe by construction: the empty-
  assistant-buf case nils the tail item (fresh Versioned on regrow) and
  reasoning.started syncs at length 0 before regrowth, so consecutive calls
  always see strictly-growing lengths — audited every Reset site
  (transcript_stream/reduce/commands). BenchmarkEnsureStreamTail: ~89ns,
  3 allocs, constant in buffer size (was O(L) hash + full string copy).
- **§4 [E8] — SSE consumer zero-copy scan loop.** `scanner.Bytes()` +
  `bytes.HasPrefix`/`CutPrefix`; safe because `json.RawMessage` copies the
  payload before the next Scan reuses the buffer.
- **§4 [E9] — events.ts prepared-statement cache.** `prepared(db, sql)`
  keyed to the open Database instance (reset on close/reopen so a Statement
  can't outlive its handle); INSERT/readEventsAfter/lastSeq reuse it;
  append-before-stream untouched. Test: rebind-after-reopen.
- **§4 [E10] — host event-cache: persistent handle + 8 MiB tail cap.** New
  `index.CacheWriter` (`OpenCacheWriter`/`Append`/`Close`); `indexEventCache`
  caches one writer per session (was ~5 syscalls per cached event);
  `LoadCachedEvents` reads only the final 8 MiB (drops the partial leading
  line); `compactCacheTail` stages the tail in a temp file + atomic rename.
  Durability unchanged (no user-space buffering before, none now). Known
  accepted edge: a second process's compaction can strand another process's
  open handle on the unlinked inode — best-effort cache, self-heals via
  runner replay. Test: TestEventCacheCapsTail (~16 MiB → bounded tail).

## 2026-07-11 — §9 per-session git worktree lifecycle (waves 1-4, design → archive)

/loop-driven: one Opus implementer per wave, Fable review + full `just check`
gate between waves. Design (all 10 questions pre-resolved):
docs/archive/worktree-lifecycle-design.md — Status block carries the layout
amendment. Commits b84f696, 633fe6d, fdcd208, d59690c.

- **Wave 1 — `Spec.WorkspacePath` split + state-dir break.** WorkspacePath
  (pod bind-mount / SDK cwd / both mutagen endpoints) split from ProjectPath
  (repo root: grouping/display/index); `SANDBOX_PROJECT_ROOT` env +
  PROJECT_PATH fallback so Status/List recover both on any pod generation.
  `ssh/` nested INSIDE stateDir (amendment: beats the sibling-diagram layout
  for WithStateDir containment; one-time dir-rename migration + ~/.ssh/config
  Include rewrite, C5 quoting preserved); `worktreesRoot()` reserved;
  `index.List` skips non-session dirs. Closed the §8 WithStateDir item.
- **Wave 2 — worktree engine.** `WorktreeMode` (Auto default/Off/On) on
  CreateOptions; `worktree add -b sandbox/<id> <stateDir>/worktrees/<id>
  HEAD` before the cluster create, deferred rollback (WithoutCancel + 30s)
  on create failure; unborn-HEAD repos fall back under Auto (Fable-added
  edge + test); index persists path/branch/repo-root; Destroy gains
  capture-then-remove BEFORE RemoveLocalState (dirty → WIP commit
  `--no-gpg-sign`, failed commit blocks removal, branch always survives);
  Connect skips file sync with a warning when the worktree dir is gone
  (empty-alpha delete-storm guard; doubles as §4.10 B1 cross-machine
  behavior). Sentinels ErrNotAGitRepo/ErrWorktreeExists/ErrWorktreeDirty.
- **Wave 3 — deterministic convert/status/reap surface.**
  `Session.WorktreeStatus` (live branch/dirty/changed); `ConvertToBranch`
  (check-ref-format up front → taken-target check BEFORE the commit so a
  collision never strands a stray commit → commit-if-dirty under the
  approved message → `branch -m`, never -M → index updated);
  `Client.ReapWorktrees` (classifies every dir: live/junk/unreadable →
  skipped, clean orphan → removed, dirty orphan → WIP-commit then removed;
  cluster List failure is fatal — never reap blind; prune per repo root;
  DryRun pure). Sentinels ErrNoWorktree/ErrInvalidBranchName/
  ErrBranchNameTaken. All temp-repo tested; sdktest pins the full surface.
- **Wave 4 — human half.** Dashboard `b` → convert modal behind a narrow
  `WorktreeOps` RunOptions seam (dashboard never imports client; sentinel
  mapping at the cli wiring): editable branch/message prefilled
  deterministically from the LLM-generated session title
  (`feat/<slug>`; resolution 8 — no proposal turn touches the transcript),
  ErrBranchNameTaken/ErrWorktreeDirty keep the modal with inline errors;
  rides `Open(id)` so convert works on suspended sessions. CLI
  `sandbox worktree gc [--dry-run]` prints the reap report;
  `--worktree auto|on|off` on claude/opencode (fail-closed parse).
  README + session-lifecycle updated.

Known residuals (tracked in TODO §1d/§3): non-git same-path collision
warning; B2 move-session-to-machine unbuilt; dashboard row Branch field
deliberately not updated on convert (pod-side source has no .git).

## 2026-07-11 — §1 burndown: server-side autopilot (§1e item 6) + §1c/§1d residuals

/loop-driven, Opus implements / Fable reviews+gates. ADR archived as
implemented: docs/archive/server-side-loop-adr.md. Commits 3c7aee1 (residual
sweep), 9943f59 (runner half), 21a709f (client/TUI half).

- **§1e item 6 — server-side autopilot loop.** Schema: `autopilot.state`
  (state/kind/reason/iteration/gen) via `just gen` + hand-written Go payload.
  Runner: `AutopilotSpec` persisted in session.json (explicit H3
  state/stopped_reason, retained on stop, arm overwrites + bumps gen);
  `PUT/DELETE /sessions/:id/autopilot` with typed 400s + 409 for driverless
  backends; `capabilities.autopilot` in /status (single-sourced
  `backendHasAutopilot`); driver in autopilot.ts behind an injectable host —
  self-submits via the shared startTurn path (extracted turns.ts owns the
  409 gate), sentinel/max_iterations(50)/token_budget stops, 409-defer
  (manual turns = free iterations), 5× retry ladder (max(interval,30s)
  doubling, 5m cap, gen-guarded, no iteration cost), 30m staleness lapse
  (anchor max(last_completed_at, boot, armed_at)), boot re-arm anchored on
  last_completed_at, persist-stopped-BEFORE-emit (crash-window rule); armed
  spec holds the session non-idle (Q1). Fable review fix: DELETE on an
  already-stopped spec preserves the original terminal record. Agent
  deviations accepted: interrupts reschedule without iterating/scanning;
  token accounting derives from summed usage.updated (restart-correct, no
  new spec field). Runner suite 251→278. Client/TUI: RunnerClient
  Arm/DisarmAutopilot (409→ErrAutopilotUnsupported, 404→ErrAutopilotNotArmed),
  public aliases + Session conveniences + sdktest pins; capability probed
  once at attach (5s-bounded — Fable fix, was unbounded); /loop//goal arm
  the runner driver when capable (chip `N/50`, renders purely from
  autopilot.state; background terminal toast/OS notification gated
  !dup + !catchingUp so replays never re-notify), stop paths DELETE,
  unexpected-unsupported drops the bit and falls back local. Fable fix:
  two tests synchronously executed interval tea.Ticks (dashboard package
  6s→307s — real 5m/5s sleeps inside execCmd); now assert synchronously
  and drive the first iteration explicitly. NOT live-verified on a real
  cluster yet.
- **§1e — driver-spec re-arm.** `index.Entry.Driver` via a `DriverStore`
  seam (all 3 RunOptions sites); bare `/loop` / `/goal` re-arms the recorded
  spec across re-attach.
- **§1c — subagent child tool lines width-safe.** `renderChildTool`
  budgets by construction (measured ANSI-aware prefix → name/arg/detail take
  remaining columns → whole-line truncate backstop), replacing independent
  w/2+w/3 caps. Test: TestSubagentChildToolWidthSafe (widths 8-80).
- **§1d — mutagen conflict per-file detail + hint.** `conflicts[]` decoded
  typed (`mutagenConflict` alpha/beta changes → `Conflict{Path,Alpha,Beta}`,
  defensive: unknown shape → count-only + "(path unavailable)");
  `Manager.StatusDetail`; SyncProber → `SyncHealth{Status,Conflicts,Hint}`;
  detail pane renders per-file lines (cap 5, "+N more") + the two-way-safe
  resolution hint. Live conflicted-mutagen shape unverified — flagged.
- **§1d — non-git same-path collision warning.** `sameDirSyncWarning` at
  Connect for non-worktree sessions: scans mutagen List, resolves other
  sessions' alphas from the index, warn-only, skips paused/self, silent
  without mutagen. Closes the §1d collision item entirely (git = §9
  worktrees, non-git = this warning).
- **§1d — transcript provenance audit trail.** `transcript-audit.jsonl`
  (state dir) appends deduped sandbox→claude-session-id mappings at the
  point the mapping is learned; survives destroy (the index entry that
  carried it does not). The unscoped ~/.claude merge stays by design.

Still open in §1: statusline row-1 overflow (folds into §2c), port-forward
mid-stream death detection (optional), §1f A1 uid-separation (gated on §7b),
hook-shape SDK-version-pin caveat.

## 2026-07-12 — batch: yolo default, ownerRef GC, §8 tui surface (cd0e87c..3d37f0e + bookkeeping)

/loop-driven batch (Opus implements, Fable reviews/gates/commits). Full
`just check` green (one round-trip: anti-cheat required `// gate-ok:` on a
color-based t.Skip).

- **§2d — yolo default (DECIDED 2026-07-07).** Runner
  `resolvePermissionMode` empty/unknown → `bypassPermissions` (was
  acceptEdits); SDK gate (`allowDangerouslySkipPermissions` +
  `IS_SANDBOX=1`) verified to cover the new default; `canUseTool` correctly
  omitted for bypass. TUI needed no status plumbing — it already pins the
  mode per turn (`transcript.go:499` defaults modeBypass,
  `autopilot.go:431` sends it) — so the statusline work was making bypass
  unmissable: inverted coral `⚠ bypass` chip (dark-on-Coral, bold) vs the
  quiet foreground tags for ask/auto/plan. 3 new statusline tests.
  `docs/runner-api.md` mode description updated.
- **§10 — oversized body now yields the mapped 413.** `readBody` no longer
  `req.destroy()`s synchronously on oversize (the route's catch mapped
  BodyTooLargeError to 413 a microtask after the socket died →
  ECONNRESET); it rejects once, discards further inbound bytes, lets the
  socket drain. The pinning test now asserts the 413 body arrives.
- **§6.3 — Secret GC for out-of-band deletion.** ownerReferences
  (Secret+PVC → Sandbox) set after the Sandbox exists (UID from Create
  return or the re-create Get), ONLY on resources this call created
  (`secretPreexisted`/`pvcPreexisted` guards — a pre-existing PVC is never
  adopted, C7), idempotent by UID, RetryOnConflict Get+Update, best-effort
  (warn, never fail the create). Credential reconcile preserves the ref
  (pinned). The C3 shape-check restructure (two Gets → one) is
  behavior-equivalent. 3 new tests + a fake-clientset UID reactor.
- **§7b — `go get .` activation hook removed** from
  `.flox/env/manifest.toml` (mutated go.mod as a cd side effect; decided in
  the accepted ADR). GOENV/KUBECONFIG lines kept.
- **§8 — public tui/* batch.** `theme.Register(Theme)` (replace-by-name
  case-insensitive, re-applies if the replaced theme is live, else append;
  startup-only like the rest of the registry) + exported
  `Denied`/`InfoSubtle`/`SuccessSubtle`/`WarningSubtle` active vars wired
  through ApplyTheme. tui/kit: every mutable render color (ANSI-16 table,
  component colors, rule/thumb, role accents) moved into one `palette`
  struct behind `atomic.Pointer` with copy-modify-store setters + a -race
  hammer test — two tea.Programs can no longer race a theme swap against a
  render; role map → fixed array with bounds-checked fallback.
  `FormatTokens` gains the B tier with boundary promotion (999,950,000 →
  "1B"); boundary table tests. tui/list: dead `Item.Finished()` dropped;
  sdktest pin updated in the same change.

## 2026-07-12 — batch 2: layout regions, §7c opencode trio, F6/F7 coverage (2967c48..0267985)

/loop batch 2 (Opus implements, Fable reviews/gates/commits). `just check`
green (one inline gofmt fix on a new test file; one review round-trip on the
opencode prompt positional).

- **§2a — declarative vertical layout regions (HIGH enabler).**
  `region`/`vlayout` types in transcript_render.go; `liveLayout()` (header,
  divider, body-flex, perm?, palette?, search?, gap, composer, statusline)
  and `previewLayout()` (banner variant) replace the four hand-counted
  copies; `scrollbarDragTo` reads `m.bodyTop()` over shared `headerBands()`.
  `App.modalRect` deliberately NOT folded in — popup margin geometry, a
  different axis; the scrollbar chain composes the two. Behavior-preserving:
  goldens/T1 byte-identical; new invariant tests (flex arithmetic, exact
  tiling when roomy, modalContent==frame height everywhere,
  render/hit-test agreement, perm band shrinks body not frame). Undersized
  frames still overflow by design (fitModal truncates), as before.
- **§7c — CLI opencode flags + initial prompt.** `--model`
  (provider/model), `--provider` (→ `CreateOptions.OpencodeProvider`,
  fail-closed), `[prompt]` positional. Review round-trip: the positional
  was initially inert (dashboard external-pane branch returns before the
  initialPrompt handoff) — reworked to a headless first turn via the
  existing `sandbox turn` precedent (StartWithProgress → DialRunner →
  `startPromptTurn` seam → StartTurn) BEFORE the TUI attaches; hard error +
  attach hint on failure, prompt cleared so it can't double-fire. 3 unit
  tests on the seam. NOT live-verified: attach-picks-up-in-flight-turn
  sequencing on a real cluster.
- **§7c — reasoning double-`message.completed` root-caused + fixed.**
  opencode `ReasoningPart` stores content in `.text` (same field as
  TextPart) so its `message.part.delta`s are indistinguishable by field;
  the mapper mis-registered them as assistant text and the `session.idle`
  flush re-emitted the reasoning as a trailing `message.completed` (seq 41
  vs 38 in the live capture). Fix: `reasoningParts` id set (from
  part.updated type:reasoning), deltas → `reasoning.delta`,
  `completeTextPart` + flush guarded, defensive delta-first undo. Both
  orderings pinned. Observer path covered too (shared mapper).
- **§7c — observer `interruptedTurns` leak bounded**: cap 8, oldest-first
  eviction (Set insertion order), regression test.
- **§10 [F6/F7] — coverage.** `waitHealthy` → `healthChecker` interface +
  `waitHealthyWithin` (injected budget/interval, literals preserved);
  tests: immediate/retry/deadline/cancel + 6 `Session.Connect` pre-dial
  branches (incl. token-failure forward teardown).
  `warnIfOpencodeCredsRotated`: fires-on-rotation (no key bytes leaked)/
  silent-fresh/no-stamp/unreadable, fake clientset + stderr capture.
  `evaluateIdle`: malformed IdleSince surfaces a parse error, M19 recheck
  error blocks suspend, Suspend error propagates (not errReaped).
  Residuals documented: Connect happy path + reaperTick wrapper need a
  runner-factory seam; model_sse.go closures excluded (dashboard owned by
  the §2a refactor this batch).

## 2026-07-12 — batch 3: row model, sync GC follow-ups, docs + e2e faithfulness (00f9b6e..709b463)

/loop batch 3. `just check` green after one round-trip (MF3's 5-field list
template broke the §1d collision test's producer-side fake — real daemon
output renders an EMPTY context field for legacy syncs, so production was
never wrong; malformed-row skip contract now pinned in sync + client).

- **§2a — dashboard row model consolidated.** Typed `listRow`
  (rowSession|rowHeader) slice from `visibleRows()`; `sessionAt(cursor)` /
  `selectedSession()` the single accessor across render, nav, actions,
  group toggle, zones overflow band, and attention routing.
  `jumpToNextNeedingAttention` unified — flat path resolves by session
  identity (was: raw cursor-as-session-index, safe only by 1:1
  coincidence), fail-closed translate-back preserved. q/g overloads left
  for the input-context binding tables (correct scoping — pure keybinding
  disambiguation). Perf note filed: visibleSessions() recomputed
  per-frame with no memoization (matches the existing §4 item).
- **§5 — MF3 context-scoped GC.** `--label sandbox-context=<ctx>` stamped
  at create; List/GC scoped; other-context syncs never reaped; label-less
  legacy syncs keep exactly pre-MF3 reapability (never immortal; re-stamped
  only on fresh recreate — mutagen create is a no-op on existing names).
  Full selection matrix pinned.
- **§5 — MF5 stalled-sync self-heal.** `syncProber` (the owning layer, not
  the dashboard) fires a 30s-debounced background `Manager.Reconcile`
  (ResumeAll+FlushAll, label-scoped, idempotent) on SyncStalled — heals
  sync loss while SSE stays healthy. Create path deliberately does not
  resume (could un-pause a deliberately suspended session).
- **§5 — SF1 CLI half.** Bounded best-effort `startupSyncGC` on
  bare-`sandbox` + `attach`; dev-reset/kind-down run `sandbox sync gc`
  before deleting pods. Remaining: dashboard-Init half + create commands.
- **§10 — runner-api.md/README/checklist docs.** healthz body (+ why it's
  tokenless), all three 409 paths verbatim from turnRejectReason, interrupt
  empty-segment fallback + opencode direct-abort-200; README auth/sync-gc/
  opencode command rows; LAUNCH-CHECKLIST corrected; HARDENING-BACKLOG
  given a provenance-not-backlog header (agent verified zero true overlaps
  with TODO.md by topic grep).
- **§10 — e2e fake-runner faithfulness ([F4] residual).** Fake now mirrors
  server.ts: auth-before-route 401 shape, full 409 set, 400 invalid-JSON/
  missing-prompt, 1 MiB → 413 body, after= validation + B5 clamp, JSON 404s,
  loud `{"error":…,"fake":"e2e"}` 501s for unmodeled routes.
  TestE2EFakeRunnerFaithfulness = 16-assertion route/status/body table.
  Not modeled (documented): seq-0 persist-failure bypass, delta compaction,
  heartbeats.

## 2026-07-12 — batch 4: §2c renderer HIGHs, Session.Shell, sandbox doctor (f110dc3..ef0d6bf)

/loop batch 4. `just check` green (one inline dead-code lint cleanup:
fmtTokenLimit + styleSLLabel orphaned by the statusline collapse).

- **§2c — the three HIGH renderer items (deliberate redesign; goldens
  regenerated).** (1) Gutter bars gone: assistant `⏺ ` + 2-space hanging
  indent (trimLeadingBlankLines guards glamour's doc-margin blank), user
  dim `> ` non-bold; tool/subagent cards own their `⏺` head at column 0;
  streaming tail keeps T1 parity. (2) Working band above the composer
  (new liveLayout region): `✳ Thinking…|Writing…|Running <Tool>…
  (elapsed · ↓tokens · esc to interrupt)`, `esc to steer` when queued,
  `loading transcript…` while replaying; composer hint row no longer
  double-reports working state. (3) Statusline = ONE budgeted row
  (slSeg/budgetRow: required segments = model + mode chip, never shed —
  the ⚠ bypass chip survives any width, closing the §1c row-1 overflow
  residual; optional segments shed tail-first, ANSI-aware). Ctx gauge only
  ≥60% AND known limit — the 200k chat fallback removed (dashboard parity).
  Cost only ≥$0.10. Rate-limit row transient: 8s window after
  rate_limit.updated (rlUpdatedAt via nowFunc), fading its last 3s.
  redesign_2c_test.go pins grammar/indicator/statusline/ctx states.
  Known smalls: transient row can linger one frame on a fully idle TUI
  (no dedicated timer, by the no-interval-tick constraint); 8s/3s/$0.10
  are chosen values. Pre-existing flagged: composer hint still says `esc
  detach` during a turn (the working line now tells the truth beside it).
  SF1 TUI half: dashboard Init fires reconcileListCmd.
- **§8 — Session.Shell + SSHTarget.** `SSHTarget(ctx) (*SSHTarget, func(),
  error)` = SSH-only forward (inverse of DialRunner) + ensureSSHKey
  material for BYO-ssh consumers; `Shell(ctx, ShellOptions) (int, error)` =
  crypto/ssh dial, remote PTY, raw mode, SIGWINCH, remote exit code
  (transport → -1+err). InsecureIgnoreHostKey documented (matches mutagen
  posture; localhost forward, ephemeral pod key). CLI shell = thin wrapper;
  k8s-exec machinery deleted; resume-if-suspended stays CLI policy.
  Deliberate change: wrapper now StartWithProgress-es even when running
  (forward needs a ready pod). sdktest signature + struct pins. Live PTY
  path unverified (needs a pod).
- **§10 — sandbox doctor.** 10 checks, injectable deps: kubeconfig / API
  (5s bounded) / agents.x-k8s.io group / namespace can FAIL and
  short-circuit (one timeout total on dead network); mutagen (daemon
  start, 4s) / ssh / opencode / claude / credential store (0 accounts =
  INFO) / image refs are advisory-only (invariant test: advisory never
  FAILs). Exit 1 only on FAIL. Cluster-check PASS paths unverified live.
  Deliberate: builds its own clientcmd config (Backend prefers in-cluster
  config — wrong for a host tool; documented).
- **§5 residual** — startupSyncGC wired into runStartSession (claude +
  opencode create paths).

## 2026-07-12 — batch 5: the §8 De-Claude coordinated break (5a2ee59, solo whole-tree agent)

One review round-trip: the conscious wire break shipped with PROTOCOL_VERSION
still 1 — bumped to 2 at the schema source of truth + the mismatch advisory
made concretely actionable (both versions named, update-image + suspend/
resume or destroy/re-create remedy; kept advisory-not-fatal BY DESIGN for
self-built runner-image pairs — documented in schema + both function docs;
actionable wording pinned by test). Post-commit `just check` fully green
(the gen-drift gate requires the schema commit in HEAD first — known gotcha).

- **ApprovalPolicy**: `TurnInput.Mode string` → `TurnInput.ApprovalPolicy
  session.ApprovalPolicy` (Default/AcceptEdits/Plan/Bypass); wire key stays
  `mode` with the SDK strings, so the persisted autopilot spec and
  resolvePermissionMode need no migration.
- **Connection.External** (`ExternalCreds`) replaces Connection.Opencode;
  dashboard-internal OpencodeCreds deliberately kept (it IS the opencode
  attach pane; codex adds its own pane over the generic SDK shape).
- **AgentSessionID**: State json `agentSession`; SSE SessionStartedPayload
  `agentSessionId` (schema + just gen); index Entry migrates the legacy
  `claudeSessionId` key on Load and rewrites new on Save. Byproduct fix:
  `mergeEntry` had omitted the resume-id field — a partial re-seed could
  clobber a learned session id to "" (latent, now pinned).
- **[D9]**: one vocabulary — `State.Status` is k8s-owned lifecycle
  (`status,omitempty`; runner no longer reports it), new `State.Activity`
  (idle/busy/error) carries runner turn-activity. Status body:
  `status`→`activity`, `claudeSession`→`agentSession` (conscious break,
  protocol v2). Runner PVC session.json format untouched (no pod-side
  migration).
- 36+ files across internal/session, index, runner(Go+TS), cli, dashboard,
  client, sdktest (pins updated same change), schema, docs. Flagged for a
  future narrow change: D10 — `TurnInput.Resume` is typed TurnID but
  carries the backend agent-session-id (comment corrected, type left).
- Unverified: live pod round-trip of the renamed wire fields (needs a
  cluster); CI-only linters.

## 2026-07-13 — settingSources (§2b gap 8) + trace-seam wiring (§10) (627f1ee, 7efeeac)

- **§2b gap 8 — on-disk settings tiers load for SDK turns.** `buildOptions`
  was pinned to `settingSources: []` (SDK isolation mode), hiding the synced
  project's `.claude/` (slash commands, skills, subagents) + CLAUDE.md and
  the PVC-staged user config (CLAUDE_CONFIG_DIR) from every turn. Default
  now `['user','project','local']` (the SDK/CLI default);
  `SANDBOX_SETTING_SOURCES` overrides (comma list; `''`/`none` = isolation).
  `resolveSettingSources` pure + exported — narrows against the
  SettingSource union (unknown tokens dropped, canonical order, deduped) —
  pinned by `runner/test/setting-sources.test.ts` incl. buildOptions
  pass-through. Title summarizer keeps `[]` deliberately (one-shot no-tools
  fork). A1 NOT reopened: settings-defined hooks run as children of the
  spawned claude binary and inherit the buildAgentEnv strip — no capability
  beyond bypass-mode Bash; SECURITY.md gained the posture note (+
  makePreToolUseBashHook pointer re-anchored). Live in-pod verify (a synced
  command actually firing in a turn) still wanted.
- **§10 — connect↔turn id bridge.** `runner.Client.SetTraceID` (set from
  `Session.Connect`'s tracer, `""` → no header) stamps `X-Sandbox-Trace-Id`
  on runner requests; POST /turns logs `trace: <flowId> turn.link
  turn=<turnId>` so one grep in merged CLI+pod logs pivots between connect
  spans and turn spans. Header treated as untrusted log input:
  `traceIDFromHeader` accepts only `[\w.-]{1,64}`, else the link no-ops.
  Pins: StartTurn header presence/absence, link/validator envelopes,
  tracer.traceID nil-safety. runner-api.md documents the header.
- **§10 — runner boot spans.** `startBootTrace` through `index.ts` main():
  event_log / session_state / registry / boot_prep / listen + total, keyed
  `boot`; `startServer` gained an optional `onListening` callback so the
  listen phase closes at socket-accept (boot.total = start → ready-to-serve).
  Fake-clock envelope tests.
- **Durable-doc catch-up:** `docs/architecture.md` gained the Observability
  section (SANDBOX_TRACE spans + the bridge — closes that half of the §10
  doc-drift note; §1d observer-cap half still open). Also: flox
  manifest.lock regenerated post-`go get .`-hook removal (af2cd2e); TODO §3
  parenthetical updated (SDK turns now read settings; programmatic
  guard/audit hooks remain SDK-turn-only).

## 2026-07-13 — §2b gap 1: subagent output flattening (131738c)

- **The bug (the parity program's one labeled correctness bug):** the SDK's
  `parent_tool_use_id` reached the mapper but was stamped only on `tool.*`
  events — a running Task's message.started RESET the main `assistantBuf`
  mid-reply and its text deltas interleaved into the main streaming reply;
  parented reasoning.completed could flush/commit the main think block.
- **Schema:** `MessagePayload.parentToolUseId` (optional) + `just gen`; the
  doc now also records that reasoning.* reuses MessagePayload. Additive
  optional field ⇒ wire-compatible both directions, protocol stays v2 (an
  old counterpart keeps today's flattening, no NEW misbehavior).
- **Mapper (`mapping.ts`):** the id rides every message.*/reasoning.* emit —
  full-message text/thinking blocks, stream content_block_start,
  text/thinking deltas, and the parented user string path (Task prompt
  injection). Bare-key style (JSON.stringify drops undefined) matching the
  existing tool.* emits.
- **Reducer:** parented message.* → `applySubagentMessage` (subagent.go):
  started resets the card's narration buffer, deltas accumulate (8KB cap,
  mid-rune-safe tail trim, Bump-only per delta mirroring the E1 tool.delta
  path), completed pins the final text; user-role echoes + unknown parent
  ids dropped. Parented reasoning.* dropped outright (never touches the
  main tail). Card renders one live italic narration line under the child
  tree, width-budgeted with backstop truncate; collapsed cards unchanged.
- **Headless:** `sandbox turn` no longer prints a parented
  message.completed into stdout as the main reply (`internal/cli/turn.go`).
- **Docs:** runner-api.md Event Types gains the subagent-attribution
  contract paragraph.
- Pins: 3 mapper tests (parented full message / stream deltas / user echo;
  main-thread stays bare), 5 reducer tests (corruption oracle,
  reasoning-tail isolation, live narration render + width, user-echo drop,
  unknown-parent drop). Follow-up (still open under §2b): per-agent FULL
  transcripts — narration is one live line by design; subagent thinking is
  dropped presentation-side but retained in the event log. Live pod verify
  wanted at next natural Task fan-out.

## 2026-07-13 — §2c numbered permission panel + §2b gap 2 session grants (3687bd3)

- **The gap:** the runner has implemented `scope:'session'` tool-name grants
  (grants.ts) + editedInput since they landed, but the TUI hardcoded
  `Scope:"once"` and offered only [a]/[d] — "always allow" was built and
  unreachable.
- **permprompt.go** (the §2a component's base): per-tool CC-signature
  question ("Do you want to run this command?" / "…make this edit?" /
  fallback) + ❯-selected numbered options — 1. Yes / 2. Yes, allow <tool>
  for the rest of this session / 3. No. `permPromptKey` = pure key grammar
  (↑/↓ clamped nav ungated; digits direct-select+resolve; ↵ confirms; a/d
  hidden accelerators = allow-once/deny; j/k left to transcript scroll),
  table-tested.
- **Session scope wired:** option 2 → `Scope:"session"`; the scrollback
  line names the grant's REAL breadth ("approved · Bash allowed for this
  session" — tool-level, never the exact argument, matching grants.ts
  semantics). `resolvePermission(allow, scope)`; plan card + perm queue stay
  allow-once by design.
- **Key grammar change:** ↵ confirms the selection now, so the diff reveal
  moved to ctrl+o (the tool-card expansion idiom), advertised only when
  diffLines exist. Grace gate (quiet-window + cap) covers every resolving
  key; pinned under a frozen clock.
- Goldens: the two permission-box goldens regenerated deliberately; no other
  golden moved. Height math safe (liveLayout measures the built box).
- Residuals kept open in §2b item 2: editedInput still never sent; SDK
  canUseTool suggestions (3rd arg) still dropped. §2a component item stays
  open for the full 4-place consolidation (plan variant + permqueue reuse).

## 2026-07-15 — §2a input contexts + binding tables; §2d q/g truthful footer + external-pane leader chord

- **The refactor (§2a):** key dispatch in all three layers is now
  context-resolved tables instead of ~180 lines of string-compare if-chains.
  `inputctx.go`: generic `boundAction[M]` (key.Binding + when-gate + run
  returning handled + footerRank) and `dispatchKey` — precedence is table
  order, not code order. Contexts are DERIVED from existing state, never
  stored (`Model.activeContext()`: confirm→help→switcher→permQueue→filter→
  rename→convert→list; `TranscriptModel.activeSubContext()`: search→
  permission→palette→normal→compose). The transcript is deliberately NOT a
  strict stack: its globals (`?`-on-empty, esc, ctrl+], space-collapse,
  shift+tab, ctrl+f) run before the sub-context, preserving e.g. shift+tab
  cycling the mode while a permission is pending. Overlay internals
  (switcherKey/permQueueKey/filter/rename/convert/search/palette/normalKey/
  permissionKey) stay as delegate fallbacks — contexts were the win, not
  exploding working components.
- **Esc cascade single-encoded:** `escStep` list in modes.go (search →
  palette → steer → interrupt → driver → vim-insert); `escapeConsumes()` =
  showHelp || any applies; the esc handler runs the first applicable step.
  This closed a REAL divergence: a queued prompt with no active turn was
  invisible to escapeConsumes and patched around at the App layer — the
  steer step now covers it (the App's queuedPrompt guard remains only for
  the ctrl+] leg, which steers rather than detaches). Pins: cascade order by
  name, first-applicable-only (search open + turn active ⇒ no interrupt),
  escapeConsumes⇔cascade equivalence table, steer-after-turn-ended.
- **Truthful footer (§2d q/g):** `KeyMap.ShortHelp()` deleted; the footer
  renders from the SAME dctxList table that dispatches
  (`Model.shortHelp()` → `footerBindings`, rank-ordered Up/Down/Attach/
  Filter/New/Help/{q-perm-queue|Quit} — the rank-7 slot flips on
  `permQueueItems()` with complementary when-gates, so advertising can't
  lie). `?` overlay: GroupToggle now reads "group view · gg top"; new
  PermQueue row. Goldens byte-identical (fixture queue is empty).
- **External-pane ctrl+] leader chord (§2d, decided 2026-07-07):** pure
  `leaderStep(armed,key)` classifier (leader.go, table-tested) + gen-guarded
  500ms `tea.Tick` timeout in the App. ctrl+]/ctrl+4 arms (swallowed, never
  forwarded); double-tap or timeout = detach (pane minimized, child kept);
  `g`/`k` = jump next/prev session needing attention (minimize + attachMsg,
  mirroring transcript ctrl+g minus its park/SSE work); any other key
  disarms + forwards. `jumpToPrevNeedingAttention` added via a
  direction-parameterized `jumpNeedingAttention(dir)` core (§1b row-model
  comments intact). UX break accepted per the decision: a lone ctrl+] now
  detaches after 500ms, not instantly — the real-PTY test
  (`TestAppExternalPaneEscIsForwardedNotDetached`) was updated to pin the
  new contract (arm-then-detach), the one deliberate test change of the
  batch.
- Verified: `just check` fully green (incl. the real-opencode PTY test,
  race-twice, e2e); goldens untouched. NOT yet live-verified: the leader
  chord + attention jumps inside a real cluster `opencode attach` pane
  (maintainer eyeball at next natural use).
- STILL OPEN (§2a): App.Update flat dispatch; permissionPrompt 4-place
  consolidation; the deferred clock-injection sweep items.

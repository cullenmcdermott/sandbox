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

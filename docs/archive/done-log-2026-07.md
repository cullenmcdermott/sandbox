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

## 2026-07-15 — §2a App.Update flat dispatch + §2c thinking render (closes §2b gap 3)

- **App.Update flat dispatch (§2a, the last structural enabler):** pure
  refactor of app.go — Update is now a ~158-line flat router (type-switch of
  one-line dispatches to named `handle*` methods → attachedID mirror →
  `delegateDashboard` → OSC/sync mirrors → per-screen switch). The 4
  duplicated detach sites (detachMsg, transcript detach keys, ctrl+g jump,
  ctrl+k switcher) share one `detachTranscript()` (park B3 → SSE restore B2
  → release §1d C1; callers keep their own screen flips). The recursive
  `a.Update(*msg.ready/failed)` re-entry in connectUpdateMsg replaced by
  direct `handleAttachReady/Failed` calls — verified equivalent because both
  original case arms returned before the delegation preamble. B17 is now
  structural: `delegateDashboard` is the only `a.dashboard.Update` call site
  (grep-pinned: 1 occurrence, 0 `a.Update(` self-calls). Per-screen tails
  extracted (`updateTranscriptScreen`/`updateExternalScreen`). Zero behavior
  change; no test or golden modified.
- **Thinking render (§2c; closes §2b gap 3):** the gap-3 live-streaming half
  had ALREADY landed with the §1a/§2a cluster commits (reducer streams
  reasoning.delta into a live "∴ Thinking" tail — the TODO entry was stale
  doc drift). The remaining half — a committed multi-line think collapsed to
  a one-line summary, full text unrecoverable — closed now: committed render
  = `∴ Thought` label + italic TextMuted body wrapped at assistantWrapWidth,
  capped at `reasoningCapLines` (6) with a dim `… +N lines (ctrl+o)` trailer;
  expanded shows all. Live tail capped to the same 6-line window
  (tail-following, `… +N earlier lines` marker; slices wrapLiveReasoning's
  OUTPUT — the E6 prefix cache untouched). ctrl+o generalized:
  `toggleLatestToolCard` → `toggleLatestExpandable` (backward walk hits tool
  cards AND capped thinks; the wrap-aware `reasoningExpandable` gate shares
  `reasoningWrappedLines` with the render so gate and trailer can't
  disagree; H7 no-silent-strand semantics preserved). Single-line thinks
  render unchanged; width parity live/committed verified (both
  assistantWrapWidth + italic TextMuted). Goldens unchanged — none carry a
  multi-line think; 6 new render/toggle tests + live-cap test.
- Verified: `just check` fully green after both changes together.

## 2026-07-15 — §2c calm-chrome batch + §2d prompt history + first-account stage

- **De-bracket notices:** the 7 `appendBlock(blockInfo,"[…]")` sites are calm
  sentences now ("Stream ended", "Reconnected", "Auto-reconnecting…" — dim via
  blockInfo's existing style); interrupted/interrupt-failed render
  `⎿  Interrupted by user` / `⎿  Interrupt failed: <err>` in Coral through
  `appendElbowNotice` (pre-styled blockShell — blockInfo would restyle the
  Coral dim), place-indented so the elbow aligns with tool cards.
- **Entry gaps:** `blockCard.turnGap` → `entryGap`; one `startsEntry(prev,
  cur)` predicate (user/assistant/reasoning/subagent/todos always; toolCard
  only after a non-toolCard; info/error/shell/footer never) computed in
  commitItems AND by ensureStreamTail for the live assistant/reasoning tails
  — identical leading blank before/after commit, T1 pinned. Golden diffs:
  blank lines + padding rebalance only (attribution reviewed per-file).
- **Header dropped:** `headerBands()` nil normally; header+divider return
  only while reconnecting/session-gone (renderHeader simplified to the alert
  content). bodyTop/scrollbar-hit-test/preview all follow via the §2a band
  walk; preview keeps its own banner band. Title moved to the terminal tab:
  `App.View` sets `tea.View.WindowTitle` (`windowTitle()`: transcript title /
  external DisplayTitle / "sandbox"), bubbletea v2.0.7 diffs + emits the OSC.
- **Todo pinned widget:** `EventTodoUpdated` mutates ONE `blockTodos` card
  (payload on the model — blockCard was owned by a concurrent landing);
  render = ✓ strikethrough dim-green (Success+Faint) / ▸ bright (ActiveForm
  preferred) / ○ dim, header dropped, empty list → dim "todos cleared".
  Append-only-blocks invariant documented at the pointer.
- **Transient scrollbar:** thumb only when `offset < total-h`; at-bottom
  routes through the existing blank-gutter branch (width math untouched).
  Pill (`↓ new output · G bottom`) deferred — needs overlay machinery.
- **Prompt history (§2d):** ↑/↓ compose-table entries gated on
  empty-or-navigating (drafts keep scrolling); histPrev saves the draft and
  walks newest→oldest with clamp; ↓ past newest restores the draft and exits;
  editing a recalled entry exits nav (post-fallthrough value-vs-histShown
  check); recorded ONLY in `submit()` (covers queued steers; driver ticks +
  initialPrompt reach submitText directly and are excluded); consecutive
  dedupe. Session-local; parked-state survival is a documented residual.
- **First-account stage (§2d, decided 2026-07-07):** zero-account stores no
  longer skip the picker — the stage shows "cluster default" (byte-identical
  `beginCreate(CreateParams{claude-sdk})` to the old skip, pinned by test) +
  "＋ add account" (existing add flow reused); §6 reauth signpost comment at
  the row-set decision point.
- Verified: `just check` fully green on the combined batch. 15 new tests
  across calm_notices/transient_scrollbar/transcript_entrygap/
  prompt_history/account_picker; deliberate test updates limited to the
  behavior that changed (blockfp memoization, parity oracles, region rows,
  repurposed header test).

## 2026-07-15 — permPrompt consolidation, tool.progress pipeline, dogfood-fix wave, /model picker, arrow-key history

- **§2a permissionPrompt consolidation (closes §2a entirely):** `permPrompt`
  component (permprompt.go) — Render/Height/HandleKey for BOTH the tool panel
  and plan card; static bodies cached (keyed pending+width+sel+showDiff+theme
  epoch), border/appear-fade assembled live — one refresh discipline replaces
  the plan-stale/tool-double-build permBox asymmetry. Plan r/a/enter grammar
  joined the tool grammar; grace gate + resolvePermission stay model-side;
  perm queue shares the wants-summary vocabulary. Zero golden drift.
- **§2b gap 5 pipeline half:** `tool.progress` schema event (+ optional
  `ToolPayload.elapsedSeconds`, protocol stays v2), mapper emit from SDK
  tool_progress heartbeats (task_id noted for future background-task
  correlation via SDKToolUseSummaryMessage), heartbeats join the E4
  delta-compaction class, runner-api.md documented. STILL OPEN: the TUI
  render half (elapsed on running tool cards).
- **Live-dogfood fixes (maintainer screenshot):** user blocks now wrap at
  assistantWrapWidth (were unwrapped + clipped); `inputRows()` counts
  soft-wrapped visual rows (bubbles LineCount is logical-only) so the
  composer grows as text wraps; ctrl+o = expand-only with ctrl+e taking
  $EDITOR composition (the dual role was the trap: hints said expand,
  drafts got $EDITOR); `tea.PasteMsg` routed by input context (composer with
  full post-edit hooks / search append / dropped while permission pending —
  was silently dropped everywhere).
- **CC-style /model picker (maintainer ask):** `/model` opens a numbered
  selector (Default row + account models from models.available; static
  fallback Fable 5/Opus 4.8/Sonnet 5/Haiku 4.5 until it arrives — Fable
  reachable pre-first-turn); per-model slash commands, the /opus /sonnet
  /haiku trio, /model-default, and modelSlug all removed; full-capture
  preempt like help; escapeConsumes covers it; selection runs setModelCmd.
- **Composer arrows own history (maintainer directive, supersedes the
  same-day gate):** compose-context ↑/↓ never scroll — ↑ recalls on the
  first line (draft saved) / moves the cursor above it; ↓ walks newer /
  restores draft / moves the cursor; wheel/PgUp/PgDn/ctrl+u+d/vim NORMAL
  keep scrolling.
- **Ops/infra:** `sandbox --version` now embeds dev-<revCount>-<shortRev>
  (a stale flake.lock cost a rebuild-debugging session — the system-config
  lock pinned b68df9d while worktrees landed 45 commits later); worktree
  agents provision from a stale base — caught by an agent refusing to edit,
  recovered via fast-forward merge (watch for this in future fan-outs).
- Verified: `just check` fully green on the integrated tree (one
  staticcheck nit caught by the CI-parity lint and fixed).

## 2026-07-18 — §2b gap 6: citations + server-tool results (with audit V6/V24/V25/V29 folded in)

- **Schema/pipeline:** `Citation` object (`url`/`title`/`citedText`) +
  `MessagePayload.citations` (message.completed only, additive — protocol
  stays v2); `mapCitations` flattens the SDK's five citation location shapes
  (dedup by url+title with NUL-separated key, renderless entries dropped,
  `cited_text` capped at 200 chars with surrogate-pair-safe truncation).
- **Server tools:** `server_tool_use` (web_search/web_fetch only, allowlisted
  via one shared `mappedToolBlock` predicate across all three
  registration/emit sites) maps to normal `tool.started`;
  `web_search_tool_result` → formatted result list / `tool.failed` on the
  error shape; `web_fetch_tool_result` → fetched-URL line / `tool.failed`.
  Unknown result shapes map to a degraded `tool.completed` — total mapping,
  no orphaned "running" card, never a thrown turn. Unmapped server tools
  (code_execution family) stay dropped, including their stream-index slots.
- **TUI render:** citations pinned on the assistant block (append path
  pre-syncItems so the list cache sees them; droppedPartialIdx replay path
  assigns + Bumps) and render as a dim numbered "Sources:" footnote inside
  the body (hanging-indent covered, truncated at wrap width). **[V6]**
  title/url sanitized web-controlled input (`sanitizeCitationField`: all-ANSI
  strip + whitespace collapse + C0 strip — the H4 class on a new surface).
- **[V24]** headless `sandbox turn` prints a plain-text Sources list under
  the reply (mirrors renderCitations selection). **[V29]** (same file)
  `turn.interrupted` is now a terminal event for `sandbox turn` — distinct
  error instead of blocking until --timeout. **[V25]** replay-path citations
  pinned by test (TestCitationsSurviveDroppedPartialReplay).
- 16 new runner mapping tests + 7 Go TUI/render tests; runner-api.md
  documents the citations array + server-tool mapping contract.

## 2026-07-18 — audit burndown wave 1: 27 findings fixed across five batches (3e67728, d77905f, d47d3b7, ceb573d, 43a15e2)

Provenance: [`docs/audit-2026-07-18.md`](../audit-2026-07-18.md) (per-finding
verdicts stamped there; every fix carries a `[Vn]` comment at the site).
Five builder batches, every diff orchestrator-reviewed, full Go suite +
342-test runner suite green per batch.

- **runner claude/core (3e67728):** [V4] agentSessionId emit-key drift (live
  laptop-resume was silently dead), [V15] AskUserQuestion de-listed,
  [V16] subagent usage no longer clobbers ctx%, [V17] camelCase secret
  redaction, [V18] SIGTERM shutdown emits turn.interrupted + boot
  hasTurnTerminal double-emit guard, [V38] undeclared delta:true dropped,
  [V39] token budget counts terminal usage rows only (was ~2× counting),
  [V40] agent.ts abort-ownership contract, [V41] turn-counter reseed from
  the log, [V42] registerTurn-throw slot wedge.
- **runner opencode (d77905f):** [V19] no turn.failed after
  turn.interrupted, [V20] verify-exhaustion fails the turn instead of
  abandoning the persisted session, [V21] warmup/first-turn create
  coalesced (shared in-flight promise), [V43] phantom-cycle guard
  (runner-consumed message ids), [V44] sessionID-less session.error strict
  match, [V45] model latch is per-value (in-TUI model switch propagates).
- **tui-state (d47d3b7):** [V5] detach carries the transcript read-model to
  the row (lost-permission/stuck-Busy class), [V22] /clear todo re-pin,
  [V23] replay-gated queued-prompt flush + boundary release, [V46] applySeed
  attachedID guard, [V47] catchingUp release on watch-fail + cap-decline.
- **go-client (ceb573d):** [V1] worktree reap ownership gate (cross-
  namespace/cluster live sessions safe; index-less dirs skipped),
  [V8] public event vocabulary completed + sdktest completeness pin,
  [V9] Connect/Close generation guard (-race pinned), [V31]/[V36] doc
  corrections, [V37] schema gate fails on untagged exported fields.
- **go-sync (43a15e2):** [V2] safety-halt auto-heal (data-loss class)
  split out of the MF5 heal, [V3] kube-context label sanitization (sync was
  entirely broken for kubeadm/EKS context names), [V7] index adapter
  lost-update fix (partial saves + locked Index.Update), [V12]
  RemoveLocalState("") state-root wipe guard, [V13] sync --terminate SSH
  alias path drift, [V14] honest Paused classification + heal, [V28]
  namespace-scoped sync GC, [V35] paused-orphan reap (CLI half; dashboard
  half deferred — TODO §5).

## 2026-07-18 — audit burndown wave 2: k8s/cli batch (442b04b) — audit fully burned down

- **k8s/cli (442b04b):** [V10] reaper RBAC docs corrected (get,update
  sandboxes / list pods / get secrets) + executable `k8s/reaper-rbac.yaml`,
  [V11] sandboxToState workspace/project split (watch-inserted worktree
  sessions no longer mis-titled / mis-synced) + mergeClusterState carry,
  [V26] claude/opencode join positionals, [V27] destroy confirm reads a
  line (bare Enter denies), [V30] read-side validateID (Load/
  LoadCachedEvents/DeleteEventCache), [V32] README image-default drift
  (ghcr.io), [V33] same-shape account-swap rotation warning + logout
  caveat, [V34] port-forward backoff resets after an established attempt.
- **Audit disposition:** all 47 findings resolved same-day (46 fixed, V35
  dashboard half + V15 answer flow as TODO residuals, 0 refuted); verdicts
  stamped per-finding in `docs/audit-2026-07-18.md`. Uncovered: the 6
  spend-limit-killed auditor subsystems (tui-public, security, docs,
  tests-ci, tui-render, tui-input) — re-run is a maintainer call.

## 2026-07-18 — SDK-example review burndown (§1g + §8 batch)

- **§1g dashboard lifecycle parity (8e6311a):** suspend/resume/destroy
  keystrokes routed through `client` via a new `clientLifecycleBackend`
  adapter (internal/cli) — embeds `*k8s.Backend` for List/Watch, delegates
  lifecycle to `client.Suspend/Resume/Destroy`. Fixes silent worktree-WIP
  loss on TUI destroy (client.teardownWorktree was skipped) and missing
  sync pause/resume on TUI suspend/resume. Destroy-hook plumbing
  (RunOptions.DestroyHook/PreDestroyHook, With*DestroyHook,
  newLocalDestroyHook/newPreDestroySyncStop) removed;
  `dashboard.NewApp/Run/RunAttached` now take the `Backend` interface.
  destroy_order_test.go deleted (ordering pinned by the client F3
  call-order spy); C2 hook tests reworked to dispatch/report contract.
- **§8 OpencodeProvider re-exports (5720b70):**
  `OpencodeProviderAnthropic/OpenAI/Zen` aliased into `client` + sdktest
  pins; CreateOptions doc no longer names unimportable `session.*`
  spellings. Note the Zen wire value is `opencode-zen`.
- **§8 Example_chat (417c334):** compile-only example of the full chat
  loop: account selection, OnPhase/Warning, delta/tool/permission/usage
  event handling with ResolvePermission linked via PermissionID,
  interrupt steering, reattach via Open + Events(afterSeq) +
  EventStreamLive boundary, detach-vs-destroy teardown.
- **Infra-name scrub (4ada2b1):** private cluster name replaced with
  "my-cluster" across 7 files; 8 historical commits still carry it in
  diffs — history rewrite is a maintainer call (pre-OSS).

## 2026-07-18 — SDK capability-gap wave 2 (watch + models)

- **Cluster watch (b97d205):** `StateEvent{State, Deleted}` moved to
  `internal/session` (k8s keeps a type alias); public `client.StateEvent`
  alias + `Watch` on the `client.Backend` interface + delegating
  `Client.Watch`. Dashboard switched to `session.StateEvent`, dropping
  the `internal/k8s` import from four files. sdktest pins the Client
  method, the interface method (method-expression pin), and the field
  shape.
- **client/models (74f12e6):** `internal/models` → `client/models`
  (history-preserving mv; surface unchanged: `Limit(modelID) Info`,
  `Info{ContextLimit, InputPrice, OutputPrice}`), new doc.go in the
  client/cred voice, dashboard import swaps, sdktest pins. CLAUDE.md +
  docs/architecture.md package tables updated in the same commit.

## 2026-07-21 — six-agent parallel fan-out batch (disjoint TODO items, all same-day)

Six worktree-isolated agents dispatched in parallel on deliberately
non-overlapping items; every diff reviewed and re-verified on main before
cherry-pick; one `just check` over the integrated batch (all gates green).

- **§1f [S2] sync credential-filename ignores (e674325):** 12 exact-name
  patterns appended to the non-overridable `securityIgnores` layer in
  `internal/sync/sync.go`, three commented groups — plaintext machine
  logins (`.netrc`, `_netrc`, `.npmrc`, `.git-credentials`), cloud creds
  (`.aws` whole-dir, `service-account*.json`), SSH private keys
  (`id_rsa|id_ed25519|id_ecdsa` + `.*` derivatives). `.aws` written
  without trailing slash to match the existing dir-entry style (broader:
  also blocks a file of that name). `TestCreateProjectSyncIgnoreLayering`
  pins each pattern present + positioned after the gitignore layer (no
  negation can re-enable); README Mutagen bullet notes the defensive
  exclusion.
- **§2e needs-input relabel (f3154c9):** display strings only — row label
  "needs input"→"ready", attention summaries "%d ready"/"%d ready below",
  detail note "ready for your next prompt". Wire string "needs-input",
  `StatusNeedsInput`, and `GlyphNeedsInput` (already a calm ❯) untouched;
  StatusWaiting keeps label + attention float. Goldens unchanged (fixtures
  never render NeedsInput — verified pre-change goldens contain no "needs"
  text); TestAttentionSummary/TestOverflowSummary pin the new strings.
- **§6 C3-codex shape guard (9199e71):** `anthropicEnvShape` renamed
  `credentialEnvShape`, detection widened to both families
  (CLAUDE_CODE_OAUTH_TOKEN/ANTHROPIC_API_KEY;
  CODEX_AUTH_JSON/OPENAI_API_KEY — first match wins, one backend per
  session so at most one family in a pod template; opencode exempt as
  before). Both C3 call sites (Secret-AlreadyExists goroutine + Sandbox-
  AlreadyExists path) now reject codex shape changes before any Secret
  mutation — closes the resume brick where `syncSessionCredential`
  stripped `codex-auth-json` under a baked NOT-Optional SecretKeyRef.
  `TestCreateSessionStripsCodexCredentialOnRecreate` flipped to
  `TestCreateSessionRejectsCodexAuthShapeChange` (error + Secret/Sandbox
  fully intact); new `TestCreateSessionSameShapeCodexAccountSwapPatchesSecret`.
- **§10 [O4][O6][O11][O12] docs (d71c64f):** `sandbox doctor` first in
  Quickstart + Commands with the two-doctors disambiguation (vs `just
  doctor` dev-env tool); experimental `sandbox codex` row documenting the
  real credential contract (per-session ChatGPT-OAuth auth.json is
  SDK-only; the CLI always uses the shared `openai-api-key` fallback —
  verified zero CodexAccountID/CodexAuthJSON hits in internal/cli|tui) +
  degraded-attach caveat; CONTRIBUTING "The `openspec/` references"
  section (untracked via `.git/info/exclude`, absent from clones by
  design, durable outcomes land in docs/); gate described identically in
  README Testing + CONTRIBUTING, matching the Justfile `check` recipe and
  naming CI's `flox activate -- just check`; recipe list gained
  sdk-conformance/verify/e2e, `just build` description corrected.
- **§4 [P4] pane input writer (8f51e83):** input-writer goroutine in
  `external_pane.go` is the sole UI-side transport writer — 64-entry
  tagged queue (`paneInput{data, size}`) carries keys/paste/mouse AND
  resize in UI order (resize routed through the queue: PaneStream.Resize
  shares writeMu, and geometry must not overtake type-ahead); enqueue is
  select-with-default, drop-on-full records a first-wins pane error on
  the existing `p.err` surface; writer holds a local transport ref, exits
  via the P5 done channel, close()'s transport.Close() unblocks a parked
  Write (no leak, channel never closed). Emulator reply pump deliberately
  keeps direct blocking writes (capability replies must never drop;
  already off the UI loop). Direct-construction test panes (`p.in == nil`)
  fall back to synchronous writes, preserving existing seams. New tests:
  stalled-transport keystroke + ctrl+] detach promptness, cross-type
  ordering, deterministic 65th-write drop; `-race -count=2` green.
- **§1h [L8a/b] stuck-"working" fix (67d6831):** UserPromptSubmit's busy
  is provisional — `armBusyConfirm` (default 10s,
  `SANDBOX_BUSY_CONFIRM_WINDOW_MS` env override, deps-injectable) reverts
  to idle through the registry `setStatus` path (the standard
  `session.status_changed` emission) unless
  MessageDisplay/PreToolUse/PostToolUse/PermissionRequest confirms;
  activity landing after the window re-asserts busy; revert is
  status-only (synthetic turn stays open for a late Stop / next-prompt
  interrupt); timer unref'd + cleared in closeTurn (covers shutdown
  reset). Stale synthetic busy now RELEASED for real in `recomputeIdle`
  — setStatus('idle') persists + emits regardless of attachment; reaper
  eligibility unchanged (idleSince still isDetached-gated on the
  recursive pass; real runner turns exempt — `syntheticBusyStale` returns
  false when activeTurns > 0). 5 new confirm-window tests (20ms injected
  window) + staleness tests upgraded to a real temp sqlite log asserting
  exactly one emitted idle status event, attached and detached. Part (c)
  (Esc-interrupt hookprobe) + the dashboard "stalled?" rendering remain
  open (§1h residual).

Live-verify wanted (next natural session): [L8] — a slash command should
show "working" for ≤~10s then flip to "ready"; [P4] — typing during a
network stall must not freeze the dashboard; ctrl+] must detach.

## 2026-07-21 — [L3] detached feed history + EventCache deletion (inline, 17735d2 + 79123e7)

- **Feed seeds from a one-shot from-zero replay (17735d2):** new
  `internal/tui/dashboard/feed_history.go` — on feed open the App runs a
  ONE-SHOT passive SSE fetch from seq 0 (mirrors startLiveSSECmd's
  attach-gate yield + connect-slot throttle + §1d C1 close-the-forward
  contract), collecting until the client-internal `EventStreamLive`
  replay-complete marker, channel close, or a 15s read bound, keeping a
  2000-event tail (halve-when-doubled, O(n) overall). Scoped strictly to
  the fetch: the dashboard read-model stream keeps `after=lastSeq`, so
  the launch-time notification-flash / usage-double-count that guard
  exists for cannot return. Ordering hazard solved: the feed dedups by
  seq, so seeding AFTER live tap ingest would drop the whole history —
  while the fetch is in flight, tapped events buffer in
  `feedPendingLive` (cap 1024) and re-apply after the seed (replay-tail
  overlap dedups). Stale results (feed closed/reopened) dropped by a
  generation guard; fetch failure degrades to live-only with a calm
  notice; partial replay seeds what arrived + an "incomplete" notice.
  Tests: pure collect helper (complete/partial/deadline/tail-cap),
  seed-then-overlap dedup pin, App-level buffer→seed→flush, stale-gen/
  wrong-id/no-feed guards, error + incomplete notices;
  TestAppViewFeedNavigation updated to complete the history handshake.
- **EventCache surface deleted (79123e7, option A follow-up):** the
  host-side cache was a reader with no production writer since a935541 —
  removed `dashboard.EventCache`/`WithEventCache`/`RunOptions.EventCache`
  + the eventCache field, the cli `indexEventCache` adapter + its three
  wiring sites (root/commands/claude_remote), and the whole
  `internal/index/cache.go` (CacheWriter, AppendCachedEvent,
  LoadCachedEvents, DeleteEventCache, compactCacheTail) + tests
  (−454 lines). V30 traversal test keeps its Load coverage; legacy
  on-disk events.ndjson still goes with the session dir on destroy.
  `just check` green over both commits.

## 2026-07-21 — seven-agent Fable fan-out batch (6e1ba20..55bc0a2, all unsigned — 1Password agent locked)

Seven worktree-isolated Fable subagents in parallel, orchestrator-reviewed
and cherry-picked onto main one by one; `just check` green over the
integrated batch (one anti-cheat catch: the new projpath windows skip needed
its `// gate-ok:` annotation, amended into the T10 commit).

- **§1f [S1]+[S3] security docs (6e1ba20):**
  `k8s/networkpolicy-egress-fqdn.yaml.example` — Cilium toFQDNs allowlist.
  The agent re-verified the host set against Anthropic's CURRENT
  network-config doc: statsig.anthropic.com/sentry.io are gone from it
  (documented as legacy/verify-with-hubble; Datadog intake is the current
  optional telemetry), and `platform.claude.com` was ADDED because the
  in-pod claude refreshes its own materialized credential — blocking it
  breaks sessions at token expiry. Commented codex/opencode/registry
  blocks, mandatory DNS-proxy rule + tunneling caveat, replaces-broad-443
  header. SECURITY.md: exfil paragraph extending [A3] (must-read
  credential file + open 443 = credential that outlives the session,
  symbol-anchored) + the [S3] "observer events are agent-influenceable"
  threat-model section (same-session spoofing accepted + bounded;
  cross-session impossible). Host set NOT live-validated.
- **§1h [L7] + §4 [P3] pane wheel-scroll (e319645):** wheel with child
  mouse-tracking OFF scrolls a local `scrollOffset` view over the vt
  scrollback (3 lines/tick, clamp to `ScrollbackLen`, alt-screen ignored);
  `scrolledBody` stacks the scrollback tail over the live-screen top
  (width-clipped, style-reset-safe); gold "↑ N lines — any key to return"
  replaces the detach hint while scrolled; key/paste/new-output snap back
  to live BEFORE forwarding (P1 drain + P4 queue untouched); tracking
  children (opencode) still get SGR wheel. [P3]:
  `SetScrollbackSize(2000)`. Five behavior tests; race suite green.
- **§4 pane RTT probe (3060b53):** `internal/runner/pane_rtt.go` —
  SANDBOX_TRACE-gated (env read mirrors client/trace.go's unexported
  traceEnabled); 5s pinger goroutine (`WriteControl` ping, 8-byte
  big-endian UnixNano; safe beside the P4 writer per gorilla contract);
  pong handler samples into a 256-slot mutex ring; `PaneStream` gains
  done/closeOnce and additive `RTTStats()`; ONE stderr line on Close
  (`trace: <id> pane.rtt n= p50= p95= max=` — µs rounding; format reserved
  for the §10 SSE-latency probe). Node ws auto-pongs — zero runner
  changes. Pure percentile helper + ring/integration/leak tests
  (pingerDone channel, not goroutine counting); sdktest PaneStream pin
  intact.
- **§10 [O3] auth status/doctor host-login probe (5445481):** harvested
  the parked `agent-a0080936a970d42b6` start (kept the shape; replaced the
  untestable KeychainPresent seam with `func() (present, ok bool)`, wired
  the draft's dead Degraded field into Level()→yellow, wrote the missing
  tests + doctor half). Presence-only probe mirrors `cred.SystemMaterial`
  exactly: darwin keychain exit-code (no `-w`) is FINAL when `security`
  exists; credentials-file stat otherwise; `CLAUDE_CONFIG_DIR` exclusive.
  `auth status` leads with "host Claude Code login"; env vars demoted to
  headless/Degraded. doctor: new claudeLogin seam, host-login headline,
  "log in with `claude` on this machine (Max mode)" remedy, shared-Secret
  wording gone. Live doctor/auth-status runs verified.
- **§10 O-docs cluster (badb706 [O1,O8], 1e5b474 [O5,O7], 511b98f
  [O9,O10]):** harvested the parked `agent-aff3123c45acf636a` draft,
  re-verified every claim against current main. [O1] root --help/Long/
  package-doc pane-first. [O8] runner-api claude-sdk→claude-pane +
  extended retired-id note (selectAgent behavior). [O5]
  `k8s/reaper-namespace.yaml` (agent-reaper, restricted PSS — reaper pod
  satisfies it per buildReaperJob) + `k8s/networkpolicy-reaper-ingress.yaml`
  (default-deny DOES block reaper pod-IP :8787 polls; exception scoped to
  the sandbox-reaper label; port-forward tunnels unaffected) + apply order
  + "sessions never auto-suspend" sentence; session-lifecycle cluster
  boxes checked. [O7] k8s/README label/prompt/shared-Secret facts. [O9]
  architecture.md worktree hedges → shipped behavior; the flagged
  out-of-scope `client/sync.go` diff was KEPT (2-line comment fix of the
  same hedge, verified against createWorktree/ReapWorktrees). [O10]
  dev/local README pane-first (env-token flow relabeled legacy). Reaper
  manifests reasoned from source, not live-applied. Four new [O15]
  residues discovered and filed.
- **§9 T10 working-directory picker (55bc0a2):** directory stage FIRST in
  the create overlay (cwd row preselected — enter-enter preserved; ≤5
  recents; free-text row with ~-expansion + Tab completion: unique→`/`,
  ambiguous→LCP, hidden dirs on `.` partials); new
  `Index.RecentProjects(limit)` (distinct ProjectPaths,
  LastActivity-desc) → `indexRecentProjects` (cap 8) → injected
  `RunOptions.RecentProjects` (dashboard never imports internal/index);
  `CreateParams.ProjectPath` joins in the beginCreate funnel so every
  create path inherits it (deep pin through the account stage); CLI-side
  `creatorProjectPath` re-validates fail-closed; shared normalization
  extracted to `internal/projpath` (`resolveProjectPath` delegates,
  behavior-identical); rows validate on enter with inline formErr (deleted
  recents visible with an explanation). Backend-stage esc now walks back
  to the dir stage. CLI commands keep pure-cwd semantics.
- **§9 host statusline chaining (d6b348c):** provisioned statusline script
  chains, first hit wins: `pane-observer/user-statusline` (pod drop-in) →
  `../statusline/user-statusline` (host-synced) → `sandbox-user-statusline`
  on PATH (future flox bin); candidate gets stdin JSON verbatim + ~1s
  (`SANDBOX_STATUSLINE_TIMEOUT_MS`); ENOENT/EACCES falls through,
  ran-but-nonzero/timeout/empty → builtin; metrics POST initiated FIRST,
  exit still gated on it (spawnSync blocks the loop, so stdout always
  precedes the fetch's exit); hard-exit 3s→5s. ConfigInputsSubs gains
  `{statusline, statusline}` — a SIBLING of runner-owned `pane-observer/`
  chosen so host→remote sync can never conflict with the runner-minted
  token (pinned: no sync arg may target pane-observer/); counts 7→8
  corrected through client/session.go + sync.go comments. Runner tests
  execute the REAL provisioned script against a capture server (7 cases);
  architecture.md config-inputs list updated.

Live-verify wanted (next natural session): [L7] trackpad scroll feel +
snap-back; T10 overlay flow; a synced `~/.claude/statusline/user-statusline`
appearing in-pane; `sandbox doctor`/`auth status` on a machine without the
host login; [S1] FQDN set via `hubble observe`; [O5] reaper manifests on the
real cluster.

## 2026-07-21 — golden harness motion/theme/size axes (551cd2c)

Closed 3 of the 4 §10 visual-testing gaps (eyeball harness still open).
Builder-implemented, orchestrator-reviewed (verified the two load-bearing
claims independently: Midnight goldens are 100% renames; all four
mid-motion offset pairs are byte-distinct, not committed-identical), then
cherry-picked. Test-only — no production code.

- **withMotionRender:** motion ON (clears SANDBOX_REDUCE_MOTION AND NO_COLOR
  since anim.ReduceMotion reads both), `nowFunc = goldenFixedNow + offset`.
- **Mid-motion:** TestGoldenRowEnter {start_0ms, mid_90ms, settled_200ms}
  pins the row fade-in (beta title fg TextDim→mid-blend→TextBody across the
  180ms window); TestGoldenStatusFlash {peak_0ms, mid_150ms, settled_350ms}
  pins the status-change bg pulse (Page→0.4-toward-accent→faint→none across
  300ms). The goldens ARE the frames — a static way to review animation.
- **Theme axis:** TestGoldenDashboard/TestGoldenFeed fan out over the whole
  registry (Midnight/Daylight/Ember) as subtests via theme.Cycle/ByName/
  ApplyTheme with t.Cleanup restore; old un-suffixed goldens became the
  /Midnight subtests (pure rename, byte-identical). TestGoldenConfirmDialog
  left Midnight-only per scope.
- **Size axis:** TestGoldenDashboardNarrow/TestGoldenFeedNarrow at 60×20 —
  dashboard drops the side detail pane to one column, feed truncates paths;
  degrades cleanly, no panic.
- Verified `-run Golden -count=3` deterministic + full dashboard package
  green on main; the ExternalPane esc test self-skips in-sandbox as expected.

## 2026-07-21 — opencode-multi-provider-auth: host-harvested per-session seeding (f5a6eb0/93ce61f/4a7f631/683c13a/521839e/ed37667)

Executed the whole openspec change (tasks 1–6) across a six-agent
tech-lead/builder fan-out; each builder diff was orchestrator-reviewed
against source and independently re-verified before cherry-pick; `just
check` green over the integrated spine; `openspec validate` green. Replaces
the shared-Secret-only opencode credential path (the live
`CreateContainerConfigError` on omni-prod, where the cluster Secret held
only the Zen key) with per-session seeding from the host's own opencode
login, mirroring the codex `codex-auth-json` transport.

- **Groundwork (1.1/1.2, orchestrator):** pinned auth.json schema against
  the live store (values-redacted) + sst/opencode auth/index.ts @v1.17.7 —
  entries are a discriminated union (api/oauth/wellknown) with NO
  last_refresh field, so the merge fingerprints each entry with sha256; and
  `OPENCODE_AUTH_CONTENT` shadows the file in opencode's `Auth.all()`, so the
  runner must scrub it. Recorded in the change's design.md.
- **Client harvest (2.x, f5a6eb0):** `client/opencodeauth.go`
  `HarvestOpencodeAuth` (XDG-aware path, JSON-object validation, value-free
  Entries index, opaque JSON bytes; leak-guard test) + `Filter`;
  `CreateOptions.OpencodeAuthJSON` + `validateOpencodeSeed` fail-closed
  (`ErrOpencodeProviderNotSeeded`); Spec field; sdktest pins.
- **k8s transport (3.x, 93ce61f):** `secretKeyOpencodeAuthJSON`; `opencodeEnv`
  seeded (`OPENCODE_AUTH_JSON` from per-session Secret, not Optional, no
  provider key per D7) vs fallback (unchanged), `SANDBOX_OPENCODE_PROVIDER`
  on both via the new shared `session.OpencodeProviderEntryKey` (Zen entry
  key = "opencode"); `reconcileSecretCredential` label made optional +
  opencode reconcile line; the seeded↔fallback shape guard
  (`opencodeSeededShape`) in BOTH re-create branches — the architectural
  call: opencode had skipped C3, but the seed adds a second shape whose
  seeded→fallback flip would strip a NOT-Optional SecretKeyRef and brick the
  next resume. 6 backend tests.
- **Runner materialization (4.x, 683c13a):** shared `runner/src/agent-auth.ts`
  (`AuthFs`/`writeAuthFile0600` + opencode's `materializeOpencodeAuth`);
  per-entry refresh-preserving merge against a `auth.json.seed-hashes`
  sidecar (unchanged seed entry keeps disk — preserves a pod-side refresh;
  changed entry wins; disk-only preserved); `assertOpencodeAuthUsable`
  fail-closed gate; child env scrubs OPENCODE_AUTH_JSON +
  OPENCODE_AUTH_CONTENT. codex refactored to consume the shared helpers,
  behavior frozen (its tests ran unmodified). 18 runner tests; codex
  content-leak self-caught + fixed during the build.
- **CLI create-UX (5.x, 4a7f631 + 521839e convergence):** `resolveOpencodeSeed`
  — harvest→seed-all default, `--seed-providers` filter (security lever),
  `--provider`-in-seed-set validation, `opencode auth login` TTY passthrough
  + re-harvest-once for a missing provider, non-TTY/corrupt-store fail-closed
  (no silent shared-Secret fallback when a local store exists), no-store →
  fallback. Dashboard creator seeds all-local when a store exists (picker
  deferred). 12 hermetic tests. The local entry-key shim later converged onto
  `session.OpencodeProviderEntryKey`.
- **Docs (6.x, ed37667):** README credentials rewrite (primary seeded path,
  shared Secret reframed as CI/headless fallback), k8s/README rescope,
  SECURITY egress posture (seeded 0600 auth.json is agent-readable/
  exfiltratable; --seed-providers narrows the blast radius; FQDN egress
  cross-ref), backend-conformance "Auth seed (opencode)" contract,
  session-lifecycle provisioning step, + architecture.md per-session-Secret
  key-list fix (added opencode-auth-json AND the pre-existing codex-auth-json
  omission).

Deferred follow-ups filed in TODO §7a: dashboard opencode provider picker
(today defaults anthropic, no picker → a login lacking anthropic hits the
fail-closed seed gate at Create); gate `stampOpencodeCredsFreshness` to the
fallback path (seeded sessions can emit a spurious rotation warning);
multi-account per provider (design non-goal). LIVE VERIFY on omni-prod
(multi-provider seed session + fallback session) is maintainer-gated and
still pending (task 6.4).

## 2026-07-21 — §8 pod-bootstrap part B: generic env/secret injection (d6e55fa)

Builder-implemented, orchestrator-reviewed against source and verified
(`just check` all gates green). Part B of
docs/design-pod-bootstrap-and-tool-injection.md — the maintainer's
directly-requested slice (inject a GitLab/GitHub PAT / Jira key into a
session). Part A (BootstrapFiles) + full docs are tracked next steps.

- **Client (client.go/errors.go):** `CreateOptions.ExtraEnv map[string]string`
  (plain pod env) + `ExtraSecretEnv map[string][]byte` (json:"-",
  per-session-Secret-backed). `validateExtraEnv` fail-closed: env-name shape
  (`^[A-Za-z_][A-Za-z0-9_]*$`), the exported `k8s.IsReservedEnvName` denylist
  (SANDBOX_ prefix + RUNNER_TOKEN/PROJECT_PATH/HOME/PATH/credential vars),
  no cross-map duplicate, 512 KiB summed ExtraSecretEnv cap. Sentinels
  ErrInvalidExtraEnvName/ErrReservedEnvName/ErrDuplicateExtraEnv/
  ErrExtraSecretEnvTooLarge; sdktest pins.
- **k8s (backend.go):** exported `IsReservedEnvName`/`reservedEnvNames`
  beside buildEnv (must track the four env emitters); Secret keys
  `extra-secret-env-<NAME>`; `appendExtraEnv` on the common path — plain
  ExtraEnv vars + Optional SecretKeyRefs for ExtraSecretEnv + sorted
  `SANDBOX_EXTRA_ENV_NAMES`/`SANDBOX_EXTRA_SECRET_ENV_NAMES` markers;
  `reconcileExtraSecretEnv` in syncSessionCredential (patch changed, strip
  removed — no shape guard needed, refs are Optional).
- **Runner:** ExtraSecretEnv is AGENT-VISIBLE (not in RUNNER_SECRET_ENV_KEYS,
  so opencode/codex see it via sanitizedExecEnv passthrough; claude-pane's
  strict allowlist admits the marker-named vars). redact.ts masks its values
  in the event log/audit via the SANDBOX_EXTRA_SECRET_ENV_NAMES marker
  (rider a). Runner suite 268 pass.
- **Security posture:** SECURITY.md gained an ExtraSecretEnv paragraph — the
  injected secret is agent-readable/exfiltratable by design (that's the
  feature), redacted from logs, and opening FQDN egress for a tool's endpoint
  also opens its token's exfil path (operator tradeoff, stated plainly).

ARCHITECTURAL DECISION (flagged for maintainer, recorded in the design doc
Status block): ExtraSecretEnv is agent-visible rather than the draft's
strip-from-agent default — required for the PAT-for-git use case, and
consistent with rider (b)'s exfil framing. Revert path (strip-by-default) is
a one-line allowlist + RUNNER_SECRET_ENV_KEYS change, documented.

## 2026-07-22 — §8 pod-bootstrap part A: file injection on Create (e9dee22)

The file-injection half of pod bootstrap (design part A), reusing part B's
per-session-Secret plumbing plus the codex materialize hook. Additive SDK
surface; `just check` green.

- **Client:** `CreateOptions.BootstrapFiles []BootstrapFile{Path, Content,
  Mode}` (Content `json:"-"`, create-time-only). `validateBootstrapFiles` is
  fail-closed — Path absolute or `~/`-relative, `path.Clean`'d, must resolve
  STRICTLY below the pod HOME (`/root`) or `/session/state` (never the synced
  workspace, no `..` escape via the trailing-`/` prefix test); resolved paths
  unique; summed Content ≤ 256 KiB. Four exported sentinels
  (`ErrInvalidBootstrapPath` / `ErrBootstrapPathOutsideRoots` /
  `ErrDuplicateBootstrapPath` / `ErrBootstrapFilesTooLarge`); no error echoes
  Content.
- **k8s:** content rides the per-session Secret (`bootstrap-<n>` keys + a
  `bootstrap-manifest` JSON of `{path,mode}`), projected read-only + Optional as
  a Secret volume mapping keys→files (`manifest.json` / `<n>`) mounted at
  `/etc/sandbox-bootstrap`, with `SANDBOX_BOOTSTRAP_DIR` pointing at it.
  `reconcileBootstrapFiles` re-syncs the `bootstrap-*` keys on re-create (added-
  file bake caveat documented, mirrors ExtraSecretEnv).
- **Runner:** `materializeBootstrapFiles()` (`bootstrap.ts`) runs at boot BEFORE
  any agent (the shared materialize step), write-if-changed with a per-file
  seed-hash sidecar on the PVC — a restart keeps an agent's in-place edit unless
  the operator rotated the seed. Re-validates every manifest path against the
  roots (defense in depth); never logs content. Text content is the supported
  case; large/binary blobs are out of scope (documented, no URL-fetch variant).
- **sdktest:** pin the new `BootstrapFile` type, `BootstrapFiles` field, + the
  four sentinels.
- **Docs:** design doc Status flipped to parts-A+B-implemented; architecture.md
  gained a `bootstrap.ts` row + an operator-injection bullet; TODO §8 checks off
  part A. Rider (b) FQDN-egress example + the skills-sync README section also
  landed (see the docs commits). Residual: the operator-prose README section for
  the ExtraEnv/BootstrapFiles injection surface.

## 2026-07-25 — §10 [O15] retired-backend (`claude-sdk`) residue sweep

Burned down [O15] plus its 2026-07-21 batch additions. The headline is a
**deliberate behavior change**: `client.Create`'s empty-`Backend` default moved
from the retired `BackendClaudeSDK` to `BackendClaudePane`. The old default
provisioned a pod whose `selectAgent` returns null, so the session came up and
then 409'd on every turn — a silent dead end. claude-pane fails LOUDLY instead:
a caller with no credential material now gets `ErrClaudePaneCredentialMissing`
(which already carries "log in with `claude` on this machine" remediation from
[L1]) before any cluster call. Pre-OSS break, per §8's rules.

- **`client/client.go`:** the default flip + a `CreateOptions.Backend` doc that
  names the material requirement and the sentinel. No production caller relied
  on the old default — every CLI command and the dashboard picker set `Backend`
  explicitly (`internal/cli/claude_remote.go`, `backend_picker.go`).
- **`internal/cli/dashboard_connector.go`:** the Creator's own empty-Backend
  fallback moved to `BackendClaudePane` to match, with a comment noting the
  picker always sets it (so this is defense in depth for direct callers).
- **`internal/session/types.go`:** `BackendClaudeSDK` gained a RETIRED doc block
  in the `agent.ts:66` pattern — what a lingering pod still serves, and "never
  select it for a NEW session". `Spec.Backend`'s doc listed the retired id and
  omitted claude-pane (fixed); the `ID` example is now `claude-pane-7f3a`;
  `State.AgentSessionID` now describes the pane's `--session-id`/`--resume`
  pinning rather than "the Claude SDK session UUID"; `TurnInput.ApprovalPolicy`
  says plainly that it is effectively inert now that opencode is the only
  runner-turn backend.
- **`internal/tui/dashboard/zones.go`:** the cluster strip's known-backend order
  omitted `claude-pane` entirely (so pane sessions fell into the sorted
  `extras` tail) AND counted by raw backend id — since `ClientLabel`/
  `BackendMark` map claude-sdk and claude-pane to the same "claude", a fleet
  holding one of each rendered `claude 1 · claude 1`. Now aggregated by display
  label over an explicit known list, first id carrying a label as the
  representative.
- **`runner/src/session.ts`:** `loadConfig`'s local-dev fallbacks were
  `claude-sdk-local` / `claude-sdk` — i.e. `npm start` outside a pod booted the
  retired id and 409'd every turn. Now `claude-pane-local` / `claude-pane`,
  tracking the CLI default, with a comment saying a real pod always gets both
  from the pod template.
- **`client/account.go`:** `SelectAnthropicAccount`'s doc said "for a claude-sdk
  session … callers apply this only for the claude backend", which post-pane-first
  reads as covering claude-pane. Reworded to name the inference-scoped token
  path and point at `SelectClaudePaneMaterial` for the pane.
- **`justfile`:** the `kind-up` comment claimed the `anthropic-credentials`
  Secret is what makes `just dev` claude work (false — the pane harvests the
  host login into a per-session Secret and ignores it); the `kind-test` cost
  model still described a claude turn that no longer exists. Both rewritten
  against `backendCases`.
- **Examples:** `Example` and `client/doc.go`'s "typical use" both drove
  `StartTurn` on an implicit backend — doubly wrong under the new default, since
  opencode is the only backend still accepting runner turns. Both now name
  `BackendOpenCode`. `Example_chat`'s step 1 swapped `SelectAnthropicAccount`
  (the retired token path) for `HarvestOpencodeAuth`, matching what
  `dashboard_connector.go` actually does.
- **Tests:** `client/orchestration_test.go`'s default assertion flipped to
  claude-pane (with shape-only `testFullPaneCred`/`testPaneAccountDoc`
  fixtures), plus a NEW subtest pinning that an empty `Backend` with no material
  returns `ErrClaudePaneCredentialMissing` and never calls `CreateSession`.
  Sites that were incidentally relying on the default (`ExtraEnv` flow, reserved
  env name, backend-error propagation, the Destroy call-order seeds, the three
  worktree Creates) now name `BackendOpenCode`; the two Anthropic-account
  contract tests in `example_test.go` name `BackendClaudeSDK` explicitly,
  because the token path IS that backend's contract and the pane gate would
  otherwise mask the sentinel under test.

**Already done, verified not residue:** `internal/k8sit/local_test.go`'s
claude-sdk conformance row was already replaced by the claude-pane row (its
comment cites O15), and SECURITY.md's A1-residual section already cites
`opencode.ts`/`codex.ts`; its two remaining `runner/src/claude.ts` mentions are
deliberate historical references to the deleted file. Test-fixture session ids
of the shape `claude-sdk-<x>` were left alone — they are arbitrary strings, and
churning ~150 of them would be noise.
## 2026-07-25 — [O8] pane OSC 52 clipboard relay (maintainer live report)

**Symptom (live, claude-pane):** selecting text in the in-pane Claude Code TUI
printed its own "sent 2165 chars via OSC 52 · if paste fails, hold Shift while
selecting for native copy" confirmation, but nothing reached the macOS
pasteboard — leaving shift-drag native selection (which loses the pane's own
selection semantics) as the only way to get text out.

**Cause:** `ExternalPane` registered no OSC 52 handler on its vt emulator, so
the child's `ESC ] 52 ; c ; <base64>` was consumed by the emulator as an
"unhandled sequence" and never re-emitted to the HOST terminal. A virtual
terminal has no clipboard of its own — the relay is the app's job, and we
weren't doing it. Verified against `charmbracelet/x/vt`: no built-in OSC 52
handling and no `Callbacks` field for it.

**Fix:**
- `tui/terminal/osc.go` (PUBLIC): new `ParseOSC52(data []byte) (text string,
  primary, ok bool)` — the parse-side counterpart to the file's emit-side OSC
  helpers. Takes the whole OSC data field (command number included, as an
  emulator hands an OSC handler); tolerates unpadded and line-wrapped base64;
  reports `ok=false` for malformed payloads and for the `?` read query (which
  can't be answered without an async host round-trip). Pinned in
  `sdktest/tui_surface_test.go`.
- `internal/tui/dashboard/external_pane.go`: `Init` registers
  `p.emu.RegisterOscHandler(52, p.handleOSC52)`; the handler queues onto a new
  `pendingClip []paneClip`, and `apply()` drains it into `tea.SetClipboard` /
  `tea.SetPrimaryClipboard` batched ahead of the next `readCmd`. The queue is
  needed because the emulator's OSC handler runs synchronously inside the
  `emu.Write` in `feed()` (on the Bubble Tea loop), where it can only stash.
  The emulator's parser reassembles a sequence across transport reads, so a
  multi-KB copy split over 32 KB chunks arrives whole.
- `app.go handlePtyOutput` batches apply's Cmd with `externalPaneFinishedMsg`
  instead of dropping it, so a copy in the child's FINAL output still lands.

**Applies to every pane backend** (claude-pane, opencode, a future codex pane)
— the relay sits at the shared `ExternalPane` seam, not in a per-backend path.

**Tests:** `external_pane_osc52_test.go` — Init-level wiring guard (drives a
fake transport so removing the `RegisterOscHandler` line fails), split-sequence
reassembly, apply batching alongside the read drain, survival across
end-of-stream, and handler queueing; `tui/terminal/osc_test.go` covers the
parse table + wrapped-base64 decode.

**Not done:** clipboard *reads* (`OSC 52 ; c ; ?`) are still dropped rather than
answered — would need `tea.ReadClipboard` plus an async reply back down the
transport. No in-pane confirmation note was added: the child prints its own.

## 2026-07-25 — empty-workspace fail-closed + sync honesty (maintainer live reports)

**Symptom (live):** a new session came up with a totally empty workspace and
the agent started working in it anyway — *"it shouldn't have let it start if
sync was failing!"*. Separately, the dashboard showed several sessions as sync
`unknown` with no glyph and no reason.

**Causes, three of them, all in the same blind spot:**

- `internal/sync/manager.go classify()` read a mutagen session stuck in
  `connecting-alpha`/`connecting-beta` with a nonzero staged-file count as
  healthy `syncing`. A dropped transport looks exactly like progress if you
  only read the counter — so the self-heal that watches for a stall never
  fired, and the status the TUI showed was a lie.
- `client`'s connect path did not gate on the first-ever sync at all. Added
  `ErrInitialSyncFailed` + `AwaitSync`, and `stagedRunner.StartTurn` refuses a
  turn whose workspace never staged.
- That gate covered the WRONG backend. `StartTurn` is the opencode headless
  adapter's path; claude-pane never submits a turn through it — the runner
  spawns the interactive child lazily on the first `GET /sessions/:id/pane`
  attach, so **the attach** is what starts an agent for a pane session. The
  reported bug was still fully open on the exact backend it was reported on.

**Fixes:** `Session.AttachPane` (`client/pane.go`) awaits the staging gate and
refuses with `ErrInitialSyncFailed` — in the SDK, not the CLI adapter, per the
maintainer's "expose it via the SDK regardless" directive. `mapPaneDial`
(`internal/cli/dashboard_connector.go`) settles that gate under its own 90s
budget so the wait is not charged to the 30s dial timeout (a slow-but-healthy
large repo must not be failed by it), and refuses only on the KNOWN failure —
a gate that merely runs out of budget falls through to the dial rather than
stranding a healthy session with no pane. A reconnect is never gated.

**Sync `unknown` was two unrelated situations wearing one label:** the mutagen
CLI/daemon could not be reached (nothing is known about ANY session), or it
answered and reported no sync session for this id (a definite answer, and a bad
one — the files are not moving). `syncProber.probe` now carries the reason in a
new `SyncHealth.Detail`, threaded through `syncStatusMsg` → `Session.SyncDetail`
→ the detail pane, and `unknown` finally has a glyph (`?`). It still degrades
rather than blocking the UI — it just no longer degrades silently.

**Footer counter:** the maintainer read "1 ready" (header) next to "⚡1 warm"
(footer) as possible duplication. They are genuinely different metrics —
`attentionSummary` over `StatusNeedsInput` vs `len(m.warmSet)` — so the answer
is no, but the footer was a bare number labelled with the codebase's internal
word. Relabelled `⚡N streaming`, which names what is counted. "ready" was left
alone: f3154c9 chose it deliberately over "needs input" for calmer language.

**Tests:** `client/pane_test.go` (refusal after a failed first sync, plus the
behavioral counter that a WARNING still proceeds), `internal/cli/
sync_support_test.go TestProbeErrDetail`, `internal/tui/dashboard/
sync_detail_render_test.go`, `footer_counters_test.go` (both counts set to 2 —
the exact case that looked like duplication).

## 2026-07-25 — ctx% divided by the wrong number (maintainer live report)

**Symptom:** the dashboard showed 20% context used for a pane whose own in-pane
statusline read 100% full. The indicator that exists to warn before a context
fills never warned.

**Cause:** exactly 5×. models.dev reports a model's MAXIMUM window — 1,000,000
for the opus tier, confirmed in the maintainer's own
`~/.local/share/sandbox/models.json` — while Claude Code runs 200,000 unless
the extended-window beta is requested, which nothing in this repo requests.
`models.Limit()` answers "what is this model capable of"; a ctx% denominator
asks "what is it running with".

**Part A — `client/models.EffectiveContextLimit`** (new exported function,
sdktest-pinned). Clamping inside `Limit()` was tried first and REVERTED: that
is also the pricing path, and several tests use its 1M value as the marker
proving the models.dev table was consulted rather than the static fallback
(`TestCacheHitAfterServerStops`, `TestStaleCacheUsedWhenFetchFails`,
`TestColdLimitDoesNotBlockOnNetwork`). Clamping there would have kept them
green while destroying what they discriminate. `Limit` keeps its meaning; the
dashboard asks the new question. Fail-safe direction: understating headroom is
visible, overstating it fails silently.

**Part B — the agent's own number, which supersedes the guess.** Claude Code
publishes `context_window.context_window_size` on every statusline sample.
`schema/events.json` gains an optional `contextLimitTokens` on `UsagePayload`
(protocolVersion unchanged — additive optional field); `claude-pane-observer`
forwards it; `readmodel.go` prefers it over anything the model id implies, so
denominator and numerator come from the same sample. `size` is accepted as a
rename hedge. A non-numeric or absent window is OMITTED, not zeroed, so
opencode/codex keep the model-derived fallback instead of losing the gauge.
Needs a runner image rebuild to reach live sessions; part A covers them until
then, and the two agree for every Claude tier anyway.

**Residual:** the part-A clamp keys off family keywords (`claude`/`opus`/
`sonnet`/`haiku`), the same heuristic `staticFallback` uses for pricing, so a
non-Anthropic model named e.g. "haiku-*" would be misread. Acceptable while it
is only a fallback behind part B.

## 2026-07-25 — main was red and the gate could not see it

Found while running the runner suite for the ctx% work: **two tests in
`runner/test/claude-pane-observer.test.ts` had been throwing `ReferenceError`
instead of asserting** since d19fd16. Both bypassPermissions provisioning tests
call `claudePaneArgs` and `paneDefaultPermissionMode` without importing them.

**Why nothing caught it:** `runner/tsconfig.json` includes only
`src/**/*.ts` — its `rootDir` cannot widen without breaking `dist/`'s layout —
and `package.json`'s lint script named `src` explicitly. The test tree was
typechecked and linted by nothing at all, so an unbound identifier survived to
runtime on green-looking `just typecheck` runs.

**Ruled out:** the `just test` recipe DOES propagate a runner failure. Verified
empirically with a scratch justfile — the recipe's `if [ -d node_modules ]; …;
fi` carries `npm test`'s exit status, and CI installs runner deps
(`.depot/workflows/ci.yml`) so the else-branch warning-skip is a local-only
hazard. The blind spot was the typecheck side, not the test side.

**Fix:** new `runner/tsconfig.test.json` (extends the build config, drops
rootDir/outDir, `noEmit`, covers `src/` + `test/`); `just typecheck` runs both
configs; lint widened to `eslint src test`. Ten pre-existing errors surfaced,
all benign strictness in test fakes — fs-fake casts needing `as unknown as`, an
arrow returning `ClientRequest` where `void` was declared, one
`SessionState`→`Record` cast, one `let` that wanted `const`.

**Also removed:** `runner/statusline/statusline`, an 8.6 MB Mach-O arm64 binary
committed in d19fd16 next to the source it was built from. The runner image
compiles its own Linux binary in the Dockerfile's statusline stage; the tracked
copy only bloated the build context. Gitignored so a local `go build` in that
directory cannot re-add it. The blob stays in history (a rewrite is not worth
it for one object).

**`just check` green on main afterwards** — the first run that actually
exercised the widened typecheck.

## `internal/authstatus` tests were not hermetic — they read the developer's environment (2026-07-27)

Triaged out of the §0 inbox. `TestClaudeProvider/nothing_anywhere`,
`TestClaudeProvider/darwin_keychain_miss_is_final`, and
`TestSystemLoginProbeCore` built providers with `Home: t.TempDir()` to mean
"nothing is configured", but left `Env` nil — and `envOr(nil)` returns
`os.Getenv` (`internal/authstatus/providers.go:14-19`), so both
`systemLoginPresent`'s `CLAUDE_CONFIG_DIR` lookup and `Status`'s
`CLAUDE_CODE_OAUTH_TOKEN`/`ANTHROPIC_API_KEY` fallbacks consulted the REAL
environment. A test asserting "nothing is configured" was letting the host
decide the answer.

**Why it had never been seen:** CI and a laptop shell set none of those vars.
Every claude-pane session pod does — `CLAUDE_CONFIG_DIR=/session/state/claude`
with a live `.credentials.json` — so the suite only fails when run from inside
the product, which is exactly where it was found.

**Fix:** a `noEnv()` helper (`env(map[string]string{})`, the existing
constructor) passed explicitly at all six previously-nil construction sites,
plus `CodexProvider`'s auth-file table (which reads a fixture `auth.json` today,
but would silently fall back to an ambient `OPENAI_API_KEY` if that read ever
regressed). The comment on the helper names the pod env as the concrete hazard.
Production code untouched — `envOr`'s os.Getenv default is correct for real
callers; only the tests were wrong to rely on it.

**Verified** with a Go 1.26.2 toolchain fetched into `/tmp` (the runner image
ships no Go): green under a clean env AND under the hostile pod-shaped env
(`CLAUDE_CONFIG_DIR` pointing at a dir with `.credentials.json`, plus all three
token vars set). Behavioral counter — reverting just the `noEnv()` injections
and re-running under the same hostile env fails exactly the three named cases
(`method = "oauth-subscription", want "none"`; `systemLoginPresent should be
false for an empty home`), so the fix is load-bearing rather than incidental.

## `go test ./...` walked into `runner/node_modules` (2026-07-27)

§0b loose end. `flatted`, a transitive npm dependency of the runner, vendors a
Go package (`runner/node_modules/flatted/golang/pkg/flatted`), and the root
module's `./...` pattern matched it — so every `just build` / `vet` / `test`
recipe was operating on a package set that included someone else's code.
Harmless in practice (that package has no test files) but the gate was not
testing what it claimed to, and an npm dep shipping a *failing* Go test would
have turned our build red for reasons entirely outside the repo.

**Fix:** one line in `go.mod` — `ignore runner/node_modules`, the directive Go
1.25 added for exactly this. Chosen over the alternative in the TODO note
(narrowing each recipe's package pattern in the `justfile`) because an explicit
package list drifts the moment a new top-level directory is added, whereas the
directive is declared once and cannot go stale. Available unconditionally here:
`go.mod` already requires go 1.26.2, so no toolchain that can build this repo
predates the directive.

**Verified:** `go list ./...` drops from 24 packages to 23 — the npm one is
gone and all 23 of ours remain; `go build ./...` and `go vet ./...` green;
`go mod tidy` leaves `go.mod` byte-identical (the directive survives a tidy).
Also confirmed on a scratch module that an `ignore` path which does not exist
on disk is a no-op, so Go gates that run before `npm install` — CI ordering —
are unaffected.

## The workspace guide reached only claude-pane sessions (2026-07-27)

§0 inbox item. `runner/src/workspace-guide.ts` writes the "your `.git` is a
pointer at a host path, git fails here by design, do not try to repair it" block
— but only to `$CLAUDE_CONFIG_DIR/CLAUDE.md`, which nothing but Claude Code
reads. An opencode or codex session still discovered the dangling worktree the
expensive way: by running git, failing, and possibly concluding the repo needed
fixing. The hazard is a property of the workspace, not of the agent looking at
it, so every backend now gets the same block.

**Fix:** a new `guideTargetFor(backend, env)` in `workspace-guide.ts` resolves
the file each backend actually reads — `$CLAUDE_CONFIG_DIR/CLAUDE.md`
(claude-pane), `$CODEX_HOME/AGENTS.md` (codex), and an `AGENTS.md` beside the
generated opencode config (opencode). `writeWorkspaceGuide` now takes an
explicit `path` instead of a `configDir` + hardcoded `CLAUDE.md`; `classifyGit`,
`guideBlock` and the marked-block `spliceGuide` were already backend-agnostic
and are untouched. The call moved out of the claude-pane branch in `index.ts` to
a single backend-dispatched call right after `materializeBootstrapFiles()`, so an
operator-seeded guide at the same path is spliced around rather than raced with.

**opencode needed one extra step:** unlike Claude Code it has no implicit pickup
for a file in our pod-local config dir, so an unregistered guide is a guide
nobody reads. `buildOpencodeConfig` now emits `instructions: [<guide path>]` —
an absolute path, which sidesteps both opencode's global-dir lookup and any
cwd-relative resolution. The field is `Config.instructions?: Array<string>`
("Additional instruction files or patterns to include"), verified against the
PINNED `@opencode-ai/sdk` types rather than the docs.

**Module structure:** `workspace-guide.ts` is kept a LEAF (imports only node +
`types.js`), which is what lets `opencode.ts` import `guideTargetFor` without a
cycle — `codex.ts` already imports `opencode.ts`, so hanging the path resolution
off the supervisors instead would have closed a loop.

**Null rather than a guess:** a backend whose config-dir env var is unset (off-pod
dev) resolves to null and the guide is quietly skipped. Deliberately NOT falling
back to the workspace — that tree syncs to the user's machine and must not grow
files they did not write.

**Verified:** both tsconfigs typecheck; six new runner tests green (per-backend
target resolution, the null cases, parent-dir creation, and both
`instructions` cases) plus the existing guide suite. NOT live-validated: that
codex reads `$CODEX_HOME/AGENTS.md` rests on CODEX_HOME being the documented
relocation of `~/.codex`, and the opencode `instructions` wiring on the schema
type — both want an eyeball on a real session (see the TODO residual).

**Unrelated pre-existing breakage observed while running the suite:** 7 runner
tests fail *inside a session pod* for the same ambient-environment reason as the
`internal/authstatus` item above — see the next entry.

## Seven runner tests read the machine they ran on (2026-07-27)

Found by running `npm test` inside a claude-pane session pod during the
workspace-guide work. Same family as the `internal/authstatus` entry above, and
the reason it matters is the same: these tests are green on CI and on a laptop
and red inside the product, which is the one place a runner test is most likely
to be run by an agent doing runner work.

**Cluster 1 — the statusline chain (5 tests, `claude-pane-statusline.test.ts`).**
`runStatusline` spread `...process.env` and only overrode PATH when a test asked
for a prepend. But `sandbox-user-statusline` on PATH is candidate 3 of the chain
the provisioned script searches, and a session pod ships exactly that at
`/usr/local/bin` — so "no user statusline anywhere" was false in-pod, and the
five built-in-line cases chained to the real host statusline, then compared its
ANSI output against the plain built-in string. Fixed with a hermetic PATH: a
private temp dir holding a single `node` symlink to `process.execPath`, used for
every run. Note `dirname(process.execPath)` would NOT have worked — node lives in
`/usr/local/bin` here too, alongside the very binary being excluded.

**The fixtures had to change with it.** Four were `#!/bin/sh` scripts calling
`cat`/`sleep`, which are PATH lookups. They had been passing by accident:
`printf` is a shell builtin, so with `cat` unresolvable the script still produced
the right stdout. Under a truly hermetic PATH the accident stopped working — and
exposed that the timeout fixture (`cat >/dev/null; sleep 5; printf late`) was
never testing a slow script at all: with `sleep` gone it printed instantly. All
fixtures are now node scripts built by a `userScript(body)` helper, so they
depend on nothing but the interpreter already required by the shebang.

**Cluster 2 — supervisor argv (2 tests, `claude-pane.test.ts`).** The tests pass
`env: {}`, which makes `paneDefaultPermissionMode` fall back to the shared
`CLAUDE_CONFIG_DIR` constant — a live config dir in-pod — while `readSettings`
defaulted to the real `readFileSync`. The pod's `settings.json` says
`bypassPermissions`, so the argv assertions were answered by the environment.
Added an explicit `noSettings` throwing seam to all 11 supervisor call sites that
were not already injecting one (the permission-mode test keeps its own stub).

**Verified:** the full runner suite is green in-pod — 314 tests, 0 failures —
where it was 7 red before. Both tsconfigs typecheck.

**Process note / self-inflicted:** while finishing this batch I ran
`npx prettier --write` over the seven files I had touched. This repo has NO
prettier dependency and no prettier config — it is hand-formatted — so that
reflowed them to prettier's defaults. Re-running with `--single-quote
--print-width 100` restored the repo's quote convention and approximate width,
but the original hand-wrapping in those files is not recoverable from inside a
session pod (the worktree's git is a dangling pointer by design). See the TODO
inbox entry; recovery is a host-side `git diff`/`git checkout`.

## `sandbox worktree path` (and completion) required a kubeconfig they never used (2026-07-27)

§0 inbox item. Both commands resolve sessions from the LOCAL index only —
`client.ResolveSessions` touches nothing but `~/.local/share/sandbox/…` — and
both document that as load-bearing: `cd $(sandbox worktree path …)` must work
with the VPN down, and a TAB press must not wait on an apiserver. Yet both were
built with `newClient()`, which goes `client.New` → `k8s.New` → kubeconfig
resolution and fails with "failed to connect to cluster" before a single line of
the index is read. The contract was right; only the construction was wrong.

**Completion failed worse than the command.** `completeSessions` swallows every
error into `ShellCompDirectiveNoFileComp`, so the kubeconfig failure was
invisible: TAB just went dead with no diagnostic. It only ever worked inside the
flox shell, which exports KUBECONFIG.

**Fix:** `internal/cli/offline.go` — `newOfflineClient()` builds the same
`*client.Client` with an `offlineBackend` injected through the EXISTING public
`client.WithBackend` seam, so no kubeconfig is ever resolved. Chosen over the
TODO's two sketched options deliberately: a `client.Offline()` constructor would
add public SDK surface (and an sdktest pin) for a CLI-local problem, and making
the k8s backend lazy would move EVERY command's connection error from
construction to first use — a behavior change well outside this item. `sdktest`
passes unchanged, which is the proof no public surface moved.

**The stub refuses rather than no-ops.** All 14 `client.Backend` methods return
`errOffline`, whose message names the cause ("this command resolves sessions
from the local index only and cannot reach the cluster") rather than the
symptom. Reaching it means an index-only command grew a cluster dependency —
a bug in the caller, not a misconfiguration on the user's machine.

**Verified** against a real binary, not just units: with `KUBECONFIG` unset and
a HOME containing no `~/.kube`, `sandbox worktree path demo` prints
`/home/u/wt/demo`, `cd $(…)` substitutes correctly, `--json` still emits its
array, and `__complete attach ""` offers the seeded session with its
title—branch description. Behavioral counter under the IDENTICAL environment:
`sandbox status`, which still uses `newClient()`, fails with "failed to connect
to cluster: k8s: load kubeconfig: invalid configuration" — so the fix is what
carries the offline paths, not the environment. Two regression tests in
`offline_test.go` pin it (resolution under a KUBECONFIG pointing at a
nonexistent file; the backend refusing cluster calls). Full Go suite green
(21 packages), gofmt clean.

## The dashboard dropped `rate_limit.updated` on the floor (2026-07-27)

§0 inbox item. Every piece existed except the wiring: the runner emits the event
off the statusline payload's `rate_limits`
(`runner/src/claude-pane-observer.ts`), `session.RateLimitPayload` is fully
defined, and the SSE stream delivered it — but `sessionReadModel.ApplyEvent` had
no case and `ApplyRunnerEvent` did not list the type, so the payload was parsed
by nobody. The renderer that used to show the windows
(`internal/tui/dashboard/statusline.go`) went with the chat stack in a935541,
and the data path was never re-pointed at a surface that still exists.

**Read model:** `RateLimitOK` plus the two utilizations and their reset instants.
The reset instants are parsed to `time.Time` IN THE REDUCER — the payload carries
RFC3339 strings and the pane status row is redrawn on every frame, so parsing at
render time would put a date parse on the hot path.

**The gate is a flag, not a value check**, which is what the TODO asked for: a
session that has never reported a window and one genuinely at 0% are
indistinguishable in the numbers, so rendering "5h 0%" for the former invents a
fact. `RateLimitOK` is set only by a report whose `Available` is true. An
unavailable report (API-key/Bedrock/Vertex auth) CLOSES the gate again rather
than freezing the last known windows — a session that lost its plan must stop
showing plan usage.

**Render:** `5h 42% ⟳2h · wk 18%` appended to the pane status row
(`external_pane.go` `statusRow`) beside ctx%/cost — the surface that inherited
the deleted status line's job. Countdowns are omitted for an unknown reset AND
for one already elapsed ("⟳-1h" is worse than nothing); utilizations are rounded
and clamped to 0-100, so a provider reporting 103 is their bug and not ours.

**Verified:** seven new tests (`ratelimit_test.go`) covering the read-model path,
the never-reported / unavailable / genuine-0% gate cases, countdown formatting
across m/h/d plus both no-countdown cases, out-of-range clamping, a malformed
timestamp degrading to a bare percentage, and the rendered status row.
Behavioral counter: deleting the one `session.EventRateLimitUpdated` line from
`ApplyRunnerEvent`'s dispatch — the exact omission that was the bug — fails four
of them. Full Go suite green (21 packages), dashboard goldens unaffected (no
fixture reports a window, so nothing new renders), gofmt clean, sdktest passes.

## "Is auto-compact on in pane sessions?" — answered (2026-07-27)

A maintainer note carried in the inbox twice (top-of-file list and §0b),
untriaged. **Answer: yes, it is on, and nothing needs seeding.** Claude Code
auto-compacts by default and the sandbox does not touch the setting at any
layer — verified across all four places it could have been changed:

- `buildClaudePaneEnv` (`runner/src/claude-pane.ts`) — the strict allowlist
  passes TERM/COLORTERM/CLAUDE_CONFIG_DIR/IS_SANDBOX plus PATH/HOME/LANG and the
  operator-declared ExtraEnv names. Nothing compaction-related.
- `mergeSettings` (`runner/src/claude-pane-observer.ts`) — owns `statusLine` and
  `sandbox.enabled`, seeds `permissions.defaultMode`. Nothing else.
- `claude-config.ts` — seeds `WORKSPACE_TRUST_SEED` + `hasCompletedOnboarding`.
- A grep of the whole tree for `autoCompact`/`auto_compact` returns nothing.

Confirmed empirically rather than by absence-of-code alone: a LIVE claude-pane
pod's `/session/state/claude/.claude.json` has no `autoCompactEnabled: false`
(the key is written only when a user turns the toggle OFF, so its absence is the
ON default) and does carry an `autoCompactWindowsCache` key, i.e. the feature is
live in that session.

**Finding attached, promoted to a new §0b item:** compaction is INVISIBLE to the
dashboard. `context.compacted` is emitted by nobody — `schema/events.json` states
this outright — while the entire Go consumer side is wired and waiting
(read-model case, `ApplyRunnerEvent` dispatch, feed marker). The gap is one entry
in `PROVISIONED_HOOK_EVENTS`: `PreCompact` is not provisioned, so the observer
never learns that a compaction happened. The user-visible symptom is a ctx% that
drops with no explanation on a long session. Caveat recorded with the item:
PreCompact's hook input carries no token counts, so the payload's
`preTokens`/`postTokens` would be 0 — harmless (the reducer skips its reset at 0
and ctx% self-corrects from the next statusline sample) but it means the value is
the marker, not the gauge reset.

## Autopilot residue in the runner (2026-07-27)

§0 inbox item: ~10 stale references left by §1e's 2026-07-20 deletion of
autopilot, plus an open "keep vs drop `capabilities.autopilot`" question.

**The keep/drop question was already decided — the item was stale on that
point.** Both `internal/session/types.go:449` ("Autopilot is always false since
claude-pane-first removed the server-side driver; the runner keeps reporting the
key so old clients still decode /status") and an sdktest pin
(`surface_test.go`, `client.State{Capabilities: ...}`) document KEEP,
always-false, deliberately. No change made, and none should be: dropping the key
would be a wire change against a pinned public surface for no gain.

**Two dead seams found, beyond the comments the item anticipated:**

- `turnSettledHandler` / `setTurnSettledHandler` (`turns.ts`) — nothing in
  `src/` ever registered a handler after the driver was deleted, so the
  `.finally()` hook on every turn was calling `null?.()`. Removed, along with
  the now-empty `.finally`. The only references were a test clearing it in
  setup/cleanup, which was updated.
- `readTurnOutcome` (`events.ts`) — ZERO references anywhere, not even a test.
  Removed per the CLAUDE.md hygiene directive.
- `sumTokens` (`events.ts`) — called only by its own [V39] regression test.
  KEPT rather than deleted: the double-counting rule it encodes (count only each
  turn's terminal usage row) is the non-obvious part and §10's observability work
  wants exactly this number. Its doc comment now says plainly that nothing in
  `src/` calls it.

**Comments corrected** in `turns.ts` (module header now says the driver is gone
and the route is the only caller, its live consumer being the opencode headless
first-turn adapter), `server.ts` ×2, and `index.ts` (the `boot_prep` phase list
mentioned "agent/autopilot setup"; it now names the workspace guide and agent
selection, which is what that phase actually covers).

**Verified:** both tsconfigs clean, runner suite green (314 tests, 0 fail).
CAVEAT — `start-turn-guard.test.ts`, the file whose imports were edited, is among
the 49 that SELF-SKIP in this environment: better-sqlite3's native addon is not
built (no C compiler in the runner pod, so `npm rebuild` fails), and the suite
skips cleanly rather than lying. Its edit is therefore verified statically only —
which is meaningful here, since a dangling reference to the removed export would
have failed `tsc -p tsconfig.test.json`. CI rebuilds the addon and sets
RUNNER_REQUIRE_SQLITE=1, so it runs there for real.

## §4 measure-first perf items: measured, one declined and one fixed (2026-07-27)

Both items said "measure before optimizing" and neither had numbers.
`perf_bench_test.go` supplies them (amd64, `-benchtime=2s`, verified stable
across `-count=2`). The bar throughout is a 60fps frame: 16.6ms for everything
the dashboard does.

**`visibleSessions()` re-filters+re-sorts 4+ times per frame — DECLINED, do not
memoize.**

| case | n=8 | n=50 | n=200 |
|---|---|---|---|
| no filter, no attention sort | 5.7ns | 5.7ns | 5.7ns |
| attention-first only | 5.0µs | 30µs | — |
| filter active | 13µs | 77µs | 275µs |
| worst case ×4 per frame | 48µs | 364µs | ~1.1ms |

The default view is FREE and flat in n — `FilterSessions` returns the input
slice on an empty query and `sortByAttention` is a passthrough when
`attentionFirst` is off, so the common case is a couple of function calls. Even
the worst realistic configuration (50 sessions, filter active AND attention
sorting on, four calls) is ~0.36ms — about 2% of a frame. Memoization would add
an invalidation surface (every session mutation, filter keystroke, and mode
toggle) to buy back 2% in a case that only exists while the user is holding down
a filter query. Not worth it; item closed as measured-and-declined rather than
left open forever.

**`fitModal` two ANSI width scans per line — FIXED, ~26%.** This one did earn a
change: 1.35ms per call on a tall feed (h=120, w=200), ~8% of a frame budget,
for ONE call. The loop measured each line twice — once directly, once inside
`padRight` — so it now computes the width once and pads inline. Measured
1.35ms → 1.00ms (120×200) and 89µs → 65µs (20×80).

**~26%, not the 50% the scan count implies** — the remainder is `strings.Split`/
`Join` and the padding `Repeat`, i.e. allocation, not measurement. That is
recorded in the code comment specifically so the next reader does not repeat the
experiment expecting a halving, and knows further micro-optimizing this loop
would have to target allocations instead.

Output is byte-identical: the full dashboard suite including the golden fixtures
passes unchanged. An over-wide line is deliberately re-measured after
`ansi.Truncate`, since grapheme clusters can land it below `w` and that shortfall
is what the padding must cover.

## [P7] feed streaming O(n²) — measured, declined (2026-07-27)

Filed "LOW, only if felt". Measured with `BenchmarkFeedAssistantStream`
(amd64, `-benchtime=2s`); numbers are for one COMPLETE assistant message, not
per frame:

| message | deltas | total | per delta |
|---|---|---|---|
| 4 KB | 200 | 0.59ms | 2.9µs |
| 32 KB | 200 | 1.05ms | 5.2µs |
| 32 KB | 1600 | 5.03ms | 3.1µs |

**3-5µs per delta** — ~0.03% of a 60fps frame, for work that happens at network
cadence (tens of deltas per second), not once per frame. Not felt, and it would
take a message orders of magnitude larger to become so.

The third row is the one that settles it. Holding the message at 32 KB and
cutting the delta count 8× made it 4.8× faster, while holding deltas at 200 and
cutting bytes 8× made it only 1.8× faster. So the cost tracks the PER-DELTA
constants (json.Unmarshal, the item allocations) far more than the O(L) rebuild
the item was about — the quadratic term is real but sub-dominant at every
realistic size. Optimizing the rebuild (the [E7] trick: length-keyed change
detection instead of full-string compare) would therefore buy back a minority of
an already-negligible cost.

Declined and closed rather than left open. The benchmark stays so a future
change that makes messages much larger, or adds per-delta work, shows up here.

## Compaction is visible: PreCompact → `context.compacted` (2026-07-27)

Claude Code auto-compacts by default (answered 2026-07-27), and it happened
INVISIBLY: ctx% dropped with no explanation and nothing in the feed recorded it.
The whole Go side was already built and waiting — `EventContextCompacted`,
`ContextCompactedPayload`, the `readmodel.go` reducer case, the feed marker —
and `schema/events.json` said outright that no backend emitted it. The gap was
provisioning: `PROVISIONED_HOOK_EVENTS` did not list `PreCompact`, the hook
Claude Code fires on compaction.

Added `{ event: 'PreCompact', matcher: true }` plus its case in the observer's
ingest switch (`runner/src/claude-pane-observer.ts`).

Two judgement calls worth recording:

- **It is a MARKER, not a gauge reset.** PreCompact's hook input carries
  `session_id`/`transcript_path`/`trigger`/`custom_instructions` and no token
  counts, and it fires BEFORE compaction runs, so the post-compaction size is
  unknowable at that point. `postTokens` is therefore omitted, which the Go
  reducer already reads as "leave the counters alone"; the next statusline
  sample corrects ctx% within seconds anyway.
- **`preTokens` is real, not 0.** The TODO item predicted 0 for both counts. The
  observer already sees context occupancy on every statusline sample, so it now
  tracks the latest as a level (`lastContextTokens`, deliberately updated even
  when the `usage.updated` emit is deduped — it is a level, not an event) in the
  SAME units as the dashboard's ctx% numerator (`input + cache-read +
  cache-write`, `dashboard/session.go:183`). The feed's "N→" therefore matches
  the number the user was just watching.

`trigger` is carried through as claude's own `auto`/`manual`, which is what lets
the feed distinguish an auto-compaction from a user's `/compact`; it defaults to
`auto` when absent since the schema requires it.

Three tests in `runner/test/claude-pane-observer.test.ts` pin the payload, the
absent `postTokens`, the no-turn/no-statusline defaults, and that dedupe cannot
stall the tracked occupancy. The existing provisioning test iterates
`PROVISIONED_HOOK_EVENTS`, so the new hook's settings entry is covered by
construction. **Needs a runner image rebuild to reach live sessions** — batch it
with the §0b ctx% part-B rebuild.

## [T3] dead `startTurnTrace` deleted (2026-07-27)

`runner/src/trace.ts`'s per-turn trace had zero production callers — the SDK turn
engine that drove it (`runTurn`) was deleted by claude-pane-first — and survived
only because `runner/test/trace.test.ts` exercised it in three cases. Removed the
function, its `TurnTrace` interface, and the `NOOP` const along with those tests.

`traceTurnLink`, `traceIDFromHeader`, and `startBootTrace` all stay — each is
wired. Two consequential tidies rather than a bare deletion: the shared options
type was renamed `TurnTraceOptions` → `TraceOptions` (it now serves only the boot
and link traces, and nothing outside `trace.ts` referenced the name), and the
module header plus `traceTurnLink`'s doc were corrected — both described
`turn.*` milestone lines that no longer exist. The header now records why the
turn trace went and what a replacement would have to hang off (the observer's
event path; there is no driving loop any more).

Runner suite: 6 pass in `trace.test.ts` (was 9), `just typecheck` clean.

## §7a seeded opencode sessions no longer stamped against the shared Secret (2026-07-27)

`stampOpencodeCredsFreshness` fingerprinted the shared `opencode-credentials`
Secret for EVERY opencode session, including seeded ones whose credentials ride
their own per-session Secret and which never read the shared one. Rotating a
shared provider key would then make `warnIfOpencodeCredsRotated` tell the
operator their pod was authenticating with a stale key, when nothing about that
session's credentials had changed.

One-line gate: `len(spec.OpencodeAuthJSON) > 0` returns early, which is the same
seeded-vs-fallback discriminator `opencodeEnv` and the re-create reconcile
already key off. Leaving the annotations off is sufficient — both
`warnIfOpencodeCredsRotated` and `refreshOpencodeCredsStamp` no-op without them.

`TestCreateSessionDoesNotStampSeededOpencodeSession` pins it, with the shared
Secret present and readable so the seeded check is the only thing under test, and
asserts the CONSEQUENCE (the warning stays silent) rather than only the
annotations. Counter-checked by reverting the gate: both annotations reappear and
the test fails. Note the fix is create-time — a seeded session created before it
still carries a stamp and can still warn spuriously until recreated.

## README: operator injection surface documented (2026-07-27)

The §8 part-A/part-B docs closeout residual. `ExtraEnv` / `ExtraSecretEnv` /
`BootstrapFiles` were fully built and covered in `docs/architecture.md` and the
design doc, but the README — the only doc an operator reads first — never
mentioned them. Added "Injecting your own config into sessions (Go SDK)" between
Commands and Testing (the README had no SDK section at all before this).

Written for the operator standing sessions up from their own tooling, not the
laptop user, and organized around the two things that surprise people:

- **`ExtraSecretEnv` is deliberately agent-visible.** Stated plainly, with the
  reason (a PAT the agent cannot read is a PAT its `git push` cannot use) and the
  honest boundary: it is protected in transit and at rest, not from the agent —
  so anything the agent must not see does not belong in a session at all.
- **`BootstrapFiles` are rejected inside the synced workspace.** Framed as *why*
  rather than as a rule: a file written there syncs back to the user's laptop and
  into their git status.

Also the fail-closed sentinel list, the write-if-changed restart behavior, and a
cross-reference to `k8s/networkpolicy-egress-fqdn.yaml.example` — an injected
tool that talks to an outside endpoint needs egress opened too, which is the
natural next wall to hit.

The Go example is **compile-checked, not eyeballed**: it was built as a
temporary file inside the `sdktest` module (which imports `client` as a genuine
external consumer) and `go vet` run over it, then removed. That caught a real
bug — the draft used `session.BackendClaudePane`, from the `internal/session`
package no external consumer can import. The public spelling is
`client.BackendClaudePane`.

## [T8] audit.jsonl size-capped with one retained generation (2026-07-27)

The unbounded-growth half of [T8]. `runner/src/audit.ts` appended forever to a
PVC that also holds `events.db`, `session.json`, and the agent's own state, with
no cap and no rotation — and [T6] had already flagged that nothing watches PVC
fill either, so the failure mode was a full volume with no warning.

Now rotates at `SANDBOX_AUDIT_MAX_BYTES` (default 8 MiB, matching the [E8-E10]
host event-cache tail cap; `0` disables) keeping exactly ONE previous generation
as `audit.jsonl.1`, so worst-case usage is 2× the cap rather than a growing
`.1/.2/.3` chain.

Three decisions the tests pin, because each has a wrong-looking alternative:

- **Rotate AFTER the write, not before.** The row that crosses the cap lands in
  the generation that was current when it happened, so no row is ever split
  across two files and a failed rotation cannot lose the row that triggered it.
- **Seed the byte counter from the file at boot**, not from 0. A session that
  restarts often would otherwise reset the counter every boot and never reach the
  cap — the exact long-lived sessions the cap exists for.
- **A failed rename keeps appending.** An unbounded log is a much better failure
  mode than a dropped audit row, so `rotate()` swallows the error and retries on
  the next append.

Rotation is a safety net, not routine: 8 MiB is far above any plausible session's
tool volume. The honest cost is stated in SECURITY.md rather than buried — the
log is append-only *within a generation*, but retention is bounded, so a session
producing more than ~16 MiB of audit rows loses its oldest, and an operator who
needs a complete trail should raise the cap or set `0`.

Testing needed a seam: `AUDIT_JSONL_PATH` is a hardcoded const pointing at the
pod's live state dir, so a test exercising the real `appendAudit` would have
written into the running session's own audit log. Added `createAuditWriter(deps)`
(path/maxBytes/fs functions injectable, mirroring `PaneObserverFs`) with
`appendAudit` as the module-level default instance over it — no call-site
changes. `runner/test/audit-rotation.test.ts`: 7 tests over an in-memory fake fs,
covering the no-rotate path, the 2× bound (asserting no `.2` is ever created),
the triggering row's integrity, restart seeding, `maxBytes=0`, rename failure,
and that rotation does not bypass redaction.

**Still open (tracked on the item):** the reader half. The log is still never
synced home and has no host-side reader, so it remains a file nobody can read.

## [T2] runner structured logging (2026-07-27) — closes C10

The runner's entire diagnostic surface was 32 ad-hoc `console.*` calls across 12
files with no level, timestamp, session id, or correlation id — and
`runner/src/server.ts` logged exactly ONE line ever (at listen), so the HTTP
surface had zero request logging. A 401 storm, a slow route, or a 500 were all
invisible without reproducing them locally.

New `runner/src/log.ts`: `createLogger(component)` → `debug/info/warn/error`
plus `child(fields)` for per-request pinning, `setLogSessionId()` called once at
boot (in `index.ts`, immediately after `loadConfig`, so nothing but loadConfig's
own warning can log without it).

**Text is the default format, not JSON.** `kubectl logs` is the primary reader
and the item's own constraint was "keep pod logs scannable by eye", so a record
renders as one line with structure as trailing `key=value` pairs:

```
2026-07-27T10:11:12.000Z INFO  opencode: config written; starting serve port=4096
2026-07-27T10:11:13.000Z ERROR events: failed to persist event event=turn.started err="disk full"
```

`SANDBOX_LOG_FORMAT=json` switches to ndjson with the same fields plus
`sessionId` (deliberately omitted from text mode — one session per pod makes it
constant noise there, while the multi-pod aggregator that reads json always
needs it). `SANDBOX_LOG_LEVEL` gates both. Values containing whitespace are
quoted so `key=value` survives both an eyeball and awk.

Details worth keeping:

- **Errors are unwrapped, not serialized.** An `err` field carrying an `Error`
  JSON-serializes to `{}` — the single most common way a structured logger loses
  the failure it was added to capture. `normalizeFields` takes `.message`, and
  adds `.stack` only at debug level.
- **HTTP request logging is level-graded**: debug for 2xx/3xx, warn for 4xx,
  error for 5xx. A flat info would drown the log on a busy pod, which is how
  request logging usually ends up disabled. Logged on response `finish` (status
  and duration known), with a `close` fallback noting a client disconnect; a 500
  additionally logs the error itself, which previously went only into a response
  body nobody may ever read. `X-Sandbox-Trace-Id` rides as `traceId`, which is
  the first half of what [T4] wants.
- **`trace.ts` keeps its own envelope.** The `trace: <id> <name> <ms>ms` lines
  are a documented, greppable contract that predates this logger; wrapping them
  would break every grep recipe written against them. They go out through a
  single `logRaw` exemption, commented as the only intended caller.
- **`no-console` added to the runner ESLint config** as the durable guard — the
  thing that keeps the migration from rotting one call site at a time.

`runner/test/log.test.ts` (9 tests): text/json shapes, whitespace quoting, level
filtering, stderr-vs-stdout routing, Error unwrapping and the debug-only stack,
child pinning without parent mutation, per-record override.

One pre-existing test needed repointing: `state-version.test.ts` mocked
`console.error` to assert the "newer state_version" warning. It now mocks
`process.stderr.write`, which is the stronger assertion anyway — it pins what an
operator actually sees rather than which function produced it.

`docs/runner-api.md` rewritten: the "runner emits no structured logs today"
caveat is replaced by the env-knob table, the format examples, the request-log
grading, and the `logRaw` exemption. **This closes C10** in
`docs/oss-launch/HARDENING-BACKLOG.md`, and it is the prerequisite for [T7]
(`sandbox logs`), which needs a log worth reading.

## [S5] pane WebSocket payload + resize bounds (2026-07-27)

Filed INFO with the instruction "when next touching `server.ts`", parked behind
the §4 slow-link compression change. That change is still gated on RTT numbers
from a real slow link, so it was holding a small hardening item hostage
indefinitely; [T2]'s request-logging work touched exactly this surface, so [S5]
rode that instead and the slow-link item no longer carries it.

Two bounds, both on what ONE authenticated client can make the runner allocate
(the pane socket is authenticated, so this is not a perimeter control):

- **`MAX_PANE_FRAME_BYTES` = 1 MiB** on the pane `WebSocketServer`. `ws` defaults
  `maxPayload` to 100 MiB; inbound pane frames are keystrokes, pasted text, and
  tiny resize control messages, so 1 MiB is orders of magnitude above a realistic
  paste. `ws` closes with 1009 past it.
- **`MAX_PANE_DIMENSION` = 2000** in `parsePaneControl`. The lower bound (`>0`)
  was always enforced; there was no upper one, so `{"cols":2147483647}` went
  straight to a node-pty resize. 2000 clears any real display (a 5120px ultrawide
  at a 6px cell is ~850 columns).

The dimension check **rejects rather than clamps**, matching how every other
malformed control frame is handled: a client asking for an impossible geometry is
confused, and honoring half its request would leave the two ends disagreeing
about the pane size — a worse failure than ignoring the frame.

`runner/test/pane-control-bounds.test.ts` (5 tests). `parsePaneControl` had no
test coverage at all before this, so the pre-existing shape and lower-bound
checks are now pinned too, not just the new ceiling.

## [T1] CLI debug log gets a per-session file sink (2026-07-27)

`internal/cli/debug.go` was a real slog JSON-lines logger with a documented
schema and exactly three call sites — and its only sink was stderr, which made it
useless in the workflow people actually use. The dashboard owns the alt-screen,
so `--debug` there either scrolls past invisibly or scribbles over the UI.

`attachDebugFileSink(sessionID, alsoStderr)` writes records to
`~/.local/share/sandbox/remote-sessions/<id>/debug.jsonl`, and
`afterTUIForSession` — a session-aware wrapper over the existing `afterTUI`
chokepoint — installs it for the TUI entry points (`sandbox attach`,
`sandbox claude`).

Decisions:

- **File-only for the TUI, not a tee.** The item said "tee", but teeing to stderr
  under an alt-screen is the exact behavior that made the flag unusable. Non-TUI
  commands keep stderr and are unchanged; `alsoStderr` exists (and is tested) for
  a future caller that wants both.
- **Append, not truncate.** A session is debugged across several commands
  (create, then attach, then suspend); truncating per attach would leave only the
  last one's records.
- **Failures are advisory and printed before the TUI starts**, so a debug log
  that cannot be opened never blocks the session the user actually asked for, and
  the warning itself cannot corrupt the screen it is warning about.
- Records carry the session id, so a log found on disk is attributable without
  reading the path it came from.

Lifecycle instrumentation added at the CLI's own suspend/resume/destroy call
sites (requested / complete / failed-with-error). 4 tests in `debug_test.go`
cover the file contents, the session stamp, cross-command appending, the
debug-off no-op (which must not create a stray directory), and tee mode.

**Deliberately not done, and why.** The rest of the item's instrumentation list —
port-forward establish and each reconnect, health-check attempts, sync
create/flush, credential resolution — lives in `client/`, `internal/k8s`, and
`internal/sync`. None of those can reach `internal/cli`'s unexported `dbg`, and
wiring them means adding a logging seam to the PUBLIC client package (a
`slog.Logger`/handler option on `client.New`). That is a public-API design
decision with sdktest pins attached, not a mechanical edit, so it is left on the
item to be decided alongside the §8 SDK surface work rather than guessed at here.

**Unverified:** the item's own verification line is a live `--debug` dashboard
run leaving a parseable file behind with no visible terminal corruption. The unit
tests pin the file contents and the no-stderr-under-TUI property; the end-to-end
run needs a cluster.

## [T6] part (a): event-log and PVC storage gauges (2026-07-27)

The event log deliberately never `VACUUM`s (it would block the single writer) and
`RETENTION_MAX_EVENTS` is opt-in and off by default, so it grows monotonically on
the same volume as `session.json`, `audit.jsonl`, and the agent's own state —
with nothing watching it. Part (a) is explicitly "measure first"; part (b) (should
the retention default change?) is meant to be decided from those numbers instead
of a guess, and stays open.

`sampleStorageStats()` in `runner/src/events.ts` reports four values, logged at
boot and every `SANDBOX_STORAGE_GAUGE_MS` (default 15 minutes; `0` disables) via
`startStorageGauge()` in `index.ts` through the [T2] logger. Fifteen minutes
because storage moves slowly and these are for trend-spotting, not alerting — a
tighter interval would just make a multi-day session's log unreadable.

Two measurement details that would have made the numbers wrong:

- **`eventLogBytes` counts `-wal` and `-shm`, not just `events.db`.** SQLite's WAL
  can dwarf the main file between checkpoints, so a main-file-only gauge
  under-reports precisely when the log is growing fastest — the case the gauge
  exists to catch.
- **Free space uses `bavail`, not `bfree`.** Root-reserved blocks are not writable
  by the runner; reporting `bfree` would overstate the headroom it actually has.

Best-effort throughout: a missing WAL, an unreadable db, or a filesystem without
`statfs` yields 0 for that field rather than throwing. A gauge that crashes the
runner is worse than a gauge that reads 0. The sampler takes an injectable
fs/db seam, so `runner/test/storage-gauge.test.ts` (4 tests) pins the WAL
summation, the missing-WAL case, the bavail-vs-bfree choice, and the
degrade-to-zero contract without touching a real PVC.

No retention behavior changed. Needs a runner image rebuild to reach live
sessions — batch with the §0b rebuild.

## §5 [V35] residual: dashboard reaps paused syncs, correctly (2026-07-27)

The CLI-side half of [V35] landed 2026-07-18; the dashboard half was left open
with a precise reason — its grace logic protects only Running/Creating, so it
could not tell a suspended session's legitimately-paused syncs from a deleted
session's immortal ones. Rather than get it wrong, it listed no paused syncs at
all, which left the deleted-session case uncollected.

The fix is the distinction itself. `OrphanSync` gains `Paused`, the CLI's
`ListOrphans` now emits paused syncs tagged with it, and `reapOrphans` asks a
different question per kind:

| sync kind | protected while | rationale |
|---|---|---|
| transport-down | pod is up (`gcRunningSet`) | it is thrashing a pod that isn't there |
| paused | session exists (`gcKnownSet`) | suspend paused it; resume unpauses it |

`gcKnownSet` is new and deliberately broader than `gcRunningSet`: everything but
`StatusGone`, so Suspended and Failed count as existing. That breadth is the
whole point — the two sets disagree exactly on a Suspended session, which is the
case the item was stuck on.

The consequences for one suspended session, now both correct and opposite:
its transport-down syncs are still reaped (the original 634-leak fix stands),
while its paused syncs survive to be resumed. And a session deleted out of band
while suspended finally has its paused syncs collected — nothing else ever
would, since `IsOrphanStatus` excludes paused by design.

Three tests in `sync_gc_test.go` cover the mixed case (one suspended session with
both kinds, plus a gone session's paused sync), and pin `gcKnownSet`'s
Gone-exclusion and that it is genuinely broader than the liveness set — a rule
built on two identical sets would be a no-op. Counter-checked by deleting the
paused branch: the suspended session's paused sync gets reaped, which is the
silent-file-sync-loss bug the item was guarding against. The `SyncReaper`
interface doc, which described only the old single rule, was rewritten.

## [T4] pane trace correlation, both ends (2026-07-27)

`X-Sandbox-Trace-Id` was consumed in exactly one place — `POST /turns`, the
*opencode headless* path. The claude-pane WebSocket upgrade neither read nor
propagated it, so a connect-flow id from `client/trace.go` dead-ended at the HTTP
routes and could not be followed into pane activity. For the primary backend that
is the entire interaction, which made the correlation id close to useless exactly
where it mattered most.

Both halves were missing, so both landed:

- **Client** (`internal/runner/pane.go`): `AttachPane` sets the header on the
  WebSocket handshake, the same one every other runner request already carried.
  Skipped when unset — an empty header value would parse server-side as a real id
  and pollute the log with an unmatchable correlation key.
- **Runner** (`runner/src/server.ts`): the pane upgrade reads the header, with a
  `traceId` query-param fallback for clients that cannot set handshake headers
  (browsers), both through the same `traceIDFromHeader` validation since both are
  untrusted input headed for a log line. The id is pinned onto a child logger and
  stamped on `pane attached` / `pane detached` records — which also gives the pane
  lifecycle its first logging of any kind.

This is the payoff of doing [T2] first: correlation needed somewhere structured
to land, and `child({traceId})` was already there.

2 tests in `internal/runner/pane_test.go` (header present with the id; absent
when unset). **Live verification still owed** — the item's own check is one
`sandbox --trace attach` followed by grepping the connect id onto pane lines,
which needs a cluster, and the runner half needs an image rebuild (batch with the
§0b rebuild).

## Unwanted prettier reflow on seven runner files — reviewed and kept (2026-07-27)

While finishing the workspace-guide and test-hermeticity items, an agent ran
`npx prettier --write` over `runner/src/{workspace-guide,opencode,index}.ts` and
`runner/test/{workspace-guide,opencode,claude-pane,claude-pane-statusline}.test.ts`.
That was a mistake: the repo has no prettier dependency and no prettier config,
so there was nothing to say what "formatted" means here — the tree is
hand-formatted. A second pass with `--single-quote --print-width 100` restored
the quote style, but the original hand-wrapping was gone, and it is not
recoverable in-pod (a session worktree's `.git` is a dangling pointer by design).

Maintainer call: **keep the reflow.** The files were reviewed rather than
reverted, and there is nothing wrong with them —

- `eslint src test` (the repo's only checked-in style enforcement) is clean.
- Both tsconfigs typecheck; the runner suite is green.
- The style markers that survived match the rest of the tree: single quotes in
  TS source, 2-space indent, semicolons, trailing commas in multiline literals.
  The double quotes in `opencode.ts` `guardrailPluginSource()` are inside a
  template literal — generated JS source, deliberately unlike the host file.
- The one residual difference is wrap width: these seven wrap at 100, where the
  rest of `runner/` runs to 130+ in places (`server.ts` peaks at 186). Tighter,
  not wrong, and well inside what the tree already contains — 20 of the other
  62 runner files also stay under 110.

The guard that stands, and the actual lesson: **no formatter runs in this tree
unless a config for it is checked in.** If the wrap-width split ever becomes
annoying, the fix is to pick a formatter repo-wide and commit its config, not to
reflow files piecemeal — which is how you get a tree where each file is
consistent with itself and with nothing else.

## Mouse capture cost text selection on every screen (2026-07-27)

**Reported as "I can't select text to copy from the dashboard TUI."** The cause
was one line: `App.View` set `v.MouseMode = tea.MouseModeCellMotion`
unconditionally, for every screen.

**Why that takes selection away is not our choice — the granularity is.** The
mode bubbletea emits for `MouseModeCellMotion` is DECSET 1002 (button-event
tracking) + 1006 (SGR), and 1002 is a *single* switch covering click, release,
wheel and drag-while-held. There is no wheel-only mouse mode to ask for, so any
app that wants the wheel also takes click-drag, and the terminal responds by
handing the app every mouse event and stopping its own selection. `shift+drag`
is the conventional bypass and the only way out. That part is the protocol.

**What WAS our bug: we paid that price on screens that consume nothing.**
`handleMouse` is reachable from `updateExternalScreen` (`app.go:962`) and
nowhere else — not the session list, not the feed, not the pickers, and
`zones.go` is layout, not hit-testing. On those screens every captured event was
dropped, so the user got neither drag-select **nor** a working wheel: strictly
worse than not capturing at all.

**Fix:** capture only on `ScreenExternal`. Off the pane the terminal handles the
mouse itself again — native selection returns, and in the alt screen the wheel
is translated to Up/Down, which the list already binds to navigation
(`model_input.go:439`, `keymap.go:51`). So those screens gained selection *and*
scroll from the same change.

**The pane keeps capture, because there it is paid for:** the wheel drives pane
scrollback and a child that enables its own tracking (opencode sets DECSET
1000/1002/1003 + SGR 1006) needs clicks re-encoded onto its PTY — its requests
reach only the emulator, since the host terminal's mouse mode belongs to the
outer program. Removing capture here would reintroduce the known bug where the
host turned the wheel into arrow keys that the child read as prompt history.
`shift+drag` is therefore still the way to select inside the pane, and since
that is invisible from the UI it is now documented: `keymapCategories` gains a
static **Mouse** category (the one help group not derived from `FullHelp`,
because terminal behavior is not a keybinding).

**Verified** by a per-screen table test asserting `MouseModeNone` on
list/feed/connecting and `MouseModeCellMotion` on the pane, plus a test that the
decision is re-made per frame rather than latched. The negative half is the
load-bearing one: a regression that re-enables global capture still renders and
still passes every other test, and the only symptom is silently losing
selection.

## Detail panel now shows the local worktree path (2026-07-27)

**Asked for as "show the local worktree path that has a session's changes in
it."** No plumbing was needed: `State.WorkspacePath` is the pod's cwd and both
Mutagen sync endpoints — i.e. the per-session git worktree whenever one exists —
and `statusFromSandbox` already recovers it from pod env on every read
(`internal/k8s/backend.go:1457`), so it was already on the dashboard's `Session`.

**The row is suppressed rather than duplicated** when `WorkspacePath` is empty or
equals `ProjectPath`, which is exactly how a non-git or `--worktree=off` session
is represented: there the repo root *is* the workspace, and one path under two
labels is noise.

**Two things the render surfaced, neither obvious from reading:**

- `kit.KV` truncates the KEY but never the VALUE. Project paths are short enough
  to have never hit this; a ~78-column worktree path is not, and would have
  overflowed the panel. Values are now fitted to `width - detailKVWidth - 1`.
- `detailKVWidth` was 7, sized to `created`/`project`. `worktree` is 8, so the
  new row rendered its own label as `worktr…`. Widened to 8.

**Paths are cut from the LEFT** (`fitPathTail`), dropping whole segments and
marking the elision `…/`. Worktree paths share a long prefix and differ only in
the final segment, so a right-truncation would render every session's as the
same `~/.local/share/sandbox/remote-ses…` and lose the only identifying part.
`project` is home-collapsed through the same helper for consistency.

## tui/theme concurrency contract — stated, not enforced (2026-07-27, §8 [P2-7]+[P2-8])

**The complaint the item recorded was real and precise:** `tui/kit` moved its
palette behind an `atomic.Pointer` so "multiple tea.Programs sharing this
process never race" (`tui/kit/palette.go:4,35`), but the layer *above* it —
`theme.ApplyTheme` — writes ~40 plain exported package vars plus `activeTheme`,
`changeHooks` and `themeEpoch` unsynchronized, and is what calls
`kit.SetANSITable`/`SetComponentColors`. So the hardening was defeated one layer
up, and the single-goroutine contract lived only in a comment on an unexported
var while the package doc advertised "reusable across TUI applications". The
split was the wrong part.

**Resolved by stating the contract, not by finishing the hardening.** The reason
is that the tokens are exported **vars**, not accessors. `theme.Charple` is a
bare memory load at render time, by design, and that is the whole reason the
package can be read from a hot render path without ceremony. Making the ~40 of
them race-free means turning every one into a function call — a break of the
entire public token vocabulary and of every render site in the tree — and after
paying that, `[P2-8]` is still unfixed: the palette is one per *process*, so two
independently themed consumers in one binary clobber each other whether or not
the writes are atomic. Synchronization would have bought memory-model
correctness for a configuration that stays semantically wrong. `tui/kit` keeps
its `atomic.Pointer` — it guards an *unexported* palette with no var surface, so
it costs its consumers nothing.

**Checked that this documents reality rather than papering over a live race**
before writing it: every `ApplyTheme`/`Cycle`/`ApplyForBackground` call site is
either package `init()` or a Bubble Tea `Update` (`app.go:438`,
`cmd/tuikit-demo/{chat,picker}.go`), both `OnChange` registrations are startup
(`internal/tui/dashboard/styles.go:36` in `init()`,
`cmd/tuikit-demo/main.go:116` before `.Run()`), and no `go func()` body or
`func() tea.Msg` closure in `internal/tui/dashboard`, `tui/`, `internal/cli` or
`cmd/` reads a `theme.*` token, `Epoch()` or `Active()`. The three PTY reader
goroutines in `external_pane.go` move raw bytes only; `notify.go:113` computes
its styled fields *before* building the `tea.Cmd` closure.

**What the doc now says**, in the package doc under "Concurrency and process
scope" and repeated on `ApplyTheme`, `Register`, `Cycle`, `OnChange`, `Epoch`,
`Active` and the token block: one goroutine — in practice the one running
Update/View, or startup before the loop begins; route theme changes through
`tea.Program.Send` rather than calling `ApplyTheme` from a worker; and one
palette per process, so **a library embedding a theme-swapping TUI should expose
theme selection to its host rather than applying a theme itself**. The escape
hatch is named explicitly: `Theme` is an inert table of colors, so
`ByName`/`DefaultForBackground` let an embedder derive its own styles without
ever calling `ApplyTheme`.

**The sdktest pin is the escape hatch, because the contract has none.** A stated
contract has no signature to break, which is exactly the "if the pin can't be
written, the seam isn't real" case — so what got pinned in
`sdktest/tui_surface_test.go` is the only mechanically checkable half:
`theme.ByName`, `theme.DefaultForBackground`, `theme.Register`, `theme.Cycle`
and `theme.Active`. If the inert-`Theme` path disappears, the documented
workaround for the process-global ceiling disappears with it, and the pin fails.

## `client.Backend` made externally implementable (2026-07-27, §8 `[P1-1]`+`[P1-6]`, folds in `[P1-7]`)

Two things blocked an outside module from implementing `client.Backend`:
`EnsureReaper`'s argument was `internal/k8s.ReaperOptions` (unnameable outside
the main module), and `PortForward`/`Connect` indexed the returned handle slice
*positionally* (`handles[0]` the runner endpoint, `handles[1]` SSH,
`handles[2]` opencode) against an ordering documented only on an internal
helper — an external implementer could compile fine and still misroute traffic
by returning handles in a different order.

**Fixed both in one change**, since exporting the type alone would have shipped
a seam that silently miscompiles into cross-wired ports. `internal/session`
gained `PortName` (`PortRunner`/`PortSSH`/`PortOpencode`/`PortCodex`) and the
standard-port constants, a `PortSpec.Name` field, a `Forwards` map type
(`map[PortName]ForwardHandle` with `Get`/`LocalPort`/`Close`), and a `Forward(names
...PortName) []PortSpec` builder. `Backend.PortForward` now returns `Forwards`
instead of `[]ForwardHandle`; `internal/k8s.Backend.PortForward` validates
fail-closed *before opening anything* (empty/duplicate `Name`, non-positive
`Remote`) and builds the returned map keyed by each spec's `Name`. The four
`k8s.ForwardSpecs*` helpers are deleted; every caller (`client/session.go`
Connect's three port-forward branches, `client/client.go` DialRunner,
`client/shell.go` SSHTarget, `internal/cli/trace.go`, `internal/k8sit/local_test.go`)
now calls `Forward(PortRunner, ...)` and looks its handle up by name.
`ReaperOptions` moved from `internal/k8s` to `internal/session` (a plain
declaration with no k8s dependency); `internal/k8s.ReaperOptions` is now a type
alias so every internal call site kept compiling unchanged. `client/client.go`
re-exports `PortName`/`PortSpec`/`ForwardHandle`/`Forwards`/`ReaperOptions` and
the port constants, and adds `client.Forward`.

`Session.SyncForwardAlive` — previously "`handles[1]` is the SSH forward by
construction" — now does a name lookup (`Get(PortSSH)`); an Observer connection
simply has no `PortSSH` entry rather than a shorter slice.

**The sdktest pin is the point of the change**, not an afterthought:
`sdktest/backend_test.go` builds a `fakeBackend` from scratch in the separate
`sdktest` module using only types exported from `client` (`Ref`, `Spec`,
`State`, `StateEvent`, `ResumeOptions`, `PortSpec`, `Forwards`,
`ReaperOptions`), pins `var _ client.Backend = (*fakeBackend)(nil)`, and a
behavioral test (`TestDialRunnerRoutesByName`) proves the routing contract: the
fake's `PortForward` returns a `Forwards` with the `PortRunner` handle pointed
at a real `httptest` server and a decoy `PortSSH` handle (different name,
different port) pointed at a server that always 500s; `client.DialRunner` +
`RunnerClient.Health` must reach the real server regardless of map iteration
order. `client.Backend`'s doc comment and `sdktest/surface_test.go`'s header
caveat were rewritten — the interface is implementable outside the module now,
intended for faking the cluster in a consumer's own tests.

## Mid-session credential-plugin handover (2026-07-27, §8 `[P1-0a]`)

Mirrored the existing subscription-login `tea.Exec` handover
(`account_picker.go`'s `startSubscriptionLogin`/`accountLoginDoneMsg`, wired in
`app.go`) for the Teleport gap: an interactive kubeconfig `exec:` plugin
(`tsh kube credentials`) authenticates fine at CLI startup but the dashboard
holds the terminal in alt-screen raw mode for its whole live-cluster-call
lifetime, so a cert TTL shorter than a left-open session makes the plugin
prompt underneath the TUI and silently fail or corrupt the display.

New seam in `internal/tui/dashboard/credrefresh.go`: a `CredentialRefresher`
interface (`NeedsRefresh(err) bool`, `Refresh() (tea.ExecCommand, func() error)`)
injected via `RunOptions.CredentialRefresher` (nil disables it — behavior is
unchanged today). `App.Update` now runs a top-of-function check ahead of normal
dispatch — `dispatch` is `Update`'s old body, split out so the check can batch
its `tea.Exec` handover command alongside the message's ordinary handling
instead of replacing it (a refresh failure still surfaces the original error).
A `credRefreshing` bool latches against an error storm (`List`+`Watch`+an
action all failing at once) spawning concurrent handovers. `credErrorFrom`
extracts the error from `seedFailedMsg`/`actionResultMsg`/`attachFailedMsg`/
`connectUpdateMsg`. On success, `credRefreshDoneMsg` re-drives the cluster via
the same `seedCmd`+`startWatchCmd` pair the dashboard's own `r` retry key uses;
on failure it's surfaced through the pre-existing `connectErr` detail-pane
field (a session-scoped toast didn't fit a cluster-wide failure).

Read bubbletea v2's `exec.go` to answer the stdout question up front:
`Program.exec` calls `SetStdout` unconditionally, but the stock
`osExecCommand.SetStdout` only fills it when `Cmd.Stdout == nil` — so a
pre-set `Stdout` survives the handover. `wrapExecCommand` (the adapter that
lets a plain `*exec.Cmd` satisfy `tea.ExecCommand`) is unexported, so
`internal/cli/credrefresh.go`'s `newKubeExecRefresher` (resolving the exec
plugin the same way `doctor.go`'s `loadAmbientKubeconfig` does — default
loading rules, current context, `AuthInfo.Exec`) ships a small `kubeExecCmd`
wrapper reproducing that exact fill-if-nil semantics, and `Refresh` sets
`Stdout = io.Discard` before returning it — the plugin's `ExecCredential` JSON
blob never paints the terminal during the handover. `NeedsRefresh` matches a
named, commented `credExpiryMarkers` list (case-insensitive, full error chain)
covering both client-go's own exec-plugin wrapper phrasing and API-server 401
signatures. Wired into all three `dashboard.RunOptions{}` sites
(`root.go`, `claude_remote.go`, `commands.go`).

**Review addendum — the latch was not enough (guard added, `[D11]`).** The
in-flight `credRefreshing` latch stops CONCURRENT handovers but says nothing
about SEQUENTIAL ones, and the sequential loop needs only a plugin that exits
**zero** while leaving the credential still bad — logged into the wrong cluster,
or an SSO session for the wrong role. Then: handover "succeeds" →
`handleCredRefreshDone` re-seeds → the seed fails identically → hand the terminal
over again, forever, with the dashboard suspended each time. A *cancelled* login
was already safe (non-zero exit ⇒ surface the error, don't re-seed); this is the
other half.

Added `maxCredRefreshAttempts = 2`, a budget of consecutive handovers.
`credErrorFrom` grew a second return distinguishing "this message reports a
cluster OUTCOME" from "this message says nothing about the cluster", because the
re-arm rule is the subtle part: the budget resets **only on an observed cluster
success** (an action that succeeded, a connect that completed), never on a
successful handover. "The plugin ran" is not evidence that the credential now
works, and counting it would re-open the exact loop the budget closes. A connect
*progress tick* likewise does not re-arm — only a `ready` one does.

Pinned by three tests, each verified to fail against a mutation: deleting the
budget check makes the thrash test report 5 handovers across 5 failing rounds
against a cap of 2, and treating every message as a cluster outcome additionally
breaks the progress-tick test. Bumping the constant alone does NOT fail the
suite — the tests reference `maxCredRefreshAttempts` symbolically, so they pin
that a cap is ENFORCED and re-armed correctly, not what its value is. That is
deliberate (the value is a tuning knob) but worth knowing before trusting the
tests to catch a change to it.

## Session titles + typed sync status promoted to the public SDK (2026-07-27, §8 "Remaining client-level capability gaps")

Two things the shipped CLI could do that an importer could not — which
`docs/design-principles.md` calls the bug, not the gap.

**Titles: the local index won, and the argument is suspend.** The item left the
shape open — promote `internal/index`, or add a title field on the runner. The
runner shape loses on one fact: a title must be settable for a **suspended**
session, whose runner is not running, and parking a session is exactly when you
rename it. `sandbox rename` works with no cluster today and had to keep working.
Everything else pointed the same way — `client.SessionMatch` already exposed
`Title` on the READ side (`client/resolve.go`), so promoting the write side was
the consistent and far smaller change, where a runner field needs a route, a
protocol bump, and a wire break. The accepted cost is that titles are
per-install rather than cross-machine; the session ID is the identity that
travels, and that trade now lives in the `client.Title` doc comment rather than
being discovered at integration time.

`client.Title{Name, Auto}` + `Display()` (Name wins, else Auto) +
`Client.Title/SetTitle/SetAutoTitle`, no `context.Context` (local-index ops,
matching `RemoveLocalState`). `SetTitle` trims and rejects blank with
`ErrEmptyTitle`, so a user-chosen label can't be silently cleared. Writes keep
the `[V7]` partial-entry discipline — only the identity fields plus the one
field the call owns, letting `index.Save`'s locked merge fill the rest, because
loading a full entry and writing it back races a concurrent snapshot writer's
newer `LastEventSeq`.

**Sync status: the raw-bytes method was dead, so it was deleted, not
supplemented.** `Client.SyncStatus` returned `[]byte` of mutagen CLI output and
had zero callers repo-wide. Publishing a raw accessor is a promise to keep
mutagen's output shape stable forever, so no `SyncStatusRaw` was retained. The
typed replacement returns `SyncStatus{State, Conflicts, Hint, Detail}` —
`Detail` being the part every caller was re-deriving: an errored probe's shaped
reason, or the definite "no sync session — attach to create one" that is an
*answer*, not a failure to answer. `probeErrDetail` moved from `internal/cli`
into `internal/sync` where the client can reach it. `Conflict.Alpha/Beta` became
`Local/Remote`: alpha/beta is mutagen's vocabulary, and principle 3's spirit —
don't make a consumer learn the implementation — applies to field names too.

**The orphan GC needed its POLICY promoted, not its predicate.** The item said
the classification was stuck in `internal/sync`, but `IsOrphanStatus` was only
half of it: the policy using it lived in `internal/cli`, carrying four
separately hard-won guards — MF3 (skip another kube context's syncs), `[V28]`
(skip another namespace's), `[V35]` (a paused sync of a deleted session is
otherwise immortal), and the refusal to run at all when the cluster can't be
listed, since during an outage every sync looks orphaned and an empty live set
would nuke them all. Exporting the predicate alone would have invited every
consumer to re-derive that policy and get one of the four wrong. `Client.SyncGC`
carries it, comments intact.

**The CLI became the consumer it was supposed to be.** `indexTitleStore`
delegates to a `sync.Once`-cached offline client; `syncProber` calls
`Client.SyncStatus` and keeps only the presentation cap (`+N more`);
`sandbox sync gc` calls `Client.SyncGC` with byte-identical output. The private
copies of `selectOrphanSyncs`/`syncGCCore`/`gcResult`/`clusterLister`/
`probeErrDetail` are gone.

**Pinned** in `sdktest/session_state_test.go`: struct-shape pins (which fail on
an added, renamed, or retyped field, not just a dropped one) for `Title`,
`SyncStatus` and `SyncConflict`, method pins for all six new `Client` methods,
and `TestOfflineTitleRoundTrip` — an external module renaming a session through
`client.Offline` with no cluster whatsoever, which is the pin that proves the
importer can do what `sandbox rename` does.

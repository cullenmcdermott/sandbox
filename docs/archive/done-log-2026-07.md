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

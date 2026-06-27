# TUI chat fixes — work-in-progress plan & resume note

Branch: `perf/transcript-load-typing-reconnect`. Captured 2026-06-23.

Nine reported TUI/dashboard issues, fully diagnosed (root cause + file:line +
fix). **No code edits applied yet.** This file is the resume point — read it,
then implement issues 1–9 (image push is partly done). Run `just check` at the end.

## Decisions already made (do not re-ask)

- **Scrollbar (#1):** enable mouse drag (accept loss of terminal-native
  click-drag text selection; shift+drag still works).
- **/vim (#2):** vim modal editing **off by default**; `/vim` slash command
  toggles it on/off.
- **Runner image (#6):** rebuild + push now (build already done; push pending).
- **Opaque bg (#9):** force unconditionally (no env opt-out).

## Status snapshot — ✅ ALL DONE (2026-06-23)

- Diagnosis: ✅ all 9.
- Runner image (#6): ✅ pushed to zot (`@sha256:88d8cefe…`) and **verified running**
  in the live pod (the new runner resume code marker is present in
  `/app/dist/claude.js`). Gotcha found: traefik caches the `:latest` *manifest*,
  so the node served the stale `7030eeac…` digest on a plain `:latest` re-pull
  (a 61 ms "pull" of a 525 MB image = node cache hit). Fix that worked: pin the
  **digest** in the Sandbox podTemplate (a digest pull uses a different URL that
  bypasses the tag cache). Note: the kubelet `imageID` field still reports the
  first-recorded RepoDigest (`7030eeac…`) for the deduplicated content — verify
  by the code marker, NOT imageID. **Durable follow-up:** new CLI-created
  sessions use `:latest` and will hit the stale traefik cache again; either bust
  traefik's manifest cache or have the CLI pin digests.
- Code: ✅ all 8 implemented with tests; `just check`-equivalent gates green
  (gen drift, gofmt, build, vet, `go test ./...`, runner test+typecheck, e2e,
  verify.sh race-twice). Two fixes went beyond the original diagnosis:
  - **#1 drift** had a *second* cause: the streaming renderer trims one trailing
    newline (`TrimSuffix`) while the finalized block trims all (`TrimRight`), so
    glamour's trailing blank line survived as an empty gutter row that vanished
    at completion (a 1-row lurch on top of the width mismatch). Both fixed.
  - **#4** is rendered as an opaque `kit.Card` (`withBackground`) over the dimmed
    preview, mirroring the chat modal's border+shadow.
- Adversarial review (8 reviewers + verifiers) confirmed **one** real bug, now
  fixed: the #5 reconcile could prune a *live* session because `k8s.Backend.List`
  silently dropped any Sandbox whose per-item `Status` Get errored (`continue`),
  so a transient API error / list-deadline truncation made a live session absent
  from a `nil`-error snapshot → reconcile pruned it after two strikes. Root fix:
  `List` now builds each state straight from the bulk-list objects via the new
  `statusFromSandbox` (no per-item Get that can fail and drop an existing CR).
  Regression test: `TestListSurvivesPerItemGetFailure`. All other areas clean.

---

## 1. Scrollbar drift + lag + mouse drag  (medium)  — DECISION: enable mouse drag

- **Bottom-drift root cause:** streaming tail wraps markdown at `width-gutterInset`
  (`transcript_list.go:86`) but the finalized block wraps 2 cols narrower at
  `width-2-gutterInset` (`transcript.go:876`); glamour wraps on
  `WithWordWrap(width)` (`chat/markdown.go:40`), so at `message.completed` the
  block gains extra wrapped lines and content lurches up off the bottom.
  **Fix:** unify the two wrap widths (ideally one shared helper). Add regression
  test: streamed-tail height == finalized-block height at a wrapping width.
- **Lag root cause:** no event coalescing — `waitForEvent` (`transcript.go:2347`)
  returns one event per receive; every `EventMessageDelta` (`transcript.go:2046`)
  is a full Update+View; keystrokes (`scrollKey` `transcript.go:1597`) queue
  behind deltas. `bodyView` calls `TotalHeight()` O(N) every render
  (`transcript_list.go:290`). **Fix:** after the blocking receive in
  `waitForEvent`, non-blockingly drain buffered events into ONE batch msg;
  `handleEvent` applies them in a loop with a single `streamDelta`/`GotoBottom`.
- **Mouse drag:** `MouseMode` never set — `tea.NewProgram` has no mouse option
  (`app.go:284`), `MouseWheelMsg` (`transcript.go:629`) + `MouseMsg` routing
  (`app.go:669`) are dead. **Fix:** set `v.MouseMode = tea.MouseModeCellMotion`
  on transcript/app views; hit-test drag against the scrollbar column (rightmost
  reserved col from `bodyView`), map drag-Y via inverse of `kit.Scrollbar` pos
  math → `m.body.ScrollBy/GotoBottom`.
- Files: `transcript_list.go`, `transcript.go`, `app.go`.

## 2. /vim toggle  (small)  — DECISION: off by default

- State already exists: `inputMode` `modeNormal`/`modeInsert` (`modes.go:14-19`),
  `enterInsert`/`enterNormal` (`modes.go:24-37`), `normalKey` (`modes.go:53-85`),
  `modeBadge` (`modes.go:88-94`) drawn at `transcript.go:1250-1255`.
- **Plan:** add `vimEnabled bool` (default false). When off, force `modeInsert`
  always — `normalKey` never engaged, prompt always focused, esc keeps its
  interrupt/steer meaning. `/vim` toggles it; when on, modal NORMAL/INSERT active
  + `modeBadge` shown. Add `/vim` to `commandGroups` (`commands.go`, Mode group).
  Add help entry (`help.go:163-171`). Tests in `modes_test.go`.
- **GOTCHA:** `ctrl+g` is already the toast "jump to next attention" key
  (`notify.go`). Do NOT bind vim toggle to ctrl+g — use the `/vim` command (the
  decision). Watch golden snapshots if the hint row changes.

## 3. Toast broken + OSC9  (small)

- **Root cause:** `renderToast` is composited ONLY in `renderZoned`
  (`zones.go:162`) = dashboard screen. The chat modal (`ScreenTranscript` via
  `App.modalView` `app.go:798`) never composites it — exactly when it should fire.
  Modal also covers Y=2 and `dimBackdrop` strips colors. Trigger is correctly
  wired (`notifyIfBackgroundAttention` after `RunnerEventMsg` `model.go:705`,
  dedup `notify.go:72`); 8s auto-clear via `toastTickMsg` (`model.go:605`) works
  on the bare dashboard. So symptom = invisible-in-chat, not never-clears.
- **Fix:** composite the toast over every screen — move toast compositing into
  `App.View`/`withTerminalSignals` (high-Z at Y=0/1). Files: `app.go`, `zones.go`.
- **OSC:** `OSCProgress`=OSC 9;4, `OSCNotify`=OSC 777 (`osc.go:38,55`), emitted
  once/frame in `withTerminalSignals` (`app.go:764`), gated
  `caps.IsGhostty && !ReduceMotion` (`model.go:2217,722`), skips ScreenExternal.
  User is on Ghostty → should emit; verify. Optionally relax the gate.

## 4. Reconnect — show "Reconnecting…" modal immediately  (small)

- **Root cause (intentional "Fix A"):** `attachMsg` (`app.go:392-403`) sets
  `ScreenConnecting` + builds `connectingPreview` (`app.go:1016-1037`) for any
  warm/cached session; `connectingView` (`app.go:1058-1064`) then renders the full
  `previewView` chat + a thin `connectingBanner` (`app.go:1042-1051`) instead of
  the centered stepper modal (`app.go:1066-1110`, used only when no preview). Doc:
  TODO.md:21-22, `app.go:127-132`.
- **Fix (Option 1):** render the preview DIMMED as a backdrop (reuse `dimBackdrop`
  `app.go:851-885`) and overlay the centered stepper modal titled **"Reconnecting…"**
  from frame 1. Stage copy already in `connectStageLabel` (`states.go:53`). Update
  `connecting_preview_test.go` (`TestConnectingPreviewShowsCachedHistory:13`).
- Files: `app.go`, `connecting_preview_test.go`.

## 5. Stale sessions don't disappear  (small)

- **Root cause:** `m.sessions` seeded once via `seedCmd`→`backend.List`
  (`model.go:780-791`, `applySeed` `model.go:1184-1275`); thereafter only mutated
  by watch `PodEventMsg` (`applyPodEvent` `model.go:1297-1360`). No periodic
  re-list. Informer misses deletes that happened before its cache sync; `Init`
  starts seed+watch in parallel, violating the `watch.go:81-82` contract.
  `syncPollTickMsg` (`model.go:636-647`) only probes warm sessions.
- **Fix:** add `reconcileTickMsg` + `reconcileTickCmd` (~30s) in `Init`/`Update`.
  Re-list backend in a Cmd that **returns a msg** carrying the listed states
  (do NOT mutate `m.sessions` inside the goroutine — data race); reconcile in
  `Update`: drop sessions not in the cluster set (`cancelLiveSSE` + `dropRetained`
  + remove from `m.sessions` + `sortSessions` + `clampCursor`). Don't re-add (watch
  handles adds). Files: `model.go`.

## 6. Runner image — rebuild + push  (IN PROGRESS)

- Default `registry.cullen.rocks/sandbox-claude-runner:latest` (`claude_remote.go:21`).
  CI pushes to GHCR, not zot (`build-runner-image.yml:20`); zot is hand-populated.
  Working tree has uncommitted runner resume fixes (`runner/src/claude.ts` +197)
  that are in NO built image.
- **DONE:** built locally linux/amd64 (`docker buildx build --platform linux/amd64
  -t registry.cullen.rocks/sandbox-claude-runner:latest --load runner/`), exit 0.
- Running pod `claude-sdk-df80e6-c8ee320c` imageID
  `sha256:7030eeac52ef584d58d49a12b47aed2f2aa4201c6ab16c89270d362ca6acd734`
  (== current pushed zot `:latest`).
- zot service `zot/zot` ClusterIP `10.107.84.222:5000`, **no auth**.
- **TODO push steps:**
  1. `docker push registry.cullen.rocks/sandbox-claude-runner:latest`.
  2. If traefik `499`s on the big (~153MB) layer (TODO.md:114-116 history):
     `kubectl -n zot port-forward svc/zot 5001:5000`, then retag
     `127.0.0.1:5001/sandbox-claude-runner:latest` + `docker push` (or
     `skopeo copy --dest-tls-verify=false`).
  3. Recreate pod to re-pull: `kubectl -n agent-sessions delete pod <pod>`
     (`:latest` ⇒ imagePullPolicy Always).
  4. Verify new pod's imageID digest != `7030eeac…`.
- Optional hardening: pin a digest/sha tag instead of `:latest`; add GHCR→zot
  mirror (skopeo) step to CI.

## 7. Mascot shear on connect splash  (small)

- **Root cause:** it's NOT kitty graphics — it's the Unicode block sprite
  `theme.ClaudeMascot()`/`OpenCodeMascot()` (`brand.go:76-85`). Lines have unequal
  display widths (Claude 8/9/7, OC 7/5/7); `gradientBlock` (`brand.go:100-106`)
  doesn't pad them; `JoinVertical(Center)` (`app.go:1105`) + `lipgloss.Place(Center)`
  (`app.go:1106`) pad each line independently → shear. Comment `brand.go:72` is false.
- **Fix:** in `gradientBlock`, pad each RAW line to the max display width
  (`lipgloss.Width`) before `GradientText`; optionally center the sprite as one
  block (`PlaceHorizontal`). Fix the misleading comments (`brand.go:72`,
  `app.go:1100-1103`). Files: `brand.go`, `app.go`.

## 8. /opus statusline lag  (small)

- **Root cause:** `setModelCmd` (`commands.go:106`) only sets `m.modelOverride`,
  not `m.model`. Statusline renders `shortModelName(m.model)` (`statusline.go:379`);
  `m.model` is written only in the `EventSessionStarted` handler
  (`transcript.go:1934-1938`), which fires when a turn starts. `modelOverride` is
  intentionally decoupled to avoid flicker (`transcript.go:231`).
- **Fix:** in `setModelCmd` also optimistically set `m.model` + `m.ctxLimit` from
  the chosen alias (`models.Limit`). For `/model-default` (empty id) capture the
  initial default in a new `defaultModel` field (set in the `EventSessionStarted`
  handler) and restore it. Self-heals to the SDK-resolved id on next
  `session.started`. Verify `models.Limit("opus")` returns a sane window.
  Files: `commands.go`, `transcript.go`.

## 9. Force opaque background  (small)  — DECISION: unconditional

- **Root cause:** app never sets `tea.View.BackgroundColor` (`app.go:728`,
  `model.go:756`, `transcript.go:642`) → unpainted cells show the (transparent)
  terminal bg. `connectingView` `Place` (`app.go:1106`) has no whitespace bg;
  `previewView` (`transcript.go:695-714`) isn't padded to w×h; help/confirm
  overlays use `WithWhitespaceChars(" ")` with no bg (`model.go:1730-1744`,
  `transcript.go:649-651`, `states.go:88`). Dashboard body already opaque via
  `clampLines`/`withBackground` (`zones.go:33-48`); modal already opaque (d8d3b00).
- **Fix:** set `v.BackgroundColor = theme.Page` globally in `App.View` (exclude
  `ScreenExternal` — `withTerminalSignals` already early-returns for it). Replace
  `WithWhitespaceChars(" ")` with `WithWhitespaceStyle(lipgloss.NewStyle().Background(theme.Page))`
  on splash/overlays; pad `previewView` to w×h (`clampLines`/`withBackground`).
  **lipgloss v2 note:** `WithWhitespaceBackground` was removed → use
  `WithWhitespaceStyle` (`whitespace.go:65`). Files: `app.go`, `transcript.go`,
  `model.go`, `states.go`.

---

## Finish

Run `just check` (gen drift, gofmt, lint, build, vet, Go+runner tests, typecheck).
In-sandbox caveat: run `just test`/`just verify` with the command-sandbox disabled
(httptest ports). Regenerate goldens (`testdata/TestGolden*.golden`) if hint
rows / overlays change rendering. Then an adversarial verification review.

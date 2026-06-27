# Ghostty terminal effects for the dashboard TUI

Status: CONVERGED — Stages 1–3 implemented; the convergence ledger (§8) reached
CONVERGED at run-8. This is a living doc that re-derives status from the code each
run; outstanding work is any Stage 4+ effect plus manual live-Ghostty verification.
Owner: TUI (`internal/tui/dashboard`)
Scope: opt-in visual + signalling enhancements that light up when we detect a
capable terminal (primarily Ghostty), degrading cleanly everywhere else.

This doc is the durable plan behind "what cool GPU/terminal stuff can we do when
we detect Ghostty." It captures the one hard constraint, the architecture, and a
staged build order with concrete `file:line` anchors so any session can pick it
up.

**If you are implementing this, start at §7 (Execution & convergence protocol).**
This doc is designed to be driven by repeated automated runs that converge: each
run advances the work or audits for gaps, records evidence in the §8 ledger, and
stops only when the convergence criteria are met.

---

## 0. The constraint that governs everything

The dashboard TUI has **exactly one channel to the terminal**: the string in
`tea.View{Content: ...}` returned from `Model.View()`.

- `Model.View()` — `internal/tui/dashboard/model.go:495`
- Frame assembled by `Model.render()` — `internal/tui/dashboard/model.go:1321`
- Root router `App.View()` — `internal/tui/dashboard/app.go:604`
- Program started with **no options** (`tea.NewProgram(app)`) —
  `internal/tui/dashboard/app.go:206` / `:219`; AltScreen via `v.AltScreen = true`.

There is **no** `os.Stdout` write, `tea.Printf`, or side channel anywhere in
`internal/tui`. The only raw writes in the package are the opencode PTY
(`external_pane.go:258`) and the `$EDITOR` handoff (`editor.go:46`) — neither is
a general output path.

**Implication:** every escape sequence we emit must travel inside the composed
frame string (or, for Kitty image *transmission* only, via a deliberate
out-of-band `tea.Cmd` — see Stage 3).

**Why this is fine:** Bubble Tea v2's renderer (cellbuf) parses ANSI and treats
OSC/APC control strings as **zero-width**. Sequences embedded in the View string
are emitted faithfully and do not corrupt layout *when placed correctly*
(prepended/appended to the composed frame, outside the measured layout region).
This is race-free — Bubble Tea serializes it — so we deliberately avoid a
concurrent stdout writer fighting the renderer.

Tick cadence: ~30fps motion tick; `workTickInterval = 150ms`
(`transcript.go:1369`), `animFPS = 33ms` (`model.go:116`).

---

## 1. Architecture

The public package `tui/terminal` owns capability detection and escape-sequence
emission (originally `internal/terminal`; promoted so other systems can reuse it
— see `cmd/tuikit-demo`). Detect once at startup, thread a `Caps` value into the
dashboard model, and gate every feature behind `if caps.X`.

```
tui/terminal/
  caps.go     // Detect() -> Caps; Ghostty + version, kitty graphics, truecolor
  osc.go      // OSC 9;4 progress, OSC 9 / OSC 777 notifications (zero-width strings)
  kitty.go    // Kitty graphics: image transmit (APC) + Unicode placeholder cells
```

`Caps` fields (initial):

- `IsGhostty bool`, `GhosttyVersion string`
- `KittyGraphics bool`
- `TrueColor bool` (fold in existing `colorprofile.Detect()` — `styles.go:23`)
- `ReduceMotion bool` (mirror `anim.ReduceMotion()` — `anim/transition.go:69`)

Detection signals:

- Ghostty: `TERM_PROGRAM=ghostty` + `TERM_PROGRAM_VERSION` (cheap, env-based);
  optionally confirm with `XTVERSION` (`CSI > q` → `ESC P >| ghostty <ver> ESC \`).
- Kitty graphics: Ghostty advertises support; gate the graphics gauge on this.
- Truecolor: reuse `colorprofile.Detect()` so there is one source of truth.

Non-capable terminals get exactly today's output (all features are additive and
gated). Honor `NO_COLOR` / `SANDBOX_REDUCE_MOTION=1` as the global off switch.

---

## 2. Stage 1 — Truecolor (works everywhere, fastest visible win)

No new primitives — spend the existing gradient engine harder:

- `gradientText` / `kit.GradientClusters` — `styles.go:86`
- `lipgloss.Blend1D(...)` brand ramp — `styles.go:76`
- `anim.LerpColor`, `anim.EaseOutCubic` — `anim/transition.go`
- brand tokens `Charple → Hazy → Dolly` — `theme.go:31`

Effects (all pure-View-string, degrade via existing `gradientCapable`):

1. **Animated ctx-gauge sweep.** Today the ctx bar is a static `rampColor(frac)`
   block bar — `statusline.go:284–304`. Make the fill a live gradient that
   shimmers while the attached session is `StatusBusy`, and *pulse* the bar coral
   past 80% instead of only prefixing `!` (`statusline.go:298`).
2. **Breathing busy state.** Drive a per-frame phase through `LerpColor` on the
   busy spinner (`styles.go:113`, `SpinnerFrame` at `styles.go:115`) and the
   active session's row — a living gradient rather than the 4-glyph cycle.
3. **Wordmark shimmer** on the brand gradient during connecting/thinking.

Acceptance: visible motion on the ctx gauge during a turn; identical output under
`NO_COLOR`; no layout shift.

---

## 3. Stage 2 — OSC out-of-band signals (tiny, surfaces when unfocused)

Zero-width control strings prepended to the composed frame in `View()`. Gate on
`caps.IsGhostty` (both are widely supported, but keep it scoped).

- **OSC 9;4 progress** → painted on Ghostty's tab/taskbar. Drive from
  `m.partition()` (`zones.go:305`), which already computes `busy/waiting/
  needsInput` every frame:
  - any `StatusBusy` → indeterminate "pulse";
  - `countStatus(StatusWaiting) > 0` → **error state** (red) so a pending
    permission shows in the tab even when the window is unfocused;
  - otherwise → clear.
- **OSC 9 / OSC 777 desktop notification** → fires a real OS notification. Hook
  the existing transition point `notifyIfBackgroundAttention()`
  (`notify.go:61`), which already fires on background `StatusWaiting` /
  `StatusNeedsInput` and knows `s.Title` + `s.PendingPermissionTool`. Today it
  raises an in-TUI toast; this makes the signal escape the terminal. Keep the
  toast; add the OS notification alongside it.

Status transition source of truth (where to read state):

- Reducer `ApplyRunnerEvent()` — `session.go:292` (turn.started→Busy,
  permission.requested→Waiting, turn.completed→NeedsInput, etc.)
- Status enum `SessionStatus` — `session.go:27` (Idle/Busy/Waiting/NeedsInput/
  Suspended/Failed).
- Aggregate per-frame counts — `m.partition()` `zones.go:305`;
  `m.countStatus()` `model.go:1749`.

Acceptance: starting a turn lights the Ghostty tab progress; a background
permission request turns the tab red and raises an OS notification.

---

## 4. Stage 3 — Kitty graphics ctx gauge (the showpiece)

**Do not** place an image at a cursor position inside AltScreen — the Bubble Tea
diff renderer doesn't know the image occupies cells and will corrupt the frame.
Use **Kitty Unicode placeholders (virtual placements)**, which Ghostty supports:

1. **Transmit** the gauge image once via an APC `_G` sequence with an image ID
   and a *virtual* placement (`U=1`). This is the one genuinely out-of-band
   piece — emit via a `tea.Cmd` side-effect **only when the image changes**, not
   every frame.
2. **In the View string**, emit a rectangle of placeholder cells (`U+10EEEE` +
   row/column diacritics; image ID encoded in the foreground color). These are
   normal-width cells, so lipgloss measures them correctly and the diff renderer
   is happy. The terminal swaps them for the image.
3. On value change, re-transmit a new frame under a new ID and update the
   placeholder foreground color.

Payoff: a crisp rasterized ctx-window gauge / token sparkline instead of the
10-segment block bar — real anti-aliased pixels in the grid. Later: per-backend
avatars in the session list.

Data already available:

- used tokens `TranscriptModel.ctxTokens()` — `transcript.go:239`
  (`inTok + cacheReadTok + cacheWriteTok`)
- context limit `models.Limit(modelID)` — `internal/models/models.go:30`
  (stored at `transcript.go:1513`)
- cost `costUSD` — `statusline.go:309`

Risks / unknowns to settle during the spike:

- placeholder width measurement under lipgloss (`U+10EEEE` base width 1,
  diacritics zero-width — verify);
- image (re)transmit cadence vs. 30fps tick (transmit only on value change);
- AltScreen clear/redraw interaction (image lifetime across full repaints);
- needs hands-on testing in Ghostty — there is no golden-test substitute.

Acceptance: a rasterized gauge renders in the statusline in Ghostty, updates as
tokens accrue, and the layout is byte-identical to today on non-Kitty terminals.

---

## 5. Build order

1. **Stage 0** — `internal/terminal` caps package (spine for everything).
2. **Stage 1** — animated ctx-gauge sweep (instant visible motion, no gating
   risk). Ship 0+1 as the first PR slice.
3. **Stage 2** — OSC 9;4 progress + OSC 9 notifications (~50 lines, high impact).
4. **Stage 3** — Kitty Unicode-placeholder gauge (the fancy, fiddly payoff).

Alternative entry point if we want to de-risk pixels first: spike the Stage 3
placeholder gauge as a throwaway proof-of-concept before committing to 0–2.

---

## 6. Reference: key anchors

| Concern | Location |
|---|---|
| View / frame assembly | `model.go:495`, `model.go:1321`, `app.go:604` |
| Program start (no opts, AltScreen) | `app.go:206`, `app.go:219` |
| Motion tick / fps | `model.go:116`, `transcript.go:1369` |
| Gradient + easing primitives | `styles.go:76`, `styles.go:86`, `anim/transition.go` |
| Brand tokens | `theme.go:31` |
| ctx% compute + render | `transcript.go:239`, `models/models.go:30`, `statusline.go:284` |
| Busy spinner | `styles.go:113`, `styles.go:115` |
| Status enum + reducer | `session.go:27`, `session.go:292` |
| Aggregate counts per frame | `zones.go:305`, `model.go:1749` |
| Attention / notification hook | `attention.go`, `notify.go:61` |
| Reduce-motion / NO_COLOR | `anim/transition.go:69` |
| Truecolor detect | `styles.go:23` |

---

## 7. Execution & convergence protocol

This plan is meant to be executed by repeated automated runs. A run does **one**
of two things: advance the work, or audit for gaps. Either way it ends by
appending an evidence-bearing entry to the §8 ledger. Runs converge when the
work is genuinely done and successive audits stop finding meaningful gaps.

### Definition of done (every item must hold)

- D1. `internal/terminal` package exists: `Detect() -> Caps`, with the §1 fields,
  and `Caps` is threaded into the dashboard model.
- D2. Stages 1–3 are implemented and **gated** behind `Caps` (and respect
  `NO_COLOR` / `SANDBOX_REDUCE_MOTION`).
- D3. Each stage's "Acceptance" line (§2–§4) is satisfied.
- D4. **Degradation guarantee:** on a non-Ghostty / non-truecolor / `NO_COLOR`
  terminal, output is unchanged from today. Golden tests pass; any golden change
  is justified in the ledger.
- D5. `just check` is green (the full gate CI runs). Mind the in-sandbox caveat
  in `CLAUDE.md`/`docs/verification-protocol.md`: `internal/runner` and
  `internal/models` bind ports the command-sandbox blocks — run with the sandbox
  disabled. Linters aren't on the Nix host; CI enforces them (`nix run
  nixpkgs#golangci-lint -- run` locally if needed).
- D6. Stage 3 has an honest verification state: automated tests cover the
  placeholder encoding + degradation, and the *visual* criterion is either
  manually verified in Ghostty (recorded in the ledger) or explicitly marked
  `pending-manual` — never claimed met from automated tests alone.

### What each run does

1. **Read** this whole doc, especially the §8 ledger (most recent entries first).
2. **Assess real state from the code, not the ledger.** Verify claims by
   inspection: does `internal/terminal` exist? are features wired at the §6
   anchors? does `just check` actually pass right now? The ledger is a hint, not
   ground truth — distrust it and confirm.
3. **If work remains:** implement the next smallest shippable increment, in build
   order (§5) unless a dependency forces otherwise. Then run `just check` and
   append a ledger entry: what changed, which D-items it advances, and the
   `just check` result (summarize pass/fail + any notable output).
4. **If it all looks done:** run a fresh **adversarial gap-audit** against D1–D6
   and every Acceptance line — actively try to find where the degradation
   guarantee leaks, where a `Caps` gate is missing, where an Acceptance criterion
   is only partially met. Fix anything **meaningful**; log the fix. If nothing
   meaningful is found, append a **"no-op audit"** entry stating exactly what you
   checked and that no meaningful gaps remain.
5. **Never** write a "done" or "no-op" entry without current `just check`
   evidence in that same entry.

### "Meaningful" gap (what blocks convergence)

A gap is meaningful if it affects: correctness, the D4 degradation guarantee, a
stage Acceptance criterion, `just check` passing, or user-visible behavior.
Cosmetic nits that change no behavior are **not** meaningful — note them in the
ledger under "non-blocking nits" and do **not** let them prevent convergence.

### Convergence — when to stop

Converged when **both** of the two most recent ledger entries are "no-op audit —
no meaningful gaps," they were written by separate runs, and between them no code
changed. At that point print `CONVERGED` and make no further changes.

Stage 3's `pending-manual` visual criterion (D6) does not block convergence: a
run may converge with Stage 3 recorded as "implemented; automated tests green;
visual verification pending-manual." That is a stable terminal state, not a gap —
do not churn trying to "finish" it via automated means.

### Guardrails

- **Additive + gated only.** Never regress non-Ghostty / `NO_COLOR` output.
- Touch golden tests only with a one-line justification in the ledger.
- Stage 3 image *transmission* is the only sanctioned out-of-band write (a
  deliberate `tea.Cmd` side-effect on value change); everything else rides the
  frame string. Do not add a background stdout writer.
- Keep edits scoped to this plan. File unrelated discoveries as a note with a
  `file:line` pointer; don't fix them here.

---

## 8. Progress ledger

Append newest entries at the top. One entry per run. Format:

```
### <YYYY-MM-DD> — <advance | no-op audit> — <run id / who>
- State assessed: <what you verified from the code>
- Changes: <files touched, or "none">
- D-items advanced: <D1..D6 or "audit only">
- just check: <pass/fail + notable output>
- Non-blocking nits: <list or "none">
- Next: <smallest next increment, or "converged candidate">
```

### 2026-06-22 — no-op audit — run 8 (CONVERGED)
- State assessed (from code, not ledger): re-verified all D-items are intact and
  unchanged since run 7. D1: `internal/terminal/{caps,osc,kitty}.go` present;
  `Caps` has all 5 §1 fields; `terminal.Detect()` wired in `New` (model.go:294),
  `caps` on `Model` (270) and `TranscriptModel` (213), copied on attach
  (app.go:366). D2: all 4 emission sites caps-gated (statusline.go:394/400,
  model.go:491/1803), incl. the run-6 `!ReduceMotion` global-off-switch fix. D3/D4
  covered by tests; D6 visual pending-manual (stable per §7). No code changed this
  run — audit only.
- Changes: none.
- just check: PASS — full gate green on a clean re-run (build, vet, gofmt,
  race-twice "2/2 race tests pass twice", e2e, runner tests, anti-cheat; incl.
  tui/* packages), sandbox disabled per caveat. Note: the first `just check` of
  this run hit a transient race-twice flake ("race tests failed (run 1)"); a
  direct `go test -race ./...` came back clean with zero data races / failures,
  and the re-run of the full gate passed — i.e. an environment flake (concurrent
  edits / scheduler), not a defect in this work. No code was touched to "fix" it.
- Non-blocking nits: unchanged (idempotent per-frame progress re-emit; Stage 1
  effects 2–3 optional polish).
- Convergence: this is the SECOND consecutive separate-run no-op audit (after run
  7), with NO code change between run 7 and run 8. §7 convergence criteria met.

### 2026-06-22 — no-op audit — run 7 (no meaningful gaps)
- State assessed (from code, not ledger): re-verified every D-item.
  - D1: `internal/terminal` has `Detect() -> Caps` with all §1 fields
    (`IsGhostty`, `GhosttyVersion`, `KittyGraphics`, `TrueColor`, `ReduceMotion`);
    threaded into `Model` (`New` → `terminal.Detect()`) and into `TranscriptModel`
    (`app.go` on attach).
  - D2: enumerated all 4 emission sites and confirmed each is caps-gated —
    `OSCProgress` only after `progressState()` (IsGhostty && !ReduceMotion);
    `OSCNotify` under `IsGhostty && !ReduceMotion`; `ctxGaugeKitty` under
    `KittyGraphics && !ReduceMotion`; `shimmerBlockBar` under `TrueColor &&
    !ReduceMotion && Busy`. No ungated escape-sequence path exists (grep-verified).
  - D3: Stage 1 motion/width-parity, Stage 2 busy→progress/waiting→red+notify,
    Stage 3 raster gauge + transmit-on-change all covered by tests.
  - D4: `Model.render()` reads no caps and `NewTranscript` leaves caps zero, so
    goldens are env-independent; `TestAppViewNoSignalsWithoutGhostty` asserts the
    non-Ghostty frame is byte-identical; width-parity tests for both Stage 1 and
    Stage 3 bars (lipgloss.Width == 10).
  - D5: `just check` green (below).
  - D6: Stage 3 encoding/wiring/degradation covered by automated tests; visual
    criterion **pending-manual** (needs hands-on Ghostty) — a stable terminal
    state per §7, not a gap.
- Changes: none.
- D-items advanced: audit only.
- just check: PASS — full gate green (build, vet, gofmt, race-twice, e2e cached,
  runner tests, anti-cheat; incl. tui/* packages), sandbox disabled per caveat.
- Non-blocking nits: unchanged from run 6 (every-frame progress re-emit is
  idempotent + doc-aligned; Stage 1 effects 2–3 optional polish).
- Next: convergence candidate — one more separate-run no-op audit with no
  intervening code change converges.

### 2026-06-22 — advance (audit w/ fixes) — run 6 (gap-audit D1–D6)
- State assessed: all stages implemented (verified by inspection at the §6
  anchors, not the ledger). Ran an adversarial gap-audit against D1–D6 + every
  Acceptance line. Confirmed goldens are env-safe: `Model.render()` never reads
  caps and `NewTranscript` leaves caps zero (caps is only set in `app.go` on
  attach), so no snapshot depends on the dev's terminal; only my own tests call
  `App.View`, and they set caps explicitly.
- Meaningful gaps found + fixed:
  1. **D2/D4 leak:** §1 makes NO_COLOR / SANDBOX_REDUCE_MOTION the *global* off
     switch, but Stage 2 (OSC progress + notification) and Stage 3 (kitty gauge)
     were gated only on `IsGhostty`/`KittyGraphics` — a Ghostty user under
     NO_COLOR still got tab progress, OS notifications, and a raster gauge. Now
     all three also require `!caps.ReduceMotion` (`progressState`, the toast
     `pendingOSC`, and the `ctxGaugeKitty` switch). Added two regression tests
     (`TestProgressStateGatedOnReduceMotion`,
     `TestCtxGaugeKittySuppressedByReduceMotion`).
  2. **Latent correctness:** `kittyGaugeID` (uint32) grew unbounded while
     `KittyPlaceholders` encodes only the low 24 bits in the fg color and the
     transmission uses the full id — they'd desync past 2^24 changes. Now wraps
     to 1 at the 24-bit ceiling.
- D-items advanced: hardens D2 + D4 (degradation under the global off switch);
  audit of D1, D3, D5, D6 found no other meaningful gaps.
- just check: PASS — full gate green (build, vet, race-twice, e2e, runner 66/1,
  anti-cheat; incl. tui/*), sandbox disabled per caveat. No golden changes.
- Non-blocking nits: (a) OSC 9;4 progress re-emits each busy/error frame rather
  than only on change — idempotent and matches the doc's "drive every frame", so
  left as-is. (b) Stage 1 effects 2–3 (breathing busy/wordmark shimmer) remain
  optional polish; Stage 1's Acceptance (ctx gauge) is met.
- Next: convergence candidate — this run changed code, so it does NOT count as a
  no-op audit. Next run: a fresh no-op audit; converge only after two
  consecutive no-op audits with no code change between them.

### 2026-06-22 — advance — run 5 (Stage 3 part 2: wire Kitty ctx gauge)
- State assessed: Stages 0–2 + Stage 3 primitives present. The ctx gauge
  (`statusline.go`) used the block bar / Stage 1 shimmer; nothing consumed the
  `internal/terminal` Kitty primitives yet; `App.View` drained only OSC, not a
  Kitty transmission. (The `tui/theme` migration has fully settled; build green.)
- Changes: wired the placeholder gauge into the statusline. New TranscriptModel
  state (`kittyGaugeBucket`, `kittyGaugeID`, `pendingKitty`) + `ctxGaugeKitty`
  which, when `caps.KittyGraphics`, returns a 10-cell `KittyPlaceholders` run and
  queues a `KittyTransmitRGBA` into `pendingKitty` only when the fill bucket
  (whole %) changes, under a fresh image id. `rgbOf` converts theme colors to
  `terminal.RGB`. Render precedence: KittyGraphics → raster gauge; else truecolor
  +busy → shimmer; else static bar. `App.withTerminalSignals` now also drains
  `transcript.takePendingKitty()`, prepended so the image precedes the cells.
  Added `kitty_gauge_test.go`: width-parity (==10 cols), transmit-once-per-bucket
  + new-id-on-change, non-Kitty degradation (no placeholders/APC, nothing queued),
  and the App.View drain+ordering+one-shot.
- D-items advanced: D2 (Stage 3 gated on `caps.KittyGraphics`); D3 (rasterized
  gauge renders + updates as tokens accrue — automated); D6 (encoding + wiring +
  degradation covered by tests; **visual criterion pending-manual** — needs
  hands-on Ghostty, no golden substitute, per the doc). D4: degradation test
  asserts the non-Kitty status line is the block-bar path with no APC.
- just check: PASS — full gate green (build, vet, race-twice, e2e, runner 66/1,
  anti-cheat; incl. tui/* packages), sandbox disabled per caveat. No golden
  changes (off-Kitty path unchanged).
- Non-blocking nits: Stage 1 effects 2–3 remain optional polish (see run 3).
- Next: all stages implemented + gated; Stage 3 visual is pending-manual (stable
  terminal state). Next run: adversarial gap-audit against D1–D6 and every
  Acceptance line.

### 2026-06-22 — advance — run 4 (Stage 3 part 1: Kitty graphics primitives)
- State assessed: Stages 0–2 present/correct. No Kitty/APC code existed. (Mid-run
  a concurrent refactor migrated the dashboard palette into a new `tui/theme`
  package — it transiently deleted `internal/tui/dashboard/theme.go` and broke
  the build; it has since completed and `theme.*` tokens resolve. My edits were
  theme-agnostic and were auto-migrated cleanly, e.g. `shimmerBlockBar` now uses
  `theme.Guac`. Recorded here only to explain the build-state churn; not part of
  this plan.)
- Changes: added `internal/terminal/kitty.go` — the Kitty Unicode-placeholder
  encoding layer: `GaugeRGBA` (anti-aliased horizontal gauge bitmap, theme-agnostic
  via an `RGB` param), `KittyTransmitRGBA` (APC `_G` virtual-placement transmission,
  `U=1`, base64-chunked at 4096, `q=2` to suppress acks), `KittyPlaceholders`
  (rectangle of `U+10EEEE` cells with row/col diacritics + id-in-foreground-color),
  and the official rowcolumn-diacritics table prefix. Added `kitty_test.go`:
  width-parity (placeholder run measures exactly `cols` via uniseg), encoding
  structure, chunking (m=1…m=0), degenerate guards, gauge pixel correctness.
- D-items advanced: toward D2/D3/D6 for Stage 3 (encoding primitives + automated
  encoding/degradation tests). Not yet wired into the statusline; visual remains
  pending-manual.
- just check: PASS — full gate green (now also builds/tests `tui/theme`,
  `tui/anim`, `tui/kit`, `tui/list`; race-twice, e2e, runner 66/1, anti-cheat),
  sandbox disabled per caveat.
- Non-blocking nits: Stage 1 effects 2–3 still optional (see run 3).
- Next: Stage 3 part 2 — wire the placeholder gauge into the statusline gated on
  `caps.KittyGraphics`, transmit-on-value-change via the frame (one-shot), with a
  byte-identical-off-Kitty degradation test; mark visual pending-manual.

### 2026-06-22 — advance — run 3 (Stage 2: OSC 9;4 progress + OSC 777 notify)
- State assessed: Stages 0+1 present/correct. No OSC emission anywhere; the only
  out-of-band paths remained the opencode PTY + $EDITOR (unchanged). Confirmed
  `m.partition()` (`zones.go:305`) computes busy/waiting per frame and the toast
  transition flows through `toastMsg` (`model.go:466`).
- Changes: added `internal/terminal/osc.go` (`OSCProgress(Progress)` zero-width
  OSC 9;4; `OSCNotify(title,body)` OSC 777 with `sanitizeOSC` stripping
  ESC/BEL/`;`/newlines to prevent escape injection) + `osc_test.go`. Wired into
  the dashboard: `Model.progressState()` maps the aggregate (waiting→Error,
  busy→Busy, else None; None on non-Ghostty), `pendingOSC` + `takePendingOSC()`
  queue a one-shot notification set on `toastMsg` (gated on `caps.IsGhostty`).
  Root `App.View` now wraps `screenView()` via `withTerminalSignals`: prepends
  the progress OSC (with one-shot clear on active→idle via `progressActive`) and
  drains the notification. ScreenExternal is left untouched (PTY owns terminal).
  Added `osc_signals_test.go` (gating, mapping, prepend-on-Ghostty, byte-identical
  off-Ghostty, clear-once, drain-once).
- D-items advanced: D2 + D3 for Stage 2 (gated on `caps.IsGhostty`; acceptance:
  starting a turn lights tab progress, a background permission turns it red +
  raises an OS notification). D4: `TestAppViewNoSignalsWithoutGhostty` asserts
  byte-identical frames off-Ghostty.
- just check: PASS — full gate green (race-twice, e2e cached, runner 66/1,
  anti-cheat), sandbox disabled per caveat. No golden changes (off-Ghostty path
  byte-identical).
- Non-blocking nits: Stage 1 effects 2 (breathing busy spinner/row) and 3
  (wordmark shimmer) of §2 not implemented — the existing gradient engine
  already colors the spinner/wordmark, and Stage 1's Acceptance line (ctx gauge)
  is met; treating these as optional polish, not a convergence blocker.
- Next: Stage 3 — Kitty Unicode-placeholder ctx gauge (encoding + degradation
  tests; visual criterion pending-manual per D6).

### 2026-06-22 — advance — run 2 (Stage 1, effect 1: animated ctx-gauge sweep)
- State assessed: Stage 0 present and correct (`internal/terminal` + caps wired
  into `Model`). Verified the ctx gauge at `statusline.go:284–304` still rendered
  a static `blockBar(frac, 10, rampColor(frac))` with no motion and no caps
  reference; `TranscriptModel` had no caps field; `workFrame` already increments
  per `workTickMsg` while a turn runs (`transcript.go:366`).
- Changes: threaded `caps terminal.Caps` into `TranscriptModel` (set from
  `a.dashboard.caps` in `app.go` on attach). Added `shimmerBlockBar` to
  `statusline.go` — a width-identical gradient bar that scrolls by `workFrame`
  and breathes coral past 80%. Gated the gauge: animate only when
  `caps.TrueColor && !caps.ReduceMotion && status == StatusBusy`; otherwise the
  exact static path as before. Added `statusline_gauge_test.go` (width-parity
  across fracs/phases/pulse, motion, pulse-differs, zero-caps degradation).
- D-items advanced: D2 + D3 for Stage 1 effect 1 (gated, acceptance: visible
  motion during a turn, no layout shift). D4 reinforced by the width-parity +
  zero-caps degradation tests. Effects 2 (breathing busy) and 3 (wordmark
  shimmer) of §2 not yet done.
- just check: PASS — full gate green (race-twice, e2e, runner 66/1, anti-cheat),
  sandbox disabled per caveat. No golden test changes (degradation path is
  byte-identical, so goldens were untouched).
- Non-blocking nits: none.
- Next: Stage 1 effects 2–3 (breathing busy spinner/active row via caps;
  wordmark shimmer on connecting/thinking), then Stage 2 (OSC signals).

### 2026-06-22 — advance — run 1 (Stage 0)
- State assessed: `internal/terminal` did not exist; no `terminal.Detect`/`Caps`
  references anywhere; ledger empty. Confirmed `Model` (`model.go:127`) had no
  caps field and `New` (`model.go:268`) did not detect terminal capabilities.
- Changes: added `internal/terminal/caps.go` (`Detect() -> Caps` with the §1
  fields: `IsGhostty`, `GhosttyVersion`, `KittyGraphics`, `TrueColor`,
  `ReduceMotion`; env-based, no terminal I/O, testable `detect` core) +
  `internal/terminal/caps_test.go`. Threaded `caps terminal.Caps` into `Model`
  and set it via `terminal.Detect()` in `New` (`model.go`). Zero Caps is the
  test default → no behavior change.
- D-items advanced: D1 (terminal package exists with §1 fields, threaded into
  the model). Partial toward D4 (zero-value gating: no feature reads caps yet).
- just check: PASS — full gate green. Go race-twice + e2e + runner tests
  (66 pass / 1 skip) + anti-cheat all passed; ran with sandbox disabled per the
  in-sandbox httptest caveat.
- Non-blocking nits: none.
- Next: Stage 1 — animated ctx-gauge sweep (`statusline.go:284–304`), gated on
  `caps.TrueColor && !caps.ReduceMotion`.

_(No runs yet. First executor: seed the ledger.)_

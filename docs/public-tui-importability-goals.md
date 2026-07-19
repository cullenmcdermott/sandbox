# Goal: public TUI packages sufficient to reproduce the Sandbox transcript

Status: planning
Owner: driver agent (run via `/goal`, see the prompt at the bottom of this file)

## Objective (unchanged from the original handoff)

An external Bubble Tea v2 app must be able to reproduce the polished Sandbox chat
+ transcript experience using ONLY packages under `github.com/cullenmcdermott/sandbox/tui/...`
(plus the public `client`), importing **nothing** under `internal/`.

The transcript **item layer** (`tui/chat`) is already complete, tested, reviewed,
and has an sdktest pin + a `cmd/chatdemo` example. What remains is the turn-footer
item, a few item-level goldens, and the higher-level interactive components
(`tui/transcript`, `tui/composer`, `tui/picker`, `tui/chrome`) plus an
event-sourced conformance harness and example.

---

## Definition of Done (Convergence Gate)

The goal is COMPLETE when **every checklist box below is ticked** AND all of these
pass (run from repo root; use the sandbox-disabled note in CLAUDE.md for
`just`/tests that bind ports):

```bash
# 1. The full CI gate is green.
just check                                   # exits 0

# 2. No public tui/ package imports internal/ (the importability promise).
#    rg exits 1 on no-match (clean) and 2 on ERROR — only 1 is a pass, so assert it.
rg -n '"github.com/cullenmcdermott/sandbox/internal' tui/; test $? -eq 1

# 3. External consumers build the transcript from PUBLIC EVENTS and drive it.
#    Assert the test EXISTS first — `go test -run <name>` exits 0 even when nothing matches.
rg -q 'func TestTranscriptFromPublicEvents\(' sdktest/
(cd sdktest && go test ./...)                                     # passes

# 4. The example drives the transcript via the public interactive components.
#    Anchor to the quoted import path so a comment mentioning the package can't satisfy it.
rg -q '"github.com/cullenmcdermott/sandbox/tui/transcript' cmd/chatdemo/*.go && \
  rg -q '"github.com/cullenmcdermott/sandbox/tui/composer' cmd/chatdemo/*.go
```

If all of the above hold, the goal is done — **do not invent new scope.**

---

## Checklist (do the top unchecked item whose blockers are all checked)

Each item: the deliverable, then its **Exit Criteria** (checkable commands/asserts).
Tick the box ONLY after you have run its Exit Criteria and seen them pass.

- [x] **T1 — Turn-footer / outcome item in `tui/chat`.**
  Add a public `FooterItem` + data struct rendering the per-turn outcome
  `◇ <model> · via <backend> · <elapsed> · ↑<in> ↓<out> · <cost>` (derive from
  `internal/tui/dashboard/transcript_render.go:turnFooter`; reuse `tui/kit`
  FormatTokens/FormatCost). Empty-safe (renders "" when nothing to summarize),
  width/ANSI/grapheme-safe, version-cached with theme-epoch invalidation,
  focus-aware — same discipline as the other `tui/chat` items.
  - Exit: `go test ./tui/chat/` passes; a width-safety case and a golden include
    the footer; `sdktest/chat_surface_test.go` pins `chat.NewFooterItem` + the
    struct; `rg '"github.com/cullenmcdermott/sandbox/internal' tui/chat` prints
    nothing.

- [x] **T2 — Close item-level golden/scenario gaps.**
  Add golden frames (ANSI-stripped, `testdata/golden/`) for: a mid-stream
  STREAMING transcript, a FATAL-ERROR transcript, and an EMPTY transcript. Add
  list-level scenario tests for resize-while-scrolled-back and
  replay-while-detached-from-bottom (append while the viewport is scrolled up →
  it must NOT jump to the bottom).
  - Exit: `go test ./tui/chat/ ./tui/list/` passes; the three new golden files
    exist; the two scenario tests exist and pass.

- [x] **T3 — `tui/transcript` (event → transcript reducer).** *(blockers: T1)*
  New public package. A `Model`/reducer that `Apply(client.Event)` mutates and
  renders through `tui/chat` items + `tui/list`. Handles: user prompts,
  assistant + reasoning deltas, tool start/progress/output/completion/failure,
  diffs, todos, citations, subagents, permissions, errors, replay, compaction,
  unknown-event degradation. Owns transcript state, event coalescing, tool
  pairing (by tool_use id / parent id), virtual-list composition, scrolling,
  follow mode, focus, expansion, responsive sizing, theme invalidation (wire
  `chat.OnThemeChange`), and markdown-renderer caching. Exposes explicit host
  callbacks/commands for prompt submission, approval decisions, interruption,
  steering, and detach. Keeps networking, connection lifecycle, and session
  ownership OUTSIDE the package. Derive structure from
  `internal/tui/dashboard/transcript_reduce.go` + `transcript_render.go`.
  - Exit: package `tui/transcript` exists; `rg '"github.com/cullenmcdermott/sandbox/internal' tui/transcript` prints nothing; `go test ./tui/transcript/` passes with a test per event kind + a replay test + an unknown-event-degradation test + a fixed-`[]client.Event`-script golden; `sdktest` pins the surface.

- [x] **T4 — `tui/composer` (input + steering).**
  New public package: multiline input, bracketed paste, responsive growth (caps
  then scrolls), history with draft preservation, queue-while-busy with editable
  queued steering, submission, interruption, and busy/ready/disabled states.
  Preserve the escape cascade + key-routing from
  `internal/tui/dashboard/transcript_input.go` / `esc_cascade_test.go`. An
  asynchronously-appearing permission request must not consume text/keystrokes
  the user was already entering.
  - Exit: `tui/composer` exists; no internal import; `go test ./tui/composer/`
    passes including a test proving an async permission arrival does not steal a
    partially-typed prompt, a queue-while-busy test, and an escape-cascade test;
    `sdktest` pins the surface.

- [x] **T5 — `tui/picker` (selection vocabulary).**
  Generalize model/backend/account selection into a reusable public picker
  (derive from `internal/tui/dashboard/modelpicker.go` / `backend_picker.go` /
  `account_picker.go`), free of app transport/lifecycle policy.
  - Exit: `tui/picker` exists; no internal import; `go test ./tui/picker/`
    passes; `sdktest` pins the surface.

- [x] **T6 — `tui/chrome` (or expand `tui/kit`).**
  Promote the reusable status line, context/token gauge, working indicator,
  transient scrollbar, calm notices, and transition catalog into a public
  package, free of app transport/lifecycle policy.
  - Exit: the package exists; no internal import; `go test` for it passes;
    `sdktest` pins the surface.

- [x] **T7 — Event-sourced conformance + example rewrite.** *(blockers: T3, T4)*
  Add `sdktest` test `TestTranscriptFromPublicEvents`: build a representative
  transcript by feeding public `client.Event` values into `tui/transcript`, and
  exercise send, scroll, follow-mode escape+recovery, arbitrary tool expansion,
  approval (via the real callback), resize, theme swap, interruption, and
  graceful detach. Render at 80x24, 100x30, 140x40. Add goldens for empty,
  connecting, streaming, reconnect, fatal-error, approval. Rewrite
  `cmd/chatdemo` (or add a new example) to drive the transcript via
  `tui/transcript` + `tui/composer` from a scripted `[]client.Event`, NOT
  hand-assembled items.
  - Exit: `cd sdktest && go test ./...` passes incl. `TestTranscriptFromPublicEvents`; the example imports `tui/transcript` and `tui/composer` and its render test passes; the six new goldens exist.

- [x] **T8 — Final gate + polish.** *(blockers: T1, T2, T3, T4, T5, T6, T7)*
  Preserve idle motion gating and reduced-motion behavior in the public
  components (honor `anim.ReduceMotion`). No placeholder/inert/empty renderer or
  component anywhere in the new packages. Update `docs/public-api-importability-plan.md`
  status and `TODO.md`.
  - Exit: `just check` exits 0; `rg -n 'not implemented|TODO|placeholder|return ""' tui/transcript tui/composer tui/picker` shows no inert renderer; a reduced-motion test exists.

---

## Rules for the driver

- Public `tui/` packages must NOT import anything under `internal/` (tests
  included — that is the whole promise).
- Do NOT change production-dashboard behavior or its golden output. The dashboard
  may (optionally) be refactored to *consume* the new public packages, but only
  if all its existing tests + goldens still pass unchanged.
- Test-driven: write the failing test first, then the implementation.
- Do NOT commit or push unless the human explicitly asks. Work on a branch off
  `main`.
- In-sandbox caveat (CLAUDE.md): `client`, `internal/runner`, `internal/k8s`,
  and `just check`/`just verify` need the sandbox disabled.
- Never tick a checkbox whose Exit Criteria you did not run and see pass. If an
  item is genuinely blocked, write the blocker under it and move to the next
  eligible item instead of spinning.
- Eligibility is determined ONLY by the `*(blockers: …)*` tick state; a written
  blocker note never makes an item ineligible. Re-check notes each session and
  delete them once resolved.
- Run at most one `/goal` session at a time. A dirty working tree may hold the
  prior session's partial work on the top eligible item — finish THAT item, don't
  restart elsewhere. Boxes are tick-only; never untick one.

## Session Log (append one line per session; newest last)

- T1 done: added public `chat.FooterItem`/`chat.TurnFooter` (empty-safe, width/ANSI/grapheme-safe, version+theme-epoch cached, focus-aware `◇ model · via backend · elapsed · ↑in ↓out · cost`), wired `styFooter`, added footer_test.go + width-safety factory, put the footer in 3 canonical goldens, and pinned the surface in sdktest; `go test ./tui/chat/` green, no internal import.
- T2 done: added streaming / fatal-error / empty golden frames (`tui/chat/testdata/golden/{streaming,fatal,empty}_80x24.txt` via new `TestGolden{Streaming,FatalError,Empty}`) and two list scenario tests (`TestResizeWhileScrolledBackPreservesPosition`, `TestReplayWhileDetachedDoesNotJumpToBottom` — resize/append while scrolled back must not jump to bottom); `go test ./tui/chat/ ./tui/list/` green.
- T8 done: final gate + polish — inert-renderer grep over tui/transcript·composer·picker is clean (named the `blankSpacer` constant, reworded a composer doc comment); reduced-motion honored + tested (`chrome.TestWorkingIndicatorReducedMotion`); fixed 9 lint findings (ineffassign/staticcheck) surfaced by the flox-provided golangci-lint; `just check` exits 0 (all gates: gen, fmt, lint 0 issues, build, vet, test Go+runner, typecheck, sdk-conformance incl. `go mod tidy`, race-twice, e2e); updated `docs/public-api-importability-plan.md` status + `TODO.md`.
- T7 done: added sdktest `TestTranscriptFromPublicEvents` (builds transcript from `client.Event`, exercises composer-send/scroll/follow-escape+recovery/tool-expand/approval-via-callback/resize/theme-swap/interrupt/detach across 80x24·100x30·140x40) + `TestTranscriptGoldens` with six goldens (empty/connecting/streaming/reconnect/fatal/approval in `sdktest/testdata/golden/`). Rewrote `cmd/chatdemo` to drive `tui/transcript`+`tui/composer`+`tui/chrome` from a scripted `[]client.Event` (no hand-assembled items); updated its render tests + README. Convergence gates #3/#4 now green.
- T6 done: new public `tui/chrome` package — width-budgeted `StatusLine`/`Segment` (Seg/Req, sheds optional tail-first), `ContextGauge`/`BlockBar`/`RampColor` (ctx% gauge, hidden below 60%, coral warn past 80%), `WorkingIndicator`/`Working` (live line honoring `anim.ReduceMotion`), and calm `Notice`/`NoticeKind`. Scrollbar/token+cost formatters/transition catalog already public (kit/anim); this fills the gaps. Free of transport/lifecycle. Tests (incl. reduced-motion) green; sdktest pins added; no new module deps. no internal import.
- T5 done: new public `tui/picker` Bubble Tea v2 selection overlay generalizing model/backend/account pickers — `Item{ID,Name,Desc,Current}` numbered rows, ↑/↓ + k/j nav, 1-9 jump-and-choose, enter confirm, esc cancel via `WithChoose`/`WithCancel`; width-safe titled box render. Free of transport/lifecycle. Tests cover preselect/nav-clamp/enter/digit/esc/view/width-safety; sdktest pins added. `go test ./tui/picker/` + sdktest green, no internal import.
- T4 done: new public `tui/composer` Bubble Tea v2 component (bubbles textarea) — multi-line input, bracketed paste (textarea-native), responsive growth capped at maxRows then scroll, ↑/↓ history recall with draft preservation + cursor-line awareness, queue-while-busy with editable steer that flushes on turn-end, escape cascade (steer→interrupt→detach), grace-gated permission answering (a/d) with type-ahead protection + hard cap, ready/busy/disabled state hints. Tests cover async-permission-doesn't-steal-prompt, queue-while-busy, escape-cascade (+history/growth/disabled/view); sdktest surface pins added (sdktest go.mod gained bubbletea/bubbles). `go test ./tui/composer/` + sdktest green, no internal import.
- T3 done: new public `tui/transcript` package — a callback-based `Model` reducer (`Apply(client.Event)`) rendering through tui/chat items + tui/list, covering every event kind (user/assistant/reasoning deltas, tool start/delta/progress/complete/fail incl. id-pairing + FIFO fallback + drain, subagents by parent id, todos pin, citations, permissions, compaction, usage→footer, errors, terminating, replay dedup + boundary, unknown-event degradation); owns follow/scroll/focus/expansion/entry-gap spacing/theme invalidation/markdown caching; host actions (submit/approve/deny/interrupt/steer/detach) via callbacks. Ported diff builder locally (no internal import). Full per-event tests + replay + unknown-event + fixed-`[]client.Event` golden (`testdata/golden/script_100x40.txt`); sdktest surface pins added. `go test ./tui/transcript/` + sdktest green, no internal import.

---

## The `/goal` prompt (feed this verbatim, repeatedly)

> Drive the goal in `docs/public-tui-importability-goals.md`. That file is the
> single source of truth for remaining work and the Definition of Done.
>
> STEP 1 — FIRST, every session, run the **Convergence Gate** commands in that
> file. If they ALL pass AND every Checklist box is ticked, output exactly
> `GOAL COMPLETE — public TUI importability` and STOP. Do nothing else.
>
> STEP 2 — Otherwise pick the TOP unchecked Checklist item whose blockers are all
> ticked. Implement it FULLY — production behavior, no placeholders/TODOs/empty
> renderers — test-first (write failing tests, then code). Match the repo's Charm
> v2 / theme / list / terminal conventions. Public `tui/` packages must not
> import anything under `internal/`. Two edge cases: (a) if every box is ticked
> but a Convergence Gate command FAILS, or a previously-ticked item's Exit
> Criteria have regressed, fixing that regression IS this session's task — never
> untick a box, just log the fix; (b) if NO unchecked item is eligible (all
> remaining items' blockers are unticked), output exactly
> `GOAL BLOCKED — <reason>` and STOP.
>
> STEP 3 — Verify against that item's **Exit Criteria** (run its listed commands;
> they must pass). Only then tick its checkbox in the file and append a one-line
> Session Log entry.
>
> STEP 4 — Re-run the Convergence Gate. If green and all boxes ticked, output
> `GOAL COMPLETE — public TUI importability` and STOP; otherwise end the session
> (the next `/goal` turn continues from the file).
>
> Guardrails: don't scope-creep beyond the checklist; don't change
> production-dashboard behavior or its goldens; don't commit or push unless
> explicitly asked (branch off `main`); use the CLAUDE.md in-sandbox caveats (run
> `just check`/`just verify`/port-binding tests with the sandbox disabled). If an
> item is genuinely blocked, record the blocker under it and move to the next
> eligible item rather than spinning. Never tick a box whose Exit Criteria you did
> not run and see pass.

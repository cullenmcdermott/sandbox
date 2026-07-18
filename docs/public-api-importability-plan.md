# Public API & importability plan

Status: **draft** (2026-07-18) — awaiting maintainer sign-off on sequencing.

## Goal

Let an external tool `go get` our packages and build a *fully-featured* agent /
chat / dashboard TUI — and drive k8s codex/opencode/claude sessions — without
copying code out of `internal/`. Maintainer priorities (2026-07-18): the way the
chat is constructed, the input box, steering, model selection, UX ✨, and auth
(**device-code primarily**).

## Current public surface

- `client` — session lifecycle, turns, events (re-exported), sync. The SDK entry point.
- `client/cred` — Anthropic multi-account credential store.
- `tui/kit`, `tui/theme`, `tui/list`, `tui/anim`, `tui/terminal` — low-level widgets + primitives.

## The gap

Everything that makes it *feel* like a chat/agent UI lives in
`internal/tui/dashboard/` (unimportable): the transcript/chat renderer, the
composer/input box, steering, the model/backend/account pickers, the permission
prompt, the statusline, transitions. A consumer today can style text and draw a
card but must reimplement the entire chat experience. The reusable widgets are
mostly implemented as *methods on the monolithic `TranscriptModel` god-struct*,
which also owns SSE streaming, connection, autopilot, and pickers — so the work
is decoupling the widget from that model, not writing it from scratch.

## Component-by-component (the pieces the maintainer named)

### 1. Chat construction (render layer) — EASY, do first
`internal/tui/dashboard/chat/` is **already decoupled**: verified zero internal
deps; its only first-party imports are `tui/list` + `tui/theme`, both
self-contained public packages. It is the assistant / user / tool / subagent /
streaming-markdown item renderers.
- **Plan:** `git mv internal/tui/dashboard/chat → tui/chat`; bump 5 caller import
  paths; add an sdktest pin. Near-mechanical. Gives importers the
  message-bubble / tool-card / streaming-markdown vocabulary immediately.

### 2. Transcript (event → frame) — MEDIUM, the keystone
`transcript_render.go` / `transcript_reduce.go` turn an event stream into a
scrollable transcript, but import `internal/session` (19×) for event types and
are methods on `TranscriptModel`. Event types are **already re-exported via
`client`** (type aliases), so the type dependency is switchable with zero
behavior change.
- **Plan:** extract `tui/transcript` — a `Model` owning only transcript state
  (blocks, todo, streaming tail) + `Apply(client.Event)` reducer + `View(width)`;
  leave SSE/connection/autopilot in the host app. §2a already did the
  one-event-reducer and row-model consolidation, so the seam exists.

### 3. Input box + steering — MEDIUM
Composer + steering (queued prompt, interrupt-with-partial, esc-cascade) are
`TranscriptModel` methods across `transcript_input.go`, `modes.go`, `editor.go`,
`inputctx.go`. Steering state is a single `queuedPrompt` string.
- **Plan:** extract `tui/composer` — multiline input + history (↑/↓ recall with
  draft preservation, already built §2d) + a steering interface
  (queue-while-busy / submit / interrupt) the host wires to turns. Fold in the
  multi-message editable queue (audit gap-9; today one string silently
  overwrites).

### 4. Model selection — EASY
`modelpicker.go` imports only `tui/kit` + `tui/theme`; the picker logic is nearly
standalone, only open/state lives on `TranscriptModel`.
- **Plan:** generalize into `tui/picker` — a reusable selectable-list-in-a-box.
  Model, backend, and account pickers all become instances, retiring
  `modelpicker.go` / `backend_picker.go` / `account_picker.go`. Aligns with the
  §2e-A dialog-stack direction.

### 5. UX ✨ — MEDIUM/LOW
`tui/theme` + `tui/anim` are public. The premium chrome (statusline, transitions,
working indicator, transient scrollbar, calm notices) is app-local.
- **Plan:** promote the reusable chrome into `tui/chrome` (or fold into
  `tui/kit`): statusline builder, working indicator, transition catalog. Lower
  priority than 1–4; do opportunistically as 2/3 extract their render bits.

### 6. Auth — device-code is the headline
Today: subscription auth shells out to the host `claude setup-token` binary (host
dependency, §6/§7b); console = paste an `sk-ant-api…` key. `client/cred` is
Anthropic-only; **no native device-code, no OpenAI/ChatGPT** (codex needs it).
- **Plan (device-code FIRST):** add a native OAuth 2.0 device-authorization flow
  to `client/cred` (or new `client/auth`): `RequestDeviceCode()` → surface
  `user_code` + `verification_uri` → `PollToken()`. Removes the host-`claude`
  dependency, generalizes to ChatGPT (codex) + provider keys, and gives importers
  a headless-friendly programmatic login. Resolves TODO §6 item-2 ("investigate
  device-code, then decide") — the decision is *build it*.

### 7. Client SDK surface (make the k8s multi-backend part easy)
- **[V8]** `client/events.go` claims "the full normalized event vocabulary" but
  is missing `EventToolProgress` + `EventContextCompacted` constants and the
  `ContextCompactedPayload` / four `Turn*Payload` / `ToolProgressPayload` /
  `Citation` aliases. Fix + add an sdktest pin that fails when `events.go` drifts
  from the generated vocabulary.
- Confirm `client.Create` exposes backend selection (claude/opencode/codex) and
  per-backend options as first-class public options; tie to the §6 credential
  lifecycle so "spin up a fully-featured agent" is one SDK call + one auth call.

## Sequencing (cheap → structural)

1. `tui/chat` promotion + **[V8]** event-surface fix — high signal, ~mechanical.
2. `tui/picker` generalization (model/backend/account).
3. `client/cred` device-code flow.
4. `tui/transcript` extraction (the keystone decomposition).
5. `tui/composer` + steering API (incl. multi-message queue).
6. `tui/chrome` UX vocabulary.

Every promotion adds an sdktest pin (the external-consumer module) so the surface
can't silently regress — and a doc example showing a minimal chat/dashboard built
only from public packages, which is the real acceptance test for "easy to import."

## Deferred aggressive verification (post-limit-reset)

A per-component extraction-feasibility study (attempt each decoupling in a
worktree, report true blast radius); finish the 47-finding end-to-end + visual
verification (`docs/audit-2026-07-18.md`); run the 6 auditors that died on the
spend limit (tui-public + security first); a public-API completeness diff
(every export-worthy internal type vs what's re-exported).

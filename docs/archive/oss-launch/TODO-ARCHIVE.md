# TODO archive — still-open items

Extracted from the repo-root `TODO.md` before it was archived out of the public tree
for the OSS launch. Only the **still-open** items are preserved here — every `[x]`
(done) row was dropped. Status legend: `[ ]` open · `[~]` in progress.

Line:file pointers were accurate at extraction; treat them as anchors.

---

## Product vision

- **[~] T10 — New session should prompt for a working directory.** Default to the pwd
  from TUI start, but allow any dir, with autocomplete and a recents list.
  `newDashboardCreator` currently hardcodes `os.Getwd()`. PAUSED before implementation;
  design + plan existed (a `dirPicker` overlay shown *before* the backend picker: `~`
  expansion, Tab completion, recents from the index; `Creator`/`createCmd` gain a
  `projectPath` param). Note for resume: T6 already edited `claude_remote.go`/`root.go`,
  so pre-T6 snippets won't match — adapt to the current tree when wiring
  `RunOptions.RecentDirs`.
  → `internal/tui/dashboard/backend_picker.go`, `internal/cli/dashboard_connector.go`

- **[x] T17 — Export TUI components as a public, reusable package.** Done: moved the
  reusable building blocks out of the unexported tree into repo-root `tui/`:
  `tui/kit` (widgets), `tui/anim` (transitions/spinner), `tui/list` (scrolling list),
  and a new `tui/theme` (palette, registry, exported tokens, gradient/spinner/fade
  helpers, status glyphs). App-coupled pieces stay in `internal/tui/dashboard`: the
  app-specific lipgloss styles and `glyphColor(SessionStatus)` now read `theme.*`
  tokens and re-skin via a `theme.OnChange` rebuild hook. Other projects import e.g.
  `github.com/cullenmcdermott/sandbox/tui/kit`.

## Harness engineering

- **[ ] HE-1 — Ratchet `.golangci.yml` stricter.** Initial config is non-strict to stay
  green on existing debt. Re-enable each after a dedicated cleanup pass (each has
  pre-existing hits): `unused` (~23 dead theme/style vars + transcript fields),
  `staticcheck` (~11: deprecated test APIs, S/QF style), and the `goimports` formatter
  (import-grouping churn).
  → `.golangci.yml` (deferred-linter comment block)

- **[ ] HE-2 — Real `SA4006`/`SA4000` findings surfaced by staticcheck.** Enabling
  staticcheck (HE-1) surfaces a likely real test bug (identical expressions around
  `!=`) worth fixing on its own.
  → `internal/tui/dashboard/transcript_a1_chat_live_test.go:70` (SA4000);
  `cmd/mockup/slash.go:307` (SA4006) — NOTE: `cmd/mockup/` was removed in the OSS
  cleanup, so the SA4006 pointer is moot; the SA4000 one stands.

- **[ ] HE-3 — Tier 2/3 enrichments (optional).** The e2e smoke uses a fake runner (the
  higher-fidelity full-stack variant with a stub Claude SDK is the documented next
  step); `sandbox trace` reads events via the runner API (a future offline mode could
  read synced `~/.claude/projects/*.jsonl`); the runner side of `--debug` (TS) is
  documented but not yet emitting the JSON-line schema.
  → `docs/design/harness-engineering-plan.md` Tiers 2-3 *(NOTE: that design doc was
  archived in the OSS cleanup; the work items themselves stand.)*

## Lifecycle & idle reaper (review backlog)

- **[ ] RV46 [LOW] Destroy does not delete the per-session reaper Job;** relies on the
  reaper noticing Gone on its next ~30s tick. Best-effort delete the reaper Job
  (`Jobs(ReaperNamespace).Delete(reaperJobName(id))`) in Destroy alongside the
  Sandbox/PVC/Secret.
  → `internal/k8s/backend.go`; `internal/cli/reap.go`

- **[ ] RV47 / RV30 [LOW] `index.LastEventSeq` is never persisted** — every fresh attach
  replays full history from seq 0. Persist `lastSeq` into the index entry (on consume or
  detach) and seed `NewTranscript`'s `lastSeq` from `index.Load` on attach. (RV30 is the
  duplicate of RV47.)
  → `internal/index/index.go`; `internal/tui/dashboard/transcript.go`; `internal/cli/commands.go`

- **[ ] RV65 [NIT] `Index.SaveFromState` is dead code** that would silently drop
  `CreatedAt`/`Namespace`/`MutagenSessions`/ports/`LastEventSeq` if ever wired up. Either
  remove it, or make it a true merge preserving all locally-owned non-State fields.
  → `internal/index/index.go`

## Attach / detach / reconnect

- **[ ] RV8 [MEDIUM] Dashboard list status opens the full attach connector** (port-forward
  + Mutagen + reaper) per session. `startLiveSSECmd` uses the same `Connector` as an
  interactive attach (resumes-if-suspended, port-forward, health-check, starts Mutagen,
  ensures reaper). Add a lightweight status-only connect (port-forward + health + SSE,
  skipping `startMutagen` and `ensureReaperForSession`) for background streams. (Largest
  open item in this backlog per the maintainer's MEMORY notes.)
  → `internal/tui/dashboard/model.go`; `internal/cli/connect.go`

- **[ ] RV27 [LOW] "New since you left" unread divider is never armed on detach.**
  `unreadIndex` is never set positive. On detach record `len(m.blocks)`; on reattach
  restore it into `unreadIndex`.
  → `internal/tui/dashboard/transcript.go`; `internal/tui/dashboard/app.go`

- **[ ] RV28 [LOW] Hard pod loss is invisible for up to 90s** before "reconnecting"
  feedback (only detected via the 90s SSE read watchdog). Show a "stream stalled / no
  data for Ns" hint after a shorter idle threshold.
  → `internal/runner/client.go`; `runner/src/events.ts`

## Error handling & resilience

- **[ ] RV14 [MEDIUM] Port-forward death only detected via the 90s SSE watchdog**
  (`Done()` never consumed). `forwardHandle.Done()` is referenced only in tests. Have the
  connector/transcript select on `handles[0].Done()` and proactively trigger reconnect.
  Also the real fix for the recurring port-22 forward error spam.
  → `internal/k8s/portforward.go`; `internal/cli/connect.go`

- **[~] RV15 [MEDIUM] Reconnect retries forever with no failure ceiling.** Partial:
  backoff + spam-throttle landed via RV29 (3→30s cap). Still open: a hard terminal
  "press r" state on enough failures / `Status==Gone` (needs a keybinding).
  → `internal/tui/dashboard/transcript.go`

- **[ ] RV39 [LOW] Opaque HTTP-status errors surfaced to the user** ("status 409",
  "status 502"). The runner client discards the JSON `{error:'…'}` body. Decode the
  body's `error` field and map common codes (401/409/429).
  → `internal/runner/client.go`

## TUI / dashboard UX

- **[ ] RV25 [MEDIUM] Connecting stepper has no active spinner and a false "Syncing" step
  for opencode sessions.** `connectingStepper` uses a fixed default list that omits
  `StageOpencode`. Pass an opencode-aware applicable list and reorder the `ConnectStage`
  enum to match actual order.
  → `internal/tui/dashboard/app.go`

- **[ ] RV59 [LOW] Detach drops transcript scroll & selection** despite the spec
  ("selection and scroll intact"). `ParkedTranscriptState` saves only content. Either
  persist a stable block index + intra-block offset and re-apply after replay, or update
  the spec.
  → `internal/tui/dashboard/transcript.go`

- **[ ] RV67 [NIT] `:` command-palette binding is disabled** though the spec lists it
  (`key.WithDisabled()`). Either implement a minimal palette and enable it, or remove the
  binding and strike `:` from the documented keymap.
  → `internal/tui/dashboard/keymap.go`

## CLI command UX

- **[ ] RV31 [LOW] No `--help` detail** — zero `Long` descriptions or `Example`s on any
  command. Add `Long` + `Example` to each, especially `destroy` (irreversibility +
  `--force`), `sync`, `suspend`/`resume` (PVC retention).
  → `internal/cli/commands.go`

## Runner HTTP+SSE API

- **[ ] RV51 [LOW] Permission scope `session` accepted but silently treated as
  allow-once.** Either implement session-scoped grants or remove `session` from the
  documented/typed scope values.
  → `runner/src/claude.ts`

- **[ ] RV52 [LOW] Permission-resolve returns `{resolved:true}` even when the underlying
  resolve was a no-op** (already auto-denied; `resolve` short-circuits on `settled`). Have
  `resolve` return a bool and reflect it, or 409 when already settled.
  → `runner/src/server.ts`

- **[ ] RV54 [LOW] Server-level turn error path emits bare `error` without a terminal
  `turn.failed`.** `claude.ts` emits both; the server fallback `.catch` emits only
  `error`. Emit `turn.failed` before `error` and make `finishTurn` idempotent.
  → `runner/src/server.ts`

- **[ ] RV55 [LOW] exec truncation marker always appended to stdout even when only stderr
  overflowed.** Single shared `truncated.flag`. Track per stream and mark whichever
  overflowed.
  → `runner/src/exec.ts`

- **[ ] RV66 [NIT] Wrong HTTP method on a valid route returns 404 instead of 405.** Match
  the path first; if path known but method isn't, respond 405 with an `Allow` header.
  → `runner/src/server.ts`

## Event model (Go ↔ TS)

- **[ ] RV41 [LOW] No `ReasoningPayload` struct;** `reasoning.completed` reuses
  `MessagePayload` by coincidence. Add a `ReasoningPayload {Content; Delta}` and a TS
  mirror; decode against it.
  → `internal/session/event.go`; `runner/src/claude.ts`; `internal/tui/dashboard/transcript.go`

- **[ ] RV42 [LOW] `ToolPayload.exitCode` declared on both sides but never populated** on
  tool events (only in the PostToolUse audit hook). Populate it on
  `tool.completed`/`tool.failed` for Bash and render it, or drop the field.
  → `internal/session/event.go`; `runner/src/types.ts`

- **[ ] RV44 [LOW] `todo.updated` event type declared on both sides but never emitted.**
  TUI has a render branch. Either map `TodoWrite` → `todo.updated` (with a `TodoPayload`),
  or remove the dead event type from both enums + the TUI branch.
  → `internal/session/event.go`; `runner/src/types.ts`; `internal/tui/dashboard/transcript.go`

- **[ ] RV64 [NIT] `turn.completed` rich payload (result/stopReason/numTurns/durationMs)
  entirely unconsumed by Go.** Define a `TurnCompletedPayload` and use durationMs/numTurns
  in the footer, or document it as intentionally informational.
  → `runner/src/claude.ts`; `internal/tui/dashboard/transcript.go`

## Mutagen / SSH sync

- **[ ] RV23 [MEDIUM] Two-way-safe conflicts are never surfaced in the TUI.** Mutagen
  halts on conflict and waits for manual resolution, but nothing in the dashboard shows
  it. Poll `Manager.Status` (or `mutagen sync list`) for the attached session and render
  a persistent warning when conflicts exist.
  → `internal/sync/sync.go`

- **[ ] RV56 [LOW] Lost local SSH key permanently breaks sync with no recovery path.**
  `ensureSSHKey` regenerates a new keypair if local files are missing, but the pod only
  trusts the original public key in its per-session Secret. On attach, detect/update the
  Secret's authorized key if the local pubkey differs.
  → `internal/cli/sync_support.go`

- **[ ] RV57 [LOW] Project secret-ignore patterns only block dotfiles at the project
  root,** not git-ignored secrets in subdirs. Extend the default ignore set (`*.tfstate`,
  `*.tfvars`, `.terraform`, `.kube`, `.aws`, `*.log`) and document it.
  → `internal/sync/sync.go`

## Design vs built reality

- **[ ] RV37 [LOW] A5 "single spinner clock" box checked while two tick loops still
  coexist** (overclaim by rescope). The dashboard still runs its own `animTickMsg` loop
  alongside the chat spinner. Either drive `m.workFrame` from the same gated anim clock
  (delete `workTickCmd`/`workTickMsg`), or, if the separate-model design is intended,
  uncheck/annotate the claim.
  → `internal/tui/dashboard/transcript.go`; `internal/tui/dashboard/model.go`

- **[ ] RV61 [NIT] Vision items T10 + T17 remain unfinished** (honestly tracked; no code
  change required until resumed). See T10/T17 above.
  → `internal/cli/dashboard_connector.go`

## Onboarding & first-run / docs

- **[ ] RV48 [LOW] First-failure errors for missing kubeconfig / cluster are technical
  and unguided** (double-wrapped `failed to connect to cluster: failed to connect…`).
  Remove the duplicate wrap and print a one-line hint (check `KUBECONFIG` /
  `kubectl config current-context`).
  → `internal/cli/root.go`

- **[ ] RV49 [LOW] Idle-reaper image is an undocumented cluster prerequisite** started on
  every connect (`ensureReaperForSession` schedules a Job using the reaper image). Add the
  reaper image to README prerequisites, mention `--reaper-image`, note its purpose.
  → `internal/k8s/reaper.go`

- **[ ] RV50 [LOW] README omits the primary entry point (bare `sandbox` dashboard)** and
  several real subcommands. Add a `sandbox` (no args → dashboard) row, document
  `opencode`/`rename`, and lead "How it works" with the dashboard.
  → `README.md`

## Process / triaged-but-open

- **[~] Port-22 forward error spam (the Mutagen SSH channel).** Recurring `an error
  occurred forwarding <localport> -> 22: … network namespace for sandbox … is closed`.
  Port 22 is the Mutagen sync SSH channel; the message fires when the forward's target pod
  goes away (suspend/reschedule/reaper) while the SPDY forward is still open (logged by
  client-go's `runtime.HandleError`). Tracked as RV12 (triage) + RV14 (no proactive
  `Done()` consumer). Real fix is forward-restart / `Done()`-watching (see RV14); until
  then the log line is noisy but benign (SSE reconnect recovers the session).
  → `internal/k8s/portforward.go`

---

## Note on the completed batches (not preserved in full)

The original `TODO.md` also tracked large **completed** batches that are intentionally
NOT carried forward here: T1-T9, T11-T16 (the UX/bug batch — all done); the
harness-engineering plan Tiers 1-4 (done); and the ~40 `[x]` RV review findings fixed
2026-06-22 (RV1-RV7 HIGHs, RV9-RV13, RV16-RV22, RV24, RV26, RV29, RV32-RV36, RV38,
RV40, RV43, RV45, RV53, RV58, RV60, RV62, RV63). They were closed out before the OSS
launch and are documented inline in git history.
</content>
</invoke>

# `/effort` integration plan

Wire an in-session **`/effort`** slash command into the Claude Agent SDK TUI so the
user can set the SDK reasoning-effort level per turn, mirroring the existing
`/model` override path end-to-end. Top tier is labeled **`ultracode`**.

Status: **planned, not started.** All file:line anchors below were verified against
the tree at plan-authoring time (branch `parity-and-dev-hardening`); line numbers may
drift — anchor on the function/struct names and the quoted strings.

---

## Locked design decisions

- **Levels exposed:** `low` · `medium` · `high` · `xhigh` · **`ultracode`**.
  The first four are the SDK `EffortLevel` values verbatim; **`ultracode` is the UI
  label for the SDK's top level `max`.** People see "ultracode" so they know it's the
  crank-it-to-the-ceiling tier.
- **Wire stays honest:** the override field and `TurnInput.Effort` carry the *real*
  SDK enum value (`"low"|"medium"|"high"|"xhigh"|"max"`). Only the **display label** is
  "ultracode". The runner validates against the real enum; an unknown value leaves
  `options.effort` unset (no forced level).
- **Default = unset.** Empty `effortOverride` ⇒ no `options.effort` ⇒ SDK adaptive
  thinking (today's behavior). `/effort-auto` clears the override.
- **Applies on the next turn** (turns are atomic POSTs), same as `/model`. Confirmation
  text says so.
- **Palette shape:** a STATIC `Effort` group shaped like the **Mode** group
  (`commands.go:59`), NOT the dynamic Model group — the levels are a fixed enum.
- **Scope:** per-turn override + status tag only. Session-default parity
  (`SANDBOX_EFFORT`/`Spec.Effort`), keyword-escalation (`think`/`ultrathink` in the
  prompt), and the spinner-verb "soul" changes are **out of scope** for this change
  (see Deferred backlog). Cut entirely per review: `/8ball`, `/redline`, the
  "dinner-receipt" cost styling.

### Why "ultracode" is honest here

In real Claude Code, `ultracode` = `xhigh` + auto-spawn a multi-agent workflow — it's
not a raw SDK level. This TUI has no auto-workflow engine, so we are **not** claiming
that behavior. We're reusing the recognizable word purely as the display name for the
SDK's genuine `max` effort tier (the actual top of `low|medium|high|xhigh|max`,
sdk.d.ts ~line 1576). It's the "bizarro" label for the real ceiling — sets expectations,
fakes nothing.

---

## SDK facts (verified)

`runner/node_modules/@anthropic-ai/claude-agent-sdk/sdk.d.ts`:
- `options.effort?: 'low'|'medium'|'high'|'xhigh'|'max'` (`EffortLevel`, ~line 1576) —
  "Controls how much effort Claude puts into its response. Works with adaptive thinking
  to guide thinking depth." Supported on **Fable 5 / Opus 4.6+ / Sonnet 4.6** only.
- Silently dropped on models without effort support; `xhigh`/`max` downgrade on models
  that don't support those tiers. There is **no echo** of the applied effort back to the
  client (`SessionStartedPayload`, `internal/session/event.go` ~line 113, has no effort
  field), so the status tag can only reflect the *request*, never an SDK confirmation.
- `options.effort` is typed `EffortLevel`, NOT `string` — assigning a raw string fails
  `tsc --noEmit` (part of `just check`). Must go through a validator that narrows.

---

## The mirror: how `/model` already flows (the template)

1. Palette handler `setModelCmd(id,label)` (`commands.go:140`) sets `m.modelOverride`.
2. `submitText` passes it to `startTurnCmd` (`transcript.go:1821`).
3. `startTurnCmd` packs `session.TurnInput{Model: ...}` (`transcript.go:2639`).
4. `runner.Client.StartTurn` POSTs the JSON (`internal/runner/client.go:67`).
5. `server.ts:169` reads `body.model`, passes to `agent.runTurn`.
6. `claude.ts` `runTurn` → `buildOptions` → `resolveModel` → `options.model`
   (`claude.ts:246`).

`/effort` clones this exactly, plus a status-line tag.

---

## Implementation checklist

### A. Wire contract (Go + TS)

- [ ] **`internal/session/types.go`** — in `TurnInput` (struct at line 114), after the
  `Model` field (line 126) add:
  ```go
  // Effort overrides the reasoning-effort level for this turn (the in-session
  // /effort switch): one of "low", "medium", "high", "xhigh", "max". Empty =>
  // the runner leaves options.effort unset (SDK adaptive-thinking default).
  // Supported on Fable 5 / Opus 4.6+ / Sonnet 4.6 only; silently ignored
  // elsewhere. NOTE: the wire value is the real SDK enum — the TUI displays
  // "max" as "ultracode".
  Effort string `json:"effort,omitempty"`
  ```
  No change needed in `internal/runner/client.go` — `json.Marshal(input)` already
  serializes the whole struct.

- [ ] **`runner/src/types.ts`** — in `TurnRequestBody` (line 79), after `model?` (line 93)
  add `effort?: string;` with a doc comment mirroring `model`'s (note the enum + the
  silent-ignore caveat + that `"max"` is shown as "ultracode" in the TUI).

### B. Runner apply (TypeScript)

- [ ] **`runner/src/claude.ts`** — add a pure validator near `resolveModel` (line 91):
  ```ts
  const VALID_EFFORTS = new Set(['low', 'medium', 'high', 'xhigh', 'max']);
  /** Narrow a per-turn effort string to the SDK EffortLevel, or undefined
   *  (leave options.effort unset) for empty/unknown values. */
  export function resolveEffort(
    turnEffort: string | undefined,
    sessionEffort?: string | undefined,
  ): EffortLevel | undefined {
    const v = turnEffort || sessionEffort;
    return v && VALID_EFFORTS.has(v) ? (v as EffortLevel) : undefined;
  }
  ```
  Import `type EffortLevel` from `@anthropic-ai/claude-agent-sdk` (top of file with the
  other SDK type imports, ~line 18).
- [ ] **`runner/src/claude.ts`** — `buildOptions` (signature at line 170): add
  `effort: string | undefined,` after the `model` param. After the model block
  (`if (resolvedModel) options.model = resolvedModel;`, line 247) add:
  ```ts
  const resolvedEffort = resolveEffort(effort);
  if (resolvedEffort) options.effort = resolvedEffort;
  ```
- [ ] **`runner/src/claude.ts`** — `runTurn` (signature at line 485): add
  `effort: string | undefined,` after `model` (line 492); forward it in the
  `buildOptions(...)` call (line 512).
- [ ] **`runner/src/agent.ts`** — `Agent.runTurn` interface (line 25): add
  `effort: string | undefined,` after `model` (line 32). This is the type fan-out
  point — `tsc --noEmit` will force the two impls + the call site to update.
- [ ] **`runner/src/server.ts`** — the `agent.runTurn(...)` call (line 169): insert
  `body.effort` as a new positional arg between `body.model` and `turn.abort`. No
  body-parse change — `readBody<TurnRequestBody>` already deserializes it.
- [ ] **`runner/src/opencode-turn.ts`** — `runTurn` (line 486): add a matching
  `_effort: string | undefined,` positional param after `model` (line 493) to satisfy
  the interface; leave it unused (opencode has no effort knob — document the no-op).

### C. TUI — palette, override field, turn-start, status tag (Go)

- [ ] **`internal/tui/dashboard/transcript.go`** — add an `effortOverride string` field
  next to `modelOverride` (line 254), doc'd like it: in-session `/effort` selection sent
  as `TurnInput.Effort` on the next turn; stores the SDK wire value (`"max"` for the
  "ultracode" label); empty ⇒ default. In-memory per-attach, NOT parked/snapshotted
  (same lifecycle as `modelOverride`).
- [ ] **`internal/tui/dashboard/transcript.go`** — `startTurnCmd` (line 2635): change
  signature to `startTurnCmd(client RunnerClient, ref session.Ref, prompt, mode, model, effort string)`
  and set `Effort: effort` in the `session.TurnInput{...}` literal (line 2639). Update
  the call in `submitText` (line 1821) to pass `m.effortOverride` as the new trailing arg.
- [ ] **`internal/tui/dashboard/commands.go`** — add `setEffortCmd(level, label string)`
  modeled on `setModelCmd` (line 140): returns `func(m *TranscriptModel) tea.Cmd` that
  sets `m.effortOverride = level` and `m.appendBlock(blockInfo, "effort → "+label)`,
  returns `nil`. No `ctxLimit` recompute, no display-model mutation.
- [ ] **`internal/tui/dashboard/commands.go`** — add an `effortGroupCmds()` STATIC list
  and register `{name: "Effort", glyph: "⚡", cmds: effortGroupCmds()}` in
  `commandGroups()` right after the Model group (line 66). Entries (note the
  label→wire-value mapping):
  | palette command | label shown | `effortOverride` value |
  |---|---|---|
  | `/effort-low`       | low       | `low`   |
  | `/effort-medium`    | medium    | `medium`|
  | `/effort-high`      | high      | `high`  |
  | `/effort-xhigh`     | xhigh     | `xhigh` |
  | `/effort-ultracode` | ultracode | `max`   |
  | `/effort-auto`      | auto      | `""` (clears) |

  e.g. `{"/effort-ultracode", "max reasoning effort", setEffortCmd("max", "ultracode")}`.
  Typing `/effort` surfaces the whole group via the group-name substring match in
  `filteredGroups` (line 183).
- [ ] **`internal/tui/dashboard/statusline.go`** — add `effortTag(level string) string`
  near `permMode.modeTag` (line 69): `⚡` glyph + label, where `level=="max"` renders as
  **"ultracode"** (not "max"); hue by intensity (e.g. Malibu→Guac→Gold→Peach→Coral for
  low→max, or reuse `rampColor`). In `renderStatusLine`, after the mode tag is appended
  (line 419) add:
  ```go
  if m.effortOverride != "" {
      row1 += muted.Render(" · ") + effortTag(m.effortOverride)
  }
  ```
  **Gate on non-empty** so default output stays byte-identical (no golden churn).

### D. Tests + docs

- [ ] **`internal/tui/dashboard/statusline_ratelimit_test.go`** — update the two
  `startTurnCmd(...)` calls (lines 277, 290) to pass the new effort arg (e.g.
  `m.effortOverride`) — required to keep the package compiling.
- [ ] **`internal/tui/dashboard/commands_test.go`** — add a test asserting
  `/effort-high` → `effortOverride=="high"`, `/effort-ultracode` → `effortOverride=="max"`,
  and `/effort-auto` → `effortOverride==""`. Mirror the existing model/mode palette tests.
- [ ] **`internal/session/types_test.go`** — add `TestTurnInputEffortJSON` mirroring
  `TestTurnInputModeJSON` (line 220): `Effort:"max"` marshals to `"effort":"max"` and
  round-trips; empty effort is omitted.
- [ ] **runner** — if a unit-test harness exists for `claude.ts`, add a `resolveEffort`
  table test (valid passthrough, empty→undefined, garbage→undefined). Otherwise rely on
  `tsc --noEmit` for the type contract.
- [ ] **`docs/runner-api.md`** — document the new `effort` field in the
  `POST /sessions/:id/turns` section (after the `model` paragraph): the enum, that it's
  the per-turn `/effort` override, the silent-ignore-on-unsupported-model caveat, that
  empty ⇒ default, and that the TUI shows `max` as "ultracode". Add it to the example body.

---

## Risks / gotchas (don't skip)

- **tsc enum gate:** `options.effort` is `EffortLevel`, not `string`. MUST go through
  `resolveEffort` (or a cast, which is NOT recommended — it lets an invalid value reach
  the SDK). Raw-string assignment fails `just check`.
- **Signature break is compile-wide:** changing `startTurnCmd`'s positional signature
  breaks `transcript.go:1821` + the two test call sites
  (`statusline_ratelimit_test.go:277,290`). All in the same change or the package won't
  build.
- **Agent-interface fan-out:** adding the 9th positional param to `Agent.runTurn` forces
  `server.ts:169`, `claude.ts:492`, AND `opencode-turn.ts:486` to update together.
  Arg-order bugs are easy with 9 positional args — if this keeps growing, consider an
  options object later (out of scope here to keep the `/model` mirror).
- **No self-heal:** there's no `session.started` effort echo, so the status tag reflects
  only the request. Acceptable since the palette is the sole producer of valid values and
  the runner drops anything unknown.
- **Silent ignore on weak models:** `/effort ultracode` on Haiku/old models does nothing
  and gives no feedback. v1 ships without model-capability gating (see Stretch); the tag
  still shows because we have no capability signal forwarded yet.
- **Golden tests:** gating the tag on `effortOverride != ""` keeps default status-line
  output byte-identical, so `osc_signals_test.go` / `toast_overlay_test.go` / statusline
  goldens don't churn. Confirm before/after.
- **Don't touch `schema/events.json`** — the gen-drift gate only fires if it changes; this
  feature needs no event-model change.

### Stretch (optional, can fold in if cheap)

- **Honesty guard:** when `m.model` is not in the effort-capable set
  (Fable 5 / Opus 4.6+ / Sonnet 4.6), render the tag dim with an `·ignored` suffix so it
  can't imply reasoning the SDK will drop. Needs a small local capable-model predicate
  keyed on `m.model`.

---

## Deferred backlog (NOT this change)

Captured from the design panel; revisit separately, in priority order:

1. **Spinner-verb escalation** (`transcript.go` `workingStatus` ~line 1906) — the "bizarro
   soul"; verbs escalate by elapsed time + effort tier. Highest charm-per-line. Respect
   reduce-motion. `[S]`
2. **`alt+e` effort-cycle** — bump effort in place without opening the palette
   (homage to CC's Alt+T). `[S]`
3. **`/cost` + `/status`** — plain, clear readouts of the cost/token + session fields the
   model already tracks. **No receipt styling — just show the numbers legibly.** `[S]`
4. **`/copy`** over OSC 52 (`tui/terminal/osc.go`) — copy last assistant message. `[S/M]`
5. **`/context`** — colored grid of ctx usage reusing `blockBar`/`rampColor`. `[M]`
6. **Keyword escalation** — scan the outgoing prompt for `think → … → ultrathink/ultracode`
   and apply a one-shot per-turn effort that wins over the sticky override (doesn't mutate
   `effortOverride`). The honest home for `ultracode`/`ultrathink` as words. `[S/M]`
7. **`SANDBOX_EFFORT` session default** — `Spec.Effort` + k8s env injection + `RunnerConfig`,
   so `resolveEffort(turn, cfg.effort)` completes the mirror with `/model`'s persistence. `[M]`
8. **`/compact`** — biggest absolute CC gap; needs runner conversation surgery. `[L]`

Cut on review: `/8ball`, `/redline`, dinner-receipt cost styling.

---

## Verify before calling it done

```bash
just check          # full CI gate (gen drift, gofmt, lint, build, vet, tests, tsc, race)
```

In-sandbox caveat (from CLAUDE.md): `internal/runner`/`internal/models` bind httptest
ports the command-sandbox blocks — run `just test`/`just check` with the sandbox
disabled. The `internal/tui/dashboard` package tests fine in-sandbox. In a constrained
shell set `GOPATH=/tmp/gopath GOMODCACHE=/tmp/gomodcache GOFLAGS=-mod=mod
GOCACHE=$TMPDIR/go-build-cache`. Runner typecheck:
`cd runner && npm install --ignore-scripts && ./node_modules/.bin/tsc --noEmit`.

Don't commit/push unless asked.

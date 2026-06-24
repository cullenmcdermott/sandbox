# Chat UX overhaul — implementation plan

One plan covering the chat/transcript work we scoped together. Three independent
workstreams; **B is the highest-impact and lowest-effort** (a real correctness
bug — the model has been losing conversation history), so it should land first.

Design reference: `cmd/chatmock` (throwaway design lab). `go run ./cmd/chatmock`,
press `3` for the **Calm** variant we picked. Delete `cmd/chatmock/` once the real
changes land.

| WS | Theme | Maps to user concerns | Effort | Risk |
|----|-------|------------------------|--------|------|
| **A** | Calm chat styling (theme-coherent markdown + de-busied chrome + real images + agent brand glyphs) | #4 styling, #5 too busy | M | low |
| **B** | Conversation continuity (resume threading) | #2 model switch, #3 mid-convo drop | **S** | medium |
| **C** | Instant attach + honest loading state | #1 replay-feels-like-work | L | medium |

---

## Workstream B — conversation continuity (do this first)

### Root cause (confirmed in code + the SDK type defs)
The runner **never resumes the Claude SDK conversation** for a normal turn, so
every user prompt after the first starts a brand-new, history-less `query()`.

- `buildOptions` only sets `options.resume` when the per-turn `resume` arg is
  truthy — `runner/src/claude.ts:166-171`. No `continue`, no `sessionId`.
- That arg is `body.resume` — `runner/src/server.ts:168`.
- The Go TUI never populates it: `session.TurnInput{Prompt, Mode, Model}` —
  `internal/tui/dashboard/transcript.go:1995`. `TurnInput.Resume` is dead.
- The runner *captures + persists* the resumable id (`claude_session_id` in
  `session.json` on the PVC; SDK transcript under
  `CLAUDE_CONFIG_DIR=/session/state/claude`, also PVC — `runner/src/types.ts:147`)
  but only feeds it back in the **title summarizer** (`claude.ts:595`), never in
  the user turn. Capture is guarded to "only if unset" — `claude.ts:677`.
- SDK semantics (`@anthropic-ai/claude-agent-sdk@0.3.181`,
  `node_modules/.../sdk.d.ts`): `resume` loads history (default off); `continue`
  continues most-recent-in-cwd (default off); with `forkSession` false (default)
  `resume` *appends* to the same session. We set none of them.

**Why it looked intermittent:** the TUI replays the old events from the runner's
SQLite log over SSE, so the transcript *looks* continuous while the model behind
it has amnesia. A long turn-1 (many tool calls in one `query()`) has full context;
the first thing that exposes the gap is turn-2 — which is exactly when the user
typed `/opus`. The model override itself is wired correctly (`claude.ts:163-164`);
it's not the cause. This also explains #3's "dropped after the pod-terminating
banner": restart reloads `claude_session_id` and replays the transcript, but the
next turn still sends no resume → fresh session again.

This is almost certainly a **lost-wiring regression**: `runner/src/index.ts:5-6`
documents that turns "continue the same Claude session via resume", and the e2e
test only ever drives **one** turn (`internal/e2e/turn_e2e_test.go:122`), so
multi-turn continuity was never exercised.

### Spike first (½ day, gates the fix)
Before changing code, confirm empirically against the live cluster:
1. **Does turn-2 currently have zero memory of turn-1 in one live pod?** Two
   prompts, second referencing the first. Confirms "every turn", not "random".
2. **On `resume`, does the SDK's `init` message report the SAME `session_id` or a
   new one?** Decides capture-once vs capture-latest below.
3. **Does `resume` carry history AND honor a *different* `options.model`?** i.e. a
   `/opus` turn resuming a Sonnet-created session sees the history and runs as Opus.
   (Expected yes — resume loads the transcript, model applies going forward.)
4. **Does resume survive suspend/resume + pod restart?** (Expected yes — id +
   transcript both on PVC.)

### Fix (small, backend-local)
1. `runner/src/claude.ts buildOptions`: default resume to the persisted id when the
   client didn't supply one —
   `const effectiveResume = resume || reg.state.claude_session_id; if (effectiveResume) options.resume = effectiveResume;`
   First turn: id is `''` → resume omitted → fresh session, then captured. Every
   later turn (and every turn after a restart): resumes. Fixes #2 and #3 together.
   *Do not* use `continue: true` — cwd-based continue is fragile across restarts
   and concurrent sessions; explicit resume-by-id is deterministic.
2. `claude.ts:677` capture-once → **capture-latest**: `if (result.claudeSessionId) reg.setClaudeSession(result.claudeSessionId)`
   so the chain follows the live resumable head instead of pinning to turn-1's id.
   (Strictly required only if the spike shows the id changes on resume; harmless
   otherwise.)
3. Gate to the claude-sdk backend only (opencode has no runner turn path —
   `selectAgent` returns null; confirm no regression).

### Tests (the oracle that was missing)
- Runner unit: `buildOptions` sets `options.resume` from `reg.state.claude_session_id`
  when `body.resume` absent; omits it when the id is empty.
- **Multi-turn e2e** (new): drive 2 turns, assert turn-2's `query()` received
  `resume = <turn-1 id>`; ideally a behavioral check that turn-2 can reference
  turn-1. This is the hard-to-fake counter that would have caught the regression.
- Restart test: reload `session.json`, assert the id is passed as resume.

### Risks / open
- Host-path migration in `TODO.md` (cwd `/session/workspace/<path>` → real host
  path): an old `claude_session_id` could point at a transcript under the old
  project dir, so `resume` fails. Need a **fail-soft**: if `query()` errors on a
  stale resume id, retry once without resume (start fresh) rather than erroring the
  turn. Add before enabling resume by default.

---

## Workstream A — Calm chat styling

Two halves: **(1) theme-coherent markdown** (fixes "doesn't fit my theme" /
"looks unrendered") and **(2) de-busied chrome** (fixes "too busy"). Plus the
**real-image** finding and the **agent brand glyphs** (A4). All additive; no
event-model or protocol changes.

### A1 — Theme-derived glamour (biggest single win)
Today `internal/tui/dashboard/chat/markdown.go:28-31` hardcodes
`glamour.WithStandardStyle("dark")` — a generic ANSI palette unrelated to the
Midnight tokens (verified: stock inline code `48;5;236`, heading `39`, H1 paints a
`63` purple block; literal `## ` heading prefixes are kept → reads as "unrendered").

- Add a `themedStyleConfig() ansi.StyleConfig` in the `chat` package derived from
  `tui/theme` tokens (Charple headings + drop the `## ` prefixes, Peach-on-Raised2
  inline code, Malibu links, theme-mapped chroma). Reference implementation:
  `cmd/chatmock/style.go` — lift it almost verbatim.
- Swap `WithStandardStyle("dark")` → `glamour.WithStyles(themedStyleConfig())`.
- **Theme-swap invalidation:** the `pool map[int]*TermRenderer` (`markdown.go:10`)
  is keyed by width only and would serve stale colors after `/theme`. Either key
  the pool by `width + theme.Active()` (as the mockup does) or clear it from a
  `theme.OnChange` hook. The streaming renderer reuses this pool, so it inherits
  the fix for free.
- Verify against the golden transcript tests
  (`internal/tui/dashboard/golden_transcript_test.go`) — they'll need regolden.

### A2 — De-busied chrome (the "Calm" variant)
Driven from `transcript.go`. In rough priority of impact-per-effort:

1. **Role gutter instead of inline prefix + per-turn footer noise.** `renderBlock`
   (`transcript.go:614-680`): assistant/user blocks get a slim role-colored bar
   (Charple / Guac) down the left + 1 col pad, replacing the `❯ ` prefix
   (`:617`). Calm reference: `cmd/chatmock/render.go` `gutterLines`.
2. **Footer only on the latest turn.** Today a `blockFooter` is appended on every
   `EventTurnCompleted` (`transcript.go:1632-1638`). Drop the previous footer block
   when a new turn starts (or render footers dim+slim). Removes the repeated
   `◇ Opus 4.8 · via claude · …` lines.
3. **One blank line between turns** (`turnGap`) — purely additive in the block
   list build.
4. **Muted tool cards.** `renderToolCard` (`transcript.go:780-812`): name in
   `TextSecondary` instead of bold Malibu; keep the status icon colored.
5. **Collapse the status block** (`renderStatusLine`, `statusline.go`). The 4-row
   status (model/path/ctx, 5h/weekly, "usage limits unavailable", "auto mode on")
   is the single biggest "busy" contributor in the screenshot. Collapse to one
   line: `opus 4.8 · ~/path · 19k/1m (2%) · $0.03 · auto`; move the rate-limit
   detail behind a keypress or only-when-present. **Biggest visual win, but
   touches the densest file — scope as its own commit** with the statusline tests
   (`statusline_*_test.go`) updated.

Keep each of 1–5 a separate commit so we can tune in isolation (the mockup lets us
preview the combination first).

### A3 — Real inline images (Kitty), #4
Feasible by **reuse** — the repo already ships the encoder
(`tui/terminal/kitty.go`: `KittyTransmitRGBA` + `KittyPlaceholders`) and the
frame-splice pattern (`app.go:738`, `statusline.go:197`). A production version
decodes a PNG → RGBA and feeds the same path.

**Blocker found while building the mockup:** `KittyPlaceholders` writes a *column*
diacritic on every cell and `diacritic()` clamps into a **32-entry** table
(`kitty.go:50`). Fine for the 10-col ctx gauge, but **any placement wider than 32
cells is garbled** (cols 32+ collapse onto index 31). Fix before real images:
emit the column diacritic only on the first cell of each row and let the terminal
auto-increment (so only row-count is table-bounded), or extend the diacritic
table. Reference: `cmd/chatmock/kittyimg.go` `placeholdersWide`. This is a
prerequisite, scoped as a small `internal/terminal` change with a unit test.

Scope note: image *rendering* is the demo; deciding *where images come from* in a
real session (tool outputs? pasted? agent-generated files?) is a separate product
question — flag it, don't build it yet.

### A4 — Agent brand glyphs (custom per-backend identity)
A custom glyph vocabulary so each agent backend is identifiable *at a glance*,
used as both decoration and a stable identity axis. This is **orthogonal to the
status glyph** (`GlyphBusy`/`GlyphIdle`/… in `tui/theme/styles.go`): status says
*what a session is doing*; the brand mark says *which agent it is* (favicon, not
state). All lives in a new `tui/theme/brand.go` (theme-only, no `session`
dependency) + thin mapping helpers in the dashboard.

A full reference implementation already exists in the working tree (the design
exploration) — lift it; the spec below is what to (re)build and the traps to
avoid.

**The vocabulary** (in `tui/theme/brand.go`):
- One-cell **marks**, for inline use in rows/labels (must be exactly 1 cell wide):
  `MarkClaude = "✳"` (U+2733, the Anthropic spark) in `Peach`; `MarkOpenCode =
  "▦"` (U+25A6, a pixel-grid square) in a monochrome bright tone. opencode's real
  brand (opencode.ai) is high-contrast **monochrome pixel blocks**, so it reads
  cool/grey next to Claude's warm spark — the two never blur.
- Block-pixel **mascots** (multi-line, quadrant-block art à la the Claude Code CLI
  robot) for the connecting splash: `MascotClaude` (`▐▛███▜▌` "guy") in the
  Peach→Coral gradient; `MascotOpenCode` (a pixel "OC" monogram) in a monochrome
  grey gradient. Every line same display width so they center cleanly.
- Brand tones are **functions** (`BrandClaude()`/`BrandOpenCode()`), not vars, so
  they re-read the active palette after `/theme` (same rule as the spinner).
- Gradients go through the existing `GradientText` (degrades to a solid brand tone
  on 16-color/NO_COLOR), applied per-line by a `gradientBlock` helper so multi-line
  art keeps its shape.
- **Dropped from the exploration** (don't rebuild): a 5-line sunburst logo and a
  line-art "face" — both rejected in review. Block mascots only.

**Dashboard mapping helpers** (in `internal/tui/dashboard/session.go`, alongside
`ClientLabel`): `BackendMark(b)` → pre-colored 1-cell mark or `""`; `BackendGlyph(b)`
→ *uncolored* glyph (caller styles it); `BackendColor(b)` → `(color.Color, bool)`;
`MarkedClientLabel(b)` → `"✳ claude"` canonical text tag. `""`/bare fallback for
unknown backends so we never render tofu.

**Consistent placement** — the mark appears everywhere an agent surfaces, each in
its brand color:
- backend **picker** rows (`backend_picker.go`, `renderBackendPicker`) — mark
  between chevron and label.
- **session list** row sub-line (`model.go`, the `sub`/`line2` build): mark in the
  6-col gutter under the title.
- **detail pane** `agent` KV row (`model.go`, `kvPairs`) → `MarkedClientLabel`.
- **transcript footer** (`transcript.go`, `turnFooter`) → `via ✳ claude`.
- **cluster strip** backend mix (`zones.go`, `backendMix`) → `✳ claude 2 · ▦ opencode 1`.
- **toast / permission queue** (`notify.go`, `permqueue.go`) — also fixes a latent
  bug: these showed the raw `claude-sdk` id instead of the friendly `claude`.
- **connecting splash** (`app.go`, `connectingView`) — mascot above the title,
  chosen by `connectingOpencode`.

**Traps (all hit while building the reference impl):**
1. `truncate` (`model.go`) is **rune-based, not ANSI-aware** — a pre-colored mark
   fed through it corrupts mid-escape. Compose the colored mark *outside* truncate.
2. **Selected-row background:** the list sub-line fills `Width(width)` with a
   `Raised` background when selected. Render the gutter mark as its own styled cell
   carrying that same background so the row fills flush (don't let the mark punch a
   hole). Use `BackendGlyph`+`BackendColor`, not the pre-colored `BackendMark`, here.
3. **Color bleed on join:** a pre-colored mark ends with a reset (`\x1b[m`) that
   clears the surrounding style for everything after it. In single-`Render` joins
   (`backendMix`, KV) self-style each part (mark + its own `val.Render(rest)`) and
   join with a styled separator, rather than wrapping the whole string once.
4. **Toast fade** (`notify.go`, `renderToast`) blends every color toward the page
   background per-frame. A pre-colored mark won't fade → use the **uncolored**
   `BackendGlyph` there so it fades uniformly with the toast.
5. **Goldens:** `TestGoldenDashboard` + `TestGoldenTranscriptStream` capture these
   surfaces — regolden with `-update` (the only diffs should be the new marks).

Scope as **one commit** (it's cohesive and additive). Preview in a real TTY (not
piped — gradients need a truecolor profile) before regolden.

---

## Workstream C — instant attach + honest loading state (#1)

### Root cause (confirmed)
1. **Full replay every attach.** The foreground transcript builds with `lastSeq=0`
   (`NewTranscript`, `transcript.go:277-296`) even though `sess.lastSeq` (the
   resume cursor) exists — so `startEventStream` requests `after=0` and the runner
   streams the entire history (`server.ts:116-130`, `events.ts:208-256`). The
   background *list* stream already resumes from `sess.lastSeq`
   (`model.go:843-857`) — the cursor machinery exists, the transcript just ignores
   it. There is **no on-disk transcript cache** — `internal/index` stores only a
   list snapshot + `LastEventSeq` (`index.go:60-112`).
2. **Replay drives the live busy UI.** A replayed `EventTurnStarted` calls
   `beginTurn()` → `StatusBusy` + working spinner (`transcript.go:1571-1576`,
   `1393-1398`), with no replay/live boundary. So catching up an old chat animates
   "working…" as if the model were live.

### Fix
1. **Host-side transcript cache.** Append-only event log per session under
   `~/.local/share/sandbox/remote-sessions/<id>/events.ndjson` (next to the
   existing `session.json`), written as the transcript observes events. On attach:
   load cache → rebuild blocks (in non-live mode, see #2) → set `lastSeq` to the
   max cached seq → `startEventStream(after=lastSeq)` streams only the delta.
   Attach becomes instant and offline-tolerant. (Quick interim win even without the
   cache: thread `sess.lastSeq` into `NewTranscript` so a warm reattach doesn't
   replay from 0.)
2. **Replay/live boundary + loading state.** Add `m.replaying bool`. Boundary
   signal — pick one:
   - *(preferred)* a runner-emitted sentinel after `replayTo` (a typed
     `stream.live` event in `schema/events.json`, or a `: replay-complete` SSE
     comment surfaced by `client.go` — today it drops non-`data:` lines,
     `client.go:291-294`), or
   - a seq watermark = runner `lastSeq` captured at attach.
   While `replaying`: gate `maybeStartWorking()` (`transcript.go:374,1412`) and the
   `workingStatus()` render (`:948`) on `live && turnActive`, and show a distinct
   **"loading transcript… N/M"** state in the header (the mockup's `l`-key phase
   demonstrates the look). After the boundary, carry the final replayed
   `turnActive` across so a genuinely in-flight turn still ends up "working".

### Risks
- Boundary off-by-one: a session attached mid-turn must end "working" after the
  boundary — carry state across, don't force false.
- Cache coherence: append-only keyed by seq, tolerate gaps/dupes (the runner
  re-emits `message.completed` at a higher seq on reconnect — the
  `droppedPartialIdx` logic at `transcript.go:133-136` assumes live ordering;
  replaying cached events must not double-render).

---

## Suggested sequencing
1. **B** — spike (confirm the 4 questions) → resume fix + fail-soft + multi-turn
   e2e. Restores conversation memory; small surface.
2. **A1 + A3-blocker** — theme-derived glamour + the placeholder width fix. High
   visual payoff, low risk.
3. **A2** — Calm chrome, commit-by-commit, previewing combos in `cmd/chatmock`.
   Status-line collapse last.
4. **A4** — agent brand glyphs (one commit, additive; lift the reference impl).
   Independent of A1–A3; can slot anywhere in the A block.
5. **C** — transcript cache + loading boundary. Largest, most independent.

Open product calls to make as we go: how aggressively to collapse the status line
(A2.5), and where real images originate (A3).

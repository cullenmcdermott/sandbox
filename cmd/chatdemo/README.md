# chatdemo

A self-contained example that reproduces the polished Sandbox **chat transcript**
using only the public `github.com/cullenmcdermott/sandbox/tui/...` packages (plus
the public `client` for the event vocabulary) — it imports nothing from
`internal/`, and in particular none of `internal/tui/dashboard`. It's the
runnable proof that the public TUI + SDK packages are enough for an external
Bubble Tea v2 app to build the transcript experience.

```bash
go run ./cmd/chatdemo
```

Crucially, the transcript is **event-sourced**: the demo feeds a scripted stream
of public `client.Event` values into `tui/transcript`, exactly as a real host
feeds the SSE event stream — nothing is hand-assembled. The interactive
components do the rest:

- **`tui/transcript`** — the reducer: `Apply(client.Event)` mutates it, and it
  renders the conversation through `tui/chat` items in a `tui/list` virtual list
  (tool pairing, subagent routing, streaming coalescing, todos, permissions,
  follow mode, focus, expansion, theme invalidation, markdown caching).
- **`tui/composer`** — the multi-line input: type + `enter` to send, queue-while
  -busy steering, the escape cascade, and prompt history. Its submissions feed
  back into the transcript via `Submit`.
- **`tui/chrome`** — the live working indicator that frames the conversation.
- **`tui/theme`** — semantic color tokens + a live theme swap.
- **`tui/terminal`** — the OSC tab-progress signal while a turn streams.

The scripted turn plays out as events: session start, a user prompt, a reasoning
block, a running → completed tool card, a todo checklist, a permission request
that resolves, and a streaming-markdown assistant reply — then a per-turn footer
(`◇ model · via backend · elapsed · ↑in ↓out · cost`).

Everything is mocked — the value is that the event reduction, streaming,
version-cached list rendering, ANSI/width/grapheme-safe wrapping, theme swap,
scrolling, and responsive layout are all real public component code.

**Keys:** type + `enter` to send (queues while the scripted turn runs) · `r`
replay · `ctrl+o` expand/collapse the latest tool · `ctrl+t` swap theme ·
`↑`/`↓`/`pgup`/`pgdn` scroll · `esc` interrupt/steer · `q` quit.

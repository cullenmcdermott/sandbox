# AGENTS.md

This repo's canonical agent instructions live in **[CLAUDE.md](./CLAUDE.md)**.
`AGENTS.md` exists only as the cross-tool entry point that many agents look for;
it intentionally duplicates nothing.

Quick orientation:

- **Read first:** [CLAUDE.md](./CLAUDE.md) — what this is, architecture, key decisions.
- **Backlog:** [TODO.md](./TODO.md) — outstanding work, each with a file:line pointer.
- **Before you call work done:** run `just check` (the full gate CI also runs).
  See [docs/verification-protocol.md](./docs/verification-protocol.md).
- **Docs:** durable references vs point-in-time plans, and when to update or
  archive each — see the "Docs map & lifecycle" section of [CLAUDE.md](./CLAUDE.md).
- **Event model:** `schema/events.json` is the source of truth — edit it, run
  `just gen`, never hand-edit a `*.gen.*` file. See the "Event model" section of
  [docs/architecture.md](./docs/architecture.md).

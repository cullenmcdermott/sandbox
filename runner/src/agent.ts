// Agent backend seam. The runner is the translation layer between a concrete
// coding-agent implementation (Claude Agent SDK, OpenCode, Codex) and the
// normalized HTTP+SSE API the CLI consumes. Each backend implements this one
// interface; everything else in the runner — the turn registry, idle clock,
// permission flow, event log, SSE replay — is backend-agnostic and reused as-is.
//
// To add a backend (e.g. `opencode serve`):
//   1. write `opencode.ts` exporting `const opencodeAgent: Agent = { runTurn }`
//      that drives the backend and maps its events into the normalized model
//      (appendEvent), reusing the session registry's registerTurn/finishTurn
//      and permission registry so the idle clock and reaper keep working;
//   2. add a case to selectAgent below.

import type { RunnerConfig } from './session.js';
import { claudeAgent } from './claude.js';

export interface Agent {
  // runTurn runs a single user-prompted turn to completion: it streams the
  // backend's output as normalized events (via appendEvent), drives the
  // permission flow through the session registry, and calls finishTurn when
  // done. It must honor `abort` (client interrupt) and never throw for an
  // aborted turn — emit `turn.interrupted` instead. Resolves when the turn
  // fully settles; the caller has already registered the turn.
  runTurn(
    cfg: RunnerConfig,
    turnId: string,
    prompt: string,
    resume: string | undefined,
    allowedTools: string[] | undefined,
    mode: string | undefined,
    model: string | undefined,
    abort: AbortController,
  ): Promise<void>;
}

// selectAgent returns the Agent implementation for a backend id (the value of
// SANDBOX_BACKEND / Spec.Backend), or null for backends that are NOT driven
// through the runner's turn path. `opencode-server` is such a backend: the
// runner only supervises `opencode serve` (see opencode.ts) while the local
// `opencode attach` client drives turns directly, so it has no Agent and its
// /turns route is rejected with 409. An unknown backend still throws so the
// runner fails fast at startup rather than at first turn.
export function selectAgent(backend: string): Agent | null {
  switch (backend) {
    case 'claude-sdk':
      return claudeAgent;
    case 'opencode-server':
      return null;
    default:
      throw new Error(`unsupported backend: ${backend} (known: claude-sdk, opencode-server)`);
  }
}

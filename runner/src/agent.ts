// Agent backend seam. The runner is the translation layer between a concrete
// coding-agent implementation and the normalized HTTP+SSE API the CLI consumes.
// Each turn-driving backend implements this one interface; everything else in
// the runner — the turn registry, idle clock, event log, SSE replay — is
// backend-agnostic and reused as-is.
//
// Only opencode-server drives turns through the runner today (its headless
// first-turn bridge, opencode-turn.ts); the interactive agents (claude-pane,
// codex-app-server) are supervise-only and return null. The claude Agent SDK
// turn engine was removed with claude-pane-first — claude sessions are now the
// interactive Claude Code TUI in the pane, not a runner-driven SDK query loop.

import type { RunnerConfig } from './session.js';
import { opencodeAgent } from './opencode-turn.js';

export interface Agent {
  // runTurn runs a single user-prompted turn to completion: it streams the
  // backend's output as normalized events (via appendEvent), drives the
  // permission flow through the session registry, and calls finishTurn when
  // done. Resolves when the turn fully settles; the caller has already
  // registered the turn.
  //
  // [V40] Abort ownership: runTurn must honor `abort` and never throw for an
  // aborted turn, and it must NOT emit any terminal (turn.interrupted/completed/
  // failed) for an aborted turn — it settles SILENTLY. The interrupt INITIATOR
  // owns the terminal: the server.ts /interrupt route emits turn.interrupted,
  // and the SIGTERM shutdown path does so too (index.ts, via
  // shutdownInterruptedEvents). Both shipping agents follow this (claude.ts R3,
  // opencode-turn.ts). A backend that emits turn.interrupted itself on abort
  // would double-emit the terminal on every route-driven interrupt.
  //
  // `mode` is the tool-approval policy (Go: TurnInput.ApprovalPolicy — 'default'
  // | 'acceptEdits' | 'plan' | 'bypassPermissions'). It is mapped PER-BACKEND: the
  // claude-sdk backend applies it as the SDK permissionMode; a backend that owns
  // its own permission surface (opencode-server, whose interactive client drives
  // the modal) does not honor it and MAY ignore it — documented here so a
  // non-honoring backend is an explicit contract, not a silent drop.
  runTurn(
    cfg: RunnerConfig,
    turnId: string,
    prompt: string,
    resume: string | undefined,
    allowedTools: string[] | undefined,
    mode: string | undefined,
    model: string | undefined,
    effort: string | undefined,
    abort: AbortController,
  ): Promise<void>;
}

// selectAgent returns the Agent implementation for a backend id (the value of
// SANDBOX_BACKEND / Spec.Backend), or null for backends that are NOT driven
// through the runner's turn path. Two backends implement the turn seam: claude-sdk
// via the Claude Agent SDK, and opencode-server by bridging to the in-pod
// `opencode serve` over its HTTP API (opencode-turn.ts) — additive to the
// interactive `opencode attach` path, which still talks to the same server.
//
// codex-app-server and claude-pane are SUPERVISE-ONLY: the runner supervises the
// backend's own interactive process (codex.ts / claude-pane.ts) plus, for codex,
// a passive metrics observer, but turns are driven by the interactive client
// (the `codex` TUI over the app-server's loopback WebSocket, or the `claude` PTY
// over GET /sessions/:id/pane), never through the runner's turn path — so both
// return null. server.ts guards a null agent (POST /turns 409s) exactly as for
// any supervise-only backend.
//
// claude-sdk is a RETIRED backend id (claude-pane-first removed the SDK turn
// engine): it returns null too, so a lingering old claude-sdk pod boots and
// serves status/idle but 409s on /turns rather than crashing. An unknown
// backend throws so the runner fails fast at startup rather than at first turn.
export function selectAgent(backend: string): Agent | null {
  switch (backend) {
    case 'opencode-server':
      return opencodeAgent;
    case 'codex-app-server':
    case 'claude-pane':
    case 'claude-sdk':
      return null;
    default:
      throw new Error(
        `unsupported backend: ${backend} (known: opencode-server, codex-app-server, claude-pane, claude-sdk)`,
      );
  }
}

// Shared turn-start path. Both the POST /sessions/:id/turns route and the
// runner-side autopilot driver reserve the single turn slot and fire a backend
// turn through THIS function, so both go through the SAME 409 gate
// (turnRejectReason) and the SAME completion signal (turnSettledHandler) rather
// than the driver HTTP-looping back to the server. Keeping the gate + fire in one
// place is what makes a self-submitted turn indistinguishable from a manual one
// downstream (idle clock, event log, audit).

import { appendEvent } from './events.js';
import { getRegistry } from './session.js';
import type { RunnerConfig } from './session.js';
import type { Agent } from './agent.js';

/**
 * Decide whether a POST /turns (or a driver self-submit) must be rejected with
 * 409, and with what message, given the backend and the session's live state.
 * Pure + exported so the gate is unit-testable without the http server (F4).
 *
 * Two ways a session can already be busy:
 *  - a registered runner turn (activeTurnCount > 0) — the R4 single-active-turn
 *    invariant; two overlapping query() calls interleave events.
 *  - B2: an interactive opencode turn. It runs INSIDE `opencode serve`, driven by
 *    the attached client, and never registers in activeTurns — it only surfaces
 *    as status:'busy' via the passive observer. Without this check a headless
 *    POST /turns is accepted mid-interactive-turn and opencode-turn.ts prompts the
 *    SAME session concurrently, freezing the observer's open-cycle mapper. Mirror
 *    of the interrupt route's `backend === 'opencode-server' && status === 'busy'`.
 *
 * Returns the 409 error message, or null when the turn may proceed.
 */
export function turnRejectReason(
  backend: string,
  activeTurnCount: number,
  status: string,
): string | null {
  if (activeTurnCount > 0) {
    return 'a turn is already active; interrupt it before starting a new one';
  }
  if (backend === 'opencode-server' && status === 'busy') {
    return 'the opencode session is busy; interrupt the active turn before starting a new one';
  }
  return null;
}

/** Per-turn overrides carried on a start. Mirrors the TurnRequestBody fields the
 * agent's runTurn accepts (resume + mode/model/effort + allowedTools). */
export interface StartTurnOptions {
  resume?: string;
  allowedTools?: string[];
  mode?: string;
  model?: string;
  effort?: string;
}

/** A turn was reserved and fired (turnId), or the 409 gate rejected it. */
export type StartTurnResult = { turnId: string } | { rejected: string };

// The autopilot driver hooks turn completion here. Called AFTER a turn fully
// settles (agent.runTurn resolved/rejected AND its own finally ran finishTurn),
// for every turn started through startTurn — manual POST /turns turns included,
// so a manual turn transparently counts as a free loop iteration (ADR H2). A
// single handler is enough (one session per pod). Null when no driver is wired
// (non-claude backends).
let turnSettledHandler: ((turnId: string) => void) | null = null;

/** Register (or clear with null) the turn-settled handler. The autopilot wiring
 * sets this at driver creation; tests clear it in cleanup. */
export function setTurnSettledHandler(fn: ((turnId: string) => void) | null): void {
  turnSettledHandler = fn;
}

/**
 * Reserve the single turn slot and fire the backend turn (fire-and-forget),
 * returning the assigned turnId — or a `rejected` reason when the 409 gate says
 * a turn is already active. The check-and-reserve is synchronous (no await
 * between turnRejectReason and registerTurn) so two near-simultaneous starts
 * can't both observe an empty slot (TOCTOU / R4). On settle, notifies the
 * autopilot driver via turnSettledHandler.
 */
export function startTurn(
  cfg: RunnerConfig,
  agent: Agent,
  prompt: string,
  opts: StartTurnOptions = {},
): StartTurnResult {
  const reg = getRegistry();
  const rejectReason = turnRejectReason(cfg.backend, reg.activeTurns.size, reg.state.status);
  if (rejectReason) return { rejected: rejectReason };
  const turnId = reg.nextTurnId();
  reg.setLastTurn(turnId);
  const turn = reg.registerTurn(turnId, prompt);
  // Fire and forget: the turn runs in the background, streaming events to SSE
  // clients. Callers get the turnId immediately.
  agent
    .runTurn(cfg, turnId, prompt, opts.resume, opts.allowedTools, opts.mode, opts.model, opts.effort, turn.abort)
    .catch((err) => {
      const message = err instanceof Error ? err.message : String(err);
      appendEvent(cfg.sessionId, turnId, 'error', { message });
      reg.finishTurn(turnId);
    })
    .finally(() => {
      // The turn has fully settled (runTurn's finally already ran finishTurn).
      // Notify the autopilot driver so it can increment iterations / scan the
      // sentinel / schedule the next tick (or retry). Best-effort: a throwing
      // handler must not become an unhandled rejection.
      try {
        turnSettledHandler?.(turnId);
      } catch (err) {
        console.error('turnSettledHandler threw (non-fatal):', err);
      }
    });
  return { turnId };
}

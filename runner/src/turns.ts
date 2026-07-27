// Shared turn-start path: reserve the single turn slot behind the 409 gate
// (turnRejectReason) and fire a backend turn.
//
// It had two callers — POST /sessions/:id/turns and the runner-side autopilot
// driver, which is why the gate and the fire live in one place rather than the
// driver HTTP-looping back to the server. claude-pane-first deleted the driver
// (and the SDK turn engine with it), so the route is now the ONLY caller and its
// only live consumer is the opencode headless first-turn adapter. The
// single-place structure is kept anyway: it is what makes any future
// self-submitted turn indistinguishable from a manual one downstream (idle
// clock, event log, audit).

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

/**
 * [V18] The turn.interrupted events a graceful shutdown (SIGTERM) must append —
 * one per active turn — BEFORE session.terminating. Both agents' runTurn
 * deliberately emit nothing terminal on abort (R3: the interrupt INITIATOR owns
 * turn.interrupted). The /interrupt route is one initiator; a SIGTERM-driven
 * shutdown is the other. Without this, a mid-turn suspend that aborts cleanly
 * within the grace window leaves the log ending with turn.started + deltas and
 * NO turn terminal, so replay after resume shows tool cards spinning forever.
 *
 * Pure + exported so the ordering/payload contract is unit-testable (index.ts's
 * shutdown() isn't importable — it runs main() at module load). Each descriptor
 * carries the turnId for the event envelope and the payload shape server.ts:405
 * emits, with a SIGTERM-specific reason.
 */
export function shutdownInterruptedEvents(
  activeTurnIds: string[],
  signal: string,
): Array<{ turnId: string; payload: { reason: string } }> {
  return activeTurnIds.map((turnId) => ({
    turnId,
    payload: { reason: `pod terminating (${signal})` },
  }));
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

/**
 * Reserve the single turn slot and fire the backend turn (fire-and-forget),
 * returning the assigned turnId — or a `rejected` reason when the 409 gate says
 * a turn is already active. The check-and-reserve is synchronous (no await
 * between turnRejectReason and registerTurn) so two near-simultaneous starts
 * can't both observe an empty slot (TOCTOU / R4).
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
  try {
    const turn = reg.registerTurn(turnId, prompt);
    // Fire and forget: the turn runs in the background, streaming events to SSE
    // clients. Callers get the turnId immediately.
    agent
      .runTurn(cfg, turnId, prompt, opts.resume, opts.allowedTools, opts.mode, opts.model, opts.effort, turn.abort)
      .catch((err) => {
        const message = err instanceof Error ? err.message : String(err);
        appendEvent(cfg.sessionId, turnId, 'error', { message });
        reg.finishTurn(turnId);
      });
  } catch (err) {
    // [V42] registerTurn does activeTurns.set THEN setStatus('busy'), which
    // persists session.json — an unguarded write that can throw (ENOSPC/EROFS
    // on the PVC) AFTER the slot is reserved but BEFORE runTurn fires. Nothing
    // else deregisters that entry, so the single turn slot wedges: every later
    // POST /turns 409s and the idle reaper can't suspend the pod until restart.
    // Free the slot and re-throw so the route replies 500 with the slot open.
    // A secondary persistence throw from finishTurn is swallowed — the
    // activeTurns.delete inside it already ran, so the slot is freed regardless.
    try {
      reg.finishTurn(turnId);
    } catch {
      /* best-effort slot release; the map entry was already deleted */
    }
    throw err;
  }
  return { turnId };
}

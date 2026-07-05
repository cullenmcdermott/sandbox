// §7 opencode-observer hardening:
//
//   1. Bound a stuck synthetic 'busy'. recomputeIdle()/idleStatus() treat an
//      observer-set status='busy' (an interactive opencode turn — no registered
//      runner turn) as an active turn, which keeps the idle reaper off. A wedged
//      mapper / missed `session.idle` would otherwise pin 'busy' forever and make
//      the pod unreapable. A STALE synthetic busy (no observer events for
//      SYNTHETIC_BUSY_STALE_MS, 5 min) with nothing attached must release to the
//      reaper; a FRESH one must stay active.
//
//   2. GC the module-global `interruptedTurns` set. Its entry is only shed on the
//      turn's own `session.idle`; a stream drop (reset) in between leaks it.
//      reset()/endCycle() must clear it.
//
// The staleness clock is injectable (noteObserverEvent(atMs)) so these backdate
// it instead of waiting out the 5-minute window.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Event } from '@opencode-ai/sdk';

import { initRegistry, reviveSessionState, setExternalActivityProbe, type RunnerConfig } from '../src/session.js';
import {
  createObserverHandler,
  markObservedTurnInterrupted,
  hasInterruptedTurn,
  type ObserverDeps,
} from '../src/opencode-observer.js';

const cfg: RunnerConfig = {
  sessionId: 'oc-stale',
  backend: 'opencode-server',
  projectPath: '/proj',
  runnerToken: 't',
};

// 6 minutes ago — safely past the 5-minute SYNTHETIC_BUSY_STALE_MS window.
const staleAgo = (): number => Date.now() - 6 * 60_000;

/** A registry for an opencode session that the observer has flipped to a
 * synthetic 'busy' (status set directly, mirroring the observer's cycle-start
 * setStatus while activeTurns stays empty). */
function busyOpencodeRegistry() {
  const reg = initRegistry(
    reviveSessionState(
      { sandbox_session_id: 'oc-stale', backend: 'opencode-server', project_path: '/proj', status: 'idle', last_turn_id: 'turn-1' },
      cfg,
    ),
  );
  reg.state.status = 'busy';
  return reg;
}

test('fresh synthetic busy counts as an active turn (reaper stays off)', () => {
  const reg = busyOpencodeRegistry();
  reg.noteObserverEvent(); // observer just mapped an event → clock fresh
  const s = reg.idleStatus();
  assert.equal(s.turnActive, true, 'fresh synthetic busy is active');
  assert.equal(s.idleSince, undefined, 'no idleSince while active — reaper waits');
});

test('stale synthetic busy with nothing attached releases to the reaper', () => {
  const reg = busyOpencodeRegistry();
  reg.noteObserverEvent(staleAgo()); // last observer event was 6 min ago
  const s = reg.idleStatus();
  assert.equal(s.turnActive, false, 'stale synthetic busy no longer counts as an active turn');
  assert.ok(s.idleSince, 'idleSince is set so the reaper can suspend');
});

test('stale synthetic busy stays non-idle while an external client is attached', () => {
  const reg = busyOpencodeRegistry();
  reg.noteObserverEvent(staleAgo());
  try {
    setExternalActivityProbe(() => true); // an opencode attach client is live
    const s = reg.idleStatus(); // samples the probe → setExternalActivity
    assert.equal(s.idleSince, undefined, 'attached client keeps the session non-idle even when the synthetic busy is stale');
  } finally {
    setExternalActivityProbe(null);
  }
});

test('a real runner turn is never treated as a stale synthetic busy', () => {
  const reg = busyOpencodeRegistry();
  reg.registerTurn('turn-2', 'hello'); // a genuine /turns turn → activeTurns > 0
  reg.noteObserverEvent(staleAgo()); // even with a stale observer clock…
  const s = reg.idleStatus();
  assert.equal(s.turnActive, true, 'a registered runner turn stays active regardless of the observer clock');
  assert.equal(s.idleSince, undefined);
});

// --- Fix 2: interruptedTurns GC -------------------------------------------

function fakeObserverDeps(): ObserverDeps {
  return {
    sessionId: () => 'oc-stale',
    ocSession: () => 'ses_oc',
    activeTurnsSize: () => 0,
    nextTurnId: () => 'turn-gc', // distinct id so the assertion is collision-free
    setLastTurn: () => {},
    setExternalActivity: () => {},
    noteObserverEvent: () => {},
    setStatus: () => {},
    setModel: () => {},
    emit: () => {},
    audit: () => {},
  };
}

function assistantMessage(): Event {
  return {
    type: 'message.updated',
    properties: { info: { id: 'm1', sessionID: 'ses_oc', role: 'assistant', providerID: 'opencode', modelID: 'big-pickle' } },
  } as unknown as Event;
}

test('reset() sheds the interruptedTurns marker instead of leaking it (GC)', () => {
  const h = createObserverHandler(fakeObserverDeps());
  h.handle(assistantMessage()); // opens a cycle → activeTurnId = 'turn-gc'
  assert.equal(h.cycleActive, true);

  markObservedTurnInterrupted('turn-gc'); // CLI interrupt marks the live turn
  assert.equal(hasInterruptedTurn('turn-gc'), true, 'the interrupt is tracked');

  h.reset(); // stream drops before the turn's session.idle arrives
  assert.equal(h.cycleActive, false);
  assert.equal(hasInterruptedTurn('turn-gc'), false, 'reset()/endCycle() GC the marker — no leak');
});

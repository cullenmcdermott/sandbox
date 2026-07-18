// Unit tests for the POST /sessions/:id/turns 409 gate (turnRejectReason). The
// HTTP router itself is dark (review F4), so we test the extracted pure decision
// function that the route delegates to.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { turnRejectReason } from '../src/server.js';
import { shutdownInterruptedEvents } from '../src/turns.js';

test('R4: a registered active turn is rejected for any backend', () => {
  assert.match(
    turnRejectReason('claude-sdk', 1, 'idle') ?? '',
    /a turn is already active/,
  );
  assert.match(
    turnRejectReason('opencode-server', 2, 'idle') ?? '',
    /a turn is already active/,
  );
});

test('an idle session with no active turn may proceed (null)', () => {
  assert.equal(turnRejectReason('claude-sdk', 0, 'idle'), null);
  assert.equal(turnRejectReason('opencode-server', 0, 'idle'), null);
});

// B2: an interactive opencode turn never registers in activeTurns — it only
// surfaces as status:'busy' via the passive observer. A headless POST /turns
// mid-interactive-turn must 409, or two prompts drive the same opencode session
// concurrently and freeze the observer's mapper.
test('B2: opencode-server busy (no registered turn) is rejected', () => {
  assert.match(
    turnRejectReason('opencode-server', 0, 'busy') ?? '',
    /opencode session is busy/,
  );
});

// The busy-gate is opencode-specific: a claude-sdk turn's busy state is always
// mirrored by a registered activeTurn, so the observer-synthetic path must not
// spuriously block it on status alone.
test('claude-sdk status:busy with no active turn is NOT blocked by the opencode gate', () => {
  assert.equal(turnRejectReason('claude-sdk', 0, 'busy'), null);
});

// [V18] A SIGTERM shutdown must append one turn.interrupted per active turn (so
// a mid-turn graceful suspend leaves a terminal in the log instead of spinning
// forever on replay). The initiator owns the terminal — the agents emit nothing
// on abort (R3) — so this list is what index.ts's shutdown() appends BEFORE
// session.terminating.
test('V18: shutdownInterruptedEvents yields one turn.interrupted per active turn', () => {
  const evs = shutdownInterruptedEvents(['turn-3', 'turn-4'], 'SIGTERM');
  assert.deepEqual(evs, [
    { turnId: 'turn-3', payload: { reason: 'pod terminating (SIGTERM)' } },
    { turnId: 'turn-4', payload: { reason: 'pod terminating (SIGTERM)' } },
  ]);
});

test('V18: no active turns → no interrupted events', () => {
  assert.deepEqual(shutdownInterruptedEvents([], 'SIGTERM'), []);
});

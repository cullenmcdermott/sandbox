// activeTurnId(): the "is there a turn to interrupt" signal exposed on
// /status. last_turn_id persists after a turn finishes (it seeds nextTurnId),
// so it must NOT be used for this — the regression these tests guard is a
// client canceling an idle session because a *previous* turn's id was still
// in last_turn_id.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initRegistry, toStatusResponse } from '../src/session.js';
import type { SessionState } from '../src/types.js';

function fakeState(overrides: Partial<SessionState> = {}): SessionState {
  return {
    sandbox_session_id: 'test-session',
    backend: 'claude-sdk',
    project_path: '/p',
    status: 'idle',
    claude_session_id: '',
    opencode_session_id: '',
    last_turn_id: '',
    last_activity: new Date().toISOString(),
    ...overrides,
  };
}

// ORACLE: idle with a lingering last_turn_id -> no active turn. This is the
// exact state after any completed turn.
test('activeTurnId is empty when idle even with a stale last_turn_id', () => {
  const reg = initRegistry(fakeState({ status: 'idle', last_turn_id: 'turn-7' }));
  assert.equal(reg.activeTurnId(), '');
  assert.equal(toStatusResponse(reg.state, reg.activeTurnId()).activeTurnId, '');
});

// ORACLE: a registered runner turn is the active turn (claude-sdk path).
// Mutate activeTurns directly instead of registerTurn() to keep the test off
// the /session/state filesystem.
test('activeTurnId reports the registered runner turn', () => {
  const reg = initRegistry(fakeState({ last_turn_id: 'turn-8' }));
  reg.activeTurns.set('turn-8', { turnId: 'turn-8', abort: new AbortController(), prompt: 'p' });
  assert.equal(reg.activeTurnId(), 'turn-8');
});

// ORACLE: interactive opencode turns never register in activeTurns — the
// observer mirrors them via status busy + last_turn_id, which must surface as
// the active turn.
test('activeTurnId falls back to last_turn_id while busy (opencode observer)', () => {
  const reg = initRegistry(fakeState({ status: 'busy', last_turn_id: 'turn-9' }));
  assert.equal(reg.activeTurnId(), 'turn-9');
});

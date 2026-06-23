// Regression for C3: after a crash/restart mid-turn, session.json holds a stale
// 'busy' status, but the fresh runner process has zero active turns. The loaded
// status must be reconciled to 'idle' so /status doesn't report a phantom turn
// that no longer exists.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { reconcileLoadedStatus } from '../src/session.js';

test('reconcileLoadedStatus coerces a stale busy to idle (C3)', () => {
  assert.equal(reconcileLoadedStatus('busy'), 'idle');
});

test('reconcileLoadedStatus preserves idle and error', () => {
  assert.equal(reconcileLoadedStatus('idle'), 'idle');
  assert.equal(reconcileLoadedStatus('error'), 'error');
});

test('reconcileLoadedStatus defaults a missing status to idle', () => {
  assert.equal(reconcileLoadedStatus(undefined), 'idle');
});

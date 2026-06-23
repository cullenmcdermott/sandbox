// Regression tests for R1 (permission abort wiring) and R2 (map leak).
// Tests the SessionRegistry.deletePermission method and verifies that
// resolvePermission returns undefined after deletion.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initRegistry } from '../src/session.js';

// Minimal SessionState for testing.
const fakeState = {
  sandbox_session_id: 'test-session',
  status: 'idle' as const,
  last_activity: new Date().toISOString(),
  last_turn_id: null,
  model: null,
  claude_session_id: null,
};

// ORACLE: deletePermission removes the entry so resolvePermission returns
// undefined — preventing unbounded map growth (R2).
test('deletePermission removes entry; resolvePermission returns undefined', () => {
  const reg = initRegistry(fakeState);
  const permId = 'perm-001';
  let resolved = false;

  reg.registerPermission({
    permissionId: permId,
    tool: 'Bash',
    input: { command: 'ls' },
    resolve: (_allow) => { resolved = true; },
  });

  // Before delete: entry is present.
  assert.ok(reg.resolvePermission(permId) !== undefined, 'expected entry before delete');
  assert.equal(resolved, false);

  // After delete: entry is gone (as if the auto-deny cleaned it up).
  reg.deletePermission(permId);
  assert.equal(reg.resolvePermission(permId), undefined, 'entry should be gone after deletePermission');
});

// ORACLE: Registering the same permId twice is safe after deletePermission
// (no double-resolve bug from R1's abort path).
test('re-registering after delete does not double-resolve', () => {
  const reg = initRegistry(fakeState);
  const permId = 'perm-002';
  let resolveCount = 0;

  reg.registerPermission({
    permissionId: permId,
    tool: 'Read',
    input: { file_path: '/tmp/test' },
    resolve: () => { resolveCount++; },
  });

  reg.deletePermission(permId);
  // Registering a fresh entry (new turn, same id-space) works cleanly.
  reg.registerPermission({
    permissionId: permId,
    tool: 'Read',
    input: { file_path: '/tmp/other' },
    resolve: () => { resolveCount++; },
  });

  const p = reg.resolvePermission(permId);
  assert.ok(p !== undefined);
  p!.resolve(true, 'once', undefined);
  assert.equal(resolveCount, 1, 'only the second registration should resolve');
});

// NEW-7: isDetached() drives the abandoned-permission auto-deny. With no SSE
// client attached and no recent external activity the session is detached, so a
// pending permission is abandonable; recent external (opencode) activity counts
// as attached and must keep it alive.
test('isDetached: true with no clients, false after external activity', () => {
  const reg = initRegistry(fakeState);
  assert.equal(reg.isDetached(), true, 'no SSE clients + no external activity → detached');
  reg.setExternalActivity();
  assert.equal(reg.isDetached(), false, 'recent external activity → attached');
});

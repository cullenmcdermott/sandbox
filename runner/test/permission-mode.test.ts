// Runner unit tests (node:test via tsx). Covers the permission-mode mapping
// that buildOptions applies to the SDK query() (Phase 0b). buildOptions itself
// has a pod-only filesystem side effect (mkdirSync under /session), so we test
// the pure decision function it delegates to.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resolvePermissionMode } from '../src/claude.js';

test('valid SDK modes pass through unchanged', () => {
  assert.equal(resolvePermissionMode('default'), 'default');
  assert.equal(resolvePermissionMode('acceptEdits'), 'acceptEdits');
  assert.equal(resolvePermissionMode('plan'), 'plan');
  assert.equal(resolvePermissionMode('bypassPermissions'), 'bypassPermissions');
});

test('empty/undefined/unknown defaults to bypassPermissions (§2d yolo default)', () => {
  // §2d: an unpinned mode now inherits yolo (bypassPermissions). The sandbox pod
  // is the isolation boundary; the bypass path stays hard-gated by
  // allowDangerouslySkipPermissions + IS_SANDBOX in buildOptions.
  assert.equal(resolvePermissionMode(undefined), 'bypassPermissions');
  assert.equal(resolvePermissionMode(''), 'bypassPermissions');
  assert.equal(resolvePermissionMode('yolo'), 'bypassPermissions');
  assert.equal(resolvePermissionMode('Plan'), 'bypassPermissions'); // case-sensitive enum
});

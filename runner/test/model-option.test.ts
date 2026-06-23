// Behavioral counter for TODO.md ① (model selection): resolveModel applies the
// precedence the SDK query() relies on — a per-turn /model override wins over
// the session default (SANDBOX_MODEL), and an empty result leaves Options.model
// unset so the account default is used. resolveModel itself is pure (no SDK
// call), mirroring resolvePermissionMode's unit test.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resolveModel } from '../src/claude.js';

test('resolveModel: per-turn override wins over the session default', () => {
  assert.equal(resolveModel('opus', 'sonnet'), 'opus');
});

test('resolveModel: falls back to the session default when no override', () => {
  assert.equal(resolveModel(undefined, 'sonnet'), 'sonnet');
  assert.equal(resolveModel('', 'sonnet'), 'sonnet');
});

test('resolveModel: undefined (account default) when neither is set', () => {
  assert.equal(resolveModel(undefined, undefined), undefined);
  assert.equal(resolveModel('', ''), undefined);
  assert.equal(resolveModel('', undefined), undefined);
});

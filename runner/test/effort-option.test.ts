// Behavioral counter for the in-session /effort switch: resolveEffort narrows a
// per-turn effort string (TurnRequestBody.effort) to the typed SDK EffortLevel
// so buildOptions can assign Options.effort. A valid enum value passes through
// (with a session-default fallback mirroring resolveModel); empty OR any
// out-of-enum string returns undefined, leaving Options.effort unset (SDK
// adaptive thinking). resolveEffort is pure (no SDK call), mirroring
// resolveModel's unit test. The narrowing — not a raw cast — is what stops an
// invalid value from reaching the typed Options.effort.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resolveEffort } from '../src/claude.js';

test('resolveEffort: every valid SDK level passes through verbatim', () => {
  for (const lvl of ['low', 'medium', 'high', 'xhigh', 'max'] as const) {
    assert.equal(resolveEffort(lvl), lvl);
  }
});

test('resolveEffort: per-turn override wins over the session default', () => {
  assert.equal(resolveEffort('high', 'low'), 'high');
});

test('resolveEffort: falls back to the session default when no override', () => {
  assert.equal(resolveEffort(undefined, 'high'), 'high');
  assert.equal(resolveEffort('', 'high'), 'high');
});

test('resolveEffort: undefined (adaptive default) when neither is set', () => {
  assert.equal(resolveEffort(undefined, undefined), undefined);
  assert.equal(resolveEffort('', ''), undefined);
  assert.equal(resolveEffort('', undefined), undefined);
});

test('resolveEffort: unknown values are rejected, not forwarded', () => {
  // "ultracode" is the TUI display label, NOT a wire value — it must NOT resolve.
  assert.equal(resolveEffort('ultracode'), undefined);
  assert.equal(resolveEffort('garbage'), undefined);
  assert.equal(resolveEffort('Max'), undefined); // case-sensitive enum
  // A garbage override does not silently fall through to a valid session default.
  assert.equal(resolveEffort('garbage', 'high'), undefined);
});

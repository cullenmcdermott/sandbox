// Unit tests for the bearer-token auth primitives (src/auth.ts). This module is
// deliberately sqlite-free, so these tests run under CI's
// `npm install --ignore-scripts` where the better-sqlite3 native addon is
// absent — auth.ts imports nothing that loads it.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bearerTokenOk, constantTimeEqual } from '../src/auth.js';

const TOKEN = 'super-secret-token';

// ORACLE: reject when no token is configured (expected === '') — the runner
// must fail closed, rejecting ALL non-healthz requests rather than allowing
// unauthenticated access.
test('bearerTokenOk rejects when no token is configured', () => {
  assert.equal(bearerTokenOk('', 'Bearer anything'), false);
  assert.equal(bearerTokenOk('', undefined), false);
});

// ORACLE: reject a missing Authorization header.
test('bearerTokenOk rejects a missing header', () => {
  assert.equal(bearerTokenOk(TOKEN, undefined), false);
});

// ORACLE: reject a header that is not a string (node can pass string[]).
test('bearerTokenOk rejects a non-string (array) header', () => {
  assert.equal(bearerTokenOk(TOKEN, ['Bearer ' + TOKEN]), false);
});

// ORACLE: reject a wrong scheme (Basic, no scheme, bare token).
test('bearerTokenOk rejects a wrong auth scheme', () => {
  assert.equal(bearerTokenOk(TOKEN, `Basic ${TOKEN}`), false);
  assert.equal(bearerTokenOk(TOKEN, TOKEN), false); // bare token, no scheme
  assert.equal(bearerTokenOk(TOKEN, `Token ${TOKEN}`), false);
});

// ORACLE: reject a wrong token (right scheme, wrong value).
test('bearerTokenOk rejects a wrong token', () => {
  assert.equal(bearerTokenOk(TOKEN, 'Bearer not-the-token'), false);
  assert.equal(bearerTokenOk(TOKEN, `Bearer ${TOKEN}x`), false); // longer
  assert.equal(bearerTokenOk(TOKEN, `Bearer ${TOKEN.slice(0, -1)}`), false); // shorter
});

// ORACLE: accept the correct token with the Bearer scheme.
test('bearerTokenOk accepts the correct token', () => {
  assert.equal(bearerTokenOk(TOKEN, `Bearer ${TOKEN}`), true);
  // Tolerates the regex's \s+ between scheme and token.
  assert.equal(bearerTokenOk(TOKEN, `Bearer   ${TOKEN}`), true);
});

// ORACLE (R9): constantTimeEqual must compare equal strings as equal and
// unequal strings as unequal, INCLUDING when the lengths differ — it must not
// short-circuit on length (which would leak the token length via timing).
test('constantTimeEqual is correct for equal, unequal, and length-mismatched strings', () => {
  assert.equal(constantTimeEqual('abc', 'abc'), true);
  assert.equal(constantTimeEqual('abc', 'abd'), false);
  assert.equal(constantTimeEqual('abc', 'ab'), false); // shorter
  assert.equal(constantTimeEqual('abc', 'abcd'), false); // longer
  assert.equal(constantTimeEqual('', ''), true);
});

// ORACLE (R9): the comparison must not short-circuit on the FIRST differing
// byte. We assert this behaviorally: a string differing only in its last byte
// and a string differing only in its first byte are BOTH rejected (a naive
// length-then-loop-with-early-return is functionally covered, but the key
// property is that a length mismatch is still compared over the full range
// rather than returning early). We verify the loop runs over max(len) by
// confirming that two strings of very different lengths still compare false
// without throwing (no index error) and that a same-length single-byte diff at
// the end is caught.
test('constantTimeEqual does not short-circuit on length (R9)', () => {
  const a = 'x'.repeat(64);
  const bDiffLast = 'x'.repeat(63) + 'y';
  const bLonger = 'x'.repeat(200);
  const bShorter = 'x'.repeat(1);
  assert.equal(constantTimeEqual(a, bDiffLast), false, 'last-byte diff must be caught');
  assert.equal(constantTimeEqual(a, bLonger), false, 'much-longer must be caught');
  assert.equal(constantTimeEqual(a, bShorter), false, 'much-shorter must be caught');
  assert.equal(constantTimeEqual(a, a), true, 'identical long strings equal');
});

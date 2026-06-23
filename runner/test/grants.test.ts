// Unit tests for the session-scope permission grant logic (src/grants.ts).
// grants.ts is pure and sqlite-free, so this runs under CI's
// `npm install --ignore-scripts` (no native addon).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SessionGrants, resolutionOutcome } from '../src/grants.js';

// ORACLE: resolutionOutcome maps allow + scope to the decision + grant flag.
test('resolutionOutcome: scope=session + allow → allow-session, grants', () => {
  assert.deepEqual(resolutionOutcome(true, 'session'), {
    decision: 'allow-session',
    grantSession: true,
  });
});

test('resolutionOutcome: allow with once/default → allow-once, no grant', () => {
  assert.deepEqual(resolutionOutcome(true, 'once'), { decision: 'allow-once', grantSession: false });
  assert.deepEqual(resolutionOutcome(true, undefined), { decision: 'allow-once', grantSession: false });
});

test('resolutionOutcome: deny → deny, no grant (even with scope=session)', () => {
  assert.deepEqual(resolutionOutcome(false, 'session'), { decision: 'deny', grantSession: false });
  assert.deepEqual(resolutionOutcome(false, 'once'), { decision: 'deny', grantSession: false });
});

// ORACLE: a granted tool name auto-allows; an ungranted one does not. This is
// the core "session scope means don't prompt again for this tool" behavior.
test('SessionGrants: grant records a tool-name grant that isGranted reports', () => {
  const grants = new SessionGrants();
  assert.equal(grants.isGranted('Bash'), false, 'no grant before granting');
  grants.grant('Bash');
  assert.equal(grants.isGranted('Bash'), true, 'granted after grant');
  assert.equal(grants.isGranted('Write'), false, 'grant is per-tool-name, not global');
});

test('SessionGrants: grant is idempotent and ignores empty names', () => {
  const grants = new SessionGrants();
  grants.grant('Edit');
  grants.grant('Edit');
  grants.grant('');
  assert.deepEqual(grants.list(), ['Edit']);
});

// ORACLE: the end-to-end grant flow — a scope:'session' resolution feeds
// resolutionOutcome whose grantSession flag drives SessionGrants.grant, after
// which the same tool auto-allows.
test('session-scope resolution then grant → tool auto-allows', () => {
  const grants = new SessionGrants();
  const { decision, grantSession } = resolutionOutcome(true, 'session');
  assert.equal(decision, 'allow-session');
  if (grantSession) grants.grant('Bash');
  assert.equal(grants.isGranted('Bash'), true, 'subsequent Bash uses auto-allow');
});

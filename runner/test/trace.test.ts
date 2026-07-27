// Behavioral counter for the §10 traces (runner/src/trace.ts): the connect↔turn
// id bridge, its untrusted-header parser, and the per-phase boot trace. All are
// off unless SANDBOX_TRACE is set. Tests inject a fake clock + log sink
// (opts.now/opts.log) and force opts.enabled so they never depend on the ambient
// env, and assert the exact emitted envelope plus the disabled no-op path.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { traceTurnLink, traceIDFromHeader, startBootTrace } from '../src/trace.js';

// fakeClock returns a now() that advances by `step` ms on each call, so elapsed
// times in the emitted lines are deterministic.
function fakeClock(start: number, step: number): () => number {
  let t = start;
  let first = true;
  return () => {
    if (first) {
      first = false;
      return start;
    }
    t += step;
    return t;
  };
}

// --- traceTurnLink: the connect-id ↔ turn-id bridge -------------------------

test('traceTurnLink: emits one greppable line keyed by BOTH ids', () => {
  const lines: string[] = [];
  traceTurnLink('3f9a1c2b', 'turn_ab12', { enabled: true, log: (l) => lines.push(l) });
  assert.deepEqual(lines, ['trace: 3f9a1c2b turn.link turn=turn_ab12']);
});

test('traceTurnLink: silent when disabled or when no connect id was supplied', () => {
  const lines: string[] = [];
  traceTurnLink('3f9a1c2b', 'turn_1', { enabled: false, log: (l) => lines.push(l) });
  traceTurnLink('', 'turn_1', { enabled: true, log: (l) => lines.push(l) });
  assert.deepEqual(lines, []);
});

// --- traceIDFromHeader: untrusted header → safe id or '' --------------------

test('traceIDFromHeader: accepts token-shaped ids, first value of a repeat', () => {
  assert.equal(traceIDFromHeader('3f9a1c2b'), '3f9a1c2b');
  assert.equal(traceIDFromHeader('conn'), 'conn');
  assert.equal(traceIDFromHeader('a.b-c_d'), 'a.b-c_d');
  assert.equal(traceIDFromHeader(['3f9a1c2b', 'other']), '3f9a1c2b');
});

test('traceIDFromHeader: absent or malformed values yield "" (link no-ops)', () => {
  assert.equal(traceIDFromHeader(undefined), '');
  assert.equal(traceIDFromHeader(''), '');
  assert.equal(traceIDFromHeader([]), '');
  assert.equal(traceIDFromHeader('has space'), '');
  assert.equal(traceIDFromHeader('new\nline'), '');
  assert.equal(traceIDFromHeader('x'.repeat(65)), '');
});

// --- startBootTrace: per-phase startup timings -------------------------------

test('startBootTrace: disabled is a silent no-op', () => {
  const lines: string[] = [];
  const boot = startBootTrace({ enabled: false, log: (l) => lines.push(l) });
  boot.phase('event_log');
  boot.done();
  assert.deepEqual(lines, []);
});

test('startBootTrace: phases are deltas from the previous mark; total is cumulative', () => {
  const lines: string[] = [];
  // start=0; each subsequent now() call adds 10ms.
  const boot = startBootTrace({ enabled: true, now: fakeClock(0, 10), log: (l) => lines.push(l) });
  boot.phase('event_log'); // now=10 → 10ms since start
  boot.phase('session_state'); // now=20 → 10ms since previous phase
  boot.done(); // now=30 → 30ms since start

  assert.deepEqual(lines, [
    'trace: boot boot.event_log 10ms',
    'trace: boot boot.session_state 10ms',
    'trace: boot boot.total 30ms',
  ]);
});

// Behavioral counter for the §10 turn-timing trace (runner/src/trace.ts). The
// trace is off unless SANDBOX_TRACE is set; when on it emits milestone lines and
// a per-turn summary correlated by turn id. Tests inject a fake clock + log sink
// (opts.now/opts.log) and force opts.enabled so they never depend on the ambient
// env, and assert the exact envelope: `trace: <turnId> <name> <ms>ms`, the
// summary's `msgs=<n>`, milestone idempotency, and the disabled no-op path.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { startTurnTrace, traceTurnLink, traceIDFromHeader, startBootTrace } from '../src/trace.js';

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

test('startTurnTrace: disabled is a silent no-op', () => {
  const lines: string[] = [];
  const trace = startTurnTrace('turn_x', { enabled: false, log: (l) => lines.push(l) });
  trace.mark('turn.first_message');
  trace.settle(5);
  assert.deepEqual(lines, []);
});

test('startTurnTrace: emits the per-turn summary line with duration + msg count', () => {
  const lines: string[] = [];
  // start=1000; each subsequent now() call adds 100ms.
  const trace = startTurnTrace('turn_ab12', {
    enabled: true,
    now: fakeClock(1000, 100),
    log: (l) => lines.push(l),
  });
  trace.settle(37);
  assert.equal(lines.length, 1);
  assert.equal(lines[0], 'trace: turn_ab12 turn.settled 100ms msgs=37');
});

test('startTurnTrace: marks are one-shot per name (first occurrence only)', () => {
  const lines: string[] = [];
  const trace = startTurnTrace('turn_1', {
    enabled: true,
    now: fakeClock(0, 50),
    log: (l) => lines.push(l),
  });
  trace.mark('turn.first_message'); // 50ms
  trace.mark('turn.first_message'); // ignored (already seen)
  trace.mark('turn.first_delta'); // 100ms
  trace.settle(2); // 150ms

  assert.deepEqual(lines, [
    'trace: turn_1 turn.first_message 50ms',
    'trace: turn_1 turn.first_delta 100ms',
    'trace: turn_1 turn.settled 150ms msgs=2',
  ]);
});

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

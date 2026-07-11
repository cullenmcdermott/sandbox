// Unit tests for the always-on opencode observer's event-mapping core
// (createObserverHandler). Driven with synthetic opencode events and fake deps —
// no server, no DB. Mirrors the opencode-turn mapper tests.
//
// The observer frames each interactive assistant cycle (message.updated(assistant)
// … session.idle) as a synthetic turn: turn.started → message/usage → turn.completed,
// reusing a fresh createOpencodeTurnMapper per cycle.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Event } from '@opencode-ai/sdk';

import { createObserverHandler, markObservedTurnInterrupted, type ObserverDeps } from '../src/opencode-observer.js';

const OC = 'ses_oc';

interface Calls {
  emitted: Array<{ turnId?: string; type: string; payload: Record<string, unknown> }>;
  statuses: string[];
  lastTurns: string[];
  models: string[];
  externalActivity: number;
  audited: Array<{ turnId: string; tool: string; input: unknown }>;
}

function fakeDeps(overrides: Partial<ObserverDeps> = {}): { deps: ObserverDeps; calls: Calls } {
  let turnCounter = 0;
  const calls: Calls = { emitted: [], statuses: [], lastTurns: [], models: [], externalActivity: 0, audited: [] };
  const deps: ObserverDeps = {
    sessionId: () => 'sandbox-1',
    ocSession: () => OC,
    activeTurnsSize: () => 0,
    nextTurnId: () => `turn-${++turnCounter}`,
    setLastTurn: (id) => calls.lastTurns.push(id),
    setExternalActivity: () => {
      calls.externalActivity++;
    },
    setStatus: (s) => calls.statuses.push(s),
    setModel: (m) => calls.models.push(m),
    emit: (turnId, type, payload) => calls.emitted.push({ turnId, type, payload }),
    audit: (turnId, tool, input) => calls.audited.push({ turnId, tool, input }),
    ...overrides,
  };
  return { deps, calls };
}

function toolPart(msgId: string, partId: string, status: 'running' | 'completed', extra: Record<string, unknown>): Event {
  return {
    type: 'message.part.updated',
    properties: {
      part: {
        id: partId,
        sessionID: OC,
        messageID: msgId,
        type: 'tool',
        callID: 'call_1',
        tool: 'bash',
        state: { status, input: { command: 'ls -la' }, ...extra },
      },
    },
  } as unknown as Event;
}

function asstMsg(msgId: string, opts: { sessionID?: string; model?: boolean } = {}): Event {
  return {
    type: 'message.updated',
    properties: {
      info: {
        id: msgId,
        sessionID: opts.sessionID ?? OC,
        role: 'assistant',
        tokens: { input: 100, output: 50, reasoning: 0, cache: { read: 0, write: 0 } },
        cost: 0.0123,
        ...(opts.model !== false ? { providerID: 'opencode', modelID: 'big-pickle' } : {}),
      },
    },
  } as unknown as Event;
}

function textPart(msgId: string, partId: string, text: string, delta: string, sessionID = OC): Event {
  return {
    type: 'message.part.updated',
    properties: {
      part: {
        id: partId,
        sessionID,
        messageID: msgId,
        type: 'text',
        text,
        time: { start: 1, end: 2 },
      },
      delta,
    },
  } as unknown as Event;
}

function sessionIdle(sessionID = OC): Event {
  return { type: 'session.idle', properties: { sessionID } } as unknown as Event;
}

function sessionTitle(title: string, id = OC): Event {
  return { type: 'session.updated', properties: { info: { id, title } } } as unknown as Event;
}

function sessionError(message: string, sessionID: string | undefined = OC): Event {
  return {
    type: 'session.error',
    properties: { sessionID, error: { name: 'ProviderError', data: { message } } },
  } as unknown as Event;
}

const types = (calls: Calls) => calls.emitted.map((e) => e.type);

test('observer frames an interactive assistant cycle as a synthetic turn', () => {
  const { deps, calls } = fakeDeps();
  const h = createObserverHandler(deps);

  h.handle(asstMsg('m1')); // cycle start
  h.handle(textPart('m1', 'p1', 'Hi there', 'Hi there'));
  h.handle(sessionIdle());

  assert.deepEqual(types(calls), [
    'turn.started',
    'session.started', // model emitted once for ctx%
    'usage.updated', // from the mapper's message.updated handling
    'message.started',
    'message.delta',
    'message.completed',
    'turn.completed',
  ]);
  // turn.started + every cycle event carry the same synthetic turn id.
  assert.ok(calls.emitted.every((e) => e.turnId === 'turn-1' || e.type === 'session.title'));
  assert.deepEqual(calls.statuses, ['busy', 'idle']);
  assert.deepEqual(calls.lastTurns, ['turn-1']);
  assert.deepEqual(calls.models, ['opencode/big-pickle']);
  assert.equal(calls.externalActivity, 1);
  assert.equal(h.cycleActive, false);
});

test('observer audits an interactive tool execution once, bound to the cycle turn', () => {
  const { deps, calls } = fakeDeps();
  const h = createObserverHandler(deps);

  h.handle(asstMsg('m1')); // cycle start (turn-1)
  h.handle(toolPart('m1', 't1', 'running', {}));
  h.handle(toolPart('m1', 't1', 'completed', { output: 'ok' })); // opencode re-sends the part
  h.handle(sessionIdle());

  assert.equal(calls.audited.length, 1, 'exactly one audit row per tool execution');
  assert.deepEqual(calls.audited[0], { turnId: 'turn-1', tool: 'bash', input: { command: 'ls -la' } });
});

test('observer emits a fresh turn per cycle (no single-turn latch leakage)', () => {
  const { deps, calls } = fakeDeps();
  const h = createObserverHandler(deps);

  // Cycle 1
  h.handle(asstMsg('m1'));
  h.handle(textPart('m1', 'p1', 'one', 'one'));
  h.handle(sessionIdle());
  // Cycle 2
  h.handle(asstMsg('m2'));
  h.handle(textPart('m2', 'p2', 'two', 'two'));
  h.handle(sessionIdle());

  const turnStarts = calls.emitted.filter((e) => e.type === 'turn.started');
  const turnDones = calls.emitted.filter((e) => e.type === 'turn.completed');
  assert.equal(turnStarts.length, 2, 'one turn.started per cycle');
  assert.equal(turnDones.length, 2, 'one turn.completed per cycle');
  assert.deepEqual(turnStarts.map((e) => e.turnId), ['turn-1', 'turn-2']);
  assert.deepEqual(turnDones.map((e) => e.turnId), ['turn-1', 'turn-2']);
  // Each cycle reproduces message.started/completed (proves per-part Set state did
  // not leak from cycle 1's mapper into cycle 2).
  assert.equal(calls.emitted.filter((e) => e.type === 'message.completed').length, 2);
  // session.started/model emitted once total, not per cycle.
  assert.equal(calls.emitted.filter((e) => e.type === 'session.started').length, 1);
  assert.deepEqual(calls.statuses, ['busy', 'idle', 'busy', 'idle']);
});

test('observer ignores events for a foreign opencode session', () => {
  const { deps, calls } = fakeDeps();
  const h = createObserverHandler(deps);

  h.handle(asstMsg('mX', { sessionID: 'ses_other' }));
  h.handle(textPart('mX', 'pX', 'hi', 'hi', 'ses_other'));
  h.handle(sessionIdle('ses_other'));

  assert.deepEqual(types(calls), [], 'no events for a session that is not ours');
  assert.equal(h.cycleActive, false);
});

test('observer suppresses mapping while a runner-driven turn owns the stream', () => {
  const { deps, calls } = fakeDeps({ activeTurnsSize: () => 1 });
  const h = createObserverHandler(deps);

  h.handle(asstMsg('m1'));
  h.handle(textPart('m1', 'p1', 'hi', 'hi'));
  h.handle(sessionIdle());

  assert.deepEqual(types(calls), [], 'runTurn is the single writer; observer stays silent');
});

test('observer mirrors a live title change, suppressing the placeholder + repeats', () => {
  const { deps, calls } = fakeDeps();
  const h = createObserverHandler(deps);

  h.handle(sessionTitle('sandbox runner session')); // placeholder → ignored
  h.handle(sessionTitle('Fix the JSON parser')); // real → emitted
  h.handle(sessionTitle('Fix the JSON parser')); // unchanged → ignored
  h.handle(sessionTitle('Refactor the parser')); // changed → emitted again

  const titles = calls.emitted.filter((e) => e.type === 'session.title');
  assert.deepEqual(titles.map((e) => e.payload.title), ['Fix the JSON parser', 'Refactor the parser']);
});

// ORACLE (D11): a session.error BEFORE any assistant cycle (e.g. a provider auth
// failure at turn start) has no open mapper, so it used to vanish and leave the
// dashboard idle. The observer now surfaces it as a synthetic failed turn.
test('observer surfaces a pre-cycle session.error as a failed turn (D11)', () => {
  const { deps, calls } = fakeDeps();
  const h = createObserverHandler(deps);

  assert.equal(h.cycleActive, false);
  h.handle(sessionError('provider auth failed'));

  assert.deepEqual(types(calls), ['turn.failed', 'error']);
  assert.equal(calls.emitted[0].payload.message, 'provider auth failed');
  assert.equal(calls.emitted[1].payload.message, 'provider auth failed');
  assert.equal(calls.statuses.at(-1), 'error');
  assert.deepEqual(calls.lastTurns, ['turn-1'], 'a synthetic turn id is stamped');
  assert.equal(h.cycleActive, false, 'no cycle is opened by an error');
});

// A pre-cycle session.error for a FOREIGN opencode session is ignored (the
// evSession gate), so an unrelated session's failure can't fail ours.
test('observer ignores a pre-cycle session.error for a foreign session (D11)', () => {
  const { deps, calls } = fakeDeps();
  const h = createObserverHandler(deps);

  h.handle(sessionError('not ours', 'ses_other'));
  assert.deepEqual(types(calls), []);
});

// ORACLE (D11): a retitle DURING a runner-driven (headless) turn must still
// surface — the title passthrough is exempt from the activeTurns suppression guard
// that silences every other observed event.
test('observer surfaces a title change during a headless turn (D11)', () => {
  const { deps, calls } = fakeDeps({ activeTurnsSize: () => 1 });
  const h = createObserverHandler(deps);

  h.handle(sessionTitle('Fix the JSON parser'));
  const titles = calls.emitted.filter((e) => e.type === 'session.title');
  assert.deepEqual(titles.map((e) => e.payload.title), ['Fix the JSON parser']);

  // Non-title events during a headless turn stay suppressed (runTurn is the writer).
  h.handle(asstMsg('m1'));
  h.handle(sessionIdle());
  assert.deepEqual(types(calls), ['session.title'], 'only the title passes through');
});

test('observer holds off until the opencode session id is resolved', () => {
  let oc = ''; // warmup not done yet
  const { deps, calls } = fakeDeps({ ocSession: () => oc });
  const h = createObserverHandler(deps);

  h.handle(asstMsg('m1')); // no session id yet → ignored
  assert.deepEqual(types(calls), []);

  oc = OC; // warmup resolved
  h.handle(asstMsg('m1'));
  assert.ok(types(calls).includes('turn.started'));
});

test('observer reset() abandons an in-flight cycle back to idle (stream dropped)', () => {
  const { deps, calls } = fakeDeps();
  const h = createObserverHandler(deps);

  h.handle(asstMsg('m1')); // opens a cycle
  assert.equal(h.cycleActive, true);
  h.reset(); // serve restarted mid-turn
  assert.equal(h.cycleActive, false);
  assert.equal(calls.statuses.at(-1), 'idle', 'status returns to idle on reset');
  assert.equal(calls.emitted.at(-1)?.type, 'turn.interrupted', 'reset terminalizes the synthetic turn');
  assert.equal(calls.emitted.at(-1)?.payload.reason, 'opencode observer stream ended');

  // reset with no active cycle is a no-op (no spurious idle).
  const before = calls.statuses.length;
  h.reset();
  assert.equal(calls.statuses.length, before);
});

test('observer suppresses turn.completed after an explicit interrupt terminal event', () => {
  const { deps, calls } = fakeDeps();
  const h = createObserverHandler(deps);

  h.handle(asstMsg('m1'));
  markObservedTurnInterrupted('turn-1');
  h.handle(sessionIdle());

  assert.equal(h.cycleActive, false);
  assert.deepEqual(types(calls), ['turn.started', 'session.started', 'usage.updated']);
  assert.equal(calls.statuses.at(-1), 'idle');
});

// Behavioral counter for the §10 turn-timing trace (runner/src/trace.ts). The
// trace is off unless SANDBOX_TRACE is set; when on it emits milestone lines and
// a per-turn summary correlated by turn id. Tests inject a fake clock + log sink
// (opts.now/opts.log) and force opts.enabled so they never depend on the ambient
// env, and assert the exact envelope: `trace: <turnId> <name> <ms>ms`, the
// summary's `msgs=<n>`, milestone idempotency, and the disabled no-op path.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { startTurnTrace } from '../src/trace.js';
import { isAssistantTextDelta } from '../src/claude.js';
import type { SDKMessage } from '@anthropic-ai/claude-agent-sdk';

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

test('isAssistantTextDelta: true only for a text_delta stream_event', () => {
  const textDelta = {
    type: 'stream_event',
    event: { type: 'content_block_delta', delta: { type: 'text_delta', text: 'hi' } },
  } as unknown as SDKMessage;
  assert.equal(isAssistantTextDelta(textDelta), true);

  const thinkingDelta = {
    type: 'stream_event',
    event: { type: 'content_block_delta', delta: { type: 'thinking_delta', thinking: 'x' } },
  } as unknown as SDKMessage;
  assert.equal(isAssistantTextDelta(thinkingDelta), false);

  const initMsg = { type: 'system', subtype: 'init' } as unknown as SDKMessage;
  assert.equal(isAssistantTextDelta(initMsg), false);

  const assistantMsg = { type: 'assistant' } as unknown as SDKMessage;
  assert.equal(isAssistantTextDelta(assistantMsg), false);
});

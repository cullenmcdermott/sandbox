// Unit tests for the pure SDK-message → normalized-event mapping (src/mapping.ts).
// mapping.ts imports nothing that loads better-sqlite3, so these tests run under
// CI's `npm install --ignore-scripts` (no native addon). We feed canned SDK
// messages and assert the emitted normalized events + payloads, exercising the
// mapping in isolation from the event log / registry.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { SDKMessage } from '@anthropic-ai/claude-agent-sdk';
import { mapMessage, todoUpdatedPayload, type EmitFn } from '../src/mapping.js';
import { assertMapperInvariants } from './backend-contract.js';

/** Collect emitted events into a list for assertions. */
function collector(): { events: Array<{ type: string; payload: Record<string, unknown> }>; emit: EmitFn } {
  const events: Array<{ type: string; payload: Record<string, unknown> }> = [];
  const emit: EmitFn = (type, payload) => events.push({ type, payload });
  return { events, emit };
}

// Cast helper: fixtures only populate the fields mapMessage actually reads.
function asMsg(m: unknown): SDKMessage {
  return m as SDKMessage;
}

// ORACLE: a system/init message emits session.started (with model/cwd) and
// returns the model + claudeSessionId + isInit observation for the caller.
test('system/init → session.started + init observation', () => {
  const { events, emit } = collector();
  const res = mapMessage(
    asMsg({
      type: 'system',
      subtype: 'init',
      model: 'opus-4.8',
      cwd: '/session/workspace/proj',
      tools: ['Read', 'Bash'],
      permissionMode: 'acceptEdits',
      session_id: 'claude-abc',
    }),
    emit,
  );
  assert.equal(events.length, 1);
  assert.equal(events[0].type, 'session.started');
  assert.deepEqual(events[0].payload, {
    model: 'opus-4.8',
    cwd: '/session/workspace/proj',
    tools: ['Read', 'Bash'],
    permissionMode: 'acceptEdits',
    claudeSessionId: 'claude-abc',
  });
  assert.deepEqual(res, { model: 'opus-4.8', claudeSessionId: 'claude-abc', isInit: true });
});

// ORACLE: a system/compact_boundary message emits context.compacted with the
// trigger + pre/post token counts (schema §2b gap 4 — previously dropped).
test('system/compact_boundary → context.compacted', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'system',
      subtype: 'compact_boundary',
      compact_metadata: { trigger: 'auto', pre_tokens: 180000, post_tokens: 42000 },
    }),
    emit,
  );
  assert.equal(events.length, 1);
  assert.equal(events[0].type, 'context.compacted');
  assert.deepEqual(events[0].payload, { trigger: 'auto', preTokens: 180000, postTokens: 42000 });
});

// post_tokens is optional in the SDK; absence maps to 0 (TUI leaves ctx% for the
// next usage event to refine).
test('system/compact_boundary without post_tokens → postTokens 0', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'system',
      subtype: 'compact_boundary',
      compact_metadata: { trigger: 'manual', pre_tokens: 150000 },
    }),
    emit,
  );
  assert.equal(events.length, 1);
  assert.deepEqual(events[0].payload, { trigger: 'manual', preTokens: 150000, postTokens: 0 });
});

// ORACLE: an assistant text block emits message.started + message.completed.
test('assistant text block → message.started + message.completed', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [{ type: 'text', text: 'hello world' }],
        usage: { input_tokens: 10, output_tokens: 5 },
      },
    }),
    emit,
  );
  assert.deepEqual(
    events.map((e) => e.type),
    ['message.started', 'message.completed', 'usage.updated'],
  );
  assert.deepEqual(events[1].payload, { role: 'assistant', content: 'hello world' });
});

// ORACLE: a Task tool_use carries subagent_type → agentName on tool.started.
test('assistant Task tool_use → tool.started with agentName from subagent_type', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'tool_use',
            id: 'toolu_1',
            name: 'Task',
            input: { subagent_type: 'reviewer', prompt: 'review this' },
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const started = events.find((e) => e.type === 'tool.started');
  assert.ok(started, 'expected a tool.started event');
  assert.equal(started!.payload.tool, 'Task');
  assert.equal(started!.payload.toolUseId, 'toolu_1');
  assert.equal(started!.payload.agentName, 'reviewer');
});

// ORACLE: a non-Task tool_use has agentName undefined.
test('assistant non-Task tool_use → tool.started without agentName', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [{ type: 'tool_use', id: 'toolu_2', name: 'Bash', input: { command: 'ls' } }],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const started = events.find((e) => e.type === 'tool.started');
  assert.equal(started!.payload.tool, 'Bash');
  assert.equal(started!.payload.agentName, undefined);
});

// ORACLE: a TodoWrite tool_use emits both tool.started AND a todo.updated event
// whose payload maps {content,status,activeForm} from the SDK input.
test('assistant TodoWrite tool_use → tool.started + todo.updated', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'tool_use',
            id: 'toolu_3',
            name: 'TodoWrite',
            input: {
              todos: [
                { content: 'write tests', status: 'in_progress', activeForm: 'writing tests' },
                { content: 'ship it', status: 'pending', activeForm: 'shipping it' },
              ],
            },
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const todo = events.find((e) => e.type === 'todo.updated');
  assert.ok(todo, 'expected a todo.updated event');
  assert.deepEqual(todo!.payload, {
    todos: [
      { content: 'write tests', status: 'in_progress', activeForm: 'writing tests' },
      { content: 'ship it', status: 'pending', activeForm: 'shipping it' },
    ],
  });
  // The generic tool.started is still emitted alongside it.
  assert.ok(events.some((e) => e.type === 'tool.started' && e.payload.tool === 'TodoWrite'));
});

// ORACLE: todoUpdatedPayload tolerates missing/extra fields and bad shapes.
test('todoUpdatedPayload maps fields and tolerates bad input', () => {
  assert.equal(todoUpdatedPayload({}), undefined, 'no todos array → undefined');
  assert.equal(todoUpdatedPayload({ todos: 'nope' }), undefined, 'non-array todos → undefined');
  assert.deepEqual(
    todoUpdatedPayload({ todos: [{ content: 'x', status: 'completed' }] }),
    { todos: [{ content: 'x', status: 'completed' }] },
    'activeForm omitted when absent',
  );
});

// ORACLE: a user tool_result (success) → tool.completed with the output text;
// the result-content array is flattened to a string.
test('user tool_result (success) → tool.completed', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'user',
      message: {
        content: [
          { type: 'tool_result', tool_use_id: 'toolu_1', content: [{ type: 'text', text: 'ok done' }] },
        ],
      },
    }),
    emit,
  );
  assert.equal(events.length, 1);
  assert.equal(events[0].type, 'tool.completed');
  assert.equal(events[0].payload.output, 'ok done');
  assert.equal(events[0].payload.toolUseId, 'toolu_1');
});

// ORACLE: a user tool_result with is_error → tool.failed with both output and
// error populated (the Go TUI renders `error`).
test('user tool_result (error) → tool.failed with error populated', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'user',
      message: {
        content: [
          { type: 'tool_result', tool_use_id: 'toolu_9', content: 'boom: file not found', is_error: true },
        ],
      },
    }),
    emit,
  );
  assert.equal(events[0].type, 'tool.failed');
  assert.equal(events[0].payload.output, 'boom: file not found');
  assert.equal(events[0].payload.error, 'boom: file not found');
});

// ORACLE: a successful result message → turn.completed + usage.updated (x2:
// one from the carried usage, one with the cost), and reports completed:true.
test('result success → turn.completed + usage + completed observation', () => {
  const { events, emit } = collector();
  const res = mapMessage(
    asMsg({
      type: 'result',
      subtype: 'success',
      result: 'all done',
      stop_reason: 'end_turn',
      num_turns: 3,
      duration_ms: 1234,
      total_cost_usd: 0.05,
      usage: {
        input_tokens: 100,
        output_tokens: 50,
        cache_read_input_tokens: 10,
        cache_creation_input_tokens: 20,
      },
    }),
    emit,
  );
  const types = events.map((e) => e.type);
  assert.ok(types.includes('turn.completed'), 'expected turn.completed');
  assert.ok(types.includes('usage.updated'), 'expected usage.updated');
  const completed = events.find((e) => e.type === 'turn.completed');
  assert.equal(completed!.payload.result, 'all done');
  assert.equal(completed!.payload.numTurns, 3);
  // The cost-bearing usage.updated carries totalCostUsd from total_cost_usd.
  const costUsage = events.find((e) => e.type === 'usage.updated' && e.payload.totalCostUsd === 0.05);
  assert.ok(costUsage, 'expected a usage.updated carrying total_cost_usd');
  assert.deepEqual(res, { completed: true });
  assertMapperInvariants(events); // same cross-backend contract as opencode
});

// ORACLE: a failed result message → turn.failed (with a non-empty message) and
// an error event (with code = subtype); usage is still emitted.
test('result error → turn.failed + error', () => {
  const { events, emit } = collector();
  const res = mapMessage(
    asMsg({
      type: 'result',
      subtype: 'error_max_turns',
      errors: ['hit the max turn limit'],
      usage: { input_tokens: 1, output_tokens: 1 },
    }),
    emit,
  );
  const failed = events.find((e) => e.type === 'turn.failed');
  const err = events.find((e) => e.type === 'error');
  assert.ok(failed, 'expected turn.failed');
  assert.equal(failed!.payload.message, 'hit the max turn limit');
  assert.ok(err, 'expected error');
  assert.equal(err!.payload.code, 'error_max_turns');
  assert.equal(err!.payload.message, 'hit the max turn limit');
  assert.deepEqual(res, {}, 'a failed result is not a normal completion');
  assertMapperInvariants(events); // same cross-backend contract as opencode
});

// ORACLE: a stream_event text delta → message.delta with delta:true.
test('stream_event content_block_delta(text) → message.delta', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'partial' } },
    }),
    emit,
  );
  assert.equal(events.length, 1);
  assert.equal(events[0].type, 'message.delta');
  assert.deepEqual(events[0].payload, { role: 'assistant', content: 'partial', delta: true });
});

// ORACLE: an unknown SDK message type emits nothing and returns {}.
test('unknown message type → no events', () => {
  const { events, emit } = collector();
  const res = mapMessage(asMsg({ type: 'tool_progress' }), emit);
  assert.equal(events.length, 0);
  assert.deepEqual(res, {});
});

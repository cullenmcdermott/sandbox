// Unit tests for the pure SDK-message → normalized-event mapping (src/mapping.ts).
// mapping.ts imports nothing that loads better-sqlite3, so these tests run under
// CI's `npm install --ignore-scripts` (no native addon). We feed canned SDK
// messages and assert the emitted normalized events + payloads, exercising the
// mapping in isolation from the event log / registry.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { SDKMessage } from '@anthropic-ai/claude-agent-sdk';
import { mapMessage, newStreamToolIndex, todoUpdatedPayload, capToolOutput, type EmitFn } from '../src/mapping.js';
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
    // [V4] the wire key is agentSessionId (schema SessionStartedPayload); the
    // pre-v2 claudeSessionId key here was the resume-history drift this pins.
    agentSessionId: 'claude-abc',
  });
  // MapResult keeps its own internal claudeSessionId observation name.
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
  assert.deepEqual(events[1].payload, {
    role: 'assistant',
    content: 'hello world',
    parentToolUseId: undefined, // main thread: the key is present but undefined (dropped by JSON.stringify)
  });
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

// ORACLE (D8): ToolPayload.tool is schema-required, but a tool_result carries only
// tool_use_id. The name captured on the matching tool.started (threaded via the
// per-turn StreamToolIndex) is recovered onto tool.completed/failed.
test('user tool_result carries the schema-required tool name via streamTools (D8)', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  // Full-message tool_use populates id→name...
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [{ type: 'tool_use', id: 'toolu_x', name: 'Bash', input: { command: 'ls' } }],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
    streamTools,
  );
  // ...so the later id-only tool_result recovers `tool: 'Bash'`.
  mapMessage(
    asMsg({
      type: 'user',
      message: { content: [{ type: 'tool_result', tool_use_id: 'toolu_x', content: 'ok' }] },
    }),
    emit,
    streamTools,
  );
  const completed = events.find((e) => e.type === 'tool.completed');
  assert.ok(completed, 'expected a tool.completed');
  assert.equal(completed!.payload.tool, 'Bash', 'tool name recovered from tool.started');
  // An error result recovers the name too.
  mapMessage(
    asMsg({
      type: 'user',
      message: {
        content: [{ type: 'tool_result', tool_use_id: 'toolu_x', content: 'boom', is_error: true }],
      },
    }),
    emit,
    streamTools,
  );
  const failed = events.find((e) => e.type === 'tool.failed');
  assert.equal(failed!.payload.tool, 'Bash');
});

// ORACLE: a Bash exit code recorded in streamTools.exitCodes by the PostToolUse
// hook rides the matching tool.completed, and the map entry is consumed (deleted)
// so it can't leak onto a later tool_result reusing the id.
test('user tool_result carries the recorded Bash exitCode and consumes it', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  streamTools.exitCodes.set('toolu_ec', 0);
  mapMessage(
    asMsg({
      type: 'user',
      message: { content: [{ type: 'tool_result', tool_use_id: 'toolu_ec', content: 'ok' }] },
    }),
    emit,
    streamTools,
  );
  const completed = events.filter((e) => e.type === 'tool.completed');
  assert.equal(completed.length, 1, 'exactly one tool.completed');
  assert.equal(completed[0].payload.exitCode, 0, 'exitCode 0 rides the event');
  assert.equal(streamTools.exitCodes.has('toolu_ec'), false, 'map entry consumed');
});

// ORACLE: with no recorded exit code, tool.completed omits the exitCode key
// entirely (matches the optional-field omit style — never a null/undefined key).
test('user tool_result without a recorded exitCode omits the field', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  mapMessage(
    asMsg({
      type: 'user',
      message: { content: [{ type: 'tool_result', tool_use_id: 'toolu_none', content: 'ok' }] },
    }),
    emit,
    streamTools,
  );
  const completed = events.find((e) => e.type === 'tool.completed');
  assert.ok(completed, 'expected a tool.completed');
  assert.equal('exitCode' in completed!.payload, false, 'no exitCode key when none recorded');
});

// ORACLE: an is_error tool_result with a recorded non-zero exit code carries it
// on tool.failed (a failed Bash command reports its code the same way).
test('user tool_result (error) carries the recorded Bash exitCode', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  streamTools.exitCodes.set('toolu_fail', 127);
  mapMessage(
    asMsg({
      type: 'user',
      message: {
        content: [{ type: 'tool_result', tool_use_id: 'toolu_fail', content: 'not found', is_error: true }],
      },
    }),
    emit,
    streamTools,
  );
  const failed = events.find((e) => e.type === 'tool.failed');
  assert.ok(failed, 'expected a tool.failed');
  assert.equal(failed!.payload.exitCode, 127);
  assert.equal(streamTools.exitCodes.has('toolu_fail'), false, 'map entry consumed');
});

// ORACLE (D8): the streaming content_block_start(tool_use) also populates the
// id→name map, so tool.delta — which schema-requires `tool` but only knows the
// block index — carries the tool name too.
test('stream_event tool.delta carries the schema-required tool name (D8)', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: {
        type: 'content_block_start',
        index: 0,
        content_block: { type: 'tool_use', id: 'tu_d', name: 'Edit', input: {} },
      },
    }),
    emit,
    streamTools,
  );
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: { type: 'content_block_delta', index: 0, delta: { type: 'input_json_delta', partial_json: '{"f' } },
    }),
    emit,
    streamTools,
  );
  const delta = events.find((e) => e.type === 'tool.delta');
  assert.ok(delta, 'expected a tool.delta');
  assert.equal(delta!.payload.tool, 'Edit');
  assert.equal(delta!.payload.toolUseId, 'tu_d');
});

// ORACLE: capToolOutput leaves within-cap output untouched, and truncates an
// oversized output to a bounded head+tail with a byte-count marker.
test('capToolOutput leaves small output unchanged', () => {
  const s = 'a'.repeat(1000);
  assert.equal(capToolOutput(s), s);
});

test('capToolOutput truncates oversized output to a bounded head+tail', () => {
  const cap = 64 * 1024;
  const s = 'x'.repeat(cap * 3);
  const capped = capToolOutput(s);
  // Bounded: never larger than the cap plus the short marker line.
  assert.ok(capped.length <= cap + 64, `capped length ${capped.length} exceeds cap+marker`);
  assert.match(capped, /… \d+ bytes truncated …/);
  // Both ends survive.
  assert.ok(capped.startsWith('x'));
  assert.ok(capped.endsWith('x'));
});

// ORACLE: an oversized tool_result carries the capped output on tool.completed.
test('user tool_result caps an oversized output', () => {
  const { events, emit } = collector();
  const big = 'y'.repeat(64 * 1024 * 2);
  mapMessage(
    asMsg({
      type: 'user',
      message: {
        content: [{ type: 'tool_result', tool_use_id: 'toolu_big', content: big }],
      },
    }),
    emit,
  );
  assert.equal(events[0].type, 'tool.completed');
  const out = events[0].payload.output as string;
  assert.ok(out.length < big.length, 'output was not capped');
  assert.match(out, /… \d+ bytes truncated …/);
});

// ORACLE (D12): a successful result message → turn.completed + EXACTLY ONE
// usage.updated carrying the real cost (previously two back-to-back rows: one
// stamped cost 0, one with the cost), and reports completed:true.
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
  const usages = events.filter((e) => e.type === 'usage.updated');
  assert.equal(usages.length, 1, 'D12: exactly one usage.updated (no back-to-back double)');
  const completed = events.find((e) => e.type === 'turn.completed');
  assert.equal(completed!.payload.result, 'all done');
  assert.equal(completed!.payload.numTurns, 3);
  // The single usage.updated carries the real totalCostUsd + cache tokens.
  assert.equal(usages[0].payload.totalCostUsd, 0.05);
  assert.equal(usages[0].payload.cacheReadTokens, 10);
  assert.equal(usages[0].payload.cacheWriteTokens, 20);
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
      total_cost_usd: 0.02,
      usage: { input_tokens: 7, output_tokens: 3, cache_read_input_tokens: 4 },
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
  // D12: a failed turn still reports its real cost (was dropped as totalCostUsd:0).
  const usages = events.filter((e) => e.type === 'usage.updated');
  assert.equal(usages.length, 1, 'exactly one usage.updated on a failed turn');
  assert.equal(usages[0].payload.totalCostUsd, 0.02);
  assert.equal(usages[0].payload.cacheReadTokens, 4);
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
  assert.deepEqual(events[0].payload, {
    role: 'assistant',
    content: 'partial',
    delta: true,
    parentToolUseId: undefined, // main thread: undefined key, dropped on the wire
  });
});

// ORACLE (§2b gap 1): a subagent's stream carries its Task's parent_tool_use_id
// on EVERY message.* / reasoning.* emit — not just tool.* — so clients can route
// the narration to the Task card instead of interleaving it into (and
// corrupting) the main streaming reply.
test('parented assistant message → message.* / reasoning.* carry parentToolUseId', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      parent_tool_use_id: 'task_9',
      message: {
        content: [
          { type: 'thinking', thinking: 'sub thinking' },
          { type: 'text', text: 'sub reply' },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const payload = (t: string) => events.find((e) => e.type === t)!.payload;
  for (const t of ['reasoning.started', 'reasoning.completed', 'message.started', 'message.completed']) {
    assert.equal(payload(t).parentToolUseId, 'task_9', `${t} must carry the Task id`);
  }
  assert.equal(payload('message.completed').content, 'sub reply');
});

// ORACLE (V16): a parented (subagent) assistant message must NOT emit
// usage.updated — its usage reflects the subagent's small context and would
// clobber the main thread's ctx% gauge mid Task fan-out. An unparented
// (main-thread) assistant message still emits usage.updated as before.
test('parented assistant message emits NO usage.updated; unparented still does', () => {
  const parented = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      parent_tool_use_id: 'task_9',
      message: {
        content: [{ type: 'text', text: 'sub reply' }],
        usage: { input_tokens: 10, output_tokens: 5 },
      },
    }),
    parented.emit,
  );
  assert.equal(
    parented.events.some((e) => e.type === 'usage.updated'),
    false,
    'subagent usage must not reach the ctx% gauge',
  );

  const main = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [{ type: 'text', text: 'main reply' }],
        usage: { input_tokens: 100, output_tokens: 20 },
      },
    }),
    main.emit,
  );
  assert.equal(
    main.events.filter((e) => e.type === 'usage.updated').length,
    1,
    'main-thread usage still emits exactly one usage.updated',
  );
});

test('parented stream text/thinking deltas carry parentToolUseId; main-thread ones do not', () => {
  const { events, emit } = collector();
  const delta = (parent: string | undefined, d: Record<string, unknown>) =>
    mapMessage(
      asMsg({
        type: 'stream_event',
        parent_tool_use_id: parent,
        event: { type: 'content_block_delta', index: 0, delta: d },
      }),
      emit,
    );
  delta('task_9', { type: 'text_delta', text: 'sub' });
  delta('task_9', { type: 'thinking_delta', thinking: 'hmm' });
  delta(undefined, { type: 'text_delta', text: 'main' });
  assert.deepEqual(
    events.map((e) => [e.type, e.payload.parentToolUseId]),
    [
      ['message.delta', 'task_9'],
      ['reasoning.delta', 'task_9'],
      ['message.delta', undefined],
    ],
  );
});

test('parented user string message → role:user message.* carry parentToolUseId', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({ type: 'user', parent_tool_use_id: 'task_9', message: { content: 'do the subtask' } }),
    emit,
  );
  assert.deepEqual(
    events.map((e) => e.type),
    ['message.started', 'message.completed'],
  );
  assert.equal(events[1].payload.role, 'user');
  assert.equal(events[1].payload.parentToolUseId, 'task_9');
});

// ORACLE (D6): input_json_delta events are attributed to the tool_use block
// opened at their content-block index — toolUseId + parentToolUseId ride the
// tool.delta payload so the client can target the exact card instead of
// guessing "newest pending". Main-thread and subagent streams have independent
// index spaces, so the same index must not cross-attribute.
test('stream_event input_json_delta → tool.delta carries toolUseId + parentToolUseId', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  // Main-thread tool_use opens at index 1.
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: {
        type: 'content_block_start',
        index: 1,
        content_block: { type: 'tool_use', id: 'tu_main', name: 'Bash', input: {} },
      },
    }),
    emit,
    streamTools,
  );
  // A subagent's tool_use opens at the SAME index in its own stream.
  mapMessage(
    asMsg({
      type: 'stream_event',
      parent_tool_use_id: 'task_1',
      event: {
        type: 'content_block_start',
        index: 1,
        content_block: { type: 'tool_use', id: 'tu_child', name: 'Read', input: {} },
      },
    }),
    emit,
    streamTools,
  );
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: { type: 'content_block_delta', index: 1, delta: { type: 'input_json_delta', partial_json: '{"cmd' } },
    }),
    emit,
    streamTools,
  );
  mapMessage(
    asMsg({
      type: 'stream_event',
      parent_tool_use_id: 'task_1',
      event: { type: 'content_block_delta', index: 1, delta: { type: 'input_json_delta', partial_json: '{"file' } },
    }),
    emit,
    streamTools,
  );
  const deltas = events.filter((e) => e.type === 'tool.delta');
  assert.equal(deltas.length, 2);
  assert.equal(deltas[0].payload.toolUseId, 'tu_main');
  assert.equal(deltas[0].payload.parentToolUseId, undefined);
  assert.equal(deltas[1].payload.toolUseId, 'tu_child');
  assert.equal(deltas[1].payload.parentToolUseId, 'task_1');
});

// ORACLE (D6): a non-tool block reusing a tool_use block's index clears the
// stale attribution; a delta at that index degrades to id-less rather than
// pointing at the wrong tool.
test('stream_event index reuse by a text block clears the tool attribution', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: {
        type: 'content_block_start',
        index: 0,
        content_block: { type: 'tool_use', id: 'tu_old', name: 'Bash', input: {} },
      },
    }),
    emit,
    streamTools,
  );
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } },
    }),
    emit,
    streamTools,
  );
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: { type: 'content_block_delta', index: 0, delta: { type: 'input_json_delta', partial_json: 'x' } },
    }),
    emit,
    streamTools,
  );
  const delta = events.find((e) => e.type === 'tool.delta');
  assert.ok(delta, 'expected a tool.delta');
  assert.equal(delta!.payload.toolUseId, undefined, 'stale tool_use id must not survive index reuse');
});

// ORACLE: an unknown SDK message type emits nothing and returns {}.
test('unknown message type → no events', () => {
  const { events, emit } = collector();
  const res = mapMessage(asMsg({ type: 'api_retry' }), emit);
  assert.equal(events.length, 0);
  assert.deepEqual(res, {});
});

// ORACLE: a tool_progress heartbeat → exactly one tool.progress event carrying the
// tool name, tool_use id, and elapsed seconds. On the main thread parentToolUseId
// is undefined (dropped by JSON.stringify); it returns no registry observation.
test('tool_progress → single tool.progress (main thread)', () => {
  const { events, emit } = collector();
  const res = mapMessage(
    asMsg({
      type: 'tool_progress',
      tool_use_id: 'toolu_run',
      tool_name: 'Bash',
      parent_tool_use_id: null,
      elapsed_time_seconds: 12.5,
    }),
    emit,
  );
  assert.equal(events.length, 1);
  assert.equal(events[0].type, 'tool.progress');
  assert.deepEqual(events[0].payload, {
    tool: 'Bash',
    toolUseId: 'toolu_run',
    elapsedSeconds: 12.5,
    parentToolUseId: undefined, // main thread: undefined key, dropped on the wire
  });
  assert.deepEqual(res, {});
});

// ORACLE (§2b gap 1): a subagent's tool_progress carries its Task's
// parent_tool_use_id so the heartbeat routes to the Task card, not the main thread.
test('tool_progress from a subagent carries parentToolUseId', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'tool_progress',
      tool_use_id: 'toolu_child',
      tool_name: 'Read',
      parent_tool_use_id: 'task_7',
      elapsed_time_seconds: 3,
    }),
    emit,
  );
  assert.equal(events.length, 1);
  assert.equal(events[0].type, 'tool.progress');
  assert.equal(events[0].payload.toolUseId, 'toolu_child');
  assert.equal(events[0].payload.parentToolUseId, 'task_7');
});

// --- §2b gap 6: citations + server-tool results ---------------------------

// ORACLE: text-block citations ride message.completed (flattened + deduped);
// message.started never carries them.
test('text block citations → message.completed carries deduped citations', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'text',
            text: 'cited answer',
            citations: [
              {
                type: 'web_search_result_location',
                url: 'https://example.com/a',
                title: 'Example A',
                cited_text: 'quoted bit',
                encrypted_index: 'x',
              },
              {
                type: 'char_location',
                document_title: 'Doc B',
                cited_text: 'doc quote',
                document_index: 0,
                start_char_index: 0,
                end_char_index: 9,
              },
              {
                // duplicate of the first (same url+title) — deduped
                type: 'web_search_result_location',
                url: 'https://example.com/a',
                title: 'Example A',
                cited_text: 'other quote',
                encrypted_index: 'y',
              },
              {
                // neither url nor title — dropped (nothing to render)
                type: 'char_location',
                document_title: null,
                cited_text: 'orphan',
                document_index: 1,
                start_char_index: 0,
                end_char_index: 6,
              },
            ],
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const started = events.find((e) => e.type === 'message.started')!;
  assert.ok(!('citations' in started.payload), 'message.started must not carry citations');
  const completed = events.find((e) => e.type === 'message.completed')!;
  assert.deepEqual(completed.payload.citations, [
    { url: 'https://example.com/a', title: 'Example A', citedText: 'quoted bit' },
    { title: 'Doc B', citedText: 'doc quote' },
  ]);
});

// citedText is capped at 200 chars at the source (event-log bloat guard).
test('citation cited_text capped at 200 chars', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'text',
            text: 'long quote',
            citations: [
              {
                type: 'web_search_result_location',
                url: 'https://example.com',
                title: 'T',
                cited_text: 'x'.repeat(500),
                encrypted_index: 'x',
              },
            ],
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const completed = events.find((e) => e.type === 'message.completed')!;
  const cites = completed.payload.citations as Array<{ citedText: string }>;
  assert.equal(cites[0].citedText.length, 200);
});

// citations: null (the common no-citations case) → no citations key at all.
test('text block with null citations → no citations key', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [{ type: 'text', text: 'plain', citations: null }],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const completed = events.find((e) => e.type === 'message.completed')!;
  assert.ok(!('citations' in completed.payload));
});

// ORACLE: a server_tool_use web_search block maps to a normal tool.started.
test('server_tool_use web_search → tool.started', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          { type: 'server_tool_use', id: 'srvtoolu_1', name: 'web_search', input: { query: 'golang generics' } },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
    streamTools,
  );
  const started = events.filter((e) => e.type === 'tool.started');
  assert.equal(started.length, 1);
  assert.equal(started[0].payload.tool, 'web_search');
  assert.equal(started[0].payload.toolUseId, 'srvtoolu_1');
  assert.deepEqual(started[0].payload.input, { query: 'golang generics' });
});

// ORACLE: a web_search_tool_result maps to tool.completed with the result list;
// the name is recovered from the server_tool_use registration (D8 path).
test('web_search_tool_result → tool.completed with formatted results', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          { type: 'server_tool_use', id: 'srvtoolu_2', name: 'web_search', input: { query: 'q' } },
          {
            type: 'web_search_tool_result',
            tool_use_id: 'srvtoolu_2',
            content: [
              { type: 'web_search_result', title: 'First', url: 'https://a.example', page_age: null, encrypted_content: 'e1' },
              { type: 'web_search_result', title: 'Second', url: 'https://b.example', page_age: null, encrypted_content: 'e2' },
            ],
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
    streamTools,
  );
  const completed = events.filter((e) => e.type === 'tool.completed');
  assert.equal(completed.length, 1);
  assert.equal(completed[0].payload.tool, 'web_search');
  assert.equal(completed[0].payload.toolUseId, 'srvtoolu_2');
  const out = completed[0].payload.output as string;
  assert.ok(out.startsWith('2 results'), out);
  assert.ok(out.includes('First — https://a.example'), out);
  assert.ok(out.includes('Second — https://b.example'), out);
});

// The web_search error shape maps to tool.failed carrying the error_code.
test('web_search_tool_result error → tool.failed', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'web_search_tool_result',
            tool_use_id: 'srvtoolu_3',
            content: { type: 'web_search_tool_result_error', error_code: 'max_uses_exceeded' },
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const failed = events.filter((e) => e.type === 'tool.failed');
  assert.equal(failed.length, 1);
  assert.equal(failed[0].payload.tool, 'web_search'); // fallback name (no registration)
  assert.ok((failed[0].payload.error as string).includes('max_uses_exceeded'));
});

// web_fetch result → tool.completed with the fetched URL; error → tool.failed.
test('web_fetch_tool_result success and error shapes', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'web_fetch_tool_result',
            tool_use_id: 'srvtoolu_4',
            content: {
              type: 'web_fetch_result',
              url: 'https://example.com/doc',
              retrieved_at: null,
              content: { type: 'document', source: { type: 'text', data: 'body', media_type: 'text/plain' }, title: null, citations: null, context: null },
            },
          },
          {
            type: 'web_fetch_tool_result',
            tool_use_id: 'srvtoolu_5',
            content: { type: 'web_fetch_tool_result_error', error_code: 'url_not_accessible' },
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const completed = events.filter((e) => e.type === 'tool.completed');
  assert.equal(completed.length, 1);
  assert.equal(completed[0].payload.tool, 'web_fetch');
  assert.equal(completed[0].payload.output, 'fetched https://example.com/doc');
  const failed = events.filter((e) => e.type === 'tool.failed');
  assert.equal(failed.length, 1);
  assert.ok((failed[0].payload.error as string).includes('url_not_accessible'));
});

// Unmapped server tools (code_execution family etc.) stay dropped — a started
// with no mapped terminal would dangle a "running" card.
test('server_tool_use code_execution → no events', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          { type: 'server_tool_use', id: 'srvtoolu_6', name: 'code_execution', input: { code: '1+1' } },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  assert.deepEqual(
    events.map((e) => e.type),
    ['usage.updated'], // only the message-level usage emit — the block itself is dropped
  );
});

// Streaming: a server_tool_use content_block_start registers the id at its
// index so input_json_delta attributes to it (same D6 path as tool_use).
test('stream server_tool_use start + input_json_delta → attributed tool.delta', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: {
        type: 'content_block_start',
        index: 2,
        content_block: { type: 'server_tool_use', id: 'srvtoolu_7', name: 'web_search', input: {} },
      },
    }),
    emit,
    streamTools,
  );
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: { type: 'content_block_delta', index: 2, delta: { type: 'input_json_delta', partial_json: '{"query' } },
    }),
    emit,
    streamTools,
  );
  const started = events.filter((e) => e.type === 'tool.started');
  assert.equal(started.length, 1);
  assert.equal(started[0].payload.tool, 'web_search');
  const deltas = events.filter((e) => e.type === 'tool.delta');
  assert.equal(deltas.length, 1);
  assert.equal(deltas[0].payload.toolUseId, 'srvtoolu_7');
  assert.equal(deltas[0].payload.tool, 'web_search'); // D8: name recovered from the registration
});

// An UNMAPPED server_tool_use at a reused index clears the slot (no stale
// attribution) and emits nothing.
test('stream unmapped server_tool_use clears index attribution', () => {
  const { events, emit } = collector();
  const streamTools = newStreamToolIndex();
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: {
        type: 'content_block_start',
        index: 0,
        content_block: { type: 'tool_use', id: 'tu_1', name: 'Bash', input: {} },
      },
    }),
    emit,
    streamTools,
  );
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: {
        type: 'content_block_start',
        index: 0,
        content_block: { type: 'server_tool_use', id: 'srvtoolu_8', name: 'code_execution', input: {} },
      },
    }),
    emit,
    streamTools,
  );
  mapMessage(
    asMsg({
      type: 'stream_event',
      event: { type: 'content_block_delta', index: 0, delta: { type: 'input_json_delta', partial_json: '{}' } },
    }),
    emit,
    streamTools,
  );
  const deltas = events.filter((e) => e.type === 'tool.delta');
  assert.equal(deltas.length, 1);
  assert.equal(deltas[0].payload.toolUseId, undefined); // cleared — not tu_1, not srvtoolu_8
});

// A subagent's server_tool_use carries parentToolUseId like any child tool.
test('parented server_tool_use → tool.started carries parentToolUseId', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      parent_tool_use_id: 'task_9',
      message: {
        content: [
          { type: 'server_tool_use', id: 'srvtoolu_9', name: 'web_fetch', input: { url: 'https://x.example' } },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const started = events.filter((e) => e.type === 'tool.started');
  assert.equal(started.length, 1);
  assert.equal(started[0].payload.parentToolUseId, 'task_9');
});

// Truncation must not leave a lone high surrogate (Go-side JSON decode would
// turn it into U+FFFD): an astral char straddling the 200 cap is dropped whole.
test('citation cited_text cap never splits a surrogate pair', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'text',
            text: 'q',
            citations: [
              {
                type: 'web_search_result_location',
                url: 'https://example.com',
                title: 'T',
                cited_text: 'x'.repeat(199) + '😀rest', // surrogate pair spans indexes 199-200
                encrypted_index: 'x',
              },
            ],
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const completed = events.find((e) => e.type === 'message.completed')!;
  const cites = completed.payload.citations as Array<{ citedText: string }>;
  assert.equal(cites[0].citedText.length, 199); // the split pair's high half was dropped
  assert.ok(!/[\ud800-\udbff]$/.test(cites[0].citedText), 'must not end with a lone high surrogate');
});

// Dedup key uses a NUL separator: url 'a b' + title 'c' must NOT collide with
// url 'a' + title 'b c'.
test('citation dedup key does not collide across url/title boundary', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'text',
            text: 'q',
            citations: [
              { type: 'web_search_result_location', url: 'a b', title: 'c', cited_text: '1', encrypted_index: 'x' },
              { type: 'web_search_result_location', url: 'a', title: 'b c', cited_text: '2', encrypted_index: 'y' },
            ],
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const completed = events.find((e) => e.type === 'message.completed')!;
  assert.equal((completed.payload.citations as unknown[]).length, 2);
});

// Mapping stays total under SDK drift AND never orphans a started card: a
// *_tool_result whose content is outside the known union maps to a degraded
// tool.completed (closing the card) instead of throwing or dropping.
test('server-tool results with unknown content shapes close the card, never throw', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          { type: 'web_search_tool_result', tool_use_id: 's1', content: null },
          { type: 'web_search_tool_result', tool_use_id: 's2', content: { type: 'future_shape' } },
          { type: 'web_fetch_tool_result', tool_use_id: 's3', content: null },
          { type: 'web_fetch_tool_result', tool_use_id: 's4', content: { type: 'future_shape' } },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const completed = events.filter((e) => e.type === 'tool.completed');
  assert.equal(completed.length, 4); // every unknown-shape result still terminates its card
  for (const e of completed) {
    assert.ok((e.payload.output as string).includes('unrecognized'), String(e.payload.output));
  }
});

// A web_search success entry outside the known shape renders what it has, never
// the literal string "undefined".
test('web_search result entries missing fields degrade cleanly', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'web_search_tool_result',
            tool_use_id: 's5',
            content: [
              { type: 'web_search_result', title: 'Only Title', page_age: null, encrypted_content: 'e' },
              { type: 'future_entry' },
            ],
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const out = events.find((e) => e.type === 'tool.completed')!.payload.output as string;
  assert.ok(!out.includes('undefined'), out);
  assert.ok(out.includes('Only Title'), out);
  assert.ok(out.includes('(unrecognized result)'), out);
});

// A null element inside a citations array is skipped, not dereferenced — a
// throw here would abort the whole turn.
test('null citation elements are skipped, never throw', () => {
  const { events, emit } = collector();
  mapMessage(
    asMsg({
      type: 'assistant',
      message: {
        content: [
          {
            type: 'text',
            text: 'q',
            citations: [
              null,
              42,
              { type: 'web_search_result_location', url: 'https://ok.example', title: 'OK', cited_text: 'c', encrypted_index: 'x' },
            ],
          },
        ],
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    }),
    emit,
  );
  const completed = events.find((e) => e.type === 'message.completed')!;
  assert.deepEqual(completed.payload.citations, [{ url: 'https://ok.example', title: 'OK', citedText: 'c' }]);
});

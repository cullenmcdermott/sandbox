// Unit tests for the opencode turn adapter's pure pieces: the event mapper
// (createOpencodeTurnMapper) and the model parser. The mapper is driven with
// synthetic opencode events and a capturing emit, so it runs with no server.
//
// Ordering mirrors the real opencode stream: message.updated(role) for a message
// always precedes that message's message.part.updated events. The user prompt is
// itself a text part on the *user* message, so the mapper must emit only parts of
// the *assistant* message (no echoing the prompt back).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Event } from '@opencode-ai/sdk';

import {
  createOpencodeTurnMapper,
  parseOpencodeModel,
  errToString,
  isMissingSessionError,
  mapPermissionDecision,
  autoPermissionResponse,
  effectiveOpencodeSession,
  effectiveOpencodeModel,
  emitOpencodeUserPrompt,
  type Emit,
} from '../src/opencode-turn.js';
import { assertMapperInvariants } from './backend-contract.js';

const SID = 'ses_test';
const ASSISTANT = 'msg_assistant';
const USER = 'msg_user';

// Minimal synthetic event builders (cast through unknown: we exercise mapping
// logic, not opencode's full type shapes).
function messageUpdated(role: 'user' | 'assistant', id: string, opts: { cost?: number; error?: unknown } = {}): Event {
  return {
    type: 'message.updated',
    properties: {
      info: {
        id,
        sessionID: SID,
        role,
        tokens: { input: 10, output: 5, reasoning: 0, cache: { read: 2, write: 3 } },
        cost: opts.cost ?? 0,
        ...(opts.error ? { error: opts.error } : {}),
      },
    },
  } as unknown as Event;
}

function textPart(
  partId: string,
  text: string,
  opts: { delta?: string; end?: boolean; sessionID?: string; messageID?: string } = {},
): Event {
  return {
    type: 'message.part.updated',
    properties: {
      part: {
        id: partId,
        sessionID: opts.sessionID ?? SID,
        messageID: opts.messageID ?? ASSISTANT,
        type: 'text',
        text,
        time: opts.end ? { start: 1, end: 2 } : { start: 1 },
      },
      delta: opts.delta,
    },
  } as unknown as Event;
}

function toolPart(partId: string, status: 'running' | 'completed' | 'error', extra: Record<string, unknown>): Event {
  return {
    type: 'message.part.updated',
    properties: {
      part: {
        id: partId,
        sessionID: SID,
        messageID: ASSISTANT,
        type: 'tool',
        callID: 'call_1',
        tool: 'bash',
        state: { status, input: { cmd: 'ls' }, ...extra },
      },
    },
  } as unknown as Event;
}

function sessionIdle(sessionID = SID): Event {
  return { type: 'session.idle', properties: { sessionID } } as unknown as Event;
}

function sessionError(error: unknown, sessionID = SID): Event {
  return { type: 'session.error', properties: { sessionID, error } } as unknown as Event;
}

// message.part.delta is opencode's streaming text channel — NOT in the SDK's
// typed Event union, so it is cast through unknown like the others.
function partDelta(
  delta: string,
  opts: { field?: string; messageID?: string; partID?: string; sessionID?: string } = {},
): Event {
  return {
    type: 'message.part.delta',
    properties: {
      delta,
      field: opts.field ?? 'text',
      messageID: opts.messageID ?? ASSISTANT,
      partID: opts.partID ?? 'p1',
      sessionID: opts.sessionID ?? SID,
    },
  } as unknown as Event;
}

function permissionUpdated(
  id: string,
  opts: { type?: string; title?: string; sessionID?: string; callID?: string; metadata?: Record<string, unknown> } = {},
): Event {
  return {
    type: 'permission.updated',
    properties: {
      id,
      type: opts.type ?? 'bash',
      title: opts.title ?? 'Allow running ls?',
      sessionID: opts.sessionID ?? SID,
      messageID: ASSISTANT,
      ...(opts.callID ? { callID: opts.callID } : {}),
      metadata: opts.metadata ?? { command: 'ls' },
      time: { created: 1 },
    },
  } as unknown as Event;
}

function permissionReplied(permissionID: string, response: string, sessionID = SID): Event {
  return {
    type: 'permission.replied',
    properties: { sessionID, permissionID, response },
  } as unknown as Event;
}

function capture(): { emit: Emit; events: Array<{ type: string; payload: Record<string, unknown> }> } {
  const events: Array<{ type: string; payload: Record<string, unknown> }> = [];
  const emit: Emit = (type, payload) => {
    events.push({ type, payload });
  };
  return { emit, events };
}

test('assistant text turn maps to message.started/delta/completed + usage + turn.completed', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);

  m.handle(messageUpdated('assistant', ASSISTANT)); // registers role + usage.updated
  assert.equal(m.handle(textPart('p1', 'Hi', { delta: 'Hi' })), false);
  assert.equal(m.handle(textPart('p1', 'Hi there', { delta: ' there', end: true })), false);
  assert.equal(m.handle(sessionIdle()), true); // settles

  assert.deepEqual(
    events.map((e) => e.type),
    ['usage.updated', 'message.started', 'message.delta', 'message.delta', 'message.completed', 'turn.completed'],
  );
  const completed = events.find((e) => e.type === 'message.completed');
  assert.equal(completed?.payload.content, 'Hi there');
  assert.equal(completed?.payload.role, 'assistant');
  const usage = events.find((e) => e.type === 'usage.updated');
  assert.deepEqual(usage?.payload, {
    inputTokens: 10,
    outputTokens: 5,
    cacheReadTokens: 2,
    cacheWriteTokens: 3,
    totalCostUsd: 0,
  });
  assert.equal(m.settled, 'completed');
  assertMapperInvariants(events); // shared cross-backend contract
});

test('the user prompt part is NOT echoed back as an assistant message', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);

  // Real order: user message + its prompt part, THEN assistant message + reply.
  m.handle(messageUpdated('user', USER));
  m.handle(textPart('pu', 'Reply with a short greeting.', { messageID: USER, end: true }));
  m.handle(messageUpdated('assistant', ASSISTANT));
  m.handle(textPart('pa', 'Hi there!', { delta: 'Hi there!', end: true }));
  m.handle(sessionIdle());

  const contents = events.filter((e) => e.type === 'message.completed').map((e) => e.payload.content);
  assert.deepEqual(contents, ['Hi there!']); // only the assistant reply, never the prompt
});

test('session.idle flushes an unterminated assistant text part as message.completed', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT));
  m.handle(textPart('p1', 'partial reply', { delta: 'partial reply' })); // no end marker
  m.handle(sessionIdle());
  const completed = events.filter((e) => e.type === 'message.completed');
  assert.equal(completed.length, 1);
  assert.equal(completed[0].payload.content, 'partial reply');
  assert.equal(events.at(-1)?.type, 'turn.completed');
});

test('events for a different session are ignored', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT));
  events.length = 0; // drop the usage from registration; focus on the foreign session
  m.handle(textPart('p1', 'other', { delta: 'other', sessionID: 'ses_other' }));
  m.handle(sessionIdle('ses_other'));
  assert.equal(events.length, 0);
  assert.equal(m.settled, undefined);
});

test('assistant message error settles as turn.failed and blocks later completion', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  assert.equal(m.handle(messageUpdated('assistant', ASSISTANT, { error: { data: { message: 'rate limited' } } })), true);
  m.handle(sessionIdle()); // must NOT emit turn.completed after a failure
  const types = events.map((e) => e.type);
  assert.ok(types.includes('turn.failed'));
  assert.ok(types.includes('error'));
  assert.ok(!types.includes('turn.completed'));
  assert.equal(events.find((e) => e.type === 'turn.failed')?.payload.message, 'rate limited');
  assert.equal(m.settled, 'failed');
  assertMapperInvariants(events); // contract: single terminal, non-empty failure message
});

test('session.error settles as turn.failed', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  assert.equal(m.handle(sessionError({ data: { message: 'provider auth failed' } })), true);
  assert.equal(m.settled, 'failed');
  assert.equal(events.find((e) => e.type === 'turn.failed')?.payload.message, 'provider auth failed');
});

test('assistant tool part maps to tool.started then tool.completed', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT));
  events.length = 0; // drop usage; focus on tool mapping
  m.handle(toolPart('t1', 'running', {}));
  m.handle(toolPart('t1', 'completed', { output: 'file1\nfile2' }));
  assert.deepEqual(
    events.map((e) => e.type),
    ['tool.started', 'tool.completed'],
  );
  assert.equal(events[0].payload.tool, 'bash');
  assert.equal(events[0].payload.toolUseId, 'call_1');
  assert.equal(events[1].payload.output, 'file1\nfile2');
});

test('the injected audit callback fires once per tool execution with tool + input', () => {
  const { emit } = capture();
  const audited: Array<{ tool: string; input: unknown }> = [];
  const m = createOpencodeTurnMapper(SID, emit, (tool, input) => audited.push({ tool, input }));
  m.handle(messageUpdated('assistant', ASSISTANT));
  m.handle(toolPart('t1', 'running', {}));
  m.handle(toolPart('t1', 'completed', { output: 'done' })); // re-sent/terminal part
  assert.equal(audited.length, 1, 'audited once, guarded by toolStarted');
  assert.deepEqual(audited[0], { tool: 'bash', input: { cmd: 'ls' } });
});

test('a tool the guardrail blocks (errored, never running) is still audited', () => {
  const { emit } = capture();
  const audited: Array<{ tool: string; input: unknown }> = [];
  const m = createOpencodeTurnMapper(SID, emit, (tool, input) => audited.push({ tool, input }));
  m.handle(messageUpdated('assistant', ASSISTANT));
  // opencode surfaces a blocked tool as an errored part with no prior running state.
  m.handle(toolPart('t1', 'error', { error: 'blocked by sandbox runner guardrail' }));
  assert.equal(audited.length, 1);
  assert.equal(audited[0].tool, 'bash');
});

// ORACLE (H6): opencode tool output is capped at the emit site with the same
// capToolOutput the claude path uses — an uncapped result would bloat the
// SQLite log, the SSE stream, and the TUI expansion alike.
test('oversized opencode tool output is capped like the claude path', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT));
  events.length = 0;
  m.handle(toolPart('t1', 'running', {}));
  m.handle(toolPart('t1', 'completed', { output: 'x'.repeat(200 * 1024) }));
  const out = events.find((e) => e.type === 'tool.completed')?.payload.output as string;
  assert.ok(out.length < 100 * 1024, `output not capped: ${out.length} bytes`);
  assert.ok(out.includes('bytes truncated'), 'expected the truncation marker');
});

test('a re-sent terminal tool part fires tool.completed only once', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT));
  events.length = 0;
  m.handle(toolPart('t1', 'running', {}));
  m.handle(toolPart('t1', 'completed', { output: 'ok' }));
  m.handle(toolPart('t1', 'completed', { output: 'ok' })); // opencode may re-emit
  assert.deepEqual(
    events.map((e) => e.type),
    ['tool.started', 'tool.completed'],
  );
});

test('session.error without our sessionID does NOT fail the turn', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  // A sessionID-less or foreign global error must be ignored (symmetric w/ idle).
  assert.equal(m.handle({ type: 'session.error', properties: { error: { data: { message: 'global' } } } } as unknown as Event), false);
  assert.equal(m.handle(sessionError({ data: { message: 'other' } }, 'ses_other')), false);
  assert.equal(m.settled, undefined);
  assert.equal(events.length, 0);
});

test('a partial assistant message.updated (missing tokens) does not throw', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  const partial = { type: 'message.updated', properties: { info: { id: ASSISTANT, sessionID: SID, role: 'assistant', cost: 0 } } } as unknown as Event;
  assert.doesNotThrow(() => m.handle(partial));
  const usage = events.find((e) => e.type === 'usage.updated');
  assert.deepEqual(usage?.payload, {
    inputTokens: 0,
    outputTokens: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    totalCostUsd: 0,
  });
});

test('isMissingSessionError detects 404 and not-found messages', () => {
  assert.equal(isMissingSessionError({ status: 404 }), true);
  assert.equal(isMissingSessionError({ statusCode: 404 }), true);
  assert.equal(isMissingSessionError({ data: { message: 'Session not found' } }), true);
  assert.equal(isMissingSessionError({ message: 'no such session: ses_x' }), true);
  assert.equal(isMissingSessionError({ status: 500, message: 'boom' }), false);
  assert.equal(isMissingSessionError('timeout'), false);
  assert.equal(isMissingSessionError(undefined), false);
});

test('parseOpencodeModel splits "provider/model", else undefined', () => {
  assert.deepEqual(parseOpencodeModel('opencode/big-pickle'), { providerID: 'opencode', modelID: 'big-pickle' });
  assert.deepEqual(parseOpencodeModel('anthropic/claude-sonnet-4-5'), {
    providerID: 'anthropic',
    modelID: 'claude-sonnet-4-5',
  });
  assert.equal(parseOpencodeModel('opus'), undefined);
  assert.equal(parseOpencodeModel(''), undefined);
  assert.equal(parseOpencodeModel(undefined), undefined);
  assert.equal(parseOpencodeModel('/x'), undefined);
  assert.equal(parseOpencodeModel('x/'), undefined);
});

test('errToString extracts message from common error shapes', () => {
  assert.equal(errToString('plain'), 'plain');
  assert.equal(errToString({ data: { message: 'deep' } }), 'deep');
  assert.equal(errToString({ message: 'mid' }), 'mid');
  assert.equal(errToString(new Error('boom')), 'boom');
  assert.equal(errToString(undefined), 'unknown error');
});

// --- G1: streaming deltas (message.part.delta) ----------------------------

test('message.part.delta(text) streams message.started once then message.delta per chunk', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT)); // registers role + usage
  events.length = 0; // focus on the streaming
  m.handle(partDelta('Hi'));
  m.handle(partDelta(' there'));
  // A final full-text message.part.updated completes the part.
  m.handle(textPart('p1', 'Hi there', { end: true }));
  m.handle(sessionIdle());
  assert.deepEqual(
    events.map((e) => e.type),
    ['message.started', 'message.delta', 'message.delta', 'message.completed', 'turn.completed'],
  );
  assert.equal(events[1].payload.content, 'Hi');
  assert.equal(events[1].payload.delta, true);
  assert.equal(events.find((e) => e.type === 'message.completed')?.payload.content, 'Hi there');
  assertMapperInvariants(events);
});

test('an empty/partial first update then trailing deltas keeps the full streamed reply', () => {
  // Regression: a part announced with an empty text update sets textOf=''; the
  // reply then arrives only as deltas. A naive `textOf ?? streamedText` returns ''
  // (empty string is not nullish) and drops the whole reply — completeTextPart must
  // take the longer of the two.
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT));
  events.length = 0;
  m.handle(textPart('p1', '', {})); // empty/partial first update → textOf['p1']=''
  m.handle(partDelta('Hello '));
  m.handle(partDelta('world'));
  m.handle(sessionIdle());
  const completed = events.find((e) => e.type === 'message.completed');
  assert.equal(completed?.payload.content, 'Hello world');
  assertMapperInvariants(events);
});

test('a deltas-only part (no full-text update) flushes accumulated text on idle', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT));
  events.length = 0;
  m.handle(partDelta('par'));
  m.handle(partDelta('tial'));
  m.handle(sessionIdle()); // no message.part.updated ever arrived
  assert.deepEqual(
    events.map((e) => e.type),
    ['message.started', 'message.delta', 'message.delta', 'message.completed', 'turn.completed'],
  );
  assert.equal(events.find((e) => e.type === 'message.completed')?.payload.content, 'partial');
  assertMapperInvariants(events);
});

test('a delta on the user message is NOT echoed back as assistant content', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('user', USER));
  events.length = 0;
  m.handle(partDelta('the user prompt', { messageID: USER, partID: 'pu' }));
  assert.equal(events.length, 0);
});

test('a delta for a foreign session or non-text field is ignored', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT));
  events.length = 0;
  m.handle(partDelta('x', { sessionID: 'ses_other' }));
  m.handle(partDelta('y', { field: 'reasoning' }));
  m.handle(partDelta('', {})); // empty delta
  assert.equal(events.length, 0);
});

// --- G2: permission flow --------------------------------------------------

test('permission.updated emits permission.requested and queues the id for auto-response', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(permissionUpdated('perm_1', { type: 'bash', title: 'Run ls?', callID: 'call_9' }));
  const requested = events.find((e) => e.type === 'permission.requested');
  assert.ok(requested);
  assert.equal(requested?.payload.permissionId, 'perm_1');
  assert.equal(requested?.payload.tool, 'bash');
  assert.deepEqual(requested?.payload.input, { title: 'Run ls?', callID: 'call_9', command: 'ls' });
  assert.equal(requested?.payload.decision, '');
  assert.deepEqual(m.takePendingPermissions(), ['perm_1']);
  assert.deepEqual(m.takePendingPermissions(), []); // drained
  assertMapperInvariants(events);
});

test('a re-sent permission.updated is deduped (one requested, one pending)', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(permissionUpdated('perm_1'));
  m.handle(permissionUpdated('perm_1')); // opencode may re-send
  assert.equal(events.filter((e) => e.type === 'permission.requested').length, 1);
  assert.deepEqual(m.takePendingPermissions(), ['perm_1']);
});

test('permission.replied emits permission.resolved reusing the requested tool/input', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(permissionUpdated('perm_1', { type: 'edit', title: 'Edit /x?', metadata: { path: '/x' } }));
  m.handle(permissionReplied('perm_1', 'once'));
  const resolved = events.find((e) => e.type === 'permission.resolved');
  assert.ok(resolved);
  assert.equal(resolved?.payload.permissionId, 'perm_1');
  assert.equal(resolved?.payload.tool, 'edit');
  assert.deepEqual(resolved?.payload.input, { title: 'Edit /x?', path: '/x' });
  assert.equal(resolved?.payload.decision, 'allow-once');
  assertMapperInvariants(events);
});

test('permission events for a foreign session are ignored', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(permissionUpdated('perm_x', { sessionID: 'ses_other' }));
  m.handle(permissionReplied('perm_x', 'once', 'ses_other'));
  assert.equal(events.length, 0);
  assert.deepEqual(m.takePendingPermissions(), []);
});

test('a full assistant turn that requests a permission still settles cleanly', () => {
  const { emit, events } = capture();
  const m = createOpencodeTurnMapper(SID, emit);
  m.handle(messageUpdated('assistant', ASSISTANT));
  m.handle(permissionUpdated('perm_1'));
  m.handle(permissionReplied('perm_1', 'once'));
  m.handle(textPart('p1', 'done', { end: true }));
  assert.equal(m.handle(sessionIdle()), true);
  const types = events.map((e) => e.type);
  assert.ok(types.includes('permission.requested'));
  assert.ok(types.includes('permission.resolved'));
  assert.equal(types.at(-1), 'turn.completed');
  assertMapperInvariants(events);
});

test('mapPermissionDecision maps opencode responses to normalized decisions', () => {
  assert.equal(mapPermissionDecision('once'), 'allow-once');
  assert.equal(mapPermissionDecision('always'), 'allow-session');
  assert.equal(mapPermissionDecision('reject'), 'deny');
  assert.equal(mapPermissionDecision('weird'), '');
  assert.equal(mapPermissionDecision(undefined), '');
});

test('autoPermissionResponse defaults to once, honors OPENCODE_AUTO_PERMISSION', () => {
  assert.equal(autoPermissionResponse({}), 'once');
  assert.equal(autoPermissionResponse({ OPENCODE_AUTO_PERMISSION: 'always' }), 'always');
  assert.equal(autoPermissionResponse({ OPENCODE_AUTO_PERMISSION: 'reject' }), 'reject');
  assert.equal(autoPermissionResponse({ OPENCODE_AUTO_PERMISSION: 'garbage' }), 'once');
});

// --- G3: resume / continuity precedence -----------------------------------

test('effectiveOpencodeSession: client resume wins, else persisted, else undefined', () => {
  assert.equal(effectiveOpencodeSession('client', 'persisted'), 'client');
  assert.equal(effectiveOpencodeSession(undefined, 'persisted'), 'persisted');
  assert.equal(effectiveOpencodeSession('', 'persisted'), 'persisted');
  assert.equal(effectiveOpencodeSession(undefined, ''), undefined);
  assert.equal(effectiveOpencodeSession('', ''), undefined);
  assert.equal(effectiveOpencodeSession(undefined, undefined), undefined);
});

// Mirrors claude's resolveModel precedence exactly (model-option.test.ts): a
// per-turn override wins; an EMPTY per-turn model falls back to the session
// default (|| not ??); neither set → undefined (server's free default).
test('effectiveOpencodeModel: per-turn override wins, empty falls back to session default', () => {
  assert.equal(effectiveOpencodeModel('anthropic/x', 'opencode/big-pickle'), 'anthropic/x');
  assert.equal(effectiveOpencodeModel(undefined, 'opencode/big-pickle'), 'opencode/big-pickle');
  assert.equal(effectiveOpencodeModel('', 'opencode/big-pickle'), 'opencode/big-pickle');
  assert.equal(effectiveOpencodeModel(undefined, undefined), undefined);
  assert.equal(effectiveOpencodeModel('', ''), undefined);
  assert.equal(effectiveOpencodeModel('', undefined), undefined);
});

// --- D5: prompt echoed as a role:user message (attach/replay parity) --------

// The runner-driven opencode turn adapter must echo the prompt as a role:"user"
// message so a replayed/attached transcript shows the question, not just the
// answer. Mirrors the Claude adapter's user-echo (mapping.ts handleUserMessage).
test('emitOpencodeUserPrompt echoes the prompt as message.started/completed role:user (D5)', () => {
  const events: Array<{ type: string; payload: Record<string, unknown> }> = [];
  const emit: Emit = (type, payload) => events.push({ type, payload });

  emitOpencodeUserPrompt(emit, 'what is 2+2?');

  assert.deepEqual(
    events.map((e) => e.type),
    ['message.started', 'message.completed'],
  );
  for (const e of events) {
    assert.equal(e.payload.role, 'user');
    assert.equal(e.payload.content, 'what is 2+2?');
  }
});

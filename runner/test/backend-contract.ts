// Shared backend-conformance contract (Phase A of docs/testing-parity-plan.md).
//
// Every backend's event mapper (claude-sdk `mapping.ts`, opencode
// `opencode-turn.ts`, Codex next) emits normalized events through the SAME
// `EmitFn = (type, payload) => void` shape into a captured list. This module
// asserts the invariants EVERY backend's emitted stream must hold, so each
// backend's unit test routes its captured output through one place — that is what
// makes "tested to parity" enforceable rather than per-backend prose.
//
// Not a *.test.ts (no suite of its own): imported by the per-backend mapper tests.

import assert from 'node:assert/strict';
import { ALL_EVENT_TYPES } from '../src/types.js';

export type CapturedEvent = { type: string; payload: Record<string, unknown> };

const KNOWN_EVENT_TYPES = new Set<string>(ALL_EVENT_TYPES);

const TERMINAL = new Set(['turn.completed', 'turn.failed']);
const CONTENT = new Set([
  'message.started',
  'message.delta',
  'message.completed',
  'reasoning.started',
  'reasoning.delta',
  'reasoning.completed',
  'tool.started',
  'tool.delta',
  'tool.completed',
  'tool.failed',
]);

/**
 * Assert the normalized-event invariants every backend mapper must satisfy:
 *  1. at most one terminal event (turn.completed | turn.failed), never both;
 *  2. no CONTENT event (message/tool/reasoning) appears after a terminal
 *     (metadata like usage.updated MAY follow — claude emits usage post-result);
 *  3. every message.* carries role ∈ {user, assistant};
 *  4. turn.failed always carries a non-empty `message` (the TUI decodes the
 *     failure reason from it).
 */
export function assertMapperInvariants(events: CapturedEvent[]): void {
  // Schema conformance (Phase D): every emitted type must be a known EventType
  // from schema/events.json, and every payload an object — no drift to a type the
  // TUI/CLI can't decode. (Go's schema_test.go validates the payload struct shapes.)
  for (const e of events) {
    assert.ok(KNOWN_EVENT_TYPES.has(e.type), `backend contract: unknown event type "${e.type}" (not in schema/events.json)`);
    assert.ok(e.payload != null && typeof e.payload === 'object', `backend contract: ${e.type} payload must be an object`);
  }

  const terminals = events.filter((e) => TERMINAL.has(e.type));
  assert.ok(
    terminals.length <= 1,
    `backend contract: at most one terminal event, got [${terminals.map((e) => e.type).join(', ')}]`,
  );

  const termIdx = events.findIndex((e) => TERMINAL.has(e.type));
  if (termIdx >= 0) {
    const contentAfter = events.slice(termIdx + 1).filter((e) => CONTENT.has(e.type));
    assert.equal(
      contentAfter.length,
      0,
      `backend contract: no content events after a terminal, got [${contentAfter.map((e) => e.type).join(', ')}]`,
    );
  }

  for (const e of events) {
    if (e.type.startsWith('message.')) {
      assert.ok(
        e.payload.role === 'user' || e.payload.role === 'assistant',
        `backend contract: message.* role must be user|assistant, got ${JSON.stringify(e.payload.role)}`,
      );
    }
    if (e.type === 'turn.failed') {
      assert.ok(
        typeof e.payload.message === 'string' && (e.payload.message as string).length > 0,
        'backend contract: turn.failed must carry a non-empty message',
      );
    }
  }
}

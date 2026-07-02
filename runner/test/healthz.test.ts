// Protocol-version handshake (HIGH: "No CLI<->runner protocol version
// handshake"). GET /healthz and /sessions/:id/status must both report the
// runner's PROTOCOL_VERSION so a skewed CLI can detect it instead of silently
// misdecoding events. schema/events.json is the source of truth (generated
// into PROTOCOL_VERSION via events.gen.ts); this test guards the two HTTP
// surfaces that read it, not the codegen itself (see
// internal/session/schema_test.go for the codegen drift gate).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { healthzBody } from '../src/server.js';
import { initRegistry, toStatusResponse } from '../src/session.js';
import { PROTOCOL_VERSION } from '../src/types.js';
import type { SessionState } from '../src/types.js';

test('healthzBody reports status ok and the runner protocol version', () => {
  const body = healthzBody();
  assert.equal(body.status, 'ok');
  assert.equal(body.protocolVersion, PROTOCOL_VERSION);
  assert.ok(Number.isInteger(body.protocolVersion) && body.protocolVersion > 0);
});

test('toStatusResponse includes the runner protocol version', () => {
  const state: SessionState = {
    sandbox_session_id: 'test-session',
    backend: 'claude-sdk',
    project_path: '/p',
    status: 'idle',
    claude_session_id: '',
    opencode_session_id: '',
    last_turn_id: '',
    last_activity: new Date().toISOString(),
  };
  const reg = initRegistry(state);
  const resp = toStatusResponse(reg.state, reg.activeTurnId());
  assert.equal(resp.protocolVersion, PROTOCOL_VERSION);
});

// session.json version-stamp contract (the 2026-07-01 review HIGH's second
// half: "session.json has no version field at all"). reviveSessionState is the
// pure load path — the production loadSessionState only adds the /session file
// I/O around it.

import { test, mock } from 'node:test';
import assert from 'node:assert/strict';
import { initRegistry, reviveSessionState, setExternalActivityProbe, STATE_VERSION, type RunnerConfig } from '../src/session.js';
import type { SessionState } from '../src/types.js';

const cfg: RunnerConfig = {
  sessionId: 'env-session',
  backend: 'claude-sdk',
  projectPath: '/proj',
  runnerToken: 't',
};

test('an unversioned (pre-versioning) file loads as v1 and is restamped', () => {
  const loaded = reviveSessionState(
    {
      sandbox_session_id: 's1',
      backend: 'claude-sdk',
      project_path: '/proj',
      status: 'idle',
      claude_session_id: 'c1',
      opencode_session_id: '',
      last_turn_id: 'turn-3',
      last_activity: '2026-07-01T00:00:00Z',
    },
    cfg,
  );
  assert.equal(loaded.state_version, STATE_VERSION);
  assert.equal(loaded.claude_session_id, 'c1');
  assert.equal(loaded.last_turn_id, 'turn-3');
});

test('a fresh/empty parse seeds every field from env at the current version', () => {
  const loaded = reviveSessionState({}, cfg);
  assert.equal(loaded.state_version, STATE_VERSION);
  assert.equal(loaded.sandbox_session_id, 'env-session');
  assert.equal(loaded.backend, 'claude-sdk');
  assert.equal(loaded.status, 'idle');
});

test('a newer-versioned file warns loudly and preserves unknown fields', () => {
  const errors = mock.method(console, 'error', () => {});
  try {
    const parsed = {
      state_version: STATE_VERSION + 1,
      sandbox_session_id: 's1',
      backend: 'claude-sdk',
      project_path: '/proj',
      status: 'busy',
      claude_session_id: 'c1',
      opencode_session_id: '',
      last_turn_id: 'turn-9',
      last_activity: '2026-07-01T00:00:00Z',
      // A field only a newer runner understands: must survive the round-trip.
      future_field: { nested: true },
    } as Partial<SessionState>;

    const loaded = reviveSessionState(parsed, cfg);

    assert.equal(errors.mock.callCount(), 1);
    assert.match(String(errors.mock.calls[0].arguments[0]), /newer than this runner supports/);
    // Restamped to what this runner writes; unknown field carried through.
    assert.equal(loaded.state_version, STATE_VERSION);
    assert.deepEqual((loaded as Record<string, unknown>).future_field, { nested: true });
    // Known-field semantics still apply (stale persisted 'busy' → 'idle').
    assert.equal(loaded.status, 'idle');
  } finally {
    errors.mock.restore();
  }
});

test('a same-version file loads silently', () => {
  const errors = mock.method(console, 'error', () => {});
  try {
    const loaded = reviveSessionState({ state_version: STATE_VERSION }, cfg);
    assert.equal(errors.mock.callCount(), 0);
    assert.equal(loaded.state_version, STATE_VERSION);
  } finally {
    errors.mock.restore();
  }
});

test('idle status treats synthetic busy status as an active turn', () => {
  const reg = initRegistry(
    reviveSessionState(
      {
        sandbox_session_id: 'oc1',
        backend: 'opencode-server',
        project_path: '/proj',
        status: 'idle',
        last_turn_id: 'turn-1',
      },
      { ...cfg, sessionId: 'oc1', backend: 'opencode-server' },
    ),
  );

  assert.ok(reg.idleStatus().idleSince, 'idle session should expose idleSince');

  reg.state.status = 'busy';
  const busy = reg.idleStatus();
  assert.equal(busy.turnActive, true);
  assert.equal(busy.idleSince, undefined);

  reg.state.status = 'idle';
  assert.ok(reg.idleStatus().idleSince, 'idleSince should return after synthetic turn finishes');
});

test('idle status samples the external activity probe synchronously', () => {
  let externalActive = false;
  const reg = initRegistry(reviveSessionState({ sandbox_session_id: 'oc2', backend: 'opencode-server' }, cfg));

  try {
    setExternalActivityProbe(() => externalActive);
    assert.ok(reg.idleStatus().idleSince, 'starts idle without external activity');

    externalActive = true;
    const active = reg.idleStatus();
    assert.equal(active.idleSince, undefined);
    assert.equal(active.turnActive, false);
  } finally {
    setExternalActivityProbe(null);
  }
});

// 5c regression: a detached claude-pane session's synthetic busy rested entirely
// on observer hook/statusline POSTs, and a single long tool call emits none for
// minutes — so the busy went stale, released to idle, and the reaper suspended a
// working session mid-turn. notePaneOutput() feeds an independent "child is
// printing" liveness window (PANE_OUTPUT_ACTIVE_WINDOW_MS) that blocks idle and
// defeats synthetic-busy staleness while the child is visibly working, then
// lapses so a genuinely quiescent pane still becomes reapable.
//
// These drive the registry directly (initRegistry + notePaneOutput's injectable
// timestamp to backdate the clock) and never trigger a status WRITE — status
// stays 'idle' or is seeded 'busy' — so no SQLite/session.json setup is needed.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initRegistry } from '../src/session.js';
import type { SessionState } from '../src/types.js';

function state(overrides: Partial<SessionState> = {}): SessionState {
  return {
    state_version: 1,
    sandbox_session_id: 'sess-pane',
    backend: 'claude-pane',
    project_path: '/session/workspace',
    status: 'idle',
    claude_session_id: '',
    opencode_session_id: '',
    last_turn_id: '',
    last_activity: new Date().toISOString(),
    ...overrides,
  };
}

// A backdate comfortably past the 90s pane-output window.
const PAST_WINDOW_MS = 2 * 60_000;
// A backdate comfortably past the 5min synthetic-busy staleness window.
const STALE_OBSERVER_MS = 6 * 60_000;

test('recent pane output blocks the idle clock, and the window lapses', () => {
  const reg = initRegistry(state());

  // A quiescent, detached, turn-less session is idle.
  assert.ok(reg.idleStatus().idleSince, 'quiescent detached session should be idle');

  // Recent pane output blocks idle even though status is 'idle' (observer hooks
  // may have been lost, but the child is visibly printing).
  reg.notePaneOutput();
  assert.equal(reg.idleStatus().idleSince, undefined, 'recent pane output blocks idle');

  // Backdate the output past the window → the idle clock resumes.
  reg.notePaneOutput(Date.now() - PAST_WINDOW_MS);
  assert.ok(reg.idleStatus().idleSince, 'the pane-output window lapses and idle resumes');
});

test('recent pane output defeats synthetic-busy staleness (no mid-turn release)', () => {
  // Seed 'busy' directly so no setStatus write is needed.
  const reg = initRegistry(state({ status: 'busy' }));

  // The child is actively printing...
  reg.notePaneOutput();
  // ...while the observer stream has been quiet long enough to be "stale".
  reg.noteObserverEvent(Date.now() - STALE_OBSERVER_MS);

  const st = reg.idleStatus();
  assert.equal(st.turnActive, true, 'a printing busy child must not be released as stale');
  assert.equal(st.idleSince, undefined, 'a working session has no idle clock');
});

// [V41] On a session.json reseed (absent or B7 corrupt move-aside) the persisted
// turn counter is lost, but events.db persists across the reseed. loadSessionState
// must seed last_turn_id from the log's highest turn-N so new turns continue
// monotonically instead of reusing turn-1..N ids already in the log (duplicate
// turn_ids break audit/trace/readTurnOutcome correlation).
//
// Exercises the REAL src/session.ts loadSessionState + src/events.ts maxTurnNumber
// against a temp better-sqlite3 DB injected via __setEventLogForTest.
//
// GUARD: SKIPS cleanly when better-sqlite3's native addon is unavailable, UNLESS
// RUNNER_REQUIRE_SQLITE=1 (CI), via the shared sqlite-probe.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';
import type { EventType } from '../src/types.js';
import { appendEvent, __setEventLogForTest } from '../src/events.js';
import { loadSessionState, initRegistry } from '../src/session.js';
import type { RunnerConfig } from '../src/session.js';

const CREATE_SQL = `
  CREATE TABLE IF NOT EXISTS events (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    time       TEXT    NOT NULL,
    session_id TEXT    NOT NULL,
    turn_id    TEXT,
    type       TEXT    NOT NULL,
    payload    TEXT    NOT NULL
  );
  CREATE INDEX IF NOT EXISTS idx_events_session_seq ON events(session_id, seq);
`;

const T = (t: string): EventType => t as EventType;

function setup(): { dir: string; cleanup: () => void } {
  const Db = Database!;
  const dir = mkdtempSync(join(tmpdir(), 'session-reseed-'));
  const db = new Db(join(dir, 'events.db'));
  db.pragma('journal_mode = WAL');
  db.exec(CREATE_SQL);
  __setEventLogForTest(db);
  return {
    dir,
    cleanup(): void {
      __setEventLogForTest(null);
      try {
        db.close();
      } catch {
        /* may already be closed */
      }
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

const cfg: RunnerConfig = {
  sessionId: 'sess-reseed',
  backend: 'claude-sdk',
  projectPath: '/session/workspace',
  runnerToken: 't',
};

test('V41: reseed with an existing log seeds the counter past turn-3 → next id is turn-4', { skip }, () => {
  const { dir, cleanup } = setup();
  try {
    // The log already carries turns 1..3 (out-of-order ids present too, to prove
    // it takes the MAX and not the last-inserted).
    appendEvent(cfg.sessionId, 'turn-1', T('turn.started'), { prompt: 'a' });
    appendEvent(cfg.sessionId, 'turn-3', T('turn.started'), { prompt: 'c' });
    appendEvent(cfg.sessionId, 'turn-2', T('turn.started'), { prompt: 'b' });

    // session.json is absent → loadSessionState reseeds a fresh empty state.
    const { state } = loadSessionState(cfg, join(dir, 'session.json'));
    assert.equal(state.last_turn_id, 'turn-3', 'counter seeded from the log max');

    // nextTurnId continues monotonically instead of colliding at turn-1.
    const reg = initRegistry(state);
    assert.equal(reg.nextTurnId(), 'turn-4');
  } finally {
    cleanup();
  }
});

test('V41: reseed with an empty log leaves the counter at the start (turn-1)', { skip }, () => {
  const { dir, cleanup } = setup();
  try {
    const { state } = loadSessionState(cfg, join(dir, 'session.json'));
    assert.equal(state.last_turn_id, '', 'no log rows → no seed');
    assert.equal(initRegistry(state).nextTurnId(), 'turn-1');
  } finally {
    cleanup();
  }
});

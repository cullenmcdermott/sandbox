// [V42] registerTurn does activeTurns.set THEN setStatus('busy'), which persists
// session.json — an unguarded write that can throw (ENOSPC/EROFS on the PVC)
// AFTER the single turn slot is reserved but BEFORE agent.runTurn fires. Nothing
// else deregisters that entry, so the slot wedges: every later POST /turns 409s
// and the idle reaper can never suspend the pod until restart. startTurn now
// frees the slot (finishTurn) and re-throws when registration fails.
//
// GUARD: SKIPS cleanly when better-sqlite3's native addon is unavailable, UNLESS
// RUNNER_REQUIRE_SQLITE=1 (CI) — finishTurn → setStatus persists a
// session.status_changed via the real event log.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';
import { __setEventLogForTest } from '../src/events.js';
import {
  initRegistry,
  __setSessionJsonPathForTest,
  type RunnerConfig,
} from '../src/session.js';
import { startTurn, setTurnSettledHandler } from '../src/turns.js';
import type { SessionState } from '../src/types.js';
import type { Agent } from '../src/agent.js';

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

const cfg: RunnerConfig = {
  sessionId: 'sess-v42',
  backend: 'claude-sdk',
  projectPath: '/work/proj',
  runnerToken: 't',
};

function fakeState(): SessionState {
  return {
    state_version: 1,
    sandbox_session_id: cfg.sessionId,
    backend: cfg.backend,
    project_path: cfg.projectPath,
    status: 'idle',
    claude_session_id: '',
    opencode_session_id: '',
    last_turn_id: '',
    last_activity: new Date().toISOString(),
  };
}

test('V42: a throw during registerTurn frees the turn slot (not wedged forever)', { skip }, () => {
  const Db = Database!;
  const dir = mkdtempSync(join(tmpdir(), 'start-turn-guard-'));
  const db = new Db(join(dir, 'events.db'));
  db.pragma('journal_mode = WAL');
  db.exec(CREATE_SQL);
  __setEventLogForTest(db);
  __setSessionJsonPathForTest(join(dir, 'session.json'));
  setTurnSettledHandler(null);

  // runTurn must never fire when registration throws before it.
  let ranTurn = 0;
  const fakeAgent: Agent = {
    runTurn: () => {
      ranTurn++;
      return Promise.resolve();
    },
  };

  try {
    const reg = initRegistry(fakeState());

    // Simulate the real partial failure: registerTurn sets the activeTurns entry
    // and flips status to 'busy' (in memory), then the session.json persist
    // throws — exactly the ENOSPC-mid-setStatus window.
    const orig = reg.registerTurn.bind(reg);
    (reg as { registerTurn: (id: string, p: string) => unknown }).registerTurn = (id: string, p: string) => {
      reg.activeTurns.set(id, { turnId: id, abort: new AbortController(), prompt: p });
      reg.state.status = 'busy';
      throw new Error('ENOSPC: simulated PVC-full during session.json persist');
    };

    assert.throws(() => startTurn(cfg, fakeAgent, 'do it'), /ENOSPC/);
    assert.equal(reg.activeTurns.size, 0, 'the reserved slot was freed, not wedged');
    assert.equal(ranTurn, 0, 'runTurn never fired for the failed registration');

    // Restore the real registerTurn: the next start must be accepted (slot free),
    // proving the session is not permanently 409-wedged.
    (reg as { registerTurn: unknown }).registerTurn = orig;
    const res = startTurn(cfg, fakeAgent, 'try again');
    assert.ok('turnId' in res, `expected a fired turn, got ${JSON.stringify(res)}`);
    assert.equal(ranTurn, 1, 'the recovery turn fired its runTurn');
  } finally {
    setTurnSettledHandler(null);
    __setEventLogForTest(null);
    __setSessionJsonPathForTest(null);
    try {
      db.close();
    } catch {
      /* may already be closed */
    }
    rmSync(dir, { recursive: true, force: true });
  }
});

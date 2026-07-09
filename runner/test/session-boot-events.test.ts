// Regression for D2: after a mid-turn pod crash (SIGKILL/OOM, or a best-effort
// SIGTERM that never flipped the status back), session.json holds a stale 'busy'
// status. loadSessionState coerces it to 'idle', but the SQLite event log still
// ends with the orphaned turn's events (turn.started, tool.started, deltas, …)
// and NO terminal event — so a client replaying with after=0 drives its
// read-model to "busy" forever. The boot sequence must append a terminal
// turn.interrupted + session.status_changed{idle} BEFORE the boot session.started.
//
// orphanedTurnBootEvents is the pure decision function (no sqlite needed); the
// last test drives its output into a REAL better-sqlite3 temp DB (guarded via
// sqlite-probe) to prove the append order survives AUTOINCREMENT seq assignment,
// matching events.test.ts's approach (events.ts hard-codes an off-pod DB path).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { orphanedTurnBootEvents } from '../src/session.js';
import type { SessionState } from '../src/types.js';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';

function state(overrides: Partial<SessionState> = {}): SessionState {
  return {
    state_version: 1,
    sandbox_session_id: 'sess-A',
    backend: 'claude-sdk',
    project_path: '/session/workspace',
    status: 'idle',
    claude_session_id: '',
    opencode_session_id: '',
    last_turn_id: '',
    last_activity: new Date().toISOString(),
    ...overrides,
  };
}

test('busy-with-orphaned-turn boot: interrupted then idle, in that order (D2)', () => {
  const events = orphanedTurnBootEvents('busy', state({ last_turn_id: 'turn-3' }));
  assert.deepEqual(events, [
    { turnId: 'turn-3', type: 'turn.interrupted', payload: { reason: 'runner restart' } },
    { type: 'session.status_changed', payload: { status: 'idle' } },
  ]);
});

test('busy-with-no-turn-id boot: only status_changed{idle}, no garbage-id interrupt', () => {
  const events = orphanedTurnBootEvents('busy', state({ last_turn_id: '' }));
  assert.deepEqual(events, [{ type: 'session.status_changed', payload: { status: 'idle' } }]);
});

test('idle boot appends nothing extra', () => {
  assert.deepEqual(orphanedTurnBootEvents('idle', state({ last_turn_id: 'turn-3' })), []);
});

test('error boot appends nothing extra (a failure must not be masked)', () => {
  assert.deepEqual(orphanedTurnBootEvents('error', state({ last_turn_id: 'turn-3' })), []);
});

test('missing/undefined status appends nothing extra', () => {
  assert.deepEqual(orphanedTurnBootEvents(undefined, state({ last_turn_id: 'turn-3' })), []);
});

// The exact schema + statements src/events.ts uses (mirrors events.test.ts).
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
const INSERT_SQL =
  'INSERT INTO events (time, session_id, turn_id, type, payload) VALUES (?, ?, ?, ?, ?)';
const SELECT_ALL_SQL =
  'SELECT seq, turn_id, type, payload FROM events WHERE session_id = ? ORDER BY seq ASC';

test('boot append order: orphaned-turn terminal precedes session.started in the log', { skip }, () => {
  const Db = Database!;
  const dir = mkdtempSync(join(tmpdir(), 'boot-events-test-'));
  const dbPath = join(dir, 'events.db');
  const db = new Db(dbPath);
  try {
    db.pragma('journal_mode = WAL');
    db.exec(CREATE_SQL);
    const insert = db.prepare(INSERT_SQL);

    const sid = 'sess-A';
    // Pre-crash tail: an orphaned turn with no terminal event.
    insert.run(new Date().toISOString(), sid, 'turn-3', 'turn.started', JSON.stringify({ prompt: 'hi' }));
    insert.run(new Date().toISOString(), sid, 'turn-3', 'tool.started', JSON.stringify({ tool: 'Bash' }));

    // Boot sequence (index.ts): append the D2 boot events, THEN session.started.
    const bootEvents = orphanedTurnBootEvents('busy', state({ last_turn_id: 'turn-3' }));
    for (const ev of bootEvents) {
      insert.run(new Date().toISOString(), sid, ev.turnId ?? null, ev.type, JSON.stringify(ev.payload));
    }
    insert.run(new Date().toISOString(), sid, null, 'session.started', JSON.stringify({ model: '', cwd: '' }));

    const rows = db.prepare(SELECT_ALL_SQL).all(sid) as Array<{
      seq: number;
      turn_id: string | null;
      type: string;
      payload: string;
    }>;

    // Strictly increasing seq (the SSE replay contract).
    for (let i = 1; i < rows.length; i++) assert.ok(rows[i].seq > rows[i - 1].seq);

    assert.deepEqual(
      rows.map((r) => r.type),
      ['turn.started', 'tool.started', 'turn.interrupted', 'session.status_changed', 'session.started'],
    );

    const interrupted = rows.find((r) => r.type === 'turn.interrupted')!;
    const statusChanged = rows.find((r) => r.type === 'session.status_changed')!;
    const started = rows.find((r) => r.type === 'session.started')!;

    // The terminal for the orphaned turn carries its id and lands before idle,
    // and both land before session.started.
    assert.equal(interrupted.turn_id, 'turn-3');
    assert.deepEqual(JSON.parse(interrupted.payload), { reason: 'runner restart' });
    assert.deepEqual(JSON.parse(statusChanged.payload), { status: 'idle' });
    assert.ok(interrupted.seq < statusChanged.seq);
    assert.ok(statusChanged.seq < started.seq);
  } finally {
    db.close();
    rmSync(dir, { recursive: true, force: true });
  }
});

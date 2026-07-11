// E9: appendEvent reuses a prepared INSERT statement (better-sqlite3 does not
// cache prepared statements, so re-`prepare()`ing per event re-parses the SQL on
// the per-delta hot path). The cache is keyed to the OPEN database instance, so a
// reopen must transparently re-prepare against the new handle — never keep writing
// through a statement bound to the previous (closed/replaced) db.
//
// Exercises the REAL src/events.ts appendEvent against temp better-sqlite3 DBs
// injected via __setEventLogForTest. GUARD: SKIPS cleanly without the native addon
// unless RUNNER_REQUIRE_SQLITE=1 (CI), via the shared sqlite-probe.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';
import type { EventType } from '../src/types.js';
import { appendEvent, __setEventLogForTest } from '../src/events.js';

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

test('E9: the cached INSERT rebinds to a reopened db (writes land in the new handle)', { skip }, () => {
  const Db = Database!;
  const dir = mkdtempSync(join(tmpdir(), 'events-stmt-cache-'));
  const db1 = new Db(join(dir, 'a.db'));
  const db2 = new Db(join(dir, 'b.db'));
  try {
    db1.pragma('journal_mode = WAL');
    db2.pragma('journal_mode = WAL');
    db1.exec(CREATE_SQL);
    db2.exec(CREATE_SQL);

    // First open: two appends caching + reusing the INSERT against db1.
    __setEventLogForTest(db1);
    const e1 = appendEvent('sess-A', 'turn-1', T('message.completed'), { content: 'one' });
    const e2 = appendEvent('sess-A', 'turn-1', T('message.completed'), { content: 'two' });
    assert.equal(e1.seq, 1);
    assert.equal(e2.seq, 2, 'monotonic seq under the reused statement');

    // Reopen against a FRESH db. If the cached statement stayed bound to db1, this
    // write would go to db1 (seq 3) and db2 would stay empty — the bug E9 guards.
    __setEventLogForTest(db2);
    const e3 = appendEvent('sess-A', 'turn-1', T('message.completed'), { content: 'three' });
    assert.equal(e3.seq, 1, 'seq restarts → the append hit the fresh db, not the old one');

    const inDb2 = db2.prepare('SELECT COUNT(*) AS n FROM events').get() as { n: number };
    assert.equal(inDb2.n, 1, 'the third append landed in db2');
    const inDb1 = db1.prepare('SELECT COUNT(*) AS n FROM events').get() as { n: number };
    assert.equal(inDb1.n, 2, 'db1 was untouched after the reopen');
  } finally {
    __setEventLogForTest(null);
    db1.close();
    db2.close();
    rmSync(dir, { recursive: true, force: true });
  }
});

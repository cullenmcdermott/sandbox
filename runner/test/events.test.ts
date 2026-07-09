// Integration test for the event-log invariants the SSE layer relies on:
//   1. append-before-stream / monotonic seq: each appended event gets a strictly
//      increasing AUTOINCREMENT seq;
//   2. readEventsAfter(after) ordering: events are returned in ascending seq
//      order, and only those with seq > after (the SSE replay contract).
//
// This exercises a REAL better-sqlite3 temp DB so it validates the actual SQLite
// engine behavior (AUTOINCREMENT monotonicity, ORDER BY seq). It is GUARDED via
// the shared native-addon probe (./sqlite-probe): when better-sqlite3's compiled
// addon is unavailable (a bare `npm install --ignore-scripts` with no rebuild)
// the suite SKIPS cleanly — UNLESS RUNNER_REQUIRE_SQLITE=1, in which case the
// probe throws so the suite fails loudly. CI sets RUNNER_REQUIRE_SQLITE=1 after
// `npm rebuild better-sqlite3` so these durability invariants can never silently
// self-skip.
//
// It replicates src/events.ts's exact schema and the INSERT / SELECT / MAX(seq)
// statements rather than importing events.ts directly, because events.ts hard-
// codes an absolute DB path under /session (unwritable off-pod); the SQL
// contract under test is identical.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';

// The exact schema + statements src/events.ts uses.
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
const SELECT_AFTER_SQL =
  'SELECT seq, time, session_id, turn_id, type, payload FROM events WHERE session_id = ? AND seq > ? ORDER BY seq ASC';
const MAX_SEQ_SQL = 'SELECT MAX(seq) AS maxSeq FROM events WHERE session_id = ?';

test('events log invariants: monotonic seq + readEventsAfter ordering', { skip }, () => {
  // Database is non-null here (the suite is skipped otherwise).
  const Db = Database!;
  const dir = mkdtempSync(join(tmpdir(), 'events-test-'));
  const dbPath = join(dir, 'events.db');
  const db = new Db(dbPath);
  try {
    db.pragma('journal_mode = WAL');
    db.exec(CREATE_SQL);

    const insert = db.prepare(INSERT_SQL);
    const append = (sessionId: string, type: string, payload: object): number => {
      const info = insert.run(new Date().toISOString(), sessionId, 'turn-1', type, JSON.stringify(payload));
      return Number(info.lastInsertRowid);
    };

    const sid = 'sess-A';
    const seqs = [
      append(sid, 'session.started', { model: 'opus-4.8' }),
      append(sid, 'turn.started', { prompt: 'hi' }),
      append(sid, 'message.completed', { content: 'hello' }),
      append(sid, 'turn.completed', {}),
    ];

    // INVARIANT 1: seqs are strictly monotonically increasing (the SSE replay
    // contract depends on a total order with no gaps-in-ordering or reuse).
    for (let i = 1; i < seqs.length; i++) {
      assert.ok(seqs[i] > seqs[i - 1], `seq must increase: ${seqs[i - 1]} -> ${seqs[i]}`);
    }

    // lastSeq == the max appended seq.
    const maxRow = db.prepare(MAX_SEQ_SQL).get(sid) as { maxSeq: number | null };
    assert.equal(maxRow.maxSeq, seqs[seqs.length - 1]);

    const readAfter = (after: number): Array<{ seq: number; type: string }> =>
      (db.prepare(SELECT_AFTER_SQL).all(sid, after) as Array<{ seq: number; type: string }>).map((r) => ({
        seq: r.seq,
        type: r.type,
      }));

    // INVARIANT 2a: readEventsAfter(0) returns ALL events in ascending seq order.
    const all = readAfter(0);
    assert.deepEqual(
      all.map((e) => e.type),
      ['session.started', 'turn.started', 'message.completed', 'turn.completed'],
    );
    for (let i = 1; i < all.length; i++) {
      assert.ok(all[i].seq > all[i - 1].seq, 'readEventsAfter must return ascending seq');
    }

    // INVARIANT 2b: readEventsAfter(seq) returns ONLY events strictly after seq
    // (the after=<seq> replay must not re-deliver what the client already saw).
    const afterSecond = readAfter(seqs[1]);
    assert.deepEqual(
      afterSecond.map((e) => e.type),
      ['message.completed', 'turn.completed'],
    );

    // INVARIANT 2c: after the highest seq there is nothing left to replay.
    assert.deepEqual(readAfter(seqs[seqs.length - 1]), []);

    // INVARIANT: a second session's events are isolated by session_id.
    append('sess-B', 'session.started', {});
    assert.equal(readAfter(0).length, 4, 'other-session events must not leak into sess-A');
  } finally {
    db.close();
    rmSync(dir, { recursive: true, force: true });
  }
});

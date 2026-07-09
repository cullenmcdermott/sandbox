// E4: delta-only compaction on turn.completed. Every `*.delta` event
// (message.delta / reasoning.delta / tool.delta — high-volume, worthless once the
// turn's completed events exist) is deleted for turns older than the last N when
// a turn completes. The completed/full events are never touched, so an after=0
// replay still reconstructs the transcript (with seq gaps, which the after=<seq>
// contract tolerates). Distinct from M34's rejected all-or-nothing retention.
//
// Exercises the REAL src/events.ts appendEvent against a temp better-sqlite3 DB.
// GUARD via the shared sqlite-probe (SKIPS unless RUNNER_REQUIRE_SQLITE=1).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';
import type { ServerResponse } from 'node:http';
import type { EventType } from '../src/types.js';
import {
  appendEvent,
  attachSseClient,
  __setEventLogForTest,
  deltaCompactKeepTurns,
} from '../src/events.js';

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

class FakeRes extends EventEmitter {
  chunks: string[] = [];
  writableEnded = false;
  destroyed = false;
  writableLength = 0;
  backpressure = false;
  statusCode = 0;
  headers: Record<string, string> = {};
  writeHead(status: number, headers?: Record<string, string>): this {
    this.statusCode = status;
    if (headers) this.headers = headers;
    return this;
  }
  write(data: string): boolean {
    if (this.writableEnded || this.destroyed) return false;
    this.chunks.push(data);
    return !this.backpressure;
  }
  end(): this {
    if (!this.writableEnded && !this.destroyed) {
      this.writableEnded = true;
      this.emit('close');
    }
    return this;
  }
  destroy(): this {
    if (!this.destroyed) {
      this.destroyed = true;
      this.emit('close');
    }
    return this;
  }
  wire(): string {
    return this.chunks.join('');
  }
  events(): Array<Record<string, unknown>> {
    return this.wire()
      .split('\n\n')
      .filter((f) => f.startsWith('data: '))
      .map((f) => JSON.parse(f.slice('data: '.length)) as Record<string, unknown>);
  }
  hasReplayComplete(): boolean {
    return this.wire().includes(': replay-complete');
  }
}

function asRes(r: FakeRes): ServerResponse {
  return r as unknown as ServerResponse;
}

function setupDb(): { db: import('better-sqlite3').Database; cleanup: () => void } {
  const Db = Database!;
  const dir = mkdtempSync(join(tmpdir(), 'events-compact-'));
  const db = new Db(join(dir, 'events.db'));
  db.pragma('journal_mode = WAL');
  db.exec(CREATE_SQL);
  __setEventLogForTest(db);
  return {
    db,
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

async function waitFor(pred: () => boolean, timeoutMs = 2000): Promise<void> {
  const start = Date.now();
  while (!pred()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor: timed out');
    await new Promise((r) => setImmediate(r));
  }
}

const T = (t: string): EventType => t as EventType;

/** Append a full turn: turn.started, two deltas, message.completed, turn.completed. */
function runTurn(sid: string, n: number): void {
  const tid = `turn-${n}`;
  appendEvent(sid, tid, T('turn.started'), { n });
  appendEvent(sid, tid, T('message.delta'), { n, chunk: 'a' });
  appendEvent(sid, tid, T('message.delta'), { n, chunk: 'b' });
  appendEvent(sid, tid, T('message.completed'), { n, text: 'ab' });
  appendEvent(sid, tid, T('turn.completed'), { n });
}

function deltaTurnsPresent(db: import('better-sqlite3').Database, sid: string): number[] {
  const rows = db
    .prepare("SELECT turn_id FROM events WHERE session_id = ? AND type LIKE '%.delta' ORDER BY seq")
    .all(sid) as Array<{ turn_id: string }>;
  return [...new Set(rows.map((r) => Number(r.turn_id.split('-')[1])))];
}

test('E4 config: DELTA_COMPACT_KEEP_TURNS parses a valid override, and 0/negative/NaN/unset fall back to the default 2', () => {
  const saved = process.env.DELTA_COMPACT_KEEP_TURNS;
  try {
    delete process.env.DELTA_COMPACT_KEEP_TURNS;
    assert.equal(deltaCompactKeepTurns(), 2, 'unset → default 2');
    process.env.DELTA_COMPACT_KEEP_TURNS = '5';
    assert.equal(deltaCompactKeepTurns(), 5, 'valid override respected');
    process.env.DELTA_COMPACT_KEEP_TURNS = '1';
    assert.equal(deltaCompactKeepTurns(), 1, 'N=1 is valid');
    process.env.DELTA_COMPACT_KEEP_TURNS = '0';
    assert.equal(deltaCompactKeepTurns(), 2, '0 → default (never disables to delete-all)');
    process.env.DELTA_COMPACT_KEEP_TURNS = '-3';
    assert.equal(deltaCompactKeepTurns(), 2, 'negative → default');
    process.env.DELTA_COMPACT_KEEP_TURNS = 'off';
    assert.equal(deltaCompactKeepTurns(), 2, 'non-numeric → default');
  } finally {
    if (saved === undefined) delete process.env.DELTA_COMPACT_KEEP_TURNS;
    else process.env.DELTA_COMPACT_KEEP_TURNS = saved;
  }
});

test('E4 compaction (default N=2): after 4 turns, deltas of turns 1-2 are gone, 3-4 remain, all non-delta events survive, replay is in seq order with gaps', { skip }, async () => {
  delete process.env.DELTA_COMPACT_KEEP_TURNS;
  const { db, cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    for (let n = 1; n <= 4; n++) runTurn(sid, n);

    // Only turns 3 and 4 keep their deltas (current + previous).
    assert.deepEqual(deltaTurnsPresent(db, sid), [3, 4], 'deltas of turns 1-2 compacted away');

    // Every non-delta event survives (5 turns worth of turn.started + completed +
    // message.completed = 3 per turn * 4 turns = 12).
    const nonDelta = db
      .prepare("SELECT COUNT(*) AS c FROM events WHERE session_id = ? AND type NOT LIKE '%.delta'")
      .get(sid) as { c: number };
    assert.equal(nonDelta.c, 12, 'no completed/full event ever deleted');

    // A full after=0 replay returns the survivors in strict ascending seq order,
    // with gaps where turn 1-2 deltas were deleted (the after=<seq> contract
    // tolerates gaps).
    const res = new FakeRes();
    const disconnect = attachSseClient(asRes(res), sid, 0);
    try {
      await waitFor(() => res.hasReplayComplete());
      const seqs = res.events().map((e) => e.seq as number);
      const sorted = [...seqs].sort((a, b) => a - b);
      assert.deepEqual(seqs, sorted, 'replay is in ascending seq order');
      assert.ok(
        seqs.some((s, i) => i > 0 && s - seqs[i - 1] > 1),
        'replay has at least one seq gap from deleted deltas',
      );
      // Deltas present in the replay belong only to turns 3 and 4.
      const deltaEvts = res
        .events()
        .filter((e) => String(e.type).endsWith('.delta'))
        .map((e) => (e.payload as { n: number }).n);
      assert.deepEqual([...new Set(deltaEvts)].sort(), [3, 4]);
    } finally {
      disconnect();
    }
  } finally {
    cleanup();
  }
});

test('E4 compaction: DELTA_COMPACT_KEEP_TURNS override (N=1) keeps only the newest turn deltas', { skip }, () => {
  const saved = process.env.DELTA_COMPACT_KEEP_TURNS;
  process.env.DELTA_COMPACT_KEEP_TURNS = '1';
  const { db, cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    for (let n = 1; n <= 3; n++) runTurn(sid, n);
    // N=1 → only the newest turn's deltas remain.
    assert.deepEqual(deltaTurnsPresent(db, sid), [3], 'only newest turn deltas kept under N=1');
    // Non-delta events untouched (3 per turn * 3 turns).
    const nonDelta = db
      .prepare("SELECT COUNT(*) AS c FROM events WHERE session_id = ? AND type NOT LIKE '%.delta'")
      .get(sid) as { c: number };
    assert.equal(nonDelta.c, 9);
  } finally {
    cleanup();
    if (saved === undefined) delete process.env.DELTA_COMPACT_KEEP_TURNS;
    else process.env.DELTA_COMPACT_KEEP_TURNS = saved;
  }
});

test('E4 compaction: a compaction that throws (closed DB) is caught and never throws out of appendEvent', { skip }, () => {
  const { db, cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    runTurn(sid, 1);
    // Close the DB so the next appendEvent's INSERT (R11-caught) AND the
    // turn.completed compaction both throw against a closed handle. appendEvent
    // must swallow both and still return an event (seq 0).
    db.close();
    let evt;
    assert.doesNotThrow(() => {
      evt = appendEvent(sid, 'turn-2', T('turn.completed'), { n: 2 });
    }, 'compaction failure must not propagate out of appendEvent');
    assert.equal(evt!.seq, 0, 'persist failed → seq 0, append still returns');
  } finally {
    cleanup();
  }
});

// E2/E3: SSE replay + live fan-out behavior of src/events.ts.
//
// These exercise the REAL events.ts functions (appendEvent, attachSseClient,
// broadcast via appendEvent) against a temp better-sqlite3 DB injected through
// __setEventLogForTest — the production EVENTS_DB_PATH is hard-coded under
// /session (unwritable off-pod). SSE clients are driven by a FakeRes that
// captures every written frame and lets a test control write()'s backpressure
// return and the reported writableLength.
//
// GUARD: like events.test.ts, this suite SKIPS cleanly when better-sqlite3's
// native addon is unavailable, UNLESS RUNNER_REQUIRE_SQLITE=1 (CI), in which
// case sqlite-probe throws at import so it can never silently self-skip.

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
  sseTotalClientCount,
  REPLAY_CHUNK_ROWS,
  MAX_SSE_CLIENT_BUFFER_BYTES,
} from '../src/events.js';

// The exact schema src/events.ts creates (openEventLog can't run off-pod).
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

/** A minimal ServerResponse stand-in that records every frame written to it and
 * lets a test simulate backpressure (write() → false) and a wedged send buffer
 * (writableLength). Extends EventEmitter so on/once/off/emit + close/error/drain
 * events work exactly as the real socket path expects. */
class FakeRes extends EventEmitter {
  statusCode = 0;
  headers: Record<string, string> = {};
  chunks: string[] = [];
  writableEnded = false;
  destroyed = false;
  /** Simulated bytes buffered in the send queue (E3 reads this). */
  writableLength = 0;
  /** When true, write() returns false to model a full socket buffer. */
  backpressure = false;

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

  // --- test helpers -------------------------------------------------------

  /** The raw joined byte stream, as a client would receive it. */
  wire(): string {
    return this.chunks.join('');
  }

  /** Parsed JSON of every `data:` frame, in order. */
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

/** Open a temp DB with the real schema and inject it into the events module. */
function setupDb(): { db: import('better-sqlite3').Database; cleanup: () => void } {
  const Db = Database!;
  const dir = mkdtempSync(join(tmpdir(), 'events-replay-'));
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
        /* may already be closed by a persist-failure test */
      }
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

/** Spin the event loop until pred() holds (replay is async: chunks + yields). */
async function waitFor(pred: () => boolean, timeoutMs = 2000): Promise<void> {
  const start = Date.now();
  while (!pred()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor: timed out');
    await new Promise((r) => setImmediate(r));
  }
}

const T = (t: string): EventType => t as EventType;

test('E2 replay: after=0 delivers every event in seq order, then : replay-complete, frames are valid JSON matching payloads', { skip }, async () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    const payloads = Array.from({ length: 5 }, (_, i) => ({ i, msg: `m${i}` }));
    for (const p of payloads) appendEvent(sid, 'turn-1', T('message.completed'), p);

    const res = new FakeRes();
    const disconnect = attachSseClient(asRes(res), sid, 0);
    try {
      await waitFor(() => res.hasReplayComplete());
      const evs = res.events();
      assert.equal(evs.length, 5, 'all appended events replayed');
      for (let i = 0; i < 5; i++) {
        assert.equal(evs[i].seq, i + 1, 'seq is monotonic from 1');
        assert.deepEqual(evs[i].payload, payloads[i], 'payload round-trips');
        assert.equal(evs[i].sessionId, sid);
        assert.equal(evs[i].turnId, 'turn-1');
      }
      // : replay-complete lands AFTER the last data frame.
      const wire = res.wire();
      assert.ok(
        wire.indexOf(': replay-complete') > wire.lastIndexOf('data: '),
        'replay-complete must follow all history',
      );
    } finally {
      disconnect();
    }
  } finally {
    cleanup();
  }
});

test('E2 replay: a multi-chunk history (> 2 * REPLAY_CHUNK_ROWS) is delivered gap-free, in order, once', { skip }, async () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-big';
    const n = REPLAY_CHUNK_ROWS * 2 + 3; // forces multiple chunks + yields
    for (let i = 0; i < n; i++) appendEvent(sid, 'turn-1', T('message.delta'), { i });

    const res = new FakeRes();
    const disconnect = attachSseClient(asRes(res), sid, 0);
    try {
      await waitFor(() => res.hasReplayComplete());
      const seqs = res.events().map((e) => e.seq as number);
      assert.equal(seqs.length, n, 'no rows dropped across chunk boundaries');
      assert.equal(new Set(seqs).size, n, 'no duplicates across chunks');
      for (let i = 0; i < n; i++) assert.equal(seqs[i], i + 1, 'strict ascending, gap-free');
      // Exactly one replay-complete boundary.
      assert.equal(res.wire().split(': replay-complete').length - 1, 1);
    } finally {
      disconnect();
    }
  } finally {
    cleanup();
  }
});

test('E2 boundary: events appended AFTER the replay handoff are delivered exactly once, in order, after : replay-complete', { skip }, async () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    appendEvent(sid, 'turn-1', T('session.started'), { a: 1 });
    appendEvent(sid, 'turn-1', T('turn.started'), { b: 2 });

    const res = new FakeRes();
    const disconnect = attachSseClient(asRes(res), sid, 0);
    try {
      await waitFor(() => res.hasReplayComplete());
      assert.equal(res.events().length, 2, 'only history so far');

      // Live events after handoff arrive via broadcast, not replay.
      appendEvent(sid, 'turn-1', T('message.completed'), { c: 3 });
      appendEvent(sid, 'turn-1', T('turn.completed'), { d: 4 });
      await waitFor(() => res.events().length === 4);

      const seqs = res.events().map((e) => e.seq as number);
      assert.deepEqual(seqs, [1, 2, 3, 4], 'in order, no gap');
      assert.equal(new Set(seqs).size, 4, 'exactly once, no duplicate');

      const wire = res.wire();
      const rc = wire.indexOf(': replay-complete');
      assert.ok(wire.indexOf('"seq":3') > rc, 'live event 3 comes after the boundary');
      assert.ok(wire.indexOf('"seq":4') > rc, 'live event 4 comes after the boundary');
    } finally {
      disconnect();
    }
  } finally {
    cleanup();
  }
});

test('E3 backpressure: a client buffered past the cap is destroyed on the next broadcast and removed; other clients still receive the frame', { skip }, async () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    const wedged = new FakeRes();
    const healthy = new FakeRes();
    const dW = attachSseClient(asRes(wedged), sid, 0);
    const dH = attachSseClient(asRes(healthy), sid, 0);
    try {
      await waitFor(() => wedged.hasReplayComplete() && healthy.hasReplayComplete());
      assert.equal(sseTotalClientCount(), 2);

      // Model a wedged reader: its send buffer sits above the cap.
      wedged.writableLength = MAX_SSE_CLIENT_BUFFER_BYTES + 1;
      const healthyBefore = healthy.events().length;

      appendEvent(sid, 'turn-1', T('message.completed'), { x: 1 }); // synchronous broadcast

      assert.equal(wedged.destroyed, true, 'wedged client hard-closed');
      assert.equal(sseTotalClientCount(), 1, 'wedged client removed from the set');
      assert.equal(healthy.events().length, healthyBefore + 1, 'healthy client still delivered');
    } finally {
      dW();
      dH();
    }
  } finally {
    cleanup();
  }
});

test('E2 raw-payload splice: tricky JSON payloads (nested/unicode/quotes/escapes) round-trip byte-exactly', { skip }, async () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    const tricky = {
      nested: { arr: [1, 2, { b: 'c' }], quote: 'he said "hi"' },
      unicode: 'café ☕ 日本語',
      control: 'a\u0001b\u0000c',
      backslash: 'C:\\path\\to',
      newline: 'line1\nline2\r\n',
      emptyObj: {},
      emptyArr: [] as unknown[],
      nulls: { n: null },
    };
    appendEvent(sid, 'turn-x', T('tool.completed'), tricky);

    const res = new FakeRes();
    const disconnect = attachSseClient(asRes(res), sid, 0);
    try {
      await waitFor(() => res.hasReplayComplete());
      const evs = res.events();
      assert.equal(evs.length, 1);
      assert.deepEqual(evs[0].payload, tricky, 'payload parses back identically');
      assert.equal(evs[0].turnId, 'turn-x');
      // Byte-exact splice: the frame's payload segment equals JSON.stringify(payload).
      const frame = res.chunks.find((c) => c.startsWith('data: '))!;
      assert.ok(
        frame.includes(`"payload":${JSON.stringify(tricky)}`),
        'raw payload column is spliced verbatim, not re-serialized',
      );
      // Single-line framing: no literal newline inside the data: frame.
      assert.equal(frame.slice(0, -2).includes('\n'), false, 'frame stays one line');
    } finally {
      disconnect();
    }
  } finally {
    cleanup();
  }
});

test('E2 replay: a NULL turn_id event omits turnId in the frame (matches live serialization)', { skip }, async () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    appendEvent(sid, undefined, T('workspace.status'), { clean: true });

    const res = new FakeRes();
    const disconnect = attachSseClient(asRes(res), sid, 0);
    try {
      await waitFor(() => res.hasReplayComplete());
      const evs = res.events();
      assert.equal(evs.length, 1);
      assert.equal('turnId' in evs[0], false, 'turnId omitted when NULL');
      const frame = res.chunks.find((c) => c.startsWith('data: '))!;
      assert.equal(frame.includes('turnId'), false);
    } finally {
      disconnect();
    }
  } finally {
    cleanup();
  }
});

test('E2 replay: after=<mid> replays only seq > after; then live tail continues in order', { skip }, async () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    for (let i = 0; i < 4; i++) appendEvent(sid, 'turn-1', T('message.delta'), { i }); // seq 1..4

    const res = new FakeRes();
    const disconnect = attachSseClient(asRes(res), sid, 2); // after=2 → expect 3,4
    try {
      await waitFor(() => res.hasReplayComplete());
      assert.deepEqual(res.events().map((e) => e.seq), [3, 4], 'only seq > after replayed');

      appendEvent(sid, 'turn-1', T('turn.completed'), {}); // seq 5, live
      await waitFor(() => res.events().length === 3);
      assert.deepEqual(res.events().map((e) => e.seq), [3, 4, 5], 'live tail continues');
    } finally {
      disconnect();
    }
  } finally {
    cleanup();
  }
});

test('E2 disconnect mid-replay: closing the socket before handoff stops the loop and never writes : replay-complete', { skip }, async () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    const n = REPLAY_CHUNK_ROWS * 3; // several chunks, so a disconnect can land mid-replay
    for (let i = 0; i < n; i++) appendEvent(sid, 'turn-1', T('message.delta'), { i });

    const res = new FakeRes();
    const disconnect = attachSseClient(asRes(res), sid, 0);
    // Tear down immediately, before the async replay can drain the whole log.
    disconnect();

    // Let any queued replay ticks run; they must observe the cancellation.
    await new Promise((r) => setTimeout(r, 50));
    assert.equal(res.hasReplayComplete(), false, 'aborted replay never reaches the boundary');
    assert.ok(res.events().length < n, 'replay stopped early');
    assert.equal(sseTotalClientCount(), 0, 'client removed from the set');
  } finally {
    cleanup();
  }
});

test('E2/B4 seq-0 bypass: a persist-failure event still broadcasts live to a caught-up client', { skip }, async () => {
  const { db, cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    appendEvent(sid, 'turn-1', T('session.started'), { a: 1 });

    const res = new FakeRes();
    const disconnect = attachSseClient(asRes(res), sid, 0);
    try {
      await waitFor(() => res.hasReplayComplete());
      assert.equal(res.events().length, 1);

      // Force a persist failure: close the DB so the next INSERT throws. appendEvent
      // (R11) catches it, keeps seq=0, and STILL broadcasts. shouldDeliver's seq-0
      // bypass must deliver it live to the (non-replaying) client.
      db.close();
      const evt = appendEvent(sid, 'turn-1', T('turn.completed'), { failed: true });
      assert.equal(evt.seq, 0, 'persist failure keeps seq 0');

      await waitFor(() => res.events().length === 2);
      const last = res.events()[1];
      assert.equal(last.seq, 0, 'seq-0 event delivered live');
      assert.deepEqual(last.payload, { failed: true });
    } finally {
      disconnect();
    }
  } finally {
    cleanup();
  }
});

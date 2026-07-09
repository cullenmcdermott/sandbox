// A2: appendEvent redacts secrets in sensitive event payloads BEFORE persist AND
// broadcast, so the SQLite log, the live SSE frame, and (via E2's rawFrame
// splice) replay all carry the masked form — matching audit.jsonl (M13). Every
// other event type passes through untouched.
//
// Exercises the REAL src/events.ts (appendEvent + attachSseClient) against a temp
// better-sqlite3 DB injected via __setEventLogForTest — the production
// EVENTS_DB_PATH is hard-coded under /session (unwritable off-pod).
//
// GUARD: SKIPS cleanly when better-sqlite3's native addon is unavailable, UNLESS
// RUNNER_REQUIRE_SQLITE=1 (CI), via the shared sqlite-probe.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';
import type { ServerResponse } from 'node:http';
import type { EventType } from '../src/types.js';
import { appendEvent, attachSseClient, __setEventLogForTest } from '../src/events.js';

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
  const dir = mkdtempSync(join(tmpdir(), 'events-redaction-'));
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

test('A2 redaction: a tool.started payload with nested secret-keyed fields is masked in the live broadcast AND persisted rows', { skip }, async () => {
  const { db, cleanup } = setupDb();
  try {
    const sid = 'sess-A';

    // A client attached and caught up, so this append is a LIVE broadcast.
    const live = new FakeRes();
    const disconnect = attachSseClient(asRes(live), sid, 0);
    await waitFor(() => live.hasReplayComplete());

    const secretPayload = {
      name: 'Bash',
      input: {
        command: 'deploy',
        apiKey: 'sk-live-supersecret-value',
        nested: { token: 'abc123def', safe: 'keep-me' },
      },
    };
    appendEvent(sid, 'turn-1', T('tool.started'), secretPayload);
    await waitFor(() => live.events().length === 1);

    // Live SSE frame carries the redacted form.
    const liveEvt = live.events()[0];
    const liveInput = (liveEvt.payload as Record<string, Record<string, unknown>>).input;
    assert.equal(liveInput.apiKey, '[redacted]', 'secret-keyed apiKey masked in live frame');
    assert.equal(
      (liveInput.nested as Record<string, unknown>).token,
      '[redacted]',
      'nested secret-keyed token masked in live frame',
    );
    assert.equal(
      (liveInput.nested as Record<string, unknown>).safe,
      'keep-me',
      'non-secret field preserved',
    );
    assert.equal(liveInput.command, 'deploy', 'non-secret field preserved');

    // The PERSISTED row (what a fresh after=0 replay would splice) is also masked.
    const row = db
      .prepare("SELECT payload FROM events WHERE type = 'tool.started'")
      .get() as { payload: string };
    assert.equal(row.payload.includes('sk-live-supersecret-value'), false, 'secret not on disk');
    assert.equal(row.payload.includes('[redacted]'), true, 'persisted payload masked');

    // A fresh replay reconstructs the masked form byte-for-byte.
    const replay = new FakeRes();
    const dR = attachSseClient(asRes(replay), sid, 0);
    try {
      await waitFor(() => replay.hasReplayComplete());
      const rInput = (replay.events()[0].payload as Record<string, Record<string, unknown>>).input;
      assert.equal(rInput.apiKey, '[redacted]', 'replayed payload masked too');
    } finally {
      dR();
    }
    disconnect();
  } finally {
    cleanup();
  }
});

test('A2 redaction: a non-sensitive event type (session.status_changed) with a secret-keyed field passes through UNtouched', { skip }, async () => {
  const { db, cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    // Contrived: a status payload happens to hold a `token` key. Per the review's
    // exact set, only turn.started / tool.* / permission.* are redacted, so this
    // must pass through verbatim (the hot path must not deep-walk it).
    const payload = { status: 'idle', token: 'not-actually-redacted' };
    const evt = appendEvent(sid, undefined, T('session.status_changed'), payload);

    assert.equal(
      (evt.payload as Record<string, unknown>).token,
      'not-actually-redacted',
      'returned payload untouched',
    );
    const row = db
      .prepare("SELECT payload FROM events WHERE type = 'session.status_changed'")
      .get() as { payload: string };
    assert.equal(
      row.payload.includes('not-actually-redacted'),
      true,
      'persisted payload untouched for non-sensitive type',
    );
  } finally {
    cleanup();
  }
});

test('A2 redaction: permission.requested and turn.started payloads are also masked', { skip }, () => {
  const { db, cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    appendEvent(sid, 'turn-1', T('permission.requested'), {
      tool: 'Bash',
      input: { password: 'hunter2' },
    });
    appendEvent(sid, 'turn-1', T('turn.started'), { prompt: 'run it', api_key: 'sk-abcdefgh' });

    const perm = db
      .prepare("SELECT payload FROM events WHERE type = 'permission.requested'")
      .get() as { payload: string };
    assert.equal(perm.payload.includes('hunter2'), false, 'permission secret masked');
    const turn = db
      .prepare("SELECT payload FROM events WHERE type = 'turn.started'")
      .get() as { payload: string };
    assert.equal(turn.payload.includes('sk-abcdefgh'), false, 'turn.started api_key masked');
  } finally {
    cleanup();
  }
});

// Cross-seam pin (A2 × D5): the turn adapters echo the driving prompt as
// message.started/completed role:"user" — the SAME text turn.started carries.
// Redacting turn.started but not the echo would leak the secret anyway, so
// role:user message.* payloads are masked too; assistant message.* stays
// untouched (model output — mangling code it wrote is worse than the marginal
// exposure, and the message.delta hot path must stay walk-free).
test('A2×D5: the role:user message echo is masked; assistant messages are not', { skip }, () => {
  const { db, cleanup } = setupDb();
  try {
    const sid = 'sess-A';
    const leaky = 'use sk-abcdefghij to auth';
    appendEvent(sid, 'turn-1', T('message.completed'), { role: 'user', content: leaky });
    appendEvent(sid, 'turn-1', T('message.completed'), { role: 'assistant', content: leaky });

    const rows = db
      .prepare("SELECT payload FROM events WHERE type = 'message.completed' ORDER BY seq ASC")
      .all() as Array<{ payload: string }>;
    assert.equal(rows[0].payload.includes('sk-abcdefghij'), false, 'user echo masked');
    assert.equal(rows[0].payload.includes('[redacted]'), true);
    assert.equal(rows[1].payload.includes('sk-abcdefghij'), true, 'assistant content untouched');
  } finally {
    cleanup();
  }
});

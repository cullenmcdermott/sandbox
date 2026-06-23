// SQLite append-only event log and SSE fan-out.
//
// One writer (the runner process). Events are persisted to events.db with a
// monotonic seq BEFORE being streamed to connected SSE clients, so replay via
// after=<seq> never misses or reorders events relative to a live tail.

import Database from 'better-sqlite3';
import { randomUUID } from 'node:crypto';
import { mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import type { ServerResponse } from 'node:http';
import type { Event, EventType } from './types.js';
import { EVENTS_DB_PATH, STATE_DIR } from './types.js';

type AnyPayload = Record<string, unknown>;

/** A connected SSE client waiting for events after a given seq. */
interface SseClient {
  res: ServerResponse;
  afterSeq: number;
}

let db: Database.Database | null = null;
const clients = new Set<SseClient>();

/** Open (or reopen) the SQLite event log and ensure the schema exists. */
export function openEventLog(): void {
  mkdirSync(STATE_DIR, { recursive: true });
  mkdirSync(dirname(EVENTS_DB_PATH), { recursive: true });
  db = new Database(EVENTS_DB_PATH);
  db.pragma('journal_mode = WAL');
  db.pragma('synchronous = NORMAL');
  db.exec(`
    CREATE TABLE IF NOT EXISTS events (
      seq        INTEGER PRIMARY KEY AUTOINCREMENT,
      time       TEXT    NOT NULL,
      session_id TEXT    NOT NULL,
      turn_id    TEXT,
      type       TEXT    NOT NULL,
      payload    TEXT    NOT NULL
    );
    CREATE INDEX IF NOT EXISTS idx_events_session_seq
      ON events(session_id, seq);
  `);
}

function getDb(): Database.Database {
  if (!db) openEventLog();
  return db!;
}

/**
 * Append an event to the log, assign it the next monotonic seq, persist, then
 * broadcast to connected SSE clients. Returns the persisted event (with seq).
 */
export function appendEvent(
  sessionId: string,
  turnId: string | undefined,
  type: EventType,
  payload: AnyPayload,
): Event {
  const d = getDb();
  const time = new Date().toISOString();
  const payloadJson = JSON.stringify(payload);
  const info = d
    .prepare(
      'INSERT INTO events (time, session_id, turn_id, type, payload) VALUES (?, ?, ?, ?, ?)',
    )
    .run(time, sessionId, turnId ?? null, type, payloadJson);
  const seq = Number(info.lastInsertRowid);
  const evt: Event = {
    seq,
    time,
    sessionId,
    ...(turnId ? { turnId } : {}),
    type,
    payload,
  };
  broadcast(evt);
  return evt;
}

/** Read all events for a session with seq > afterSeq, ordered by seq. */
export function readEventsAfter(sessionId: string, afterSeq: number): Event[] {
  const d = getDb();
  const rows = d
    .prepare(
      'SELECT seq, time, session_id, turn_id, type, payload FROM events WHERE session_id = ? AND seq > ? ORDER BY seq ASC',
    )
    .all(sessionId, afterSeq) as Array<{
    seq: number;
    time: string;
    session_id: string;
    turn_id: string | null;
    type: string;
    payload: string;
  }>;
  return rows.map((r) => ({
    seq: r.seq,
    time: r.time,
    sessionId: r.session_id,
    ...(r.turn_id ? { turnId: r.turn_id } : {}),
    type: r.type as EventType,
    payload: JSON.parse(r.payload) as AnyPayload,
  }));
}

/** Highest seq seen for a session (0 if none). */
export function lastSeq(sessionId: string): number {
  const d = getDb();
  const row = d
    .prepare('SELECT MAX(seq) AS maxSeq FROM events WHERE session_id = ?')
    .get(sessionId) as { maxSeq: number | null } | undefined;
  return row?.maxSeq ?? 0;
}

// --- SSE fan-out ----------------------------------------------------------

function sseFrame(evt: Event): string {
  return `data: ${JSON.stringify(evt)}\n\n`;
}

function broadcast(evt: Event): void {
  for (const client of clients) {
    if (evt.seq <= client.afterSeq) continue;
    writeSse(client.res, sseFrame(evt));
  }
}

/** Send historical replay to a freshly connected client (seq > afterSeq). */
function replayTo(client: SseClient, sessionId: string): void {
  const events = readEventsAfter(sessionId, client.afterSeq);
  for (const evt of events) {
    writeSse(client.res, sseFrame(evt));
  }
}

function writeSse(res: ServerResponse, data: string): void {
  if (res.writableEnded || res.destroyed) return;
  res.write(data);
}

/**
 * Attach an SSE client to the event stream for `sessionId`, replaying events
 * after `afterSeq`, then tailing live events. Returns a disconnect function.
 */
export function attachSseClient(
  res: ServerResponse,
  sessionId: string,
  afterSeq: number,
): () => void {
  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache, no-transform',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  });
  // Heartbeat comment keeps proxies from timing out the idle stream.
  res.write(': stream-open\n\n');

  const client: SseClient = { res, afterSeq };
  clients.add(client);
  replayTo(client, sessionId);

  const cleanup = (): void => {
    clients.delete(client);
    if (!res.writableEnded && !res.destroyed) {
      try {
        res.end();
      } catch {
        /* already closed */
      }
    }
  };
  res.on('close', cleanup);
  res.on('error', cleanup);
  return cleanup;
}

/** Generate a short unique id, e.g. for permission requests. */
export function shortId(prefix: string): string {
  const id = randomUUID().split('-')[0];
  return `${prefix}-${id}`;
}

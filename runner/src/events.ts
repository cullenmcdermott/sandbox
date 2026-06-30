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
  /** A passive client is a status observer (e.g. the command-center dashboard's
   * background list streams) — it receives events but does NOT count as
   * "attached" for idle detection, so it cannot keep the idle reaper from
   * suspending a session the user is only glancing at in the list (RV6). */
  passive: boolean;
}

let db: Database.Database | null = null;
const clients = new Set<SseClient>();

/** Current event-log schema version (bump + migrate on shape changes). */
const SCHEMA_VERSION = 1;

/** Max concurrent SSE clients per session; 0 disables the cap. A single bad or
 * buggy client can otherwise open unbounded streams and fan-out every event to
 * each (M33). The dashboard uses at most a few per session. */
export const MAX_SSE_CLIENTS = 16;

/** Keep at most this many most-recent events (one session per pod). 0 disables
 * retention — the default, because pruning truncates after=0 replay history.
 * Opt in via RETENTION_MAX_EVENTS to bound long-lived logs (M34). */
const RETENTION_MAX_EVENTS = ((): number => {
  const v = parseInt(process.env.RETENTION_MAX_EVENTS ?? '', 10);
  return Number.isFinite(v) && v > 0 ? v : 0;
})();

/** Number of ACTIVE (non-passive) SSE clients currently attached. This is the
 * count used for idle detection: a session is "detached" only when no real
 * client is watching, so passive status observers (dashboard list streams) are
 * excluded and do not block the idle reaper (RV6). */
export function sseClientCount(): number {
  let n = 0;
  for (const c of clients) {
    if (!c.passive) n++;
  }
  return n;
}

/** Total SSE clients (active + passive). Used only to bound concurrent streams
 * against fan-out abuse (M33) — passive observers still consume a connection. */
export function sseTotalClientCount(): number {
  return clients.size;
}

// Optional hook invoked whenever the attached-client count changes, so the
// session registry can recompute idleSince (turn-done AND detached).
let onClientsChanged: (() => void) | null = null;
export function setClientsChangedHandler(fn: () => void): void {
  onClientsChanged = fn;
}

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
  db.pragma(`user_version = ${SCHEMA_VERSION}`);
  pruneOldEvents();
}

// pruneOldEvents bounds the event log when RETENTION_MAX_EVENTS is set, keeping
// the most-recent N events (one session per pod). Default disabled (M34).
function pruneOldEvents(): void {
  if (RETENTION_MAX_EVENTS <= 0 || !db) return;
  db.prepare(
    'DELETE FROM events WHERE seq <= (SELECT COALESCE(MAX(seq), 0) FROM events) - ?',
  ).run(RETENTION_MAX_EVENTS);
}

function getDb(): Database.Database {
  if (!db) openEventLog();
  return db!;
}

/** Checkpoint the WAL and close the DB on shutdown so no events are lost. */
export function closeEventLog(): void {
  if (!db) return;
  try {
    db.pragma('wal_checkpoint(TRUNCATE)');
    db.close();
  } catch {
    /* best effort during shutdown */
  }
  db = null;
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
  let seq = 0;
  try {
    const info = d
      .prepare(
        'INSERT INTO events (time, session_id, turn_id, type, payload) VALUES (?, ?, ?, ?, ?)',
      )
      .run(time, sessionId, turnId ?? null, type, payloadJson);
    seq = Number(info.lastInsertRowid);
  } catch (err) {
    // R11: SQLite write failure must not crash the turn loop. Log and continue
    // with seq=0. Callers (mapMessage, appendBlock) are fire-and-forget; a
    // missed event in the log is preferable to a killed turn.
    console.error(`appendEvent: failed to persist ${type}:`, err);
  }
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
  // Serialize the SSE frame once, not once per client: the wire bytes are
  // identical for every recipient, so a fan-out of N clients did N redundant
  // JSON.stringify passes over the same event. Hoisting it makes broadcast
  // O(clients) string writes with a single O(payload) serialization.
  const frame = sseFrame(evt);
  for (const client of clients) {
    if (evt.seq <= client.afterSeq) continue;
    writeSse(client.res, frame);
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
  passive = false,
): () => void {
  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache, no-transform',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  });
  // Heartbeat comment keeps proxies and LBs from timing out the idle stream.
  // The one-shot open comment is sent immediately; a periodic timer sends
  // further keepalives every 30 s so half-open sockets are detected promptly
  // and the stream survives idle periods behind proxy/LB timeouts (R5).
  res.write(': stream-open\n\n');
  const heartbeatInterval = setInterval(() => {
    if (res.writableEnded || res.destroyed) {
      clearInterval(heartbeatInterval);
      return;
    }
    try {
      res.write(': heartbeat\n\n');
    } catch {
      clearInterval(heartbeatInterval);
    }
  }, 30_000);

  const client: SseClient = { res, afterSeq, passive };
  clients.add(client);
  onClientsChanged?.();
  replayTo(client, sessionId);
  // Replay/live boundary (Workstream C): replayTo is synchronous, so this comment
  // lands immediately after the last historical frame and before any live event
  // can be broadcast. The CLI surfaces it as a stream.live marker so the TUI knows
  // the catch-up is done and stops showing "loading transcript…".
  writeSse(res, ': replay-complete\n\n');

  let cleanedUp = false;
  const cleanup = (): void => {
    if (cleanedUp) return;
    cleanedUp = true;
    clearInterval(heartbeatInterval);
    clients.delete(client);
    onClientsChanged?.();
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

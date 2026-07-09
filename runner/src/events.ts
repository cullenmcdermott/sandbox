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
  /** E2: true while the client is still catching up on history via the async
   * chunk-reader. broadcast() skips a replaying client (its persisted events
   * arrive through replay instead), and the replay driver flips this to false in
   * the same synchronous tick it writes `: replay-complete`, handing the client
   * over to the live tail with no gap, no duplicate, and no reordering. */
  replaying: boolean;
  /** Idempotent teardown (clears the heartbeat, removes from `clients`, ends the
   * socket). Stored on the client so broadcast()'s E3 backpressure cap can evict
   * a wedged client synchronously without reaching into attachSseClient's closure. */
  cleanup: () => void;
}

let db: Database.Database | null = null;
const clients = new Set<SseClient>();

/** Current event-log schema version, stamped into SQLite's `user_version` and
 * read back on every open. Bump it — and register a step in MIGRATIONS — when
 * the table shape changes. openEventLog refuses a database stamped NEWER than
 * this (a rolled-back runner image must not reinterpret state it doesn't
 * understand) and upgrades an older one step-by-step. */
export const SCHEMA_VERSION = 1;

/** The current (version-SCHEMA_VERSION) shape, applied to fresh databases. */
const SCHEMA_SQL = `
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
`;

/** MIGRATIONS[n] upgrades a version-n database to version n+1. Each step runs
 * in a transaction with the version stamp, so a crash mid-migration leaves the
 * database at a well-defined version. Empty today: v1 is the first shape. */
const MIGRATIONS: Record<number, (d: Database.Database) => void> = {};

/** Max concurrent SSE clients per session; 0 disables the cap. A single bad or
 * buggy client can otherwise open unbounded streams and fan-out every event to
 * each (M33). The dashboard uses at most a few per session. */
export const MAX_SSE_CLIENTS = 16;

/** E2: how many rows the async replay reader pulls per chunk. Replay reads the
 * log in bounded batches (never `.all()` over the whole log) and yields to the
 * event loop between chunks, so a long session's after=0 attach can't blow up
 * RSS or block live turns / /healthz / interrupts in one synchronous write burst.
 * Each batch is fully consumed (materialized by `.all(... LIMIT ?)`) before any
 * await, so no open SQLite iterator holds the single better-sqlite3 connection
 * busy across a yield (which would make a concurrent appendEvent INSERT throw). */
export const REPLAY_CHUNK_ROWS = 512;

/** E3: cap on bytes a single LIVE SSE client may have buffered (res.writableLength)
 * before broadcast() treats it as wedged and destroys the connection. A half-open
 * socket or a reader that has stopped consuming otherwise accumulates every
 * broadcast frame in runner RSS until the pod OOMs — which surfaces to users as
 * "the session died". 4 MiB is far above any single frame or a healthy client's
 * transient backlog, so only a genuinely stuck reader trips it; it then reconnects
 * and replays from its last seq. The REPLAY path does NOT use this cap: replay
 * awaits `drain`, which is its own backpressure. */
export const MAX_SSE_CLIENT_BUFFER_BYTES = 4 * 1024 * 1024;

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

/** Open (or reopen) the SQLite event log and ensure the schema exists.
 * Throws (crashing the boot into a visible CrashLoopBackOff) when the on-disk
 * schema is newer than this runner supports — see migrateEventLog. */
export function openEventLog(): void {
  mkdirSync(STATE_DIR, { recursive: true });
  mkdirSync(dirname(EVENTS_DB_PATH), { recursive: true });
  db = new Database(EVENTS_DB_PATH);
  db.pragma('journal_mode = WAL');
  db.pragma('synchronous = NORMAL');
  try {
    migrateEventLog(db);
  } catch (err) {
    // Don't leave a half-opened handle for getDb() to reuse.
    db.close();
    db = null;
    throw err;
  }
  pruneOldEvents();
}

/**
 * Read-compare-migrate the event-log schema. With user-built runner images and
 * `:latest` tags, version skew between the runner binary and PVC state is the
 * steady state, not an edge case:
 *
 * - on-disk version > SCHEMA_VERSION: an older runner is reading state written
 *   by a newer one — refuse, so it cannot silently misread (or corrupt) rows
 *   under stale shape assumptions. The fix is a runner image at least as new
 *   as the one that wrote the PVC.
 * - on-disk version < SCHEMA_VERSION: walk MIGRATIONS one version at a time.
 * - user_version 0 with an existing events table predates read-back
 *   versioning; every such database has the v1 shape (v1 has been stamped
 *   since the log's first release), so it is treated as version 1.
 *
 * Exported for tests (the production path constants point at /session).
 */
export function migrateEventLog(d: Database.Database): void {
  const onDisk = d.pragma('user_version', { simple: true }) as number;
  if (onDisk > SCHEMA_VERSION) {
    throw new Error(
      `events.db schema version ${onDisk} is newer than this runner supports (${SCHEMA_VERSION}); ` +
        'refusing to open. Use a runner image at least as new as the one that last wrote this session.',
    );
  }
  const hasEvents =
    d.prepare("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'events'").get() !==
    undefined;
  if (!hasEvents) {
    // Fresh database: create the current shape directly, no migration walk.
    d.exec(SCHEMA_SQL);
    d.pragma(`user_version = ${SCHEMA_VERSION}`);
    return;
  }
  for (let v = Math.max(onDisk, 1); v < SCHEMA_VERSION; v++) {
    const step = MIGRATIONS[v];
    if (!step) {
      throw new Error(`events.db: no migration from schema version ${v} to ${v + 1}`);
    }
    d.transaction(() => {
      step(d);
      d.pragma(`user_version = ${v + 1}`);
    })();
  }
  d.pragma(`user_version = ${SCHEMA_VERSION}`);
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

/**
 * Test-only: point the module at an already-opened database (or null to reset)
 * and clear the connected-client set, so the SSE fan-out + async replay can be
 * exercised against a temp DB. The production EVENTS_DB_PATH is hard-coded under
 * /session (unwritable off-pod), so tests build their own DB and inject it here
 * rather than importing that path. Not part of the runner API — internal runner
 * code never calls it, and it is unreachable over HTTP.
 */
export function __setEventLogForTest(d: Database.Database | null): void {
  db = d;
  clients.clear();
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

/** A raw event row as stored in SQLite (payload is still a JSON string). */
interface EventRow {
  seq: number;
  time: string;
  session_id: string;
  turn_id: string | null;
  type: string;
  payload: string;
}

/**
 * Read all events for a session with seq > afterSeq, ordered by seq, parsing each
 * payload into an Event. NOTE: this materializes the whole matching range in
 * memory — the hot replay path (attachSseClient → streamReplayThenAttach) does
 * NOT use it; it streams raw rows in bounded chunks (E2). Kept for callers that
 * want fully-decoded events.
 */
export function readEventsAfter(sessionId: string, afterSeq: number): Event[] {
  const d = getDb();
  const rows = d
    .prepare(
      'SELECT seq, time, session_id, turn_id, type, payload FROM events WHERE session_id = ? AND seq > ? ORDER BY seq ASC',
    )
    .all(sessionId, afterSeq) as EventRow[];
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

/**
 * Whether a live-broadcast event with `eventSeq` should be delivered to a client
 * caught up to `afterSeq`. Pure + exported so the fan-out gate is unit-testable.
 *
 * Normal case: deliver only events strictly after what the client already has
 * (`eventSeq > afterSeq`) — the after=<seq> replay contract, so a live tail never
 * re-delivers replayed history.
 *
 * B4: an event whose SQLite insert FAILED keeps seq === 0 (appendEvent's R11
 * fallback: "a missed log event beats a killed turn"). afterSeq is always >= 0,
 * so `eventSeq <= afterSeq` would drop every seq-0 event for every client — e.g.
 * a disk-full during `turn.completed` vanishes from BOTH the log and the live
 * stream, leaving the TUI stuck "working" until a reattach. So seq === 0 bypasses
 * the filter and is delivered live to everyone. This is safe against replay
 * ordering: real seqs start at 1 (AUTOINCREMENT), so seq 0 never collides with a
 * persisted event; and because it was never written to the log, a client that
 * later reconnects with after=<lastPersistedSeq> simply never replays it — which
 * is exactly the intended live-only, best-effort delivery.
 */
export function shouldDeliver(eventSeq: number, afterSeq: number): boolean {
  if (eventSeq === 0) return true;
  return eventSeq > afterSeq;
}

function broadcast(evt: Event): void {
  if (clients.size === 0) return;
  // Serialize the frame once — it is identical for every client.
  const frame = sseFrame(evt);
  for (const client of clients) {
    // E2: a client still replaying history receives its persisted events through
    // the replay chunk-reader, not the live broadcast. Skipping it here is what
    // keeps replay and live from interleaving (and prevents duplicates): an event
    // appended DURING replay is persisted BEFORE this broadcast (appendEvent's
    // append-before-stream invariant), so the replay reader picks it up from
    // SQLite by seq; the client is switched live only at the replay handoff. A
    // seq-0 persist-failure event (B4) is never in the log, so a client mid-replay
    // simply doesn't see that one — exactly as under the old synchronous replay,
    // where no appendEvent could run mid-replay at all; every already-attached
    // (non-replaying) client still gets it live.
    if (client.replaying) continue;
    if (!shouldDeliver(evt.seq, client.afterSeq)) continue;
    const res = client.res;
    if (res.writableEnded || res.destroyed) continue;
    // E3: evict a client that has buffered more than the cap. Ignoring
    // res.write()'s backpressure signal lets a wedged reader accumulate every
    // frame in runner RSS until the pod OOMs; destroy it instead (a healthy
    // client reconnects and replays from its last seq). Only the LIVE path caps
    // this way — replay awaits `drain`, which is its own backpressure.
    if (res.writableLength > MAX_SSE_CLIENT_BUFFER_BYTES) {
      console.error(
        `broadcast: SSE client buffered ${res.writableLength}B > ${MAX_SSE_CLIENT_BUFFER_BYTES}B cap; ` +
          'destroying wedged stream (it can reconnect and replay from its last seq)',
      );
      res.destroy();
      client.cleanup();
      continue;
    }
    res.write(frame);
  }
}

/**
 * E2: build an SSE `data:` frame directly from a raw event row, WITHOUT
 * JSON.parse-ing the payload and re-stringifying the whole event. The payload
 * column already holds a JSON document (appendEvent stored JSON.stringify(payload)),
 * so it is spliced in verbatim. Field order and the omit-turnId-when-NULL rule
 * match how a live Event serializes via sseFrame → JSON.stringify (seq, time,
 * sessionId, turnId?, type, payload), so replay and live frames are byte-identical
 * for the same event. Because JSON.stringify escapes embedded newlines, the frame
 * stays a single `data:` line, which the Go client's SSE scanner requires.
 */
function rawFrame(row: EventRow): string {
  const turnPart = row.turn_id != null ? `"turnId":${JSON.stringify(row.turn_id)},` : '';
  return (
    `data: {"seq":${row.seq},"time":${JSON.stringify(row.time)},` +
    `"sessionId":${JSON.stringify(row.session_id)},${turnPart}` +
    `"type":${JSON.stringify(row.type)},"payload":${row.payload}}\n\n`
  );
}

/** Resolve on the socket's next `drain`, or immediately if it closes/errors —
 * so the replay loop unblocks and then notices the disconnect and aborts. */
function onceDrainOrClose(res: ServerResponse): Promise<void> {
  return new Promise((resolve) => {
    const done = (): void => {
      res.off('drain', done);
      res.off('close', done);
      res.off('error', done);
      resolve();
    };
    res.once('drain', done);
    res.once('close', done);
    res.once('error', done);
  });
}

/** Yield a macrotask so a large replay can't monopolize the event loop. */
function yieldToEventLoop(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

/**
 * E2: stream historical replay to a freshly attached client in bounded chunks,
 * then atomically hand the client over to the live tail.
 *
 * Correctness rests on appendEvent's append-before-stream invariant (persist to
 * SQLite, THEN broadcast) plus a synchronous handoff:
 *   - The client is in `clients` from attach time (so idle detection / the M33
 *     cap see it immediately) but carries replaying=true, so broadcast() skips it
 *     for every live event during replay — no interleave, no duplicate.
 *   - Each chunk is read with `.all(... LIMIT ?)` (fully materialized, no open
 *     iterator held across an await) starting from a cursor; rows are written in
 *     ascending seq; the cursor advances to the last written seq. An event
 *     appended during replay has a seq greater than everything read so far, so a
 *     later chunk read picks it up — delivered exactly once, in order, via replay.
 *   - When a chunk read returns zero rows the client is caught up. The handoff —
 *     set afterSeq=cursor, replaying=false, write `: replay-complete` — runs in
 *     ONE synchronous tick with NO await between the zero-row read and the flip,
 *     so no appendEvent can slip in unseen: anything appended after the handoff
 *     has seq > cursor and is delivered live by broadcast().
 * Aborts on disconnect (isCancelled / socket closed) at every iteration so a
 * client that leaves mid-replay stops the loop and never gets re-registered.
 */
async function streamReplayThenAttach(
  client: SseClient,
  sessionId: string,
  isCancelled: () => boolean,
): Promise<void> {
  const d = getDb();
  const stmt = d.prepare(
    'SELECT seq, time, session_id, turn_id, type, payload FROM events ' +
      'WHERE session_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?',
  );
  const res = client.res;
  let cursor = client.afterSeq;

  for (;;) {
    if (isCancelled() || res.writableEnded || res.destroyed) return;
    const rows = stmt.all(sessionId, cursor, REPLAY_CHUNK_ROWS) as EventRow[];
    if (rows.length === 0) {
      // Handoff — MUST stay synchronous (no await) through the end of this block.
      client.afterSeq = cursor;
      client.replaying = false;
      writeSse(res, ': replay-complete\n\n');
      return;
    }
    for (const row of rows) {
      if (isCancelled() || res.writableEnded || res.destroyed) return;
      const ok = res.write(rawFrame(row));
      cursor = row.seq;
      // Replay backpressure: let the socket drain before queueing more (this IS
      // the replay path's flow control — it never uses the E3 destroy cap).
      if (!ok) await onceDrainOrClose(res);
    }
    // Yield between chunks so live turns / health checks keep flowing.
    await yieldToEventLoop();
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

  // E2: register the client immediately (so idle detection and the M33 cap count
  // it right away — RV6) but mark it replaying, so broadcast() withholds live
  // events until the async replay catches it up and writes `: replay-complete`.
  const client: SseClient = { res, afterSeq, passive, replaying: true, cleanup: () => {} };
  clients.add(client);
  onClientsChanged?.();

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
  client.cleanup = cleanup;
  res.on('close', cleanup);
  res.on('error', cleanup);

  // Stream history in bounded chunks, then hand off to the live tail. The handoff
  // writes the `: replay-complete` boundary the CLI surfaces as a stream.live
  // marker (TUI stops showing "loading transcript…"). Kicked off async so a long
  // replay yields to the event loop instead of blocking it. A replay that throws
  // (e.g. DB closed under a disconnecting client) must not become an unhandled
  // rejection that crashes the runner, so it falls through to cleanup.
  void streamReplayThenAttach(client, sessionId, () => cleanedUp).catch((err) => {
    console.error(`attachSseClient: replay failed for ${sessionId}:`, err);
    cleanup();
  });

  return cleanup;
}

/** Generate a short unique id, e.g. for permission requests. */
export function shortId(prefix: string): string {
  const id = randomUUID().split('-')[0];
  return `${prefix}-${id}`;
}

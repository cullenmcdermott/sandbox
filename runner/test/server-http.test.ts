// F4: end-to-end coverage of the runner's HTTP layer (src/server.ts) by booting
// the REAL createRunnerServer on an ephemeral port with a stub agent and driving
// it over a real localhost socket. Everything below the Claude/opencode agent
// boundary is real: the node:http router, bearer-auth enforcement, the 409 turn
// gate, the typed-body (B9) error mapping, and SSE replay/clamp against a real
// better-sqlite3 event log in a temp dir. Only Agent.runTurn is faked (so a turn
// stays "active" without invoking a backend) and saveSessionState is redirected
// to a temp file (off-pod /session is read-only — see __setSessionJsonPathForTest).
//
// GUARD: like events-replay.test.ts, this suite SKIPS cleanly when better-sqlite3's
// native addon is unavailable, UNLESS RUNNER_REQUIRE_SQLITE=1 (CI), in which case
// sqlite-probe throws at import so it can never silently self-skip.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';
import { createRunnerServer } from '../src/server.js';
import { initRegistry, loadConfig, __setSessionJsonPathForTest } from '../src/session.js';
import { appendEvent, __setEventLogForTest } from '../src/events.js';
import { PROTOCOL_VERSION } from '../src/types.js';
import type { EventType, SessionState } from '../src/types.js';
import type { Agent } from '../src/agent.js';

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

const T = (t: string): EventType => t as EventType;

interface BootOpts {
  backend?: string;
  token?: string;
  sid?: string;
  status?: SessionState['status'];
  lastTurnId?: string;
  /** Pass `null` to model a supervise-only backend (no Agent). Omit for the
   * default never-resolving stub that keeps a started turn "active". */
  agent?: Agent | null;
}

interface Booted {
  port: number;
  token: string;
  sid: string;
  runTurnCalls: unknown[][];
  cleanup: () => void;
}

/** Boot the real router on an ephemeral port with a real temp event log + a
 * writable temp session.json, returning the port and a cleanup(). */
async function boot(opts: BootOpts = {}): Promise<Booted> {
  const token = opts.token ?? 'secret-token';
  const sid = opts.sid ?? 'sess-http';
  const backend = opts.backend ?? 'claude-sdk';
  process.env.RUNNER_TOKEN = token;
  process.env.SANDBOX_SESSION_ID = sid;
  process.env.SANDBOX_BACKEND = backend;
  process.env.PROJECT_PATH = '/work/proj';

  const dir = mkdtempSync(join(tmpdir(), 'server-http-'));
  const Db = Database!;
  const db = new Db(join(dir, 'events.db'));
  db.pragma('journal_mode = WAL');
  db.exec(CREATE_SQL);
  __setEventLogForTest(db);
  __setSessionJsonPathForTest(join(dir, 'session.json'));

  const state: SessionState = {
    state_version: 1,
    sandbox_session_id: sid,
    backend,
    project_path: '/work/proj',
    status: opts.status ?? 'idle',
    claude_session_id: '',
    opencode_session_id: '',
    last_turn_id: opts.lastTurnId ?? '',
    last_activity: new Date().toISOString(),
  };
  initRegistry(state);

  const cfg = loadConfig();
  const runTurnCalls: unknown[][] = [];
  // Default stub: record the call and never resolve, so the turn stays registered
  // (activeTurns > 0) for the second-turn 409 gate. Fire-and-forget in the route.
  const stub: Agent = { runTurn: (...args: unknown[]) => { runTurnCalls.push(args); return new Promise<void>(() => {}); } };
  const agent = opts.agent === undefined ? stub : opts.agent;

  const server = createRunnerServer(cfg, agent);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;

  return {
    port,
    token,
    sid,
    runTurnCalls,
    cleanup(): void {
      // Force-close keep-alive + open SSE sockets so server.close() resolves and
      // each SSE client's 'close' fires (clearing its heartbeat interval).
      server.closeAllConnections();
      server.close();
      __setEventLogForTest(null);
      __setSessionJsonPathForTest(null);
      try {
        db.close();
      } catch {
        /* may already be closed */
      }
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

interface Resp {
  status: number;
  headers: http.IncomingHttpHeaders;
  text: string;
  json: unknown;
}

/** One buffered HTTP request against 127.0.0.1:port. */
function req(
  port: number,
  method: string,
  path: string,
  o: { token?: string | null; body?: string } = {},
): Promise<Resp> {
  return new Promise((resolve, reject) => {
    const headers: Record<string, string> = {};
    if (o.token) headers['authorization'] = `Bearer ${o.token}`;
    if (o.body !== undefined) headers['content-type'] = 'application/json';
    let responded = false;
    const request = http.request({ host: '127.0.0.1', port, method, path, headers }, (res) => {
      responded = true;
      const chunks: Buffer[] = [];
      res.on('data', (c: Buffer) => chunks.push(c));
      const finish = (): void => {
        const text = Buffer.concat(chunks).toString('utf8');
        let json: unknown;
        try {
          json = text ? JSON.parse(text) : undefined;
        } catch {
          /* leave json undefined for non-JSON bodies */
        }
        resolve({ status: res.statusCode ?? 0, headers: res.headers, text, json });
      };
      res.on('end', finish);
      // The 413 path destroys the request socket after replying; 'close' still
      // delivers whatever response bytes arrived.
      res.on('close', finish);
    });
    // A reset that lands before any response is a real failure; after the
    // response it's just the server tearing down the oversized upload.
    request.on('error', (e) => {
      if (!responded) reject(e);
    });
    if (o.body !== undefined) request.write(o.body);
    request.end();
  });
}

/** A live SSE reader: collects the raw wire and parses `data:` frames. */
function openSse(port: number, path: string, token: string): {
  wire: () => string;
  events: () => Array<Record<string, unknown>>;
  hasReplayComplete: () => boolean;
  close: () => void;
} {
  const chunks: string[] = [];
  const request = http.request(
    { host: '127.0.0.1', port, method: 'GET', path, headers: { authorization: `Bearer ${token}` } },
    (res) => {
      res.setEncoding('utf8');
      res.on('data', (c: string) => chunks.push(c));
    },
  );
  request.on('error', () => {
    /* torn down on close() */
  });
  request.end();
  const wire = (): string => chunks.join('');
  return {
    wire,
    events: (): Array<Record<string, unknown>> =>
      wire()
        .split('\n\n')
        .filter((f) => f.startsWith('data: '))
        .map((f) => JSON.parse(f.slice('data: '.length)) as Record<string, unknown>),
    hasReplayComplete: (): boolean => wire().includes(': replay-complete'),
    close: (): void => request.destroy(),
  };
}

async function waitFor(pred: () => boolean, timeoutMs = 2000): Promise<void> {
  const start = Date.now();
  while (!pred()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor: timed out');
    await new Promise((r) => setTimeout(r, 5));
  }
}

// --- healthz + bearer-token enforcement -----------------------------------

test('GET /healthz is unauthenticated and reports the protocol version', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'GET', '/healthz'); // no token
    assert.equal(r.status, 200);
    assert.deepEqual(r.json, { status: 'ok', protocolVersion: PROTOCOL_VERSION });
  } finally {
    b.cleanup();
  }
});

test('a non-healthz route 401s with no bearer token', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'GET', `/sessions/${b.sid}/status`); // no token
    assert.equal(r.status, 401);
    assert.deepEqual(r.json, { error: 'unauthorized' });
  } finally {
    b.cleanup();
  }
});

test('a non-healthz route 401s with the wrong bearer token', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'GET', `/sessions/${b.sid}/status`, { token: 'wrong-token' });
    assert.equal(r.status, 401);
  } finally {
    b.cleanup();
  }
});

test('the right bearer token authorizes a GET (200 + protocol version)', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'GET', `/sessions/${b.sid}/status`, { token: b.token });
    assert.equal(r.status, 200);
    const body = r.json as Record<string, unknown>;
    assert.equal(body.id, b.sid);
    assert.equal(body.status, 'idle');
    assert.equal(body.protocolVersion, PROTOCOL_VERSION);
  } finally {
    b.cleanup();
  }
});

// --- 404s ------------------------------------------------------------------

test('an unknown route 404s (authed)', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'GET', '/nope', { token: b.token });
    assert.equal(r.status, 404);
    assert.deepEqual(r.json, { error: 'not found' });
  } finally {
    b.cleanup();
  }
});

test('a wrong session id 404s "session not found" (no cross-session leak)', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'GET', '/sessions/some-other-id/status', { token: b.token });
    assert.equal(r.status, 404);
    assert.deepEqual(r.json, { error: 'session not found' });
  } finally {
    b.cleanup();
  }
});

// --- POST /turns 409 gate + happy path ------------------------------------

test('POST /turns accepts a first valid turn (200 + turnId, agent invoked), then 409s a concurrent second turn', { skip }, async () => {
  const b = await boot(); // claude-sdk, stub agent whose runTurn never resolves
  try {
    const first = await req(b.port, 'POST', `/sessions/${b.sid}/turns`, {
      token: b.token,
      body: JSON.stringify({ prompt: 'hello' }),
    });
    assert.equal(first.status, 200);
    assert.deepEqual(first.json, { turnId: 'turn-1' });
    // The route delegated to the agent with the prompt (fire-and-forget).
    assert.equal(b.runTurnCalls.length, 1);
    assert.equal(b.runTurnCalls[0][2], 'hello', 'prompt forwarded to agent.runTurn');

    // The first turn is still registered (stub never finishes) → R4 gate.
    const second = await req(b.port, 'POST', `/sessions/${b.sid}/turns`, {
      token: b.token,
      body: JSON.stringify({ prompt: 'again' }),
    });
    assert.equal(second.status, 409);
    assert.match((second.json as { error: string }).error, /a turn is already active/);
    assert.equal(b.runTurnCalls.length, 1, 'the rejected turn did not reach the agent');
  } finally {
    b.cleanup();
  }
});

test('POST /turns 409s on an opencode-server synthetic-busy session (no registered turn)', { skip }, async () => {
  // B2: an interactive opencode turn surfaces only as status:busy via the observer;
  // a headless POST must be rejected or two prompts drive one opencode session.
  const b = await boot({ backend: 'opencode-server', status: 'busy' });
  try {
    const r = await req(b.port, 'POST', `/sessions/${b.sid}/turns`, {
      token: b.token,
      body: JSON.stringify({ prompt: 'hi' }),
    });
    assert.equal(r.status, 409);
    assert.match((r.json as { error: string }).error, /opencode session is busy/);
  } finally {
    b.cleanup();
  }
});

test('POST /turns 409s a supervise-only backend (no Agent)', { skip }, async () => {
  const b = await boot({ backend: 'supervise-only', agent: null });
  try {
    const r = await req(b.port, 'POST', `/sessions/${b.sid}/turns`, {
      token: b.token,
      body: JSON.stringify({ prompt: 'hi' }),
    });
    assert.equal(r.status, 409);
    assert.match((r.json as { error: string }).error, /does not accept runner turns/);
  } finally {
    b.cleanup();
  }
});

test('POST /turns to a wrong session id 404s before the gate', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'POST', '/sessions/other/turns', {
      token: b.token,
      body: JSON.stringify({ prompt: 'hi' }),
    });
    assert.equal(r.status, 404);
    assert.deepEqual(r.json, { error: 'session not found' });
  } finally {
    b.cleanup();
  }
});

// --- typed body errors (B9) + validation ----------------------------------

test('POST /turns with a valid JSON body but no prompt 400s "prompt is required"', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'POST', `/sessions/${b.sid}/turns`, {
      token: b.token,
      body: JSON.stringify({ notPrompt: 1 }),
    });
    assert.equal(r.status, 400);
    assert.deepEqual(r.json, { error: 'prompt is required' });
  } finally {
    b.cleanup();
  }
});

test('B9: malformed JSON body maps to 400 (InvalidJsonError), not 500', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'POST', `/sessions/${b.sid}/turns`, {
      token: b.token,
      body: '{not valid json',
    });
    assert.equal(r.status, 400);
    assert.equal((r.json as { error: string }).error, 'invalid JSON body');
  } finally {
    b.cleanup();
  }
});

// §10 (was B9 finding): the 413 mapping in server.ts's createRunnerServer catch
// now actually reaches the client. readBody previously called `req.destroy()`
// SYNCHRONOUSLY right after `reject(...)`; the catch that writes the 413 runs as
// a microtask AFTER that sync frame, so the shared request/response socket was
// already torn down and the client saw ECONNRESET, not the JSON. The fix stops
// destroying in readBody (httputil.ts) and lets the route's error mapping respond
// while remaining inbound bytes drain — so the mapped 413 flushes. This test now
// asserts the clean 413 body arrives (the typed BodyTooLargeError itself is also
// unit-covered in robustness-b5-b9.test.ts).
test('§10: an oversized body is rejected with the mapped 413 body, which reaches the client', { skip }, async () => {
  const b = await boot();
  try {
    const big = 'x'.repeat((1 << 20) + 1024); // just over the 1 MiB cap
    const resp = await req(b.port, 'POST', `/sessions/${b.sid}/turns`, {
      token: b.token,
      body: JSON.stringify({ prompt: big }),
    });
    assert.equal(resp.status, 413);
    assert.deepEqual(resp.json, { error: 'request body too large' });
    // The upload never reached the agent — the point of the size cap holds.
    assert.equal(b.runTurnCalls.length, 0);
  } finally {
    b.cleanup();
  }
});

// --- SSE replay + B5 clamp -------------------------------------------------

test('GET /events?after=N replays seq > N contiguously in order, then live events flow', { skip }, async () => {
  const b = await boot();
  try {
    // Seed history seq 1..4 in the real temp log.
    for (let i = 0; i < 4; i++) appendEvent(b.sid, 'turn-1', T('message.delta'), { i });

    const sse = openSse(b.port, `/sessions/${b.sid}/events?after=2`, b.token);
    try {
      await waitFor(() => sse.hasReplayComplete());
      // Replay delivers only seq > 2, contiguous and in order.
      assert.deepEqual(sse.events().map((e) => e.seq), [3, 4]);
      // replay-complete marks the boundary after all history.
      const wire = sse.wire();
      assert.ok(wire.indexOf(': replay-complete') > wire.lastIndexOf('data: '));

      // A live event after the handoff flows through, in order.
      appendEvent(b.sid, 'turn-1', T('turn.completed'), { done: true });
      await waitFor(() => sse.events().length === 3);
      assert.deepEqual(sse.events().map((e) => e.seq), [3, 4, 5]);
      assert.deepEqual(sse.events()[2].payload, { done: true });
    } finally {
      sse.close();
    }
  } finally {
    b.cleanup();
  }
});

test('GET /events?after=0 replays the whole log from seq 1', { skip }, async () => {
  const b = await boot();
  try {
    for (let i = 0; i < 3; i++) appendEvent(b.sid, 'turn-1', T('message.delta'), { i });
    const sse = openSse(b.port, `/sessions/${b.sid}/events?after=0`, b.token);
    try {
      await waitFor(() => sse.hasReplayComplete());
      assert.deepEqual(sse.events().map((e) => e.seq), [1, 2, 3]);
    } finally {
      sse.close();
    }
  } finally {
    b.cleanup();
  }
});

test('B5: an `after` cursor beyond the head is clamped — live events still flow (not silently swallowed)', { skip }, async () => {
  const b = await boot();
  try {
    // Head is at seq 3; request a bogus cursor far beyond it.
    for (let i = 0; i < 3; i++) appendEvent(b.sid, 'turn-1', T('message.delta'), { i });
    const sse = openSse(b.port, `/sessions/${b.sid}/events?after=999`, b.token);
    try {
      await waitFor(() => sse.hasReplayComplete());
      // Nothing to replay (clamped to head=3, no seq > 3 yet).
      assert.equal(sse.events().length, 0);
      // Without the B5 clamp, afterSeq would stay 999 and this live event (seq 4)
      // would be dropped by shouldDeliver — the stream would look frozen.
      appendEvent(b.sid, 'turn-1', T('turn.completed'), { live: true });
      await waitFor(() => sse.events().length === 1);
      assert.equal(sse.events()[0].seq, 4);
      assert.deepEqual(sse.events()[0].payload, { live: true });
    } finally {
      sse.close();
    }
  } finally {
    b.cleanup();
  }
});

test('R8: a non-integer/negative `after` 400s', { skip }, async () => {
  const b = await boot();
  try {
    const bad = await req(b.port, 'GET', `/sessions/${b.sid}/events?after=abc`, { token: b.token });
    assert.equal(bad.status, 400);
    assert.match((bad.json as { error: string }).error, /non-negative integer/);
    const neg = await req(b.port, 'GET', `/sessions/${b.sid}/events?after=-5`, { token: b.token });
    assert.equal(neg.status, 400);
  } finally {
    b.cleanup();
  }
});

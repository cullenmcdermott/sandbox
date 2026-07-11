// End-to-end coverage of the PUT/DELETE /sessions/:id/autopilot endpoint + the
// /status capability bit, booting the REAL createRunnerServer on an ephemeral
// port with a real autopilot driver (createAutopilot) wired to a real temp event
// log. Mirrors server-http.test.ts's harness. Only Agent.runTurn is faked (a
// never-resolving stub, so an armed driver's immediate self-submit stays "active"
// without a backend) and saveSessionState is redirected to a temp file.
//
// GUARD: SKIPS cleanly when better-sqlite3's native addon is unavailable, UNLESS
// RUNNER_REQUIRE_SQLITE=1 (CI), matching the other real-server suites.

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
import { __setEventLogForTest } from '../src/events.js';
import { setTurnSettledHandler } from '../src/turns.js';
import { createAutopilot } from '../src/autopilot.js';
import type { SessionState } from '../src/types.js';
import type { Agent } from '../src/agent.js';

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

interface BootOpts {
  backend?: string;
  /** Wire a real autopilot driver (claude-backend). Defaults to true. */
  withDriver?: boolean;
}

interface Booted {
  port: number;
  token: string;
  sid: string;
  cleanup: () => void;
}

async function boot(opts: BootOpts = {}): Promise<Booted> {
  const token = 'secret-token';
  const sid = 'sess-ap';
  const backend = opts.backend ?? 'claude-sdk';
  process.env.RUNNER_TOKEN = token;
  process.env.SANDBOX_SESSION_ID = sid;
  process.env.SANDBOX_BACKEND = backend;
  process.env.PROJECT_PATH = '/work/proj';

  const dir = mkdtempSync(join(tmpdir(), 'autopilot-ep-'));
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
    status: 'idle',
    claude_session_id: '',
    opencode_session_id: '',
    last_turn_id: '',
    last_activity: new Date().toISOString(),
  };
  initRegistry(state);

  const cfg = loadConfig();
  // Never-resolving stub so an armed driver's immediate self-submit stays active.
  const agent: Agent = { runTurn: () => new Promise<void>(() => {}) };
  const withDriver = opts.withDriver ?? true;
  const autopilot = withDriver ? createAutopilot(cfg, agent) : null;

  const server = createRunnerServer(cfg, agent, autopilot);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;

  return {
    port,
    token,
    sid,
    cleanup(): void {
      server.closeAllConnections();
      server.close();
      setTurnSettledHandler(null); // don't leak this driver's handler across tests
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
  json: unknown;
}

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
    const request = http.request({ host: '127.0.0.1', port, method, path, headers }, (res) => {
      const chunks: Buffer[] = [];
      res.on('data', (c: Buffer) => chunks.push(c));
      res.on('end', () => {
        const text = Buffer.concat(chunks).toString('utf8');
        let json: unknown;
        try {
          json = text ? JSON.parse(text) : undefined;
        } catch {
          /* leave undefined */
        }
        resolve({ status: res.statusCode ?? 0, json });
      });
    });
    request.on('error', reject);
    if (o.body !== undefined) request.write(o.body);
    request.end();
  });
}

/** Open a live SSE reader that collects parsed `data:` frames. */
function openSse(port: number, sid: string, token: string): {
  events: () => Array<Record<string, unknown>>;
  close: () => void;
} {
  const chunks: string[] = [];
  const request = http.request(
    { host: '127.0.0.1', port, method: 'GET', path: `/sessions/${sid}/events?after=0`, headers: { authorization: `Bearer ${token}` } },
    (res) => {
      res.setEncoding('utf8');
      res.on('data', (c: string) => chunks.push(c));
    },
  );
  request.on('error', () => {});
  request.end();
  return {
    events: () =>
      chunks
        .join('')
        .split('\n\n')
        .filter((f) => f.startsWith('data: '))
        .map((f) => JSON.parse(f.slice('data: '.length)) as Record<string, unknown>),
    close: () => request.destroy(),
  };
}

async function waitFor(pred: () => boolean, timeoutMs = 2000): Promise<void> {
  const start = Date.now();
  while (!pred()) {
    if (Date.now() - start > timeoutMs) throw new Error('waitFor: timed out');
    await new Promise((r) => setTimeout(r, 5));
  }
}

const PUT_BODY = JSON.stringify({ kind: 'loop', prompt: 'burn down TODO.md', sentinel: 'ALL_DONE' });

// --- capability bit --------------------------------------------------------

test('GET /status carries capabilities.autopilot=true for claude-sdk', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'GET', `/sessions/${b.sid}/status`, { token: b.token });
    assert.equal(r.status, 200);
    assert.deepEqual((r.json as { capabilities: unknown }).capabilities, { autopilot: true });
  } finally {
    b.cleanup();
  }
});

test('GET /status carries capabilities.autopilot=false for opencode-server', { skip }, async () => {
  const b = await boot({ backend: 'opencode-server', withDriver: false });
  try {
    const r = await req(b.port, 'GET', `/sessions/${b.sid}/status`, { token: b.token });
    assert.equal(r.status, 200);
    assert.deepEqual((r.json as { capabilities: unknown }).capabilities, { autopilot: false });
  } finally {
    b.cleanup();
  }
});

// --- auth + 404 ------------------------------------------------------------

test('PUT /autopilot 401s with no bearer token', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'PUT', `/sessions/${b.sid}/autopilot`, { body: PUT_BODY });
    assert.equal(r.status, 401);
  } finally {
    b.cleanup();
  }
});

test('PUT /autopilot to a wrong session id 404s', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'PUT', '/sessions/other/autopilot', { token: b.token, body: PUT_BODY });
    assert.equal(r.status, 404);
    assert.deepEqual(r.json, { error: 'session not found' });
  } finally {
    b.cleanup();
  }
});

// --- no-driver backend 409 -------------------------------------------------

test('PUT /autopilot 409s on a backend without a runner-side driver', { skip }, async () => {
  const b = await boot({ backend: 'opencode-server', withDriver: false });
  try {
    const r = await req(b.port, 'PUT', `/sessions/${b.sid}/autopilot`, { token: b.token, body: PUT_BODY });
    assert.equal(r.status, 409);
    assert.match((r.json as { error: string }).error, /no runner-side autopilot driver/);
  } finally {
    b.cleanup();
  }
});

// --- body validation (400s) ------------------------------------------------

test('PUT /autopilot validation: typed 400s for bad bodies', { skip }, async () => {
  const b = await boot();
  try {
    const cases: Array<[Record<string, unknown>, RegExp]> = [
      [{ prompt: 'p' }, /kind must be/],
      [{ kind: 'nope', prompt: 'p' }, /kind must be/],
      [{ kind: 'loop' }, /prompt is required/],
      [{ kind: 'loop', prompt: 'p', intervalMs: -1 }, /intervalMs/],
      [{ kind: 'loop', prompt: 'p', maxIterations: 0 }, /maxIterations/],
      [{ kind: 'loop', prompt: 'p', tokenBudget: -5 }, /tokenBudget/],
    ];
    for (const [body, re] of cases) {
      const r = await req(b.port, 'PUT', `/sessions/${b.sid}/autopilot`, {
        token: b.token,
        body: JSON.stringify(body),
      });
      assert.equal(r.status, 400, `expected 400 for ${JSON.stringify(body)}`);
      assert.match((r.json as { error: string }).error, re);
    }
  } finally {
    b.cleanup();
  }
});

// --- arm + disarm happy path ----------------------------------------------

test('PUT /autopilot arms (200 + emits armed), DELETE disarms (200 + emits stopped(user))', { skip }, async () => {
  const b = await boot();
  const sse = openSse(b.port, b.sid, b.token);
  try {
    const put = await req(b.port, 'PUT', `/sessions/${b.sid}/autopilot`, { token: b.token, body: PUT_BODY });
    assert.equal(put.status, 200);
    assert.deepEqual((put.json as { capabilities: unknown }).capabilities, { autopilot: true });
    await waitFor(() => sse.events().some((e) => e.type === 'autopilot.state' && (e.payload as { state: string }).state === 'armed'));
    const armed = sse.events().find((e) => e.type === 'autopilot.state');
    assert.deepEqual(armed?.payload, { state: 'armed', kind: 'loop', iteration: 0, gen: 1 });

    const idle = await req(b.port, 'GET', `/sessions/${b.sid}/idle`, { token: b.token });
    assert.equal((idle.json as { turnActive: boolean }).turnActive, true, 'armed → non-idle');

    const del = await req(b.port, 'DELETE', `/sessions/${b.sid}/autopilot`, { token: b.token });
    assert.equal(del.status, 200);
    await waitFor(() =>
      sse.events().some((e) => e.type === 'autopilot.state' && (e.payload as { state: string }).state === 'stopped'),
    );
    const stopped = sse.events().find((e) => e.type === 'autopilot.state' && (e.payload as { state: string }).state === 'stopped');
    assert.equal((stopped?.payload as { reason: string }).reason, 'user');
    assert.equal((stopped?.payload as { gen: number }).gen, 2, 'disarm bumped gen');
  } finally {
    sse.close();
    b.cleanup();
  }
});

test('DELETE /autopilot 404s when the driver was never armed', { skip }, async () => {
  const b = await boot();
  try {
    const r = await req(b.port, 'DELETE', `/sessions/${b.sid}/autopilot`, { token: b.token });
    assert.equal(r.status, 404);
    assert.match((r.json as { error: string }).error, /no autopilot spec to disarm/);
  } finally {
    b.cleanup();
  }
});

// HTTP + SSE server. Plain node:http (no framework deps). Bearer token auth
// on every non-healthz route. Routes per ./docs/runner-api.md.
//
// One sandbox = one pod = one session. The session id comes from env
// (SANDBOX_SESSION_ID); /sessions and /sessions/:id all address that single
// session. The id in the path is validated against the configured session so
// a wrong id returns 404 rather than cross-session leakage.

import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { readBody } from './httputil.js';
import { appendEvent, attachSseClient, lastSeq } from './events.js';
import { getRegistry, loadConfig, toStatusResponse } from './session.js';
import { PORT } from './types.js';
import { runTurn } from './claude.js';

// --- Auth -----------------------------------------------------------------

function authOk(req: IncomingMessage): boolean {
  const cfg = loadConfig();
  const expected = cfg.runnerToken;
  if (!expected) return false; // no token configured => reject all non-healthz
  const header = req.headers['authorization'];
  if (!header || typeof header !== 'string') return false;
  const m = /^Bearer\s+(.+)$/.exec(header);
  if (!m) return false;
  // Constant-time-ish comparison.
  const got = m[1];
  if (got.length !== expected.length) return false;
  let diff = 0;
  for (let i = 0; i < got.length; i++) diff |= got.charCodeAt(i) ^ expected.charCodeAt(i);
  return diff === 0;
}

function unauthorized(res: ServerResponse): void {
  res.writeHead(401, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ error: 'unauthorized' }));
}

function notFound(res: ServerResponse, msg = 'not found'): void {
  res.writeHead(404, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ error: msg }));
}

function badRequest(res: ServerResponse, msg: string): void {
  res.writeHead(400, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ error: msg }));
}

function ok(res: ServerResponse, body: unknown, status = 200): void {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(body));
}

// --- Router ---------------------------------------------------------------

export function startServer(): void {
  const cfg = loadConfig();
  const server = createServer((req, res) => {
    handle(req, res, cfg).catch((err) => {
      const message = err instanceof Error ? err.message : String(err);
      if (!res.headersSent) {
        res.writeHead(500, { 'Content-Type': 'application/json' });
      }
      if (!res.writableEnded) res.end(JSON.stringify({ error: message }));
    });
  });
  server.listen(PORT, () => {
    console.log(`runner listening on :${PORT} (session=${cfg.sessionId})`);
  });
}

async function handle(req: IncomingMessage, res: ServerResponse, cfg: ReturnType<typeof loadConfig>): Promise<void> {
  const url = new URL(req.url ?? '/', `http://localhost:${PORT}`);
  const path = url.pathname;
  const method = req.method ?? 'GET';

  // healthz: no auth.
  if (path === '/healthz' && method === 'GET') {
    return ok(res, { status: 'ok' });
  }

  if (!authOk(req)) return unauthorized(res);

  const reg = getRegistry();
  const sid = cfg.sessionId;

  // All /sessions* routes address the single configured session.
  if (path === '/sessions' && method === 'GET') {
    return ok(res, [toStatusResponse(reg.state)]);
  }

  // /sessions/:id
  const sessMatch = /^\/sessions\/([^/]+)$/.exec(path);
  if (sessMatch && method === 'GET') {
    if (sessMatch[1] !== sid) return notFound(res, 'session not found');
    return ok(res, toStatusResponse(reg.state));
  }

  // /sessions/:id/status
  const statusMatch = /^\/sessions\/([^/]+)\/status$/.exec(path);
  if (statusMatch && method === 'GET') {
    if (statusMatch[1] !== sid) return notFound(res, 'session not found');
    return ok(res, toStatusResponse(reg.state));
  }

  // /sessions/:id/events (SSE)
  const eventsMatch = /^\/sessions\/([^/]+)\/events$/.exec(path);
  if (eventsMatch && method === 'GET') {
    if (eventsMatch[1] !== sid) return notFound(res, 'session not found');
    const afterParam = url.searchParams.get('after');
    const afterSeq = afterParam ? parseInt(afterParam, 10) : lastSeq(sid);
    if (Number.isNaN(afterSeq)) return badRequest(res, 'after must be an integer');
    attachSseClient(res, sid, afterSeq);
    return; // SSE: response stays open
  }

  // /sessions/:id/turns (POST)
  const turnsMatch = /^\/sessions\/([^/]+)\/turns$/.exec(path);
  if (turnsMatch && method === 'POST') {
    if (turnsMatch[1] !== sid) return notFound(res, 'session not found');
    const body = await readBody<{ prompt?: string; resume?: string; allowedTools?: string[] }>(req);
    if (!body || typeof body.prompt !== 'string' || !body.prompt) {
      return badRequest(res, 'prompt is required');
    }
    const turnId = reg.nextTurnId();
    reg.setLastTurn(turnId);
    const turn = reg.registerTurn(turnId, body.prompt);
    // Fire and forget: the turn runs in the background, streaming events to
    // SSE clients. The HTTP response returns immediately with the turnId.
    runTurn(cfg, turnId, body.prompt, body.resume, body.allowedTools, turn.abort).catch((err) => {
      const message = err instanceof Error ? err.message : String(err);
      appendEvent(sid, turnId, 'error', { message });
      reg.finishTurn(turnId);
    });
    return ok(res, { turnId });
  }

  // /sessions/:id/turns/:turn_id/interrupt (POST)
  const interruptMatch = /^\/sessions\/([^/]+)\/turns\/([^/]+)\/interrupt$/.exec(path);
  if (interruptMatch && method === 'POST') {
    if (interruptMatch[1] !== sid) return notFound(res, 'session not found');
    const turnId = interruptMatch[2];
    const turn = reg.activeTurns.get(turnId);
    if (!turn) return notFound(res, 'turn not found or not active');
    turn.abort.abort();
    appendEvent(sid, turnId, 'turn.interrupted', { reason: 'client interrupt' });
    return ok(res, { turnId }, 200);
  }

  // /sessions/:id/permissions/:permission_id (POST)
  const permMatch = /^\/sessions\/([^/]+)\/permissions\/([^/]+)$/.exec(path);
  if (permMatch && method === 'POST') {
    if (permMatch[1] !== sid) return notFound(res, 'session not found');
    const permissionId = permMatch[2];
    const pending = reg.resolvePermission(permissionId);
    if (!pending) return notFound(res, 'permission request not found');
    const body = await readBody<{ allow?: boolean; scope?: string; editedInput?: string }>(req);
    if (!body || typeof body.allow !== 'boolean') {
      return badRequest(res, 'allow (boolean) is required');
    }
    pending.resolve(body.allow, body.scope ?? 'once', body.editedInput);
    return ok(res, { permissionId, resolved: true });
  }

  return notFound(res);
}

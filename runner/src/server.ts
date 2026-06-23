// HTTP + SSE server. Plain node:http (no framework deps). Bearer token auth
// on every non-healthz route. Routes per ./docs/runner-api.md.
//
// One sandbox = one pod = one session. The session id comes from env
// (SANDBOX_SESSION_ID); /sessions and /sessions/:id all address that single
// session. The id in the path is validated against the configured session so
// a wrong id returns 404 rather than cross-session leakage.

import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { readBody } from './httputil.js';
import { appendEvent, attachSseClient, lastSeq, sseTotalClientCount, MAX_SSE_CLIENTS } from './events.js';
import { getRegistry, loadConfig, toStatusResponse } from './session.js';
import { PORT, type PermissionRequestBody, type TurnRequestBody, type TurnResponse, type ExecRequestBody } from './types.js';
import { selectAgent, type Agent } from './agent.js';
import { runExec } from './exec.js';
import { appendAudit } from './audit.js';
import { bashCommandBlocked } from './guards.js';
import { bearerTokenOk } from './auth.js';

// --- Auth -----------------------------------------------------------------

// Thin wrapper binding the configured runner token to the pure (sqlite-free)
// bearer-token check in ./auth.ts so the comparison logic stays unit-testable
// without the native sqlite addon.
function authOk(req: IncomingMessage): boolean {
  return bearerTokenOk(loadConfig().runnerToken, req.headers['authorization']);
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
  // Resolve the agent backend up front so an unknown SANDBOX_BACKEND fails at
  // startup rather than on the first turn. May be null for backends not driven
  // through the runner turn path (opencode-server); the /turns route 409s then.
  const agent = selectAgent(cfg.backend);
  const server = createServer((req, res) => {
    handle(req, res, cfg, agent).catch((err) => {
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

async function handle(req: IncomingMessage, res: ServerResponse, cfg: ReturnType<typeof loadConfig>, agent: Agent | null): Promise<void> {
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

  // /sessions/:id/idle — idle state for the reaper (turn-done AND detached).
  const idleMatch = /^\/sessions\/([^/]+)\/idle$/.exec(path);
  if (idleMatch && method === 'GET') {
    if (idleMatch[1] !== sid) return notFound(res, 'session not found');
    return ok(res, reg.idleStatus());
  }

  // /sessions/:id/events (SSE)
  const eventsMatch = /^\/sessions\/([^/]+)\/events$/.exec(path);
  if (eventsMatch && method === 'GET') {
    if (eventsMatch[1] !== sid) return notFound(res, 'session not found');
    const afterParam = url.searchParams.get('after');
    const afterSeq = afterParam ? parseInt(afterParam, 10) : lastSeq(sid);
    // R8: reject non-integers (NaN) and negatives; parseInt("-5") → -5 not NaN.
    if (Number.isNaN(afterSeq) || afterSeq < 0) return badRequest(res, 'after must be a non-negative integer');
    // passive=1 marks a status observer (the dashboard's background list stream)
    // that must NOT count as an attached client for idle detection (RV6).
    const passive = url.searchParams.get('passive') === '1';
    // M33: bound concurrent SSE clients so one bad client can't fan-out-DoS.
    // Count ALL clients here (passive observers still hold a connection).
    if (MAX_SSE_CLIENTS > 0 && sseTotalClientCount() >= MAX_SSE_CLIENTS) {
      res.writeHead(429, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'too many concurrent event streams' }));
      return;
    }
    attachSseClient(res, sid, afterSeq, passive);
    return; // SSE: response stays open
  }

  // /sessions/:id/turns (POST)
  const turnsMatch = /^\/sessions\/([^/]+)\/turns$/.exec(path);
  if (turnsMatch && method === 'POST') {
    if (turnsMatch[1] !== sid) return notFound(res, 'session not found');
    if (!agent) {
      // opencode-server: turns are driven by the local `opencode attach`
      // client talking to `opencode serve` directly, not through the runner.
      res.writeHead(409, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: `backend ${cfg.backend} does not accept runner turns` }));
      return;
    }
    // Read the body FIRST, then check-and-reserve the turn slot synchronously.
    // The active-turn check and registerTurn must not be split by an `await`
    // (the only yield, readBody, is now done): otherwise two near-simultaneous
    // POSTs both observe activeTurns.size===0, both await their bodies, and both
    // register — defeating R4's single-active-turn invariant and colliding on the
    // same nextTurnId (TOCTOU).
    const body = await readBody<TurnRequestBody>(req);
    if (!body || typeof body.prompt !== 'string' || !body.prompt) {
      return badRequest(res, 'prompt is required');
    }
    // R4: reject concurrent turns — two overlapping query() calls against one
    // Claude session interleave events. Callers must interrupt the active turn
    // first, then POST a new one. (Synchronous from here through registerTurn.)
    if (reg.activeTurns.size > 0) {
      res.writeHead(409, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'a turn is already active; interrupt it before starting a new one' }));
      return;
    }
    const turnId = reg.nextTurnId();
    reg.setLastTurn(turnId);
    const turn = reg.registerTurn(turnId, body.prompt);
    // Fire and forget: the turn runs in the background, streaming events to
    // SSE clients. The HTTP response returns immediately with the turnId.
    agent.runTurn(cfg, turnId, body.prompt, body.resume, body.allowedTools, body.mode, body.model, turn.abort).catch((err) => {
      const message = err instanceof Error ? err.message : String(err);
      appendEvent(sid, turnId, 'error', { message });
      reg.finishTurn(turnId);
    });
    const turnResp: TurnResponse = { turnId };
    return ok(res, turnResp);
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
    const body = await readBody<PermissionRequestBody>(req);
    if (!body || typeof body.allow !== 'boolean') {
      return badRequest(res, 'allow (boolean) is required');
    }
    // Validate editedInput as JSON at the boundary so a malformed edit can't
    // throw inside the permission resolver and wedge the turn (C8). The
    // permission stays pending, so the client can retry with valid JSON.
    if (body.allow && body.editedInput) {
      try {
        JSON.parse(body.editedInput);
      } catch {
        return badRequest(res, 'editedInput must be valid JSON');
      }
    }
    pending.resolve(body.allow, body.scope ?? 'once', body.editedInput);
    reg.deletePermission(permissionId); // R2: prevent unbounded map growth
    return ok(res, { permissionId, resolved: true });
  }

  // /sessions/:id/exec (POST) — one-shot shell command in the session cwd.
  const execMatch = /^\/sessions\/([^/]+)\/exec$/.exec(path);
  if (execMatch && method === 'POST') {
    if (execMatch[1] !== sid) return notFound(res, 'session not found');
    const body = await readBody<ExecRequestBody>(req);
    if (!body || typeof body.command !== 'string' || !body.command) {
      return badRequest(res, 'command is required');
    }
    const command = body.command;
    const blocked = bashCommandBlocked(command);
    const result = await runExec(command);
    // Audit every exec attempt, mirroring the PostToolUse(Bash) audit so the
    // `!cmd` passthrough is not an unaudited shell escape (O2).
    appendAudit({
      time: new Date().toISOString(),
      session_id: sid,
      turn_id: 'exec',
      tool: 'Exec',
      input: blocked ? { command, blocked: true } : { command },
      exit_code: result.exitCode,
    });
    return ok(res, result);
  }

  return notFound(res);
}

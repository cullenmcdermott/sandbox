// HTTP + SSE server. Plain node:http (no framework deps). Bearer token auth
// on every non-healthz route. Routes per ./docs/runner-api.md.
//
// One sandbox = one pod = one session. The session id comes from env
// (SANDBOX_SESSION_ID); /sessions and /sessions/:id all address that single
// session. The id in the path is validated against the configured session so
// a wrong id returns 404 rather than cross-session leakage.

import { createServer, type IncomingMessage, type ServerResponse, type Server } from 'node:http';
import { type Duplex } from 'node:stream';
import { WebSocketServer, type WebSocket, type RawData } from 'ws';
import { readBody, BodyTooLargeError, InvalidJsonError } from './httputil.js';
import { type ClaudePaneSupervisor, type PaneSocket } from './claude-pane.js';
import { type PaneObserverCore } from './claude-pane-observer.js';
import { appendEvent, attachSseClient, lastSeq, sseTotalClientCount, MAX_SSE_CLIENTS } from './events.js';
import { getRegistry, loadConfig, toStatusResponse } from './session.js';
import { PORT, PROTOCOL_VERSION, type TurnRequestBody, type TurnResponse, type ExecRequestBody } from './types.js';
import { type Agent } from './agent.js';
import { startTurn, turnRejectReason } from './turns.js';
import { traceTurnLink, traceIDFromHeader } from './trace.js';
import { opencodeTurnClient } from './opencode-turn.js';
import { markObservedTurnInterrupted } from './opencode-observer.js';
import { runExec } from './exec.js';
import { appendAudit } from './audit.js';
import { bashCommandBlocked } from './guards.js';
import { bearerTokenOk } from './auth.js';

// Re-exported for the turn-gate unit tests (turn-gate.test.ts), which import it
// from './server.js'. The definition moved to ./turns.ts so the shared
// startTurn path and the autopilot driver reuse the exact same 409 gate.
export { turnRejectReason };

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

/** GET /healthz response body. Unauthenticated, so a CLI can distinguish "no
 * runner listening" from "runner listening but protocol-skewed" before it has
 * a bearer token. Exported (pure, no I/O) so it's unit-testable without
 * spinning up the http server. */
export function healthzBody(): { status: 'ok'; protocolVersion: number } {
  return { status: 'ok', protocolVersion: PROTOCOL_VERSION };
}

/**
 * B5: clamp an SSE `after` cursor to the session's current head (`lastSeq`). A
 * client that requests `after` beyond the real max seq — its own bug, or a log
 * truncated by a pod rebuild that reset the AUTOINCREMENT counter — would
 * otherwise have every live event (seq <= after) silently dropped by
 * attachSseClient's shouldDeliver filter: the stream stays open but never
 * delivers, and the session looks frozen. Clamping to lastSeq makes such a client
 * simply tail live from the true head. A well-behaved `after <= lastSeq` is
 * returned unchanged (normal replay). Pure + exported for unit tests.
 */
export function clampAfterSeq(after: number, lastSeq: number): number {
  return after > lastSeq ? lastSeq : after;
}

// --- WebSocket pane (claude-pane backend) ---------------------------------

/** The outcome of authorizing a `GET /sessions/:id/pane` WebSocket upgrade. */
export type PaneUpgradeOutcome =
  | { ok: true }
  | { ok: false; status: 401 | 404 | 409; message: string };

/**
 * Decide whether a WebSocket upgrade to `path` may proceed. Rules, in order:
 *   - the path must be exactly `/sessions/:id/pane` (else 404);
 *   - the bearer token must be valid (401) — checked BEFORE the session-id match
 *     so a bad token can't probe which session id is live;
 *   - the id must match the configured session (404);
 *   - the backend must be `claude-pane` (409) — no other backend has a pane.
 * Pure/exported so the authorization is unit-testable without a socket.
 */
export function evaluatePaneUpgrade(
  path: string,
  sid: string,
  backend: string,
  authed: boolean,
): PaneUpgradeOutcome {
  const m = /^\/sessions\/([^/]+)\/pane$/.exec(path);
  if (!m) return { ok: false, status: 404, message: 'not found' };
  if (!authed) return { ok: false, status: 401, message: 'unauthorized' };
  if (m[1] !== sid) return { ok: false, status: 404, message: 'session not found' };
  if (backend !== 'claude-pane') {
    return { ok: false, status: 409, message: `backend ${backend} has no interactive pane` };
  }
  return { ok: true };
}

/** Parse a pane text control frame. Currently only a resize:
 * `{"type":"resize","cols":N,"rows":N}`. Returns null for anything invalid so a
 * malformed frame is ignored rather than throwing on the socket. Pure/exported. */
export function parsePaneControl(text: string): { type: 'resize'; cols: number; rows: number } | null {
  let msg: unknown;
  try {
    msg = JSON.parse(text);
  } catch {
    return null;
  }
  if (!msg || typeof msg !== 'object') return null;
  const m = msg as Record<string, unknown>;
  if (m.type !== 'resize') return null;
  const cols = m.cols;
  const rows = m.rows;
  if (typeof cols !== 'number' || typeof rows !== 'number') return null;
  if (!Number.isInteger(cols) || !Number.isInteger(rows) || cols <= 0 || rows <= 0) return null;
  return { type: 'resize', cols, rows };
}

/** Coalesce a `ws` RawData frame into a single Buffer. */
function rawToBuffer(data: RawData): Buffer {
  if (Array.isArray(data)) return Buffer.concat(data);
  if (Buffer.isBuffer(data)) return data;
  return Buffer.from(data as ArrayBuffer);
}

/** Adapt a `ws` WebSocket to the supervisor's minimal PaneSocket seam. Sends are
 * always binary frames (raw PTY bytes); send/close are guarded so a closed
 * socket never throws into the supervisor. bufferedAmount is passed through so
 * the supervisor's backpressure eviction (claude-pane.ts P2) sees real buffered
 * bytes. */
function paneSocketAdapter(ws: WebSocket): PaneSocket {
  return {
    send(data: Buffer): void {
      try {
        ws.send(data, { binary: true });
      } catch {
        /* socket closing */
      }
    },
    close(code?: number, reason?: string): void {
      try {
        ws.close(code, reason);
      } catch {
        /* already closed */
      }
    },
    get bufferedAmount(): number {
      return ws.bufferedAmount;
    },
  };
}

/**
 * Wire the `GET /sessions/:id/pane` WebSocket endpoint onto `server` for a
 * claude-pane session. Runs in noServer mode: the http server's 'upgrade' event
 * authorizes the request (evaluatePaneUpgrade) and either rejects it with a raw
 * HTTP status (before any upgrade) or hands the socket to `ws` and attaches it to
 * the supervisor. Binary frames are raw PTY bytes (both directions); text frames
 * are JSON control (resize). Only wired when a pane supervisor is present.
 */
function attachPaneUpgrade(server: Server, cfg: ReturnType<typeof loadConfig>, pane: ClaudePaneSupervisor): void {
  const wss = new WebSocketServer({ noServer: true });
  server.on('upgrade', (req: IncomingMessage, socket: Duplex, head: Buffer) => {
    const url = new URL(req.url ?? '/', `http://localhost:${PORT}`);
    const outcome = evaluatePaneUpgrade(url.pathname, cfg.sessionId, cfg.backend, authOk(req));
    if (!outcome.ok) {
      // Reject before upgrading: write a minimal HTTP response and destroy.
      socket.write(
        `HTTP/1.1 ${outcome.status} ${httpStatusText(outcome.status)}\r\n` +
          'Connection: close\r\nContent-Length: 0\r\n\r\n',
      );
      socket.destroy();
      return;
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      const adapter = paneSocketAdapter(ws);
      pane.attach(adapter);
      ws.on('message', (data: RawData, isBinary: boolean) => {
        if (isBinary) {
          pane.write(rawToBuffer(data));
          return;
        }
        const control = parsePaneControl(rawToBuffer(data).toString('utf8'));
        if (control) pane.resize(control.cols, control.rows);
      });
      const onGone = (): void => {
        // Only clear if we're still the active socket — a later attach may have
        // preempted us (the supervisor already closed us with 4001), and its own
        // close must not detach the newcomer.
        if (pane.current() === adapter) pane.detachAll();
      };
      ws.on('close', onGone);
      ws.on('error', onGone);
    });
  });
}

/** Reason phrase for the pane-upgrade reject statuses. */
function httpStatusText(status: 401 | 404 | 409): string {
  switch (status) {
    case 401:
      return 'Unauthorized';
    case 404:
      return 'Not Found';
    case 409:
      return 'Conflict';
  }
}

// --- Router ---------------------------------------------------------------

/**
 * Build the runner's node:http server around `handle`, wiring the B9 typed-body
 * error mapping. Does NOT listen — exported (F4) so a test can boot the real
 * router on an ephemeral port with an injected cfg + stub agent, exercising
 * bearer-auth enforcement, route dispatch, the 409 turn gate, and SSE `after=`
 * replay against the same code path production runs. startServer() adds the
 * .listen(PORT); nothing about routing changes.
 */
/** The claude-pane observer's server-side handle: the mapping core plus the
 * scoped ingestion token the provisioned hook scripts authenticate with. */
export interface PaneObserverHandle {
  core: PaneObserverCore;
  token: string;
}

export function createRunnerServer(
  cfg: ReturnType<typeof loadConfig>,
  agent: Agent | null,
  pane: ClaudePaneSupervisor | null = null,
  paneObserver: PaneObserverHandle | null = null,
): Server {
  const server = createServer((req, res) => {
    handle(req, res, cfg, agent, paneObserver).catch((err) => {
      const message = err instanceof Error ? err.message : String(err);
      // B9: readBody's typed rejections are client faults, not server bugs — map
      // an oversized body to 413 and malformed JSON to 400 instead of a blanket
      // 500. Anything else stays a 500.
      const status =
        err instanceof BodyTooLargeError ? 413 : err instanceof InvalidJsonError ? 400 : 500;
      if (!res.headersSent) {
        res.writeHead(status, { 'Content-Type': 'application/json' });
      }
      if (!res.writableEnded) res.end(JSON.stringify({ error: message }));
    });
  });
  // claude-pane sessions add a WebSocket pane endpoint on the same server (the
  // interactive `claude` PTY, driven over GET /sessions/:id/pane). No-op for
  // every other backend (pane is null).
  if (pane) attachPaneUpgrade(server, cfg, pane);
  return server;
}

export function startServer(
  agent: Agent | null,
  onListening?: () => void,
  pane: ClaudePaneSupervisor | null = null,
  paneObserver: PaneObserverHandle | null = null,
): void {
  const cfg = loadConfig();
  const server = createRunnerServer(cfg, agent, pane, paneObserver);
  server.listen(PORT, () => {
    console.log(`runner listening on :${PORT} (session=${cfg.sessionId})`);
    // Fires once the socket is actually accepting, not when listen() was
    // initiated — index.ts closes its boot.listen trace phase here.
    onListening?.();
  });
}

async function handle(req: IncomingMessage, res: ServerResponse, cfg: ReturnType<typeof loadConfig>, agent: Agent | null, paneObserver: PaneObserverHandle | null = null): Promise<void> {
  const url = new URL(req.url ?? '/', `http://localhost:${PORT}`);
  const path = url.pathname;
  const method = req.method ?? 'GET';

  // healthz: no auth.
  if (path === '/healthz' && method === 'GET') {
    return ok(res, healthzBody());
  }

  // claude-pane observer ingestion (hook/statusline forwarders). These run
  // inside the pane child's scrubbed env — which deliberately lacks the runner
  // token — so they authenticate with the pane-observer token minted at
  // provision time, accepted ONLY for these telemetry routes (runner-token
  // callers pass too). Placed before the global auth gate for that reason.
  if (paneObserver && path.startsWith('/observer/claude/') && method === 'POST') {
    const authed =
      bearerTokenOk(paneObserver.token, req.headers['authorization']) || authOk(req);
    if (!authed) return unauthorized(res);
    const body = await readBody<Record<string, unknown>>(req);
    if (path === '/observer/claude/hook') {
      if (body !== null) paneObserver.core.handleHook(body);
      return ok(res, {});
    }
    if (path === '/observer/claude/statusline') {
      if (body !== null) paneObserver.core.handleStatusline(body);
      return ok(res, {});
    }
    return notFound(res);
  }

  if (!authOk(req)) return unauthorized(res);

  const reg = getRegistry();
  const sid = cfg.sessionId;

  // All /sessions* routes address the single configured session.
  if (path === '/sessions' && method === 'GET') {
    return ok(res, [toStatusResponse(reg.state, reg.activeTurnId())]);
  }

  // /sessions/:id
  const sessMatch = /^\/sessions\/([^/]+)$/.exec(path);
  if (sessMatch && method === 'GET') {
    if (sessMatch[1] !== sid) return notFound(res, 'session not found');
    return ok(res, toStatusResponse(reg.state, reg.activeTurnId()));
  }

  // /sessions/:id/status
  const statusMatch = /^\/sessions\/([^/]+)\/status$/.exec(path);
  if (statusMatch && method === 'GET') {
    if (statusMatch[1] !== sid) return notFound(res, 'session not found');
    return ok(res, toStatusResponse(reg.state, reg.activeTurnId()));
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
    const requestedAfter = afterParam ? parseInt(afterParam, 10) : lastSeq(sid);
    // R8: reject non-integers (NaN) and negatives; parseInt("-5") → -5 not NaN.
    if (Number.isNaN(requestedAfter) || requestedAfter < 0) return badRequest(res, 'after must be a non-negative integer');
    // B5: clamp `after` beyond the real head to lastSeq so a bogus cursor tails
    // live from head instead of silently swallowing every live event.
    const afterSeq = clampAfterSeq(requestedAfter, lastSeq(sid));
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
      // A supervise-only backend (no Agent) does not accept runner turns. Both
      // shipping backends do, so this is only reached for a future such backend.
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
    // R4/B2: reject concurrent turns — two overlapping query() calls against one
    // Claude session interleave events, and a headless POST into a busy opencode
    // session drives the same session concurrently. startTurn runs the same 409
    // gate (turnRejectReason) the autopilot driver uses, then reserves the slot
    // synchronously (no await between check and registerTurn) — callers must
    // interrupt the active turn first, then POST a new one.
    const started = startTurn(cfg, agent, body.prompt, {
      ...(body.resume ? { resume: body.resume } : {}),
      ...(body.allowedTools ? { allowedTools: body.allowedTools } : {}),
      ...(body.mode ? { mode: body.mode } : {}),
      ...(body.model ? { model: body.model } : {}),
      ...(body.effort ? { effort: body.effort } : {}),
    });
    if ('rejected' in started) {
      res.writeHead(409, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: started.rejected }));
      return;
    }
    const turnResp: TurnResponse = { turnId: started.turnId };
    // §10 observability: bridge the CLI's connect correlation id (the optional
    // X-Sandbox-Trace-Id header) to the assigned turn id in the pod log, so one
    // grep pivots between the CLI's connect spans and this turn's turn.* spans.
    // No-op unless SANDBOX_TRACE is set AND the client sent a well-formed id.
    traceTurnLink(traceIDFromHeader(req.headers['x-sandbox-trace-id']), started.turnId);
    return ok(res, turnResp);
  }

  // /sessions/:id/turns/:turn_id/interrupt (POST). The turn segment may be EMPTY
  // (note [^/]* not [^/]+): the client doesn't always know the live turn id when
  // the user hits esc — it can fire before StartTurn's response or the first SSE
  // event lands, so the TUI sends an empty segment ("…/turns//interrupt"). When
  // the id is empty or doesn't match an active turn, fall back to the session's
  // sole active turn. R4 guarantees at most one active turn, so "interrupt the
  // active turn" is unambiguous without an id.
  const interruptMatch = /^\/sessions\/([^/]+)\/turns\/([^/]*)\/interrupt$/.exec(path);
  if (interruptMatch && method === 'POST') {
    if (interruptMatch[1] !== sid) return notFound(res, 'session not found');
    const reqTurnId = interruptMatch[2];
    let turnId = reqTurnId;
    let turn = reqTurnId ? reg.activeTurns.get(reqTurnId) : undefined;
    if (!turn && reg.activeTurns.size === 1) {
      [[turnId, turn]] = reg.activeTurns.entries();
    }
    if (!turn) {
      // Interactive opencode turns don't register in reg.activeTurns — the live
      // turn runs inside `opencode serve`, driven by the attached client, and is
      // only mirrored by the passive observer (which sets last_turn_id). So
      // `sandbox cancel` lands here with no matching runner turn; abort the
      // opencode session directly instead of 404ing (Phase 4). The observer's next
      // session.idle then emits turn.completed. Only while a turn is actually
      // live (status busy): aborting an idle opencode session would emit a
      // spurious turn.interrupted for a turn that already finished.
      if (cfg.backend === 'opencode-server' && reg.state.opencode_session_id && reg.state.status === 'busy') {
        const ocId = reg.state.opencode_session_id;
        void opencodeTurnClient().session.abort({ path: { id: ocId } }).catch(() => {});
        const interruptedTurn = turnId || reg.state.last_turn_id;
        markObservedTurnInterrupted(interruptedTurn);
        appendEvent(sid, interruptedTurn || undefined, 'turn.interrupted', { reason: 'client interrupt' });
        return ok(res, { turnId: interruptedTurn }, 200);
      }
      return notFound(res, 'turn not found or not active');
    }
    turn.abort.abort();
    appendEvent(sid, turnId, 'turn.interrupted', { reason: 'client interrupt' });
    return ok(res, { turnId }, 200);
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

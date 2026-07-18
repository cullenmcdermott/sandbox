// Always-on passive metrics observer for the in-pod `codex app-server`.
//
// codex is a supervise-only backend: its turns are driven by the interactive
// `codex` TUI, which the runner never sees. Without this observer the dashboard
// would read a codex session as permanently "idle" with no live status, tokens,
// or tool cards (the same Phase 4 parity gap the opencode observer closes).
//
// The app-server broadcasts JSON-RPC notifications (one message per WS text
// frame) to EVERY connected client, so the runner opens its own loopback ws
// client alongside the TUI and maps those notifications onto the normalized SSE
// channel. It is deliberately much simpler than opencode-observer.ts: codex frames
// each turn explicitly (turn/started … turn/completed), so we don't have to
// synthesize turn boundaries from message shapes.
//
// It is STRICTLY PASSIVE. Server→client REQUESTS (approvals like
// execCommandApproval / applyPatchApproval / item/permissions/requestApproval and
// account/chatgptAuthTokens/refresh) are broadcast to us too, but the interactive
// TUI owns them — so we answer each with a polite JSON-RPC "method not handled"
// error and NEVER auto-approve or auto-refresh anything. Wedging one of those
// would silently strip the human's approval prompt.
//
// The frame-mapping core (createCodexObserverHandler) is split from the WS I/O
// loop so it can be unit-tested with synthetic frames and fake deps — no sockets.

import { appendEvent } from './events.js';
import { getRegistry } from './session.js';
import type { EventType } from './types.js';

// Backoff before reconnecting after the socket closes — `codex app-server`
// restarts ~1s after any exit (codex.ts), so a tight respin would just spam
// connection errors while it boots.
const RECONNECT_BACKOFF_MS = 750;

// The JSON-RPC id we use for our own `initialize` request. Its response carries
// this id and a `result` (no `method`), which the loop recognizes to send the
// follow-up `initialized` notification.
const INITIALIZE_ID = 1;

export interface CodexObserver {
  stop(): Promise<void>;
}

/** The runner bookkeeping the observer drives, abstracted so the mapping core is
 * unit-testable with fakes (the real impl wires these to the session registry +
 * appendEvent). */
export interface CodexObserverDeps {
  nextTurnId(): string;
  setLastTurn(id: string): void;
  setExternalActivity(): void;
  /** Refresh the synthetic-busy staleness clock (optional so lean fake deps stay
   * valid) — a live interactive turn keeps this fresh; a quiet stream lets the
   * reaper reclaim the pod. */
  noteObserverEvent?(): void;
  setStatus(s: 'busy' | 'idle' | 'error'): void;
  /** Append a normalized event to the log/SSE channel. */
  emit(turnId: string | undefined, type: EventType, payload: Record<string, unknown>): void;
  /** Send a raw JSON-RPC frame back to the server (error replies + the
   * `initialized` notification). */
  send(frame: unknown): void;
}

/** A minimally-typed inbound JSON-RPC frame. A server→client REQUEST has both
 * `id` and `method`; a NOTIFICATION has `method` and no `id`; a RESPONSE to one
 * of our requests has `id` and `result`/`error` but no `method`. */
interface RpcFrame {
  id?: unknown;
  method?: string;
  params?: Record<string, unknown>;
  result?: unknown;
  error?: unknown;
}

/** Map a codex ThreadItem to a normalized tool name, or undefined for item types
 * that are not tool calls (agent messages, reasoning, plans — those stream via
 * their own delta notifications we don't mirror). Best-effort: the label is
 * whatever is recoverable from the item, since the schema's ToolPayload.tool is
 * only a display string. */
function codexToolName(item: Record<string, unknown>): string | undefined {
  switch (item.type) {
    case 'commandExecution':
      return 'shell';
    case 'fileChange':
      return 'apply_patch';
    case 'webSearch':
      return 'web_search';
    case 'mcpToolCall':
      return typeof item.tool === 'string' ? `mcp:${String(item.server ?? '')}/${item.tool}` : 'mcpToolCall';
    case 'dynamicToolCall':
      return typeof item.tool === 'string' ? item.tool : 'dynamicToolCall';
    default:
      return undefined; // not a tool item — no tool.* event
  }
}

/** The set of server→client request methods we must decline politely (mirrors
 * ServerRequest in the generated protocol). Kept only for documentation/log
 * clarity — the handler declines ANY frame carrying both `id` and `method`, so a
 * newly-added approval method is auto-declined rather than silently auto-run. */
export const CODEX_SERVER_REQUEST_METHODS = [
  'item/commandExecution/requestApproval',
  'item/fileChange/requestApproval',
  'item/tool/requestUserInput',
  'item/tool/call',
  'item/permissions/requestApproval',
  'mcpServer/elicitation/request',
  'account/chatgptAuthTokens/refresh',
  'attestation/generate',
  'applyPatchApproval',
  'execCommandApproval',
] as const;

/**
 * The observer's frame-mapping core. handle(frame) maps ONE parsed JSON-RPC frame
 * into zero or more normalized events (and, for server requests, a decline reply)
 * via deps. reset() abandons an in-flight cycle when the socket drops mid-turn.
 * Pure of I/O — unit-tested directly with synthetic frames.
 */
export function createCodexObserverHandler(deps: CodexObserverDeps) {
  let activeTurnId: string | undefined;

  const endCycle = (): void => {
    activeTurnId = undefined;
  };

  const startCycle = (): string => {
    const turnId = deps.nextTurnId();
    deps.setLastTurn(turnId); // satisfies the CLI cancel/suspend LastTurnID guard
    deps.setExternalActivity(); // keep the idle reaper from suspending mid-turn
    deps.setStatus('busy');
    deps.emit(turnId, 'turn.started', {}); // no prompt: the TUI owns the input
    activeTurnId = turnId;
    return turnId;
  };

  return {
    /** True while a synthetic turn is open (test/visibility aid). */
    get cycleActive(): boolean {
      return activeTurnId !== undefined;
    },
    /** Abandon an in-flight cycle and return status to idle (socket dropped). */
    reset(): void {
      if (activeTurnId !== undefined) {
        deps.emit(activeTurnId, 'turn.interrupted', { reason: 'codex observer stream ended' });
        deps.setStatus('idle');
        endCycle();
      }
    },
    handle(frame: unknown): void {
      const f = (frame ?? {}) as RpcFrame;

      // Server→client REQUEST (has BOTH id and method): decline politely. The
      // interactive TUI owns every approval/refresh; we must never auto-answer,
      // but we MUST reply so the server doesn't wait on us. -32601 = method not
      // found, the JSON-RPC "I don't handle this" code.
      if (f.id !== undefined && f.id !== null && typeof f.method === 'string') {
        deps.send({
          jsonrpc: '2.0',
          id: f.id,
          error: {
            code: -32601,
            message: 'sandbox runner observer: not handled — answer from the interactive client',
          },
        });
        return;
      }

      // Not a notification (a response to one of our own requests, or garbage): no-op.
      if (typeof f.method !== 'string') return;

      const method = f.method;
      const params = (f.params ?? {}) as Record<string, unknown>;
      deps.noteObserverEvent?.();

      // --- turn lifecycle ---------------------------------------------------
      // codex frames turns explicitly. `turn/started` opens a synthetic turn;
      // `turn/completed` closes it (mapping the terminal status to the matching
      // normalized event — a minimal payload is acceptable per the event schema).
      if (method === 'turn/started') {
        if (activeTurnId === undefined) startCycle();
        return;
      }
      if (method === 'turn/completed') {
        const turnId = activeTurnId ?? startCycle();
        const turn = (params.turn ?? {}) as { status?: string; error?: { message?: string } };
        if (turn.status === 'failed') {
          const message = turn.error?.message ?? 'codex turn failed';
          deps.emit(turnId, 'turn.failed', { message });
          deps.emit(turnId, 'error', { message });
          deps.setStatus('error');
        } else if (turn.status === 'interrupted') {
          deps.emit(turnId, 'turn.interrupted', { reason: 'codex turn interrupted' });
          deps.setStatus('idle');
        } else {
          deps.emit(turnId, 'turn.completed', {});
          deps.setStatus('idle');
        }
        endCycle();
        return;
      }

      // --- token usage ------------------------------------------------------
      // `thread/tokenUsage/updated` carries per-turn (`last`) + cumulative
      // (`total`) TokenUsageBreakdowns. Map the per-turn breakdown to the
      // normalized usage.updated (schema UsagePayload). codex reports no cost or
      // cache-write count, so those are 0.
      if (method === 'thread/tokenUsage/updated') {
        const usage = (params.tokenUsage ?? {}) as { last?: Record<string, number> };
        const last = usage.last ?? {};
        deps.emit(activeTurnId, 'usage.updated', {
          inputTokens: last.inputTokens ?? 0,
          outputTokens: last.outputTokens ?? 0,
          cacheReadTokens: last.cachedInputTokens ?? 0,
          cacheWriteTokens: 0,
          totalCostUsd: 0,
        });
        return;
      }

      // --- tool (item) lifecycle -------------------------------------------
      // `item/started` / `item/completed` bracket a codex ThreadItem. Only tool-
      // shaped items become tool.* events; message/reasoning items are skipped
      // (they arrive as their own delta notifications the observer does not
      // mirror). The item id is the tool_use id so start/complete correlate.
      if (method === 'item/started' || method === 'item/completed') {
        const item = (params.item ?? {}) as Record<string, unknown>;
        const tool = codexToolName(item);
        if (!tool) return; // not a tool item
        const toolUseId = typeof item.id === 'string' ? item.id : undefined;
        if (method === 'item/started') {
          deps.emit(activeTurnId, 'tool.started', { tool, input: item, toolUseId });
        } else {
          const status = item.status;
          const output = typeof item.aggregatedOutput === 'string' ? item.aggregatedOutput : undefined;
          const exitCode = typeof item.exitCode === 'number' ? item.exitCode : undefined;
          if (status === 'failed' || status === 'declined') {
            deps.emit(activeTurnId, 'tool.failed', { tool, output, error: output, exitCode, toolUseId });
          } else {
            deps.emit(activeTurnId, 'tool.completed', { tool, output, exitCode, toolUseId });
          }
        }
        return;
      }

      // --- errors -----------------------------------------------------------
      if (method === 'error') {
        const err = (params.error ?? {}) as { message?: string };
        const message = err.message ?? 'codex error';
        deps.emit(activeTurnId, 'error', { message });
        return;
      }

      // Unknown / unmapped notification (deltas, thread/name, account/*, …): no-op.
    },
  };
}

/** Build CodexObserverDeps from the live session registry + the SSE event log,
 * bound to a `send` that writes a frame to the live ws socket. */
function registryDeps(send: (frame: unknown) => void): CodexObserverDeps {
  return {
    nextTurnId: () => getRegistry().nextTurnId(),
    setLastTurn: (id) => getRegistry().setLastTurn(id),
    setExternalActivity: () => getRegistry().setExternalActivity(),
    noteObserverEvent: () => getRegistry().noteObserverEvent(),
    setStatus: (s) => getRegistry().setStatus(s),
    emit: (turnId, type, payload) => appendEvent(getRegistry().state.sandbox_session_id, turnId, type, payload),
    send,
  };
}

/** The JSON-RPC `initialize` request the observer sends on connect so the server
 * starts broadcasting notifications to it. A passive metrics client: it declares
 * no experimental API and no attestation. */
function initializeFrame(): unknown {
  return {
    jsonrpc: '2.0',
    id: INITIALIZE_ID,
    method: 'initialize',
    params: {
      clientInfo: { name: 'sandbox-runner-observer', title: null, version: '0.1.0' },
      capabilities: { experimentalApi: false, requestAttestation: false },
    },
  };
}

/**
 * Start the always-on codex metrics observer. Fire-and-forget: its own reconnect
 * loop absorbs the `codex app-server` boot delay, so this must not be awaited at
 * runner boot. Returns a handle whose stop() closes the socket and ends the loop
 * within the shutdown grace window.
 */
export function startCodexObserver(env: NodeJS.ProcessEnv = process.env): CodexObserver {
  const port = parseInt(env.CODEX_PORT ?? '8788', 10);
  const url = `ws://127.0.0.1:${port}`;
  let stopped = false;
  let sock: WebSocket | undefined;

  const connect = (): void => {
    if (stopped) return;
    let ws: WebSocket;
    try {
      ws = new WebSocket(url);
    } catch {
      // Constructor threw (bad url shouldn't happen) — retry with backoff.
      scheduleReconnect();
      return;
    }
    sock = ws;
    const send = (frame: unknown): void => {
      try {
        ws.send(JSON.stringify(frame));
      } catch {
        /* socket closing — drop */
      }
    };
    const core = createCodexObserverHandler(registryDeps(send));

    ws.addEventListener('open', () => {
      send(initializeFrame());
    });
    ws.addEventListener('message', (ev: MessageEvent) => {
      if (stopped) return;
      let frame: RpcFrame;
      try {
        const data = typeof ev.data === 'string' ? ev.data : String(ev.data);
        frame = JSON.parse(data) as RpcFrame;
      } catch {
        return; // non-JSON / partial frame — ignore defensively
      }
      // Our initialize response (id matches, carries result/error, no method):
      // acknowledge the handshake with the `initialized` notification, then let
      // the server stream notifications.
      if (frame.id === INITIALIZE_ID && frame.method === undefined) {
        send({ jsonrpc: '2.0', method: 'initialized' });
        return;
      }
      try {
        core.handle(frame);
      } catch (err) {
        console.error('codex observer: frame handling error:', err);
      }
    });
    const onGone = (): void => {
      // The stream ended mid-cycle (server restart) — abandon the synthetic turn
      // so the next cycle starts clean rather than wedging status 'busy'.
      try {
        core.reset();
      } catch {
        /* registry gone during shutdown */
      }
      if (sock === ws) sock = undefined;
      scheduleReconnect();
    };
    ws.addEventListener('close', onGone);
    ws.addEventListener('error', () => {
      // 'error' is followed by 'close'; listening here just prevents an unhandled
      // error. The reconnect is driven by 'close' (onGone).
    });
  };

  const scheduleReconnect = (): void => {
    if (stopped) return;
    setTimeout(connect, RECONNECT_BACKOFF_MS);
  };

  connect();

  return {
    async stop(): Promise<void> {
      stopped = true;
      try {
        sock?.close();
      } catch {
        /* already closed */
      }
    },
  };
}

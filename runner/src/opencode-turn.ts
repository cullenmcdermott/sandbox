// OpenCode one-shot turn adapter.
//
// Bridges the runner's normalized turn API (POST /turns → Agent.runTurn) to the
// in-pod `opencode serve` HTTP API via the official @opencode-ai/sdk, so the
// opencode backend gains the same headless one-shot turn seam as claude-sdk
// (StartTurn → SSE events → reply). This is ADDITIVE: the supervised
// `opencode serve` (opencode.ts) and the interactive `opencode attach` path are
// unchanged — both talk to the same server; the runner now also can.
//
// Event mapping (see docs/archive/opencode-turn-adapter-notes.md):
//   message.part.updated(text)      → message.started / message.delta / message.completed
//   message.part.delta(text)        → message.started / message.delta (streaming channel)
//   message.part.updated(reasoning) → reasoning.started / reasoning.delta / reasoning.completed
//   message.part.updated(tool)      → tool.started / tool.completed / tool.failed
//   message.updated(assistant)      → usage.updated (tokens/cost); error → turn.failed
//   permission.updated              → permission.requested (+ runTurn auto-responds)
//   permission.replied              → permission.resolved
//   session.idle                    → turn.completed
//   session.error                   → turn.failed
//   abort                           → session.abort + turn.interrupted
//
// Permission flow: a headless opencode turn has no interactive responder, so the
// pure mapper queues each permission.updated and runTurn auto-responds via the
// opencode API (default "once" = auto-allow; OPENCODE_AUTO_PERMISSION-tunable).
// Continuity: the opencode session id is persisted (opencode_session_id, the
// opencode analogue of claude_session_id) so turns share history across pod
// restarts; a client-supplied `resume` id overrides it.
//
// Auth: in the pod `opencode serve` requires HTTP Basic auth (user "opencode",
// password = OPENCODE_SERVER_PASSWORD). Unset (e.g. an unsecured local serve in
// tests) → no auth header.

import { createOpencodeClient } from '@opencode-ai/sdk';
import type { Event, Part, AssistantMessage } from '@opencode-ai/sdk';

import type { Agent } from './agent.js';
import { appendEvent } from './events.js';
import { capToolOutput } from './mapping.js';
import { appendAudit } from './audit.js';
import { getRegistry, type RunnerConfig } from './session.js';
import type { EventType } from './types.js';

const DEFAULT_PORT = 4096;

type OpencodeClient = ReturnType<typeof createOpencodeClient>;

/** Emit a normalized event. Injected so the mapper is testable without the log. */
export type Emit = (type: EventType, payload: Record<string, unknown>) => void;

/**
 * Audit a tool execution the mapper observes. Injected (and bound to the current
 * session/turn by the caller, exactly like `emit`) so the pure mapper stays
 * I/O-free and unit-testable — production wires it to appendAudit. This is the
 * opencode analogue of the Claude PostToolUse audit hook (claude.ts): it fires
 * for BOTH the headless /turns seam and the always-on interactive observer, since
 * both drive tool events through createOpencodeTurnMapper.
 */
export type AuditTool = (tool: string, input: unknown) => void;

/**
 * D5: echo the driving prompt as a role:"user" message so a replayed/attached
 * opencode transcript shows the question, not just the answer. turn.started
 * carries the prompt, but the Go transcript reducer does not render that payload
 * as a block — it renders user blocks off message.completed(role:"user"). This
 * mirrors the Claude adapter's user-echo (mapping.ts handleUserMessage), so every
 * client gets the prompt on attach with no TUI special-casing. The reducer
 * dedupes this echo against the optimistic user block a live foreground submit
 * appends, so it does not double-print for interactive turns — it only fills the
 * gap on cold attach/replay. Emitted only by the runner-driven turn adapter; the
 * always-on observer (external opencode client) has no prompt and does not echo.
 */
export function emitOpencodeUserPrompt(emit: Emit, prompt: string): void {
  emit('message.started', { role: 'user', content: prompt });
  emit('message.completed', { role: 'user', content: prompt });
}

/** Build a client for the in-pod opencode server with basic auth when secured. */
export function opencodeTurnClient(env: NodeJS.ProcessEnv = process.env): OpencodeClient {
  const port = parseInt(env.OPENCODE_PORT ?? String(DEFAULT_PORT), 10);
  const headers: Record<string, string> = {};
  const pw = env.OPENCODE_SERVER_PASSWORD;
  if (pw) {
    headers.Authorization = 'Basic ' + Buffer.from(`opencode:${pw}`).toString('base64');
  }
  return createOpencodeClient({ baseUrl: `http://127.0.0.1:${port}`, headers });
}

/** Parse a "providerID/modelID" model id; anything else → undefined (server default). */
export function parseOpencodeModel(model: string | undefined): { providerID: string; modelID: string } | undefined {
  if (!model) return undefined;
  const slash = model.indexOf('/');
  if (slash <= 0 || slash === model.length - 1) return undefined;
  return { providerID: model.slice(0, slash), modelID: model.slice(slash + 1) };
}

export function errToString(err: unknown): string {
  if (!err) return 'unknown error';
  if (typeof err === 'string') return err;
  const e = err as { data?: { message?: string }; message?: string; name?: string };
  // Pull a known message field only — never JSON.stringify the whole object into a
  // client-visible payload: it can throw on a cycle AND leak the Authorization
  // header if the error carries request config.
  return e.data?.message ?? e.message ?? e.name ?? 'opencode error';
}

/** A missing/unknown-session error (the cached opencode session was lost). */
export function isMissingSessionError(err: unknown): boolean {
  const e = err as { status?: number; statusCode?: number; data?: { message?: string }; message?: string } | undefined;
  if (e?.status === 404 || e?.statusCode === 404) return true;
  const msg = (e?.data?.message ?? e?.message ?? '').toLowerCase();
  return /session not found|no such session|unknown session/.test(msg);
}

/**
 * Resolve the opencode session id a turn should continue (the opencode analogue
 * of claude's effectiveResume): a client-supplied resume id wins; otherwise the
 * persisted session head; undefined when neither is set (the first turn creates
 * one). Pure so the precedence is unit-testable.
 */
export function effectiveOpencodeSession(
  clientResume: string | undefined,
  persistedId: string | undefined,
): string | undefined {
  return clientResume || persistedId || undefined;
}

/**
 * Resolve the model id for a turn (the opencode analogue of claude's resolveModel):
 * a per-turn override wins over the session default; empty → undefined (the server
 * picks its free default). Uses `||` (NOT `??`) so an empty-string per-turn model
 * still falls back to the session default — matching claude's precedence exactly.
 */
export function effectiveOpencodeModel(
  turnModel: string | undefined,
  sessionModel: string | undefined,
): string | undefined {
  return turnModel || sessionModel || undefined;
}

/** opencode permission responses ("once" | "always" | "reject") the runner may
 * auto-send. Mirrors PostSessionIdPermissionsPermissionIdData.body.response. */
export type OpencodePermissionResponse = 'once' | 'always' | 'reject';

/**
 * Map an opencode permission response to a normalized permission.resolved
 * decision (schema/events.json PermissionPayload). "once"→allow-once,
 * "always"→allow-session, "reject"→deny; anything else → "" (pending/unknown).
 */
export function mapPermissionDecision(response: string | undefined): string {
  switch (response) {
    case 'once':
      return 'allow-once';
    case 'always':
      return 'allow-session';
    case 'reject':
      return 'deny';
    default:
      return '';
  }
}

/**
 * The response the runner auto-sends for an opencode permission prompt. A
 * headless opencode turn has no interactive responder, so a tool that requires
 * approval would otherwise hang until the deadline. Default "once" (auto-allow
 * for this one use) is justified: the sandbox pod IS the isolation boundary and
 * it matches the claude turn command's bypassPermissions default. Env-tunable
 * (OPENCODE_AUTO_PERMISSION = once | always | reject) for operators who want
 * session-wide grants or a hard deny.
 */
export function autoPermissionResponse(env: NodeJS.ProcessEnv = process.env): OpencodePermissionResponse {
  switch (env.OPENCODE_AUTO_PERMISSION) {
    case 'always':
      return 'always';
    case 'reject':
      return 'reject';
    default:
      return 'once';
  }
}

/** opencode's incremental streaming event. NOT in the SDK's typed `Event` union
 * (the SDK only types the full-text `message.part.updated`), so the mapper casts
 * to this shape to handle it. `field` is the part field being streamed
 * ("text" | "reasoning" | ...); we map only text deltas today. */
interface PartDeltaEvent {
  type: 'message.part.delta';
  properties: {
    delta?: string;
    field?: string;
    messageID?: string;
    partID?: string;
    sessionID?: string;
  };
}

/** Narrow an opencode Event to the untyped `message.part.delta` shape, or
 * undefined if it is some other event. */
function asPartDelta(ev: Event): PartDeltaEvent | undefined {
  const e = ev as unknown as { type?: string };
  return e.type === 'message.part.delta' ? (ev as unknown as PartDeltaEvent) : undefined;
}

// Assistant message ids a runner-driven turn (runTurn) has already consumed. The
// always-on observer holds a SEPARATE SSE connection that can lag; without this it
// would replay a just-finished headless turn's trailing message.updated as a
// phantom synthetic cycle once activeTurns has already dropped to 0 (V43). The
// observer consults wasRunnerConsumedMessage() at cycle start and skips those ids.
// Bounded (evict oldest-first) since it only guards the brief lag window after a
// turn — the live cycle's ids are always the most-recently added.
const RUNNER_CONSUMED_MESSAGE_CAP = 256;
const runnerConsumedMessageIds = new Set<string>();

/** Record an assistant message id a runner-driven turn processed (V43). */
export function markRunnerConsumedMessage(messageId: string): void {
  if (!messageId) return;
  runnerConsumedMessageIds.add(messageId);
  while (runnerConsumedMessageIds.size > RUNNER_CONSUMED_MESSAGE_CAP) {
    const oldest = runnerConsumedMessageIds.values().next().value as string | undefined;
    if (oldest === undefined) break;
    runnerConsumedMessageIds.delete(oldest);
  }
}

/** True when a runner-driven turn already consumed this assistant message id, so a
 * lagging observer must not reopen it as a synthetic interactive cycle (V43). */
export function wasRunnerConsumedMessage(messageId: string): boolean {
  return runnerConsumedMessageIds.has(messageId);
}

/** If `ev` is a message.updated for an assistant message of `ocSession`, claim its
 * id as runner-consumed (V43) so the always-on observer skips it after this turn. */
function noteRunnerConsumedMessage(ev: Event, ocSession: string): void {
  const e = ev as unknown as {
    type?: string;
    properties?: { info?: { id?: string; role?: string; sessionID?: string } };
  };
  if (e.type !== 'message.updated') return;
  const info = e.properties?.info;
  if (info?.role === 'assistant' && info.sessionID === ocSession && info.id) {
    markRunnerConsumedMessage(info.id);
  }
}

type Settled = 'completed' | 'failed' | undefined;

/**
 * Stateful, pure mapper for one turn's opencode event stream. `handle(ev)` maps a
 * single opencode Event to zero or more normalized events via `emit`, filtering to
 * `ocSession`, and returns true once the turn has settled (idle/error). It owns the
 * per-part bookkeeping so started/completed fire exactly once. No I/O — unit-tested
 * directly with synthetic events.
 */
export function createOpencodeTurnMapper(ocSession: string, emit: Emit, audit?: AuditTool) {
  const textStarted = new Set<string>();
  const textCompleted = new Set<string>();
  const textOf = new Map<string, string>();
  // Delta-accumulated text per part, a fallback source for message.completed when
  // the turn ends without a full-text message.part.updated (deltas-only stream).
  const streamedText = new Map<string, string>();
  const reasoningStarted = new Set<string>();
  const reasoningCompleted = new Set<string>();
  // Part ids known (from a message.part.updated(type:reasoning)) to be reasoning
  // parts. opencode streams a ReasoningPart's content in the SAME `.text` field a
  // TextPart uses, so its incremental message.part.delta events also carry
  // field:'text' — indistinguishable from an assistant text delta by field alone.
  // Tracking the ids lets handleTextDelta route them to reasoning.* and keeps the
  // session.idle text-flush from re-emitting the reasoning as a second
  // message.completed (the double-render bug).
  const reasoningParts = new Set<string>();
  const toolStarted = new Set<string>();
  const toolSettled = new Set<string>();
  // messageID → role. opencode streams the user prompt itself as a text part on
  // the user message; message.updated(role) always precedes that message's parts,
  // so we use this to map ONLY assistant parts (not echo the user's prompt back).
  const messageRole = new Map<string, string>();
  // Permission bookkeeping: permissionSeen dedupes a re-sent permission.updated;
  // permissionMeta remembers tool+input so the later permission.replied (which
  // carries only the id + response) can emit a complete permission.resolved;
  // pendingPermissions queues ids the runTurn loop must auto-respond to.
  const permissionSeen = new Set<string>();
  const permissionMeta = new Map<string, { tool: string; input: Record<string, unknown> }>();
  const pendingPermissions: string[] = [];
  let settled: Settled;

  const fail = (message: string) => {
    if (settled) return;
    settled = 'failed';
    emit('turn.failed', { message });
    emit('error', { message });
  };

  const completeTextPart = (partId: string) => {
    // Never complete a reasoning part as an assistant message: its content belongs
    // to reasoning.* only. Guards the session.idle flush loop against a reasoning
    // part that a field:'text' delta mis-registered before its type was known.
    if (reasoningParts.has(partId)) return;
    if (textStarted.has(partId) && !textCompleted.has(partId)) {
      textCompleted.add(partId);
      // textOf is the authoritative cumulative full text from message.part.updated;
      // streamedText is the sum of message.part.delta chunks. Both build toward the
      // SAME final string, so take the longer (= more complete) — never concatenate
      // (that double-counts). This guards the case where a part is announced with an
      // empty/partial update and then filled only by trailing deltas: textOf would
      // be '' (which `??` would NOT fall through, dropping the whole reply).
      const full = textOf.get(partId) ?? '';
      const streamed = streamedText.get(partId) ?? '';
      const content = streamed.length > full.length ? streamed : full;
      emit('message.completed', { role: 'assistant', content });
    }
  };

  // Emit message.started exactly once for an assistant text part (deltas and the
  // full-text update share this guard so started fires before either).
  const ensureTextStarted = (partId: string) => {
    if (!textStarted.has(partId)) {
      textStarted.add(partId);
      emit('message.started', { role: 'assistant', content: '' });
    }
  };

  const mapPart = (part: Part, delta: string | undefined) => {
    if (part.sessionID !== ocSession) return;
    // Only map parts of an assistant message; skip the user prompt's own part and
    // any part whose message role we have not seen yet.
    if (messageRole.get(part.messageID) !== 'assistant') return;
    switch (part.type) {
      case 'text': {
        ensureTextStarted(part.id);
        textOf.set(part.id, part.text);
        // A message.part.updated MAY carry a delta on some opencode builds; the
        // primary streaming channel is the separate message.part.delta event
        // (handleTextDelta). Both emit message.delta — they never co-occur for the
        // same chunk in a real stream, so this can't double-emit.
        if (delta) emit('message.delta', { role: 'assistant', content: delta, delta: true });
        if (part.time?.end) completeTextPart(part.id);
        break;
      }
      case 'reasoning': {
        reasoningParts.add(part.id);
        // If a field:'text' delta for this part arrived before we learned it was a
        // reasoning part, it was mis-registered as an assistant text part. Undo that
        // so the session.idle flush cannot re-emit the reasoning as a trailing
        // message.completed. (In opencode's normal ordering the part is announced via
        // this message.part.updated before any delta, so this is defensive.)
        textStarted.delete(part.id);
        streamedText.delete(part.id);
        textOf.delete(part.id);
        if (!reasoningStarted.has(part.id)) {
          reasoningStarted.add(part.id);
          emit('reasoning.started', {});
        }
        if (delta) emit('reasoning.delta', { content: delta, delta: true });
        if (part.time?.end && !reasoningCompleted.has(part.id)) {
          reasoningCompleted.add(part.id);
          emit('reasoning.completed', { content: part.text });
        }
        break;
      }
      case 'tool': {
        const st = part.state;
        if (st.status !== 'pending' && !toolStarted.has(part.id)) {
          toolStarted.add(part.id);
          // Audit every observed tool execution (once per part, guarded by
          // toolStarted) with its inputs, mirroring the Claude PostToolUse audit.
          // Fires here (not on completion) so a tool the in-agent guardrail
          // BLOCKS — which opencode surfaces as an errored part, never a
          // "completed" one — is still recorded.
          audit?.(part.tool, st.input);
          emit('tool.started', { tool: part.tool, input: st.input, toolUseId: part.callID });
        }
        // Fire the terminal event once: opencode may re-send a completed/error part.
        if ((st.status === 'completed' || st.status === 'error') && !toolSettled.has(part.id)) {
          toolSettled.add(part.id);
          if (st.status === 'completed') {
            // Cap the captured output at the source, same as the claude path
            // (mapping.ts) — an uncapped opencode result would bloat the SQLite
            // log, the SSE stream, and the TUI's ctrl+o expansion alike (H6).
            const output = typeof st.output === 'string' ? capToolOutput(st.output) : st.output;
            emit('tool.completed', { tool: part.tool, output, toolUseId: part.callID });
          } else {
            emit('tool.failed', { tool: part.tool, error: st.error, toolUseId: part.callID });
          }
        }
        break;
      }
    }
  };

  // Map an incremental message.part.delta (the streaming text channel). Gated to
  // assistant text parts of our session, mirroring mapPart so the user prompt's
  // own streamed deltas are never echoed back as assistant content.
  const handleTextDelta = (p: PartDeltaEvent['properties']) => {
    if (p.sessionID !== ocSession) return;
    if (p.field !== 'text') return; // tool/reasoning deltas not mapped yet
    const messageID = p.messageID ?? '';
    if (messageRole.get(messageID) !== 'assistant') return;
    const delta = p.delta;
    if (typeof delta !== 'string' || delta.length === 0) return;
    const partId = p.partID ?? messageID;
    // A ReasoningPart streams its content in `.text` too, so its deltas also carry
    // field:'text'. Once the part is known to be reasoning, route its deltas to
    // reasoning.* — never the assistant text channel — so it is emitted once as
    // reasoning.* only and does not double-render (§2b owns how reasoning is shown).
    if (reasoningParts.has(partId)) {
      emit('reasoning.delta', { content: delta, delta: true });
      return;
    }
    ensureTextStarted(partId);
    streamedText.set(partId, (streamedText.get(partId) ?? '') + delta);
    emit('message.delta', { role: 'assistant', content: delta, delta: true });
  };

  // Map an opencode permission.updated → normalized permission.requested and queue
  // the id for the runTurn loop to auto-respond to. Dedupes a re-sent update.
  const requestPermission = (perm: {
    id: string;
    type?: string;
    title?: string;
    sessionID: string;
    callID?: string;
    metadata?: Record<string, unknown>;
  }) => {
    if (perm.sessionID !== ocSession) return;
    if (permissionSeen.has(perm.id)) return;
    permissionSeen.add(perm.id);
    // opencode has no single "tool name" field on a Permission; type is the
    // closest (e.g. "bash", "edit"). Carry title + metadata + callID as input so
    // the TUI permission modal has something meaningful to show.
    const tool = perm.type || 'tool';
    const input: Record<string, unknown> = {
      ...(perm.title ? { title: perm.title } : {}),
      ...(perm.callID ? { callID: perm.callID } : {}),
      ...(perm.metadata ?? {}),
    };
    permissionMeta.set(perm.id, { tool, input });
    pendingPermissions.push(perm.id);
    emit('permission.requested', { permissionId: perm.id, tool, input, decision: '' });
  };

  // Map an opencode permission.replied → normalized permission.resolved, reusing
  // the tool+input recorded at request time (the reply event carries neither).
  const resolvePermission = (permissionID: string, response: string) => {
    const meta = permissionMeta.get(permissionID);
    emit('permission.resolved', {
      permissionId: permissionID,
      tool: meta?.tool ?? 'tool',
      input: meta?.input ?? {},
      decision: mapPermissionDecision(response),
    });
  };

  return {
    get settled(): Settled {
      return settled;
    },
    fail,
    /**
     * Remove and return the opencode permission ids surfaced since the last call.
     * runTurn drains this each loop iteration and auto-responds via the opencode
     * API (the I/O the pure mapper must not do itself).
     */
    takePendingPermissions(): string[] {
      if (pendingPermissions.length === 0) return [];
      return pendingPermissions.splice(0, pendingPermissions.length);
    },
    /** Map one opencode event; returns true once the turn has settled. */
    handle(ev: Event): boolean {
      // message.part.delta is not in the SDK's typed Event union; handle it via a
      // cast before the typed switch (the streaming text channel).
      const partDelta = asPartDelta(ev);
      if (partDelta) {
        handleTextDelta(partDelta.properties);
        return settled !== undefined;
      }
      switch (ev.type) {
        case 'message.part.updated':
          mapPart(ev.properties.part, ev.properties.delta);
          break;
        case 'permission.updated':
          requestPermission(ev.properties);
          break;
        case 'permission.replied':
          if (ev.properties.sessionID !== ocSession) break;
          resolvePermission(ev.properties.permissionID, ev.properties.response);
          break;
        case 'message.updated': {
          const info = ev.properties.info;
          if (info.sessionID !== ocSession) break;
          messageRole.set(info.id, info.role); // record role so mapPart can filter
          if (info.role !== 'assistant') break;
          const a: AssistantMessage = info;
          // Defensive reads: a partial assistant message.updated must not throw and
          // turn an otherwise-good turn into a spurious failure.
          emit('usage.updated', {
            inputTokens: a.tokens?.input ?? 0,
            outputTokens: a.tokens?.output ?? 0,
            cacheReadTokens: a.tokens?.cache?.read ?? 0,
            cacheWriteTokens: a.tokens?.cache?.write ?? 0,
            totalCostUsd: a.cost ?? 0,
          });
          if (a.error) fail(errToString(a.error));
          break;
        }
        case 'session.error':
          // Strict match (symmetric with session.idle): a sessionID-less or foreign
          // error must not fail our turn.
          if (ev.properties.sessionID !== ocSession) break;
          fail(errToString(ev.properties.error));
          break;
        case 'session.idle':
          if (ev.properties.sessionID !== ocSession) break;
          for (const id of textStarted) completeTextPart(id); // flush any open text
          if (!settled) {
            settled = 'completed';
            emit('turn.completed', {});
          }
          break;
      }
      return settled !== undefined;
    },
  };
}

// Absolute per-turn backstop. If the turn never settles (session.idle never
// arrives — a hung server, a permission-gated tool with no responder, etc.) this
// aborts + fails so the turn can't wedge the session 'busy' forever. Env-tunable.
const DEFAULT_TURN_DEADLINE_MS = 600_000;
function turnDeadlineMs(env: NodeJS.ProcessEnv = process.env): number {
  const v = parseInt(env.OPENCODE_TURN_DEADLINE_MS ?? '', 10);
  return Number.isFinite(v) && v > 0 ? v : DEFAULT_TURN_DEADLINE_MS;
}

/** Retry budget for the session verify/create loops. Injectable so tests can run
 * the exhaustion paths without waiting out 20×500ms of real timers. */
export interface SessionRetryOpts {
  attempts?: number;
  delayMs?: number;
}

// A single in-flight session-CREATE promise shared between warmupOpencodeSession
// and ensureSession so a warmup racing the headless first turn can never create
// two opencode sessions (V21). Whichever call arrives first owns the create; a
// concurrent peer awaits the same promise and adopts its id. Cleared once settled.
let inflightSessionCreate: Promise<string> | undefined;

/**
 * Create (and persist) a fresh opencode session, coalescing concurrent callers so
 * only ONE session is ever created (V21). warmupOpencodeSession and ensureSession's
 * create path both funnel through here: if a session id already landed (a prior
 * create, or a peer's create resolving), that id is adopted instead of creating a
 * second, empty session whose head could clobber the one the first prompt ran in.
 */
export async function establishOpencodeSession(
  client: OpencodeClient,
  signal: AbortSignal,
  reg: ReturnType<typeof getRegistry>,
  opts: SessionRetryOpts = {},
): Promise<string> {
  if (reg.state.opencode_session_id) return reg.state.opencode_session_id;
  if (inflightSessionCreate) return inflightSessionCreate;
  const attempts = opts.attempts ?? 20;
  const delayMs = opts.delayMs ?? 500;
  inflightSessionCreate = (async () => {
    let lastErr: unknown = 'no response';
    for (let attempt = 0; attempt < attempts && !signal.aborted; attempt++) {
      // A peer create (warmup vs first turn) may have landed a session between
      // iterations — adopt it rather than creating another.
      if (reg.state.opencode_session_id) return reg.state.opencode_session_id;
      const res = await client.session
        .create({ body: { title: 'sandbox runner session' } })
        .catch((e: unknown) => {
          lastErr = e;
          return undefined;
        });
      if (res && !res.error && res.data) {
        // Belt-and-braces (V21): re-check the persisted head immediately before
        // committing, so a peer create that landed while our request was in flight
        // wins and this create's (now-orphan) session id is never persisted.
        if (reg.state.opencode_session_id) return reg.state.opencode_session_id;
        reg.setOpencodeSession(res.data.id);
        return res.data.id;
      }
      if (res?.error) lastErr = res.error;
      await new Promise((r) => setTimeout(r, delayMs));
    }
    if (signal.aborted) throw new Error('aborted before opencode session was created');
    throw new Error(`opencode session.create failed: ${errToString(lastErr)}`);
  })();
  try {
    return await inflightSessionCreate;
  } finally {
    inflightSessionCreate = undefined;
  }
}

export async function ensureSession(
  client: OpencodeClient,
  signal: AbortSignal,
  reg: ReturnType<typeof getRegistry>,
  resume: string | undefined,
  opts: SessionRetryOpts = {},
): Promise<string> {
  // Continuity (mirrors claude's effectiveResume): a client-supplied resume id
  // wins; otherwise continue the persisted opencode session head so turns share
  // history across pod restarts.
  let lastErr: unknown = 'no response';
  const attempts = opts.attempts ?? 20;
  const delayMs = opts.delayMs ?? 500;
  const existing = effectiveOpencodeSession(resume, reg.state.opencode_session_id);
  if (existing) {
    // VERIFY the session is reachable AND still exists before using it — do NOT
    // return it blind. After a suspend/resume the runner is healthy before
    // `opencode serve` (a child process) finishes booting, so an immediate prompt
    // races the boot and fails with "fetch failed" (the first opencode turn after
    // every resume). Probe session.get with the same retry budget as the create
    // path below to absorb that boot delay. A missing-session (404) means the
    // server lost the session (data gone) → clear it and fall through to create a
    // fresh one; a connection error means it is still booting → retry.
    let missing = false; // set true ONLY on a confirmed missing-session 404
    for (let attempt = 0; attempt < attempts && !signal.aborted; attempt++) {
      const res = await client.session.get({ path: { id: existing } }).catch((e: unknown) => {
        lastErr = e;
        return undefined;
      });
      if (res && !res.error && res.data) {
        reg.setOpencodeSession(existing);
        return existing;
      }
      if (res?.error) {
        lastErr = res.error;
        if (isMissingSessionError(res.error)) {
          reg.clearOpencodeSession(); // session gone → recreate below
          missing = true;
          break;
        }
      }
      await new Promise((r) => setTimeout(r, delayMs));
    }
    if (signal.aborted) throw new Error('aborted before opencode session was verified');
    // Exhausted the verify budget WITHOUT a confirmed 404 (connection errors, a
    // crashed/booting serve, non-404 server errors): the session is unverified, not
    // proven gone. Falling through to create would silently abandon the persisted
    // conversation and start a fresh, history-less session (V20). Fail the turn
    // instead and leave the persisted head untouched so a later turn — once serve
    // is reachable — resumes it. Only a confirmed 404 (missing) recreates below.
    if (!missing) {
      throw new Error(`opencode serve unreachable / session unverified: ${errToString(lastErr)}`);
    }
  }
  // No usable persisted id (first turn, or the persisted one was confirmed gone
  // via 404): create a session and persist it (coalesced with any concurrent
  // warmup so only one session is ever created — V21).
  return establishOpencodeSession(client, signal, reg, { attempts, delayMs });
}

async function runTurn(
  cfg: RunnerConfig,
  turnId: string,
  prompt: string,
  resume: string | undefined,
  _allowedTools: string[] | undefined,
  // The tool-approval policy (Go: TurnInput.ApprovalPolicy) is NOT honored by the
  // opencode backend: the interactive `opencode` client owns its own permission
  // modal, so the runner has no mode to apply. Ignored by contract (see agent.ts),
  // not silently dropped; the param exists only to satisfy the Agent.runTurn seam.
  _mode: string | undefined,
  model: string | undefined,
  // opencode exposes no reasoning-effort knob, so the per-turn /effort override
  // is a no-op here; the param exists only to satisfy the Agent.runTurn seam.
  _effort: string | undefined,
  abort: AbortController,
): Promise<void> {
  const reg = getRegistry();
  const sessionId = reg.state.sandbox_session_id;
  const client = opencodeTurnClient();
  const emit: Emit = (type, payload) => {
    appendEvent(sessionId, turnId, type, payload);
  };
  const audit: AuditTool = (tool, input) => {
    appendAudit({ time: new Date().toISOString(), session_id: sessionId, turn_id: turnId, tool, input });
  };

  emit('turn.started', { prompt });
  emitOpencodeUserPrompt(emit, prompt);

  let stream: AsyncGenerator<Event> | undefined;
  let mapper: ReturnType<typeof createOpencodeTurnMapper> | undefined;
  let deadline: ReturnType<typeof setTimeout> | undefined;
  // Closing the SSE stream is what actually wakes the `for await` loop. Anything
  // that settles the turn out-of-band (prompt failure, deadline, abort) MUST call
  // this, or the loop blocks forever and finishTurn never runs (session wedged).
  const stopStream = () => {
    if (stream?.return) void stream.return(undefined).catch(() => {});
  };

  try {
    const ocSession = await ensureSession(client, abort.signal, reg, resume);
    mapper = createOpencodeTurnMapper(ocSession, emit, audit);

    // Abort → ask opencode to stop AND close the stream so the loop ends even if
    // the abort produces no event. We do NOT emit turn.interrupted here: the
    // /interrupt route in server.ts already emits it (avoid the double event).
    const onAbort = () => {
      void client.session.abort({ path: { id: ocSession } }).catch(() => {});
      stopStream();
    };
    if (abort.signal.aborted) onAbort();
    else abort.signal.addEventListener('abort', onAbort, { once: true });

    // Subscribe BEFORE prompting so the turn's events can't be missed.
    const sub = await client.event.subscribe();
    stream = sub.stream as AsyncGenerator<Event>;

    deadline = setTimeout(() => {
      if (mapper && !mapper.settled && !abort.signal.aborted) {
        mapper.fail(`opencode turn exceeded ${turnDeadlineMs()}ms deadline`);
        void client.session.abort({ path: { id: ocSession } }).catch(() => {});
      }
      stopStream();
    }, turnDeadlineMs());

    // Fire the prompt; completion is driven off the event stream. On a synchronous
    // failure (bad model, server error) settle AND close the stream so the loop
    // ends; if the session id was lost, invalidate the cache so the next turn
    // recreates it instead of failing forever.
    if (!abort.signal.aborted) {
      const promptModel = parseOpencodeModel(effectiveOpencodeModel(model, cfg.model));
      const onPromptError = (why: unknown, label: string) => {
        // A lost session (server restart / GC) clears the persisted head so the
        // next turn recreates it instead of failing forever.
        if (isMissingSessionError(why)) reg.clearOpencodeSession();
        mapper?.fail(`${label}: ${errToString(why)}`);
        stopStream();
      };
      void client.session
        .prompt({
          path: { id: ocSession },
          body: { parts: [{ type: 'text', text: prompt }], ...(promptModel ? { model: promptModel } : {}) },
        })
        .then((r) => {
          if (r.error) onPromptError(r.error, 'opencode prompt failed');
        })
        .catch((e: unknown) => onPromptError(e, 'opencode prompt error'));
    }

    for await (const ev of stream) {
      if (abort.signal.aborted) break;
      // Claim this turn's assistant message ids so the always-on observer, whose
      // separate SSE stream can lag past finishTurn, won't reopen them as a phantom
      // cycle (V43).
      noteRunnerConsumedMessage(ev, ocSession);
      const done = mapper.handle(ev);
      // Auto-respond to any permission surfaced this iteration (the I/O the pure
      // mapper must not do). Default "once" = auto-allow; the pod is the isolation
      // boundary. Fire-and-forget: a failed response can't wedge the turn (the
      // deadline backstops a never-answered prompt) and opencode's permission.replied
      // drives the normalized permission.resolved.
      for (const permissionID of mapper.takePendingPermissions()) {
        void client
          .postSessionIdPermissionsPermissionId({
            path: { id: ocSession, permissionID },
            body: { response: autoPermissionResponse() },
          })
          .catch(() => {});
      }
      if (done) break;
    }

    // On abort, server.ts owns turn.interrupted — emit nothing terminal here.
    if (!mapper.settled && !abort.signal.aborted) {
      mapper.fail('opencode event stream ended before session.idle');
    }
  } catch (e) {
    // Abort path emits nothing (server.ts already emitted turn.interrupted).
    if (!abort.signal.aborted) {
      if (mapper) {
        mapper.fail(`opencode turn error: ${errToString(e)}`);
      } else {
        const message = errToString(e);
        emit('turn.failed', { message });
        emit('error', { message });
      }
    }
  } finally {
    if (deadline) clearTimeout(deadline);
    stopStream();
    reg.finishTurn(turnId);
  }
}

export const opencodeAgent: Agent = { runTurn };

/**
 * Pre-create an opencode session during runner startup so that
 * `opencode attach --continue` finds a valid session immediately and does not
 * fall back to a "dummy" placeholder ID (which OpenCode rejects, showing an
 * error dialog). If a session already exists in state (pod resume), this is a
 * no-op. Non-fatal: the turn path (ensureSession in runTurn) retries on first
 * user prompt if this fails.
 */
export async function warmupOpencodeSession(env: NodeJS.ProcessEnv = process.env): Promise<void> {
  const reg = getRegistry();
  if (reg.state.opencode_session_id) return; // already have a session from a prior pod run

  const client = opencodeTurnClient(env);
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 60_000);
  try {
    // Share the single in-flight create promise with ensureSession (V21): if the
    // headless first turn's ensureSession is already creating a session, adopt it
    // rather than racing a second create whose empty session could win the head.
    const id = await establishOpencodeSession(client, ctrl.signal, reg);
    console.log(`opencode: pre-created session ${id}`);
  } catch (e) {
    console.error('opencode warmup: session pre-create failed; turn path will retry:', e);
  } finally {
    clearTimeout(timer);
  }
}

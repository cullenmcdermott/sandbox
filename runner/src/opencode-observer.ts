// Always-on passive observer of the in-pod `opencode serve` event stream.
//
// The per-turn adapter (opencode-turn.ts runTurn) subscribes to opencode's event
// stream ONLY while a runner-driven POST /turns request is in flight. But the
// actual opencode UX drives turns through the interactive `opencode attach`
// client, which the runner never sees — so without this observer the dashboard
// reads opencode as permanently "idle" with no live title, cost, or tools (the
// Phase 4 parity gap).
//
// This observer subscribes for the WHOLE pod lifetime and frames each interactive
// assistant cycle as a synthetic turn on the runner's normalized SSE channel:
//
//   message.updated(assistant)  → turn.started (+ status busy, last_turn_id)
//   message.part.* / tool.* / …  → message.* / tool.* / usage.updated
//   session.idle                 → turn.completed (+ status idle)
//   session.updated(title)       → session.title
//
// It REUSES createOpencodeTurnMapper PER CYCLE: that mapper already maps every
// per-part/usage/permission event and emits exactly one turn.completed on
// session.idle, then latches. A fresh instance per cycle gives correct multi-turn
// behavior without forking the battle-tested mapper — the observer only adds the
// turn.started bookend, the title/model passthrough, and the runner bookkeeping
// (status, last_turn_id, external-activity) the headless seam normally does.
//
// It is PASSIVE: it never auto-responds to permission prompts. The attached
// `opencode` client owns the permission modal; the observer only mirrors the
// permission.* events as status (draining + discarding the mapper's queue).
//
// The event-mapping core (createObserverHandler) is split from the I/O loop so it
// can be unit-tested with synthetic events and fake deps — no server, no DB.

import type { Event } from '@opencode-ai/sdk';

import { appendEvent } from './events.js';
import { appendAudit } from './audit.js';
import { createOpencodeTurnMapper, errToString, opencodeTurnClient } from './opencode-turn.js';
import { getRegistry } from './session.js';
import type { EventType } from './types.js';

// Backoff before re-subscribing after the stream ends — `opencode serve` restarts
// ~1s after any exit (opencode.ts), so a tight respin would just spam `fetch
// failed` while it boots.
const RECONNECT_BACKOFF_MS = 750;

// The placeholder title the runner creates sessions with (opencode-turn.ts
// ensureSession / warmupOpencodeSession). Never surface it as the agent title.
const PLACEHOLDER_TITLE = 'sandbox runner session';

const interruptedTurns = new Set<string>();

// Upper bound on interruptedTurns. An entry is normally consumed by its own
// turn's session.idle (createObserverHandler) or shed by endCycle; but an
// interrupt marked for an id that NEVER becomes the active cycle (a stale or
// phantom turn id from POST /interrupt) is never consumed and would leak forever.
// The set only ever meaningfully holds the current synthetic turn's id, so a
// small cap is safe: on overflow we evict oldest-first (Set preserves insertion
// order), keeping the most recent — and thus the live cycle's — marker.
const MAX_INTERRUPTED_TURNS = 8;

export function markObservedTurnInterrupted(turnId: string): void {
  if (!turnId) return;
  interruptedTurns.add(turnId);
  while (interruptedTurns.size > MAX_INTERRUPTED_TURNS) {
    const oldest = interruptedTurns.values().next().value as string | undefined;
    if (oldest === undefined) break;
    interruptedTurns.delete(oldest);
  }
}

/** Test-only: whether a turn id is still tracked as interrupted. Lets the GC
 * regression assert reset()/endCycle() shed the entry (the set is module-local
 * and only meaningfully holds the current synthetic turn's id). */
export function hasInterruptedTurn(turnId: string): boolean {
  return interruptedTurns.has(turnId);
}

export interface OpencodeObserver {
  stop(): Promise<void>;
}

/** The runner bookkeeping the observer drives, abstracted so the mapping core is
 * unit-testable with fakes (the real impl wires these to the session registry +
 * appendEvent). */
export interface ObserverDeps {
  /** The sandbox session id appended events are keyed on. */
  sessionId(): string;
  /** The opencode session id to filter events to ("" until warmup resolves it). */
  ocSession(): string;
  /** Non-zero while a runner-driven /turns request owns the stream (suppress). */
  activeTurnsSize(): number;
  nextTurnId(): string;
  setLastTurn(id: string): void;
  setExternalActivity(): void;
  /** Refresh the synthetic-busy staleness clock: called for every observed event
   * that pertains to our session so a live interactive turn stays "active" while
   * a wedged/quiet stream lets the reaper reclaim the pod (optional so existing
   * fake deps stay valid). */
  noteObserverEvent?(): void;
  setStatus(s: 'busy' | 'idle' | 'error'): void;
  setModel(model: string): void;
  /** Append a normalized event to the log/SSE channel. */
  emit(turnId: string | undefined, type: EventType, payload: Record<string, unknown>): void;
  /** Audit a tool execution observed during an interactive cycle (bound to the
   * cycle's turn id). Mirrors the headless seam's appendAudit so interactive
   * opencode tool use is recorded too — the other half of the parity gap. */
  audit(turnId: string, tool: string, input: unknown): void;
}

/** Extract the opencode sessionID an event pertains to, across the few event
 * shapes opencode uses (info.sessionID, part.sessionID, top-level sessionID). */
function eventSessionId(props: Record<string, unknown>): string | undefined {
  const info = props.info as { sessionID?: string } | undefined;
  const part = props.part as { sessionID?: string } | undefined;
  return info?.sessionID ?? part?.sessionID ?? (props.sessionID as string | undefined);
}

/**
 * The observer's event-mapping core. handle(ev) maps one opencode event into zero
 * or more normalized events via deps, framing interactive assistant cycles as
 * synthetic turns. reset() abandons an in-flight cycle (called when the upstream
 * stream drops mid-cycle). Pure of I/O — unit-tested directly with synthetic
 * events.
 */
export function createObserverHandler(deps: ObserverDeps) {
  let activeTurnId: string | undefined;
  let mapper: ReturnType<typeof createOpencodeTurnMapper> | undefined;
  let lastTitle = '';
  let modelEmitted = false;

  const endCycle = () => {
    // GC the interrupt marker for this turn: it is otherwise only shed when the
    // turn's own `session.idle` arrives, so a stream drop (reset) or a non-idle
    // settle in between would leak the module-global entry forever. Covers the
    // reset() stream-drop path (which calls endCycle) and the mapper-settle path.
    if (activeTurnId !== undefined) interruptedTurns.delete(activeTurnId);
    activeTurnId = undefined;
    mapper = undefined;
  };

  return {
    /** True while a synthetic turn is open (test/visibility aid). */
    get cycleActive(): boolean {
      return activeTurnId !== undefined;
    },
    /** Abandon an in-flight cycle and return status to idle (stream dropped). */
    reset(): void {
      if (activeTurnId !== undefined) {
        deps.emit(activeTurnId, 'turn.interrupted', { reason: 'opencode observer stream ended' });
        deps.setStatus('idle');
        endCycle();
      }
    },
    handle(ev: Event): void {
      const e = ev as unknown as { type?: string; properties?: Record<string, unknown> };
      const type = e.type;
      const props = e.properties ?? {};
      const oc = deps.ocSession();
      if (!oc) return; // session id not resolved yet (warmup still in flight)

      // Title passthrough — independent of the turn cycle AND exempt from the
      // headless-turn suppression guard below (D11): opencode can retitle a session
      // DURING a runner-driven turn, and that rename must still surface — the guard
      // previously short-circuited it. opencode mutates the title live (it
      // auto-generates one a beat after the first turn), so mirror every
      // non-placeholder change, not just the first.
      if (type === 'session.updated' || type === 'session.created') {
        const info = (props.info as { id?: string; title?: string }) ?? {};
        if (info.id === oc && typeof info.title === 'string') {
          const title = info.title.trim();
          if (title && title !== PLACEHOLDER_TITLE && title !== lastTitle) {
            lastTitle = title;
            deps.emit(undefined, 'session.title', { title });
          }
        }
        return;
      }

      // A runner-driven /turns request is the single writer for its own turn —
      // don't double-map while one is active. Pure interactive opencode never
      // registers a turn, so this only guards the headless seam (tests / k8sit).
      // The title passthrough above is deliberately exempt (D11).
      if (deps.activeTurnsSize() > 0) return;

      // Everything below is gated to our opencode session (a user can open extra
      // sessions inside the opencode TUI; their events are not ours to surface).
      const evSession = eventSessionId(props);
      if (evSession && evSession !== oc) return;

      // The observer stream is delivering an event for our session — refresh the
      // synthetic-busy staleness clock. A live interactive turn keeps this fresh
      // (stays "active"); a wedged/quiet stream lets it go stale so the reaper
      // can reclaim the pod instead of being blocked by a pinned 'busy'.
      deps.noteObserverEvent?.();

      // Pre-cycle session.error (D11): a session.error that arrives before any
      // assistant message.updated (e.g. a provider auth failure at turn start) has
      // no open cycle mapper to map it, so it would vanish and the dashboard would
      // sit idle. Surface it as a synthetic failed turn (turn.failed + error +
      // status error) so the failure is visible. A session.error DURING a cycle is
      // handled by the cycle's mapper below.
      if (activeTurnId === undefined && type === 'session.error') {
        const turnId = deps.nextTurnId();
        deps.setLastTurn(turnId);
        const message = errToString((props as { error?: unknown }).error);
        deps.emit(turnId, 'turn.failed', { message });
        deps.emit(turnId, 'error', { message });
        deps.setStatus('error');
        return;
      }

      // Cycle start: the first assistant message.updated of a cycle. opencode
      // always emits message.updated(role) before that message's parts, so this
      // reliably bookends the assistant turn.
      if (activeTurnId === undefined && type === 'message.updated') {
        const info =
          (props.info as { role?: string; sessionID?: string; providerID?: string; modelID?: string }) ?? {};
        if (info.role === 'assistant' && info.sessionID === oc) {
          const turnId = deps.nextTurnId();
          deps.setLastTurn(turnId); // satisfies the CLI cancel/suspend st.LastTurnID guard
          deps.setExternalActivity(); // keep the idle reaper from suspending mid-turn
          deps.setStatus('busy'); // safe: activeTurnsSize() === 0 here
          deps.emit(turnId, 'turn.started', {});
          activeTurnId = turnId;
          const cycleTurnId = turnId;
          mapper = createOpencodeTurnMapper(
            oc,
            (t, p) => deps.emit(cycleTurnId, t, p),
            (tool, input) => deps.audit(cycleTurnId, tool, input),
          );

          // Emit the model once so the Go side resolves a context-window limit for
          // ctx% (session.go: session.started → sess.Model + CtxLimit). opencode's
          // assistant message carries providerID/modelID (e.g. opencode/big-pickle).
          if (!modelEmitted && info.providerID && info.modelID) {
            modelEmitted = true;
            const model = `${info.providerID}/${info.modelID}`;
            deps.setModel(model);
            deps.emit(cycleTurnId, 'session.started', { model, cwd: '' });
          }
        }
      }

      // Feed the event to the active cycle's mapper. It returns true once the cycle
      // settles (session.idle/error → it already emitted turn.completed/turn.failed).
      if (activeTurnId !== undefined && mapper) {
        if (interruptedTurns.has(activeTurnId) && type === 'session.idle') {
          interruptedTurns.delete(activeTurnId);
          deps.setStatus('idle');
          endCycle();
          return;
        }
        const settled = mapper.handle(ev);
        // Discard any queued permission ids WITHOUT responding — the attached
        // opencode client owns the permission modal; the observer only mirrors.
        mapper.takePendingPermissions();
        if (settled) {
          deps.setStatus('idle'); // safe: activeTurnsSize() === 0
          endCycle();
        }
      }
    },
  };
}

/** Build ObserverDeps from the live session registry + the SSE event log. */
function registryDeps(): ObserverDeps {
  return {
    sessionId: () => getRegistry().state.sandbox_session_id,
    ocSession: () => getRegistry().state.opencode_session_id,
    activeTurnsSize: () => getRegistry().activeTurns.size,
    nextTurnId: () => getRegistry().nextTurnId(),
    setLastTurn: (id) => getRegistry().setLastTurn(id),
    setExternalActivity: () => getRegistry().setExternalActivity(),
    noteObserverEvent: () => getRegistry().noteObserverEvent(),
    setStatus: (s) => getRegistry().setStatus(s),
    setModel: (m) => getRegistry().setModel(m),
    emit: (turnId, type, payload) => appendEvent(getRegistry().state.sandbox_session_id, turnId, type, payload),
    audit: (turnId, tool, input) =>
      appendAudit({
        time: new Date().toISOString(),
        session_id: getRegistry().state.sandbox_session_id,
        turn_id: turnId,
        tool,
        input,
      }),
  };
}

/**
 * Start the always-on opencode metrics observer. Fire-and-forget: its own
 * reconnect loop absorbs the `opencode serve` boot delay, so this must not be
 * awaited at runner boot. Returns a handle whose stop() ends the loop within the
 * shutdown grace window.
 */
export function startOpencodeObserver(env: NodeJS.ProcessEnv = process.env): OpencodeObserver {
  const client = opencodeTurnClient(env);
  const core = createObserverHandler(registryDeps());
  let stopped = false;
  let stream: AsyncGenerator<Event> | undefined;

  void (async () => {
    while (!stopped) {
      try {
        const sub = await client.event.subscribe();
        stream = sub.stream as AsyncGenerator<Event>;
        for await (const ev of stream) {
          if (stopped) break;
          try {
            core.handle(ev);
          } catch (err) {
            console.error('opencode observer: event handling error:', err);
          }
        }
      } catch {
        // `opencode serve` is booting or just restarted — retry with backoff.
      }
      stream = undefined;
      // A stream that ended mid-cycle (serve restart) abandons the synthetic turn;
      // reset so the next cycle starts clean rather than wedging status 'busy'.
      try {
        core.reset();
      } catch {
        /* registry gone during shutdown */
      }
      if (stopped) break;
      await new Promise((r) => setTimeout(r, RECONNECT_BACKOFF_MS));
    }
  })().catch((err) => console.error('opencode observer loop crashed:', err));

  return {
    async stop(): Promise<void> {
      stopped = true;
      if (stream?.return) {
        await stream.return(undefined).catch(() => {});
      }
    },
  };
}

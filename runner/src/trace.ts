// Minimal timing spans (§10 observability), dependency-free: human-scannable,
// greppable lines on stdout (the pod's log stream), correlated by a stable id.
//
//   trace: boot boot.listen 2ms
//   trace: 3f9a1c2b turn.link turn=turn_ab12
//
// Off by default: enabled only when SANDBOX_TRACE is set. Pure aside from the
// injected clock/logger, so every line is unit-testable.
//
// A per-turn trace (startTurnTrace: first_message / first_delta / settled
// milestones) used to live here, driven by the SDK turn engine's runTurn.
// claude-pane-first deleted that engine, and the trace outlived it as dead code
// kept green by its own tests until it was removed [T3]. If turn-level timing is
// wanted again for the pane backend, it has to hang off the observer's event
// path, not a driving loop — there isn't one any more.

// The `trace: …` envelope is a documented, greppable contract that predates the
// structured logger, so these lines go out raw rather than wrapped in a log
// record — see logRaw's comment.
import { logRaw } from './log.js';

/** Overrides for testing: an injectable clock, log sink, and enable flag. */
export interface TraceOptions {
  now?: () => number;
  log?: (line: string) => void;
  enabled?: boolean;
}

/**
 * Bridge a client connect/create correlation id — propagated across the HTTP
 * seam as the X-Sandbox-Trace-Id header on POST /turns — to the runner's turn
 * id, so a single grep for either id in the merged CLI+pod logs pivots to the
 * other:
 *
 *   trace: 3f9a1c2b turn.link turn=turn_ab12
 *
 * Grepping the connect id (3f9a1c2b) finds this line → the turn id; grepping the
 * turn id finds this line and whatever the event log records for it. A no-op
 * unless tracing is enabled AND a connect id was supplied (header absent, or an
 * older CLI that doesn't stamp it, yields ""), so the turns path pays ~nothing
 * when off.
 */
export function traceTurnLink(
  connectId: string,
  turnId: string,
  opts: TraceOptions = {},
): void {
  const enabled = opts.enabled ?? !!process.env.SANDBOX_TRACE;
  if (!enabled || !connectId) return;
  const log = opts.log ?? logRaw;
  log(`trace: ${connectId} turn.link turn=${turnId}`);
}

/**
 * Extract + validate an X-Sandbox-Trace-Id header value. Untrusted input headed
 * for a log line: accept only short token-shaped ids (word chars, dots, dashes;
 * ≤64 chars) so a hostile header can't smuggle arbitrary content into the
 * greppable trace stream. Absent, repeated, or malformed values yield '' — and
 * traceTurnLink no-ops on ''. Pure for unit tests.
 */
export function traceIDFromHeader(value: string | string[] | undefined): string {
  const v = Array.isArray(value) ? value[0] : value;
  if (!v) return '';
  return /^[\w.-]{1,64}$/.test(v) ? v : '';
}

/**
 * A boot trace: per-phase startup timings for the runner process, correlated by
 * a fixed `boot` id (one boot per pod), so a slow pod start is attributable to a
 * specific phase — open event log, load session state, registry init, boot
 * emits, server listen:
 *
 *   trace: boot boot.event_log 3ms
 *   trace: boot boot.session_state 12ms
 *   trace: boot boot.registry 1ms
 *   trace: boot boot.boot_prep 4ms
 *   trace: boot boot.listen 2ms
 *   trace: boot boot.total 22ms
 *
 * A disabled trace is a no-op with near-zero cost.
 */
export interface BootTrace {
  /**
   * Close the phase leading up to this call and emit its duration under
   * `boot.<name>`; the next phase begins now. Call once per boot phase, in order.
   */
  phase(name: string): void;
  /** Emit the cumulative `boot.total` measured from startBootTrace. */
  done(): void;
}

const NOOP_BOOT: BootTrace = { phase() {}, done() {} };

/**
 * Start a boot trace, timed from the moment of the call. Returns a no-op unless
 * tracing is enabled (SANDBOX_TRACE set, or opts.enabled) so main() pays
 * ~nothing when off. Injectable clock/log sink for unit tests.
 */
export function startBootTrace(opts: TraceOptions = {}): BootTrace {
  const enabled = opts.enabled ?? !!process.env.SANDBOX_TRACE;
  if (!enabled) return NOOP_BOOT;
  const now = opts.now ?? ((): number => Date.now());
  const log = opts.log ?? logRaw;
  const start = now();
  let mark = start;
  return {
    phase(name: string): void {
      const t = now();
      log(`trace: boot boot.${name} ${t - mark}ms`);
      mark = t;
    },
    done(): void {
      log(`trace: boot boot.total ${now() - start}ms`);
    },
  };
}

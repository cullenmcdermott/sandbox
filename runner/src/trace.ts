// Minimal turn-lifecycle timing spans (§10 observability). The goal is to make
// runtime fan-out cost analysis possible without an OpenTelemetry dependency:
// runTurn opens a trace at turn start and records a few milestones plus a
// per-turn summary, emitting human-scannable, greppable lines to stdout (the
// pod's log stream), correlated by turn id:
//
//   trace: turn_ab12 turn.first_message 812ms
//   trace: turn_ab12 turn.first_delta 913ms
//   trace: turn_ab12 turn.settled 4210ms msgs=37
//
// The turn id lets a trace line up against the CLI's connect spans (client/) for
// an end-to-end cold-start + steady-state picture. Off by default: enabled only
// when SANDBOX_TRACE is set. Kept dependency-free and pure aside from the
// injected clock/logger so the summary line is unit-testable without a live SDK
// turn.

/** A live turn trace. A disabled trace is a no-op with near-zero cost. */
export interface TurnTrace {
  /** Record the first occurrence of a named milestone (idempotent per name). */
  mark(name: string): void;
  /** Emit the per-turn summary: total duration + SDK messages consumed. */
  settle(msgCount: number): void;
}

const NOOP: TurnTrace = { mark() {}, settle() {} };

/** Overrides for testing: an injectable clock, log sink, and enable flag. */
export interface TurnTraceOptions {
  now?: () => number;
  log?: (line: string) => void;
  enabled?: boolean;
}

/**
 * Start a turn trace, timed from the moment of the call (turn start). Returns a
 * no-op unless tracing is enabled (SANDBOX_TRACE set, or opts.enabled) so the
 * turn loop pays ~nothing when off.
 */
export function startTurnTrace(turnId: string, opts: TurnTraceOptions = {}): TurnTrace {
  const enabled = opts.enabled ?? !!process.env.SANDBOX_TRACE;
  if (!enabled) return NOOP;
  const now = opts.now ?? ((): number => Date.now());
  const log = opts.log ?? ((line: string): void => console.log(line));
  const start = now();
  const seen = new Set<string>();
  return {
    mark(name: string): void {
      if (seen.has(name)) return;
      seen.add(name);
      log(`trace: ${turnId} ${name} ${now() - start}ms`);
    },
    settle(msgCount: number): void {
      log(`trace: ${turnId} turn.settled ${now() - start}ms msgs=${msgCount}`);
    },
  };
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
 * turn id finds both this line (turn=…) and the turn.* milestone lines. A no-op
 * unless tracing is enabled AND a connect id was supplied (header absent, or an
 * older CLI that doesn't stamp it, yields ""), so the turns path pays ~nothing
 * when off.
 */
export function traceTurnLink(
  connectId: string,
  turnId: string,
  opts: TurnTraceOptions = {},
): void {
  const enabled = opts.enabled ?? !!process.env.SANDBOX_TRACE;
  if (!enabled || !connectId) return;
  const log = opts.log ?? ((line: string): void => console.log(line));
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
export function startBootTrace(opts: TurnTraceOptions = {}): BootTrace {
  const enabled = opts.enabled ?? !!process.env.SANDBOX_TRACE;
  if (!enabled) return NOOP_BOOT;
  const now = opts.now ?? ((): number => Date.now());
  const log = opts.log ?? ((line: string): void => console.log(line));
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

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

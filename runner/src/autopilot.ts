// The runner-owned autopilot driver (server-side /loop-/goal loop).
//
// docs/archive/server-side-loop-adr.md is the contract. The driver self-submits the next
// turn off its own turn-completion signal (not a polled wall clock): on arm it
// submits immediately (unless a turn is in flight); on each completion it
// increments iterations, scans the completed assistant text for the sentinel,
// enforces the runaway guards (max_iterations / token_budget), then schedules the
// next turn after interval_ms. A `gen` guard drops any scheduled tick that fires
// after a disarm/rearm. Failure paths (H2): a self-submit that 409s a manual turn
// defers (no iteration, no error); transient turn failures climb a bounded retry
// ladder; exhaustion/non-retriable stops(error). Boot re-arm (H1) re-emits armed
// and re-schedules anchored on last_completed_at. A staleness bound (Q1) lapses an
// armed-but-wedged driver after 30m so it can't pin the pod unreapable forever.
//
// The driver's side effects go through the injectable AutopilotHost so the state
// machine is unit-testable with fake timers/clock and no sqlite. createAutopilot()
// binds the production host (session registry + event log + shared startTurn).

import { appendEvent, readTurnOutcome, sumTokens } from './events.js';
import { appendAudit } from './audit.js';
import { getRegistry } from './session.js';
import type { RunnerConfig } from './session.js';
import type { Agent } from './agent.js';
import { startTurn, setTurnSettledHandler } from './turns.js';
import type { AutopilotSpec, AutopilotStopReason } from './types.js';

// --- Committed constants (ADR sign-off) -----------------------------------

/** Max self-submit retry attempts on a transient failure before stopping(error). */
export const MAX_RETRIES = 5;
/** Retry backoff floor: max(interval_ms, 30s) for the first retry. */
export const RETRY_MIN_MS = 30_000;
/** Retry backoff cap: doubling is clamped to 5 minutes. */
export const RETRY_CAP_MS = 5 * 60_000;
/** Staleness bound (Q1/H1): an armed driver with no completion for this long
 * (anchored on max(last_completed_at, boot time, armed_at)) is lapsed. */
export const STALENESS_MS = 30 * 60_000;

// --- The spec the endpoint validates into (arm fills the rest) ------------

/** The client-supplied portion of an arm request; the driver fills state, gen,
 * iterations, armed_at, last_completed_at, stopped_reason. */
export interface AutopilotArmInput {
  kind: 'loop' | 'goal';
  prompt: string;
  sentinel: string;
  interval_ms: number;
  overrides: { model?: string; effort?: string; mode?: string };
  max_iterations: number;
  token_budget: number | null;
}

// --- Host abstraction (all side effects) ----------------------------------

/** Opaque timer handle the host hands back from schedule(). */
export type TimerHandle = unknown;

/** Everything the driver touches outside its own state machine. Injected so the
 * logic is unit-testable with fakes (no timers, no sqlite, no registry). */
export interface AutopilotHost {
  /** The persisted spec (registry state.autopilot), or undefined if never armed. */
  getSpec(): AutopilotSpec | undefined;
  /** Persist the spec wholesale + recompute idle (registry.setAutopilot). */
  putSpec(spec: AutopilotSpec): void;
  /** Emit an autopilot.state event (append-before-stream). */
  emit(payload: {
    state: 'armed' | 'ticked' | 'stopped';
    kind: string;
    reason?: AutopilotStopReason;
    iteration: number;
    gen: number;
  }): void;
  /** Emit a user-visible warning (an `error` event) — used for the lapse toast. */
  warn(message: string, code: string): void;
  /** Append an audit entry (the durable record for a stopped(error)/lapse). */
  audit(detail: string): void;
  /** True when any turn (manual or self-submitted) is currently in flight. */
  turnInFlight(): boolean;
  /** Submit a turn via the shared startTurn path (same 409 gate). */
  submitTurn(spec: AutopilotSpec): { turnId: string } | { rejected: string };
  /** The terminal outcome of a settled turn (from the event log). */
  turnOutcome(turnId: string): { status: 'completed' | 'failed' | 'interrupted'; resultText: string } | undefined;
  /** Cumulative input+output tokens across the loop (for the token_budget guard). */
  tokensUsed(): number;
  /** Current time in ms (injectable for tests). */
  now(): number;
  /** Schedule fn after ms; returns a handle (injectable for tests). */
  schedule(fn: () => void, ms: number): TimerHandle;
  /** Cancel a scheduled timer. */
  cancel(h: TimerHandle | null): void;
}

// --- The driver -----------------------------------------------------------

/** The public driver surface the endpoint + boot wiring use. */
export interface Autopilot {
  /** Arm (or replace) the driver: overwrite the spec wholesale, bump gen, emit
   * armed, and submit the first turn immediately (unless a turn is in flight). */
  arm(input: AutopilotArmInput): void;
  /** Disarm: stop(user) + bump gen (kills pending ticks). Returns false when
   * there is no spec to disarm (the endpoint 404s then). */
  disarm(): boolean;
  /** Boot re-arm (H1): re-emit armed + re-schedule for a still-armed persisted
   * spec; no-op for a stopped (or absent) spec. */
  bootReArm(): void;
}

export class AutopilotDriver implements Autopilot {
  private nextTimer: TimerHandle | null = null;
  private staleTimer: TimerHandle | null = null;
  private retries = 0;
  private readonly bootTime: number;

  constructor(private readonly host: AutopilotHost) {
    this.bootTime = host.now();
  }

  // --- arm / disarm -------------------------------------------------------

  arm(input: AutopilotArmInput): void {
    const prevGen = this.host.getSpec()?.gen ?? 0;
    const nowIso = new Date(this.host.now()).toISOString();
    const spec: AutopilotSpec = {
      kind: input.kind,
      state: 'armed',
      stopped_reason: null,
      prompt: input.prompt,
      sentinel: input.sentinel,
      interval_ms: input.interval_ms,
      overrides: input.overrides,
      max_iterations: input.max_iterations,
      token_budget: input.token_budget,
      iterations: 0,
      armed_at: nowIso,
      last_completed_at: null,
      gen: prevGen + 1,
    };
    this.clearTimers();
    this.retries = 0;
    this.host.putSpec(spec);
    this.host.emit({ state: 'armed', kind: spec.kind, iteration: 0, gen: spec.gen });
    this.scheduleStaleness(spec);
    // Submit the first turn immediately (deferring if a turn is already running).
    this.attemptSubmit(spec);
  }

  disarm(): boolean {
    const spec = this.host.getSpec();
    if (!spec) return false;
    this.clearTimers();
    // Idempotent for an already-stopped spec: keep the original terminal record
    // (a DELETE after stopped(sentinel) must not rewrite the reason to 'user' or
    // re-emit a second stopped event — replay clients would mis-render the cause).
    if (spec.state === 'stopped') return true;
    // Bump gen so any tick already scheduled against the old gen is dropped, then
    // stop(user).
    spec.gen += 1;
    this.stop(spec, 'user');
    return true;
  }

  // --- boot re-arm (H1) ---------------------------------------------------

  bootReArm(): void {
    const spec = this.host.getSpec();
    // A spec that already reached a terminal state before the crash re-emits
    // nothing and stays stopped (H1 mechanism (a)). Only a persisted 'armed'
    // resumes.
    if (!spec || spec.state !== 'armed') return;
    this.retries = 0;
    // Re-emit armed so replay clients / a fresh attach re-render the armed chip.
    this.host.emit({ state: 'armed', kind: spec.kind, iteration: spec.iterations, gen: spec.gen });
    // Anchor the next turn on last_completed_at + interval_ms (H1 step 2): a crash
    // mid-interval resumes on the remaining time, not a fresh interval. If no turn
    // has completed yet, anchor on armed_at. Past-due fires immediately.
    const anchor = spec.last_completed_at
      ? Date.parse(spec.last_completed_at)
      : Date.parse(spec.armed_at);
    const delay = Math.max(0, anchor + spec.interval_ms - this.host.now());
    this.scheduleFire(spec, delay);
    this.scheduleStaleness(spec);
  }

  // --- turn completion signal (wired via turnSettledHandler) --------------

  /** Called after every turn (manual or self-submitted) fully settles. */
  onTurnSettled(turnId: string): void {
    const spec = this.host.getSpec();
    if (!spec || spec.state !== 'armed') return; // disarmed/stopped: nothing to do.
    const outcome = this.host.turnOutcome(turnId);

    if (!outcome || outcome.status === 'failed') {
      // A transient failure (SDK error result, or a runTurn that threw without a
      // terminal). Climb the retry ladder — does NOT count as an iteration.
      this.onTransientError(spec, outcome ? outcome.resultText : 'no terminal event for turn');
      return;
    }
    if (outcome.status === 'interrupted') {
      // A user interrupt (esc). Not an error and not a completed iteration — the
      // output is partial, so don't scan the sentinel or increment. Keep looping:
      // reschedule (gen-guarded; a DELETE would have bumped gen). Reset retries —
      // an interrupt is a clean tear-down, not a transient failure.
      this.retries = 0;
      this.scheduleNext(spec);
      return;
    }

    // A completed turn (manual or self-submit) — one loop iteration.
    this.retries = 0;
    spec.iterations += 1;
    spec.last_completed_at = new Date(this.host.now()).toISOString();
    this.host.putSpec(spec);
    this.host.emit({ state: 'ticked', kind: spec.kind, iteration: spec.iterations, gen: spec.gen });
    // Reset the staleness clock now that a turn completed.
    this.scheduleStaleness(spec);

    // Sentinel termination — scan the just-completed assistant text.
    if (spec.sentinel && outcome.resultText.includes(spec.sentinel)) {
      this.stop(spec, 'sentinel');
      return;
    }
    // Runaway guards BEFORE scheduling the next turn (Q2 — hard stops).
    if (spec.iterations >= spec.max_iterations) {
      this.stop(spec, 'budget');
      return;
    }
    if (spec.token_budget != null && this.host.tokensUsed() >= spec.token_budget) {
      this.stop(spec, 'budget');
      return;
    }
    this.scheduleNext(spec);
  }

  // --- scheduling ---------------------------------------------------------

  private scheduleNext(spec: AutopilotSpec): void {
    this.scheduleFire(spec, Math.max(0, spec.interval_ms));
  }

  private scheduleFire(spec: AutopilotSpec, delay: number): void {
    this.host.cancel(this.nextTimer);
    const gen = spec.gen;
    this.nextTimer = this.host.schedule(() => this.fire(gen), delay);
  }

  private fire(gen: number): void {
    this.nextTimer = null;
    const spec = this.host.getSpec();
    // gen guard: a disarm/rearm since scheduling makes this tick stale — drop it.
    if (!spec || spec.state !== 'armed' || spec.gen !== gen) return;
    this.attemptSubmit(spec);
  }

  private attemptSubmit(spec: AutopilotSpec): void {
    // H2 defer: a manual turn is in flight (the user is driving). Do not submit —
    // that turn's settle re-hooks us via onTurnSettled → scheduleNext. No error,
    // no iteration increment; a manual turn is a free iteration.
    if (this.host.turnInFlight()) return;
    const res = this.host.submitTurn(spec);
    if ('rejected' in res) {
      // Lost the check-vs-submit race to a manual turn (the 409 gate). Defer just
      // like turnInFlight above: the winner's settle re-hooks us.
      return;
    }
    // Started; onTurnSettled will drive the next step when it completes.
  }

  // --- retry ladder (H2) --------------------------------------------------

  private onTransientError(spec: AutopilotSpec, detail: string): void {
    this.retries += 1;
    if (this.retries > MAX_RETRIES) {
      this.host.audit(
        `autopilot: giving up after ${MAX_RETRIES} retries (gen ${spec.gen}, iteration ${spec.iterations}): ${detail}`,
      );
      this.stop(spec, 'error');
      return;
    }
    // Backoff: max(interval_ms, 30s) doubling per attempt, capped at 5m. Retries
    // do NOT count against max_iterations (they produced no completed turn).
    const base = Math.max(spec.interval_ms, RETRY_MIN_MS);
    const backoff = Math.min(base * 2 ** (this.retries - 1), RETRY_CAP_MS);
    this.host.audit(
      `autopilot: transient failure (retry ${this.retries}/${MAX_RETRIES}, backoff ${backoff}ms): ${detail}`,
    );
    this.scheduleFire(spec, backoff);
  }

  // --- staleness bound (Q1) ----------------------------------------------

  private staleAnchor(spec: AutopilotSpec): number {
    return Math.max(
      spec.last_completed_at ? Date.parse(spec.last_completed_at) : 0,
      this.bootTime,
      Date.parse(spec.armed_at),
    );
  }

  private scheduleStaleness(spec: AutopilotSpec): void {
    this.host.cancel(this.staleTimer);
    const delay = Math.max(0, this.staleAnchor(spec) + STALENESS_MS - this.host.now());
    const gen = spec.gen;
    this.staleTimer = this.host.schedule(() => this.onStaleTimer(gen), delay);
  }

  private onStaleTimer(gen: number): void {
    this.staleTimer = null;
    const spec = this.host.getSpec();
    if (!spec || spec.state !== 'armed' || spec.gen !== gen) return;
    if (this.host.now() - this.staleAnchor(spec) >= STALENESS_MS) {
      // Wedged: armed with no completion for the whole window. Warn + lapse so the
      // session becomes idle-eligible again (the reaper can reclaim the pod).
      this.host.warn(
        `autopilot lapsed: no turn completed for ${Math.round(STALENESS_MS / 60_000)}m; disarming`,
        'autopilot_lapsed',
      );
      this.host.audit(`autopilot: lapsed (no completion for ${STALENESS_MS}ms, gen ${spec.gen})`);
      this.stop(spec, 'lapsed');
      return;
    }
    // A completion advanced the anchor between scheduling and firing — reschedule.
    this.scheduleStaleness(spec);
  }

  // --- terminal transition ------------------------------------------------

  private stop(spec: AutopilotSpec, reason: AutopilotStopReason): void {
    this.clearTimers();
    // Crash-window rule (H1): persist state:'stopped' BEFORE emitting the stopped
    // event, so the durable record and the event stream can't disagree — a crash
    // in the window leaves state:'armed' and boot re-arm correctly resumes.
    spec.state = 'stopped';
    spec.stopped_reason = reason;
    this.host.putSpec(spec);
    this.host.emit({ state: 'stopped', kind: spec.kind, reason, iteration: spec.iterations, gen: spec.gen });
  }

  private clearTimers(): void {
    this.host.cancel(this.nextTimer);
    this.host.cancel(this.staleTimer);
    this.nextTimer = null;
    this.staleTimer = null;
  }
}

// --- Production wiring -----------------------------------------------------

/**
 * Build the production autopilot driver for a claude-backend session: bind the
 * host to the live session registry, event log, audit log, and shared startTurn
 * path, and register the turn-settled handler so completions drive the loop.
 * Returns the driver (the endpoint arms/disarms it; index.ts boot-re-arms it).
 */
export function createAutopilot(cfg: RunnerConfig, agent: Agent): AutopilotDriver {
  const host: AutopilotHost = {
    getSpec: () => getRegistry().getAutopilot(),
    putSpec: (spec) => getRegistry().setAutopilot(spec),
    emit: (payload) => {
      appendEvent(cfg.sessionId, undefined, 'autopilot.state', payload as Record<string, unknown>);
    },
    warn: (message, code) => {
      appendEvent(cfg.sessionId, undefined, 'error', { message, code });
    },
    audit: (detail) => {
      appendAudit({
        time: new Date().toISOString(),
        session_id: cfg.sessionId,
        turn_id: 'autopilot',
        tool: 'Autopilot',
        input: { detail },
      });
    },
    turnInFlight: () => getRegistry().activeTurns.size > 0,
    submitTurn: (spec) =>
      startTurn(cfg, agent, spec.prompt, {
        ...(spec.overrides.mode ? { mode: spec.overrides.mode } : {}),
        ...(spec.overrides.model ? { model: spec.overrides.model } : {}),
        ...(spec.overrides.effort ? { effort: spec.overrides.effort } : {}),
      }),
    turnOutcome: (turnId) => readTurnOutcome(cfg.sessionId, turnId),
    tokensUsed: () => sumTokens(cfg.sessionId),
    now: () => Date.now(),
    schedule: (fn, ms) => setTimeout(fn, ms),
    cancel: (h) => {
      if (h != null) clearTimeout(h as ReturnType<typeof setTimeout>);
    },
  };
  const driver = new AutopilotDriver(host);
  setTurnSettledHandler((turnId) => driver.onTurnSettled(turnId));
  return driver;
}

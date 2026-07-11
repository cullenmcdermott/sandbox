// Unit tests for the runner-owned autopilot driver state machine
// (src/autopilot.ts), driven through an injected fake host so the logic is
// exercised with a controllable clock + timers and no sqlite/registry/network.
// Covers the ADR contract: sentinel stop, max_iterations stop (default 50),
// token_budget stop, the gen guard, the 409/defer path, boot re-arm, the
// persist-before-emit crash-window rule, the staleness lapse, and the retry
// ladder. The registry-side idle rule (Q1) is covered at the bottom.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AutopilotDriver,
  MAX_RETRIES,
  RETRY_MIN_MS,
  STALENESS_MS,
  type AutopilotHost,
  type AutopilotArmInput,
  type TimerHandle,
} from '../src/autopilot.js';
import { initRegistry } from '../src/session.js';
import type { AutopilotSpec, SessionState } from '../src/types.js';

// --- Fake host -------------------------------------------------------------

interface FakeTimer {
  id: number;
  fn: () => void;
  delay: number;
}

type Emit = { state: string; kind: string; reason?: string; iteration: number; gen: number };
type Outcome = { status: 'completed' | 'failed' | 'interrupted'; resultText: string };

class FakeHost implements AutopilotHost {
  spec: AutopilotSpec | undefined;
  emits: Emit[] = [];
  warns: Array<{ message: string; code: string }> = [];
  audits: string[] = [];
  submits: AutopilotSpec[] = [];
  timers: FakeTimer[] = [];
  clock = 1_000_000;
  inFlight = false;
  submitResult: { turnId: string } | { rejected: string } = { turnId: 'turn-1' };
  outcomes = new Map<string, Outcome>();
  tokens = 0;
  private nextId = 1;
  // Records host.spec.state at the instant a `stopped` event is emitted, to pin
  // the persist-before-emit crash-window rule.
  stoppedStateAtEmit: string | null = null;

  getSpec(): AutopilotSpec | undefined {
    return this.spec;
  }
  putSpec(spec: AutopilotSpec): void {
    this.spec = spec;
  }
  emit(payload: Emit): void {
    if (payload.state === 'stopped') this.stoppedStateAtEmit = this.spec?.state ?? null;
    this.emits.push(payload);
  }
  warn(message: string, code: string): void {
    this.warns.push({ message, code });
  }
  audit(detail: string): void {
    this.audits.push(detail);
  }
  turnInFlight(): boolean {
    return this.inFlight;
  }
  submitTurn(spec: AutopilotSpec): { turnId: string } | { rejected: string } {
    this.submits.push({ ...spec });
    return this.submitResult;
  }
  turnOutcome(turnId: string): Outcome | undefined {
    return this.outcomes.get(turnId);
  }
  tokensUsed(): number {
    return this.tokens;
  }
  now(): number {
    return this.clock;
  }
  schedule(fn: () => void, ms: number): TimerHandle {
    const t: FakeTimer = { id: this.nextId++, fn, delay: ms };
    this.timers.push(t);
    return t.id;
  }
  cancel(h: TimerHandle | null): void {
    if (h == null) return;
    this.timers = this.timers.filter((t) => t.id !== h);
  }

  // --- test helpers ---
  advance(ms: number): void {
    this.clock += ms;
  }
  /** Fire every currently-pending timer once (in schedule order). */
  runTimers(): void {
    const due = this.timers.slice();
    this.timers = [];
    for (const t of due) t.fn();
  }
  timerWithDelay(ms: number): FakeTimer | undefined {
    return this.timers.find((t) => t.delay === ms);
  }
}

function armInput(overrides: Partial<AutopilotArmInput> = {}): AutopilotArmInput {
  return {
    kind: 'loop',
    prompt: 'do the thing',
    sentinel: '',
    interval_ms: 0,
    overrides: {},
    max_iterations: 50,
    token_budget: null,
    ...overrides,
  };
}

/** Complete a turn: register its outcome, then drive the settle signal. */
function completeTurn(driver: AutopilotDriver, host: FakeHost, turnId: string, resultText = ''): void {
  host.outcomes.set(turnId, { status: 'completed', resultText });
  driver.onTurnSettled(turnId);
}

// --- arm + immediate submit ------------------------------------------------

test('arm emits armed(gen 1) and submits the first turn immediately', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput());
  assert.equal(host.emits.length, 1);
  assert.deepEqual(host.emits[0], { state: 'armed', kind: 'loop', iteration: 0, gen: 1 });
  assert.equal(host.submits.length, 1, 'first turn submitted immediately');
  assert.equal(host.spec?.state, 'armed');
  assert.equal(host.spec?.gen, 1);
});

test('a completed turn emits ticked and schedules the next turn', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ interval_ms: 100 }));
  completeTurn(driver, host, 'turn-1');
  assert.equal(host.spec?.iterations, 1);
  const ticked = host.emits.find((e) => e.state === 'ticked');
  assert.deepEqual(ticked, { state: 'ticked', kind: 'loop', iteration: 1, gen: 1 });
  assert.ok(host.timerWithDelay(100), 'next turn scheduled after interval_ms');
});

// --- sentinel stop ---------------------------------------------------------

test('sentinel in the completed text stops(sentinel)', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ sentinel: 'GOAL_MET' }));
  completeTurn(driver, host, 'turn-1', 'work done — GOAL_MET');
  assert.equal(host.spec?.state, 'stopped');
  assert.equal(host.spec?.stopped_reason, 'sentinel');
  const stopped = host.emits.find((e) => e.state === 'stopped');
  assert.equal(stopped?.reason, 'sentinel');
  // No next turn scheduled after a terminal stop (only the pre-check staleness
  // timer, if any, is cleared).
  assert.equal(host.submits.length, 1, 'no further submit after sentinel stop');
});

// --- max_iterations (default 50) -------------------------------------------

test('max_iterations default 50 stops(budget) exactly at the 50th completion', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ max_iterations: 50 }));
  for (let i = 1; i <= 49; i++) {
    completeTurn(driver, host, `turn-${i}`);
    assert.equal(host.spec?.state, 'armed', `still armed after iteration ${i}`);
  }
  completeTurn(driver, host, 'turn-50');
  assert.equal(host.spec?.iterations, 50);
  assert.equal(host.spec?.state, 'stopped');
  assert.equal(host.spec?.stopped_reason, 'budget');
});

// --- token_budget ----------------------------------------------------------

test('token_budget exhaustion stops(budget)', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ token_budget: 1000 }));
  host.tokens = 500;
  completeTurn(driver, host, 'turn-1');
  assert.equal(host.spec?.state, 'armed', 'under budget: keeps looping');
  host.tokens = 1000; // reached the ceiling
  completeTurn(driver, host, 'turn-2');
  assert.equal(host.spec?.state, 'stopped');
  assert.equal(host.spec?.stopped_reason, 'budget');
});

// --- 409 / defer semantics -------------------------------------------------

test('arm defers (no submit, no error) when a manual turn is in flight; the manual turn counts as a free iteration', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  host.inFlight = true;
  driver.arm(armInput({ interval_ms: 5 }));
  assert.equal(host.submits.length, 0, 'no self-submit while a manual turn runs');
  assert.equal(host.spec?.state, 'armed');
  // The manual turn completes → re-hooks via onTurnSettled → counts as iteration 1.
  host.inFlight = false;
  completeTurn(driver, host, 'manual-turn');
  assert.equal(host.spec?.iterations, 1, 'manual turn counted as a free iteration');
  assert.ok(host.timerWithDelay(5), 'next iteration scheduled after the manual turn');
});

test('a submit that loses the 409 race defers without erroring or incrementing', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  host.submitResult = { rejected: 'a turn is already active' };
  driver.arm(armInput());
  assert.equal(host.spec?.state, 'armed', 'still armed after a rejected submit');
  assert.equal(host.spec?.iterations, 0);
  assert.equal(host.emits.filter((e) => e.state === 'stopped').length, 0, 'no stop on a 409');
});

// --- gen guard (disarm cancels pending tick) -------------------------------

test('disarm bumps gen, cancels pending timers, and a stale tick fired anyway is dropped', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ interval_ms: 100 }));
  completeTurn(driver, host, 'turn-1'); // schedules the next tick (gen 1)
  const tick = host.timerWithDelay(100);
  assert.ok(tick, 'a next tick is pending');
  const submitsBefore = host.submits.length;

  assert.equal(driver.disarm(), true);
  assert.equal(host.spec?.state, 'stopped');
  assert.equal(host.spec?.stopped_reason, 'user');
  assert.equal(host.spec?.gen, 2, 'disarm bumped gen');
  assert.equal(host.timers.length, 0, 'disarm cancelled all pending timers');

  // Even if the old timer fires anyway (a race), the gen guard drops it.
  tick!.fn();
  assert.equal(host.submits.length, submitsBefore, 'stale tick did not submit');
});

test('disarm returns false when there is no spec to disarm', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  assert.equal(driver.disarm(), false);
});

test('disarm on an already-stopped spec preserves the original terminal record', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ sentinel: 'DONE' }));
  completeTurn(driver, host, 'turn-1', 'all set DONE'); // → stopped(sentinel)
  assert.equal(host.spec?.state, 'stopped');
  assert.equal(host.spec?.stopped_reason, 'sentinel');
  const gen = host.spec!.gen;
  const emitsBefore = host.emits.length;

  assert.equal(driver.disarm(), true, 'idempotent success');
  assert.equal(host.spec?.stopped_reason, 'sentinel', 'reason not rewritten to user');
  assert.equal(host.spec?.gen, gen, 'gen not bumped for a no-op disarm');
  assert.equal(host.emits.length, emitsBefore, 'no second stopped event emitted');
});

// --- boot re-arm (H1) ------------------------------------------------------

function armedSpec(overrides: Partial<AutopilotSpec> = {}): AutopilotSpec {
  return {
    kind: 'loop',
    state: 'armed',
    stopped_reason: null,
    prompt: 'p',
    sentinel: '',
    interval_ms: 0,
    overrides: {},
    max_iterations: 50,
    token_budget: null,
    iterations: 3,
    armed_at: new Date(1_000_000).toISOString(),
    last_completed_at: null,
    gen: 5,
    ...overrides,
  };
}

test('boot re-arm on a persisted armed spec re-emits armed and schedules', () => {
  const host = new FakeHost();
  host.spec = armedSpec();
  const driver = new AutopilotDriver(host);
  driver.bootReArm();
  assert.deepEqual(host.emits[0], { state: 'armed', kind: 'loop', iteration: 3, gen: 5 });
  assert.ok(host.timers.length >= 1, 'boot re-arm scheduled a fire + staleness timer');
});

test('boot re-arm on a stopped spec does nothing (no phantom armed)', () => {
  const host = new FakeHost();
  host.spec = armedSpec({ state: 'stopped', stopped_reason: 'sentinel' });
  const driver = new AutopilotDriver(host);
  driver.bootReArm();
  assert.equal(host.emits.length, 0);
  assert.equal(host.timers.length, 0);
});

test('boot re-arm anchors the next turn on last_completed_at + interval_ms', () => {
  const host = new FakeHost();
  // Completed 100ms ago; interval 1000ms → ~900ms remaining.
  host.spec = armedSpec({
    interval_ms: 1000,
    last_completed_at: new Date(host.clock - 100).toISOString(),
  });
  const driver = new AutopilotDriver(host);
  driver.bootReArm();
  // The fire timer (not the 30m staleness one) is scheduled for the remaining ~900ms.
  const fire = host.timers.find((t) => t.delay < STALENESS_MS);
  assert.ok(fire && Math.abs(fire.delay - 900) < 5, `fire anchored on remaining interval (got ${fire?.delay})`);
});

// --- persist-before-emit (crash-window rule) -------------------------------

test('a stopped transition persists state:stopped BEFORE emitting the stopped event', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ sentinel: 'DONE' }));
  completeTurn(driver, host, 'turn-1', 'DONE');
  assert.equal(host.stoppedStateAtEmit, 'stopped', 'spec was already persisted stopped when the event was emitted');
});

// --- staleness lapse (Q1) --------------------------------------------------

test('an armed driver with no completion for 30m lapses (warn + stop(lapsed))', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput()); // submits turn-1, which never completes (wedged)
  // Jump past the staleness window and fire the staleness timer.
  host.advance(STALENESS_MS + 1);
  host.runTimers();
  assert.equal(host.spec?.state, 'stopped');
  assert.equal(host.spec?.stopped_reason, 'lapsed');
  assert.equal(host.warns.length, 1);
  assert.equal(host.warns[0].code, 'autopilot_lapsed');
});

test('the staleness clock is reset by a completion (no premature lapse)', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ interval_ms: 0 }));
  // Almost stale, then a turn completes → anchor advances.
  host.advance(STALENESS_MS - 10);
  completeTurn(driver, host, 'turn-1');
  // Fire the (rescheduled) staleness timer: not stale yet relative to the new anchor.
  const stale = host.timerWithDelay(STALENESS_MS);
  assert.ok(stale, 'staleness rescheduled after completion');
  stale!.fn();
  assert.equal(host.spec?.state, 'armed', 'not lapsed — the completion reset the clock');
});

// --- retry ladder (H2) -----------------------------------------------------

test('transient failures climb the retry ladder, then stop(error) on exhaustion', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ interval_ms: 0 }));
  host.outcomes.set('turn-x', { status: 'failed', resultText: 'boom' });
  for (let i = 1; i <= MAX_RETRIES; i++) {
    driver.onTurnSettled('turn-x');
    assert.equal(host.spec?.state, 'armed', `still armed after retry ${i}`);
    assert.equal(host.spec?.iterations, 0, 'retries never count as iterations');
    // Backoff floor is max(interval,30s)=30s on the first retry.
    if (i === 1) assert.ok(host.timerWithDelay(RETRY_MIN_MS), 'first retry backoff is 30s floor');
  }
  driver.onTurnSettled('turn-x'); // exhausts the ladder
  assert.equal(host.spec?.state, 'stopped');
  assert.equal(host.spec?.stopped_reason, 'error');
  assert.ok(host.audits.some((a) => a.includes('giving up')), 'exhaustion audited');
});

test('a failed turn followed by a completion resets the retry counter', () => {
  const host = new FakeHost();
  const driver = new AutopilotDriver(host);
  driver.arm(armInput({ interval_ms: 0, max_iterations: 100 }));
  host.outcomes.set('t-fail', { status: 'failed', resultText: 'x' });
  driver.onTurnSettled('t-fail'); // retry 1
  completeTurn(driver, host, 't-ok'); // resets retries, iteration 1
  // Drive MAX_RETRIES more failures — if the counter had NOT reset, the 4th here
  // would already exhaust. It must take a full ladder from zero.
  host.outcomes.set('t-fail2', { status: 'failed', resultText: 'y' });
  for (let i = 1; i <= MAX_RETRIES; i++) {
    driver.onTurnSettled('t-fail2');
    assert.equal(host.spec?.state, 'armed', `armed after post-reset retry ${i}`);
  }
});

// --- registry idle rule (Q1) ----------------------------------------------

function idleState(overrides: Partial<SessionState> = {}): SessionState {
  return {
    sandbox_session_id: 'sess-idle',
    backend: 'claude-sdk',
    project_path: '/p',
    status: 'idle',
    claude_session_id: '',
    opencode_session_id: '',
    last_turn_id: '',
    last_activity: new Date().toISOString(),
    ...overrides,
  };
}

test('an armed autopilot marks the session non-idle; stopping releases it', () => {
  const reg = initRegistry(idleState());
  reg.state.autopilot = armedSpec(); // set directly (avoid the /session write)
  reg.recomputeIdle();
  assert.equal(reg.idleStatus().turnActive, true, 'armed → non-idle');
  assert.equal(reg.idleStatus().idleSince, undefined, 'armed blocks the idle clock');

  reg.state.autopilot = armedSpec({ state: 'stopped', stopped_reason: 'user' });
  reg.recomputeIdle();
  assert.equal(reg.idleStatus().turnActive, false, 'stopped → idle-eligible');
  assert.ok(reg.idleStatus().idleSince, 'idle clock starts once stopped');
});

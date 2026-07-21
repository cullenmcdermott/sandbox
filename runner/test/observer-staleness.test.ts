// §7 opencode-observer hardening + [L8b] stale-busy release:
//
//   1. Bound a stuck synthetic 'busy'. recomputeIdle()/idleStatus() treat an
//      observer-set status='busy' (an interactive opencode turn — no registered
//      runner turn) as an active turn, which keeps the idle reaper off. A wedged
//      mapper / missed `session.idle` would otherwise pin 'busy' forever and make
//      the pod unreapable. A STALE synthetic busy (no observer events for
//      SYNTHETIC_BUSY_STALE_MS, 5 min) must release; a FRESH one must stay active.
//
//      [L8b] The release is now REAL, not reaper-only: recomputeIdle flips the
//      persisted status to 'idle' through setStatus — which emits
//      `session.status_changed` — regardless of attachment, so an attached
//      dashboard stops showing "working" instead of never being corrected.
//      Reaper idle-eligibility is unchanged (idleSince still requires
//      isDetached). The release tests therefore drive a REAL temp sqlite event
//      log + a temp session.json (setStatus persists + appends), guarded via
//      sqlite-probe like session-boot-events.test.ts.
//
//   2. GC the module-global `interruptedTurns` set. Its entry is only shed on the
//      turn's own `session.idle`; a stream drop (reset) in between leaks it.
//      reset()/endCycle() must clear it.
//
// The staleness clock is injectable (noteObserverEvent(atMs)) so these backdate
// it instead of waiting out the 5-minute window.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { Event } from '@opencode-ai/sdk';

import {
  initRegistry,
  reviveSessionState,
  setExternalActivityProbe,
  __setSessionJsonPathForTest,
  type RunnerConfig,
} from '../src/session.js';
import { __setEventLogForTest } from '../src/events.js';
import {
  createObserverHandler,
  markObservedTurnInterrupted,
  hasInterruptedTurn,
  type ObserverDeps,
} from '../src/opencode-observer.js';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';

const cfg: RunnerConfig = {
  sessionId: 'oc-stale',
  backend: 'opencode-server',
  projectPath: '/proj',
  runnerToken: 't',
};

// 6 minutes ago — safely past the 5-minute SYNTHETIC_BUSY_STALE_MS window.
const staleAgo = (): number => Date.now() - 6 * 60_000;

/** A registry for an opencode session that the observer has flipped to a
 * synthetic 'busy' (status set directly, mirroring the observer's cycle-start
 * setStatus while activeTurns stays empty). */
function busyOpencodeRegistry() {
  const reg = initRegistry(
    reviveSessionState(
      { sandbox_session_id: 'oc-stale', backend: 'opencode-server', project_path: '/proj', status: 'idle', last_turn_id: 'turn-1' },
      cfg,
    ),
  );
  reg.state.status = 'busy';
  return reg;
}

// The exact events schema src/events.ts uses (mirrors session-boot-events.test.ts).
const CREATE_SQL = `
  CREATE TABLE IF NOT EXISTS events (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    time       TEXT    NOT NULL,
    session_id TEXT    NOT NULL,
    turn_id    TEXT,
    type       TEXT    NOT NULL,
    payload    TEXT    NOT NULL
  );
  CREATE INDEX IF NOT EXISTS idx_events_session_seq ON events(session_id, seq);
`;

/** [L8b] The stale release goes through setStatus, which persists session.json
 * AND appends session.status_changed — both point at /session in production, so
 * redirect both to a temp dir for the duration of `fn`. Returns the appended
 * status_changed payloads for assertions. */
function withReleaseSeams(fn: (statusEvents: () => Array<{ status: string }>) => void): void {
  const Db = Database!;
  const dir = mkdtempSync(join(tmpdir(), 'stale-release-'));
  const db = new Db(join(dir, 'events.db'));
  db.pragma('journal_mode = WAL');
  db.exec(CREATE_SQL);
  __setEventLogForTest(db);
  __setSessionJsonPathForTest(join(dir, 'session.json'));
  try {
    const statusEvents = (): Array<{ status: string }> =>
      (
        db
          .prepare("SELECT payload FROM events WHERE session_id = 'oc-stale' AND type = 'session.status_changed' ORDER BY seq")
          .all() as Array<{ payload: string }>
      ).map((r) => JSON.parse(r.payload) as { status: string });
    fn(statusEvents);
  } finally {
    __setSessionJsonPathForTest(null);
    __setEventLogForTest(null);
    try {
      db.close();
    } catch {
      /* may already be closed */
    }
    rmSync(dir, { recursive: true, force: true });
  }
}

test('fresh synthetic busy counts as an active turn (reaper stays off)', () => {
  const reg = busyOpencodeRegistry();
  reg.noteObserverEvent(); // observer just mapped an event → clock fresh
  const s = reg.idleStatus();
  assert.equal(s.turnActive, true, 'fresh synthetic busy is active');
  assert.equal(s.idleSince, undefined, 'no idleSince while active — reaper waits');
});

test('stale synthetic busy releases for real: idle status + status_changed emitted (L8b)', { skip }, () => {
  withReleaseSeams((statusEvents) => {
    const reg = busyOpencodeRegistry();
    reg.noteObserverEvent(staleAgo()); // last observer event was 6 min ago → releases here
    assert.equal(reg.state.status, 'idle', 'the stale busy is actually flipped to idle, not just idle-eligible');
    assert.deepEqual(statusEvents(), [{ status: 'idle' }], 'the release emits session.status_changed via the standard setStatus path');
    const s = reg.idleStatus();
    assert.equal(s.turnActive, false, 'stale synthetic busy no longer counts as an active turn');
    assert.ok(s.idleSince, 'idleSince is set so the reaper can suspend');
  });
});

test('stale release also fires while a client is attached; reaper eligibility still waits (L8b)', { skip }, () => {
  withReleaseSeams((statusEvents) => {
    const reg = busyOpencodeRegistry();
    try {
      setExternalActivityProbe(() => true); // an opencode attach client is live
      reg.setExternalActivity(); // attached BEFORE staleness is noticed
      reg.noteObserverEvent(staleAgo());
      // The dashboard correction happens regardless of attachment…
      assert.equal(reg.state.status, 'idle', 'attached sessions get the status release too');
      assert.deepEqual(statusEvents(), [{ status: 'idle' }]);
      // …but the reaper side effect is unchanged: attached ⇒ not idle-eligible.
      const s = reg.idleStatus(); // samples the probe → setExternalActivity
      assert.equal(s.idleSince, undefined, 'attached client keeps the session non-idle even after the release');
    } finally {
      setExternalActivityProbe(null);
    }
  });
});

test('a real runner turn is never treated as a stale synthetic busy', () => {
  const reg = busyOpencodeRegistry();
  reg.registerTurn('turn-2', 'hello'); // a genuine /turns turn → activeTurns > 0
  reg.noteObserverEvent(staleAgo()); // even with a stale observer clock…
  const s = reg.idleStatus();
  assert.equal(s.turnActive, true, 'a registered runner turn stays active regardless of the observer clock');
  assert.equal(s.idleSince, undefined);
});

// --- Fix 2: interruptedTurns GC -------------------------------------------

function fakeObserverDeps(): ObserverDeps {
  return {
    sessionId: () => 'oc-stale',
    ocSession: () => 'ses_oc',
    activeTurnsSize: () => 0,
    nextTurnId: () => 'turn-gc', // distinct id so the assertion is collision-free
    setLastTurn: () => {},
    setExternalActivity: () => {},
    noteObserverEvent: () => {},
    setStatus: () => {},
    setModel: () => {},
    emit: () => {},
    audit: () => {},
  };
}

function assistantMessage(): Event {
  return {
    type: 'message.updated',
    properties: { info: { id: 'm1', sessionID: 'ses_oc', role: 'assistant', providerID: 'opencode', modelID: 'big-pickle' } },
  } as unknown as Event;
}

test('reset() sheds the interruptedTurns marker instead of leaking it (GC)', () => {
  const h = createObserverHandler(fakeObserverDeps());
  h.handle(assistantMessage()); // opens a cycle → activeTurnId = 'turn-gc'
  assert.equal(h.cycleActive, true);

  markObservedTurnInterrupted('turn-gc'); // CLI interrupt marks the live turn
  assert.equal(hasInterruptedTurn('turn-gc'), true, 'the interrupt is tracked');

  h.reset(); // stream drops before the turn's session.idle arrives
  assert.equal(h.cycleActive, false);
  assert.equal(hasInterruptedTurn('turn-gc'), false, 'reset()/endCycle() GC the marker — no leak');
});

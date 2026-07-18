// [V39] sumTokens must count only each turn's TERMINAL (result) usage row, not
// the per-assistant-message rows. Every turn emits one usage.updated per
// assistant message (mapping.ts emitUsage) plus a final aggregate row
// (emitResultUsage) — summing all of them double-counts and trips the autopilot
// token_budget at ~half its ceiling. The result row is the max-seq usage.updated
// within a turn, so sumTokens takes MAX(seq) per turn_id.
//
// Exercises the REAL src/events.ts (appendEvent + sumTokens) against a temp
// better-sqlite3 DB injected via __setEventLogForTest.
//
// GUARD: SKIPS cleanly when better-sqlite3's native addon is unavailable, UNLESS
// RUNNER_REQUIRE_SQLITE=1 (CI), via the shared sqlite-probe.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database, sqliteSkip as skip } from './sqlite-probe.js';
import type { EventType } from '../src/types.js';
import { appendEvent, sumTokens, __setEventLogForTest } from '../src/events.js';

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

function setupDb(): { cleanup: () => void } {
  const Db = Database!;
  const dir = mkdtempSync(join(tmpdir(), 'token-accounting-'));
  const db = new Db(join(dir, 'events.db'));
  db.pragma('journal_mode = WAL');
  db.exec(CREATE_SQL);
  __setEventLogForTest(db);
  return {
    cleanup(): void {
      __setEventLogForTest(null);
      try {
        db.close();
      } catch {
        /* may already be closed */
      }
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

const T = (t: string): EventType => t as EventType;

function usage(input: number, output: number, totalCostUsd: number): Record<string, unknown> {
  return { inputTokens: input, outputTokens: output, cacheReadTokens: 0, cacheWriteTokens: 0, totalCostUsd };
}

test('V39: sumTokens counts only the terminal usage row per turn (no double-count)', { skip }, () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-tok';
    // turn-1: two per-message rows (cost 0) then the terminal aggregate row.
    appendEvent(sid, 'turn-1', T('usage.updated'), usage(100, 10, 0));
    appendEvent(sid, 'turn-1', T('usage.updated'), usage(120, 12, 0));
    appendEvent(sid, 'turn-1', T('usage.updated'), usage(500, 50, 0.03)); // result aggregate
    // turn-2: one per-message row + a terminal row.
    appendEvent(sid, 'turn-2', T('usage.updated'), usage(80, 8, 0));
    appendEvent(sid, 'turn-2', T('usage.updated'), usage(300, 30, 0.02)); // result aggregate

    // Only the two terminal rows count: (500+50) + (300+30) = 880 — NOT the
    // per-message rows (which would inflate it well past 880).
    assert.equal(sumTokens(sid), 880);
  } finally {
    cleanup();
  }
});

test('V39: a turn with only per-message rows still contributes its last row', { skip }, () => {
  const { cleanup } = setupDb();
  try {
    const sid = 'sess-tok2';
    // An interrupted turn never emitted a result row — its last per-message row
    // is the best available estimate and must not be dropped (or double-counted).
    appendEvent(sid, 'turn-1', T('usage.updated'), usage(40, 4, 0));
    appendEvent(sid, 'turn-1', T('usage.updated'), usage(60, 6, 0));
    assert.equal(sumTokens(sid), 66); // only the max-seq row: 60 + 6
  } finally {
    cleanup();
  }
});

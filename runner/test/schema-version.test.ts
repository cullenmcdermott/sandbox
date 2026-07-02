// Read-compare-migrate contract for the events.db schema version (the HIGH from
// the 2026-07-01 review: SCHEMA_VERSION was write-only — stamped on every open,
// never read back, with no migration mechanism).
//
//   1. fresh db  → schema created, user_version stamped to SCHEMA_VERSION;
//   2. newer db  → migrateEventLog throws (a rolled-back runner must not
//      reinterpret state written by a newer one);
//   3. pre-versioning db (events table present, user_version 0) → treated as
//      v1, restamped, existing rows still readable.
//
// Uses REAL better-sqlite3 in-memory databases via the exported migrateEventLog
// (the production path constants point at /session, unwritable off-pod). Same
// native-addon guard as events.test.ts: CI installs with --ignore-scripts, so
// the suite must SKIP (not fail) when the compiled addon is absent.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { migrateEventLog, SCHEMA_VERSION } from '../src/events.js';

const require = createRequire(import.meta.url);

// Probe for the native addon by actually opening a db — a bare require() of the
// JS wrapper succeeds even when the compiled .node bindings are missing.
let Database: typeof import('better-sqlite3') | null = null;
let loadError: unknown;
try {
  const Db = require('better-sqlite3') as typeof import('better-sqlite3');
  new Db(':memory:').close();
  Database = Db;
} catch (err) {
  loadError = err;
}

const skip = Database
  ? false
  : `better-sqlite3 native addon unavailable: ${loadError instanceof Error ? loadError.message : String(loadError)}`;

test('fresh database gets the current schema and version stamp', { skip }, () => {
  const d = new Database!(':memory:');
  try {
    migrateEventLog(d);
    assert.equal(d.pragma('user_version', { simple: true }), SCHEMA_VERSION);
    // The events table exists and accepts the shape appendEvent writes.
    d.prepare(
      'INSERT INTO events (time, session_id, turn_id, type, payload) VALUES (?, ?, ?, ?, ?)',
    ).run('2026-07-01T00:00:00Z', 's1', null, 'session.started', '{}');
    const row = d.prepare('SELECT COUNT(*) AS n FROM events').get() as { n: number };
    assert.equal(row.n, 1);
  } finally {
    d.close();
  }
});

test('a database stamped newer than SCHEMA_VERSION is refused', { skip }, () => {
  const d = new Database!(':memory:');
  try {
    d.pragma(`user_version = ${SCHEMA_VERSION + 1}`);
    assert.throws(() => migrateEventLog(d), /newer than this runner supports/);
  } finally {
    d.close();
  }
});

test('re-opening at the same version is a no-op that keeps data', { skip }, () => {
  const d = new Database!(':memory:');
  try {
    migrateEventLog(d);
    d.prepare(
      'INSERT INTO events (time, session_id, turn_id, type, payload) VALUES (?, ?, ?, ?, ?)',
    ).run('2026-07-01T00:00:00Z', 's1', 'turn-1', 'message.completed', '{}');
    migrateEventLog(d); // second open
    assert.equal(d.pragma('user_version', { simple: true }), SCHEMA_VERSION);
    const row = d.prepare('SELECT COUNT(*) AS n FROM events').get() as { n: number };
    assert.equal(row.n, 1);
  } finally {
    d.close();
  }
});

test('pre-versioning db (events table, user_version 0) is treated as v1 and restamped', { skip }, () => {
  const d = new Database!(':memory:');
  try {
    // Simulate a db written before read-back versioning: v1 shape, version 0.
    d.exec(`
      CREATE TABLE events (
        seq        INTEGER PRIMARY KEY AUTOINCREMENT,
        time       TEXT    NOT NULL,
        session_id TEXT    NOT NULL,
        turn_id    TEXT,
        type       TEXT    NOT NULL,
        payload    TEXT    NOT NULL
      );
    `);
    d.prepare(
      'INSERT INTO events (time, session_id, turn_id, type, payload) VALUES (?, ?, ?, ?, ?)',
    ).run('2026-07-01T00:00:00Z', 's1', null, 'session.started', '{}');
    assert.equal(d.pragma('user_version', { simple: true }), 0);

    migrateEventLog(d);

    assert.equal(d.pragma('user_version', { simple: true }), SCHEMA_VERSION);
    const row = d.prepare('SELECT COUNT(*) AS n FROM events').get() as { n: number };
    assert.equal(row.n, 1);
  } finally {
    d.close();
  }
});

// Shared better-sqlite3 native-addon probe for the runner tests that exercise a
// REAL SQLite database (events.test.ts, schema-version.test.ts).
//
// better-sqlite3's JS wrapper require()s fine even when the compiled .node addon
// is absent — the bindings are located lazily inside the Database constructor —
// so we actually open + close an in-memory DB to detect a missing addon.
//
// Default behavior: export `sqliteSkip` (a node:test `{ skip }` reason string)
// so a suite SKIPS cleanly when the addon is unavailable (e.g. a bare
// `npm install --ignore-scripts` with no rebuild).
//
// GUARD: when RUNNER_REQUIRE_SQLITE=1, a missing addon THROWS here at import
// time so the importing suite FAILS LOUDLY instead of skipping. CI sets
// RUNNER_REQUIRE_SQLITE=1 after `npm rebuild better-sqlite3`, which makes it
// structurally impossible for the SQLite durability tests to silently self-skip:
// if the rebuild step ever breaks, CI goes red here instead of green-with-skips.

import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);

let db: typeof import('better-sqlite3') | null = null;
let loadError: unknown;
try {
  const Db = require('better-sqlite3') as typeof import('better-sqlite3');
  // Construct + close: throws "Could not locate the bindings file" when the
  // native addon was never built (an --ignore-scripts install with no rebuild).
  new Db(':memory:').close();
  db = Db;
} catch (err) {
  loadError = err;
}

const reason = loadError instanceof Error ? loadError.message : String(loadError);

if (db === null && process.env.RUNNER_REQUIRE_SQLITE === '1') {
  throw new Error(
    `RUNNER_REQUIRE_SQLITE=1 but the better-sqlite3 native addon failed to load: ${reason}. ` +
      'Build it with `npm rebuild better-sqlite3` before running the runner tests. ' +
      'This guard exists so the SQLite durability tests can never silently self-skip in CI.',
  );
}

/** The loaded better-sqlite3 constructor, or null when the addon is unavailable. */
export const Database = db;

/**
 * node:test `{ skip }` value: `false` to run, or a reason string to skip when
 * the native addon is unavailable and NOT required (RUNNER_REQUIRE_SQLITE!=1).
 */
export const sqliteSkip: false | string = db
  ? false
  : `better-sqlite3 native addon unavailable: ${reason}`;

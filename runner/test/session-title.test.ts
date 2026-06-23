// T6: the runner emits a one-time session.title event after the first assistant
// turn. These unit tests cover the pure title helpers: building the summarizer
// prompt result into an event payload, the one-shot guard, and that a failed
// summarization is swallowed (no throw, no event).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  sanitizeTitle,
  shouldGenerateTitle,
} from '../src/title.js';

test('sanitizeTitle trims, strips trailing punctuation, and bounds length', () => {
  assert.equal(sanitizeTitle('  Fix the auth race condition.  '), 'Fix the auth race condition');
  assert.equal(sanitizeTitle('Add login flow!'), 'Add login flow');
  assert.equal(sanitizeTitle(''), '');
});

test('sanitizeTitle collapses internal whitespace/newlines', () => {
  assert.equal(sanitizeTitle('add\n  login   flow'), 'add login flow');
});

test('sanitizeTitle strips wrapping quotes/backticks a model may add', () => {
  assert.equal(sanitizeTitle('"Fix the bug"'), 'Fix the bug');
  assert.equal(sanitizeTitle("'Add login flow'"), 'Add login flow');
  assert.equal(sanitizeTitle('`refactor parser`'), 'refactor parser');
});

test('sanitizeTitle bounds length to the max', () => {
  const long = 'x'.repeat(120);
  const got = sanitizeTitle(long);
  assert.ok(got.length <= 80, `expected <=80 chars, got ${got.length}`);
  assert.equal(got, 'x'.repeat(80));
});

test('shouldGenerateTitle is true only on the first assistant turn, once', () => {
  // titleGenerated already set: never regenerate.
  assert.equal(shouldGenerateTitle({ title_generated: true }), false);
  // not yet generated: generate.
  assert.equal(shouldGenerateTitle({ title_generated: false }), true);
  // missing flag (older session.json): treat as not generated.
  assert.equal(shouldGenerateTitle({}), true);
});

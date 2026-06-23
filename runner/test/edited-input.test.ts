// Regression for C8: a malformed editedInput must never throw inside the
// permission resolver. JSON.parse(editedInput) was unguarded, so an invalid
// edit threw before the canUseTool promise resolved — hanging the turn forever
// (activeTurns never drains, idleSince never set, the reaper can't suspend the
// pod). parseEditedInput must instead return undefined so the caller falls back
// to the original tool input.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseEditedInput } from '../src/claude.js';

test('parseEditedInput returns undefined for malformed JSON (never throws) — C8', () => {
  assert.equal(parseEditedInput('not valid json{'), undefined);
  assert.equal(parseEditedInput('{"unterminated":'), undefined);
  assert.equal(parseEditedInput(']['), undefined);
});

test('parseEditedInput parses valid JSON objects', () => {
  assert.deepEqual(parseEditedInput('{"command":"ls -la"}'), { command: 'ls -la' });
});

test('parseEditedInput treats empty/undefined as no edit', () => {
  assert.equal(parseEditedInput(undefined), undefined);
  assert.equal(parseEditedInput(''), undefined);
});

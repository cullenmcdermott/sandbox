// D10: pin that the TS TurnRequestBody mirrors the Go session.TurnInput
// (internal/session/types.go). TypeScript interfaces are erased at runtime, so the
// real gate is `tsc --noEmit` accepting this fully-populated literal — a dropped or
// renamed field fails the typecheck. The runtime asserts below just keep it a live
// test node. `advisor` was previously absent from the mirror; `resume` is the
// backend session id (its Go type `session.TurnID` is corrected under §8).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { TurnRequestBody } from '../src/types.js';

test('TurnRequestBody mirrors every Go TurnInput field (D10)', () => {
  // A value using EVERY field the Go type declares (prompt, resume, allowedTools,
  // mode, model, effort, advisor). Omitting any is legal (all but prompt are
  // optional), but naming one the interface lacks would fail tsc.
  const body: TurnRequestBody = {
    prompt: 'do the thing',
    resume: 'claude-session-uuid', // backend session id, not a TurnID
    allowedTools: ['Read', 'Bash'],
    mode: 'acceptEdits',
    model: 'opus',
    effort: 'high',
    advisor: true,
  };

  assert.equal(body.advisor, true, 'advisor is part of the mirror (D10)');
  assert.equal(body.resume, 'claude-session-uuid');
  assert.deepEqual(body.allowedTools, ['Read', 'Bash']);
});

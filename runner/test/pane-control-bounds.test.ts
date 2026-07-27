// [S5] pane control-frame bounds. The pane WebSocket is authenticated, so this
// is not a perimeter control — it bounds what ONE authenticated-but-buggy or
// hostile client can make the runner allocate on its behalf.

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { parsePaneControl, MAX_PANE_DIMENSION, MAX_PANE_FRAME_BYTES } from '../src/server.js';

test('a normal resize parses', () => {
  assert.deepEqual(parsePaneControl('{"type":"resize","cols":120,"rows":40}'), {
    type: 'resize',
    cols: 120,
    rows: 40,
  });
});

test('a resize at the maximum dimension is still accepted', () => {
  const at = `{"type":"resize","cols":${MAX_PANE_DIMENSION},"rows":${MAX_PANE_DIMENSION}}`;
  assert.deepEqual(parsePaneControl(at), {
    type: 'resize',
    cols: MAX_PANE_DIMENSION,
    rows: MAX_PANE_DIMENSION,
  });
});

test('an over-large dimension is rejected, not clamped', () => {
  // Dropped rather than clamped on purpose: silently honoring half of a
  // confused client's request leaves the two ends disagreeing about pane size.
  const over = MAX_PANE_DIMENSION + 1;
  assert.equal(parsePaneControl(`{"type":"resize","cols":${over},"rows":40}`), null);
  assert.equal(parsePaneControl(`{"type":"resize","cols":120,"rows":${over}}`), null);
  // The case that motivated the bound: a 2-billion-column pty allocation.
  assert.equal(parsePaneControl('{"type":"resize","cols":2147483647,"rows":2147483647}'), null);
});

test('the pre-existing lower bound and shape checks still hold', () => {
  assert.equal(parsePaneControl('{"type":"resize","cols":0,"rows":40}'), null);
  assert.equal(parsePaneControl('{"type":"resize","cols":-1,"rows":40}'), null);
  assert.equal(parsePaneControl('{"type":"resize","cols":1.5,"rows":40}'), null);
  assert.equal(parsePaneControl('{"type":"resize","cols":"120","rows":40}'), null);
  assert.equal(parsePaneControl('{"type":"nope"}'), null);
  assert.equal(parsePaneControl('not json'), null);
});

test('the inbound frame cap is well under the ws default of 100 MiB', () => {
  assert.ok(MAX_PANE_FRAME_BYTES > 0);
  assert.ok(MAX_PANE_FRAME_BYTES < 100 * 1024 * 1024);
});

// [S5] pane control-frame bounds. The pane WebSocket is authenticated, so this
// is not a perimeter control — it bounds what ONE authenticated-but-buggy or
// hostile client can make the runner allocate on its behalf.

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import {
  parsePaneControl,
  parsePaneGeometry,
  MAX_PANE_DIMENSION,
  MAX_PANE_FRAME_BYTES,
} from '../src/server.js';

const geom = (qs: string): { cols: number; rows: number } | null =>
  parsePaneGeometry(new URL(`http://localhost/sessions/s/pane${qs}`));

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

// [R4] The upgrade URL is a second, earlier path from client geometry to a PTY
// ioctl, so it carries exactly the same bounds as the control frame above.
test('upgrade-URL geometry parses and honours the same bounds', () => {
  assert.deepEqual(geom('?cols=203&rows=51'), { cols: 203, rows: 51 });
  assert.deepEqual(geom('?traceId=abc&cols=120&rows=40'), { cols: 120, rows: 40 });
  assert.deepEqual(geom(`?cols=${MAX_PANE_DIMENSION}&rows=${MAX_PANE_DIMENSION}`), {
    cols: MAX_PANE_DIMENSION,
    rows: MAX_PANE_DIMENSION,
  });
});

test('absent or malformed upgrade-URL geometry falls back to null, never a bad ioctl', () => {
  const over = MAX_PANE_DIMENSION + 1;
  assert.equal(geom(''), null, 'no params → keep the default geometry');
  assert.equal(geom('?cols=120'), null, 'half a size is not a size');
  assert.equal(geom('?rows=40'), null);
  assert.equal(geom(`?cols=${over}&rows=40`), null);
  assert.equal(geom(`?cols=120&rows=${over}`), null);
  assert.equal(geom('?cols=0&rows=40'), null);
  assert.equal(geom('?cols=-1&rows=40'), null);
  assert.equal(geom('?cols=1.5&rows=40'), null);
  assert.equal(geom('?cols=abc&rows=40'), null);
  assert.equal(geom('?cols=&rows='), null);
  assert.equal(geom('?cols=2147483647&rows=2147483647'), null);
});

test('the inbound frame cap is well under the ws default of 100 MiB', () => {
  assert.ok(MAX_PANE_FRAME_BYTES > 0);
  assert.ok(MAX_PANE_FRAME_BYTES < 100 * 1024 * 1024);
});

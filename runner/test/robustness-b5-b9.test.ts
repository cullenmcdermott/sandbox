// Unit tests for the §1f robustness fixes B5, B6, B8, B9. Each is exercised
// through the pure, exported decision/parse function the route (or emitter)
// delegates to, mirroring turn-gate.test.ts's approach — the HTTP router itself
// is dark (review F4), so we don't boot a server. B7 lives in session-corrupt.test.ts.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { clampAfterSeq, permissionResolveResponse } from '../src/server.js';
import { parseAheadBehind } from '../src/claude.js';
import { readBody, BodyTooLargeError, InvalidJsonError } from '../src/httputil.js';

// --- B5: SSE `after` cursor clamp ----------------------------------------

test('B5: after beyond the head clamps to lastSeq (tail live from head)', () => {
  // A bogus cursor (client bug, or a log truncated by a pod rebuild that reset
  // AUTOINCREMENT) must not swallow every live event — clamp to the real head.
  assert.equal(clampAfterSeq(9999, 12), 12);
});

test('B5: after at or below the head is returned unchanged (normal replay)', () => {
  assert.equal(clampAfterSeq(5, 12), 5);
  assert.equal(clampAfterSeq(12, 12), 12);
  assert.equal(clampAfterSeq(0, 12), 0);
});

test('B5: after against an empty log (lastSeq 0) clamps to 0', () => {
  assert.equal(clampAfterSeq(7, 0), 0);
  assert.equal(clampAfterSeq(0, 0), 0);
});

// --- B6: ahead/behind parse pinned across the sync→async git move --------

test('B6: parseAheadBehind reads "<behind>\\t<ahead>"', () => {
  assert.deepEqual(parseAheadBehind('2\t5'), { behind: 2, ahead: 5 });
  assert.deepEqual(parseAheadBehind('0 0'), { behind: 0, ahead: 0 });
});

test('B6: parseAheadBehind yields {0,0} for empty/unparseable output (no upstream)', () => {
  assert.deepEqual(parseAheadBehind(''), { ahead: 0, behind: 0 });
  assert.deepEqual(parseAheadBehind('fatal: no upstream'), { ahead: 0, behind: 0 });
});

// --- B8: honest permission-resolve outcome (first-write-wins) ------------

test('B8: a winning resolution reports 200 resolved:true (unchanged shape)', () => {
  assert.deepEqual(permissionResolveResponse('perm-1', true), {
    status: 200,
    body: { permissionId: 'perm-1', resolved: true },
  });
});

test('B8: a lost race reports 409 resolved:false with reason:expired', () => {
  const { status, body } = permissionResolveResponse('perm-1', false);
  assert.equal(status, 409);
  assert.equal(body.resolved, false);
  assert.equal(body.reason, 'expired');
  assert.equal(body.permissionId, 'perm-1');
  // The Go client (internal/runner/client.go) surfaces non-200/204 via the
  // {error} field — pin that it's a non-empty string so the race is a visible
  // error, not a silent lie.
  assert.equal(typeof body.error, 'string');
  assert.ok((body.error as string).length > 0);
});

// --- B9: distinguishable request-body failure modes ----------------------

class FakeReq extends EventEmitter {
  destroy(): void {
    /* readBody calls this on the too-large path; nothing to tear down here */
  }
}

test('B9: an oversized body rejects with BodyTooLargeError (→ 413)', async () => {
  const req = new FakeReq();
  const p = readBody(req as never);
  // One chunk past the 1 MiB cap trips the size guard.
  req.emit('data', Buffer.alloc((1 << 20) + 1));
  await assert.rejects(p, (e) => e instanceof BodyTooLargeError);
});

test('B9: malformed JSON rejects with InvalidJsonError (→ 400)', async () => {
  const req = new FakeReq();
  const p = readBody(req as never);
  req.emit('data', Buffer.from('{not valid json'));
  req.emit('end');
  await assert.rejects(p, (e) => e instanceof InvalidJsonError);
});

test('B9: valid JSON resolves the parsed body; empty body resolves null', async () => {
  const good = new FakeReq();
  const gp = readBody<{ a: number }>(good as never);
  good.emit('data', Buffer.from('{"a":1}'));
  good.emit('end');
  assert.deepEqual(await gp, { a: 1 });

  const empty = new FakeReq();
  const ep = readBody(empty as never);
  empty.emit('end');
  assert.equal(await ep, null);
});

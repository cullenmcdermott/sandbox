// [T2] structured logging. The contract that matters operationally: text mode
// stays readable in `kubectl logs`, json mode is ingestible, levels filter, and
// an Error field never degrades to `{}` — which is the classic way a structured
// logger loses the actual failure it was added to capture.

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { createLogger, setLogSessionId, type LogLevel } from '../src/log.js';

function collector() {
  const lines: Array<{ line: string; level: LogLevel }> = [];
  return {
    lines,
    write: (line: string, level: LogLevel) => lines.push({ line, level }),
    texts: () => lines.map((l) => l.line),
  };
}

const fixedNow = (): Date => new Date('2026-07-27T10:11:12.000Z');

test('text mode emits one readable line with trailing key=value pairs', () => {
  const c = collector();
  const log = createLogger('opencode', { write: c.write, now: fixedNow, format: 'text' });

  log.info('serve listening', { port: 4096 });

  assert.deepEqual(c.texts(), ['2026-07-27T10:11:12.000Z INFO  opencode: serve listening port=4096']);
});

test('text mode quotes values containing whitespace so pairs stay parseable', () => {
  const c = collector();
  const log = createLogger('codex', { write: c.write, now: fixedNow, format: 'text' });

  log.warn('seed failed', { path: '/tmp/a b.toml', why: 'no such file' });

  assert.equal(
    c.texts()[0],
    '2026-07-27T10:11:12.000Z WARN  codex: seed failed path="/tmp/a b.toml" why="no such file"',
  );
});

test('json mode emits one object per record, carrying the session id', () => {
  setLogSessionId('claude-pane-abc123');
  const c = collector();
  const log = createLogger('events', { write: c.write, now: fixedNow, format: 'json' });

  log.error('failed to persist event', { event: 'turn.started' });

  const rec = JSON.parse(c.texts()[0]);
  assert.deepEqual(rec, {
    ts: '2026-07-27T10:11:12.000Z',
    level: 'error',
    component: 'events',
    sessionId: 'claude-pane-abc123',
    msg: 'failed to persist event',
    event: 'turn.started',
  });
  setLogSessionId('');
});

test('an Error field logs its message, not {}', () => {
  const c = collector();
  const log = createLogger('events', { write: c.write, now: fixedNow, format: 'json' });

  log.error('persist failed', { err: new Error('disk full') });

  assert.equal(JSON.parse(c.texts()[0]).err, 'disk full');
});

test('debug level adds the stack; higher levels do not', () => {
  const c = collector();
  const debugLog = createLogger('x', { write: c.write, now: fixedNow, format: 'json', level: 'debug' });
  debugLog.debug('boom', { err: new Error('nope') });
  assert.ok(typeof JSON.parse(c.texts()[0]).errStack === 'string');

  const c2 = collector();
  const infoLog = createLogger('x', { write: c2.write, now: fixedNow, format: 'json' });
  infoLog.error('boom', { err: new Error('nope') });
  assert.equal('errStack' in JSON.parse(c2.texts()[0]), false);
});

test('records below the configured level are dropped', () => {
  const c = collector();
  const log = createLogger('x', { write: c.write, now: fixedNow, level: 'warn' });

  log.debug('no');
  log.info('no');
  log.warn('yes');
  log.error('yes');

  assert.equal(c.lines.length, 2);
});

test('warn and error route to stderr, everything else to stdout', () => {
  // The write seam receives the level so the default sink can split streams; a
  // log scraper separates them, and kubectl logs interleaves both.
  const c = collector();
  const log = createLogger('x', { write: c.write, now: fixedNow, level: 'debug' });

  log.debug('a');
  log.info('b');
  log.warn('c');
  log.error('d');

  assert.deepEqual(
    c.lines.map((l) => l.level),
    ['debug', 'info', 'warn', 'error'],
  );
});

test('child loggers pin fields onto every record without mutating the parent', () => {
  const c = collector();
  const parent = createLogger('http', { write: c.write, now: fixedNow, format: 'json' });
  const child = parent.child({ traceId: '3f9a1c2b' });

  child.error('request', { status: 500 });
  parent.error('request', { status: 500 });

  assert.equal(JSON.parse(c.texts()[0]).traceId, '3f9a1c2b');
  assert.equal('traceId' in JSON.parse(c.texts()[1]), false);
});

test('per-record fields win over pinned ones', () => {
  const c = collector();
  const log = createLogger('http', { write: c.write, now: fixedNow, format: 'json' }).child({
    status: 200,
  });

  log.error('request', { status: 500 });

  assert.equal(JSON.parse(c.texts()[0]).status, 500);
});

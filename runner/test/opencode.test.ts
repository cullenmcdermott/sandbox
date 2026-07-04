// Unit tests for the opencode activity signal (Seam B): parsing /proc/net/tcp
// to count ESTABLISHED connections on the opencode port. Uses an injected
// reader so it runs anywhere (not just Linux).

import { test, mock } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import type { ChildProcess } from 'node:child_process';
import {
  establishedConnections,
  externalClientConnections,
  runnerOwnedConnections,
  buildOpencodeConfig,
  startOpencodeSupervisor,
  STOP_GRACE_MS,
} from '../src/opencode.js';

// A controllable stand-in for the spawned child. The supervisor uses .once,
// .kill, .exitCode and .signalCode. kill() RECORDS the signal but does NOT
// auto-exit, so tests drive the exit explicitly via .emit('exit', …).
interface FakeChild extends ChildProcess {
  signals: string[];
}
function fakeChild(): FakeChild {
  const ee = new EventEmitter() as unknown as FakeChild;
  ee.signals = [];
  (ee as { exitCode: number | null }).exitCode = null;
  (ee as { signalCode: string | null }).signalCode = null;
  (ee as unknown as { kill: (s?: NodeJS.Signals | number) => boolean }).kill = (s) => {
    ee.signals.push(String(s ?? 'SIGTERM'));
    return true;
  };
  return ee;
}

type SpawnFn = typeof import('node:child_process').spawn;

// Port 4096 = 0x1000. State 01 = ESTABLISHED; 0A = LISTEN.
const PROC_TCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid
   0: 00000000:1000 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0
   1: 0100007F:1000 0100007F:C001 01 00000000:00000000 00:00000000 00000000     0
   2: 0100007F:1F90 0100007F:C002 01 00000000:00000000 00:00000000 00000000     0
`;

test('counts only ESTABLISHED connections on the target port', () => {
  const reader = (p: string) => (p === '/proc/net/tcp' ? PROC_TCP : (() => { throw new Error('no tcp6'); })());
  assert.equal(establishedConnections(4096, reader as typeof import('node:fs').readFileSync), 1);
});

test('external client count subtracts runner-owned opencode sockets', () => {
  const observerOnly = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid timeout inode
   0: 0100007F:1000 0100007F:C001 01 0 0 0 0 0 222
   1: 0100007F:C001 0100007F:1000 01 0 0 0 0 0 111
`;
  const observerPlusAttach = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid timeout inode
   0: 0100007F:1000 0100007F:C001 01 0 0 0 0 0 222
   1: 0100007F:C001 0100007F:1000 01 0 0 0 0 0 111
   2: 0100007F:1000 0100007F:C002 01 0 0 0 0 0 333
`;
  const one = (p: string) => (p === '/proc/net/tcp' ? observerOnly : (() => { throw new Error('no tcp6'); })());
  const two = (p: string) => (p === '/proc/net/tcp' ? observerPlusAttach : (() => { throw new Error('no tcp6'); })());
  const readdir = () => ['3'] as string[];
  const readlink = () => 'socket:[111]';
  assert.equal(runnerOwnedConnections(4096, one as typeof import('node:fs').readFileSync, readdir as typeof import('node:fs').readdirSync, readlink as typeof import('node:fs').readlinkSync), 1);
  assert.equal(externalClientConnections(4096, one as typeof import('node:fs').readFileSync, readdir as typeof import('node:fs').readdirSync, readlink as typeof import('node:fs').readlinkSync), 0);
  assert.equal(externalClientConnections(4096, two as typeof import('node:fs').readFileSync, readdir as typeof import('node:fs').readdirSync, readlink as typeof import('node:fs').readlinkSync), 1);
});

test('runner-owned socket scan tolerates disappearing fds', () => {
  const proc = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid timeout inode
   0: 0100007F:1000 0100007F:C001 01 0 0 0 0 0 222
   1: 0100007F:C001 0100007F:1000 01 0 0 0 0 0 111
`;
  const reader = (p: string) => (p === '/proc/net/tcp' ? proc : (() => { throw new Error('no tcp6'); })());
  const readdir = () => ['3', '4'] as string[];
  const readlink = (p: string) => {
    if (p.endsWith('/3')) throw new Error('ENOENT');
    return 'socket:[111]';
  };
  assert.equal(runnerOwnedConnections(4096, reader as typeof import('node:fs').readFileSync, readdir as typeof import('node:fs').readdirSync, readlink as typeof import('node:fs').readlinkSync), 1);
  assert.equal(externalClientConnections(4096, reader as typeof import('node:fs').readFileSync, readdir as typeof import('node:fs').readdirSync, readlink as typeof import('node:fs').readlinkSync), 0);
});

test('returns 0 when the port has only a LISTEN socket', () => {
  const listenOnly = `  sl  local_address rem_address   st\n   0: 00000000:1000 00000000:0000 0A 0\n`;
  const reader = (p: string) => (p === '/proc/net/tcp' ? listenOnly : (() => { throw new Error('no tcp6'); })());
  assert.equal(establishedConnections(4096, reader as typeof import('node:fs').readFileSync), 0);
});

test('missing proc files yield 0, not a throw', () => {
  const reader = () => { throw new Error('ENOENT'); };
  assert.equal(establishedConnections(4096, reader as unknown as typeof import('node:fs').readFileSync), 0);
});

test('buildOpencodeConfig enables only providers present in env', () => {
  const cfg = buildOpencodeConfig({ ANTHROPIC_API_KEY: 'x', OPENCODE_DEFAULT_MODEL: 'kimi' } as NodeJS.ProcessEnv);
  assert.ok((cfg.provider as Record<string, unknown>).anthropic);
  assert.equal((cfg.provider as Record<string, unknown>).openai, undefined);
  assert.equal(cfg.model, 'kimi');
});

// REGRESSION (O3): the supervisor must FAIL CLOSED rather than bind an
// unauthenticated agent-with-shell to 0.0.0.0. Without OPENCODE_SERVER_PASSWORD
// it must throw before spawning; with it, it spawns `opencode serve` bound to
// the configured port. The previous code spawned unconditionally.
test('refuses to start opencode serve without a server password', () => {
  let spawned = 0;
  const spy = (() => { spawned++; return fakeChild(); }) as unknown as typeof import('node:child_process').spawn;
  assert.throws(
    () => startOpencodeSupervisor({ OPENCODE_PORT: '4096' } as NodeJS.ProcessEnv, spy),
    /OPENCODE_SERVER_PASSWORD is unset/,
  );
  assert.equal(spawned, 0, 'must not spawn opencode serve when unauthenticated');
});

test('starts opencode serve when a server password is present', async () => {
  const calls: Array<{ cmd: string; args: string[] }> = [];
  let child: FakeChild | undefined;
  const spy = ((cmd: string, args: string[]) => { calls.push({ cmd, args }); child = fakeChild(); return child; }) as unknown as SpawnFn;
  const sup = startOpencodeSupervisor(
    { OPENCODE_PORT: '4096', OPENCODE_SERVER_PASSWORD: 's3cret' } as NodeJS.ProcessEnv,
    spy,
  );
  try {
    assert.equal(calls.length, 1);
    assert.equal(calls[0].cmd, 'opencode');
    assert.deepEqual(calls[0].args, ['serve', '--hostname', '0.0.0.0', '--port', '4096']);
  } finally {
    const stopped = sup.stop(); // clears the activity interval + SIGTERMs the child
    child?.emit('exit', 0, null); // simulate the child exiting on SIGTERM
    await stopped;
  }
});

// REGRESSION (O5): stop() must AWAIT the child's exit (the old stop() returned
// void, so shutdown could process.exit while `opencode serve` was still alive →
// orphaned child). The returned promise must stay pending until the child exits.
test('stop() SIGTERMs the child and resolves only after it exits', async () => {
  const child = fakeChild();
  const sup = startOpencodeSupervisor(
    { OPENCODE_PORT: '4096', OPENCODE_SERVER_PASSWORD: 's3cret' } as NodeJS.ProcessEnv,
    (() => child) as unknown as SpawnFn,
  );
  let resolved = false;
  const p = sup.stop().then(() => { resolved = true; });
  assert.deepEqual(child.signals, ['SIGTERM'], 'stop() must SIGTERM the child');
  await Promise.resolve(); // flush microtasks
  assert.equal(resolved, false, 'stop() must not resolve before the child exits');
  child.emit('exit', 0, null);
  await p;
  assert.equal(resolved, true, 'stop() must resolve once the child exits');
});

// REGRESSION (O5): if the child ignores SIGTERM, stop() escalates to SIGKILL
// after STOP_GRACE_MS so shutdown never blocks past the pod grace period.
test('stop() escalates to SIGKILL when the child ignores SIGTERM', async () => {
  mock.timers.enable({ apis: ['setTimeout'] });
  try {
    const child = fakeChild();
    const sup = startOpencodeSupervisor(
      { OPENCODE_PORT: '4096', OPENCODE_SERVER_PASSWORD: 's3cret' } as NodeJS.ProcessEnv,
      (() => child) as unknown as SpawnFn,
    );
    const p = sup.stop();
    assert.deepEqual(child.signals, ['SIGTERM']);
    mock.timers.tick(STOP_GRACE_MS);
    assert.ok(child.signals.includes('SIGKILL'), 'should SIGKILL after the grace period');
    child.emit('exit', null, 'SIGKILL'); // the forced kill lands
    await p;
  } finally {
    mock.timers.reset();
  }
});

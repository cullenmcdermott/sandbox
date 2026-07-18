// Unit tests for the codex-app-server backend: the supervisor (spawn args,
// restart-on-exit, stop/kill), the fail-closed auth materialization, the child
// env sanitization, and the passive observer's frame-mapping core. All use
// injected fakes — no real `codex` binary, no filesystem, no sockets.

import { test, mock } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import type { ChildProcess } from 'node:child_process';
import {
  buildCodexServeEnv,
  materializeCodexAuth,
  startCodexSupervisor,
  STOP_GRACE_MS,
  type CodexAuthFs,
} from '../src/codex.js';
import { createCodexObserverHandler, type CodexObserverDeps } from '../src/codex-observer.js';

// A controllable stand-in for the spawned child (copied from opencode.test.ts):
// the supervisor uses .once, .kill, .exitCode and .signalCode. kill() RECORDS the
// signal but does NOT auto-exit, so tests drive the exit explicitly via emit.
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

// --- supervisor: spawn args ------------------------------------------------

test('starts `codex app-server` on the loopback ws listen addr', async () => {
  const calls: Array<{ cmd: string; args: string[] }> = [];
  let child: FakeChild | undefined;
  const spy = ((cmd: string, args: string[]) => {
    calls.push({ cmd, args });
    child = fakeChild();
    return child;
  }) as unknown as SpawnFn;
  const sup = startCodexSupervisor({ CODEX_PORT: '8788' } as NodeJS.ProcessEnv, spy);
  try {
    assert.equal(calls.length, 1);
    assert.equal(calls[0].cmd, 'codex');
    assert.deepEqual(calls[0].args, ['app-server', '--listen', 'ws://127.0.0.1:8788']);
  } finally {
    const stopped = sup.stop();
    child?.emit('exit', 0, null);
    await stopped;
  }
});

// REGRESSION (B1): a spawn failure emits 'error' (with NO 'exit'); without a
// listener Node re-throws it and kills the runner. The supervisor must catch it
// and schedule exactly one backoff respawn (error + a late exit on the same child
// must not double-respawn).
test('a spawn error does not crash the supervisor and schedules exactly one respawn', async () => {
  mock.timers.enable({ apis: ['setTimeout'] });
  const children: FakeChild[] = [];
  const spy = (() => {
    const c = fakeChild();
    children.push(c);
    return c;
  }) as unknown as SpawnFn;
  const sup = startCodexSupervisor({ CODEX_PORT: '8788' } as NodeJS.ProcessEnv, spy);
  try {
    assert.equal(children.length, 1);
    assert.doesNotThrow(() => children[0].emit('error', new Error('spawn codex ENOENT')));
    children[0].emit('exit', null, 'SIGKILL'); // a late exit on the SAME child
    mock.timers.tick(1000);
    assert.equal(children.length, 2, 'error+exit on one child must respawn exactly once');
  } finally {
    const stopped = sup.stop();
    children[children.length - 1].emit('exit', 0, null);
    await stopped;
    mock.timers.reset();
  }
});

// REGRESSION (O5): stop() must AWAIT the child's exit so shutdown never
// process.exits while `codex app-server` is still alive (orphaned child).
test('stop() SIGTERMs the child and resolves only after it exits', async () => {
  const child = fakeChild();
  const sup = startCodexSupervisor(
    { CODEX_PORT: '8788' } as NodeJS.ProcessEnv,
    (() => child) as unknown as SpawnFn,
  );
  let resolved = false;
  const p = sup.stop().then(() => {
    resolved = true;
  });
  assert.deepEqual(child.signals, ['SIGTERM'], 'stop() must SIGTERM the child');
  await Promise.resolve();
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
    const sup = startCodexSupervisor(
      { CODEX_PORT: '8788' } as NodeJS.ProcessEnv,
      (() => child) as unknown as SpawnFn,
    );
    const p = sup.stop();
    assert.deepEqual(child.signals, ['SIGTERM']);
    mock.timers.tick(STOP_GRACE_MS);
    assert.ok(child.signals.includes('SIGKILL'), 'should SIGKILL after the grace period');
    child.emit('exit', null, 'SIGKILL');
    await p;
  } finally {
    mock.timers.reset();
  }
});

// --- materializeCodexAuth --------------------------------------------------

/** A fake CodexAuthFs backed by an in-memory file map, recording writes. */
function fakeAuthFs(files: Record<string, string> = {}): {
  fs: CodexAuthFs;
  writes: Array<{ path: string; content: string; mode?: number }>;
  files: Record<string, string>;
} {
  const writes: Array<{ path: string; content: string; mode?: number }> = [];
  const fs: CodexAuthFs = {
    readFileSync: ((p: string) => {
      if (p in files) return files[p];
      throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' });
    }) as CodexAuthFs['readFileSync'],
    writeFileSync: ((p: string, content: string, opts?: { mode?: number }) => {
      writes.push({ path: String(p), content: String(content), mode: opts?.mode });
      files[String(p)] = String(content);
    }) as CodexAuthFs['writeFileSync'],
    mkdirSync: (() => undefined) as CodexAuthFs['mkdirSync'],
  };
  return { fs, writes, files };
}

// FAIL-CLOSED: no auth.json seed AND no OPENAI_API_KEY must throw before the pod
// comes up (a codex pod with no credential fails every turn opaquely).
test('materializeCodexAuth fails closed with neither auth.json nor OPENAI_API_KEY', () => {
  const { fs } = fakeAuthFs();
  assert.throws(
    () => materializeCodexAuth({ CODEX_HOME: '/session/state/codex' } as NodeJS.ProcessEnv, fs),
    /no readable auth\.json/,
  );
});

test('materializeCodexAuth allows OPENAI_API_KEY as the fallback credential', () => {
  const { fs, writes } = fakeAuthFs();
  const home = materializeCodexAuth(
    { CODEX_HOME: '/session/state/codex', OPENAI_API_KEY: 'sk-x' } as NodeJS.ProcessEnv,
    fs,
  );
  assert.equal(home, '/session/state/codex');
  assert.equal(writes.length, 0, 'no seed to write when only OPENAI_API_KEY is present');
});

test('materializeCodexAuth writes the seed at mode 0600 when auth.json is absent', () => {
  const { fs, writes } = fakeAuthFs();
  const seed = JSON.stringify({ tokens: { access_token: 'a' }, last_refresh: '2026-07-17T00:00:00Z' });
  materializeCodexAuth(
    { CODEX_HOME: '/session/state/codex', CODEX_AUTH_JSON: seed } as NodeJS.ProcessEnv,
    fs,
  );
  assert.equal(writes.length, 1);
  assert.equal(writes[0].path, '/session/state/codex/auth.json');
  assert.equal(writes[0].content, seed);
  assert.equal(writes[0].mode, 0o600, 'auth.json must be owner-only (0600)');
});

test('materializeCodexAuth skips the write when on-disk content is identical', () => {
  const seed = JSON.stringify({ tokens: { access_token: 'a' }, last_refresh: '2026-07-17T00:00:00Z' });
  const { fs, writes } = fakeAuthFs({ '/session/state/codex/auth.json': seed });
  materializeCodexAuth(
    { CODEX_HOME: '/session/state/codex', CODEX_AUTH_JSON: seed } as NodeJS.ProcessEnv,
    fs,
  );
  assert.equal(writes.length, 0, 'identical content must not churn the PVC');
});

test('materializeCodexAuth keeps a newer on-disk refresh over an older seed', () => {
  const onDisk = JSON.stringify({ tokens: { access_token: 'new' }, last_refresh: '2026-07-17T12:00:00Z' });
  const seed = JSON.stringify({ tokens: { access_token: 'old' }, last_refresh: '2026-07-17T00:00:00Z' });
  const { fs, writes } = fakeAuthFs({ '/session/state/codex/auth.json': onDisk });
  materializeCodexAuth(
    { CODEX_HOME: '/session/state/codex', CODEX_AUTH_JSON: seed } as NodeJS.ProcessEnv,
    fs,
  );
  assert.equal(writes.length, 0, 'a newer pod-side refresh must win over an older seed');
});

test('materializeCodexAuth lets a rotated seed overwrite an older on-disk file', () => {
  const onDisk = JSON.stringify({ tokens: { access_token: 'old' }, last_refresh: '2026-07-17T00:00:00Z' });
  const seed = JSON.stringify({ tokens: { access_token: 'rotated' }, last_refresh: '2026-07-17T12:00:00Z' });
  const { fs, writes } = fakeAuthFs({ '/session/state/codex/auth.json': onDisk });
  materializeCodexAuth(
    { CODEX_HOME: '/session/state/codex', CODEX_AUTH_JSON: seed } as NodeJS.ProcessEnv,
    fs,
  );
  assert.equal(writes.length, 1, 'a newer operator seed must overwrite the older on-disk file');
  assert.equal(writes[0].content, seed);
});

// --- buildCodexServeEnv ----------------------------------------------------

test('buildCodexServeEnv drops RUNNER_TOKEN + CODEX_AUTH_JSON, keeps OPENAI_API_KEY', () => {
  const env = {
    RUNNER_TOKEN: 'secret-bearer',
    CODEX_AUTH_JSON: '{"tokens":{}}',
    OPENAI_API_KEY: 'sk-x',
    CODEX_HOME: '/session/state/codex',
    PATH: '/usr/bin',
  } as NodeJS.ProcessEnv;
  const out = buildCodexServeEnv(env);
  assert.equal(out.RUNNER_TOKEN, undefined, 'runner bearer token must not reach the child');
  assert.equal(out.CODEX_AUTH_JSON, undefined, 'the raw OAuth seed must not reach the child');
  assert.equal(out.OPENAI_API_KEY, 'sk-x', 'the API-key fallback the child needs is restored');
  assert.equal(out.CODEX_HOME, '/session/state/codex');
  assert.equal(out.PATH, '/usr/bin', 'user-workflow env is preserved');
});

// --- observer frame-mapping core -------------------------------------------

/** Fake CodexObserverDeps recording emits + sends, with a sequential turn id. */
function fakeObserverDeps(): {
  deps: CodexObserverDeps;
  emits: Array<{ turnId: string | undefined; type: string; payload: Record<string, unknown> }>;
  sends: unknown[];
  statuses: string[];
} {
  const emits: Array<{ turnId: string | undefined; type: string; payload: Record<string, unknown> }> = [];
  const sends: unknown[] = [];
  const statuses: string[] = [];
  let n = 0;
  const deps: CodexObserverDeps = {
    nextTurnId: () => `turn-${++n}`,
    setLastTurn: () => {},
    setExternalActivity: () => {},
    noteObserverEvent: () => {},
    setStatus: (s) => statuses.push(s),
    emit: (turnId, type, payload) => emits.push({ turnId, type, payload }),
    send: (frame) => sends.push(frame),
  };
  return { deps, emits, sends, statuses };
}

test('observer declines a server→client request with a JSON-RPC error and no emits', () => {
  const { deps, emits, sends } = fakeObserverDeps();
  const core = createCodexObserverHandler(deps);
  core.handle({ jsonrpc: '2.0', id: 7, method: 'execCommandApproval', params: { command: 'rm -rf /' } });
  assert.equal(emits.length, 0, 'a server request must never map to a normalized event');
  assert.equal(sends.length, 1);
  const reply = sends[0] as { id: number; error: { code: number } };
  assert.equal(reply.id, 7);
  assert.equal(reply.error.code, -32601, 'must decline (method-not-found), never auto-approve');
});

test('observer frames a turn/started → turn/completed pair as turn.started + turn.completed', () => {
  const { deps, emits, statuses } = fakeObserverDeps();
  const core = createCodexObserverHandler(deps);
  core.handle({ jsonrpc: '2.0', method: 'turn/started', params: { threadId: 't1', turn: { id: 'turn-a' } } });
  assert.ok(core.cycleActive, 'turn/started opens a synthetic cycle');
  core.handle({ jsonrpc: '2.0', method: 'turn/completed', params: { turn: { status: 'completed' } } });
  assert.ok(!core.cycleActive, 'turn/completed closes the cycle');
  const types = emits.map((e) => e.type);
  assert.deepEqual(types, ['turn.started', 'turn.completed']);
  assert.deepEqual(statuses, ['busy', 'idle']);
});

test('observer maps a failed turn/completed to turn.failed + error + status error', () => {
  const { deps, emits, statuses } = fakeObserverDeps();
  const core = createCodexObserverHandler(deps);
  core.handle({ jsonrpc: '2.0', method: 'turn/started', params: { turn: {} } });
  core.handle({
    jsonrpc: '2.0',
    method: 'turn/completed',
    params: { turn: { status: 'failed', error: { message: 'boom' } } },
  });
  const types = emits.map((e) => e.type);
  assert.deepEqual(types, ['turn.started', 'turn.failed', 'error']);
  assert.equal(emits[1].payload.message, 'boom');
  assert.deepEqual(statuses, ['busy', 'error']);
});

test('observer maps thread/tokenUsage/updated to usage.updated', () => {
  const { deps, emits } = fakeObserverDeps();
  const core = createCodexObserverHandler(deps);
  core.handle({ jsonrpc: '2.0', method: 'turn/started', params: { turn: {} } });
  core.handle({
    jsonrpc: '2.0',
    method: 'thread/tokenUsage/updated',
    params: { tokenUsage: { last: { inputTokens: 100, outputTokens: 20, cachedInputTokens: 5 } } },
  });
  const usage = emits.find((e) => e.type === 'usage.updated');
  assert.ok(usage);
  assert.equal(usage.payload.inputTokens, 100);
  assert.equal(usage.payload.outputTokens, 20);
  assert.equal(usage.payload.cacheReadTokens, 5);
  assert.equal(usage.payload.cacheWriteTokens, 0);
  assert.equal(usage.payload.totalCostUsd, 0);
});

test('observer maps a command item start/complete to tool.started + tool.completed', () => {
  const { deps, emits } = fakeObserverDeps();
  const core = createCodexObserverHandler(deps);
  core.handle({ jsonrpc: '2.0', method: 'turn/started', params: { turn: {} } });
  core.handle({
    jsonrpc: '2.0',
    method: 'item/started',
    params: { item: { type: 'commandExecution', id: 'item-1', command: 'ls', status: 'inProgress' } },
  });
  core.handle({
    jsonrpc: '2.0',
    method: 'item/completed',
    params: {
      item: { type: 'commandExecution', id: 'item-1', status: 'completed', aggregatedOutput: 'a\nb', exitCode: 0 },
    },
  });
  const started = emits.find((e) => e.type === 'tool.started');
  const completed = emits.find((e) => e.type === 'tool.completed');
  assert.ok(started);
  assert.equal(started.payload.tool, 'shell');
  assert.equal(started.payload.toolUseId, 'item-1');
  assert.ok(completed);
  assert.equal(completed.payload.output, 'a\nb');
  assert.equal(completed.payload.exitCode, 0);
});

test('observer ignores unknown notifications and non-tool items', () => {
  const { deps, emits, sends } = fakeObserverDeps();
  const core = createCodexObserverHandler(deps);
  core.handle({ jsonrpc: '2.0', method: 'turn/started', params: { turn: {} } });
  emits.length = 0; // discard the turn.started for this assertion
  core.handle({ jsonrpc: '2.0', method: 'thread/name/updated', params: { name: 'x' } });
  core.handle({ jsonrpc: '2.0', method: 'item/started', params: { item: { type: 'agentMessage', id: 'm1' } } });
  core.handle({ jsonrpc: '2.0', id: 99, result: {} }); // a response to our own request
  assert.equal(emits.length, 0, 'unknown notifications and non-tool items are no-ops');
  assert.equal(sends.length, 0, 'a plain response frame is not a request — no reply');
});

test('observer reset() interrupts an in-flight cycle', () => {
  const { deps, emits, statuses } = fakeObserverDeps();
  const core = createCodexObserverHandler(deps);
  core.handle({ jsonrpc: '2.0', method: 'turn/started', params: { turn: {} } });
  core.reset();
  assert.ok(!core.cycleActive);
  const interrupted = emits.find((e) => e.type === 'turn.interrupted');
  assert.ok(interrupted, 'a dropped stream mid-cycle emits turn.interrupted');
  assert.equal(statuses[statuses.length - 1], 'idle');
});

// claude-pane supervisor unit tests. The PTY and socket layers are faked behind
// the PaneSpawner / PaneSocket seams so the supervisor is exercised WITHOUT the
// native node-pty addon or a real WebSocket. Covers:
//   - scrollback ring bounding (byte cap + tail retention),
//   - first-spawn (`--session-id`) vs resume (`--resume`) arg selection + uuid
//     persistence across a respawn,
//   - the env allowlist (no runner secret leaks into the child),
//   - single-attacher preemption (a new attach closes the previous socket 4001,
//     replays scrollback to the newcomer, and reroutes live output),
//   - backpressure eviction (a socket buffered over the P2 cap is closed 4003;
//     the child keeps running and a reattach replays the ring).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ScrollbackRing,
  SCROLLBACK_BYTES,
  CLOSE_BACKPRESSURE,
  MAX_PANE_CLIENT_BUFFER_BYTES,
  buildClaudePaneEnv,
  claudePaneArgs,
  createClaudePaneSupervisor,
  type PanePty,
  type PaneSocket,
  type PaneSpawner,
  type PaneSpawnOptions,
  type PaneExitInfo,
} from '../src/claude-pane.js';

// --- fakes ----------------------------------------------------------------

class FakePty implements PanePty {
  private dataCb?: (d: Buffer) => void;
  private exitCb?: (e: { exitCode: number; signal?: number }) => void;
  readonly writes: Buffer[] = [];
  readonly resizes: Array<[number, number]> = [];
  killed = 0;
  onData(cb: (d: Buffer) => void): void {
    this.dataCb = cb;
  }
  onExit(cb: (e: { exitCode: number; signal?: number }) => void): void {
    this.exitCb = cb;
  }
  write(d: Buffer): void {
    this.writes.push(d);
  }
  resize(cols: number, rows: number): void {
    this.resizes.push([cols, rows]);
  }
  kill(): void {
    this.killed++;
  }
  // test drivers
  emit(d: Buffer): void {
    this.dataCb?.(d);
  }
  exit(code: number, signal?: number): void {
    this.exitCb?.({ exitCode: code, signal });
  }
}

class FakeSocket implements PaneSocket {
  readonly sent: Buffer[] = [];
  closed: { code?: number; reason?: string } | null = null;
  /** Test-settable stand-in for ws.bufferedAmount (bytes queued, unread). */
  bufferedAmount = 0;
  send(d: Buffer): void {
    if (this.closed) throw new Error('send after close');
    this.sent.push(d);
  }
  close(code?: number, reason?: string): void {
    if (!this.closed) this.closed = { code, reason };
  }
  get text(): string {
    return Buffer.concat(this.sent).toString();
  }
}

function makeSpawner(): { spawn: PaneSpawner; spawns: PaneSpawnOptions[]; ptys: FakePty[] } {
  const spawns: PaneSpawnOptions[] = [];
  const ptys: FakePty[] = [];
  const spawn: PaneSpawner = (opts) => {
    spawns.push(opts);
    const p = new FakePty();
    ptys.push(p);
    return p;
  };
  return { spawn, spawns, ptys };
}

function makePersistence(initial = ''): { get: () => string; set: (u: string) => void; sets: string[] } {
  let v = initial;
  const sets: string[] = [];
  return {
    get: () => v,
    set: (u: string) => {
      v = u;
      sets.push(u);
    },
    sets,
  };
}

// --- scrollback ring ------------------------------------------------------

test('ScrollbackRing evicts oldest bytes past the cap and keeps the tail', () => {
  const ring = new ScrollbackRing(10);
  ring.push(Buffer.from('abcdef')); // 6
  ring.push(Buffer.from('ghijkl')); // total 12 → drop 2 oldest ('ab')
  assert.equal(ring.size, 10);
  assert.equal(ring.snapshot().toString(), 'cdefghijkl');
});

test('ScrollbackRing tail-trims a single chunk larger than the cap', () => {
  const ring = new ScrollbackRing(4);
  ring.push(Buffer.from('abcdefgh'));
  assert.equal(ring.size, 4);
  assert.equal(ring.snapshot().toString(), 'efgh');
});

test('ScrollbackRing under the cap retains everything, in order', () => {
  const ring = new ScrollbackRing(64);
  ring.push(Buffer.from('one'));
  ring.push(Buffer.from('two'));
  assert.equal(ring.snapshot().toString(), 'onetwo');
  assert.equal(ring.size, 6);
});

test('ScrollbackRing default cap bounds output to 256 KiB', () => {
  const ring = new ScrollbackRing();
  ring.push(Buffer.alloc(SCROLLBACK_BYTES + 4096, 0x61));
  assert.equal(ring.size, SCROLLBACK_BYTES);
});

// --- arg selection --------------------------------------------------------

test('claudePaneArgs starts a session on first spawn and resumes afterwards', () => {
  assert.deepEqual(claudePaneArgs('uuid-1', false), ['--session-id', 'uuid-1']);
  assert.deepEqual(claudePaneArgs('uuid-1', true), ['--resume', 'uuid-1']);
});

test('supervisor generates + persists a uuid on first spawn, resumes it after exit', () => {
  const { spawn, spawns, ptys } = makeSpawner();
  const persistence = makePersistence('');
  let n = 0;
  const sup = createClaudePaneSupervisor({
    cwd: '/work/proj',
    env: {},
    persistence,
    spawn,
    generateUuid: () => `gen-${++n}`,
  });

  // First attach ever: no persisted uuid → generate one and --session-id it.
  sup.attach(new FakeSocket());
  assert.equal(spawns.length, 1);
  assert.equal(spawns[0].cwd, '/work/proj');
  assert.deepEqual(spawns[0].args, ['--session-id', 'gen-1']);
  assert.equal(persistence.sets.length, 1, 'the fresh uuid is persisted once');
  assert.equal(persistence.get(), 'gen-1');

  // Child exits; a later attach must RESUME the same uuid (no new generation).
  ptys[0].exit(0);
  assert.equal(sup.running(), false);
  sup.attach(new FakeSocket());
  assert.equal(spawns.length, 2);
  assert.deepEqual(spawns[1].args, ['--resume', 'gen-1']);
  assert.equal(n, 1, 'generateUuid is called exactly once across respawns');
  assert.equal(persistence.sets.length, 1, 'the uuid is persisted once, not again on resume');
});

test('supervisor with a pre-persisted uuid resumes without generating', () => {
  const { spawn, spawns } = makeSpawner();
  let generated = 0;
  const sup = createClaudePaneSupervisor({
    cwd: '/w',
    env: {},
    persistence: makePersistence('existing-uuid'),
    spawn,
    generateUuid: () => {
      generated++;
      return 'should-not-be-used';
    },
  });
  sup.attach(new FakeSocket());
  assert.deepEqual(spawns[0].args, ['--resume', 'existing-uuid']);
  assert.equal(generated, 0);
});

// --- env allowlist --------------------------------------------------------

test('buildClaudePaneEnv is a strict allowlist and leaks no runner secrets', () => {
  const env: NodeJS.ProcessEnv = {
    RUNNER_TOKEN: 'bearer-secret',
    ANTHROPIC_API_KEY: 'sk-ant',
    CLAUDE_CODE_OAUTH_TOKEN: 'oauth-tok',
    CLAUDE_CREDENTIALS_JSON: '{"creds":true}',
    CLAUDE_OAUTH_ACCOUNT_JSON: '{"acct":true}',
    OPENAI_API_KEY: 'sk-openai',
    OPENCODE_SERVER_PASSWORD: 'oc-pw',
    SOME_OTHER_SECRET: 'nope',
    PATH: '/usr/bin',
    HOME: '/root',
    LANG: 'en_US.UTF-8',
    CLAUDE_CONFIG_DIR: '/session/state/claude',
  };
  const out = buildClaudePaneEnv(env);

  // Fixed terminal vars + passthroughs are present.
  assert.equal(out.TERM, 'xterm-256color');
  assert.equal(out.COLORTERM, 'truecolor');
  assert.equal(out.PATH, '/usr/bin');
  assert.equal(out.HOME, '/root');
  assert.equal(out.LANG, 'en_US.UTF-8');
  assert.equal(out.CLAUDE_CONFIG_DIR, '/session/state/claude');

  // NOTHING else — every runner secret must be absent.
  for (const k of [
    'RUNNER_TOKEN',
    'ANTHROPIC_API_KEY',
    'CLAUDE_CODE_OAUTH_TOKEN',
    'CLAUDE_CREDENTIALS_JSON',
    'CLAUDE_OAUTH_ACCOUNT_JSON',
    'OPENAI_API_KEY',
    'OPENCODE_SERVER_PASSWORD',
    'SOME_OTHER_SECRET',
  ]) {
    assert.equal(k in out, false, `${k} must not leak into the pane child`);
  }
  // Exactly the allowlisted keys, nothing more.
  assert.deepEqual(
    Object.keys(out).sort(),
    ['CLAUDE_CONFIG_DIR', 'COLORTERM', 'HOME', 'LANG', 'PATH', 'TERM'],
  );
});

test('buildClaudePaneEnv defaults CLAUDE_CONFIG_DIR to the PVC config dir', () => {
  const out = buildClaudePaneEnv({ PATH: '/bin' });
  assert.equal(out.CLAUDE_CONFIG_DIR, '/session/state/claude');
  // Absent passthroughs are not injected as undefined.
  assert.equal('HOME' in out, false);
  assert.equal('LANG' in out, false);
});

// Part B: operator-injected env (ExtraEnv/ExtraSecretEnv) is DELIBERATELY admitted
// to the pane agent — the two marker vars name exactly which vars cross the
// otherwise-strict allowlist. The runner's own secrets are never named, so they
// stay withheld.
test('buildClaudePaneEnv admits marker-named ExtraEnv/ExtraSecretEnv but still withholds runner secrets', () => {
  const env: NodeJS.ProcessEnv = {
    RUNNER_TOKEN: 'bearer-secret',
    CLAUDE_CREDENTIALS_JSON: '{"creds":true}',
    // Operator-injected — named by the markers.
    TOOL_ENDPOINT: 'https://tool.internal',
    GITLAB_TOKEN: 'glpat-injected',
    SANDBOX_EXTRA_ENV_NAMES: 'TOOL_ENDPOINT',
    SANDBOX_EXTRA_SECRET_ENV_NAMES: 'GITLAB_TOKEN',
    PATH: '/usr/bin',
  };
  const out = buildClaudePaneEnv(env);

  // The operator-declared vars are admitted (the feature).
  assert.equal(out.TOOL_ENDPOINT, 'https://tool.internal', 'ExtraEnv var admitted to the pane');
  assert.equal(out.GITLAB_TOKEN, 'glpat-injected', 'ExtraSecretEnv var admitted to the pane');

  // The runner's own secrets are NOT named by the markers, so they stay withheld —
  // and the marker vars themselves are not leaked into the child.
  assert.equal('RUNNER_TOKEN' in out, false, 'RUNNER_TOKEN must never reach the pane child');
  assert.equal('CLAUDE_CREDENTIALS_JSON' in out, false, 'pane credential material stays withheld');
  assert.equal('SANDBOX_EXTRA_ENV_NAMES' in out, false, 'the marker var is not passed through');
  assert.equal('SANDBOX_EXTRA_SECRET_ENV_NAMES' in out, false, 'the marker var is not passed through');
});

// --- single-attacher preemption -------------------------------------------

test('a new attach preempts the previous socket and reroutes output', () => {
  const { spawn, ptys } = makeSpawner();
  const sup = createClaudePaneSupervisor({
    cwd: '/w',
    env: {},
    persistence: makePersistence('u'), // pre-persisted → single resume child
    spawn,
  });

  const a = new FakeSocket();
  sup.attach(a);
  assert.equal(sup.current(), a);
  assert.equal(sup.attached(), true);

  // Live output reaches the current socket and is buffered in the ring.
  ptys[0].emit(Buffer.from('hello'));
  assert.equal(a.text, 'hello');

  // Second attach preempts `a` with close 4001 and replays scrollback to `b`.
  const b = new FakeSocket();
  sup.attach(b);
  assert.deepEqual(a.closed, { code: 4001, reason: 'replaced by a new pane attach' });
  assert.equal(sup.current(), b);
  assert.equal(b.sent.length, 1, 'scrollback replayed as one binary frame');
  assert.equal(b.sent[0].toString(), 'hello');

  // New output flows to `b` only; `a` receives nothing further.
  ptys[0].emit(Buffer.from('world'));
  assert.equal(b.text, 'helloworld');
  assert.equal(a.text, 'hello');

  // Exactly one child spawned across both attaches (resume id was persisted).
  assert.equal(ptys.length, 1);
});

// --- backpressure eviction (P2) -------------------------------------------

test('a socket buffered over the cap is evicted 4003 on the next send', () => {
  const { spawn, ptys } = makeSpawner();
  const sup = createClaudePaneSupervisor({ cwd: '/w', env: {}, persistence: makePersistence('u'), spawn });
  const s = new FakeSocket();
  sup.attach(s);

  // Healthy client: output flows.
  ptys[0].emit(Buffer.from('before'));
  assert.equal(s.text, 'before');

  // The client stops reading (suspended laptop / wedged port-forward): its
  // buffered bytes cross the cap, so the NEXT send evicts it instead of
  // queueing more into pod memory.
  s.bufferedAmount = MAX_PANE_CLIENT_BUFFER_BYTES + 1;
  ptys[0].emit(Buffer.from('dropped'));
  assert.deepEqual(s.closed, {
    code: CLOSE_BACKPRESSURE,
    reason: 'pane client not reading (backpressure)',
  });
  assert.equal(s.text, 'before', 'the over-cap chunk is not sent to the wedged socket');
  assert.equal(sup.attached(), false);
  assert.equal(sup.current(), null);
  assert.equal(sup.running(), true, 'eviction closes the socket, never the child');

  // Sends after the eviction are no-ops (FakeSocket.send would throw).
  ptys[0].emit(Buffer.from('later'));
  assert.equal(s.text, 'before');

  // Recovery path: a reattach replays the ring, which kept accumulating.
  const b = new FakeSocket();
  sup.attach(b);
  assert.equal(b.text, 'beforedroppedlater');
  assert.equal(ptys.length, 1, 'no respawn — the same child serves the reattach');
});

test('a socket buffered at or under the cap is not evicted', () => {
  const { spawn, ptys } = makeSpawner();
  const sup = createClaudePaneSupervisor({ cwd: '/w', env: {}, persistence: makePersistence('u'), spawn });
  const s = new FakeSocket();
  sup.attach(s);

  // Exactly at the cap: the check is strictly-greater-than (E3 parity).
  s.bufferedAmount = MAX_PANE_CLIENT_BUFFER_BYTES;
  ptys[0].emit(Buffer.from('still flowing'));
  assert.equal(s.closed, null);
  assert.equal(s.text, 'still flowing');
});

test('write and resize forward to the running child', () => {
  const { spawn, ptys } = makeSpawner();
  const sup = createClaudePaneSupervisor({ cwd: '/w', env: {}, persistence: makePersistence('u'), spawn });
  sup.attach(new FakeSocket());

  sup.write(Buffer.from('keystrokes'));
  assert.equal(Buffer.concat(ptys[0].writes).toString(), 'keystrokes');

  sup.resize(120, 40);
  assert.deepEqual(ptys[0].resizes.at(-1), [120, 40]);
});

test('detachAll drops the socket with close 1000 but leaves the child running', () => {
  const { spawn } = makeSpawner();
  const sup = createClaudePaneSupervisor({ cwd: '/w', env: {}, persistence: makePersistence('u'), spawn });
  const s = new FakeSocket();
  sup.attach(s);
  sup.detachAll();
  assert.equal(sup.attached(), false);
  assert.deepEqual(s.closed, { code: 1000, reason: 'detached' });
  assert.equal(sup.running(), true, 'the child survives a detach (re-attach resumes it)');
});

test('child exit records the outcome, closes the socket 4002, and notifies onExit', () => {
  const { spawn, ptys } = makeSpawner();
  const exits: PaneExitInfo[] = [];
  const sup = createClaudePaneSupervisor({
    cwd: '/w',
    env: {},
    persistence: makePersistence('u'),
    spawn,
    onExit: (info) => exits.push(info),
  });
  const s = new FakeSocket();
  sup.attach(s);
  ptys[0].exit(3, 15);

  assert.equal(sup.running(), false);
  assert.equal(sup.attached(), false);
  assert.deepEqual(s.closed, { code: 4002, reason: 'pane process exited' });
  assert.equal(exits.length, 1);
  assert.equal(exits[0].code, 3);
  assert.equal(exits[0].signal, 15);
  assert.equal(typeof exits[0].at, 'string');
  assert.deepEqual(sup.lastExit(), exits[0]);
});

test('stop kills the child and refuses further attaches', () => {
  const { spawn, ptys } = makeSpawner();
  let stopped = 0;
  const sup = createClaudePaneSupervisor({
    cwd: '/w',
    env: {},
    persistence: makePersistence('u'),
    spawn,
    onStop: () => stopped++,
  });
  sup.attach(new FakeSocket());
  sup.stop();
  assert.equal(ptys[0].killed, 1);
  assert.equal(sup.running(), false);
  assert.equal(stopped, 1);

  // A post-stop attach is refused (socket closed) and spawns nothing new.
  const late = new FakeSocket();
  sup.attach(late);
  assert.equal(late.closed?.code, 4002);
  assert.equal(ptys.length, 1);
});

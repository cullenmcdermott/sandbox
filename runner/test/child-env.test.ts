// A1 (HIGH): agent child processes must NOT inherit RUNNER_TOKEN — the bearer
// token protecting the runner's own HTTP API. A prompt-injected agent that could
// read $RUNNER_TOKEN would POST to its own session's /permissions/:id endpoint
// and self-approve, defeating the approval flow. These tests pin the env built
// for the two spawn surfaces: the claude SDK child (buildAgentEnv) and the
// `opencode serve` child (buildOpencodeServeEnv + the live supervisor spawn).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import type { ChildProcess } from 'node:child_process';
import { buildAgentEnv } from '../src/claude.js';
import { buildOpencodeServeEnv, startOpencodeSupervisor } from '../src/opencode.js';

// --- claude SDK child (buildAgentEnv) -------------------------------------

test('buildAgentEnv drops RUNNER_TOKEN but keeps provider creds and overrides', () => {
  const env: NodeJS.ProcessEnv = {
    RUNNER_TOKEN: 'bearer-secret',
    ANTHROPIC_API_KEY: 'sk-ant',
    CLAUDE_CODE_OAUTH_TOKEN: 'oauth-tok',
    PATH: '/usr/bin',
    HOME: '/root',
    OPENCODE_SERVER_PASSWORD: 'oc-pw',
  };
  const out = buildAgentEnv(
    {
      CLAUDE_CONFIG_DIR: '/session/state/claude',
      CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1',
      IS_SANDBOX: '1',
    },
    env,
  );

  // The A1 fix: the runner's bearer token never reaches the claude child.
  assert.equal(out.RUNNER_TOKEN, undefined, 'RUNNER_TOKEN must be stripped');

  // The claude binary authenticates with these — they must survive.
  assert.equal(out.ANTHROPIC_API_KEY, 'sk-ant');
  assert.equal(out.CLAUDE_CODE_OAUTH_TOKEN, 'oauth-tok');

  // Explicit overrides at the spawn site are applied.
  assert.equal(out.CLAUDE_CONFIG_DIR, '/session/state/claude');
  assert.equal(out.CLAUDE_CODE_DISABLE_AUTO_MEMORY, '1');
  assert.equal(out.IS_SANDBOX, '1');

  // User-workflow vars still pass through (the child needs a usable shell env).
  assert.equal(out.PATH, '/usr/bin');
  assert.equal(out.HOME, '/root');
});

test('buildAgentEnv overrides win over the inherited env', () => {
  const out = buildAgentEnv({ IS_SANDBOX: '0' }, { IS_SANDBOX: '1', PATH: '/bin' });
  assert.equal(out.IS_SANDBOX, '0');
});

test('buildAgentEnv omits provider keys that are absent (no undefined injection)', () => {
  const out = buildAgentEnv({}, { PATH: '/bin' });
  assert.equal('ANTHROPIC_API_KEY' in out, false);
  assert.equal('CLAUDE_CODE_OAUTH_TOKEN' in out, false);
});

// --- opencode serve child (buildOpencodeServeEnv) -------------------------

test('buildOpencodeServeEnv drops RUNNER_TOKEN but keeps OPENCODE_SERVER_PASSWORD', () => {
  const out = buildOpencodeServeEnv({
    RUNNER_TOKEN: 'bearer-secret',
    OPENCODE_SERVER_PASSWORD: 'oc-pw',
    ANTHROPIC_API_KEY: 'sk-ant',
    PATH: '/usr/bin',
  });
  assert.equal(out.RUNNER_TOKEN, undefined, 'RUNNER_TOKEN must be stripped');
  // CRITICAL: the supervisor hard-refuses to start without this (O3), and it is
  // serve's own client-auth credential — it must be retained.
  assert.equal(out.OPENCODE_SERVER_PASSWORD, 'oc-pw');
  // The single injected provider key (buildOpencodeConfig references {env:KEY}).
  assert.equal(out.ANTHROPIC_API_KEY, 'sk-ant');
  assert.equal(out.PATH, '/usr/bin');
});

// --- live supervisor spawn ------------------------------------------------

// A controllable stand-in for the spawned child (mirrors opencode.test.ts).
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

test('startOpencodeSupervisor spawns serve with RUNNER_TOKEN stripped but the password kept', async () => {
  let captured: NodeJS.ProcessEnv | undefined;
  let child: FakeChild | undefined;
  const spy = ((_cmd: string, _args: string[], opts: { env?: NodeJS.ProcessEnv }) => {
    captured = opts.env;
    child = fakeChild();
    return child;
  }) as unknown as typeof import('node:child_process').spawn;

  const sup = startOpencodeSupervisor(
    {
      OPENCODE_PORT: '4096',
      OPENCODE_SERVER_PASSWORD: 's3cret',
      RUNNER_TOKEN: 'bearer-secret',
      ANTHROPIC_API_KEY: 'sk-ant',
      PATH: '/usr/bin',
    } as NodeJS.ProcessEnv,
    spy,
  );
  try {
    assert.ok(captured, 'spawn must receive an env');
    assert.equal(captured!.RUNNER_TOKEN, undefined, 'serve child must not inherit RUNNER_TOKEN');
    assert.equal(captured!.OPENCODE_SERVER_PASSWORD, 's3cret', 'serve child keeps its auth password');
    assert.equal(captured!.ANTHROPIC_API_KEY, 'sk-ant', 'serve child keeps the provider key');
    assert.equal(captured!.PATH, '/usr/bin');
  } finally {
    const stopped = sup.stop();
    child?.emit('exit', 0, null);
    await stopped;
  }
});

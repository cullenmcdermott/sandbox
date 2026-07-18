// Regressions for the medium-severity TS batch: M14 (exec env leak), M15 (cwd
// traversal), M13 (audit redaction).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sanitizedExecEnv, resolveWorkspaceDir } from '../src/exec.js';
import { redactSecrets } from '../src/audit.js';

test('sanitizedExecEnv strips runner infra secrets but keeps user vars (M14)', () => {
  const env = {
    PATH: '/usr/bin',
    HOME: '/root',
    GITHUB_TOKEN: 'gh-user-token', // user-workflow secret: must survive
    RUNNER_TOKEN: 'runner-bearer',
    ANTHROPIC_API_KEY: 'sk-ant-xxx',
    OPENCODE_SERVER_PASSWORD: 'pw',
  };
  const out = sanitizedExecEnv(env);
  assert.equal(out.RUNNER_TOKEN, undefined);
  assert.equal(out.ANTHROPIC_API_KEY, undefined);
  assert.equal(out.OPENCODE_SERVER_PASSWORD, undefined);
  assert.equal(out.PATH, '/usr/bin');
  assert.equal(out.HOME, '/root');
  assert.equal(out.GITHUB_TOKEN, 'gh-user-token');
});

test('resolveWorkspaceDir returns the absolute project path, rejects relative + traversal (M15)', () => {
  // Option B: cwd is the real host project path, bind-mounted into the pod.
  assert.equal(resolveWorkspaceDir('/Users/cullen/git/homelab'), '/Users/cullen/git/homelab');
  assert.throws(() => resolveWorkspaceDir('proj/sub'), /absolute path without traversal/);
  assert.throws(() => resolveWorkspaceDir('../../etc'), /absolute path without traversal/);
  assert.throws(() => resolveWorkspaceDir('/Users/../../etc'), /absolute path without traversal/);
});

test('redactSecrets masks secret-named fields and known tokens (M13)', () => {
  const r = redactSecrets({
    command: 'curl -H "Authorization: Bearer sk-abcdefgh12345"',
    api_key: 'sk-live-xyz',
    nested: { password: 'hunter2', safe: 'keep-me' },
  }) as {
    command: string;
    api_key: string;
    nested: { password: string; safe: string };
  };
  assert.match(r.command, /Bearer \[redacted\]/);
  assert.equal(r.api_key, '[redacted]');
  assert.equal(r.nested.password, '[redacted]');
  assert.equal(r.nested.safe, 'keep-me');
});

// [V17] camelCase secret keys must be masked too — a structured tool input like
// {authToken: "ghp_..."} previously slipped past the snake/kebab-only key rule
// and reached the event log + SSE verbatim.
test('redactSecrets masks camelCase secret keys (V17)', () => {
  const r = redactSecrets({
    authToken: 'ghp_abcdefghijklmnop',
    accessToken: 'ya29.secretvalue',
    clientSecret: 'cs_supersecret',
    sessionToken: 'AKIAIOSFODNN7EXAMPLE',
    myApiKey: 'key-abc123',
    // False-positive guards: fully-lowercase runs are NOT secret-keyed.
    stoken: 'not-a-secret',
    broken: 'still-fine',
    monotonic: 'clock',
  }) as Record<string, string>;
  assert.equal(r.authToken, '[redacted]', 'authToken masked');
  assert.equal(r.accessToken, '[redacted]', 'accessToken masked');
  assert.equal(r.clientSecret, '[redacted]', 'clientSecret masked');
  assert.equal(r.sessionToken, '[redacted]', 'sessionToken masked');
  assert.equal(r.myApiKey, '[redacted]', 'myApiKey masked');
  assert.equal(r.stoken, 'not-a-secret', 'lowercase "stoken" is not a false positive');
  assert.equal(r.broken, 'still-fine', 'lowercase "broken" is not a false positive');
  assert.equal(r.monotonic, 'clock', 'lowercase "monotonic" is not a false positive');
});

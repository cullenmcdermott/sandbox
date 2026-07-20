// Unit tests for claude-pane config materialization (claude-config.ts): the
// fail-closed credential path, refresh-preservation, and the merge-not-clobber
// .claude.json seed. All through the injectable fs seam — no real fs writes.

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import {
  detectClaudeVersion,
  materializeClaudePaneConfig,
  WORKSPACE_TRUST_SEED,
  type ClaudeConfigFs,
} from '../src/claude-config.js';

const WS = '/session/workspace/Users/u/proj';
const CREDS = '{"claudeAiOauth":{"accessToken":"at","refreshToken":"rt","futureField":1}}';
const ACCT = '{"oauthAccount":{"accountUuid":"u-1","emailAddress":"a@b.c"}}';

/** In-memory ClaudeConfigFs recording writes (path → {data, mode}). */
function memFs(initial: Record<string, string> = {}) {
  const files = new Map(Object.entries(initial));
  const writes: Array<{ path: string; data: string; mode?: number }> = [];
  const fs: ClaudeConfigFs = {
    readFileSync: ((path: string) => {
      if (!files.has(path)) throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' });
      return files.get(path)!;
    }) as ClaudeConfigFs['readFileSync'],
    writeFileSync: ((path: string, data: string, opts?: { mode?: number }) => {
      files.set(path, data);
      writes.push({ path, data, mode: opts?.mode });
    }) as ClaudeConfigFs['writeFileSync'],
    mkdirSync: (() => undefined) as unknown as ClaudeConfigFs['mkdirSync'],
  };
  return { fs, files, writes };
}

function materialize(fs: ClaudeConfigFs, env: NodeJS.ProcessEnv, version = '2.1.215'): void {
  materializeClaudePaneConfig({
    workspaceDir: WS,
    env,
    configDir: '/cfg',
    fs,
    claudeVersion: () => version,
  });
}

test('fresh dir: credentials written verbatim 0600 and full seed created', () => {
  const { fs, files, writes } = memFs();
  materialize(fs, { CLAUDE_CREDENTIALS_JSON: CREDS, CLAUDE_OAUTH_ACCOUNT_JSON: ACCT });

  assert.equal(files.get('/cfg/.credentials.json'), CREDS); // byte-for-byte
  const credWrite = writes.find((w) => w.path === '/cfg/.credentials.json');
  assert.equal(credWrite?.mode, 0o600);

  const seed = JSON.parse(files.get('/cfg/.claude.json')!);
  assert.equal(seed.hasCompletedOnboarding, true);
  assert.equal(seed.lastOnboardingVersion, '2.1.215');
  assert.deepEqual(seed.oauthAccount, { accountUuid: 'u-1', emailAddress: 'a@b.c' });
  assert.deepEqual(seed.projects[WS], WORKSPACE_TRUST_SEED);
});

test('existing credentials are never clobbered by Secret material', () => {
  const refreshed = '{"claudeAiOauth":{"accessToken":"newer-after-refresh"}}';
  const { fs, files } = memFs({ '/cfg/.credentials.json': refreshed });
  materialize(fs, { CLAUDE_CREDENTIALS_JSON: CREDS, CLAUDE_OAUTH_ACCOUNT_JSON: ACCT });
  assert.equal(files.get('/cfg/.credentials.json'), refreshed);
});

test('existing .claude.json is merged, not overwritten', () => {
  const existing = JSON.stringify({
    numStartups: 7,
    hasCompletedOnboarding: true,
    oauthAccount: { accountUuid: 'kept' },
    projects: { '/other': { hasTrustDialogAccepted: true } },
  });
  const { fs, files } = memFs({ '/cfg/.claude.json': existing, '/cfg/.credentials.json': CREDS });
  materialize(fs, { CLAUDE_OAUTH_ACCOUNT_JSON: ACCT });

  const doc = JSON.parse(files.get('/cfg/.claude.json')!);
  assert.equal(doc.numStartups, 7); // claude's own state preserved
  assert.deepEqual(doc.oauthAccount, { accountUuid: 'kept' }); // env does not replace
  assert.equal(doc.projects['/other'].hasTrustDialogAccepted, true);
  assert.deepEqual(doc.projects[WS], WORKSPACE_TRUST_SEED); // workspace trust added
});

test('fully seeded state produces no write', () => {
  const seeded = JSON.stringify({
    hasCompletedOnboarding: true,
    lastOnboardingVersion: '2.1.0',
    oauthAccount: { accountUuid: 'u' },
    projects: { [WS]: { hasTrustDialogAccepted: true } },
  });
  const { fs, writes } = memFs({ '/cfg/.claude.json': seeded, '/cfg/.credentials.json': CREDS });
  materialize(fs, {});
  assert.equal(writes.length, 0);
});

test('missing credential material fails boot', () => {
  const { fs } = memFs();
  assert.throws(() => materialize(fs, {}), /Secret material is missing/);
});

test('invalid credential material fails boot without echoing bytes', () => {
  const { fs } = memFs();
  try {
    materialize(fs, { CLAUDE_CREDENTIALS_JSON: 'secret-not-json' });
    assert.fail('expected throw');
  } catch (err) {
    assert.match((err as Error).message, /not valid JSON/);
    assert.ok(!(err as Error).message.includes('secret-not-json'));
  }
  assert.throws(
    () => materialize(fs, { CLAUDE_CREDENTIALS_JSON: '{"claudeAiOauth":{}}' }),
    /no claudeAiOauth\.accessToken/,
  );
});

test('unknown version omits lastOnboardingVersion; bad account JSON degrades', () => {
  const { fs, files } = memFs();
  materializeClaudePaneConfig({
    workspaceDir: WS,
    env: { CLAUDE_CREDENTIALS_JSON: CREDS, CLAUDE_OAUTH_ACCOUNT_JSON: 'not-json' },
    configDir: '/cfg',
    fs,
    claudeVersion: () => '',
  });
  const seed = JSON.parse(files.get('/cfg/.claude.json')!);
  assert.ok(!('lastOnboardingVersion' in seed));
  assert.ok(!('oauthAccount' in seed));
  assert.equal(seed.hasCompletedOnboarding, true);
});

test('detectClaudeVersion parses the version line and degrades on failure', () => {
  assert.equal(
    detectClaudeVersion(() => '2.1.215 (Claude Code)\n'),
    '2.1.215',
  );
  assert.equal(
    detectClaudeVersion(() => {
      throw new Error('ENOENT');
    }),
    '',
  );
});

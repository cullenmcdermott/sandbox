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

// --- change 4: re-materialize a dead on-PVC credential (validity, not presence) ---

// claude blanks its own claudeAiOauth block on a failed refresh / logout,
// leaving a file that "exists" but cannot authenticate. Each shape below must be
// treated as NOT usable and replaced from the Secret, preserving the per-pod
// mcpOAuth tokens the Secret does not carry.
const DEAD_SHAPES: Record<string, string> = {
  'blanked tokens': '{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0},"mcpOAuth":{"srv":"tok"}}',
  'null token': '{"claudeAiOauth":{"accessToken":null},"mcpOAuth":{"srv":"tok"}}',
  'no oauth block': '{"mcpOAuth":{"srv":"tok"}}',
};

for (const [name, dead] of Object.entries(DEAD_SHAPES)) {
  test(`dead credential (${name}) is replaced from the Secret, mcpOAuth preserved at 0600`, () => {
    const { fs, files, writes } = memFs({ '/cfg/.credentials.json': dead });
    materialize(fs, { CLAUDE_CREDENTIALS_JSON: CREDS, CLAUDE_OAUTH_ACCOUNT_JSON: ACCT });

    const doc = JSON.parse(files.get('/cfg/.credentials.json')!);
    assert.equal(doc.claudeAiOauth.accessToken, 'at'); // usable token swapped in
    assert.equal(doc.claudeAiOauth.refreshToken, 'rt');
    assert.deepEqual(doc.mcpOAuth, { srv: 'tok' }); // per-pod tokens survive recovery
    const credWrite = writes.find((w) => w.path === '/cfg/.credentials.json');
    assert.equal(credWrite?.mode, 0o600);
  });
}

test('unparseable on-PVC credential falls back to the Secret bytes verbatim', () => {
  const { fs, files } = memFs({ '/cfg/.credentials.json': 'not-json-garbage' });
  materialize(fs, { CLAUDE_CREDENTIALS_JSON: CREDS, CLAUDE_OAUTH_ACCOUNT_JSON: ACCT });
  assert.equal(files.get('/cfg/.credentials.json'), CREDS); // verbatim, nothing to merge
});

test('dead on-PVC credential with no Secret material still fails boot', () => {
  const { fs } = memFs({ '/cfg/.credentials.json': '{"claudeAiOauth":{"accessToken":""}}' });
  assert.throws(() => materialize(fs, {}), /Secret material is missing/);
});

test('detectClaudeVersion prefers $CLAUDE_CODE_VERSION over spawning claude', () => {
  const version = detectClaudeVersion(
    () => {
      throw new Error('should not spawn');
    },
    { CLAUDE_CODE_VERSION: '3.0.0' },
  );
  assert.equal(version, '3.0.0');
});

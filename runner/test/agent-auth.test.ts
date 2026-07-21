// Unit tests for the shared opencode auth materialization + fail-closed gate
// (agent-auth.ts) and the serve child-env scrub (opencode.ts). All use an
// injected in-memory AuthFs — no real filesystem, no `opencode` binary, no
// sockets. The codex whole-file materializer is pinned by codex.test.ts; this
// file covers opencode's PER-ENTRY, refresh-preserving reconciliation.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  assertOpencodeAuthUsable,
  materializeOpencodeAuth,
  opencodeStorePath,
  type AuthFs,
} from '../src/agent-auth.js';
import { buildOpencodeServeEnv } from '../src/opencode.js';

// Deterministic store location for every test (XDG_DATA_HOME set → no real HOME).
const ENV = { XDG_DATA_HOME: '/state/opencode/data' } as NodeJS.ProcessEnv;
const STORE = opencodeStorePath(ENV); // /state/opencode/data/opencode/auth.json
const SIDECAR = STORE + '.seed-hashes';

/** A fake AuthFs backed by an in-memory file map, recording writes (mirrors the
 * fakeAuthFs in codex.test.ts). readFileSync throws ENOENT when a path is absent. */
function fakeAuthFs(files: Record<string, string> = {}): {
  fs: AuthFs;
  writes: Array<{ path: string; content: string; mode?: number }>;
  files: Record<string, string>;
} {
  const writes: Array<{ path: string; content: string; mode?: number }> = [];
  const fs: AuthFs = {
    readFileSync: ((p: string) => {
      if (p in files) return files[p];
      throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' });
    }) as AuthFs['readFileSync'],
    writeFileSync: ((p: string, content: string, opts?: { mode?: number }) => {
      writes.push({ path: String(p), content: String(content), mode: opts?.mode });
      files[String(p)] = String(content);
    }) as AuthFs['writeFileSync'],
    mkdirSync: (() => undefined) as AuthFs['mkdirSync'],
  };
  return { fs, writes, files };
}

const sha = (s: string): string => createHash('sha256').update(s).digest('hex');
/** Canonical on-disk store bytes for an entries object (matches the merge writer). */
const store = (entries: Record<string, unknown>): string => JSON.stringify(entries, null, 2);
/** Sidecar bytes recording the per-entry seed hashes for an entries object. */
const sidecar = (entries: Record<string, unknown>): string => {
  const h: Record<string, string> = {};
  for (const [k, v] of Object.entries(entries)) h[k] = sha(JSON.stringify(v));
  return JSON.stringify(h);
};

// Sample opencode auth entries (the discriminated union: oauth / api).
const A_SEED = { type: 'oauth', refresh: 'r1', access: 'a1', expires: 0 };
const A_REFRESHED = { type: 'oauth', refresh: 'r2', access: 'a2', expires: 123 };
const A_NEW = { type: 'oauth', refresh: 'r3', access: 'a3', expires: 0 };
const B_SEED = { type: 'api', key: 'sk-b' };
const B_REFRESHED = { type: 'api', key: 'sk-b2' };

// --- materializeOpencodeAuth ------------------------------------------------

test('materializeOpencodeAuth seeds the store 0600 + sidecar when the store is absent', () => {
  const seed = { anthropic: A_SEED };
  const seedRaw = JSON.stringify(seed);
  const { fs, writes, files } = fakeAuthFs();
  materializeOpencodeAuth({ ...ENV, OPENCODE_AUTH_JSON: seedRaw } as NodeJS.ProcessEnv, fs);
  assert.equal(writes.length, 2, 'absent store → one store write + one sidecar write');
  assert.equal(writes[0].path, STORE);
  assert.equal(writes[0].mode, 0o600, 'the store must be owner-only (0600)');
  assert.equal(writes[0].content, seedRaw, 'an absent store is seeded wholesale');
  assert.equal(writes[1].path, SIDECAR);
  assert.equal(writes[1].mode, 0o600, 'the sidecar must be owner-only (0600)');
  assert.equal(writes[1].content, sidecar(seed));
  assert.deepEqual(JSON.parse(files[STORE]), seed);
});

test('materializeOpencodeAuth does not write when disk + sidecar already match the seed', () => {
  const seed = { anthropic: A_SEED, openai: B_SEED };
  const { fs, writes } = fakeAuthFs({ [STORE]: store(seed), [SIDECAR]: sidecar(seed) });
  materializeOpencodeAuth({ ...ENV, OPENCODE_AUTH_JSON: JSON.stringify(seed) } as NodeJS.ProcessEnv, fs);
  assert.equal(writes.length, 0, 'unchanged content must not churn the PVC');
});

test('materializeOpencodeAuth keeps a pod-side refresh when the seed is unchanged', () => {
  const seed = { anthropic: A_SEED };
  const disk = { anthropic: A_REFRESHED }; // the pod refreshed the token in place
  const { fs, writes, files } = fakeAuthFs({
    [STORE]: store(disk),
    [SIDECAR]: sidecar(seed), // recorded the ORIGINAL seed hash
  });
  materializeOpencodeAuth({ ...ENV, OPENCODE_AUTH_JSON: JSON.stringify(seed) } as NodeJS.ProcessEnv, fs);
  assert.deepEqual(JSON.parse(files[STORE]).anthropic, A_REFRESHED, 'the refreshed disk entry must survive');
  assert.equal(writes.filter((w) => w.path === STORE).length, 0, 'an unchanged seed must not clobber the refresh');
});

test('materializeOpencodeAuth takes a reseeded entry per-entry but keeps a refreshed sibling', () => {
  // Last materialization recorded {anthropic: A_SEED, openai: B_SEED}. Since then
  // the operator reseeded anthropic (A_NEW) and the pod refreshed openai on disk.
  const newSeed = { anthropic: A_NEW, openai: B_SEED };
  const disk = { anthropic: A_SEED, openai: B_REFRESHED };
  const recorded = { anthropic: A_SEED, openai: B_SEED };
  const { fs, files } = fakeAuthFs({ [STORE]: store(disk), [SIDECAR]: sidecar(recorded) });
  materializeOpencodeAuth({ ...ENV, OPENCODE_AUTH_JSON: JSON.stringify(newSeed) } as NodeJS.ProcessEnv, fs);
  const merged = JSON.parse(files[STORE]);
  assert.deepEqual(merged.anthropic, A_NEW, 'the reseeded entry (anthropic) must be taken');
  assert.deepEqual(merged.openai, B_REFRESHED, 'the refreshed sibling (openai) must be kept');
});

test('materializeOpencodeAuth preserves an entry present only on disk', () => {
  const seed = { anthropic: A_SEED };
  const disk = { anthropic: A_SEED, openai: B_SEED }; // openai exists only on disk
  const { fs, files } = fakeAuthFs({ [STORE]: store(disk), [SIDECAR]: sidecar(seed) });
  materializeOpencodeAuth({ ...ENV, OPENCODE_AUTH_JSON: JSON.stringify(seed) } as NodeJS.ProcessEnv, fs);
  assert.deepEqual(JSON.parse(files[STORE]).openai, B_SEED, 'a disk-only entry must always be preserved');
});

test('materializeOpencodeAuth seeds wholesale when the on-disk store is unparseable', () => {
  const seed = { anthropic: A_SEED };
  const seedRaw = JSON.stringify(seed);
  const { fs, writes, files } = fakeAuthFs({
    [STORE]: '{ this is not valid json',
    [SIDECAR]: sidecar({ anthropic: A_REFRESHED }), // stale/irrelevant record
  });
  materializeOpencodeAuth({ ...ENV, OPENCODE_AUTH_JSON: seedRaw } as NodeJS.ProcessEnv, fs);
  assert.equal(writes[0].path, STORE);
  assert.equal(writes[0].content, seedRaw, 'an unparseable disk store is overwritten wholesale by the seed');
  assert.deepEqual(JSON.parse(files[STORE]), seed);
});

test('materializeOpencodeAuth lets the seed win when there is no sidecar record (documented posture)', () => {
  const seed = { anthropic: A_NEW };
  const disk = { anthropic: A_SEED }; // differs from the seed; no sidecar to disambiguate
  const { fs, files } = fakeAuthFs({ [STORE]: store(disk) }); // sidecar absent
  materializeOpencodeAuth({ ...ENV, OPENCODE_AUTH_JSON: JSON.stringify(seed) } as NodeJS.ProcessEnv, fs);
  assert.deepEqual(
    JSON.parse(files[STORE]).anthropic,
    A_NEW,
    'no record → seed wins (self-corrects once the sidecar exists)',
  );
});

test('materializeOpencodeAuth is a no-op when OPENCODE_AUTH_JSON is absent (fallback path)', () => {
  const { fs, writes } = fakeAuthFs();
  materializeOpencodeAuth(ENV, fs);
  assert.equal(writes.length, 0, 'no injected auth document → touch nothing on disk');
});

test('materializeOpencodeAuth throws on a non-object seed without echoing content', () => {
  const { fs } = fakeAuthFs();
  assert.throws(
    () => materializeOpencodeAuth({ ...ENV, OPENCODE_AUTH_JSON: 'sk-super-secret' } as NodeJS.ProcessEnv, fs),
    (err: Error) => /not a JSON object/.test(err.message) && !err.message.includes('sk-super-secret'),
  );
});

// --- assertOpencodeAuthUsable (fail-closed gate) ---------------------------

test('assertOpencodeAuthUsable passes when the default provider entry is on disk', () => {
  const { fs } = fakeAuthFs({ [STORE]: store({ anthropic: A_SEED }) });
  assert.doesNotThrow(() => assertOpencodeAuthUsable(ENV, fs));
});

test('assertOpencodeAuthUsable throws (naming store path + entry) when the entry + fallback are absent', () => {
  const { fs } = fakeAuthFs({ [STORE]: store({ openai: B_SEED }) }); // no anthropic entry
  assert.throws(
    () => assertOpencodeAuthUsable(ENV, fs),
    (err: Error) =>
      /refusing to start `opencode serve`/.test(err.message) &&
      err.message.includes(STORE) &&
      /`anthropic`/.test(err.message),
  );
});

test('assertOpencodeAuthUsable passes on the fallback env var alone (no store)', () => {
  const { fs } = fakeAuthFs();
  assert.doesNotThrow(() =>
    assertOpencodeAuthUsable({ ...ENV, ANTHROPIC_API_KEY: 'k' } as NodeJS.ProcessEnv, fs),
  );
});

test('assertOpencodeAuthUsable maps SANDBOX_OPENCODE_PROVIDER=opencode to OPENCODE_API_KEY', () => {
  const { fs } = fakeAuthFs(); // empty store
  assert.doesNotThrow(() =>
    assertOpencodeAuthUsable(
      { ...ENV, SANDBOX_OPENCODE_PROVIDER: 'opencode', OPENCODE_API_KEY: 'k' } as NodeJS.ProcessEnv,
      fs,
    ),
  );
  assert.throws(
    () => assertOpencodeAuthUsable({ ...ENV, SANDBOX_OPENCODE_PROVIDER: 'opencode' } as NodeJS.ProcessEnv, fs),
    (err: Error) => /`opencode`/.test(err.message) && err.message.includes('OPENCODE_API_KEY'),
  );
});

// --- buildOpencodeServeEnv child-env scrub ---------------------------------

test('buildOpencodeServeEnv scrubs OPENCODE_AUTH_JSON + OPENCODE_AUTH_CONTENT, keeps the serve keys', () => {
  const env = {
    RUNNER_TOKEN: 'secret-bearer',
    OPENCODE_AUTH_JSON: '{"anthropic":{}}',
    OPENCODE_AUTH_CONTENT: '{"anthropic":{}}',
    OPENCODE_SERVER_PASSWORD: 's3cret',
    ANTHROPIC_API_KEY: 'sk-a',
    PATH: '/usr/bin',
  } as NodeJS.ProcessEnv;
  const out = buildOpencodeServeEnv(env);
  assert.equal(out.OPENCODE_AUTH_JSON, undefined, 'the raw auth seed must not reach the child');
  assert.equal(out.OPENCODE_AUTH_CONTENT, undefined, 'the env-store override must not reach the child');
  assert.equal(out.RUNNER_TOKEN, undefined, 'the runner bearer token is still stripped (A1)');
  assert.equal(out.OPENCODE_SERVER_PASSWORD, 's3cret', 'serve credentials are still restored');
  assert.equal(out.ANTHROPIC_API_KEY, 'sk-a', 'the single provider key serve needs is restored');
  assert.equal(out.PATH, '/usr/bin', 'user-workflow env is preserved');
});

// Unit tests for the operator bootstrap-file materializer (bootstrap.ts). All use
// an injected in-memory BootstrapFs — no real filesystem, no mounted Secret. Cover
// the write-if-changed seed reconciliation (seed vs pod-side edit vs operator
// rotation, mirroring the opencode per-entry logic) and the fail-closed path
// re-validation (traversal / outside-roots paths are skipped, never written).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  materializeBootstrapFiles,
  resolveBootstrapTarget,
  type BootstrapFs,
} from '../src/bootstrap.js';

const MOUNT = '/mnt/boot';
const SIDECAR = '/session/state/sandbox/bootstrap-seed-hashes.json';
const HOME = '/root';
const sha = (s: string): string => createHash('sha256').update(s).digest('hex');

/** In-memory BootstrapFs recording writes; readFileSync throws ENOENT when absent
 * (mirrors the fakeAuthFs in agent-auth.test.ts). */
function fakeFs(files: Record<string, string> = {}): {
  fs: BootstrapFs;
  writes: Array<{ path: string; content: string; mode?: number }>;
  files: Record<string, string>;
} {
  const writes: Array<{ path: string; content: string; mode?: number }> = [];
  const fs: BootstrapFs = {
    readFileSync: ((p: string) => {
      if (p in files) return files[p];
      throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' });
    }) as BootstrapFs['readFileSync'],
    writeFileSync: ((p: string, content: string, opts?: { mode?: number }) => {
      writes.push({ path: String(p), content: String(content), mode: opts?.mode });
      files[String(p)] = String(content);
    }) as BootstrapFs['writeFileSync'],
    mkdirSync: (() => undefined) as BootstrapFs['mkdirSync'],
  };
  return { fs, writes, files };
}

/** Build a mounted-volume file set (manifest + numbered content) for a set of
 * {path, content, mode} files. */
function mount(files: Array<{ path: string; content: string; mode?: number }>): Record<string, string> {
  const out: Record<string, string> = {
    [`${MOUNT}/manifest.json`]: JSON.stringify(files.map((f) => ({ path: f.path, mode: f.mode }))),
  };
  files.forEach((f, i) => {
    out[`${MOUNT}/${i}`] = f.content;
  });
  return out;
}

const ENV = { SANDBOX_BOOTSTRAP_DIR: MOUNT, HOME } as NodeJS.ProcessEnv;

test('materializeBootstrapFiles is a no-op when the marker is unset', () => {
  const { fs, writes } = fakeFs();
  materializeBootstrapFiles({ HOME } as NodeJS.ProcessEnv, fs);
  assert.equal(writes.length, 0);
});

test('materializeBootstrapFiles is a no-op when the manifest is absent', () => {
  const { fs, writes } = fakeFs(); // mount dir empty
  materializeBootstrapFiles(ENV, fs);
  assert.equal(writes.length, 0);
});

test('materializeBootstrapFiles seeds an absent target with its mode + records the sidecar', () => {
  const files = [{ path: '~/.claude/CLAUDE.md', content: '# guidance', mode: 0o644 }];
  const { fs, writes, files: disk } = fakeFs(mount(files));
  materializeBootstrapFiles(ENV, fs);

  const target = '/root/.claude/CLAUDE.md';
  const fileWrite = writes.find((w) => w.path === target);
  assert.ok(fileWrite, 'the file must be written');
  assert.equal(fileWrite.content, '# guidance');
  assert.equal(fileWrite.mode, 0o644);
  assert.equal(disk[target], '# guidance');
  // Sidecar records the seed hash.
  const sidecarWrite = writes.find((w) => w.path === SIDECAR);
  assert.ok(sidecarWrite, 'the sidecar must be written');
  assert.equal(sidecarWrite.mode, 0o600);
  assert.deepEqual(JSON.parse(sidecarWrite.content), { [target]: sha('# guidance') });
});

test('materializeBootstrapFiles defaults mode 0 to 0644', () => {
  const files = [{ path: '/session/state/tool.cfg', content: 'k=v', mode: 0 }];
  const { fs, writes } = fakeFs(mount(files));
  materializeBootstrapFiles(ENV, fs);
  const w = writes.find((x) => x.path === '/session/state/tool.cfg');
  assert.ok(w);
  assert.equal(w.mode, 0o644, 'mode 0 must default to 0644');
});

test('materializeBootstrapFiles does not write when on-disk already equals the seed', () => {
  const files = [{ path: '~/.claude/CLAUDE.md', content: 'same', mode: 0o644 }];
  const target = '/root/.claude/CLAUDE.md';
  const { fs, writes } = fakeFs({
    ...mount(files),
    [target]: 'same',
    [SIDECAR]: JSON.stringify({ [target]: sha('same') }),
  });
  materializeBootstrapFiles(ENV, fs);
  assert.equal(writes.length, 0, 'identical content must not churn the PVC');
});

test('materializeBootstrapFiles KEEPS a pod-side edit when the seed is unchanged', () => {
  // The agent edited the file in place (disk != seed) but the operator did NOT
  // rotate the seed (recorded hash == current seed hash) -> keep the edit.
  const seed = 'seed-body';
  const files = [{ path: '~/.claude/CLAUDE.md', content: seed, mode: 0o644 }];
  const target = '/root/.claude/CLAUDE.md';
  const { fs, writes, files: disk } = fakeFs({
    ...mount(files),
    [target]: 'agent-edited-body',
    [SIDECAR]: JSON.stringify({ [target]: sha(seed) }),
  });
  materializeBootstrapFiles(ENV, fs);
  assert.equal(disk[target], 'agent-edited-body', 'a pod-side edit must survive when the seed is unchanged');
  assert.ok(!writes.some((w) => w.path === target), 'the target file must not be rewritten');
});

test('materializeBootstrapFiles OVERWRITES a pod-side edit when the operator rotated the seed', () => {
  // disk != seed AND the recorded hash differs from the current seed hash (operator
  // reseeded) -> take the seed.
  const newSeed = 'rotated-seed';
  const files = [{ path: '~/.claude/CLAUDE.md', content: newSeed, mode: 0o644 }];
  const target = '/root/.claude/CLAUDE.md';
  const { fs, files: disk } = fakeFs({
    ...mount(files),
    [target]: 'agent-edited-body',
    [SIDECAR]: JSON.stringify({ [target]: sha('OLD-seed') }), // recorded an older seed
  });
  materializeBootstrapFiles(ENV, fs);
  assert.equal(disk[target], newSeed, 'an operator seed rotation must win over a pod-side edit');
});

test('materializeBootstrapFiles skips a path that escapes the allowed roots', () => {
  const files = [
    { path: '/session/state/../workspace/repo/CLAUDE.md', content: 'ESCAPE', mode: 0o644 },
    { path: '~/ok.txt', content: 'ok', mode: 0o644 },
  ];
  const { fs, writes } = fakeFs(mount(files));
  materializeBootstrapFiles(ENV, fs);
  // The traversal target is never written; the valid sibling still is.
  assert.ok(!writes.some((w) => w.path.includes('workspace')), 'a traversal path must be skipped');
  assert.ok(writes.some((w) => w.path === '/root/ok.txt'), 'the valid file must still materialize');
});

test('resolveBootstrapTarget accepts allowed roots and rejects escapes', () => {
  // Accepted.
  assert.equal(resolveBootstrapTarget('~/.claude/CLAUDE.md', HOME), '/root/.claude/CLAUDE.md');
  assert.equal(resolveBootstrapTarget('/session/state/x', HOME), '/session/state/x');
  assert.equal(resolveBootstrapTarget('/root/a/b', HOME), '/root/a/b');
  // Rejected.
  assert.equal(resolveBootstrapTarget('/etc/passwd', HOME), null);
  assert.equal(resolveBootstrapTarget('relative/path', HOME), null);
  assert.equal(resolveBootstrapTarget('~', HOME), null, 'the HOME dir itself is not a file target');
  assert.equal(resolveBootstrapTarget('/session/state/../workspace/x', HOME), null);
  assert.equal(resolveBootstrapTarget('~/../../etc/x', HOME), null);
  assert.equal(resolveBootstrapTarget('/session/workspace/repo/x', HOME), null, 'the synced workspace is not an allowed root');
});

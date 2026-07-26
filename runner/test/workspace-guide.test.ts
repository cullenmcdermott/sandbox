// Tests for the workspace guide — the CLAUDE.md the runner writes so the in-pane
// agent knows what its tree is. Every case builds a REAL directory: the whole
// point of classifyGit is that it reports a filesystem fact rather than trusting
// config, and a mocked fs would test the mock.

import { strict as assert } from 'node:assert';
import { after, test } from 'node:test';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  GUIDE_BEGIN,
  GUIDE_END,
  classifyGit,
  guideBlock,
  spliceGuide,
  writeWorkspaceGuide,
} from '../src/workspace-guide.js';

const dirs: string[] = [];
after(() => {
  for (const d of dirs) rmSync(d, { recursive: true, force: true });
});

function tmp(prefix: string): string {
  const d = mkdtempSync(join(tmpdir(), prefix));
  dirs.push(d);
  return d;
}

/** A workspace whose .git is a pointer file at `target` (created or not). */
function worktreeWorkspace(targetExists: boolean): { ws: string; target: string } {
  const root = tmp('guide-wt-');
  const ws = join(root, 'workspace');
  mkdirSync(ws);
  const target = join(root, 'host-repo', '.git', 'worktrees', 'sess');
  if (targetExists) mkdirSync(target, { recursive: true });
  writeFileSync(join(ws, '.git'), `gitdir: ${target}\n`);
  return { ws, target };
}

test('a .git pointer to a missing gitdir is a detached worktree', () => {
  const { ws } = worktreeWorkspace(false);
  assert.equal(classifyGit(ws), 'detached-worktree');
});

// The same pointer file with its target PRESENT is a working worktree — git
// commands succeed, so the guide must not claim otherwise.
test('a .git pointer to a present gitdir is a working worktree', () => {
  const { ws } = worktreeWorkspace(true);
  assert.equal(classifyGit(ws), 'worktree');
});

test('a real .git directory is a plain repo (the --worktree=off case)', () => {
  const ws = tmp('guide-repo-');
  mkdirSync(join(ws, '.git'));
  assert.equal(classifyGit(ws), 'repo');
});

test('no .git at all is not a repository', () => {
  assert.equal(classifyGit(tmp('guide-none-')), 'none');
});

test('a relative gitdir resolves against the workspace, not the cwd', () => {
  const root = tmp('guide-rel-');
  const ws = join(root, 'workspace');
  mkdirSync(join(ws, 'real-gitdir'), { recursive: true });
  writeFileSync(join(ws, '.git'), 'gitdir: ./real-gitdir\n');
  assert.equal(classifyGit(ws), 'worktree');
});

test('the detached-worktree guide says git fails and says not to repair it', () => {
  const block = guideBlock('/session/workspace/proj', 'detached-worktree');
  for (const want of [
    '/session/workspace/proj',
    'git worktree',
    'not a git repository',
    'git init',
    'commit',
  ]) {
    assert.ok(block.includes(want), `guide missing ${want}:\n${block}`);
  }
});

// A working checkout must NOT be told its git is broken.
test('a working repo gets no git-is-broken section', () => {
  for (const kind of ['repo', 'worktree'] as const) {
    const block = guideBlock('/session/workspace/proj', kind);
    assert.ok(!block.includes('not a git repository'), `${kind} claimed a broken repo:\n${block}`);
    assert.ok(block.includes('/session/workspace/proj'), 'guide lost the workspace path');
  }
});

test('splice replaces the managed block and preserves everything around it', () => {
  const user = '# My own memory\n\nAlways use tabs.\n';
  const first = spliceGuide(user, guideBlock('/ws', 'detached-worktree'));
  assert.ok(first.includes('Always use tabs.'), 'user content dropped');
  assert.ok(first.includes('git worktree'), 'guide not added');

  // A second boot with a DIFFERENT situation rewrites the block in place: one
  // pair of fences, no stale claim, user content still there.
  const second = spliceGuide(first, guideBlock('/ws', 'repo'));
  assert.equal(second.split(GUIDE_BEGIN).length - 1, 1, `duplicated block:\n${second}`);
  assert.equal(second.split(GUIDE_END).length - 1, 1, `duplicated end fence:\n${second}`);
  assert.ok(!second.includes('git worktree'), `stale worktree claim survived:\n${second}`);
  assert.ok(second.includes('Always use tabs.'), 'user content lost on rewrite');
});

test('writeWorkspaceGuide writes CLAUDE.md into the config dir, not the workspace', () => {
  const { ws } = worktreeWorkspace(false);
  const cfg = tmp('guide-cfg-');
  const kind = writeWorkspaceGuide({ workspaceDir: ws, configDir: cfg });
  assert.equal(kind, 'detached-worktree');
  const written = readFileSync(join(cfg, 'CLAUDE.md'), 'utf8');
  assert.ok(written.includes('git worktree'), written);
  // The workspace syncs to the user's machine; the guide must never land there.
  assert.throws(() => readFileSync(join(ws, 'CLAUDE.md'), 'utf8'));
});

test('writeWorkspaceGuide is best-effort: an unwritable config dir does not throw', () => {
  const { ws } = worktreeWorkspace(false);
  // A path whose parent is a FILE cannot be mkdir'd — the boot must survive it.
  const root = tmp('guide-bad-');
  const notADir = join(root, 'file');
  writeFileSync(notADir, 'x');
  assert.equal(writeWorkspaceGuide({ workspaceDir: ws, configDir: join(notADir, 'claude') }), null);
});

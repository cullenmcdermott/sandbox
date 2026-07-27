// The workspace guide: what the runner tells the in-pane agent about the tree it
// woke up in.
//
// A sandbox workspace is not a normal checkout, and the difference is invisible
// from inside. The common case is a per-session git WORKTREE: the host creates
// it with `git worktree add`, so its `.git` is a one-line pointer FILE
// ("gitdir: /Users/…/project/.git/worktrees/<session>") rather than a
// directory. Mutagen syncs that file into the pod faithfully — and the path it
// points at is a HOST path that does not exist here, so every git command in the
// pod dies with "not a git repository". An agent that does not know this burns a
// turn discovering it, and worse, may conclude the repo is broken and start
// "repairing" it.
//
// So: state it up front. This module writes the guide into a POD-LOCAL config
// dir (on the PVC — NOT the workspace, which syncs back to the user's machine
// and must not grow files they did not write), at whatever path the session's
// backend reads ambient instructions from, so the agent has it before its first
// turn. Every backend gets the same block: the hazard is a property of the
// workspace, not of the agent looking at it.
//
// The file is written as a MARKED BLOCK: content outside the markers is
// preserved verbatim across boots, so an operator-supplied guide (bootstrap
// files can target these same paths) survives, and the block itself is rewritten
// each boot so it never describes a stale workspace.
//
// This module is deliberately a LEAF — it imports nothing from the backend
// supervisors, so `opencode.ts` can import guideTargetFor to register the file
// in its generated config without an import cycle.

import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { CLAUDE_CONFIG_DIR } from './types.js';

/** Injectable filesystem surface (mirrors bootstrap.ts's BootstrapFs) so the
 * writer is unit-testable off-pod. */
export interface GuideFs {
  existsSync: typeof existsSync;
  readFileSync: typeof readFileSync;
  writeFileSync: typeof writeFileSync;
  mkdirSync: typeof mkdirSync;
  statSync: typeof statSync;
}

const realGuideFs: GuideFs = {
  existsSync,
  readFileSync,
  writeFileSync,
  mkdirSync,
  statSync,
};

/** Fences around the runner-owned region of CLAUDE.md. Anything outside them is
 * another author's and is preserved byte-for-byte. */
export const GUIDE_BEGIN = '<!-- BEGIN sandbox runner (managed — edits inside are overwritten) -->';
export const GUIDE_END = '<!-- END sandbox runner -->';

/** What the workspace's `.git` turns out to be. */
export type GitKind =
  | 'repo' // a real .git directory: git works normally
  | 'detached-worktree' // a .git FILE whose gitdir target is absent (the common sandbox case)
  | 'worktree' // a .git file whose gitdir target IS present: git works
  | 'none'; // no .git at all: not a git project

/** Classify the workspace's git situation. Every branch is a filesystem FACT,
 * never an assumption from config: a session created with --worktree=off syncs a
 * real .git directory and git works fine in the pod, and telling that agent it
 * has no git would be its own kind of wrong. */
export function classifyGit(workspaceDir: string, fs: GuideFs = realGuideFs): GitKind {
  const dotGit = join(workspaceDir, '.git');
  if (!fs.existsSync(dotGit)) return 'none';
  try {
    if (fs.statSync(dotGit).isDirectory()) return 'repo';
  } catch {
    return 'none';
  }
  let pointer = '';
  try {
    pointer = fs.readFileSync(dotGit, 'utf8');
  } catch {
    return 'none';
  }
  const m = /^gitdir:\s*(.+?)\s*$/m.exec(pointer);
  if (!m) return 'none';
  // A relative gitdir resolves against the workspace; absolute is the host path
  // `git worktree add` writes, and is what will be missing here.
  const target = isAbsolute(m[1]) ? m[1] : resolve(workspaceDir, m[1]);
  return fs.existsSync(target) ? 'worktree' : 'detached-worktree';
}

/** The runner-owned block for a given workspace + git situation. */
export function guideBlock(workspaceDir: string, git: GitKind): string {
  const lines = [
    GUIDE_BEGIN,
    '',
    '# This session runs in a sandbox pod',
    '',
    `Your working tree is \`${workspaceDir}\`. It is continuously synced back to the`,
    "user's machine, so edits you make here reach them without any action from you.",
    '',
  ];

  if (git === 'detached-worktree') {
    lines.push(
      '## git does not work in this checkout',
      '',
      'This tree is a **git worktree**. Its `.git` is a pointer file naming a path on',
      "the user's machine, and that path does not exist in this pod, so every git",
      'command fails here with something like:',
      '',
      '```',
      'fatal: not a git repository: /Users/…/project/.git/worktrees/<session>',
      '```',
      '',
      'This is expected and is **not** a broken repo. Do not try to fix it — do not',
      'run `git init`, do not delete or rewrite `.git`, and do not clone. Doing so',
      "syncs damage back to the user's worktree.",
      '',
      'What this means in practice:',
      '',
      '- You cannot commit, branch, stash, diff against HEAD, or read git history.',
      '- Read files directly to see the current state; you have no `git status`, so',
      '  track what you changed as you go and say so explicitly when you report back.',
      "- Committing is the **user's** step, on their machine, where the worktree is a",
      '  real checkout. Leave the work uncommitted here and describe it.',
      '',
    );
  } else if (git === 'none') {
    lines.push(
      '## not a git repository',
      '',
      'This workspace is not under version control, so there is no history to consult',
      'and no way to undo an edit but to edit it back. Be correspondingly careful with',
      'destructive changes.',
      '',
    );
  }

  lines.push(GUIDE_END);
  return lines.join('\n');
}

/** Splice the runner block into an existing CLAUDE.md, preserving everything
 * outside the fences. A file with no fences (operator-authored) keeps all of its
 * content and gains the block on top, where the agent reads it first. */
export function spliceGuide(existing: string, block: string): string {
  const begin = existing.indexOf(GUIDE_BEGIN);
  const end = existing.indexOf(GUIDE_END);
  if (begin >= 0 && end > begin) {
    return existing.slice(0, begin) + block + existing.slice(end + GUIDE_END.length);
  }
  if (existing.trim() === '') return block + '\n';
  return block + '\n\n' + existing;
}

/**
 * Where a given backend's agent reads ambient instructions from — always a
 * pod-local config dir, never the workspace.
 *
 * - `claude-pane` → `$CLAUDE_CONFIG_DIR/CLAUDE.md` (Claude Code's user memory).
 * - `codex-app-server` → `$CODEX_HOME/AGENTS.md` (codex's global AGENTS.md;
 *   CODEX_HOME is the documented relocation of ~/.codex and the pod always sets
 *   it to the PVC-persisted state dir).
 * - `opencode-server` → an `AGENTS.md` beside the generated opencode config,
 *   registered explicitly in that config's `instructions` array
 *   (buildOpencodeConfig) rather than relying on opencode's global-dir lookup.
 *
 * Returns null when the backend is unknown or its config-dir env var is unset —
 * off-pod dev, mostly. The guide is best-effort, so "nowhere to put it" is a
 * quiet no-op, not an error: deliberately NOT falling back to the workspace,
 * which syncs to the user's machine.
 */
export function guideTargetFor(
  backend: string,
  env: NodeJS.ProcessEnv = process.env,
): string | null {
  switch (backend) {
    case 'claude-pane':
      return join(env.CLAUDE_CONFIG_DIR?.trim() || CLAUDE_CONFIG_DIR, 'CLAUDE.md');
    case 'codex-app-server': {
      const home = env.CODEX_HOME?.trim();
      return home ? join(home, 'AGENTS.md') : null;
    }
    case 'opencode-server': {
      const cfg = env.OPENCODE_CONFIG?.trim();
      return cfg ? join(dirname(cfg), 'AGENTS.md') : null;
    }
    default:
      return null;
  }
}

export interface WriteGuideOpts {
  /** The agent's working tree (resolveWorkspaceDir(projectPath)). */
  workspaceDir: string;
  /** Absolute path of the guide file to write (guideTargetFor(backend)). */
  path: string;
  fs?: GuideFs;
}

/** Write (or refresh) the workspace guide. Best-effort by design: a guide the
 * runner could not write is a less-informed agent, never a failed boot — unlike
 * credentials, nothing downstream depends on this file existing. */
export function writeWorkspaceGuide(opts: WriteGuideOpts): GitKind | null {
  const fs = opts.fs ?? realGuideFs;
  const path = opts.path;
  const dir = dirname(path);
  try {
    const git = classifyGit(opts.workspaceDir, fs);
    let existing = '';
    if (fs.existsSync(path)) {
      try {
        existing = fs.readFileSync(path, 'utf8');
      } catch {
        existing = '';
      }
    }
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path, spliceGuide(existing, guideBlock(opts.workspaceDir, git)), {
      mode: 0o644,
    });
    return git;
  } catch {
    return null;
  }
}

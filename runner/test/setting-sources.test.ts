// §2b gap 8 — project slash commands / skills / CLAUDE.md in-pod. The runner
// previously pinned `settingSources: []` (SDK isolation mode) in buildOptions,
// so the SDK loaded NO on-disk settings tiers: project/user slash commands,
// skills, subagents and CLAUDE.md were invisible to every turn even though the
// files were synced onto the pod (project sync → cwd/.claude + CLAUDE.md;
// config-input sync → CLAUDE_CONFIG_DIR/{skills,agents,commands,hooks}). The fix
// loads the tiers (default: all three; overridable via SANDBOX_SETTING_SOURCES).
//
// These are the in-repo oracles:
//   - resolveSettingSources — the pure env→SettingSource[] mapping (default,
//                             isolation, explicit list, canonical order, dedup,
//                             garbage rejection)
//   - buildOptions wiring   — options.settingSources IS what query() receives
//
// Live verification (the SDK actually discovering a project command/skill/CLAUDE.md)
// needs a pod: buildOptions only assembles Options; discovery happens inside the
// spawned `claude` binary, which a unit test does not run. buildOptions is
// side-effect-light (mkdirSync on the project cwd; no sqlite, no session.json
// write) so it runs off-pod against a writable temp projectPath, exactly like
// resume.test.ts.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { resolveSettingSources, buildOptions } from '../src/claude.js';
import { initRegistry } from '../src/session.js';
import type { RunnerConfig } from '../src/session.js';
import type { SessionState } from '../src/types.js';

// --- resolveSettingSources: the pure env→SettingSource[] mapping ----------

test('resolveSettingSources: unset loads all three tiers (SDK/CLI default)', () => {
  assert.deepEqual(resolveSettingSources(undefined), ['user', 'project', 'local']);
});

test('resolveSettingSources: empty / "none" is isolation mode ([])', () => {
  assert.deepEqual(resolveSettingSources(''), []);
  assert.deepEqual(resolveSettingSources('   '), []);
  assert.deepEqual(resolveSettingSources('none'), []);
  assert.deepEqual(resolveSettingSources(' NONE '), []); // trimmed + lowercased
});

test('resolveSettingSources: an explicit list keeps only the requested valid tiers', () => {
  assert.deepEqual(resolveSettingSources('project'), ['project']);
  assert.deepEqual(resolveSettingSources('project,local'), ['project', 'local']);
  assert.deepEqual(resolveSettingSources('user'), ['user']);
});

test('resolveSettingSources: canonical user→project→local order regardless of input order', () => {
  assert.deepEqual(resolveSettingSources('local,project,user'), ['user', 'project', 'local']);
  assert.deepEqual(resolveSettingSources('project,user'), ['user', 'project']);
});

test('resolveSettingSources: de-dupes and tolerates whitespace/case', () => {
  assert.deepEqual(resolveSettingSources('Project, PROJECT , project'), ['project']);
  assert.deepEqual(resolveSettingSources(' user , user '), ['user']);
});

test('resolveSettingSources: unknown tokens are dropped, never reach typed Options', () => {
  assert.deepEqual(resolveSettingSources('managed'), []); // 'managed' is not a SettingSource
  assert.deepEqual(resolveSettingSources('project,bogus'), ['project']);
  assert.deepEqual(resolveSettingSources('flag,../etc'), []);
});

// --- buildOptions wiring: options.settingSources IS what query() receives --

const tmpProject = (): string => mkdtempSync(join(tmpdir(), 'settingsources-test-'));

function freshState(over: Partial<SessionState> = {}): SessionState {
  return {
    sandbox_session_id: 'sess-test',
    backend: 'claude-sdk',
    claude_session_id: '',
    opencode_session_id: '',
    project_path: tmpProject(),
    status: 'idle',
    last_turn_id: '',
    last_activity: '2026-07-12T00:00:00Z',
    ...over,
  };
}

const cfg = (projectPath: string): RunnerConfig => ({
  sessionId: 'sess-test',
  backend: 'claude-sdk',
  projectPath,
  runnerToken: 't',
});

const settingSourcesOf = (c: RunnerConfig): string[] | undefined =>
  buildOptions(c, 'turn-1', undefined, undefined, undefined, undefined, undefined, new AbortController())
    .settingSources;

// Run a body with SANDBOX_SETTING_SOURCES set to `val` (or deleted when
// undefined), always restoring the prior value — process.env is global.
function withEnv(val: string | undefined, body: () => void): void {
  const prev = process.env.SANDBOX_SETTING_SOURCES;
  if (val === undefined) delete process.env.SANDBOX_SETTING_SOURCES;
  else process.env.SANDBOX_SETTING_SOURCES = val;
  try {
    body();
  } finally {
    if (prev === undefined) delete process.env.SANDBOX_SETTING_SOURCES;
    else process.env.SANDBOX_SETTING_SOURCES = prev;
  }
}

test('buildOptions: default (env unset) loads all three tiers — NOT the old []', () => {
  const reg = initRegistry(freshState());
  withEnv(undefined, () => {
    assert.deepEqual(settingSourcesOf(cfg(reg.state.project_path)), ['user', 'project', 'local']);
  });
});

test('buildOptions: SANDBOX_SETTING_SOURCES narrows the tiers that reach query()', () => {
  const reg = initRegistry(freshState());
  withEnv('project', () => {
    assert.deepEqual(settingSourcesOf(cfg(reg.state.project_path)), ['project']);
  });
});

test('buildOptions: SANDBOX_SETTING_SOURCES="none" restores SDK isolation mode', () => {
  const reg = initRegistry(freshState());
  withEnv('none', () => {
    assert.deepEqual(settingSourcesOf(cfg(reg.state.project_path)), []);
  });
});

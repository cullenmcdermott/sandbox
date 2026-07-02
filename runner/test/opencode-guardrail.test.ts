// Unit tests for the generated opencode in-agent Bash guardrail plugin
// (opencode.ts guardrailPluginSource / writeOpencodeGuardrailPlugin /
// writeOpencodeConfig). Verifies the generator is a faithful, single-source-of-
// truth projection of guards.ts and that the emitted plugin actually blocks.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, existsSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

import { BLOCKED_BASH_PATTERNS } from '../src/guards.js';
import {
  guardrailPluginSource,
  guardrailPluginPath,
  writeOpencodeGuardrailPlugin,
  writeOpencodeConfig,
} from '../src/opencode.js';

// The plugin's tool.execute.before signature (opencode v1.17.7):
//   (input: { tool; sessionID; callID }, output: { args }) => Promise<void>
type Before = (input: { tool: string }, output: { args: { command?: string } }) => Promise<void>;

async function loadGeneratedHook(dir: string): Promise<Before> {
  const file = join(dir, 'guardrail.mjs');
  writeFileSync(file, guardrailPluginSource(), 'utf8');
  const mod = (await import(pathToFileURL(file).href)) as {
    SandboxGuardrail: () => Promise<Record<string, unknown>>;
  };
  const hooks = await mod.SandboxGuardrail();
  return hooks['tool.execute.before'] as Before;
}

test('generated source embeds every pattern from BLOCKED_BASH_PATTERNS (single source of truth)', () => {
  const src = guardrailPluginSource();
  for (const re of BLOCKED_BASH_PATTERNS) {
    // Each pattern is emitted as new RegExp(<json source>, <json flags>) — assert
    // the JSON-encoded source appears verbatim, so no pattern is silently dropped.
    assert.ok(
      src.includes(`new RegExp(${JSON.stringify(re.source)}, ${JSON.stringify(re.flags)})`),
      `generated plugin is missing pattern ${re}`,
    );
  }
  // Count matches the blocklist length exactly (no extras, no omissions).
  assert.equal((src.match(/new RegExp\(/g) ?? []).length, BLOCKED_BASH_PATTERNS.length);
});

test('the generated plugin blocks a matching bash command and allows a benign one', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'oc-guardrail-'));
  try {
    const before = await loadGeneratedHook(dir);
    // Blocked: matches /\bkubectl\b/.
    await assert.rejects(
      () => before({ tool: 'bash' }, { args: { command: 'kubectl get pods' } }),
      /blocked by sandbox runner guardrail/,
    );
    // Benign bash command: resolves (no throw).
    await before({ tool: 'bash' }, { args: { command: 'ls -la' } });
    // A non-bash tool is never gated, even with a blocked-looking command.
    await before({ tool: 'read' }, { args: { command: 'kubectl get pods' } });
    // Missing args must not throw (defensive).
    await before({ tool: 'bash' }, { args: {} });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('guardrailPluginPath is null without OPENCODE_CONFIG, else a sibling of the config', () => {
  assert.equal(guardrailPluginPath({} as NodeJS.ProcessEnv), null);
  assert.equal(
    guardrailPluginPath({ OPENCODE_CONFIG: '/session/state/opencode/opencode.json' } as NodeJS.ProcessEnv),
    '/session/state/opencode/sandbox-plugin/guardrail.mjs',
  );
});

test('writeOpencodeConfig writes the plugin file and registers it in the config plugin array', () => {
  const dir = mkdtempSync(join(tmpdir(), 'oc-cfg-'));
  try {
    const cfgPath = join(dir, 'opencode.json');
    const env = { OPENCODE_CONFIG: cfgPath } as NodeJS.ProcessEnv;

    const written = writeOpencodeConfig(env);
    assert.equal(written, cfgPath);

    const cfg = JSON.parse(readFileSync(cfgPath, 'utf8')) as { plugin?: unknown[] };
    assert.ok(Array.isArray(cfg.plugin) && cfg.plugin.length === 1, 'config registers exactly one plugin');
    const entry = String(cfg.plugin![0]);
    assert.ok(entry.startsWith('file://') && entry.endsWith('/sandbox-plugin/guardrail.mjs'), entry);

    const pluginFile = guardrailPluginPath(env)!;
    assert.ok(existsSync(pluginFile), 'the plugin file is written to disk');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('writeOpencodeGuardrailPlugin returns a file:// URL and is null without a config path', () => {
  assert.equal(writeOpencodeGuardrailPlugin({} as NodeJS.ProcessEnv), null);
  const dir = mkdtempSync(join(tmpdir(), 'oc-plugin-'));
  try {
    const url = writeOpencodeGuardrailPlugin({ OPENCODE_CONFIG: join(dir, 'opencode.json') } as NodeJS.ProcessEnv);
    assert.ok(url && url.startsWith('file://') && url.endsWith('/sandbox-plugin/guardrail.mjs'), String(url));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

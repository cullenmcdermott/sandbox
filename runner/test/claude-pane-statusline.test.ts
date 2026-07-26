// Behavioral tests for the provisioned claude-pane statusline script — the
// REAL artifact provisionPaneObserver writes is executed under node with
// controlled stdin/env, a local capture server standing in for the runner
// observer route, and user-statusline fixtures covering the chain contract:
//   present (config-dir drop-in / host-synced sibling / PATH) → chained output,
//   absent → built-in line,
//   nonzero exit / timeout / empty output → built-in line,
//   and the metrics POST fires with the verbatim stdin JSON in EVERY case.
// The built-in line's own content (model · ctx% · the claude.ai 5h/weekly plan
// windows) is covered by the last group.
// The chain timeout is injected via SANDBOX_STATUSLINE_TIMEOUT_MS (the script's
// env seam; production uses the 1s default) and the POST target via
// SANDBOX_OBSERVER_URL, so no fakes stand between the tests and the shipped
// script text.

import { strict as assert } from 'node:assert';
import { after, before, test } from 'node:test';
import { spawn } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import { delimiter, join } from 'node:path';
import { provisionPaneObserver } from '../src/claude-pane-observer.js';

const STDIN_JSON = JSON.stringify({
  model: { id: 'claude-opus-4-8', display_name: 'Opus 4.8' },
  context_window: { used_percentage: 16 },
});
const BUILTIN_LINE = 'Opus 4.8 · ctx 16%';

// A user statusline that proves it received the SAME stdin JSON by echoing a
// field parsed from it (plus a trailing newline the chain must trim).
const CHAINED_USER_SCRIPT = `#!/usr/bin/env node
let d = '';
process.stdin.on('data', (c) => (d += c));
process.stdin.on('end', () => {
  const j = JSON.parse(d);
  process.stdout.write('USER[' + j.model.display_name + ']\\n');
});
`;

const posts: Array<{ auth: string | undefined; body: string }> = [];
let server: Server;
let observerUrl = '';

before(async () => {
  server = createServer((req, res) => {
    let body = '';
    req.on('data', (c) => (body += c));
    req.on('end', () => {
      posts.push({ auth: req.headers.authorization, body });
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end('{}');
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  observerUrl = `http://127.0.0.1:${(server.address() as AddressInfo).port}/observer/claude/statusline`;
});

const tempDirs: string[] = [];

after(() => {
  server.close();
  for (const d of tempDirs) rmSync(d, { recursive: true, force: true });
});

/** Provision a fresh config dir with the real fs and return its path. */
function freshConfigDir(): string {
  const dir = mkdtempSync(join(tmpdir(), 'pane-statusline-'));
  tempDirs.push(dir);
  provisionPaneObserver(dir);
  return dir;
}

function tempBinDir(): string {
  const dir = mkdtempSync(join(tmpdir(), 'pane-statusline-bin-'));
  tempDirs.push(dir);
  return dir;
}

function writeExecutable(path: string, content: string): void {
  writeFileSync(path, content);
  chmodSync(path, 0o755);
}

/** Run the provisioned statusline.js with STDIN_JSON (or opts.stdin); resolve
 * once the process exits (which the script does only after the metrics fetch
 * settles, so any POST is already captured). Returns the printed line and the
 * posts this run added. */
async function runStatusline(
  configDir: string,
  opts: { pathPrepend?: string; timeoutMs?: number; stdin?: string } = {},
): Promise<{ line: string; posted: Array<{ auth: string | undefined; body: string }> }> {
  const seen = posts.length;
  const env: NodeJS.ProcessEnv = {
    ...process.env,
    SANDBOX_OBSERVER_URL: observerUrl,
    SANDBOX_STATUSLINE_TIMEOUT_MS: String(opts.timeoutMs ?? 1000),
  };
  if (opts.pathPrepend) env.PATH = opts.pathPrepend + delimiter + (process.env.PATH ?? '');
  const child = spawn(process.execPath, [join(configDir, 'pane-observer', 'statusline.js')], {
    env,
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let line = '';
  child.stdout.on('data', (c: Buffer) => (line += c.toString('utf8')));
  child.stdin.end(opts.stdin ?? STDIN_JSON);
  await new Promise<void>((resolve, reject) => {
    const kill = setTimeout(() => {
      child.kill('SIGKILL');
      reject(new Error('statusline.js did not exit within 10s'));
    }, 10_000);
    child.on('close', () => {
      clearTimeout(kill);
      resolve();
    });
  });
  return { line, posted: posts.slice(seen) };
}

test('no user statusline anywhere → built-in line; metrics POSTed with token', async () => {
  const cfg = freshConfigDir();
  const { line, posted } = await runStatusline(cfg);
  assert.equal(line, BUILTIN_LINE);
  assert.equal(posted.length, 1);
  assert.equal(posted[0].body, STDIN_JSON); // verbatim stdin JSON
  const token = readFileSync(join(cfg, 'pane-observer', 'token'), 'utf8').trim();
  assert.equal(posted[0].auth, `Bearer ${token}`);
});

test('config-dir drop-in chains (and wins over a PATH candidate); POST still fires', async () => {
  const cfg = freshConfigDir();
  writeExecutable(join(cfg, 'pane-observer', 'user-statusline'), CHAINED_USER_SCRIPT);
  const bin = tempBinDir();
  writeExecutable(join(bin, 'sandbox-user-statusline'), '#!/bin/sh\ncat >/dev/null\nprintf FROM-PATH\n');
  const { line, posted } = await runStatusline(cfg, { pathPrepend: bin });
  assert.equal(line, 'USER[Opus 4.8]'); // stdin JSON piped through, trailing \n trimmed
  assert.equal(posted.length, 1);
});

test('host-synced statusline/ sibling chains; a non-executable drop-in falls through to it', async () => {
  const cfg = freshConfigDir();
  // Present but NOT executable → EACCES → next candidate, not the builtin.
  writeFileSync(join(cfg, 'pane-observer', 'user-statusline'), CHAINED_USER_SCRIPT);
  mkdirSync(join(cfg, 'statusline'));
  writeExecutable(join(cfg, 'statusline', 'user-statusline'), '#!/bin/sh\ncat >/dev/null\nprintf FROM-SYNC\n');
  const { line, posted } = await runStatusline(cfg);
  assert.equal(line, 'FROM-SYNC');
  assert.equal(posted.length, 1);
});

test('sandbox-user-statusline on PATH chains (the future flox binary hook)', async () => {
  const cfg = freshConfigDir();
  const bin = tempBinDir();
  writeExecutable(join(bin, 'sandbox-user-statusline'), '#!/bin/sh\ncat >/dev/null\nprintf FROM-PATH\n');
  const { line, posted } = await runStatusline(cfg, { pathPrepend: bin });
  assert.equal(line, 'FROM-PATH');
  assert.equal(posted.length, 1);
});

test('nonzero exit → built-in line (user output discarded); POST still fires', async () => {
  const cfg = freshConfigDir();
  writeExecutable(join(cfg, 'pane-observer', 'user-statusline'), '#!/bin/sh\necho garbage\nexit 3\n');
  const { line, posted } = await runStatusline(cfg);
  assert.equal(line, BUILTIN_LINE);
  assert.equal(posted.length, 1);
});

test('timeout → built-in line; POST still fires', async () => {
  const cfg = freshConfigDir();
  writeExecutable(
    join(cfg, 'pane-observer', 'user-statusline'),
    '#!/bin/sh\ncat >/dev/null\nsleep 5\nprintf late\n',
  );
  const started = Date.now();
  const { line, posted } = await runStatusline(cfg, { timeoutMs: 250 });
  assert.equal(line, BUILTIN_LINE);
  assert.equal(posted.length, 1);
  // The chain gave up at ~250ms, not the sleep's 5s.
  assert.ok(Date.now() - started < 4000, `took ${Date.now() - started}ms`);
});

test('empty stdout with exit 0 → built-in line (a blank statusline is useless)', async () => {
  const cfg = freshConfigDir();
  writeExecutable(join(cfg, 'pane-observer', 'user-statusline'), '#!/bin/sh\ncat >/dev/null\nexit 0\n');
  const { line, posted } = await runStatusline(cfg);
  assert.equal(line, BUILTIN_LINE);
  assert.equal(posted.length, 1);
});

// --- claude.ai plan usage windows ------------------------------------------
//
// `rate_limits` rides in on the SAME stdin payload the observer POST consumes,
// and it is the ONLY channel by which a pod learns its plan usage — the
// session's token is inference-scoped and cannot call /api/oauth/usage the way
// the host-side statusline does. Before this, the built-in line dropped the
// windows on the floor and a sandbox session showed no 5h/weekly at all.

test('rate_limits in the payload → 5h + weekly windows on the built-in line', async () => {
  const cfg = freshConfigDir();
  // resets_at are absolute epoch SECONDS; pick offsets from now so the
  // countdown is deterministic without freezing the clock. The countdown FLOORS
  // to whole minutes, so each offset carries an extra 59s of slack — the
  // rendered value then survives up to a minute of test-run drift instead of
  // flaking one unit low the moment the spawn takes a millisecond.
  const now = Math.floor(Date.now() / 1000);
  const stdin = JSON.stringify({
    model: { id: 'claude-opus-4-8', display_name: 'Opus 4.8' },
    context_window: { used_percentage: 16 },
    rate_limits: {
      five_hour: { used_percentage: 41.4, resets_at: now + 2 * 3600 + 12 * 60 + 59 },
      seven_day: { used_percentage: 4, resets_at: now + 3 * 86400 + 4 * 3600 + 59 * 60 },
    },
  });
  const { line, posted } = await runStatusline(cfg, { stdin });
  assert.equal(line, 'Opus 4.8 · ctx 16% · 5h 41% 2h12m · wk 4% 3d4h');
  assert.equal(posted.length, 1);
  assert.equal(posted[0].body, stdin); // the metrics POST is still verbatim
});

test('a window without resets_at prints its percentage alone', async () => {
  const cfg = freshConfigDir();
  const stdin = JSON.stringify({
    model: { display_name: 'Opus 4.8' },
    context_window: { used_percentage: 16 },
    rate_limits: { five_hour: { used_percentage: 41 } },
  });
  const { line } = await runStatusline(cfg, { stdin });
  assert.equal(line, 'Opus 4.8 · ctx 16% · 5h 41%');
});

test('an already-elapsed resets_at prints no countdown (never a negative one)', async () => {
  const cfg = freshConfigDir();
  const stdin = JSON.stringify({
    model: { display_name: 'Opus 4.8' },
    rate_limits: { seven_day: { used_percentage: 88, resets_at: Math.floor(Date.now() / 1000) - 60 } },
  });
  const { line } = await runStatusline(cfg, { stdin });
  assert.equal(line, 'Opus 4.8 · wk 88%');
});

test('no rate_limits (API-key / pre-first-response) → the model+ctx line, unchanged', async () => {
  const cfg = freshConfigDir();
  const { line } = await runStatusline(cfg);
  assert.equal(line, BUILTIN_LINE);
});

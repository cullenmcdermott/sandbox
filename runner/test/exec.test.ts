// Runner unit tests for the one-shot exec endpoint helper (Phase 2a). Runs in a
// temp cwd so it doesn't depend on the pod's /session/workspace mount.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { tmpdir } from 'node:os';
import { writeFileSync, rmSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { runExec, EXEC_BLOCKED_EXIT } from '../src/exec.js';

test('captures stdout and a zero exit code', async () => {
  const r = await runExec('printf hello', tmpdir());
  assert.equal(r.stdout, 'hello');
  assert.equal(r.exitCode, 0);
});

test('captures stderr and a nonzero exit code', async () => {
  const r = await runExec('echo oops >&2; exit 3', tmpdir());
  assert.match(r.stderr, /oops/);
  assert.equal(r.exitCode, 3);
});

test('bounds oversized output with a truncation marker', async () => {
  // ~70 KiB of output exceeds the 64 KiB cap.
  const r = await runExec("head -c 70000 /dev/zero | tr '\\0' a", tmpdir());
  assert.ok(r.stdout.length <= 64 * 1024 + 64, `output not bounded: ${r.stdout.length}`);
  assert.match(r.stdout, /output truncated/);
});

test('a spawn failure surfaces as exitCode 127, never a throw', async () => {
  const r = await runExec('definitely-not-a-real-command-xyz', tmpdir());
  assert.notEqual(r.exitCode, 0);
});

// REGRESSION (O2): /exec must apply the same blocklist as the SDK Bash tool, so
// `!cmd` is not an unguarded shell escape around it. A blocked command must be
// refused BEFORE the shell runs — proven by a marker file that only appears if
// bash actually executed the command.
test('a blocked command is refused before the shell runs', async () => {
  const marker = join(tmpdir(), `exec-guard-marker-${process.pid}-${Date.now()}`);
  if (existsSync(marker)) rmSync(marker);
  try {
    // If this string reached `bash -c`, kubectl would fail (absent) and the
    // `|| touch` would create the marker. A correct guard prevents the spawn.
    const r = await runExec(`kubectl get nodes || touch ${marker}`, tmpdir());
    assert.equal(r.exitCode, EXEC_BLOCKED_EXIT, 'blocked command should return the guard exit code');
    assert.match(r.stderr, /blocked by sandbox exec guard/);
    assert.equal(existsSync(marker), false, 'blocked command must not have executed in a shell');
  } finally {
    if (existsSync(marker)) rmSync(marker);
  }
});

// REGRESSION (B3): a command that backgrounds a child (`sleep 30 &`) leaves the
// grandchild holding the stdout pipe, so the old resolve-on-'close' hung until
// the grandchild died (~30s). runExec now resolves on the direct bash's 'exit',
// so this must return promptly with the foreground output captured.
test('backgrounding a child does not hang exec past the parent exit', async () => {
  const start = Date.now();
  const r = await runExec('sleep 30 & echo done', tmpdir());
  const elapsed = Date.now() - start;
  assert.match(r.stdout, /done/, 'foreground output must be captured');
  assert.equal(r.exitCode, 0);
  assert.ok(elapsed < 5000, `must resolve at bash exit, not wait for the child: ${elapsed}ms`);
});

// REGRESSION (B3): the timeout SIGKILLs the whole process GROUP (detached child),
// so a command whose bash stays alive (blocked in `wait`) — and its backgrounded
// grandchild — are both killed near the timeout rather than surviving. Uses a
// sub-second injected timeout to keep the test fast.
test('the timeout kills the backgrounded process group and reports 124', async () => {
  const start = Date.now();
  const r = await runExec('sleep 30 & wait', tmpdir(), 300);
  const elapsed = Date.now() - start;
  assert.equal(r.exitCode, 124, 'a timed-out command returns 124');
  assert.ok(elapsed < 5000, `must be killed near the timeout, not wait 30s: ${elapsed}ms`);
});

// Sanity: a benign command sharing no blocked token still runs normally.
test('a benign command still executes', async () => {
  const probe = join(tmpdir(), `exec-benign-${process.pid}-${Date.now()}`);
  if (existsSync(probe)) rmSync(probe);
  try {
    writeFileSync(probe, 'ok');
    const r = await runExec(`cat ${probe}`, tmpdir());
    assert.equal(r.exitCode, 0);
    assert.equal(r.stdout, 'ok');
  } finally {
    if (existsSync(probe)) rmSync(probe);
  }
});

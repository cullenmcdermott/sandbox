// One-shot shell exec for the chat `!cmd` passthrough (slice 2a). Each call
// runs an independent command in the session cwd — no persisted cd/env between
// calls. Output is bounded and the command is killed after a timeout so a
// runaway or interactive program can't wedge the runner. Interactive programs
// are unsupported (stdin is closed).

import { spawn, type ChildProcess } from 'node:child_process';
import { isAbsolute, resolve, sep } from 'node:path';
import { loadConfig } from './session.js';
import { type ExecResponse } from './types.js';
import { bashCommandBlocked } from './guards.js';

/** Exit code used when a command is refused by the shared bash guardrails. */
export const EXEC_BLOCKED_EXIT = 126;

/** Per-stream output cap (bytes). Output beyond this is truncated. */
const MAX_OUTPUT = 64 * 1024;
/** Hard wall-clock limit for a single command. */
const EXEC_TIMEOUT_MS = 30_000;

// The runner's own infrastructure secrets. These gate the runner API and the
// model providers; a `!cmd` passthrough must not be able to read them (e.g.
// `!env` would otherwise print RUNNER_TOKEN, the bearer token protecting the
// whole API). User-workflow vars (PATH, HOME, GITHUB_TOKEN, etc.) are kept so
// `!cmd` still works — this is a denylist of the runner's own secrets, M14.
const RUNNER_SECRET_ENV_KEYS = new Set([
  'RUNNER_TOKEN',
  'ANTHROPIC_API_KEY',
  'OPENAI_API_KEY',
  'OPENCODE_API_KEY',
  'OPENCODE_SERVER_PASSWORD',
  'CLAUDE_CODE_OAUTH_TOKEN',
]);

/** Returns process.env with the runner's own infrastructure secrets removed. */
export function sanitizedExecEnv(env: NodeJS.ProcessEnv = process.env): NodeJS.ProcessEnv {
  const out: NodeJS.ProcessEnv = {};
  for (const [k, v] of Object.entries(env)) {
    if (RUNNER_SECRET_ENV_KEYS.has(k)) continue;
    out[k] = v;
  }
  return out;
}

/**
 * Resolve the SDK/exec working directory: the real host project path (e.g.
 * /Users/cullen/git/homelab), which the pod bind-mounts from the session PVC
 * (see k8s runnerVolumeMounts). Running at the real path — not under
 * /session/workspace — makes the SDK's transcript dir match the host's so a k8s
 * session can be resumed locally (TODO.md "Resumable transcripts").
 *
 * projectPath comes from the PROJECT_PATH env the CLI sets; reject a
 * non-absolute or `..`-bearing path (M15) so a crafted value can't point cwd at
 * an arbitrary location.
 */
export function resolveWorkspaceDir(projectPath: string): string {
  if (!isAbsolute(projectPath) || projectPath.split(sep).includes('..')) {
    throw new Error(`projectPath must be an absolute path without traversal: ${projectPath}`);
  }
  return resolve(projectPath);
}

/** runExec's result is the /exec HTTP response body (types.ts ExecResponse):
 * the route returns it verbatim, so they are the same contract. */
export type ExecResult = ExecResponse;

/** Append chunk to buf, capping at MAX_OUTPUT; sets truncated.flag when clipped. */
function capAppend(buf: string, chunk: string, truncated: { flag: boolean }): string {
  if (buf.length >= MAX_OUTPUT) {
    truncated.flag = true;
    return buf;
  }
  const room = MAX_OUTPUT - buf.length;
  if (chunk.length > room) {
    truncated.flag = true;
    return buf + chunk.slice(0, room);
  }
  return buf + chunk;
}

// Kill the whole process group led by `child`. `detached: true` makes the child
// a group leader (its own session/pgid), so a negative-pid signal reaches every
// descendant — including a backgrounded grandchild (`cmd &`) that inherited the
// stdout pipe. Killing only the direct bash (B3) would orphan such a grandchild
// and leak it. Falls back to killing the direct child if the group is already
// gone (ESRCH) or the pid is unknown.
function killProcessGroup(child: ChildProcess, signal: NodeJS.Signals): void {
  if (typeof child.pid === 'number') {
    try {
      process.kill(-child.pid, signal);
      return;
    } catch {
      /* group already gone / not a group leader — fall through */
    }
  }
  try {
    child.kill(signal);
  } catch {
    /* already gone */
  }
}

// runExec runs `command` via `bash -c` in `cwd` (defaulting to the session
// project cwd) and resolves with the captured output and exit code. It never
// rejects: spawn errors and timeouts surface as a non-zero exitCode (127 spawn
// failure, 124 timeout).
//
// B3: it resolves on the child's 'exit' (the direct bash terminating), NOT on
// 'close' (which waits for every stdio pipe to close). A command that backgrounds
// a child — `sleep 30 &` — leaves that grandchild holding the stdout pipe open,
// so 'close' would not fire until the grandchild dies (minutes) and the caller
// (and the TUI's `!cmd`) would hang despite the timeout. Trade-off: any output a
// surviving grandchild writes AFTER bash exits is dropped. `timeoutMs` is
// injectable for tests.
export function runExec(command: string, cwd?: string, timeoutMs: number = EXEC_TIMEOUT_MS): Promise<ExecResult> {
  // Apply the SAME blocklist the SDK Bash tool enforces in its PreToolUse hook,
  // so `!cmd` is not an unguarded escape around it (O2). Refused before spawn.
  if (bashCommandBlocked(command)) {
    return Promise.resolve({
      stdout: '',
      stderr:
        'blocked by sandbox exec guard: command matches a host/cluster/credential pattern',
      exitCode: EXEC_BLOCKED_EXIT,
    });
  }
  const dir = cwd ?? resolveWorkspaceDir(loadConfig().projectPath);
  return new Promise<ExecResult>((resolve) => {
    const child = spawn('bash', ['-c', command], {
      cwd: dir,
      env: sanitizedExecEnv(),
      stdio: ['ignore', 'pipe', 'pipe'],
      // Own process group so the timeout can SIGKILL the whole tree (B3).
      detached: true,
    });
    let stdout = '';
    let stderr = '';
    const truncated = { flag: false };
    let timedOut = false;
    let settled = false;

    child.stdout.on('data', (d: Buffer) => {
      stdout = capAppend(stdout, d.toString(), truncated);
    });
    child.stderr.on('data', (d: Buffer) => {
      stderr = capAppend(stderr, d.toString(), truncated);
    });

    const timer = setTimeout(() => {
      timedOut = true;
      killProcessGroup(child, 'SIGKILL');
    }, timeoutMs);

    child.on('error', (e: Error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve({ stdout, stderr: stderr + String(e.message), exitCode: 127 });
    });
    child.on('exit', (code: number | null) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      // Resolve after one turn of the loop so any stdout/stderr already buffered
      // from the direct child is delivered before we read it — 'exit' can fire
      // before the final 'data' events drain. Then destroy our end of the pipes
      // so a surviving detached grandchild holding the write end cannot keep the
      // event loop (and this process) alive.
      setImmediate(() => {
        if (truncated.flag) stdout += '\n…[output truncated]';
        const exitCode = timedOut ? 124 : code ?? -1;
        resolve({ stdout, stderr, exitCode });
        child.stdout?.destroy();
        child.stderr?.destroy();
      });
    });
  });
}

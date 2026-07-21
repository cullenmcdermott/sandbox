// Codex backend supervisor (backend id `codex-app-server`).
//
// For codex sessions the runner stays the pod's control plane (/healthz,
// /status, /idle for the reaper, SIGTERM handling) and supervises a child
// `codex app-server` process. That server listens on a loopback WebSocket
// (ws://127.0.0.1:<port>); the local `codex` TUI attaches to it over a
// port-forward and the runner's passive metrics observer (codex-observer.ts)
// connects to the SAME socket. We do NOT proxy the interactive client through
// the runner — codex is a supervise-only backend (agent.ts selectAgent returns
// null for it, so POST /turns 409s).
//
// This module mirrors opencode.ts (the template): a fail-closed auth
// materialization step, a restart-on-exit supervisor, and an activity poller
// that feeds the idle clock off live loopback connections. It reuses the
// generic connection-counting helpers from opencode.ts (externalClientConnections)
// since they are backend-agnostic (they parse /proc/net/tcp by port).

import { spawn, type ChildProcess } from 'node:child_process';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { type AuthFs, realAuthFs, writeAuthFile0600 } from './agent-auth.js';
import { sanitizedExecEnv } from './exec.js';
import { externalClientConnections } from './opencode.js';
import { getRegistry, setExternalActivityProbe } from './session.js';

/** Default loopback port `codex app-server --listen ws://127.0.0.1:<port>` binds.
 * Distinct from opencode's 4096 so the two backends never collide on a pod. */
const DEFAULT_PORT = 8788;

/** How often to check for a live codex client (feeds the idle clock). Mirrors
 * opencode.ts ACTIVITY_POLL_MS — a codex session has no runner turn and no SSE
 * client, so a connected loopback ws client is its only liveness signal. */
const ACTIVITY_POLL_MS = 20_000;

/** Max time to wait for `codex app-server` to exit after SIGTERM before SIGKILL.
 * Mirrors opencode.ts STOP_GRACE_MS so shutdown never blocks past the pod grace
 * period. */
export const STOP_GRACE_MS = 5_000;

/** Injectable filesystem surface for materializeCodexAuth so the auth-seeding +
 * fail-closed logic is unit-testable off-pod (production uses node:fs). Aliases
 * the shared AuthFs (agent-auth.ts) so codex and opencode share one type. */
export type CodexAuthFs = AuthFs;

/** Resolve $CODEX_HOME (PVC-persisted state dir) from the env, defaulting to
 * ~/.codex the same way codex itself does. Exported so the boot wiring and the
 * observer agree on the path. */
export function codexHomeDir(env: NodeJS.ProcessEnv = process.env): string {
  return env.CODEX_HOME && env.CODEX_HOME.trim() ? env.CODEX_HOME : join(homedir(), '.codex');
}

/** Parse an auth.json `last_refresh` timestamp to epoch ms, or NaN when the
 * document is unparseable or carries no usable timestamp. Kept tolerant: any
 * failure yields NaN so the caller falls back to "seed wins". */
function lastRefreshMs(json: string): number {
  try {
    const doc = JSON.parse(json) as { last_refresh?: unknown };
    if (typeof doc.last_refresh === 'string') return Date.parse(doc.last_refresh);
    if (typeof doc.last_refresh === 'number') return doc.last_refresh;
  } catch {
    /* not JSON, or no last_refresh — fall through */
  }
  return NaN;
}

/**
 * Materialize the ChatGPT-OAuth auth.json into $CODEX_HOME and enforce the
 * fail-closed auth invariant. The k8s side injects the full auth.json document
 * as env CODEX_AUTH_JSON (from a per-session Secret) and sets CODEX_HOME to a
 * PVC-persisted dir; codex reads the on-disk auth.json, not the env.
 *
 * Seed-vs-on-disk reconciliation: a pod-side token refresh may have rewritten
 * auth.json with a NEWER credential than the operator's seed, so the seed must
 * NOT blindly clobber it. We overwrite only when the content actually differs,
 * and even then prefer the on-disk file when its `last_refresh` is newer than
 * the seed's (both trivially parseable). If either side is unparseable, the seed
 * wins — the operator explicitly rotated the credential. The auth.json contents
 * are NEVER logged.
 *
 * FAIL-CLOSED: after materialization, if there is no readable auth.json at
 * CODEX_HOME AND no OPENAI_API_KEY fallback in the env, throw — a codex pod with
 * no usable credential would otherwise start and fail every turn opaquely
 * (mirrors opencode's O3 fail-closed refusal). `fs` is injectable for tests.
 *
 * Returns the resolved $CODEX_HOME.
 */
export function materializeCodexAuth(env: NodeJS.ProcessEnv = process.env, fs: CodexAuthFs = realAuthFs): string {
  const codexHome = codexHomeDir(env);
  const authPath = join(codexHome, 'auth.json');

  if (env.CODEX_AUTH_JSON) {
    fs.mkdirSync(codexHome, { recursive: true });
    const seed = env.CODEX_AUTH_JSON;
    let existing: string | undefined;
    try {
      existing = fs.readFileSync(authPath, 'utf8') as string;
    } catch {
      existing = undefined; // absent/unreadable — seed it
    }
    if (existing === undefined) {
      writeAuthFile0600(fs, authPath, seed, 'codex: auth.json');
    } else if (existing !== seed) {
      // Content diverged: prefer the on-disk file only if it is demonstrably a
      // NEWER refresh; otherwise the operator rotated the credential — seed wins.
      const onDisk = lastRefreshMs(existing);
      const seedTs = lastRefreshMs(seed);
      const onDiskIsNewer = Number.isFinite(onDisk) && Number.isFinite(seedTs) && onDisk > seedTs;
      if (!onDiskIsNewer) writeAuthFile0600(fs, authPath, seed, 'codex: auth.json');
    }
    // existing === seed: identical, skip the write (avoids churning the PVC).
  }

  // Fail-closed: prove a usable credential is present before we let the pod come
  // up. Either a readable auth.json (ChatGPT OAuth) or the OPENAI_API_KEY fallback.
  let haveAuthFile = false;
  try {
    fs.readFileSync(authPath, 'utf8');
    haveAuthFile = true;
  } catch {
    haveAuthFile = false;
  }
  if (!haveAuthFile && !env.OPENAI_API_KEY) {
    throw new Error(
      `refusing to start \`codex app-server\`: no readable auth.json at ${authPath} and ` +
        'no OPENAI_API_KEY fallback — a codex pod with no credential fails every turn opaquely',
    );
  }
  return codexHome;
}

/**
 * Build the env for the `codex app-server` child: sanitizedExecEnv (drops
 * RUNNER_TOKEN + the other runner-infra secrets, A1) with only the credentials
 * codex genuinely needs restored — OPENAI_API_KEY (sanitizedExecEnv strips it as
 * a runner secret) and CODEX_HOME. CODEX_AUTH_JSON is DELIBERATELY removed: the
 * child reads the materialized on-disk auth.json, and the raw seed must never
 * leak into the agent's process env (an in-agent shell could otherwise read the
 * full OAuth document). Pure/exported for unit tests.
 */
export function buildCodexServeEnv(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const out = sanitizedExecEnv(env);
  if (env.OPENAI_API_KEY !== undefined) out.OPENAI_API_KEY = env.OPENAI_API_KEY;
  if (env.CODEX_HOME !== undefined) out.CODEX_HOME = env.CODEX_HOME;
  // Never pass the raw OAuth seed to the child — it reads auth.json instead.
  delete out.CODEX_AUTH_JSON;
  return out;
}

export interface CodexSupervisor {
  /**
   * Terminate the child (SIGTERM) and resolve once it has actually exited, or
   * after STOP_GRACE_MS (escalating to SIGKILL). Awaiting this on shutdown keeps
   * the runner from exiting while `codex app-server` is still alive (O5). Also
   * clears the activity poller + external-activity probe.
   */
  stop(): Promise<void>;
  /** The child process, for liveness checks. */
  child: ChildProcess;
}

/**
 * Spawn `codex app-server --listen ws://127.0.0.1:<port>`, restarting it on
 * unexpected exit (until stop() is called). Loopback bind needs no ws auth (the
 * pod network can't reach 127.0.0.1), so — unlike opencode's 0.0.0.0 bind — there
 * is no server-password fail-closed here; the auth fail-closed lives in
 * materializeCodexAuth, which index.ts runs first.
 *
 * `spawnFn` is injectable for tests; production uses node's child_process spawn.
 * NOTE: this does NOT materialize auth — index.ts calls materializeCodexAuth()
 * before this, so the supervisor stays free of filesystem side effects and is
 * trivially unit-testable with a fake spawn.
 */
export function startCodexSupervisor(
  env: NodeJS.ProcessEnv = process.env,
  spawnFn: typeof spawn = spawn,
): CodexSupervisor {
  const port = parseInt(env.CODEX_PORT ?? String(DEFAULT_PORT), 10);
  const listen = `ws://127.0.0.1:${port}`;
  console.log(`codex: starting app-server on ${listen}`);
  // Feed /idle synchronously off live loopback ws clients (the interactive codex
  // TUI). The observer's own ws client is runner-owned and subtracted out.
  setExternalActivityProbe(() => externalClientConnections(port) > 0);

  let stopped = false;
  let child: ChildProcess;

  // Activity poller (Seam B): while a codex client is connected, mark the session
  // active so the idle reaper doesn't suspend it mid-session.
  const activityTimer = setInterval(() => {
    try {
      if (externalClientConnections(port) > 0) getRegistry().setExternalActivity();
    } catch {
      /* registry not ready / proc unreadable — best effort */
    }
  }, ACTIVITY_POLL_MS);

  // A1: strip RUNNER_TOKEN + the raw auth seed from the child (keep OPENAI_API_KEY
  // + CODEX_HOME).
  const childEnv = buildCodexServeEnv(env);
  const spawnServe = (): ChildProcess => {
    const proc = spawnFn('codex', ['app-server', '--listen', listen], {
      stdio: 'inherit',
      env: childEnv,
      cwd: env.PROJECT_PATH ?? process.cwd(),
    });
    // B1: a spawn failure (ENOENT `codex`, EAGAIN) emits 'error' on the child with
    // NO 'exit'; with no listener Node re-throws it as an uncaught exception that
    // kills the whole runner. Both 'error' and 'exit' schedule the SAME backoff
    // respawn, guarded by a per-child flag so a child that fires both respawns at
    // most once.
    let respawnScheduled = false;
    const scheduleRespawn = (why: string): void => {
      if (stopped || respawnScheduled) return;
      respawnScheduled = true;
      console.error(`codex app-server ${why}; restarting in 1s`);
      setTimeout(() => {
        if (!stopped) child = spawnServe();
      }, 1000);
    };
    proc.on('exit', (code, signal) => {
      scheduleRespawn(`exited (code=${code} signal=${signal})`);
    });
    proc.on('error', (err: Error) => {
      scheduleRespawn(`failed to spawn (${err.message})`);
    });
    return proc;
  };

  child = spawnServe();

  return {
    child,
    stop(): Promise<void> {
      stopped = true;
      setExternalActivityProbe(null);
      clearInterval(activityTimer);
      // Operate on the current child (the restart handler may have reassigned it).
      const c = child;
      if (c.exitCode !== null || c.signalCode !== null) return Promise.resolve();
      return new Promise<void>((resolve) => {
        const onExit = (): void => {
          clearTimeout(killTimer);
          resolve();
        };
        c.once('exit', onExit);
        // Arm the SIGKILL escalation BEFORE SIGTERM so a synchronous exit still
        // clears it; this guarantees stop() resolves within STOP_GRACE_MS.
        const killTimer = setTimeout(() => {
          try {
            c.kill('SIGKILL');
          } catch {
            /* already gone */
          }
        }, STOP_GRACE_MS);
        c.kill('SIGTERM');
      });
    },
  };
}

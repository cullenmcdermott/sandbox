// OpenCode backend supervisor.
//
// For opencode-server sessions the runner stays the pod's control plane
// (/healthz, /status, /idle for the reaper, SIGTERM handling) and supervises a
// child `opencode serve` process. The local `opencode attach` client talks to
// that server over a port-forward; we do NOT proxy it through the runner.
//
// On boot we generate an opencode config from whichever provider API keys are
// present in the environment (injected from cluster Secrets by the pod spec).
// opencode also auto-detects ANTHROPIC_API_KEY / OPENAI_API_KEY / OPENCODE_API_KEY
// from env on its own; the generated file mainly pins a default model and gives
// OPENCODE_CONFIG a valid target.

import { spawn, type ChildProcess } from 'node:child_process';
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { getRegistry } from './session.js';

const DEFAULT_PORT = 4096;

/** How often to check for a live opencode client (feeds the idle clock). */
const ACTIVITY_POLL_MS = 20_000;

/**
 * Count ESTABLISHED TCP connections whose local port is `port`, by parsing
 * /proc/net/tcp[6]. An attached `opencode attach` client holds such a
 * connection, so a non-zero count means the session is in use. This is
 * self-contained (no dependency on opencode's API) and Linux-native (the pod).
 */
export function establishedConnections(port: number, readFile = readFileSync): number {
  let count = 0;
  for (const path of ['/proc/net/tcp', '/proc/net/tcp6']) {
    let text: string;
    try {
      text = readFile(path, 'utf8') as string;
    } catch {
      continue; // file absent (non-Linux/test) — treat as no connections
    }
    const lines = text.split('\n').slice(1); // drop header
    for (const line of lines) {
      const cols = line.trim().split(/\s+/);
      if (cols.length < 4) continue;
      // cols[1] = local_address "HEXIP:HEXPORT"; cols[3] = state (01=ESTABLISHED)
      const localPort = parseInt(cols[1].split(':')[1] ?? '', 16);
      if (localPort === port && cols[3] === '01') count++;
    }
  }
  return count;
}

/** Provider env var -> opencode provider id, used to decide what to enable. */
const PROVIDER_ENV: Record<string, string> = {
  ANTHROPIC_API_KEY: 'anthropic',
  OPENAI_API_KEY: 'openai',
  OPENCODE_API_KEY: 'opencode', // opencode Zen
};

/** Build the opencode config object from the providers present in env. */
export function buildOpencodeConfig(env: NodeJS.ProcessEnv = process.env): Record<string, unknown> {
  const provider: Record<string, unknown> = {};
  for (const [envVar, id] of Object.entries(PROVIDER_ENV)) {
    if (env[envVar]) {
      // opencode substitutes {env:VAR} at load time; this keeps the key out of
      // the on-disk config while still pinning which providers are enabled.
      provider[id] = { options: { apiKey: `{env:${envVar}}` } };
    }
  }

  const config: Record<string, unknown> = {
    $schema: 'https://opencode.ai/config.json',
  };
  if (Object.keys(provider).length > 0) {
    config.provider = provider;
  }
  // Default model is operator-controlled (cluster env) rather than guessed, so
  // we never pin a model id the installed opencode build doesn't know.
  if (env.OPENCODE_DEFAULT_MODEL) {
    config.model = env.OPENCODE_DEFAULT_MODEL;
  }
  if (env.OPENCODE_SMALL_MODEL) {
    config.small_model = env.OPENCODE_SMALL_MODEL;
  }
  return config;
}

/** Write the generated config to OPENCODE_CONFIG (creating parent dirs). */
export function writeOpencodeConfig(env: NodeJS.ProcessEnv = process.env): string | null {
  const path = env.OPENCODE_CONFIG;
  if (!path) return null;
  mkdirSync(dirname(path), { recursive: true });
  const cfg = buildOpencodeConfig(env);
  writeFileSync(path, JSON.stringify(cfg, null, 2) + '\n', 'utf8');
  return path;
}

/** Max time to wait for `opencode serve` to exit after SIGTERM before SIGKILL. */
export const STOP_GRACE_MS = 5_000;

export interface OpencodeSupervisor {
  /**
   * Terminate the child (SIGTERM) and resolve once it has actually exited, or
   * after STOP_GRACE_MS (escalating to SIGKILL). Awaiting this on shutdown
   * keeps the runner from exiting while `opencode serve` is still alive, which
   * would orphan it (O5).
   */
  stop(): Promise<void>;
  /** The child process, for liveness checks. */
  child: ChildProcess;
}

/**
 * Generate the config and spawn `opencode serve`, restarting it on unexpected
 * exit (until stop() is called). Binds 0.0.0.0:<OPENCODE_PORT> so the
 * port-forwarded client can reach it; basic auth comes from
 * OPENCODE_SERVER_PASSWORD/USERNAME in the inherited env.
 *
 * FAIL-CLOSED (O3): `opencode serve` is an agent-with-shell bound to 0.0.0.0 on
 * the pod network. We refuse to start it unless OPENCODE_SERVER_PASSWORD is
 * present — otherwise a missing/empty credential would expose an UNAUTHENTICATED
 * shell to anything on the pod network. The Go backend injects this from a
 * required Secret (backend.go opencodeEnv, not Optional); this is the runner-side
 * backstop. NOTE: this guarantees the credential is *present*, not that the
 * pinned opencode build enforces it — auth enforcement remains an opencode/
 * homelab assumption that cannot be asserted from here.
 *
 * `spawnFn` is injectable for tests; production uses node's child_process spawn.
 */
export function startOpencodeSupervisor(
  env: NodeJS.ProcessEnv = process.env,
  spawnFn: typeof spawn = spawn,
): OpencodeSupervisor {
  if (!env.OPENCODE_SERVER_PASSWORD) {
    throw new Error(
      'refusing to start `opencode serve`: OPENCODE_SERVER_PASSWORD is unset — ' +
        'binding an agent-with-shell to 0.0.0.0 without basic auth is unsafe (O3)',
    );
  }
  const port = parseInt(env.OPENCODE_PORT ?? String(DEFAULT_PORT), 10);
  const configPath = writeOpencodeConfig(env);
  console.log(`opencode: config written to ${configPath ?? '(none)'}; starting serve on :${port}`);

  let stopped = false;
  let child: ChildProcess;

  // Activity poller (Seam B): while an opencode client is connected, mark the
  // session active so the idle reaper doesn't suspend it. opencode sessions have
  // no runner turn and no SSE client, so this is their only liveness signal.
  const activityTimer = setInterval(() => {
    try {
      if (establishedConnections(port) > 0) getRegistry().setExternalActivity();
    } catch {
      /* registry not ready / proc unreadable — best effort */
    }
  }, ACTIVITY_POLL_MS);

  const spawnServe = (): ChildProcess => {
    const proc = spawnFn(
      'opencode',
      ['serve', '--hostname', '0.0.0.0', '--port', String(port)],
      { stdio: 'inherit', env, cwd: env.PROJECT_PATH ?? process.cwd() },
    );
    proc.on('exit', (code, signal) => {
      if (stopped) return;
      console.error(`opencode serve exited (code=${code} signal=${signal}); restarting in 1s`);
      setTimeout(() => {
        if (!stopped) child = spawnServe();
      }, 1000);
    });
    return proc;
  };

  child = spawnServe();

  return {
    child,
    stop(): Promise<void> {
      stopped = true;
      clearInterval(activityTimer);
      // Operate on the current child (the restart handler may have reassigned
      // it). If it already exited there is nothing to wait for.
      const c = child;
      if (c.exitCode !== null || c.signalCode !== null) return Promise.resolve();
      return new Promise<void>((resolve) => {
        const onExit = (): void => {
          clearTimeout(killTimer);
          resolve();
        };
        c.once('exit', onExit);
        // Arm the SIGKILL escalation BEFORE SIGTERM so a synchronous exit still
        // clears it; this guarantees stop() resolves within STOP_GRACE_MS and
        // never blocks shutdown past the pod grace period.
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

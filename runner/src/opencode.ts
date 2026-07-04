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
import { mkdirSync, writeFileSync, readFileSync, readdirSync, readlinkSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { serializeBlockedPatterns } from './guards.js';
import { getRegistry, setExternalActivityProbe } from './session.js';

const DEFAULT_PORT = 4096;

/** How often to check for a live opencode client (feeds the idle clock). */
const ACTIVITY_POLL_MS = 20_000;

/**
 * Count ESTABLISHED TCP connections whose local port is `port`, by parsing
 * /proc/net/tcp[6]. An attached `opencode attach` client holds such a
 * connection. This is self-contained (no dependency on opencode's API) and
 * Linux-native (the pod).
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

/** Count runner-owned client sockets connected to opencode. These are internal
 * SDK clients (observer, warmup/headless turn, abort) and must not keep an
 * otherwise-detached pod alive. */
export function runnerOwnedConnections(
  port: number,
  readFile = readFileSync,
  readdir = readdirSync,
  readlink = readlinkSync,
): number {
  const ownedInodes = new Set<string>();
  try {
    for (const fd of readdir('/proc/self/fd')) {
      let target: string;
      try {
        target = readlink(`/proc/self/fd/${fd}`);
      } catch {
        continue; // fd disappeared between readdir and readlink.
      }
      const match = /^socket:\[(\d+)\]$/.exec(target);
      if (match) ownedInodes.add(match[1]);
    }
  } catch {
    return 0;
  }

  let count = 0;
  for (const path of ['/proc/net/tcp', '/proc/net/tcp6']) {
    let text: string;
    try {
      text = readFile(path, 'utf8') as string;
    } catch {
      continue;
    }
    const lines = text.split('\n').slice(1);
    for (const line of lines) {
      const cols = line.trim().split(/\s+/);
      if (cols.length < 10) continue;
      const remotePort = parseInt(cols[2].split(':')[1] ?? '', 16);
      const inode = cols[9];
      if (remotePort === port && cols[3] === '01' && ownedInodes.has(inode)) count++;
    }
  }
  return count;
}

/** ESTABLISHED server-side connections beyond runner-owned SDK clients. */
export function externalClientConnections(
  port: number,
  readFile = readFileSync,
  readdir = readdirSync,
  readlink = readlinkSync,
): number {
  return Math.max(0, establishedConnections(port, readFile) - runnerOwnedConnections(port, readFile, readdir, readlink));
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

// --- In-agent Bash guardrail plugin (defense-in-depth) --------------------
//
// The Claude backend gates Bash pre-execution via a PreToolUse hook and the
// /exec passthrough blocks the same patterns (guards.ts). opencode is the gap:
// its in-agent tools run inside the `opencode serve` child process, driven by an
// interactive `opencode attach` client the runner never proxies — so a
// runner-side HTTP interceptor cannot see, let alone block, them. Gating has to
// run INSIDE opencode's own process, which opencode supports via its JS plugin
// system: a `tool.execute.before` hook that THROWS prevents the tool running.
//
// We generate that plugin at boot from the SAME blocklist (guards.ts,
// serializeBlockedPatterns) so there is one source of truth, and register it via
// the config `plugin` array (opencode v1.17.7 treats a file:// / absolute-path
// spec as a local "file" plugin and imports it directly). Like guards.ts this is
// DEFENSE-IN-DEPTH only, NOT a hard boundary — the NetworkPolicy + the absent
// service-account token are; the patterns are trivially evadable.

/** Absolute path of the generated guardrail plugin (a sibling dir of the
 * opencode config), or null when OPENCODE_CONFIG is unset (no config to register
 * it in). A dedicated dir avoids opencode resolving a sibling package.json's
 * entrypoint instead of our file. */
export function guardrailPluginPath(env: NodeJS.ProcessEnv = process.env): string | null {
  const cfg = env.OPENCODE_CONFIG;
  if (!cfg) return null;
  return join(dirname(cfg), 'sandbox-plugin', 'guardrail.mjs');
}

/** The generated plugin's source. A flat ESM module whose single named export is
 * an opencode plugin (legacy function form — no id required) that blocks the
 * `bash` tool when its command matches the embedded blocklist. Kept dependency-
 * free (no import of guards.js) because it is imported by `opencode serve`, not
 * the runner. */
export function guardrailPluginSource(): string {
  return `// AUTO-GENERATED by the sandbox runner (opencode.ts) at boot from
// runner/src/guards.ts — DO NOT EDIT; changes here are overwritten every boot.
//
// Defense-in-depth Bash guardrail for the opencode backend, mirroring the Claude
// PreToolUse(Bash) hook. Loaded INSIDE \`opencode serve\` (the runner cannot
// proxy interactive opencode tool use). Throwing in tool.execute.before prevents
// the tool from running so the agent sees why. NOT a hard boundary: the
// NetworkPolicy + absent service-account token are; these patterns are trivially
// evadable (variable aliasing, base64, wrappers).

const BLOCKED_BASH_PATTERNS = ${serializeBlockedPatterns()};

export const SandboxGuardrail = async () => ({
  "tool.execute.before": async (input, output) => {
    if (!input || input.tool !== "bash") return;
    const command = String((output && output.args && output.args.command) || "");
    for (const re of BLOCKED_BASH_PATTERNS) {
      if (re.test(command)) {
        throw new Error(
          "blocked by sandbox runner guardrail: command matches a host/cluster/credential operation pattern",
        );
      }
    }
  },
});
`;
}

/**
 * Generate + install the guardrail plugin, returning a file:// URL to register in
 * the config `plugin` array, or null if it could not be installed. FAIL-OPEN by
 * design (defense-in-depth): a write failure must NOT stop `opencode serve` from
 * starting — but it is logged loudly because it silently drops the in-agent Bash
 * gate.
 */
export function writeOpencodeGuardrailPlugin(env: NodeJS.ProcessEnv = process.env): string | null {
  const file = guardrailPluginPath(env);
  if (!file) return null;
  try {
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, guardrailPluginSource(), 'utf8');
    console.log(`opencode: Bash guardrail plugin installed at ${file}`);
    return pathToFileURL(file).href;
  } catch (err) {
    console.error(
      'opencode: FAILED to install the Bash guardrail plugin — in-agent tool use will NOT be ' +
        'gated (defense-in-depth only; NetworkPolicy + absent SA token remain the real boundary):',
      err instanceof Error ? err.message : err,
    );
    return null;
  }
}

/** Write the generated config to OPENCODE_CONFIG (creating parent dirs). */
export function writeOpencodeConfig(env: NodeJS.ProcessEnv = process.env): string | null {
  const path = env.OPENCODE_CONFIG;
  if (!path) return null;
  mkdirSync(dirname(path), { recursive: true });
  const cfg = buildOpencodeConfig(env);
  // Register the in-agent Bash guardrail (best-effort; a failure logs and leaves
  // the array unset rather than blocking serve — see writeOpencodeGuardrailPlugin).
  const pluginUrl = writeOpencodeGuardrailPlugin(env);
  if (pluginUrl) cfg.plugin = [pluginUrl];
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
  setExternalActivityProbe(() => externalClientConnections(port) > 0);

  let stopped = false;
  let child: ChildProcess;

  // Activity poller (Seam B): while an opencode client is connected, mark the
  // session active so the idle reaper doesn't suspend it. opencode sessions have
  // no runner turn and no SSE client, so this is their only liveness signal.
  const activityTimer = setInterval(() => {
    try {
      if (externalClientConnections(port) > 0) getRegistry().setExternalActivity();
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
      setExternalActivityProbe(null);
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

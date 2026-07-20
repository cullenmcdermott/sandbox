// Claude interactive-pane supervisor (backend id `claude-pane`).
//
// For claude-pane sessions the runner stays the pod's control plane (/healthz,
// /status, /idle for the reaper, SIGTERM handling) and owns an INTERACTIVE
// `claude` Code child running inside a PTY. Unlike the claude-sdk backend (which
// drives headless query() turns via the Agent SDK), this backend runs the real
// interactive Claude Code TUI and streams its PTY bytes to a single attached
// WebSocket pane (the CLI's `sandbox` pane view over a port-forward). We do NOT
// proxy turns/permissions — the interactive child owns its own turn + approval
// UX; selectAgent returns null for claude-pane so POST /turns 409s.
//
// Design notes:
//   - LAZY spawn: the child is started on the FIRST pane attach, not at boot, so
//     a detached/suspended claude-pane pod never runs an idle interactive TUI.
//   - The claude session UUID is generated ONCE and persisted in session.json
//     (claude_pane_session_id). The very first spawn ever passes `--session-id
//     <uuid>`; every later spawn (after a child exit, or a fresh runner process
//     that inherits the persisted id) passes `--resume <uuid>` so the same
//     conversation continues.
//   - ENV ALLOWLIST ONLY: the child inherits a fixed, credential-free env. The
//     runner's own process env holds RUNNER_TOKEN and the OAuth/account secrets;
//     an interactive shell inside the pane must never see them (hooks inherit the
//     child env). This is a hard security constraint, mirrored from exec.ts's
//     sanitizedExecEnv denylist but implemented as a strict allowlist here.
//   - Single attacher: a new attach preempts the previous socket (close 4001).
//     On attach the accumulated scrollback (bounded ring buffer) is replayed as
//     one binary frame, then live PTY output follows.
//
// The node-pty dependency is required lazily (createRequire) inside the default
// spawner so importing this module — for unit tests or `tsc --noEmit` — never
// needs the compiled native addon (which is built in the runner image, not under
// `npm install --ignore-scripts`). Tests inject a fake spawner behind PaneSpawner.

import { createRequire } from 'node:module';
import { randomUUID } from 'node:crypto';
import { resolveWorkspaceDir } from './exec.js';
import { getRegistry, setExternalActivityProbe, type RunnerConfig } from './session.js';
import { CLAUDE_CONFIG_DIR } from './types.js';

/** Default scrollback retained and replayed to a (re)attaching pane, in bytes. */
export const SCROLLBACK_BYTES = 256 * 1024;

/** Default PTY geometry before the client sends its first resize control frame. */
const DEFAULT_COLS = 80;
const DEFAULT_ROWS = 24;

/** WebSocket close code used when a new pane attach preempts the current one. */
export const CLOSE_REPLACED = 4001;
/** WebSocket close code used when the interactive child process exits. */
export const CLOSE_CHILD_EXITED = 4002;

// --- PTY + socket seams (injectable for tests) ----------------------------

/** The narrow PTY surface the supervisor drives. The default spawner adapts
 * node-pty to this; tests supply a fake. Data is Buffers in BOTH directions so
 * the passthrough is binary-safe (e.g. terminal image/APC sequences survive). */
export interface PanePty {
  onData(cb: (data: Buffer) => void): void;
  onExit(cb: (info: { exitCode: number; signal?: number }) => void): void;
  write(data: Buffer): void;
  resize(cols: number, rows: number): void;
  kill(signal?: string): void;
}

export interface PaneSpawnOptions {
  command: string;
  args: string[];
  cwd: string;
  env: NodeJS.ProcessEnv;
  cols: number;
  rows: number;
}

/** Spawns a PTY child. Injected so the supervisor is unit-testable without the
 * native node-pty addon. */
export type PaneSpawner = (opts: PaneSpawnOptions) => PanePty;

/** The minimal socket surface the supervisor forwards to. A `ws` WebSocket
 * satisfies it structurally; server.ts adapts one at the upgrade seam. */
export interface PaneSocket {
  send(data: Buffer): void;
  close(code?: number, reason?: string): void;
}

/** Recorded outcome of the interactive child's last exit. */
export interface PaneExitInfo {
  code: number | null;
  signal: number | null;
  /** RFC3339 instant the child exited. */
  at: string;
}

// --- Scrollback ring ------------------------------------------------------

/**
 * A byte-bounded ring of recent PTY output. Retains at most `cap` bytes: once
 * appended output exceeds the cap, the oldest bytes are dropped (whole chunks
 * first, then a partial trim within the head chunk) so a long-lived session
 * keeps only the tail. A single chunk larger than the cap is itself tail-trimmed.
 * Pure + exported for unit tests.
 */
export class ScrollbackRing {
  private chunks: Buffer[] = [];
  private total = 0;

  constructor(private readonly cap: number = SCROLLBACK_BYTES) {}

  push(data: Buffer): void {
    if (data.length === 0) return;
    this.chunks.push(data);
    this.total += data.length;
    while (this.total > this.cap) {
      const head = this.chunks[0];
      const over = this.total - this.cap;
      if (head.length <= over) {
        // Dropping the whole head still leaves us at/over cap; evict it.
        this.chunks.shift();
        this.total -= head.length;
      } else {
        // Trim exactly `over` bytes off the front of the head chunk (this brings
        // total to cap, ending the loop).
        this.chunks[0] = head.subarray(over);
        this.total -= over;
      }
    }
  }

  /** A single Buffer snapshot of the retained scrollback (oldest → newest). */
  snapshot(): Buffer {
    return Buffer.concat(this.chunks, this.total);
  }

  get size(): number {
    return this.total;
  }
}

// --- Env allowlist --------------------------------------------------------

/** Env keys passed THROUGH to the interactive child from the runner env (when
 * present). Everything else — including RUNNER_TOKEN, CLAUDE_CREDENTIALS_JSON,
 * CLAUDE_OAUTH_ACCOUNT_JSON, and every provider key — is withheld. */
const PANE_ENV_PASSTHROUGH = ['PATH', 'HOME', 'LANG'] as const;

/**
 * Build the interactive child's env as a strict ALLOWLIST: fixed terminal vars
 * plus a handful of passthrough keys (PATH/HOME/LANG). CLAUDE_CONFIG_DIR points
 * the child at the same PVC-backed config dir the claude-sdk backend uses
 * (falling back to the shared constant when the env doesn't carry it) so the
 * interactive TUI reads the materialized ~/.claude credential + settings. The
 * runner's secrets never appear here — this is the hard boundary that keeps an
 * in-pane shell (and any hook it runs) from reading the bearer token or OAuth
 * material. Pure/exported for unit tests.
 */
export function buildClaudePaneEnv(env: NodeJS.ProcessEnv = process.env): NodeJS.ProcessEnv {
  const out: NodeJS.ProcessEnv = {
    TERM: 'xterm-256color',
    COLORTERM: 'truecolor',
    CLAUDE_CONFIG_DIR: env.CLAUDE_CONFIG_DIR || CLAUDE_CONFIG_DIR,
  };
  for (const k of PANE_ENV_PASSTHROUGH) {
    if (env[k] !== undefined) out[k] = env[k];
  }
  return out;
}

// --- Spawn arg selection --------------------------------------------------

/**
 * The `claude` args for a pane spawn. The very first spawn ever for a session
 * (no persisted uuid yet) STARTS a session with `--session-id <uuid>`; every
 * later spawn RESUMES it with `--resume <uuid>`. Pure/exported for unit tests.
 */
export function claudePaneArgs(uuid: string, resume: boolean): string[] {
  return resume ? ['--resume', uuid] : ['--session-id', uuid];
}

// --- Persistence seam -----------------------------------------------------

/** Read/persist the session's claude-pane UUID. Backed by the session registry
 * (session.json) in production; a plain object in tests. */
export interface ClaudePanePersistence {
  /** The persisted uuid, or '' when the session has never spawned a pane. */
  get(): string;
  /** Persist a freshly generated uuid (called once, on the first spawn ever). */
  set(uuid: string): void;
}

// --- Supervisor -----------------------------------------------------------

export interface ClaudePaneSupervisor {
  /** Attach a pane socket. Preempts any current socket (close 4001), spawns the
   * child on the first attach, replays scrollback, then forwards live output. */
  attach(socket: PaneSocket): void;
  /** Drop the current socket (close 1000). The child keeps running so a later
   * attach resumes it; idle reaping handles suspend. Idempotent. */
  detachAll(): void;
  /** Resize the PTY (and remember the geometry for the next spawn). */
  resize(cols: number, rows: number): void;
  /** Write client input to the PTY. No-op when no child is running. */
  write(data: Buffer): void;
  /** True while an interactive child is alive. */
  running(): boolean;
  /** True while a pane socket is attached (drives the external-activity probe). */
  attached(): boolean;
  /** The current pane socket (for the server's close-identity check), or null. */
  current(): PaneSocket | null;
  /** The child's last recorded exit, or null if it has not exited. */
  lastExit(): PaneExitInfo | null;
  /** Terminate the child and stop accepting attaches (runner shutdown). */
  stop(): void;
}

export interface ClaudePaneDeps {
  /** Working directory for the interactive child (the session workspace). */
  cwd: string;
  persistence: ClaudePanePersistence;
  /** Runner env the allowlist is derived from (defaults to process.env). */
  env?: NodeJS.ProcessEnv;
  /** PTY spawner (defaults to the node-pty adapter). */
  spawn?: PaneSpawner;
  /** UUID generator for the first spawn (defaults to node:crypto randomUUID). */
  generateUuid?: () => string;
  /** Notified when the child exits (the observer integration lands in a later
   * task; kept as a narrow seam for now). */
  onExit?: (info: PaneExitInfo) => void;
  /** Called once from stop() (e.g. to clear the external-activity probe). */
  onStop?: () => void;
  /** Scrollback cap in bytes (defaults to SCROLLBACK_BYTES). */
  scrollbackBytes?: number;
}

class Supervisor implements ClaudePaneSupervisor {
  private readonly cwd: string;
  private readonly env: NodeJS.ProcessEnv;
  private readonly spawnFn: PaneSpawner;
  private readonly generateUuid: () => string;
  private readonly persistence: ClaudePanePersistence;
  private readonly onExitCb: ((info: PaneExitInfo) => void) | undefined;
  private readonly onStopCb: (() => void) | undefined;
  private readonly ring: ScrollbackRing;

  private pty: PanePty | null = null;
  private socket: PaneSocket | null = null;
  private cols = DEFAULT_COLS;
  private rows = DEFAULT_ROWS;
  private exitInfo: PaneExitInfo | null = null;
  private stopped = false;

  constructor(deps: ClaudePaneDeps) {
    this.cwd = deps.cwd;
    this.env = deps.env ?? process.env;
    this.spawnFn = deps.spawn ?? defaultPaneSpawner;
    this.generateUuid = deps.generateUuid ?? randomUUID;
    this.persistence = deps.persistence;
    this.onExitCb = deps.onExit;
    this.onStopCb = deps.onStop;
    this.ring = new ScrollbackRing(deps.scrollbackBytes ?? SCROLLBACK_BYTES);
  }

  attach(socket: PaneSocket): void {
    if (this.stopped) {
      // The runner is shutting down; refuse the attach so the client reconnects
      // once the pod is back rather than talking to a dying child.
      try {
        socket.close(CLOSE_CHILD_EXITED, 'runner stopping');
      } catch {
        /* socket already gone */
      }
      return;
    }
    // Single attacher: preempt the previous socket so only one pane drives the
    // PTY. Its own 'close' fires server-side but is ignored (identity check).
    if (this.socket && this.socket !== socket) {
      try {
        this.socket.close(CLOSE_REPLACED, 'replaced by a new pane attach');
      } catch {
        /* previous socket already gone */
      }
    }
    this.socket = socket;
    this.ensureSpawned();
    // Replay accumulated scrollback so a (re)attaching pane catches up, as one
    // binary frame; live output follows via the onData forwarder. No data can
    // interleave here — attach() runs to completion synchronously.
    const snap = this.ring.snapshot();
    if (snap.length > 0) this.safeSend(snap);
  }

  detachAll(): void {
    if (!this.socket) return;
    try {
      this.socket.close(1000, 'detached');
    } catch {
      /* already gone */
    }
    this.socket = null;
  }

  resize(cols: number, rows: number): void {
    if (cols > 0 && rows > 0) {
      this.cols = cols;
      this.rows = rows;
    }
    this.pty?.resize(this.cols, this.rows);
  }

  write(data: Buffer): void {
    this.pty?.write(data);
  }

  running(): boolean {
    return this.pty !== null;
  }

  attached(): boolean {
    return this.socket !== null;
  }

  current(): PaneSocket | null {
    return this.socket;
  }

  lastExit(): PaneExitInfo | null {
    return this.exitInfo;
  }

  stop(): void {
    if (this.stopped) return;
    this.stopped = true;
    this.onStopCb?.();
    if (this.pty) {
      try {
        this.pty.kill();
      } catch {
        /* already gone */
      }
      this.pty = null;
    }
    this.detachAll();
  }

  private ensureSpawned(): void {
    if (this.pty || this.stopped) return;
    const persisted = this.persistence.get();
    const resume = persisted !== '';
    const uuid = resume ? persisted : this.generateUuid();
    if (!resume) this.persistence.set(uuid);
    const args = claudePaneArgs(uuid, resume);
    const env = buildClaudePaneEnv(this.env);
    const pty = this.spawnFn({
      command: 'claude',
      args,
      cwd: this.cwd,
      env,
      cols: this.cols,
      rows: this.rows,
    });
    pty.onData((d) => {
      this.ring.push(d);
      this.safeSend(d);
    });
    pty.onExit((e) => this.handleExit(e));
    this.pty = pty;
  }

  private handleExit(e: { exitCode: number; signal?: number }): void {
    this.exitInfo = {
      code: e.exitCode,
      signal: e.signal ?? null,
      at: new Date().toISOString(),
    };
    this.pty = null;
    this.onExitCb?.(this.exitInfo);
    // End the attached pane's read loop; the next attach respawns with --resume
    // (the uuid is persisted now).
    if (this.socket) {
      try {
        this.socket.close(CLOSE_CHILD_EXITED, 'pane process exited');
      } catch {
        /* already gone */
      }
      this.socket = null;
    }
  }

  private safeSend(data: Buffer): void {
    if (!this.socket) return;
    try {
      this.socket.send(data);
    } catch {
      /* socket closed between our check and the send — drop it */
    }
  }
}

/** Construct a claude-pane supervisor. Exported so tests can build one with
 * injected seams; production uses startClaudePaneSupervisor. */
export function createClaudePaneSupervisor(deps: ClaudePaneDeps): ClaudePaneSupervisor {
  return new Supervisor(deps);
}

/**
 * Wire a claude-pane supervisor to the live session registry: it reads/persists
 * the pane uuid via the registry (session.json), resolves the workspace cwd from
 * PROJECT_PATH, and registers an external-activity probe so /idle treats an
 * attached pane as live (mirroring opencode.ts / codex.ts). The child is still
 * spawned lazily on the first attach.
 */
export function startClaudePaneSupervisor(
  cfg: RunnerConfig,
  spawn: PaneSpawner = defaultPaneSpawner,
  onExit?: (info: PaneExitInfo) => void,
): ClaudePaneSupervisor {
  const reg = getRegistry();
  const sup = createClaudePaneSupervisor({
    cwd: resolveWorkspaceDir(cfg.projectPath),
    persistence: {
      get: () => reg.getClaudePaneSession(),
      set: (uuid) => reg.setClaudePaneSession(uuid),
    },
    spawn,
    onExit: (info) => {
      // Pod-log visibility first, then the observer (which closes any open
      // synthetic turn as interrupted — Stop/SessionEnd hooks are graceful-only
      // and never fire on a crash).
      console.log(`claude-pane: interactive child exited (code=${info.code} signal=${info.signal})`);
      onExit?.(info);
    },
    onStop: () => setExternalActivityProbe(null),
  });
  // An attached pane has no runner turn and no SSE client, so — like opencode/
  // codex — the attached socket is the session's only liveness signal.
  setExternalActivityProbe(() => sup.attached());
  return sup;
}

// --- Default node-pty spawner ---------------------------------------------

const nodeRequire = createRequire(import.meta.url);

/** The subset of a node-pty child we drive. Declared locally (rather than
 * imported from node-pty) so `tsc --noEmit` never depends on node-pty's typings
 * and the module imports cleanly without the native addon. */
interface NodePtyChild {
  onData(cb: (data: string | Buffer) => void): void;
  onExit(cb: (e: { exitCode: number; signal?: number }) => void): void;
  write(data: string): void;
  resize(cols: number, rows: number): void;
  kill(signal?: string): void;
}
type NodePtyModule = {
  spawn: (file: string, args: readonly string[], opts: Record<string, unknown>) => NodePtyChild;
};

/**
 * Default PaneSpawner: spawn the interactive child via node-pty and adapt it to
 * PanePty. node-pty is required LAZILY here (not at module import) so tests and
 * typecheck never load the native addon. `encoding: null` makes node-pty emit
 * Buffers so PTY output stays binary-safe; client input (keystrokes) is UTF-8
 * text, which node-pty.write accepts as a string.
 */
export const defaultPaneSpawner: PaneSpawner = (opts) => {
  const pty = nodeRequire('node-pty') as NodePtyModule;
  const child = pty.spawn(opts.command, opts.args, {
    name: 'xterm-256color',
    cols: opts.cols,
    rows: opts.rows,
    cwd: opts.cwd,
    env: opts.env,
    encoding: null,
  });
  return {
    onData(cb) {
      child.onData((d) => cb(typeof d === 'string' ? Buffer.from(d, 'utf8') : d));
    },
    onExit(cb) {
      child.onExit((e) => cb(e));
    },
    write(data) {
      child.write(data.toString('utf8'));
    },
    resize(cols, rows) {
      try {
        child.resize(cols, rows);
      } catch {
        /* pty gone */
      }
    },
    kill(signal) {
      try {
        child.kill(signal);
      } catch {
        /* already gone */
      }
    },
  };
};

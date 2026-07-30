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
//   - Backpressure: a client that stops reading is evicted (close 4003) once its
//     send buffer crosses MAX_PANE_CLIENT_BUFFER_BYTES; it reattaches into the
//     scrollback ring. No PTY pause/resume — close-and-replay is the recovery.
//
// The node-pty dependency is required lazily (createRequire) inside the default
// spawner so importing this module — for unit tests or `tsc --noEmit` — never
// needs the compiled native addon (which is built in the runner image, not under
// `npm install --ignore-scripts`). Tests inject a fake spawner behind PaneSpawner.

import { createRequire } from 'node:module';
import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { resolveWorkspaceDir } from './exec.js';
import { getRegistry, setExternalActivityProbe, type RunnerConfig } from './session.js';
import { CLAUDE_CONFIG_DIR } from './types.js';
import { createLogger } from './log.js';

const log = createLogger('claude-pane');

/** Default scrollback retained and replayed to a (re)attaching pane, in bytes. */
export const SCROLLBACK_BYTES = 256 * 1024;

/** Default PTY geometry before the client sends its first resize control frame. */
const DEFAULT_COLS = 80;
const DEFAULT_ROWS = 24;

/** WebSocket close code used when a new pane attach preempts the current one. */
export const CLOSE_REPLACED = 4001;
/** WebSocket close code used when the interactive child process exits. */
export const CLOSE_CHILD_EXITED = 4002;
/** WebSocket close code used when the attached pane is evicted for backpressure
 * (its send buffer exceeded MAX_PANE_CLIENT_BUFFER_BYTES). */
export const CLOSE_BACKPRESSURE = 4003;

/** P2: cap on bytes the attached pane socket may have buffered (bufferedAmount)
 * before safeSend treats the client as wedged and closes it (CLOSE_BACKPRESSURE).
 * A client that stops reading without closing TCP — a suspended laptop, a wedged
 * port-forward — otherwise accumulates PTY output in runner RSS at the child's
 * output rate, unbounded, until the pod OOMs. This mirrors the SSE path's E3 cap
 * (events.ts MAX_SSE_CLIENT_BUFFER_BYTES, same 4 MiB): far above any healthy
 * client's transient backlog, so only a genuinely stuck reader trips it. No
 * PTY pause/resume flow control — recovery is close-and-reattach, replaying the
 * scrollback ring. */
export const MAX_PANE_CLIENT_BUFFER_BYTES = 4 * 1024 * 1024;

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
  /** Bytes queued but not yet flushed to the client (ws.bufferedAmount);
   * drives the P2 backpressure eviction in safeSend. */
  readonly bufferedAmount: number;
}

/** Recorded outcome of the interactive child's last exit. */
export interface PaneExitInfo {
  code: number | null;
  signal: number | null;
  /** RFC3339 instant the child exited. */
  at: string;
}

// --- Scrollback ring ------------------------------------------------------

/** Bytes that terminate a CSI sequence (final byte, 0x40-0x7E). */
const CSI_FINAL_MIN = 0x40;
const CSI_FINAL_MAX = 0x7e;
const ESC = 0x1b;
const BEL = 0x07;

/**
 * `]` (0x5D) is a syntactically legal CSI final byte that no real sequence uses,
 * and excluding it is what keeps bracketed COUNTS out of the trim. Requiring a
 * parameter byte (below) already protects `[warn]` and `[INFO]`, but not
 * `[12] server started`, `[100%] done`, `[1] 12345` or `[0]abc` — all of which
 * are `[`, parameter/intermediate bytes, then `]`, and all of which were losing
 * their bracketed prefix. Bracketed counters and percentages are far more common
 * in terminal output than a `]`-terminated CSI, which is to say: than nothing.
 */
const CSI_FINAL_EXCLUDED = 0x5d;

/**
 * How far into a snapshot to look for the end of a chopped escape sequence
 * ([X3]). Real sequences are a handful of bytes; an OSC carrying a window title
 * can be longer but not unboundedly so. If nothing terminates within this
 * window the buffer almost certainly does not START with a partial sequence at
 * all, so we return it untouched rather than eat real output.
 */
const MAX_PARTIAL_ESCAPE_SCAN = 256;

/**
 * [X3] Drop a leading PARTIAL escape sequence — the remnant left when the ring
 * trimmed the head mid-sequence, i.e. one whose introducer (ESC) was cut away.
 *
 * Deliberately conservative: it only acts on a buffer whose first byte is a
 * plausible mid-sequence remnant, only scans a bounded window, and returns the
 * input unchanged whenever it cannot identify a clean resumption point. Dropping
 * a few bytes of a broken sequence is safe; eating real output is not.
 *
 * Exported for unit tests.
 */
export function trimPartialEscape(buf: Buffer): Buffer {
  if (buf.length === 0) return buf;
  // A buffer starting with ESC is a WHOLE sequence — nothing was chopped.
  if (buf[0] === ESC) return buf;
  // '[' or ']' as the very first byte is the classic remnant: "ESC [" / "ESC ]"
  // split by the trim. Anything else is ordinary text (or a lone final byte we
  // cannot distinguish from real output, which we leave alone on purpose).
  const first = buf[0];
  if (first !== 0x5b /* [ */ && first !== 0x5d /* ] */) return buf;

  const limit = Math.min(buf.length, MAX_PARTIAL_ESCAPE_SCAN);
  if (first === 0x5b) {
    // CSI remnant. Note a bare "[w" is *syntactically* a valid CSI, so matching
    // "'[' then any final byte" would eat the first two characters of ordinary
    // output like "[warn] ..." or "[INFO] ..." — text a terminal shows all the
    // time. Require at least one PARAMETER byte (0x30-0x3F: digits, ';', '?')
    // before the final byte, which every colour/cursor sequence the pane
    // actually emits has ("[32m", "[1;31m", "[?25l"). The cost is not trimming a
    // parameterless remnant like "[m"; that renders as two stray characters,
    // which is strictly better than swallowing real text.
    let i = 1;
    while (i < limit && buf[i] >= 0x30 && buf[i] <= 0x3f) i++;
    if (i === 1) return buf; // no parameter bytes → treat as ordinary text
    while (i < limit && buf[i] >= 0x20 && buf[i] <= 0x2f) i++; // intermediates
    if (
      i < limit &&
      buf[i] >= CSI_FINAL_MIN &&
      buf[i] <= CSI_FINAL_MAX &&
      buf[i] !== CSI_FINAL_EXCLUDED
    ) {
      return buf.subarray(i + 1);
    }
    return buf;
  }
  // OSC remnant: "]<digits>;<text>" terminated by BEL or ST (ESC \). Requiring
  // the digits-then-';' preamble keeps ordinary text starting with ']' safe.
  let j = 1;
  while (j < limit && buf[j] >= 0x30 && buf[j] <= 0x39) j++;
  if (j === 1 || j >= limit || buf[j] !== 0x3b /* ; */) return buf;
  for (let i = j + 1; i < limit; i++) {
    if (buf[i] === BEL) return buf.subarray(i + 1);
    if (buf[i] === ESC && i + 1 < buf.length && buf[i + 1] === 0x5c /* \ */) {
      return buf.subarray(i + 2);
    }
  }
  return buf;
}

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

  /**
   * A single Buffer snapshot of the retained scrollback (oldest → newest), with
   * any partial escape sequence at the HEAD removed.
   *
   * [X3] The ring is a byte tail, not a screen model: `push` trims the head
   * chunk mid-byte-stream, so the first bytes of a snapshot can be the tail of a
   * chopped-in-half escape sequence (`[32m` with its ESC gone, an unterminated
   * OSC, …). Replayed as-is, the attaching terminal renders that remnant as
   * literal garbage on the first line. `forceRepaint()` bounds how long it
   * survives, but the cheap fix is not to send it: skip to the first byte that
   * can legally begin output.
   */
  snapshot(): Buffer {
    return trimPartialEscape(Buffer.concat(this.chunks, this.total));
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
    // Asserted unconditionally, NOT a passthrough: the k8s side sets IS_SANDBOX=1
    // pod-wide (buildEnv) because interactive `claude` refuses to start with
    // permissions.defaultMode=bypassPermissions as uid 0 unless IS_SANDBOX=1. The
    // pane's strict allowlist would otherwise drop it, wedging a bypassPermissions
    // pane at boot. The runner ONLY ever runs inside a network-isolated sandbox pod
    // (default-deny ingress + egress allowlist), so this is always true regardless
    // of how the runner was started — stamp it here rather than trust the inherited env.
    IS_SANDBOX: '1',
  };
  for (const k of PANE_ENV_PASSTHROUGH) {
    if (env[k] !== undefined) out[k] = env[k];
  }
  // Operator-injected env (CreateOptions.ExtraEnv / ExtraSecretEnv, part B) is
  // DELIBERATELY admitted through the otherwise-strict allowlist: making it
  // visible to the pane agent IS the feature (the agent's git/gh/glab use the
  // injected PAT). The two marker vars name exactly which vars to pass through, so
  // only operator-declared names cross the boundary — the runner's own secrets
  // (RUNNER_TOKEN, credentials) are never named and stay withheld. (opencode/codex
  // children receive these automatically via sanitizedExecEnv passthrough; the
  // pane is the only strict-allowlist child, so it needs this explicit admit.)
  for (const marker of ['SANDBOX_EXTRA_ENV_NAMES', 'SANDBOX_EXTRA_SECRET_ENV_NAMES']) {
    for (const raw of (env[marker] ?? '').split(',')) {
      const n = raw.trim();
      if (n && env[n] !== undefined) out[n] = env[n];
    }
  }
  return out;
}

// --- Spawn arg selection --------------------------------------------------

/**
 * The `claude` args for a pane spawn. The very first spawn ever for a session
 * (no persisted uuid yet) STARTS a session with `--session-id <uuid>`; every
 * later spawn RESUMES it with `--resume <uuid>`. When permissionMode is
 * non-empty it is appended as `--permission-mode <mode>` on EVERY spawn:
 * interactive claude applies settings.json's permissions.defaultMode only to
 * NEW sessions, so a `--resume` spawn would otherwise come back in default
 * (prompting) mode — silently downgrading a bypassPermissions ("yolo") session
 * on every suspend/resume and every child restart. Pure/exported for unit tests.
 */
export function claudePaneArgs(uuid: string, resume: boolean, permissionMode = ''): string[] {
  const args = resume ? ['--resume', uuid] : ['--session-id', uuid];
  if (permissionMode !== '') args.push('--permission-mode', permissionMode);
  return args;
}

/** The claude permission modes --permission-mode accepts; anything else found
 * in settings is ignored rather than passed through to the child's argv. */
const PANE_PERMISSION_MODES = new Set(['default', 'acceptEdits', 'plan', 'bypassPermissions']);

/**
 * The permissions.defaultMode from the pane's settings.json, or '' on a missing
 * file, missing key, unrecognized mode, or any throw. Read FRESH on each spawn
 * (not cached) so a settings edit inside the session applies to the next
 * respawn. Exported + injectable-read for unit tests.
 */
export function paneDefaultPermissionMode(
  env: NodeJS.ProcessEnv = process.env,
  read: (path: string) => string = (p) => readFileSync(p, 'utf8'),
): string {
  try {
    const raw = read(join(env.CLAUDE_CONFIG_DIR || CLAUDE_CONFIG_DIR, 'settings.json'));
    const doc = JSON.parse(raw) as { permissions?: { defaultMode?: unknown } };
    const mode = doc.permissions?.defaultMode;
    if (typeof mode === 'string' && PANE_PERMISSION_MODES.has(mode)) return mode;
    return '';
  } catch {
    return '';
  }
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
   * child on the first attach, replays scrollback, then forwards live output.
   * `cols`/`rows` are the attaching client's geometry, adopted before the lazy
   * spawn so the child is born at the right size; omit to keep the current. */
  attach(socket: PaneSocket, cols?: number, rows?: number): void;
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
  /** Reads the pane's settings.json for paneDefaultPermissionMode (test seam,
   * defaults to node:fs readFileSync). */
  readSettings?: (path: string) => string;
  /** Tapped for every PTY output chunk, attached client or not — the "child is
   * printing/working" liveness signal (drives the pane-output idle window). */
  onOutput?: () => void;
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
  private readonly readSettings: (path: string) => string;
  private readonly onOutputCb: (() => void) | undefined;
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
    this.readSettings = deps.readSettings ?? ((p) => readFileSync(p, 'utf8'));
    this.onOutputCb = deps.onOutput;
    this.onExitCb = deps.onExit;
    this.onStopCb = deps.onStop;
    this.ring = new ScrollbackRing(deps.scrollbackBytes ?? SCROLLBACK_BYTES);
  }

  attach(socket: PaneSocket, cols?: number, rows?: number): void {
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
    // Whether a child was ALREADY running before this attach. A fresh spawn (below)
    // paints itself, so only a reattach to a live child needs the forced repaint.
    const wasRunning = this.pty !== null;
    this.socket = socket;
    // [R4] Adopt the attaching client's geometry BEFORE the lazy spawn, so a
    // fresh child is born at the real terminal size instead of painting 80x24
    // and reflowing a round trip later. On a reattach the child already exists,
    // so the new geometry reaches the PTY through forceRepaint() below — whose
    // jiggle-and-restore lands on the restored (new) size.
    if (cols !== undefined && rows !== undefined && cols > 0 && rows > 0) {
      this.cols = cols;
      this.rows = rows;
    }
    this.ensureSpawned();
    // Replay accumulated scrollback so a (re)attaching pane catches up, as one
    // binary frame; live output follows via the onData forwarder. No data can
    // interleave here — attach() runs to completion synchronously.
    const snap = this.ring.snapshot();
    if (snap.length > 0) this.safeSend(snap);
    // The scrollback ring is a byte TAIL, not a screen model: its oldest
    // full-screen paint may have scrolled out, leaving only incremental frames,
    // so the input box + statusline reconstruct stale/garbled until the child
    // repaints. Force one for an already-running child (a live-hit; /status + esc
    // "fixed" it by forcing a repaint).
    if (wasRunning) this.forceRepaint();
  }

  /** Nudge a running fullscreen child into a full redraw by momentarily changing
   * the PTY row count and restoring it: the size change delivers SIGWINCH and
   * fullscreen claude redraws the whole screen at the restored geometry. A fresh
   * spawn needs none of this (it paints itself), and a same-size client resize
   * arriving right after attach is a no-op ioctl, so this is the only repaint
   * source on reattach. */
  private forceRepaint(): void {
    if (!this.pty) return;
    const jiggleRows = this.rows > 1 ? this.rows - 1 : this.rows + 1;
    this.pty.resize(this.cols, jiggleRows);
    this.pty.resize(this.cols, this.rows);
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
    const env = buildClaudePaneEnv(this.env);
    // Re-apply the settings default permission mode on EVERY spawn (fresh read):
    // --resume ignores settings.defaultMode, so without this a respawn silently
    // drops a bypassPermissions session back to prompting.
    const args = claudePaneArgs(uuid, resume, paneDefaultPermissionMode(env, this.readSettings));
    const pty = this.spawnFn({
      command: 'claude',
      args,
      cwd: this.cwd,
      env,
      cols: this.cols,
      rows: this.rows,
    });
    pty.onData((d) => {
      // Tap FIRST (before the ring push / socket send) so the "child is printing"
      // liveness signal fires even when no client is attached — a long tool call
      // emits no observer hooks for minutes, and this is what keeps the reaper
      // from suspending a working detached session mid-turn.
      this.onOutputCb?.();
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
    const socket = this.socket;
    if (!socket) return;
    // P2: evict a client that has buffered more than the cap (the WebSocket
    // analog of events.ts's E3 SSE eviction). Fire-and-forget sends to a reader
    // that stopped consuming otherwise grow the ws send buffer in pod memory at
    // PTY output rate; close it instead — the child keeps running, the ring
    // keeps accumulating, and a reattach replays the scrollback.
    if (socket.bufferedAmount > MAX_PANE_CLIENT_BUFFER_BYTES) {
      log.error('socket over the buffer cap; closing wedged pane (a reattach replays from the scrollback ring)', {
        bufferedBytes: socket.bufferedAmount,
        capBytes: MAX_PANE_CLIENT_BUFFER_BYTES,
      });
      this.socket = null;
      try {
        socket.close(CLOSE_BACKPRESSURE, 'pane client not reading (backpressure)');
      } catch {
        /* already gone */
      }
      return;
    }
    try {
      socket.send(data);
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
    // Every PTY output chunk is "child is working" evidence — feed the pane-output
    // idle window so a long, hook-silent tool call can't be mislabeled idle.
    onOutput: () => reg.notePaneOutput(),
    onExit: (info) => {
      // Pod-log visibility first, then the observer (which closes any open
      // synthetic turn as interrupted — Stop/SessionEnd hooks are graceful-only
      // and never fire on a crash).
      log.info('interactive child exited', { code: info.code, signal: info.signal });
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

// Per-pod session state: session.json persistence, turn registry, and the
// in-flight permission/abort bookkeeping that the HTTP layer drives.
//
// One sandbox = one runner pod = one Claude SDK session (spec 8.1). The
// session id, project path, and backend come from environment variables set
// by the pod spec; session.json is the durable on-disk state reloaded on
// resume.

import { mkdirSync, readFileSync, writeFileSync, existsSync, renameSync } from 'node:fs';
import { dirname } from 'node:path';
import type { AutopilotSpec, EventType, IdleStatus, SessionState, StatusResponse } from './types.js';
import { PROTOCOL_VERSION, SESSION_JSON_PATH } from './types.js';
import { appendEvent, sseClientCount, setClientsChangedHandler } from './events.js';

let externalActivityProbe: (() => boolean) | null = null;

/** Register a backend-specific current-activity probe. The opencode supervisor
 * uses this so /idle can sample live attach sockets synchronously instead of
 * relying only on its periodic activity poller. */
export function setExternalActivityProbe(fn: (() => boolean) | null): void {
  externalActivityProbe = fn;
}

/** Runner env configuration (set by the pod spec). */
export interface RunnerConfig {
  sessionId: string;
  backend: string;
  projectPath: string;
  runnerToken: string;
  /** Optional session-default model id/alias (SANDBOX_MODEL). Empty/undefined
   * => the account default. Per-turn TurnRequestBody.model overrides it. */
  model?: string;
}

export function loadConfig(): RunnerConfig {
  const sessionId = process.env.SANDBOX_SESSION_ID ?? 'claude-sdk-local';
  const backend = process.env.SANDBOX_BACKEND ?? 'claude-sdk';
  const projectPath = process.env.PROJECT_PATH ?? process.cwd();
  const runnerToken = process.env.RUNNER_TOKEN ?? '';
  const model = process.env.SANDBOX_MODEL ?? '';
  if (!runnerToken) {
    // Auth is still enforced (token === '' rejects all non-healthz), but warn.
    console.warn('RUNNER_TOKEN not set: all non-healthz requests will be rejected');
  }
  return { sessionId, backend, projectPath, runnerToken, ...(model ? { model } : {}) };
}

// --- session.json ---------------------------------------------------------

/** Current session.json shape version, stamped on every save. Additive fields
 * do NOT need a bump — the per-field defaulting in reviveSessionState is the
 * migration for those. Bump it (and add explicit handling in
 * reviveSessionState) when a field changes meaning or shape. A file stamped
 * newer than this loads best-effort with a loud warning: unknown fields are
 * preserved across the load/save round-trip, but known fields are interpreted
 * under this runner's (older) semantics. */
export const STATE_VERSION = 1;

function emptyState(cfg: RunnerConfig): SessionState {
  return {
    state_version: STATE_VERSION,
    sandbox_session_id: cfg.sessionId,
    backend: cfg.backend,
    project_path: cfg.projectPath,
    status: 'idle',
    claude_session_id: '',
    opencode_session_id: '',
    last_turn_id: '',
    last_activity: new Date().toISOString(),
  };
}

/** External backends (opencode) are considered "in use" if activity was seen
 * within this window; keeps the reaper from suspending an active opencode
 * session that has neither a runner turn nor an SSE client. */
const EXTERNAL_ACTIVE_WINDOW_MS = 90_000;

/** An observer-set synthetic 'busy' (an interactive opencode turn — no
 * registered runner turn) normally clears when the always-on observer sees
 * `session.idle`. But a wedged mapper or a missed `session.idle` would pin
 * status='busy' forever; since recomputeIdle()/idleStatus() treat a 'busy'
 * status as an active turn, that would block the idle reaper indefinitely. Bound
 * it: once the observer has been quiet for this long, a synthetic busy is
 * considered STALE and — when nothing is attached (isDetached) — the session
 * becomes idle-eligible again. A real runner turn (activeTurns > 0) is never
 * subject to this; it settles deterministically via finishTurn(). */
const SYNTHETIC_BUSY_STALE_MS = 5 * 60_000;

/** A freshly-started runner has no in-flight turns (activeTurns is rebuilt
 * empty), so a persisted 'busy' status — saved just before a crash/restart
 * mid-turn — is stale and would make /status report a turn that no longer
 * exists. Coerce it to 'idle' on load; 'idle' and 'error' are preserved (C3). */
export function reconcileLoadedStatus(status: SessionState['status'] | undefined): SessionState['status'] {
  return status === 'busy' ? 'idle' : (status ?? 'idle');
}

/** A normalized event the boot sequence appends before the boot `session.started`
 * (see orphanedTurnBootEvents / index.ts). Mirrors appendEvent's parameters. */
export interface BootEvent {
  turnId?: string;
  type: EventType;
  payload: Record<string, unknown>;
}

/**
 * Events to append on boot when a persisted 'busy' session status is coerced to
 * 'idle' — i.e. the pod died mid-turn (hard SIGKILL/OOM, or even a best-effort
 * SIGTERM that never flipped the status back). The SQLite event log still ends
 * with the orphaned turn's events (turn.started, tool.started, deltas, …) and no
 * terminal event, so any client attaching with after=0 (or resuming from its
 * last seq) replays a stream that drives its read-model to "busy" with nothing
 * to flip it back — the TUI shows the session working forever (D2).
 *
 * We append, matching the live emitters' payload shapes exactly (server.ts's
 * turn.interrupted `{ reason }`, setStatus's session.status_changed `{ status }`):
 *   1. `turn.interrupted { reason }` for the orphaned turn, recovered from
 *      `state.last_turn_id`. server.ts persists that via setLastTurn() *before*
 *      registerTurn() flips the status to 'busy', so a persisted 'busy' always
 *      carries the running turn's id there — it is the cheapest recovery and the
 *      exact signal activeTurnId() already trusts while busy (no event-log scan
 *      needed). Skipped when last_turn_id is empty (corrupt/partial state) so we
 *      never emit an interrupt for a garbage id.
 *   2. `session.status_changed { status: 'idle' }` so the read-model flips back
 *      to needs-input (readmodel.go's idle case).
 *
 * Returns [] unless the persisted status was 'busy'; idle/error/undefined boots
 * append nothing extra.
 */
export function orphanedTurnBootEvents(
  persistedStatus: SessionState['status'] | undefined,
  state: SessionState,
): BootEvent[] {
  if (persistedStatus !== 'busy') return [];
  const events: BootEvent[] = [];
  const turnId = state.last_turn_id;
  if (turnId) {
    events.push({ turnId, type: 'turn.interrupted', payload: { reason: 'runner restart' } });
  }
  events.push({ type: 'session.status_changed', payload: { status: 'idle' } });
  return events;
}

/**
 * Normalize a parsed session.json into live state. Exported for tests (the
 * production path constant points at /session).
 *
 * Version handling mirrors the event log's read-compare-migrate: a file with
 * state_version <= STATE_VERSION migrates via the per-field defaulting below;
 * a NEWER file (written by a newer runner — `:latest` skew) loads best-effort
 * with a loud warning rather than refusing, because every field here degrades
 * safely (worst case a stale resume id, which the turn paths already fail-soft
 * on). Unknown fields are spread through untouched so they survive this
 * runner's saves and are intact when a matching-version runner returns.
 */
export function reviveSessionState(parsed: Partial<SessionState>, cfg: RunnerConfig): SessionState {
  const onDisk = parsed.state_version ?? 1;
  if (onDisk > STATE_VERSION) {
    console.error(
      `session.json state_version ${onDisk} is newer than this runner supports (${STATE_VERSION}); ` +
        'loading best-effort (unknown fields preserved). Use a runner image at least as new as the one that last wrote this session.',
    );
  }
  return {
    ...parsed,
    state_version: STATE_VERSION,
    sandbox_session_id: parsed.sandbox_session_id ?? cfg.sessionId,
    backend: parsed.backend ?? cfg.backend,
    project_path: parsed.project_path ?? cfg.projectPath,
    status: reconcileLoadedStatus(parsed.status),
    claude_session_id: parsed.claude_session_id ?? '',
    opencode_session_id: parsed.opencode_session_id ?? '',
    last_turn_id: parsed.last_turn_id ?? '',
    last_activity: parsed.last_activity ?? new Date().toISOString(),
    ...(parsed.model ? { model: parsed.model } : {}),
    ...(parsed.title_generated ? { title_generated: true } : {}),
  };
}

/** The result of loading session.json on boot: the live state plus any events
 * the boot sequence must append before `session.started` (see BootEvent /
 * orphanedTurnBootEvents). bootEvents is empty except after a mid-turn crash. */
export interface LoadedSession {
  state: SessionState;
  bootEvents: BootEvent[];
}

/**
 * B7: read + parse session.json at `path`, surviving a corrupt/truncated file.
 * Returns the parsed object, or null when the file is absent OR unparseable.
 *
 * A truncated or garbage file (partial write killed by an OOM, a bad disk) would
 * otherwise throw at boot from JSON.parse; index.ts has no catch, so the pod
 * restarts, reads the SAME corrupt file, and crash-loops forever. Instead we move
 * the bad file aside (session.json.corrupt-<ts> — Date.now() is fine in the
 * runner) and return null so the caller falls through to fresh emptyState seeding
 * and the pod comes up. The moved-aside copy is preserved for post-mortem. Loud
 * console.error so the incident is visible in pod logs, not silently swallowed.
 * Exported for unit tests (production callers use SESSION_JSON_PATH).
 */
export function readSessionFile(path: string): Partial<SessionState> | null {
  if (!existsSync(path)) return null;
  const raw = readFileSync(path, 'utf8');
  try {
    return JSON.parse(raw) as Partial<SessionState>;
  } catch (err) {
    const aside = `${path}.corrupt-${Date.now()}`;
    try {
      renameSync(path, aside);
    } catch {
      /* best-effort: if we can't move it aside, still fall through to reseed */
    }
    console.error(
      `session.json at ${path} is corrupt (${err instanceof Error ? err.message : String(err)}); ` +
        `moved aside to ${aside} and reseeding a fresh empty session so the pod can boot`,
    );
    return null;
  }
}

/** Load session.json, or seed it from env if absent OR corrupt (B7). Also returns
 * the boot events needed to terminate an orphaned turn when a persisted 'busy'
 * status is coerced to 'idle' (D2); the caller must append them before
 * `session.started`. A corrupt file (moved aside) yields no bootEvents — its
 * pre-crash state is unrecoverable, so we can't know a turn was orphaned. `path`
 * is injectable for tests; production uses SESSION_JSON_PATH. */
export function loadSessionState(cfg: RunnerConfig, path: string = SESSION_JSON_PATH): LoadedSession {
  const parsed = readSessionFile(path);
  if (parsed) {
    const loaded = reviveSessionState(parsed, cfg);
    // Persist corrections (busy→idle, version restamp) so disk matches reality.
    if (parsed.status !== loaded.status || parsed.state_version !== loaded.state_version) {
      saveSessionStateTo(loaded, path);
    }
    return { state: loaded, bootEvents: orphanedTurnBootEvents(parsed.status, loaded) };
  }
  // Absent or corrupt (moved aside): seed a fresh empty session; no boot events.
  const state = emptyState(cfg);
  saveSessionStateTo(state, path);
  return { state, bootEvents: [] };
}

/** Persist session.json atomically (write+rename) to `path`. Always stamps the
 * current STATE_VERSION: the file's contents conform to this runner's shape as of
 * this write (plus any preserved unknown fields, best-effort). */
function saveSessionStateTo(state: SessionState, path: string): void {
  mkdirSync(dirname(path), { recursive: true });
  const tmp = `${path}.tmp`;
  writeFileSync(tmp, JSON.stringify({ ...state, state_version: STATE_VERSION }, null, 2) + '\n', 'utf8');
  // Rename is atomic on POSIX.
  renameSync(tmp, path);
}

/** Destination for saveSessionState(). Defaults to the production path; a test
 * can point it at a writable temp file so routes that persist state (registerTurn
 * → setStatus, setLastTurn) run for real off-pod, where /session is read-only.
 * Mirrors events.ts's __setEventLogForTest seam; production never touches it. */
let sessionJsonPath = SESSION_JSON_PATH;

/** Test-only: redirect saveSessionState() to `path` (or null to restore the
 * production SESSION_JSON_PATH). Not part of the runner API. */
export function __setSessionJsonPathForTest(path: string | null): void {
  sessionJsonPath = path ?? SESSION_JSON_PATH;
}

/** Persist session.json atomically to the configured path (SESSION_JSON_PATH,
 * unless a test redirected it via __setSessionJsonPathForTest). */
export function saveSessionState(state: SessionState): void {
  saveSessionStateTo(state, sessionJsonPath);
}

/** True when `backend` has a runner-side autopilot driver (the server-side
 * /loop-/goal loop). Claude-backend-first: only the Claude SDK backend runs the
 * driver in-runner today (it lives in the SDK turn loop). opencode-server is
 * driven through an external pane and supervise-only has no turn path, so both
 * report false and the TUI keeps its local tea.Tick driver. This is the single
 * source for the /status capability bit and the PUT/DELETE endpoint's gate. */
export function backendHasAutopilot(backend: string): boolean {
  return backend === 'claude-sdk';
}

export function toStatusResponse(state: SessionState, activeTurnId = ''): StatusResponse {
  return {
    id: state.sandbox_session_id,
    backend: state.backend,
    projectPath: state.project_path,
    activity: state.status,
    agentSession: state.claude_session_id,
    lastTurnId: state.last_turn_id,
    activeTurnId,
    lastActivity: state.last_activity,
    ...(state.model ? { model: state.model } : {}),
    protocolVersion: PROTOCOL_VERSION,
    capabilities: { autopilot: backendHasAutopilot(state.backend) },
  };
}

// --- Turn + permission registry -------------------------------------------

/** A pending permission request awaiting an HTTP POST resolution. */
export interface PendingPermission {
  permissionId: string;
  tool: string;
  input: Record<string, unknown>;
  // B8: returns true when THIS call won the first-write-wins race (the canUseTool
  // closure was still pending and is now resolved), false when the closure had
  // already settled (auto-denied by deadline / abort / detach) so this resolution
  // was a no-op. The server maps false → 409 rather than lying resolved:true. The
  // `void` arm keeps pre-existing test doubles that return nothing compiling; the
  // real callback (claude.ts makeCanUseTool) always returns a boolean.
  resolve: (allow: boolean, scope: string, editedInput?: string) => boolean | void;
}

/** In-flight turn bookkeeping. */
export interface ActiveTurn {
  turnId: string;
  abort: AbortController;
  prompt: string;
}

class SessionRegistry {
  state: SessionState;
  readonly activeTurns = new Map<string, ActiveTurn>();
  readonly pendingPermissions = new Map<string, PendingPermission>();

  // RFC3339 instant the session last became idle (turn-done AND no attached
  // clients), or null when active. The reaper reads this via /idle; keeping the
  // clock here (not in the reaper) makes the reaper stateless across restarts.
  private idleSince: string | null = null;

  // Epoch ms of the last externally-observed activity (opencode client traffic),
  // or 0 if never. An opencode session has no runner turn and no SSE client, so
  // without this signal the reaper would read it as permanently idle. The
  // opencode supervisor calls setExternalActivity() while the client is live.
  private externalActivityAt = 0;

  // Epoch ms of the last event the always-on opencode observer mapped for our
  // session, or 0 if the observer has never fired. Distinguishes a live
  // synthetic 'busy' (fresh observer events) from a wedged one (status pinned
  // 'busy' but the stream went quiet). See SYNTHETIC_BUSY_STALE_MS.
  private lastObserverEventAt = 0;

  constructor(state: SessionState) {
    this.state = state;
  }

  setStatus(status: SessionState['status']): void {
    if (this.state.status === status) {
      this.recomputeIdle();
      return;
    }
    this.state.status = status;
    this.state.last_activity = new Date().toISOString();
    saveSessionState(this.state);
    appendEvent(this.state.sandbox_session_id, undefined, 'session.status_changed', {
      status,
    });
    this.recomputeIdle();
  }

  /**
   * Recompute idleSince from the current turn + attached-client state. Idle =
   * no active turn, no (fresh) synthetic backend turn, AND no attached SSE
   * clients. A synthetic 'busy' normally blocks idle, but a STALE one does not
   * (syntheticBusyStale) so a wedged mapper can't pin the pod unreapable. Sets
   * idleSince on the transition into idle, clears it on any activity. Safe to
   * call often.
   */
  /** Q1: an armed autopilot driver marks the session non-idle (keyed off the
   * explicit lifecycle field from §1/H3, never off `kind`) — the same way an
   * in-flight turn does — so the reaper can't suspend a session mid-loop between
   * iterations. When the driver transitions to 'stopped' (any reason) the session
   * becomes idle-eligible again. Reads the PVC-persisted state, so it is correct
   * across a runner restart: the boot re-arm keeps a genuinely-armed spec
   * non-idle, while a spec that already reached 'stopped' stays idle-eligible.
   * The staleness bound (armed but wedged for 30m) is enforced by the driver,
   * which flips state to 'stopped'(lapsed) and thereby releases this hold. */
  autopilotArmed(): boolean {
    return this.state.autopilot?.state === 'armed';
  }

  recomputeIdle(): void {
    const busyBlocksIdle = this.state.status === 'busy' && !this.syntheticBusyStale();
    const idle =
      this.activeTurns.size === 0 && !busyBlocksIdle && !this.autopilotArmed() && this.isDetached();
    if (idle && this.idleSince === null) {
      // Only reachable with status still 'busy' via the stale-synthetic-busy
      // release (a normal turn end flips status to 'idle' first); surface it.
      if (this.state.status === 'busy') {
        console.warn(
          `session ${this.state.sandbox_session_id}: synthetic 'busy' went stale ` +
            `(no opencode observer events for >=${SYNTHETIC_BUSY_STALE_MS}ms) with nothing ` +
            'attached; releasing to the idle reaper',
        );
      }
      this.idleSince = new Date().toISOString();
    } else if (!idle) {
      this.idleSince = null;
    }
  }

  /**
   * True when the session is in an observer-driven synthetic 'busy' that has
   * gone stale — no observer event for SYNTHETIC_BUSY_STALE_MS. Only a synthetic
   * busy (no registered runner turn) can be stale; a real /turns turn keeps
   * activeTurns.size > 0 and settles via finishTurn(). A synthetic busy that has
   * never recorded an observer event (lastObserverEventAt === 0) is treated as
   * fresh, not stale — the observer stamps the clock as it opens a cycle, so
   * this only shields the direct status-mutation path used by tests.
   */
  private syntheticBusyStale(): boolean {
    if (this.activeTurns.size > 0 || this.lastObserverEventAt === 0) return false;
    return Date.now() - this.lastObserverEventAt >= SYNTHETIC_BUSY_STALE_MS;
  }

  /**
   * True when no SSE client is attached and there is no recent external
   * (opencode) activity — i.e. the session is detached. Mirrors the "attached"
   * notion used by recomputeIdle so an abandoned pending permission can be
   * auto-denied and the pod reaped (NEW-7): otherwise a turn blocked on an
   * unanswered permission keeps activeTurns > 0 forever and idleSince is never
   * set, so the reaper can never suspend.
   */
  isDetached(): boolean {
    const externalActive =
      this.externalActivityAt > 0 && Date.now() - this.externalActivityAt < EXTERNAL_ACTIVE_WINDOW_MS;
    return sseClientCount() === 0 && !externalActive;
  }

  /** Record externally-observed activity (opencode client traffic) so the idle
   * clock treats the session as in use. Called by the opencode supervisor. */
  setExternalActivity(): void {
    this.externalActivityAt = Date.now();
    this.recomputeIdle();
  }

  /** Record that the always-on opencode observer just mapped an event for our
   * session, refreshing the synthetic-busy staleness clock so a live interactive
   * turn stays "active" while a wedged/quiet stream eventually releases the pod
   * to the reaper. `atMs` is injectable so tests can backdate the clock without
   * waiting out the staleness window. */
  noteObserverEvent(atMs: number = Date.now()): void {
    this.lastObserverEventAt = atMs;
    this.recomputeIdle();
  }

  /** Persist the active model id reported by the backend (Seam C). */
  setModel(model: string): void {
    if (!model || this.state.model === model) return;
    this.state.model = model;
    saveSessionState(this.state);
  }

  /** Persist the autopilot spec wholesale (state.autopilot) and recompute idle —
   * an armed spec marks the session non-idle (Q1), a stopped one releases it.
   * The driver (autopilot.ts) owns the spec's field mutations and calls this
   * after every transition; passing `undefined` is disallowed (the spec is never
   * deleted — H3), so callers always hand back a spec. */
  setAutopilot(spec: AutopilotSpec): void {
    this.state.autopilot = spec;
    this.state.last_activity = new Date().toISOString();
    saveSessionState(this.state);
    this.recomputeIdle();
  }

  /** The persisted autopilot spec, or undefined when the driver has never been
   * armed for this session. */
  getAutopilot(): AutopilotSpec | undefined {
    return this.state.autopilot;
  }

  /** Persist the one-shot auto-title guard (title_generated = true) (T6). */
  setTitleGenerated(): void {
    if (this.state.title_generated === true) return;
    this.state.title_generated = true;
    saveSessionState(this.state);
  }

  idleStatus(): IdleStatus {
    try {
      if (externalActivityProbe?.()) this.setExternalActivity();
    } catch {
      /* best-effort; stale cached activity still applies */
    }
    this.recomputeIdle();
    return {
      // A synthetic 'busy' counts as an active turn only while fresh; a stale
      // one (wedged mapper) reports inactive so it can't block the reaper. An
      // armed autopilot driver (Q1) also reports active so the reaper leaves the
      // pod alone between iterations.
      turnActive:
        this.activeTurns.size > 0 ||
        (this.state.status === 'busy' && !this.syntheticBusyStale()) ||
        this.autopilotArmed(),
      attachedClients: sseClientCount(),
      ...(this.idleSince ? { idleSince: this.idleSince } : {}),
    };
  }

  setClaudeSession(claudeSessionId: string): void {
    this.state.claude_session_id = claudeSessionId;
    this.state.last_activity = new Date().toISOString();
    saveSessionState(this.state);
  }

  /**
   * Drop the persisted Claude session id (the resumable head). Called by the
   * fail-soft path in runTurn when a resume id turns out stale ("No conversation
   * found") so the retry — and every later turn — starts fresh instead of
   * repeatedly hard-failing on the orphaned id. No-op when already empty.
   */
  clearClaudeSession(): void {
    if (!this.state.claude_session_id) return;
    this.state.claude_session_id = '';
    this.state.last_activity = new Date().toISOString();
    saveSessionState(this.state);
  }

  /**
   * Persist the opencode server session id (the opencode analogue of
   * setClaudeSession). The opencode turn adapter calls this once it has resolved
   * (created or resumed) a session so subsequent turns — and turns after a pod
   * restart — continue the same conversation instead of starting fresh. No-op
   * when unchanged to avoid a redundant session.json write each turn.
   */
  setOpencodeSession(opencodeSessionId: string): void {
    if (!opencodeSessionId || this.state.opencode_session_id === opencodeSessionId) return;
    this.state.opencode_session_id = opencodeSessionId;
    this.state.last_activity = new Date().toISOString();
    saveSessionState(this.state);
  }

  /**
   * Drop the persisted opencode session id (the opencode analogue of
   * clearClaudeSession). Called by the opencode adapter when a prompt fails with
   * a missing-session error (the server lost the session, e.g. an independent
   * `opencode serve` restart) so the next turn recreates it instead of failing
   * forever. No-op when already empty.
   */
  clearOpencodeSession(): void {
    if (!this.state.opencode_session_id) return;
    this.state.opencode_session_id = '';
    this.state.last_activity = new Date().toISOString();
    saveSessionState(this.state);
  }

  /**
   * The id of the turn currently running, or '' when idle. Registered runner
   * turns win; for interactive opencode turns (which never register — they run
   * inside `opencode serve` and are only mirrored by the observer, via
   * setStatus('busy') + setLastTurn) fall back to last_turn_id while busy.
   * last_turn_id alone is NOT this signal: it persists after a turn finishes
   * to seed nextTurnId.
   */
  activeTurnId(): string {
    const first = this.activeTurns.keys().next();
    if (!first.done) return first.value;
    if (this.state.status === 'busy') return this.state.last_turn_id;
    return '';
  }

  setLastTurn(turnId: string): void {
    this.state.last_turn_id = turnId;
    this.state.last_activity = new Date().toISOString();
    saveSessionState(this.state);
  }

  nextTurnId(): string {
    // Sequential, human-readable turn ids: turn-1, turn-2, ...
    const last = this.state.last_turn_id;
    let n = 0;
    if (last) {
      const m = /^turn-(\d+)$/.exec(last);
      if (m) n = parseInt(m[1], 10);
    }
    return `turn-${n + 1}`;
  }

  registerTurn(turnId: string, prompt: string): ActiveTurn {
    const abort = new AbortController();
    const turn: ActiveTurn = { turnId, abort, prompt };
    this.activeTurns.set(turnId, turn);
    this.setStatus('busy');
    return turn;
  }

  finishTurn(turnId: string): void {
    this.activeTurns.delete(turnId);
    if (this.activeTurns.size === 0) this.setStatus('idle');
  }

  registerPermission(p: PendingPermission): void {
    this.pendingPermissions.set(p.permissionId, p);
  }

  resolvePermission(
    permissionId: string,
  ): PendingPermission | undefined {
    return this.pendingPermissions.get(permissionId);
  }

  /** Delete a pending permission entry. Called after resolving (R2). */
  deletePermission(permissionId: string): void {
    this.pendingPermissions.delete(permissionId);
  }
}

let registry: SessionRegistry | null = null;

export function initRegistry(state: SessionState): SessionRegistry {
  registry = new SessionRegistry(state);
  // Recompute idleSince whenever a client attaches/detaches so "detached"
  // transitions are reflected immediately for the reaper.
  setClientsChangedHandler(() => registry?.recomputeIdle());
  return registry;
}

export function getRegistry(): SessionRegistry {
  if (!registry) throw new Error('session registry not initialized');
  return registry;
}

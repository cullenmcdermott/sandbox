// Per-pod session state: session.json persistence, turn registry, and the
// in-flight permission/abort bookkeeping that the HTTP layer drives.
//
// One sandbox = one runner pod = one Claude SDK session (spec 8.1). The
// session id, project path, and backend come from environment variables set
// by the pod spec; session.json is the durable on-disk state reloaded on
// resume.

import { mkdirSync, readFileSync, writeFileSync, existsSync, renameSync } from 'node:fs';
import { dirname } from 'node:path';
import type { IdleStatus, SessionState, StatusResponse } from './types.js';
import { SESSION_JSON_PATH } from './types.js';
import { appendEvent, sseClientCount, setClientsChangedHandler } from './events.js';

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

function emptyState(cfg: RunnerConfig): SessionState {
  return {
    sandbox_session_id: cfg.sessionId,
    backend: cfg.backend,
    project_path: cfg.projectPath,
    status: 'idle',
    claude_session_id: '',
    last_turn_id: '',
    last_activity: new Date().toISOString(),
  };
}

/** External backends (opencode) are considered "in use" if activity was seen
 * within this window; keeps the reaper from suspending an active opencode
 * session that has neither a runner turn nor an SSE client. */
const EXTERNAL_ACTIVE_WINDOW_MS = 90_000;

/** A freshly-started runner has no in-flight turns (activeTurns is rebuilt
 * empty), so a persisted 'busy' status — saved just before a crash/restart
 * mid-turn — is stale and would make /status report a turn that no longer
 * exists. Coerce it to 'idle' on load; 'idle' and 'error' are preserved (C3). */
export function reconcileLoadedStatus(status: SessionState['status'] | undefined): SessionState['status'] {
  return status === 'busy' ? 'idle' : (status ?? 'idle');
}

/** Load session.json, or seed it from env if absent. */
export function loadSessionState(cfg: RunnerConfig): SessionState {
  if (existsSync(SESSION_JSON_PATH)) {
    const raw = readFileSync(SESSION_JSON_PATH, 'utf8');
    const parsed = JSON.parse(raw) as Partial<SessionState>;
    const loaded: SessionState = {
      sandbox_session_id: parsed.sandbox_session_id ?? cfg.sessionId,
      backend: parsed.backend ?? cfg.backend,
      project_path: parsed.project_path ?? cfg.projectPath,
      status: reconcileLoadedStatus(parsed.status),
      claude_session_id: parsed.claude_session_id ?? '',
      last_turn_id: parsed.last_turn_id ?? '',
      last_activity: parsed.last_activity ?? new Date().toISOString(),
      ...(parsed.model ? { model: parsed.model } : {}),
      ...(parsed.title_generated ? { title_generated: true } : {}),
    };
    // Persist the correction so disk matches the live (idle) reality.
    if (parsed.status !== loaded.status) {
      saveSessionState(loaded);
    }
    return loaded;
  }
  const state = emptyState(cfg);
  saveSessionState(state);
  return state;
}

/** Persist session.json atomically (write+rename). */
export function saveSessionState(state: SessionState): void {
  mkdirSync(dirname(SESSION_JSON_PATH), { recursive: true });
  const tmp = `${SESSION_JSON_PATH}.tmp`;
  writeFileSync(tmp, JSON.stringify(state, null, 2) + '\n', 'utf8');
  // Rename is atomic on POSIX.
  renameSync(tmp, SESSION_JSON_PATH);
}

export function toStatusResponse(state: SessionState): StatusResponse {
  return {
    id: state.sandbox_session_id,
    backend: state.backend,
    projectPath: state.project_path,
    status: state.status,
    claudeSession: state.claude_session_id,
    lastTurnId: state.last_turn_id,
    lastActivity: state.last_activity,
    ...(state.model ? { model: state.model } : {}),
  };
}

// --- Turn + permission registry -------------------------------------------

/** A pending permission request awaiting an HTTP POST resolution. */
export interface PendingPermission {
  permissionId: string;
  tool: string;
  input: Record<string, unknown>;
  resolve: (allow: boolean, scope: string, editedInput?: string) => void;
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
   * no active turn AND no attached SSE clients. Sets idleSince on the
   * transition into idle, clears it on any activity. Safe to call often.
   */
  recomputeIdle(): void {
    const idle = this.activeTurns.size === 0 && this.isDetached();
    if (idle && this.idleSince === null) {
      this.idleSince = new Date().toISOString();
    } else if (!idle) {
      this.idleSince = null;
    }
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

  /** Persist the active model id reported by the backend (Seam C). */
  setModel(model: string): void {
    if (!model || this.state.model === model) return;
    this.state.model = model;
    saveSessionState(this.state);
  }

  /** Persist the one-shot auto-title guard (title_generated = true) (T6). */
  setTitleGenerated(): void {
    if (this.state.title_generated === true) return;
    this.state.title_generated = true;
    saveSessionState(this.state);
  }

  idleStatus(): IdleStatus {
    this.recomputeIdle();
    return {
      turnActive: this.activeTurns.size > 0,
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

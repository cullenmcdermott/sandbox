// Per-pod session state: session.json persistence, turn registry, and the
// in-flight permission/abort bookkeeping that the HTTP layer drives.
//
// One sandbox = one runner pod = one Claude SDK session (spec 8.1). The
// session id, project path, and backend come from environment variables set
// by the pod spec; session.json is the durable on-disk state reloaded on
// resume.

import { mkdirSync, readFileSync, writeFileSync, existsSync, renameSync } from 'node:fs';
import { dirname } from 'node:path';
import type { SessionState, StatusResponse } from './types.js';
import { SESSION_JSON_PATH } from './types.js';
import { appendEvent } from './events.js';

/** Runner env configuration (set by the pod spec). */
export interface RunnerConfig {
  sessionId: string;
  backend: string;
  projectPath: string;
  runnerToken: string;
}

export function loadConfig(): RunnerConfig {
  const sessionId = process.env.SANDBOX_SESSION_ID ?? 'claude-sdk-local';
  const backend = process.env.SANDBOX_BACKEND ?? 'claude-sdk';
  const projectPath = process.env.PROJECT_PATH ?? process.cwd();
  const runnerToken = process.env.RUNNER_TOKEN ?? '';
  if (!runnerToken) {
    // Auth is still enforced (token === '' rejects all non-healthz), but warn.
    console.warn('RUNNER_TOKEN not set: all non-healthz requests will be rejected');
  }
  return { sessionId, backend, projectPath, runnerToken };
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

/** Load session.json, or seed it from env if absent. */
export function loadSessionState(cfg: RunnerConfig): SessionState {
  if (existsSync(SESSION_JSON_PATH)) {
    const raw = readFileSync(SESSION_JSON_PATH, 'utf8');
    const parsed = JSON.parse(raw) as Partial<SessionState>;
    return {
      sandbox_session_id: parsed.sandbox_session_id ?? cfg.sessionId,
      backend: parsed.backend ?? cfg.backend,
      project_path: parsed.project_path ?? cfg.projectPath,
      status: parsed.status ?? 'idle',
      claude_session_id: parsed.claude_session_id ?? '',
      last_turn_id: parsed.last_turn_id ?? '',
      last_activity: parsed.last_activity ?? new Date().toISOString(),
    };
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

  constructor(state: SessionState) {
    this.state = state;
  }

  setStatus(status: SessionState['status']): void {
    if (this.state.status === status) return;
    this.state.status = status;
    this.state.last_activity = new Date().toISOString();
    saveSessionState(this.state);
    appendEvent(this.state.sandbox_session_id, undefined, 'session.status_changed', {
      status,
    });
  }

  setClaudeSession(claudeSessionId: string): void {
    this.state.claude_session_id = claudeSessionId;
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
}

let registry: SessionRegistry | null = null;

export function initRegistry(state: SessionState): SessionRegistry {
  registry = new SessionRegistry(state);
  return registry;
}

export function getRegistry(): SessionRegistry {
  if (!registry) throw new Error('session registry not initialized');
  return registry;
}

// Normalized event and session model for the runner. Mirrors
// ./internal/session/event.go and the runner API contract
// (./docs/runner-api.md). The runner maps Claude Agent SDK
// messages into these types before persisting to events.db and streaming
// to SSE clients.

/** Canonical event type enum (see internal/session/event.go). */
export type EventType =
  | 'session.started'
  | 'session.status_changed'
  | 'turn.started'
  | 'turn.completed'
  | 'turn.failed'
  | 'turn.interrupted'
  | 'message.started'
  | 'message.delta'
  | 'message.completed'
  | 'reasoning.started'
  | 'reasoning.delta'
  | 'reasoning.completed'
  | 'tool.started'
  | 'tool.delta'
  | 'tool.completed'
  | 'tool.failed'
  | 'permission.requested'
  | 'permission.resolved'
  | 'todo.updated'
  | 'diff.updated'
  | 'usage.updated'
  | 'sync.status_changed'
  | 'error';

/** A single normalized event in the session event log. */
export interface Event {
  seq: number;
  /** RFC3339 timestamp. */
  time: string;
  sessionId: string;
  turnId?: string;
  type: EventType;
  /** Type-specific JSON payload. */
  payload: Record<string, unknown>;
}

// --- Payload shapes (mirror event.go) -------------------------------------

export interface MessagePayload {
  role: 'user' | 'assistant';
  content: string;
  /** true for message.delta events. */
  delta?: boolean;
}

export interface ToolPayload {
  tool: string;
  input?: unknown;
  output?: string;
  /** Bash exit code, when available. */
  exitCode?: number;
  error?: string;
}

export interface PermissionPayload {
  permissionId: string;
  tool: string;
  input: unknown;
  /** "allow-once" | "allow-session" | "deny" | "" (pending). */
  decision?: string;
}

export interface UsagePayload {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalCostUsd: number;
}

export interface ErrorPayload {
  message: string;
  code?: string;
}

export interface SessionStatusPayload {
  status: 'idle' | 'busy' | 'error';
}

// --- Session state (session.json, snake_case per spec 8.3) ----------------

export interface SessionState {
  sandbox_session_id: string;
  backend: string;
  claude_session_id: string;
  project_path: string;
  status: 'idle' | 'busy' | 'error';
  last_turn_id: string;
  /** RFC3339. */
  last_activity: string;
}

// --- HTTP request/response bodies (camelCase per runner-api.md) -----------

export interface TurnRequestBody {
  prompt: string;
  resume?: string;
  allowedTools?: string[];
}

export interface TurnResponse {
  turnId: string;
}

export interface PermissionRequestBody {
  session: string;
  permission: string;
  allow: boolean;
  scope: 'once' | 'session';
  editedInput?: string;
}

/** /sessions/:id/status response shape (runner-api.md). */
export interface StatusResponse {
  id: string;
  backend: string;
  projectPath: string;
  status: string;
  claudeSession: string;
  lastTurnId: string;
  lastActivity: string;
}

/** Audit row appended to audit.jsonl (spec 8.5). */
export interface AuditRow {
  time: string;
  session_id: string;
  turn_id: string;
  tool: string;
  input: unknown;
  exit_code?: number;
}

// --- Paths ----------------------------------------------------------------

export const STATE_DIR = '/session/state/sandbox';
export const EVENTS_DB_PATH = `${STATE_DIR}/events.db`;
export const SESSION_JSON_PATH = `${STATE_DIR}/session.json`;
export const AUDIT_JSONL_PATH = `${STATE_DIR}/audit.jsonl`;
export const CLAUDE_CONFIG_DIR = '/session/state/claude';
export const WORKSPACE_ROOT = '/session/workspace';

export const PORT = 8787;

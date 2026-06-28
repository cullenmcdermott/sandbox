// Normalized event and session model for the runner. Mirrors
// ./internal/session/event.go and the runner API contract
// (./docs/runner-api.md). The runner maps Claude Agent SDK
// messages into these types before persisting to events.db and streaming
// to SSE clients.

// The EventType union, ALL_EVENT_TYPES, and the event payload interfaces are
// generated from schema/events.json by cmd/gen-eventschema (run `just gen`;
// never hand-edit *.gen.ts). They are re-exported here so existing imports
// (`import { MessagePayload } from './types'`) keep working.
import type { EventType } from './events.gen.js';
export type {
  EventType,
  SessionStartedPayload,
  SessionStatusPayload,
  TerminatingPayload,
  MessagePayload,
  ToolPayload,
  PermissionPayload,
  UsagePayload,
  RateLimitPayload,
  WorkspaceStatusPayload,
  SessionTitlePayload,
  TodoItem,
  TodoUpdatedPayload,
  ErrorPayload,
} from './events.gen.js';
export { ALL_EVENT_TYPES } from './events.gen.js';

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

// Event payload shapes (MessagePayload, ToolPayload, etc.) are generated from
// schema/events.json into ./events.gen.ts and re-exported above.

/** GET /sessions/:id/idle response (consumed by the reaper). */
export interface IdleStatus {
  turnActive: boolean;
  attachedClients: number;
  /** RFC3339 instant the session last became idle, or omitted if active. */
  idleSince?: string;
}

// --- Session state (session.json, snake_case per spec 8.3) ----------------

export interface SessionState {
  sandbox_session_id: string;
  backend: string;
  claude_session_id: string;
  /** Persisted opencode server session id (the opencode analogue of
   * claude_session_id). Lets opencode turns continue the same conversation
   * across pod restarts instead of re-creating a fresh session each boot.
   * Empty until the first opencode turn creates one. */
  opencode_session_id: string;
  project_path: string;
  status: 'idle' | 'busy' | 'error';
  last_turn_id: string;
  /** RFC3339. */
  last_activity: string;
  /** Active model id reported by the backend (e.g. "opus-4.8"). Optional. */
  model?: string;
  /** True once the one-shot auto title has been generated for this session (T6). */
  title_generated?: boolean;
}

// --- HTTP request/response bodies (camelCase per runner-api.md) -----------

/** POST /sessions/:id/turns request body (runner-api.md). All fields but
 * `prompt` are optional; the server reads them via readBody<TurnRequestBody>. */
export interface TurnRequestBody {
  prompt?: string;
  resume?: string;
  allowedTools?: string[];
  /**
   * SDK permission mode for this turn: 'default' | 'acceptEdits' | 'plan' |
   * 'bypassPermissions'. Omitted/empty => the runner uses 'acceptEdits'.
   */
  mode?: string;
  /**
   * Model id/alias for this turn (the in-session /model switch), e.g. 'opus',
   * 'sonnet', 'haiku', or a full id. Omitted/empty => the runner falls back to
   * its session default (SANDBOX_MODEL) and then the account default.
   */
  model?: string;
  /**
   * Reasoning-effort level for this turn (the in-session /effort switch): one of
   * 'low' | 'medium' | 'high' | 'xhigh' | 'max'. Omitted/empty (or any unknown
   * value) => the runner leaves options.effort unset (SDK adaptive-thinking
   * default). Supported on Fable 5 / Opus 4.6+ / Sonnet 4.6 only; silently
   * ignored on other models. The wire value is the real SDK enum — the TUI
   * displays 'max' as "ultracode".
   */
  effort?: string;
}

/** POST /sessions/:id/turns response: the assigned turn id. */
export interface TurnResponse {
  turnId: string;
}

/** POST /sessions/:id/exec request: one-shot shell command. */
export interface ExecRequestBody {
  command?: string;
}

/** POST /sessions/:id/exec response: bounded captured output + exit code.
 * Matches exec.ts's ExecResult, which runExec returns and the route emits. */
export interface ExecResponse {
  stdout: string;
  stderr: string;
  exitCode: number;
}

/** POST /sessions/:id/permissions/:permission_id request body. The session and
 * permission ids come from the URL path, so the body is just the resolution:
 * allow, an optional scope ('once' default | 'session'), and an optional edited
 * tool input (JSON string). Read via readBody<PermissionRequestBody>. */
export interface PermissionRequestBody {
  allow?: boolean;
  scope?: 'once' | 'session';
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
  model?: string;
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
// Physical workspace root on the session PVC. The project subtree lives here
// (workspace/<host project path>) and is bind-mounted into the pod at the real
// host path so the SDK runs with a host-matching cwd (see resolveWorkspaceDir and
// k8s runnerVolumeMounts). This is the PVC-internal location, NOT the SDK cwd.
export const WORKSPACE_ROOT = '/session/workspace';

export const PORT = 8787;

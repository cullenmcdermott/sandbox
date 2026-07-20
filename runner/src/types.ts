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
  TurnStartedPayload,
  TurnCompletedPayload,
  TurnFailedPayload,
  TurnInterruptedPayload,
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
  Citation,
} from './events.gen.js';
export { ALL_EVENT_TYPES, PROTOCOL_VERSION } from './events.gen.js';

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
  /** session.json shape version (see STATE_VERSION in session.ts). Absent in
   * files written before versioning, which are treated as version 1. */
  state_version?: number;
  sandbox_session_id: string;
  backend: string;
  claude_session_id: string;
  /** Persisted opencode server session id (the opencode analogue of
   * claude_session_id). Lets opencode turns continue the same conversation
   * across pod restarts instead of re-creating a fresh session each boot.
   * Empty until the first opencode turn creates one. */
  opencode_session_id: string;
  /** Persisted UUID for the interactive `claude-pane` backend (claude-pane.ts).
   * Generated ONCE, on the first pane spawn ever (passed as `--session-id`), and
   * reused as `--resume` on every later spawn so the interactive conversation
   * continues across child exits and pod restarts. Absent/empty until the first
   * pane attach; unused by every other backend. Additive-optional field (no
   * STATE_VERSION bump). */
  claude_pane_session_id?: string;
  project_path: string;
  status: 'idle' | 'busy' | 'error';
  last_turn_id: string;
  /** RFC3339. */
  last_activity: string;
  /** Active model id reported by the backend (e.g. "opus-4.8"). Optional. */
  model?: string;
  /** True once the one-shot auto title has been generated for this session (T6). */
  title_generated?: boolean;
  /** Persisted autopilot driver spec (the server-side /loop-/goal loop; see
   * docs/archive/server-side-loop-adr.md). Absent until the driver is first armed via
   * PUT /sessions/:id/autopilot; retained with state:'stopped' after it
   * terminates (never deleted — H3) so attach + reaper idle logic read a stable
   * terminal record instead of inferring stop from a missing key. */
  autopilot?: AutopilotSpec;
}

/** Why an armed autopilot driver terminated (AutopilotSpec.stopped_reason and
 * the autopilot.state stopped event's `reason`). */
export type AutopilotStopReason = 'sentinel' | 'budget' | 'user' | 'lapsed' | 'error';

/** The autopilot driver spec, persisted per session in session.json (ADR §1).
 * `state` is the explicit lifecycle field (H3): the spec always carries a
 * non-null `kind` once armed, and `state` alone distinguishes a live driver
 * (`armed`) from a terminated one (`stopped`, with `stopped_reason`). Every
 * downstream rule (idle computation Q1, boot re-arm H1) keys off
 * `state === 'armed'`, never off `kind`. Arming overwrites the spec wholesale
 * and bumps `gen`; disarm sets state:'stopped' (reason 'user') and bumps `gen`. */
export interface AutopilotSpec {
  /** The driver flavour (never null once armed). */
  kind: 'loop' | 'goal';
  /** Explicit lifecycle field: 'armed' (live) or 'stopped' (terminated). */
  state: 'armed' | 'stopped';
  /** Set when state === 'stopped'; null while armed. */
  stopped_reason: AutopilotStopReason | null;
  /** The /loop or /goal prompt submitted each iteration. */
  prompt: string;
  /** Completion marker scanned in the just-completed assistant text; '' disables
   * sentinel termination (the loop then runs to max_iterations/token_budget). */
  sentinel: string;
  /** Delay between iterations in ms (0 = immediate). */
  interval_ms: number;
  /** Per-turn overrides applied to every self-submitted turn. */
  overrides: { model?: string; effort?: string; mode?: string };
  /** Hard iteration ceiling (always enforced; default 50). */
  max_iterations: number;
  /** Optional hard token ceiling (input+output summed across the loop); null =
   * no token cap. */
  token_budget: number | null;
  /** Completed-iteration counter. */
  iterations: number;
  /** RFC3339 instant the driver was armed (boot re-arm anchor when no turn has
   * completed yet — H1). */
  armed_at: string;
  /** RFC3339 of the last completed self-submitted (or manual) turn, or null.
   * The boot re-arm interval anchor (H1) and the staleness anchor (Q1). */
  last_completed_at: string | null;
  /** Monotonic generation; a disarm/rearm bumps it, dropping stale scheduled
   * ticks (the gen guard). */
  gen: number;
}

/** PUT /sessions/:id/autopilot request body (arm/replace the driver). All fields
 * but `kind` and `prompt` are optional; the runner fills defaults (sentinel '',
 * intervalMs 0, maxIterations 50, tokenBudget null, overrides {}). Validated at
 * the route boundary (B9 typed errors). */
export interface AutopilotRequestBody {
  kind?: string;
  prompt?: string;
  sentinel?: string;
  intervalMs?: number;
  overrides?: { model?: string; effort?: string; mode?: string };
  maxIterations?: number;
  tokenBudget?: number | null;
}

// --- HTTP request/response bodies (camelCase per runner-api.md) -----------

/** POST /sessions/:id/turns request body (runner-api.md). All fields but
 * `prompt` are optional; the server reads them via readBody<TurnRequestBody>. */
export interface TurnRequestBody {
  prompt?: string;
  /**
   * The AGENT session id to continue (Go: TurnInput.Resume). Despite the Go type
   * being `session.TurnID`, the runner treats this as the backend's own session
   * identifier — the Claude SDK session UUID (claude.ts effectiveResume) or the
   * opencode session id — NOT a turn id. An SDK consumer must pass the agent
   * session id here, not a TurnID. (D10: the Go-side field is still typed
   * session.TurnID; retyping it to a plain agent-session-id string is deferred —
   * the §8 De-Claude break renamed State.ClaudeSession → State.AgentSessionID but
   * left TurnInput.Resume's type unchanged.)
   */
  resume?: string;
  allowedTools?: string[];
  /**
   * Tool-approval policy for this turn (Go: TurnInput.ApprovalPolicy, an owned
   * enum): 'default' | 'acceptEdits' | 'plan' | 'bypassPermissions'. Omitted,
   * empty, or unrecognized => the runner defaults to 'bypassPermissions' (§2d
   * yolo default — the sandbox pod is the isolation boundary). The runner maps
   * this per-backend: the claude-sdk backend applies it 1:1 as the SDK
   * permissionMode (claude.ts resolvePermissionMode); the opencode-server backend
   * does NOT honor it — its interactive client owns its own permission modal — so
   * the field is ignored there rather than silently dropped (see agent.ts).
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
  /**
   * Request the SDK "advisor" tool for this turn (the in-session /advisor toggle;
   * Go: TurnInput.Advisor). Mirrors the Go type so the wire contract is complete
   * (D10). A harmless no-op on the pinned @anthropic-ai/claude-agent-sdk, which
   * exposes no advisor option — the runner does not yet read it.
   */
  advisor?: boolean;
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
  /** Runner-reported turn activity: 'idle' | 'busy' | 'error'. Distinct from the
   * k8s lifecycle status (CREATING/RUNNING/…), which the runner does not report —
   * the Go side keeps them on separate fields (State.Activity vs State.Status, D9).
   * Renamed from `status` in the §8 De-Claude break. */
  activity: string;
  /** The backend's own resume id — the Claude SDK session UUID (claude-sdk) or the
   * opencode session id. One backend per session ⇒ one resume id. Renamed from
   * `claudeSession` in the §8 De-Claude break (Go: State.AgentSessionID). */
  agentSession: string;
  lastTurnId: string;
  /** The currently running turn id, or '' when idle. Unlike lastTurnId (which
   * persists after a turn finishes to seed nextTurnId), this is live registry
   * state — the signal for "is there a turn to interrupt". */
  activeTurnId: string;
  lastActivity: string;
  model?: string;
  /** The runner's PROTOCOL_VERSION (see events.gen.ts), so a status poll also
   * surfaces CLI/runner skew, not just /healthz. */
  protocolVersion: number;
  /** Backend capability bits the CLI reads to pick a code path. `autopilot` is
   * true when this backend has a runner-side autopilot driver (the server-side
   * /loop-/goal loop): the TUI then arms that driver via PUT/DELETE
   * /sessions/:id/autopilot and renders from autopilot.state events instead of
   * running its local tea.Tick loop (ADR §Q3 precedence). False for backends
   * without a runner driver (opencode/supervise-only), where the TUI keeps its
   * local driver. */
  capabilities: { autopilot: boolean };
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

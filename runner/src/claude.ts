// Claude Agent SDK integration: builds SDK options, installs hooks and the
// permission callback, runs query(), and maps SDKMessages into normalized
// events persisted via the event log.
//
// SDK option shapes follow @anthropic-ai/claude-agent-sdk (see
// /tmp/sdk-extract/package/sdk.d.ts). Key types:
//   query({prompt, options}): Query  // AsyncGenerator<SDKMessage, void>
//   Options: { cwd, permissionMode, allowedTools, disallowedTools, env,
//              settingSources, hooks, canUseTool, resume, abortController,
//              includePartialMessages, ... }
//   SDKMessage union: assistant | user | result | system | stream_event | ...
//   SDKPartialAssistantMessage: { type:'stream_event', event: BetaRawMessageStreamEvent }
//   BetaRawMessageStreamEvent: content_block_start | content_block_delta | ...
//
// Events are persisted (appendEvent) BEFORE being streamed, so SSE replay is
// consistent with the live tail.

import { query, type EffortLevel, type Options, type PermissionMode, type Query, type SDKMessage } from '@anthropic-ai/claude-agent-sdk';
import type { BetaContentBlock, BetaTextBlock } from '@anthropic-ai/sdk/resources/beta/messages/messages';
import type { PermissionResult, HookCallback, HookInput, SyncHookJSONOutput } from '@anthropic-ai/claude-agent-sdk';
import { mkdirSync } from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

// B6: emitWorkspaceStatus previously ran git via execFileSync — up to three
// synchronous subprocess calls (branch/dirty/ahead-behind) on the event loop,
// worst case ~9s (3× the 3s timeout) of blocked I/O per turn on a slow/huge repo,
// stalling every SSE stream and health check. Run git async instead.
const execFileAsync = promisify(execFile);
import { appendEvent, shortId } from './events.js';
import { appendAudit } from './audit.js';
import { bashCommandBlocked } from './guards.js';
import { resolveWorkspaceDir, sanitizedExecEnv } from './exec.js';
import { getRegistry, loadConfig } from './session.js';
import type { RunnerConfig } from './session.js';
import type { Agent } from './agent.js';
import { CLAUDE_CONFIG_DIR } from './types.js';
import { TITLE_PROMPT, sanitizeTitle, shouldGenerateTitle } from './title.js';
import { SessionGrants, resolutionOutcome } from './grants.js';
import { mapMessage as mapMessagePure, type StreamToolIndex } from './mapping.js';
import { startTurnTrace } from './trace.js';

// Tool-name-level "allow for this session" grants (permission scope:'session').
// One session per pod, so a single module-level store is the whole session's
// grant set; it is not persisted across pod restarts (a restart re-prompts).
const sessionGrants = new SessionGrants();

// --- Tool policy (spec 8.4) ----------------------------------------------

const DEFAULT_ALLOWED_TOOLS = [
  'Read',
  'Write',
  'Edit',
  'Glob',
  'Grep',
  'Bash',
  'WebFetch',
  'WebSearch',
  'AskUserQuestion',
];

const DEFAULT_DISALLOWED_TOOLS = [
  'Bash(kubectl *)',
  'Bash(talosctl *)',
  'Bash(helm *)',
  'Bash(argocd *)',
  'Bash(op *)',
  'Bash(sudo *)',
];

// BLOCKED_BASH_PATTERNS / bashCommandBlocked moved to ./guards.js so the same
// blocklist gates both the SDK Bash tool (below) and the /exec passthrough (O2).

// --- Child-process env hardening (A1) -------------------------------------

// Provider credentials the spawned `claude` binary authenticates with.
// sanitizedExecEnv strips these (it is the /exec denylist, which must hide
// provider keys from a `!cmd` passthrough), but the claude child MUST keep them
// or every turn fails to authenticate. Containment of their *exfil* is a
// network-layer concern (A3), not something we solve by withholding them here.
// RUNNER_TOKEN is deliberately NOT in this set: the claude runtime never needs
// it, and dropping it is the A1 fix — a prompt-injected agent that could read
// $RUNNER_TOKEN would POST to its own session's /permissions/:id endpoint and
// self-approve the permission it was just asked about.
// SCOPE (adversarial review 2026-07-08): this closes the trivial env-read
// vector only. The runner and the agent child share uid 0, so the token is
// still recoverable via /proc/<runner-pid>/environ; truly closing self-approval
// needs uid separation (or hidepid) at the pod level — tracked in TODO §1f.
const CLAUDE_PROVIDER_ENV_KEYS = ['ANTHROPIC_API_KEY', 'CLAUDE_CODE_OAUTH_TOKEN'] as const;

/**
 * Build the env for a spawned `claude` child (buildOptions + the title
 * summarizer). Starts from sanitizedExecEnv — which drops RUNNER_TOKEN and the
 * other runner-infra secrets (A1) — then restores the provider credentials the
 * claude binary needs, and finally applies the caller's explicit overrides
 * (CLAUDE_CONFIG_DIR, IS_SANDBOX, …). Pure and exported so the A1 stripping is
 * unit-testable without invoking the SDK.
 */
export function buildAgentEnv(
  overrides: NodeJS.ProcessEnv,
  env: NodeJS.ProcessEnv = process.env,
): NodeJS.ProcessEnv {
  const out = sanitizedExecEnv(env);
  for (const k of CLAUDE_PROVIDER_ENV_KEYS) {
    if (env[k] !== undefined) out[k] = env[k];
  }
  return { ...out, ...overrides };
}

// --- SDK options ----------------------------------------------------------

/**
 * Resolve a client-supplied permission-mode string to a valid SDK
 * PermissionMode. An empty/unknown value defaults to 'acceptEdits' so the
 * pre-mode-switching behavior is preserved.
 */
export function resolvePermissionMode(mode: string | undefined): PermissionMode {
  switch (mode) {
    case 'default':
    case 'acceptEdits':
    case 'plan':
    case 'bypassPermissions':
      return mode;
    default:
      return 'acceptEdits';
  }
}

/**
 * Resolve the model for a turn: a per-turn override (the in-session /model
 * switch, TurnRequestBody.model) wins over the session default (SANDBOX_MODEL /
 * cfg.model). An empty result means "unset" so the SDK uses the account
 * default. Returns undefined (not '') so callers can leave Options.model unset.
 */
export function resolveModel(
  turnModel: string | undefined,
  sessionModel: string | undefined,
): string | undefined {
  return turnModel || sessionModel || undefined;
}

// The SDK EffortLevel enum (sdk.d.ts EffortLevel). "max" is the genuine top
// tier; the TUI surfaces it under the label "ultracode".
const VALID_EFFORTS: ReadonlySet<string> = new Set(['low', 'medium', 'high', 'xhigh', 'max']);

/**
 * Resolve a per-turn effort string (the in-session /effort switch,
 * TurnRequestBody.effort) to a typed SDK EffortLevel, with a session default
 * fallback that mirrors resolveModel's precedence. Returns undefined — so the
 * caller leaves Options.effort unset (SDK adaptive thinking) — for an empty
 * value OR any string outside the enum. Narrowing here (not a raw cast) is what
 * keeps an invalid value from ever reaching the typed Options.effort and is
 * required for `tsc --noEmit` to accept the assignment.
 */
export function resolveEffort(
  turnEffort: string | undefined,
  sessionEffort?: string | undefined,
): EffortLevel | undefined {
  const v = turnEffort || sessionEffort;
  return v && VALID_EFFORTS.has(v) ? (v as EffortLevel) : undefined;
}

/**
 * Resolve the Claude SDK session id to resume for a turn (Workstream B). A
 * client-supplied resume id wins; otherwise default to the persisted session
 * head (reg.state.claude_session_id) so every turn after the first continues
 * the same conversation. Returns undefined when neither is set — the first turn
 * (persisted id still '') leaves Options.resume unset so the SDK starts a fresh
 * session, whose id mapMessage then captures. This is the core of the
 * continuity fix: without it every turn was a brand-new, history-less query().
 */
export function effectiveResume(
  clientResume: string | undefined,
  persistedId: string | undefined,
): string | undefined {
  return clientResume || persistedId || undefined;
}

/**
 * True when a query() failure means the resume id is stale/unknown — the SDK
 * throws e.g. "No conversation found with session ID: <id>" (matched
 * empirically against SDK 0.3.181 in the B spike). Drives the fail-soft retry
 * in runTurn: a stale id must NOT hard-fail the turn (a host-path migration or
 * transcript GC can orphan an id); we retry once without resume so the user's
 * prompt still runs. Conservative — only this resume-specific phrasing triggers
 * a retry, so an unrelated failure still surfaces as today.
 */
export function isStaleResumeError(message: string): boolean {
  return /no conversation found/i.test(message);
}

/**
 * True for the terminal SDK `result` message that reports a stale/unknown resume
 * id. The SDK yields this is_error result and THEN throws (confirmed against
 * sdk 0.3.181), so runTurn must skip mapping it when a fail-soft retry is still
 * available — otherwise mapMessage emits a spurious turn.failed+error into the
 * stream ahead of the successful retry.
 */
export function isStaleResultMessage(msg: SDKMessage): boolean {
  if (msg.type !== 'result' || msg.subtype === 'success') return false;
  const errors = (msg as { errors?: string[] }).errors ?? [];
  return isStaleResumeError(errors.join('; '));
}

/**
 * Whether a query() failure should trigger the fail-soft retry: a resume id was
 * in play, we have not retried yet, and the message is the stale-resume
 * signature. Pure so the at-most-once + used-resume guards are unit-testable
 * (runTurn itself binds the sqlite event log and isn't).
 */
export function shouldRetryStaleResume(
  usedResume: string | undefined,
  alreadyRetried: boolean,
  message: string,
): boolean {
  return !!usedResume && !alreadyRetried && isStaleResumeError(message);
}

/**
 * Whether to (re)persist the Claude session id observed on an init message.
 * Capture-LATEST: follow the live resumable head rather than pinning to turn-1's
 * id, so a chain of resumes always threads the current session. The spike shows
 * the id is stable across a plain resume (forkSession off), so this normally
 * no-ops after turn 1; the `!== current` guard avoids a redundant session.json
 * write each turn, and it self-heals if a future SDK forks the id on resume.
 */
export function shouldCaptureClaudeSession(
  current: string | undefined,
  observed: string | undefined,
): boolean {
  return !!observed && observed !== current;
}

/** Build the SDK Options for a turn (spec 8.4). */
export function buildOptions(
  cfg: RunnerConfig,
  turnId: string,
  resume: string | undefined,
  allowedToolsOverride: string[] | undefined,
  mode: string | undefined,
  model: string | undefined,
  effort: string | undefined,
  abort: AbortController,
): Options {
  const reg = getRegistry();
  const sessionId = reg.state.sandbox_session_id;
  const cwd = resolveWorkspaceDir(cfg.projectPath);

  // The PVC mounts over /session and shadows the workspace dir baked into the
  // image, and Mutagen may not have synced (or created) the project path yet.
  // The SDK spawns the `claude` binary with this cwd; a missing dir makes the
  // spawn fail with a misleading "binary failed to launch / libc" error. Ensure
  // it exists, mirroring how events/session/audit create their state dirs.
  mkdirSync(cwd, { recursive: true });

  const permissionMode = resolvePermissionMode(mode);
  const options: Options = {
    cwd,
    permissionMode,
    // bypassPermissions is a hard SDK safety gate: it is ignored unless this
    // flag is also set. Only enable it for that mode.
    allowDangerouslySkipPermissions: permissionMode === 'bypassPermissions',
    allowedTools: allowedToolsOverride ?? DEFAULT_ALLOWED_TOOLS,
    disallowedTools: DEFAULT_DISALLOWED_TOOLS,
    // buildAgentEnv strips RUNNER_TOKEN (and the other runner-infra secrets)
    // before the spawn so a prompt-injected agent's Bash tool can't read the
    // bearer token and self-approve its own permissions (A1); it retains the
    // provider creds the claude binary needs.
    env: buildAgentEnv({
      CLAUDE_CONFIG_DIR,
      CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1',
      // The spawned `claude` binary refuses --dangerously-skip-permissions
      // (bypassPermissions mode) when running as root unless IS_SANDBOX=1. The
      // pod sets this too (k8s buildEnv), but set it on the spawn env directly so
      // bypass also works for local/non-k8s dev where the pod env is absent.
      IS_SANDBOX: process.env.IS_SANDBOX ?? '1',
    }),
    settingSources: [],
    abortController: abort,
    includePartialMessages: true,
    hooks: {
      PreToolUse: [
        {
          matcher: 'Bash',
          hooks: [makePreToolUseBashHook(sessionId, turnId)],
        },
      ],
      PostToolUse: [
        {
          matcher: 'Edit|Write|Bash',
          hooks: [makePostToolUseAuditHook(sessionId, turnId)],
        },
      ],
      SessionEnd: [
        {
          hooks: [makeSessionEndHook(sessionId, turnId)],
        },
      ],
    },
  };

  // canUseTool drives the interactive permission prompt. In bypassPermissions
  // (yolo) mode the SDK never issues permission requests, so wiring canUseTool is
  // contradictory and pointless — omit it (mirrors the title summarizer, which
  // also runs bypass without canUseTool).
  if (permissionMode !== 'bypassPermissions') {
    options.canUseTool = makeCanUseTool(sessionId, turnId, abort.signal);
  }

  // Model selection: a per-turn override (the in-session /model switch) wins
  // over the session default (SANDBOX_MODEL / cfg.model); an empty value leaves
  // options.model unset so the SDK uses the account default. The SDK maps this
  // to `--model <id>` on the spawned claude binary, which resolves aliases like
  // "opus"/"sonnet"/"haiku" to the latest model the account can use.
  const resolvedModel = resolveModel(model, cfg.model);
  if (resolvedModel) options.model = resolvedModel;

  // Effort selection: a per-turn override (the in-session /effort switch) sets
  // the SDK reasoning-effort level; an empty/unknown value leaves options.effort
  // unset so the SDK uses its adaptive-thinking default. resolveEffort narrows
  // to the typed EffortLevel (a raw string fails tsc). The SDK silently drops or
  // downgrades the level on models without that effort tier.
  const resolvedEffort = resolveEffort(effort);
  if (resolvedEffort) options.effort = resolvedEffort;

  // Resume defaulting (Workstream B): a client-supplied resume id wins;
  // otherwise default to the persisted Claude session head so every turn after
  // the first continues the same conversation (fixes #2 model-switch and #3
  // mid-convo drop — same root cause). First turn: persisted id is '' → omit →
  // fresh session, then captured by mapMessage. runTurn clears the persisted id
  // and retries without resume if it turns out stale (isStaleResumeError).
  const resumeId = effectiveResume(resume, reg.state.claude_session_id);
  if (resumeId) options.resume = resumeId;

  return options;
}

// --- Hooks ---------------------------------------------------------------

// makePreToolUseBashHook builds the SDK PreToolUse(Bash) hook — the primary
// enforcement path for the Bash blocklist (the SDK Bash tool is what agents use
// constantly; the /exec passthrough and opencode plugin gate the other two
// surfaces). A blocked command returns the SDK's `decision:'block'` shape (which
// stops the tool call) and records a tool.failed event; anything else permits.
// The `emit` seam defaults to the live event log but is injectable so the guard
// can be unit-tested without an open SQLite database (F2).
export function makePreToolUseBashHook(
  sessionId: string,
  turnId: string,
  emit: (type: 'tool.failed', payload: Record<string, unknown>) => void =
    (type, payload) => appendEvent(sessionId, turnId, type, payload),
): HookCallback {
  return async (input: HookInput): Promise<SyncHookJSONOutput> => {
    if (input.hook_event_name !== 'PreToolUse') return { continue: true };
    const command = String((input.tool_input as { command?: unknown })?.command ?? '');
    if (bashCommandBlocked(command)) {
      emit('tool.failed', {
        tool: 'Bash',
        input: input.tool_input,
        error: `blocked by PreToolUse hook: command matches a host/cluster/credential pattern`,
      });
      return {
        decision: 'block',
        reason: `Command blocked by sandbox PreToolUse(Bash) hook: matches a host/cluster/credential operation pattern. Use an approved profile to allow this command.`,
        continue: false,
      };
    }
    return { continue: true };
  };
}
function makePostToolUseAuditHook(
  sessionId: string,
  turnId: string,
): HookCallback {
  return async (input: HookInput): Promise<SyncHookJSONOutput> => {
    if (input.hook_event_name !== 'PostToolUse') return { continue: true };
    const toolInput = input.tool_input as Record<string, unknown> | undefined;
    let exitCode: number | undefined;
    if (input.tool_name === 'Bash') {
      // Bash tool_response is { stdout, stderr, exitCode, interrupted }.
      const resp = input.tool_response as { exitCode?: number; interrupted?: boolean } | undefined;
      exitCode = resp?.exitCode;
    }
    appendAudit({
      time: new Date().toISOString(),
      session_id: sessionId,
      turn_id: turnId,
      tool: input.tool_name,
      input: toolInput ?? input.tool_input,
      ...(exitCode !== undefined ? { exit_code: exitCode } : {}),
    });
    return { continue: true };
  };
}
function makeSessionEndHook(
  sessionId: string,
  turnId: string,
): HookCallback {
  return async (input: HookInput): Promise<SyncHookJSONOutput> => {
    if (input.hook_event_name !== 'SessionEnd') return { continue: true };
    appendEvent(sessionId, turnId, 'session.status_changed', {
      status: 'idle',
      reason: input.reason,
    });
    return { continue: true };
  };
}

// --- Permission callback --------------------------------------------------

// PERMISSION_ABANDON_MS bounds how long a pending permission may keep the turn
// (and thus the pod) alive once the client has detached (NEW-7). While a client
// is attached the abandon check reschedules; the absolute deadline below is what
// bounds an attached-but-unanswered prompt.
const PERMISSION_ABANDON_MS = 120_000;

// PERMISSION_MAX_WAIT_MS is the absolute deadline after which a still-pending
// permission auto-denies even while a client is attached (C8: "auto-deny after a
// timeout"). This guarantees an unanswered prompt can never hold the turn — and
// the pod — open indefinitely. Configurable via the env var of the same name.
const PERMISSION_MAX_WAIT_MS = ((): number => {
  const v = parseInt(process.env.PERMISSION_MAX_WAIT_MS ?? '', 10);
  return Number.isFinite(v) && v > 0 ? v : 600_000;
})();

// parseEditedInput safely parses a permission's edited tool input. A malformed
// edit returns undefined so the caller falls back to the original input — it
// must NEVER throw, because a throw inside the resolve callback would leave
// canUseTool unresolved and hang the turn (and the pod) forever (C8).
export function parseEditedInput(editedInput: string | undefined): Record<string, unknown> | undefined {
  if (!editedInput) return undefined;
  try {
    return JSON.parse(editedInput) as Record<string, unknown>;
  } catch {
    return undefined;
  }
}

function makeCanUseTool(
  sessionId: string,
  turnId: string,
  abortSignal: AbortSignal,
): (toolName: string, input: Record<string, unknown>) => Promise<PermissionResult> {
  return (toolName, input) => {
    const reg = getRegistry();
    const permissionId = shortId('perm');
    appendEvent(sessionId, turnId, 'permission.requested', {
      permissionId,
      tool: toolName,
      input,
      decision: '',
    });
    // Session-scope grant: a prior permission resolved with scope:'session' for
    // this tool name, so auto-allow WITHOUT prompting. Still emit a resolved
    // event (decision 'allow-session') so the transcript/audit shows the
    // auto-allow rather than a silent gap.
    if (sessionGrants.isGranted(toolName)) {
      appendEvent(sessionId, turnId, 'permission.resolved', {
        permissionId,
        tool: toolName,
        input,
        decision: 'allow-session',
      });
      return Promise.resolve<PermissionResult>({ behavior: 'allow' });
    }
    return new Promise<PermissionResult>((resolve) => {
      let settled = false;
      let abandonTimer: ReturnType<typeof setTimeout> | undefined;
      let deadlineTimer: ReturnType<typeof setTimeout> | undefined;

      const cleanup = (): void => {
        if (abandonTimer !== undefined) clearTimeout(abandonTimer);
        if (deadlineTimer !== undefined) clearTimeout(deadlineTimer);
        abandonTimer = undefined;
        deadlineTimer = undefined;
        abortSignal.removeEventListener('abort', onAbort);
      };

      // Auto-deny a pending permission and unblock the turn. Idempotent: the
      // abort, abandon (detached), absolute-deadline and user-resolve paths can
      // race, but the turn must settle exactly once (one permission.resolved).
      const denyAndResolve = (message: string): void => {
        if (settled) return;
        settled = true;
        cleanup();
        reg.deletePermission(permissionId);
        appendEvent(sessionId, turnId, 'permission.resolved', {
          permissionId,
          tool: toolName,
          input,
          decision: 'deny',
        });
        resolve({ behavior: 'deny', message });
      };

      // R1: if the turn is interrupted while a permission is pending, auto-deny
      // so query() can unblock and propagate the abort.
      const onAbort = (): void => denyAndResolve('Turn interrupted — pending permission auto-denied');
      if (abortSignal.aborted) {
        onAbort();
        return;
      }
      abortSignal.addEventListener('abort', onAbort, { once: true });

      // NEW-7: a permission pending with the client detached is abandoned after
      // the grace window so the turn finishes (activeTurns → 0, idleSince set)
      // and the reaper can suspend the pod. While a client is attached this
      // reschedules; the absolute deadline below bounds the attached case.
      const checkAbandoned = (): void => {
        if (settled) return;
        if (reg.isDetached()) {
          denyAndResolve('Pending permission auto-denied — client detached');
          return;
        }
        abandonTimer = setTimeout(checkAbandoned, PERMISSION_ABANDON_MS);
      };
      abandonTimer = setTimeout(checkAbandoned, PERMISSION_ABANDON_MS);

      // C8: absolute deadline — auto-deny even while a client is attached so an
      // unanswered prompt can never hold the turn (and the pod) open forever.
      deadlineTimer = setTimeout(
        () => denyAndResolve('Permission timed out — auto-denied after no response'),
        PERMISSION_MAX_WAIT_MS,
      );

      reg.registerPermission({
        permissionId,
        tool: toolName,
        input,
        resolve: (allow, scope, editedInput): boolean => {
          // B8: first-write-wins. Report to the caller (server.ts) whether this
          // resolution actually took effect: false when already auto-denied
          // (abort / abandon / deadline) so the route replies honestly (409)
          // instead of a lying resolved:true; true when this call wins the race.
          if (settled) return false;
          settled = true;
          cleanup();
          // Honor the resolution scope: scope:'session' records a tool-name
          // grant so future uses of this tool auto-allow (above); 'once'/default
          // is a single allow. resolutionOutcome maps allow+scope → decision.
          const { decision, grantSession } = resolutionOutcome(allow, scope);
          if (grantSession) sessionGrants.grant(toolName);
          appendEvent(sessionId, turnId, 'permission.resolved', {
            permissionId,
            tool: toolName,
            input,
            decision,
          });
          reg.deletePermission(permissionId); // R2: clean up after resolve
          if (allow) {
            // editedInput is validated as JSON at the server boundary, but guard
            // here too: a malformed edit must never throw and leave canUseTool
            // unresolved — that would hang the turn forever (C8). Fall back to
            // the original input instead.
            const updatedInput = parseEditedInput(editedInput);
            resolve({
              behavior: 'allow',
              ...(updatedInput ? { updatedInput } : {}),
            });
          } else {
            resolve({
              behavior: 'deny',
              message: `Permission denied by user (permission ${permissionId})`,
            });
          }
          return true;
        },
      });
    });
  };
}

// --- SDK message → normalized event mapping ------------------------------

/**
 * True when an SDK message carries the first streamed assistant TEXT token — a
 * `stream_event` whose partial event is a content_block_delta of type
 * `text_delta`. Drives the §10 `turn.first_delta` trace milestone (the moment
 * the model starts producing visible output), distinct from `turn.first_message`
 * (the first SDK message of any kind, typically the init system message). Pure
 * and exported so the classification is unit-testable.
 */
export function isAssistantTextDelta(msg: SDKMessage): boolean {
  if (msg.type !== 'stream_event') return false;
  const ev = (msg as { event?: { type?: string; delta?: { type?: string } } }).event;
  return ev?.type === 'content_block_delta' && ev.delta?.type === 'text_delta';
}

/** Run a single turn: call query() and map the message stream to events. */
export async function runTurn(
  cfg: RunnerConfig,
  turnId: string,
  prompt: string,
  resume: string | undefined,
  allowedToolsOverride: string[] | undefined,
  mode: string | undefined,
  model: string | undefined,
  effort: string | undefined,
  abort: AbortController,
): Promise<void> {
  const reg = getRegistry();
  const sessionId = reg.state.sandbox_session_id;

  // §10 observability: time the turn lifecycle (first SDK message, first
  // assistant delta, settle) under the turn id. No-op unless SANDBOX_TRACE is
  // set. msgCount tallies SDK messages consumed across the (possibly retried)
  // query loop for the per-turn summary line.
  const trace = startTurnTrace(turnId);
  let msgCount = 0;

  appendEvent(sessionId, turnId, 'turn.started', { prompt });

  // A turn normally resumes the persisted Claude session head (buildOptions's
  // effectiveResume). Fail-soft (Workstream B): if that id is stale — the SDK
  // throws "No conversation found" (confirmed by the B spike; a host-path
  // migration or transcript GC can orphan an id) — drop it and retry ONCE as a
  // fresh session so the user's prompt still runs instead of erroring the whole
  // turn. Any other failure surfaces as before.
  let clientResume = resume;
  let resultSeen = false;
  let retriedStaleResume = false;

  try {
    for (;;) {
      const options = buildOptions(cfg, turnId, clientResume, allowedToolsOverride, mode, model, effort, abort);
      const usedResume = options.resume;
      const q: Query = query({ prompt, options });
      // Per-attempt content-block→tool_use-id attribution for tool.delta (D6);
      // fresh per query() because block indexes restart with the stream.
      const streamTools: StreamToolIndex = new Map();
      let rateLimitsFetched = false;
      let modelsFetched = false;
      let staleResume = false;
      try {
        for await (const msg of q) {
          msgCount++;
          trace.mark('turn.first_message');
          if (isAssistantTextDelta(msg)) trace.mark('turn.first_delta');
          // Fail-soft (1/2): the SDK yields an is_error `result` for a stale
          // resume id and THEN throws. While a retry is still available, do NOT
          // map that terminal result — mapMessage would emit a spurious
          // turn.failed+error ahead of the successful retry. Flag it and let the
          // throw (or, defensively, the stream simply ending) drive the retry.
          if (usedResume && !retriedStaleResume && isStaleResultMessage(msg)) {
            staleResume = true;
            continue;
          }
          await mapMessage(msg, sessionId, turnId, streamTools);
          // Fetch the claude.ai plan rate-limit windows once per turn, triggered
          // by the SDK init message. The control channel (stdin) is open from
          // init until the result message closes it (single-user-turn mode), so
          // this MUST fire mid-turn — a post-loop call would race the closed
          // stdin. Fire-and-forget: it never blocks or fails the turn.
          if (!rateLimitsFetched && msg.type === 'system' && msg.subtype === 'init') {
            rateLimitsFetched = true;
            void fetchAndEmitRateLimits(q, sessionId, turnId);
          }
          // Likewise fetch the SDK's supported-model list once per turn on init
          // (same open-control-channel window) so the TUI's /model palette lists
          // the account's real models instead of the hardcoded aliases.
          if (!modelsFetched && msg.type === 'system' && msg.subtype === 'init') {
            modelsFetched = true;
            void fetchAndEmitModels(q, sessionId, turnId);
          }
        }
        // Stream ended without throwing. Unless we skipped a stale result (and
        // must retry), the turn completed normally.
        if (!staleResume) {
          resultSeen = true;
          break;
        }
      } catch (err) {
        // When aborted, server.ts already emitted 'turn.interrupted' at the
        // /interrupt route; do not re-emit it here (R3: would produce a duplicate).
        if (abort.signal.aborted) break;
        const message = err instanceof Error ? err.message : String(err);
        // Fail-soft (2/2): retry once on a stale resume id (detected as the
        // skipped result above, or via the throw message defensively); any other
        // failure surfaces as before.
        if (!(staleResume || shouldRetryStaleResume(usedResume, retriedStaleResume, message))) {
          appendEvent(sessionId, turnId, 'turn.failed', { message });
          appendEvent(sessionId, turnId, 'error', { message });
          break;
        }
      }
      // Fail-soft retry: clear the orphaned head (so later turns don't re-fail on
      // it) and run once more without resume — degraded (history not loaded) but
      // the turn still runs instead of erroring.
      retriedStaleResume = true;
      clientResume = undefined;
      reg.clearClaudeSession();
      console.error(`runTurn: resume id ${usedResume} is stale; retrying once without resume`);
    }
  } finally {
    // §10: settle the turn trace (total duration + SDK message count) before the
    // idle/title bookkeeping, so the summary line reflects the query() lifetime.
    trace.settle(msgCount);
    // Finalize the turn FIRST so the session goes idle promptly: finishTurn
    // clears the active-turn count and starts the idle reaper's clock. The
    // one-time title summary below runs an extra summarizer round-trip, and we
    // must not keep the session "busy" (or delay suspension) while it does.
    reg.finishTurn(turnId);
    // T6: one-time auto title after the first assistant turn. Only on a normal
    // completion (not aborted/errored). Non-fatal: maybeGenerateTitle swallows
    // any failure so it can never break the turn loop. Ordered after finishTurn
    // per the above; the turn is already done/idle by the time this runs.
    if (resultSeen && !abort.signal.aborted) {
      await maybeGenerateTitle(liveTitleDeps(cfg, sessionId, turnId));
    }
  }
}

// --- Plan rate-limit windows (status line) -------------------------------

/**
 * Fetch the claude.ai plan rate-limit windows (5-hour + weekly) from the SDK's
 * structured /usage data and emit a rate_limit.updated event, so the status
 * line shows REAL reset instants instead of projecting 5h/7d from the wall
 * clock (TODO.md). The TUI counts down locally from the reset instant, so one
 * fetch per turn keeps the display fresh.
 *
 * Best-effort and fully fail-soft:
 *   - The underlying method is experimental (the verbose name is a deliberate
 *     warning) and may throw or be absent in some SDK versions.
 *   - rate_limits is null for API-key / Bedrock / Vertex / missing-scope
 *     sessions (rate_limits_available=false); we emit available:false then so
 *     the TUI hides the windows rather than fabricating values.
 *   - The control channel may already be closing — any error is swallowed.
 *
 * Must be called while the control channel is open (during a turn, after the
 * init message), never after the turn's result message closes stdin.
 */
async function fetchAndEmitRateLimits(q: Query, sessionId: string, turnId: string): Promise<void> {
  try {
    const usage = await q.usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET();
    const rl = usage?.rate_limits;
    // subscription_type is 'pro'/'max'/etc. on a claude.ai session, or null for
    // headless setup-token / API-key / 3P sessions. Pass it through so the TUI
    // can label the unavailable case "n/a (headless auth)" instead of a blank.
    const sub = usage?.subscription_type;
    if (!usage?.rate_limits_available || !rl) {
      appendEvent(sessionId, turnId, 'rate_limit.updated', {
        available: false,
        fiveHourUtil: 0,
        sevenDayUtil: 0,
        ...(sub ? { subscriptionType: sub } : {}),
      });
      return;
    }
    const five = rl.five_hour;
    const week = rl.seven_day;
    // Per-model weekly caps (Max plans). The window object is present only when
    // the plan has a separate cap for that model; include the field then (even
    // at 0% util) so the Go side sees a non-nil pointer = "present".
    const opus = rl.seven_day_opus;
    const sonnet = rl.seven_day_sonnet;
    appendEvent(sessionId, turnId, 'rate_limit.updated', {
      available: true,
      ...(sub ? { subscriptionType: sub } : {}),
      fiveHourUtil: five?.utilization ?? 0,
      sevenDayUtil: week?.utilization ?? 0,
      ...(five?.resets_at ? { fiveHourResetsAt: five.resets_at } : {}),
      ...(week?.resets_at ? { sevenDayResetsAt: week.resets_at } : {}),
      ...(opus ? { sevenDayOpusUtil: opus.utilization ?? 0 } : {}),
      ...(opus?.resets_at ? { sevenDayOpusResetsAt: opus.resets_at } : {}),
      ...(sonnet ? { sevenDaySonnetUtil: sonnet.utilization ?? 0 } : {}),
      ...(sonnet?.resets_at ? { sevenDaySonnetResetsAt: sonnet.resets_at } : {}),
    });
  } catch (err) {
    // Experimental API absent, non-streaming session, or the control channel
    // raced shut: never fatal — emit nothing and keep the turn healthy.
    console.error(
      'fetchAndEmitRateLimits (non-fatal):',
      err instanceof Error ? err.message : err,
    );
  }
}

/**
 * Fetch the account's supported models via the SDK control channel and emit a
 * models.available event so the TUI can build the /model palette dynamically
 * (instead of the hardcoded opus/sonnet/haiku aliases). Same constraints as
 * fetchAndEmitRateLimits: must run while the control channel is open (during a
 * turn, after init) and is fire-and-forget — any failure is swallowed so the
 * turn stays healthy and the TUI just keeps the alias fallback.
 */
async function fetchAndEmitModels(q: Query, sessionId: string, turnId: string): Promise<void> {
  try {
    const models = await q.supportedModels();
    if (!Array.isArray(models) || models.length === 0) return;
    appendEvent(sessionId, turnId, 'models.available', {
      models: models.map((m) => ({
        value: m.value,
        displayName: m.displayName,
        ...(m.description ? { description: m.description } : {}),
      })),
    });
  } catch (err) {
    console.error(
      'fetchAndEmitModels (non-fatal):',
      err instanceof Error ? err.message : err,
    );
  }
}

// --- One-time auto session title (T6) ------------------------------------

/**
 * Dependencies for the one-shot title generation, injected so the logic is
 * unit-testable without an SDK call or a live registry. In production these are
 * bound to the session registry, the event log, and a real SDK summarizer.
 */
export interface TitleDeps {
  sessionId: string;
  turnId: string;
  /** True if the one-shot title was already generated for this session. */
  isTitleGenerated: () => boolean;
  /** Persist the one-shot guard (title_generated = true) in session.json. */
  markTitleGenerated: () => void;
  /** Append a normalized event to the log (append-before-stream). */
  emit: (type: 'session.title', payload: { title: string }) => void;
  /** Produce a short task summary; may throw or return '' (both swallowed). */
  summarize: () => Promise<string>;
}

/**
 * Generate the session's auto title exactly once, after the first assistant
 * turn. Idempotent (guarded by isTitleGenerated) and non-fatal: any failure or
 * empty summary is logged and swallowed so the title stays the derived basename.
 * The guard is persisted before emitting so a crash mid-emit can't double-fire —
 * i.e. we choose at-most-once over at-least-once delivery. That is acceptable
 * because the auto title is a non-essential nicety: if the event is lost, the
 * derived basename remains as the durable fallback.
 */
export async function maybeGenerateTitle(deps: TitleDeps): Promise<void> {
  if (deps.isTitleGenerated()) return;
  let title = '';
  try {
    title = sanitizeTitle(await deps.summarize());
  } catch (err) {
    console.error('maybeGenerateTitle: summarization failed (non-fatal):', err);
    deps.markTitleGenerated(); // do not retry a failing summary every turn
    return;
  }
  // Set the guard regardless of whether we emit, so an empty summary doesn't
  // trigger a fresh summarizer call on every subsequent turn.
  deps.markTitleGenerated();
  if (title === '') return;
  deps.emit('session.title', { title });
}

/**
 * Build a TitleDeps bound to the live registry + event log for a turn. The
 * summarizer runs a single cheap query() over the just-completed conversation
 * (resumed via the captured claude session id) with the hardcoded prompt.
 */
function liveTitleDeps(cfg: RunnerConfig, sessionId: string, turnId: string): TitleDeps {
  const reg = getRegistry();
  return {
    sessionId,
    turnId,
    isTitleGenerated: () => !shouldGenerateTitle(reg.state),
    markTitleGenerated: () => reg.setTitleGenerated(),
    emit: (type, payload) => {
      appendEvent(sessionId, turnId, type, payload);
    },
    summarize: async () => {
      const opts: Options = {
        cwd: resolveWorkspaceDir(cfg.projectPath),
        permissionMode: 'bypassPermissions',
        allowDangerouslySkipPermissions: true,
        allowedTools: [],
        disallowedTools: DEFAULT_DISALLOWED_TOOLS,
        // IS_SANDBOX: this summarizer always runs bypassPermissions, so without it
        // the root guard in the spawned binary rejects every title generation as
        // uid 0 (silently swallowed by maybeGenerateTitle's catch — auto-titling
        // was quietly broken). See buildOptions for the full rationale.
        // buildAgentEnv strips RUNNER_TOKEN here too (A1).
        env: buildAgentEnv({
          CLAUDE_CONFIG_DIR,
          CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1',
          IS_SANDBOX: process.env.IS_SANDBOX ?? '1',
        }),
        settingSources: [],
        // Resume the just-completed conversation for context, but FORK it: the
        // TITLE_PROMPT Q&A is written to a throwaway forked session, never to the
        // live resumable head. Without forkSession the summary would pollute the
        // head's transcript — invisible before, but now that every user turn
        // resumes that head (Workstream B) the next turn would load this stray
        // "Summarize this task…" exchange into its history. Forking also keeps the
        // summarizer off the head entirely, so it can't collide with a user turn
        // that resumes the head while this one-shot summary is still in flight.
        ...(reg.state.claude_session_id ? { resume: reg.state.claude_session_id, forkSession: true } : {}),
      };
      let text = '';
      for await (const msg of query({ prompt: TITLE_PROMPT, options: opts })) {
        if (msg.type === 'assistant') {
          for (const block of msg.message.content as BetaContentBlock[]) {
            if (block.type === 'text') text += (block as BetaTextBlock).text;
          }
        }
      }
      return text;
    },
  };
}

// claudeAgent is the Claude Agent SDK backend (the default). It is a thin
// binding of the module's runTurn to the Agent interface; see ./agent.ts.
export const claudeAgent: Agent = { runTurn };

// --- Workspace status (git branch + dirty) -------------------------------

/**
 * Parse `git rev-list --left-right --count @{upstream}...HEAD` output ("<behind>
 * \t<ahead>") into {ahead, behind}. Pure + exported so B6's async git path keeps
 * the exact same parsing (unit-tested) it had when it ran synchronously; an
 * unparseable/empty string (no upstream) yields {0,0}.
 */
export function parseAheadBehind(revListOutput: string): { ahead: number; behind: number } {
  const m = /^(\d+)\s+(\d+)$/.exec(revListOutput);
  if (!m) return { ahead: 0, behind: 0 };
  return { behind: parseInt(m[1], 10), ahead: parseInt(m[2], 10) };
}

/**
 * Emit a workspace.status event (git branch + dirty/ahead/behind) for the chat
 * status line. Runs git in the session cwd; emits nothing (and never throws)
 * when cwd is not a git repo or git is unavailable. Called at session start and
 * after each turn completes.
 *
 * B6: async so the (up to three) git subprocess calls never block the event loop.
 * Callers await it (mapMessage → the runTurn loop) so the workspace.status event
 * keeps landing AFTER turn.completed, preserving the prior ordering.
 */
async function emitWorkspaceStatus(sessionId: string, turnId: string): Promise<void> {
  const cwd = resolveWorkspaceDir(loadConfig().projectPath);
  // A1: git executes repo-local config from the agent-writable workspace
  // (core.fsmonitor, hooks), so this child must not inherit RUNNER_TOKEN either
  // — a workspace fsmonitor command would run with the runner's env. Sanitize
  // like every other child spawn, and disable the two repo-local code-execution
  // knobs for good measure (they have no bearing on branch/dirty/ahead-behind).
  // The env sanitization + `-c core.fsmonitor= -c core.hooksPath=/dev/null` flags
  // are the A1 security fix — preserved verbatim across the sync→async move.
  const git = async (args: string[]): Promise<string> => {
    const { stdout } = await execFileAsync(
      'git',
      ['-c', 'core.fsmonitor=', '-c', 'core.hooksPath=/dev/null', ...args],
      {
        cwd,
        encoding: 'utf8',
        timeout: 3000,
        env: sanitizedExecEnv(process.env),
      },
    );
    return stdout.trim();
  };

  let branch: string;
  try {
    branch = await git(['rev-parse', '--abbrev-ref', 'HEAD']);
  } catch {
    return; // not a git repo (or git missing): emit nothing, no error.
  }

  let dirty = false;
  try {
    dirty = (await git(['status', '--porcelain'])).length > 0;
  } catch {
    // leave dirty=false
  }

  let ahead = 0;
  let behind = 0;
  try {
    // "<behind>\t<ahead>" relative to the upstream; errors when no upstream.
    ({ ahead, behind } = parseAheadBehind(
      await git(['rev-list', '--left-right', '--count', '@{upstream}...HEAD']),
    ));
  } catch {
    // no upstream configured: ahead/behind stay 0
  }

  appendEvent(sessionId, turnId, 'workspace.status', { branch, dirty, ahead, behind });
}

// mapMessage is the live binding of the pure SDK-message→event mapping
// (./mapping.ts) to this pod's event log and session registry. The mapping
// logic itself is pure and sqlite-free (so it is unit-testable without the
// native addon); here we bind its `emit` to appendEvent (append-before-stream)
// and apply the registry-affecting observations it returns: persist the model
// into session.json, capture the Claude session id, and emit workspace.status
// (which shells out to git) at session start and after a completed turn.
async function mapMessage(
  msg: SDKMessage,
  sessionId: string,
  turnId: string,
  streamTools?: StreamToolIndex,
): Promise<void> {
  const reg = getRegistry();
  const result = mapMessagePure(
    msg,
    (type, payload) => appendEvent(sessionId, turnId, type, payload),
    streamTools,
  );
  if (result.isInit) {
    // Persist the model into session.json so /status (and the dashboard list,
    // even when suspended) reports it (Seam C).
    if (result.model) reg.setModel(result.model);
    // Capture the Claude session id from the init system message. Capture-LATEST
    // (follow the live resumable head) so a chain of resumes keeps threading the
    // current session; the spike shows the id is stable across a plain resume so
    // this normally no-ops after turn 1 (shouldCaptureClaudeSession's `!==` guard
    // avoids a redundant session.json write each turn).
    const observedId = result.claudeSessionId ?? '';
    if (shouldCaptureClaudeSession(reg.state.claude_session_id, observedId)) {
      reg.setClaudeSession(observedId);
    }
    await emitWorkspaceStatus(sessionId, turnId);
  }
  if (result.completed) await emitWorkspaceStatus(sessionId, turnId);
}

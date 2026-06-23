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

import { query, type Options, type Query, type SDKMessage } from '@anthropic-ai/claude-agent-sdk';
import type { BetaContentBlock, BetaRawContentBlockDeltaEvent, BetaRawContentBlockStartEvent, BetaTextBlock, BetaThinkingBlock, BetaToolUseBlock } from '@anthropic-ai/sdk/resources/beta/messages/messages';
import type { PermissionResult, HookCallback, HookInput, SyncHookJSONOutput } from '@anthropic-ai/claude-agent-sdk';
import { join } from 'node:path';
import { appendEvent, shortId } from './events.js';
import { appendAudit } from './audit.js';
import { getRegistry } from './session.js';
import type { RunnerConfig } from './session.js';
import { CLAUDE_CONFIG_DIR, WORKSPACE_ROOT } from './types.js';

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

/**
 * Bash commands blocked by the PreToolUse(Bash) hook (spec 8.5). Blocks
 * obvious host/cluster/credential operations. Pattern-matched as a prefix on
 * the raw command string.
 */
const BLOCKED_BASH_PATTERNS: RegExp[] = [
  /\bkubectl\b/,
  /\btalosctl\b/,
  /\bhelm\b/,
  /\bargocd\b/,
  /\bop\s+(auth|login|whoami|token|kubeconfig)\b/,
  /\bsudo\b/,
  /\bssh\b/,
  /\bscp\b/,
  /\brsync\b/,
  /\bdocker\b/,
  /\bpodman\b/,
  /\bcrictl\b/,
  /\bctr\b/,
  /\bnsenter\b/,
  /\bchroot\b/,
  /\bmount\b/,
  /\bumount\b/,
  /\bip\s+(addr|link|route)\b/,
  /\biptables\b/,
  /\bchmod\s+[0-7]{4}?\s+\/etc\b/,
  /\bcat\s+\/etc\/shadow\b/,
  /\bcat\s+\/etc\/passwd\b/,
  /~\/\.ssh\//,
  /\/etc\/kubernetes\//,
  /\/var\/run\/docker\.sock/,
  /\/run\/containerd\//,
  /\bANTHROPIC_API_KEY\b/,
  /\bAWS_SECRET_ACCESS_KEY\b/,
  /\bKUBECONFIG\b/,
];

function bashCommandBlocked(command: string): boolean {
  return BLOCKED_BASH_PATTERNS.some((re) => re.test(command));
}

// --- SDK options ----------------------------------------------------------

/** Build the SDK Options for a turn (spec 8.4). */
export function buildOptions(
  cfg: RunnerConfig,
  turnId: string,
  resume: string | undefined,
  allowedToolsOverride: string[] | undefined,
  abort: AbortController,
): Options {
  const reg = getRegistry();
  const sessionId = reg.state.sandbox_session_id;
  const cwd = join(WORKSPACE_ROOT, cfg.projectPath);

  const options: Options = {
    cwd,
    permissionMode: 'acceptEdits',
    allowedTools: allowedToolsOverride ?? DEFAULT_ALLOWED_TOOLS,
    disallowedTools: DEFAULT_DISALLOWED_TOOLS,
    env: {
      ...process.env,
      CLAUDE_CONFIG_DIR,
      CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1',
    },
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
    canUseTool: makeCanUseTool(sessionId, turnId),
  };

  if (resume) {
    // resume is the Claude SDK session id (not a turn id). The CLI maps turn
    // resume→claude session id before calling the runner in practice; here we
    // pass through whatever the client supplied.
    options.resume = resume;
  }

  return options;
}

// --- Hooks ---------------------------------------------------------------

function makePreToolUseBashHook(
  sessionId: string,
  turnId: string,
): HookCallback {
  return async (input: HookInput): Promise<SyncHookJSONOutput> => {
    if (input.hook_event_name !== 'PreToolUse') return { continue: true };
    const command = String((input.tool_input as { command?: unknown })?.command ?? '');
    if (bashCommandBlocked(command)) {
      appendEvent(sessionId, turnId, 'tool.failed', {
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

function makeCanUseTool(
  sessionId: string,
  turnId: string,
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
    return new Promise<PermissionResult>((resolve) => {
      reg.registerPermission({
        permissionId,
        tool: toolName,
        input,
        resolve: (allow, _scope, editedInput) => {
          const decision = allow ? 'allow-once' : 'deny';
          appendEvent(sessionId, turnId, 'permission.resolved', {
            permissionId,
            tool: toolName,
            input,
            decision,
          });
          if (allow) {
            resolve({
              behavior: 'allow',
              ...(editedInput ? { updatedInput: JSON.parse(editedInput) as Record<string, unknown> } : {}),
            });
          } else {
            resolve({
              behavior: 'deny',
              message: `Permission denied by user (permission ${permissionId})`,
            });
          }
        },
      });
    });
  };
}

// --- SDK message → normalized event mapping ------------------------------

/** Run a single turn: call query() and map the message stream to events. */
export async function runTurn(
  cfg: RunnerConfig,
  turnId: string,
  prompt: string,
  resume: string | undefined,
  allowedToolsOverride: string[] | undefined,
  abort: AbortController,
): Promise<void> {
  const reg = getRegistry();
  const sessionId = reg.state.sandbox_session_id;
  const options = buildOptions(cfg, turnId, resume, allowedToolsOverride, abort);

  appendEvent(sessionId, turnId, 'turn.started', { prompt });

  const q: Query = query({ prompt, options });
  let resultSeen = false;

  try {
    for await (const msg of q) {
      mapMessage(msg, sessionId, turnId);
      // Capture the Claude session id from the init system message.
      if (!reg.state.claude_session_id && msg.type === 'system' && msg.subtype === 'init') {
        reg.setClaudeSession(msg.session_id);
      }
    }
    resultSeen = true;
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    if (abort.signal.aborted) {
      appendEvent(sessionId, turnId, 'turn.interrupted', { reason: 'client interrupt' });
    } else {
      appendEvent(sessionId, turnId, 'turn.failed', { message });
      appendEvent(sessionId, turnId, 'error', { message });
    }
  } finally {
    // The SDK emits a 'result' message as the final non-error outcome; if we
    // never saw one and weren't aborted/errored above, treat the turn as
    // completed (some SDK versions close the generator without a result).
    if (resultSeen) {
      // turn.completed is emitted by mapMessage when the result message
      // arrives; only emit a fallback if it somehow didn't.
    }
    reg.finishTurn(turnId);
  }
}

function mapMessage(msg: SDKMessage, sessionId: string, turnId: string): void {
  const reg = getRegistry();
  switch (msg.type) {
    case 'system': {
      if (msg.subtype === 'init') {
        appendEvent(sessionId, turnId, 'session.started', {
          model: msg.model,
          cwd: msg.cwd,
          tools: msg.tools,
          permissionMode: msg.permissionMode,
          claudeSessionId: msg.session_id,
        });
      }
      break;
    }
    case 'assistant': {
      // Full assistant message: one or more content blocks (text, thinking,
      // tool_use). Emit message.completed + tool.started for each block.
      handleAssistantMessage(msg, sessionId, turnId);
      // Usage is carried on the BetaMessage.
      emitUsage(msg.message.usage, sessionId, turnId);
      break;
    }
    case 'user': {
      // User messages in the SDK stream carry tool_result blocks. Map each to
      // tool.completed or tool.failed.
      handleUserMessage(msg, sessionId, turnId);
      break;
    }
    case 'stream_event': {
      // Partial assistant message: incremental deltas for text/thinking/tool
      // input as they arrive.
      handleStreamEvent(msg.event, sessionId, turnId);
      break;
    }
    case 'result': {
      handleResultMessage(msg, sessionId, turnId);
      break;
    }
    default:
      // Other SDK message types (tool_progress, status, api_retry, etc.) are
      // not part of the normalized event model; ignore them.
      break;
  }
}

// --- Assistant / user / stream / result handlers -------------------------

function handleAssistantMessage(
  msg: Extract<SDKMessage, { type: 'assistant' }>,
  sessionId: string,
  turnId: string,
): void {
  for (const block of msg.message.content as BetaContentBlock[]) {
    switch (block.type) {
      case 'text': {
        appendEvent(sessionId, turnId, 'message.started', {
          role: 'assistant',
          content: '',
        });
        appendEvent(sessionId, turnId, 'message.completed', {
          role: 'assistant',
          content: (block as BetaTextBlock).text,
        });
        break;
      }
      case 'thinking': {
        const tb = block as BetaThinkingBlock;
        appendEvent(sessionId, turnId, 'reasoning.started', {});
        appendEvent(sessionId, turnId, 'reasoning.completed', {
          content: tb.thinking,
        });
        break;
      }
      case 'tool_use': {
        const tu = block as BetaToolUseBlock;
        appendEvent(sessionId, turnId, 'tool.started', {
          tool: tu.name,
          input: tu.input,
        });
        break;
      }
      default:
        // Other block kinds (redacted_thinking, server_tool_use, mcp_*,
        // compaction, etc.) are not normalized.
        break;
    }
  }
}

function handleUserMessage(
  msg: Extract<SDKMessage, { type: 'user' }>,
  sessionId: string,
  turnId: string,
): void {
  const content = msg.message.content;
  // MessageParam.content is string | array of block params. Tool results are
  // { type: 'tool_result', tool_use_id, content, is_error } blocks.
  if (!Array.isArray(content)) {
    appendEvent(sessionId, turnId, 'message.started', {
      role: 'user',
      content: typeof content === 'string' ? content : '',
    });
    appendEvent(sessionId, turnId, 'message.completed', {
      role: 'user',
      content: typeof content === 'string' ? content : '',
    });
    return;
  }
  for (const block of content as Array<{ type: string; tool_use_id?: string; content?: unknown; is_error?: boolean }>) {
    if (block.type === 'tool_result') {
      const outputStr =
        typeof block.content === 'string'
          ? block.content
          : Array.isArray(block.content)
            ? (block.content as Array<{ text?: string }>)
                .map((c) => c?.text ?? '')
                .join('')
            : '';
      if (block.is_error) {
        appendEvent(sessionId, turnId, 'tool.failed', {
          output: outputStr,
        });
      } else {
        appendEvent(sessionId, turnId, 'tool.completed', {
          output: outputStr,
        });
      }
    }
  }
}

function handleStreamEvent(
  event: BetaRawContentBlockStartEvent | BetaRawContentBlockDeltaEvent | { type: string },
  sessionId: string,
  turnId: string,
): void {
  switch (event.type) {
    case 'content_block_start': {
      const e = event as BetaRawContentBlockStartEvent;
      const block = e.content_block;
      switch (block.type) {
        case 'text':
          appendEvent(sessionId, turnId, 'message.started', { role: 'assistant', content: '' });
          break;
        case 'thinking':
          appendEvent(sessionId, turnId, 'reasoning.started', {});
          break;
        case 'tool_use':
          appendEvent(sessionId, turnId, 'tool.started', {
            tool: (block as BetaToolUseBlock).name,
            input: (block as BetaToolUseBlock).input,
          });
          break;
        default:
          break;
      }
      break;
    }
    case 'content_block_delta': {
      const e = event as BetaRawContentBlockDeltaEvent;
      const delta = e.delta;
      switch (delta.type) {
        case 'text_delta':
          appendEvent(sessionId, turnId, 'message.delta', {
            role: 'assistant',
            content: delta.text,
            delta: true,
          });
          break;
        case 'thinking_delta':
          appendEvent(sessionId, turnId, 'reasoning.delta', {
            content: delta.thinking,
            delta: true,
          });
          break;
        case 'input_json_delta':
          appendEvent(sessionId, turnId, 'tool.delta', {
            partialJson: delta.partial_json,
            delta: true,
          });
          break;
        default:
          break;
      }
      break;
    }
    case 'content_block_stop':
      // Block completion is reflected in the full assistant/user message that
      // follows; no separate event needed for the normalized model.
      break;
    case 'message_start':
      // Carried by the full assistant message's usage; nothing to emit here.
      break;
    case 'message_delta': {
      // message_delta carries cumulative usage in some SDK configs; the
      // authoritative usage comes on the result message. Skip to avoid
      // duplicate usage.updated events.
      break;
    }
    case 'message_stop':
      break;
    default:
      break;
  }
}

function handleResultMessage(
  msg: Extract<SDKMessage, { type: 'result' }>,
  sessionId: string,
  turnId: string,
): void {
  const reg = getRegistry();
  if (msg.subtype === 'success') {
    appendEvent(sessionId, turnId, 'turn.completed', {
      result: msg.result,
      stopReason: msg.stop_reason,
      numTurns: msg.num_turns,
      durationMs: msg.duration_ms,
    });
    emitUsage(msg.usage, sessionId, turnId);
    appendEvent(sessionId, turnId, 'usage.updated', {
      inputTokens: msg.usage.input_tokens,
      outputTokens: msg.usage.output_tokens,
      cacheReadTokens: msg.usage.cache_read_input_tokens ?? 0,
      cacheWriteTokens: msg.usage.cache_creation_input_tokens ?? 0,
      totalCostUsd: msg.total_cost_usd,
    });
  } else {
    appendEvent(sessionId, turnId, 'turn.failed', {
      subtype: msg.subtype,
      errors: msg.errors,
    });
    appendEvent(sessionId, turnId, 'error', {
      message: msg.errors.join('; ') || `turn failed: ${msg.subtype}`,
      code: msg.subtype,
    });
    emitUsage(msg.usage, sessionId, turnId);
  }
}

function emitUsage(
  usage: { input_tokens: number; output_tokens: number; cache_read_input_tokens?: number | null; cache_creation_input_tokens?: number | null } | undefined,
  sessionId: string,
  turnId: string,
): void {
  if (!usage) return;
  appendEvent(sessionId, turnId, 'usage.updated', {
    inputTokens: usage.input_tokens,
    outputTokens: usage.output_tokens,
    cacheReadTokens: usage.cache_read_input_tokens ?? 0,
    cacheWriteTokens: usage.cache_creation_input_tokens ?? 0,
    totalCostUsd: 0,
  });
}

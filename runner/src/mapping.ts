// Pure SDK-message → normalized-event mapping.
//
// This module deliberately imports NOTHING that loads better-sqlite3 (or any
// other native addon) at module load: it takes an `emit` callback and only maps
// SDK message shapes to normalized event types + payloads. That keeps the
// mapping unit-testable under CI's `npm install --ignore-scripts`, where the
// sqlite native addon is unavailable, while claude.ts binds `emit` to the real
// append-before-stream event log.
//
// Side effects that need the session registry (persisting the model, capturing
// the Claude session id, emitting workspace.status) are NOT done here; instead
// mapMessage returns the observations the caller needs (see MapResult) so the
// caller (claude.ts) can apply them against the live registry.

import type { SDKMessage } from '@anthropic-ai/claude-agent-sdk';
import type {
  BetaContentBlock,
  BetaRawContentBlockDeltaEvent,
  BetaRawContentBlockStartEvent,
  BetaTextBlock,
  BetaThinkingBlock,
  BetaToolUseBlock,
} from '@anthropic-ai/sdk/resources/beta/messages/messages';
import type { EventType, TodoItem } from './types.js';

/** Append a normalized event (append-before-stream in production). */
export type EmitFn = (type: EventType, payload: Record<string, unknown>) => void;

/** Registry-affecting observations the caller should apply after mapping. */
export interface MapResult {
  /** Set on a system/init message: the active model id (persist via setModel). */
  model?: string;
  /** Set on a system/init message: the Claude session id (persist if unset). */
  claudeSessionId?: string;
  /** True on a system/init message; the caller emits workspace.status then. */
  isInit?: boolean;
  /** True on a successful result message; the caller emits workspace.status. */
  completed?: boolean;
}

/**
 * TOOL_OUTPUT_CAP bounds the captured tool output carried on tool.completed /
 * tool.failed events. A tool result can be arbitrarily large (a Bash command
 * dumping a whole file), and that output now flows through the SQLite event log,
 * the SSE stream, and the TUI's ctrl+o expansion — so it is capped here, at the
 * source, rather than letting a pathological result bloat all three. Normal
 * outputs are untouched; the summary (line count / first line) the TUI derives is
 * unaffected for them.
 */
const TOOL_OUTPUT_CAP = 64 * 1024;

/**
 * capToolOutput returns s unchanged when within TOOL_OUTPUT_CAP, otherwise keeps
 * the first and last half of the cap with a "… N bytes truncated …" marker so
 * both the start (the command echo / headers) and the end (the exit / error
 * tail) survive.
 */
export function capToolOutput(s: string): string {
  if (s.length <= TOOL_OUTPUT_CAP) return s;
  const half = Math.floor(TOOL_OUTPUT_CAP / 2);
  const head = s.slice(0, half);
  const tail = s.slice(s.length - half);
  const omitted = s.length - head.length - tail.length;
  return `${head}\n… ${omitted} bytes truncated …\n${tail}`;
}

/**
 * Per-turn streaming state, created once per query() and passed to every
 * mapMessage call. Two maps:
 *
 *   byIndex — `${parentToolUseId}:${blockIndex}` → the tool_use id opened by
 *     content_block_start at that index, so input_json_delta events (which carry
 *     only the block index) can be attributed to the right tool card (D6). Keyed
 *     with the parent id because a subagent's stream and the main thread's
 *     interleave with independent content-block index spaces.
 *
 *   names — tool_use id → tool name. The id-only tool_result (mapped to
 *     tool.completed/failed by handleUserMessage) and tool.delta carry no name of
 *     their own, but ToolPayload.tool is schema-required (D8); this lets those
 *     emitters recover it. Populated at every tool_use content_block_start AND
 *     full-message tool_use, both of which precede the tool_result, so the name is
 *     available whether or not partial-message streaming is on.
 */
export interface StreamToolIndex {
  byIndex: Map<string, string>;
  names: Map<string, string>;
}

/** Fresh per-turn StreamToolIndex (block indexes + ids restart with each query()). */
export function newStreamToolIndex(): StreamToolIndex {
  return { byIndex: new Map(), names: new Map() };
}

function streamToolKey(parentToolUseId: string | undefined, index: number): string {
  return `${parentToolUseId ?? ''}:${index}`;
}

/** Resolve the schema-required tool name for an id-only tool event (D8): the
 * per-turn names map, else '' when the id is unknown (no streamTools threaded, or
 * a tool_result with no matching tool_use — the pre-D8 fallback). */
function toolNameFor(streamTools: StreamToolIndex | undefined, toolUseId: string | undefined): string {
  return (toolUseId && streamTools?.names.get(toolUseId)) || '';
}

/**
 * Map a single SDKMessage to normalized events via `emit`. Pure aside from the
 * emit callback: no I/O, no registry access, no sqlite. Returns the registry
 * observations the caller must apply (model, claude session id, init/completed).
 * streamTools carries the per-turn content-block→tool_use-id attribution (D6);
 * omitting it degrades tool.delta to id-less (pre-D6) behavior.
 */
export function mapMessage(msg: SDKMessage, emit: EmitFn, streamTools?: StreamToolIndex): MapResult {
  switch (msg.type) {
    case 'system': {
      if (msg.subtype === 'init') {
        emit('session.started', {
          model: msg.model,
          cwd: msg.cwd,
          tools: msg.tools,
          permissionMode: msg.permissionMode,
          claudeSessionId: msg.session_id,
        });
        return { model: msg.model, claudeSessionId: msg.session_id, isInit: true };
      }
      if (msg.subtype === 'compact_boundary') {
        // The SDK compacted (summarized) the conversation to fit the context
        // window. Surface it as a normalized event so the TUI can reset the ctx%
        // gauge to the post-compaction size and mark scrollback — previously this
        // system message was dropped and ctx% stayed stale (schema §2b gap 4).
        const meta = msg.compact_metadata;
        emit('context.compacted', {
          trigger: meta.trigger,
          preTokens: meta.pre_tokens,
          postTokens: meta.post_tokens ?? 0,
        });
        return {};
      }
      return {};
    }
    case 'assistant': {
      handleAssistantMessage(msg, emit, streamTools);
      emitUsage(msg.message.usage, emit);
      return {};
    }
    case 'user': {
      handleUserMessage(msg, emit, streamTools);
      return {};
    }
    case 'stream_event': {
      handleStreamEvent(
        msg.event,
        emit,
        (msg as { parent_tool_use_id?: string | null }).parent_tool_use_id ?? undefined,
        streamTools,
      );
      return {};
    }
    case 'result': {
      return handleResultMessage(msg, emit);
    }
    default:
      // Other SDK message types (tool_progress, status, api_retry, etc.) are
      // not part of the normalized event model; ignore them.
      return {};
  }
}

// --- TodoWrite → todo.updated --------------------------------------------

/**
 * Map the SDK TodoWrite tool input to a todo.updated payload. The SDK shape is
 * { todos: { content, status, activeForm }[] } with status pending|in_progress|
 * completed. Returns undefined when the input has no usable todo array so the
 * caller emits nothing.
 */
export function todoUpdatedPayload(input: unknown): { todos: TodoItem[] } | undefined {
  const raw = (input as { todos?: unknown })?.todos;
  if (!Array.isArray(raw)) return undefined;
  const todos: TodoItem[] = raw.map((t) => {
    const item = t as { content?: unknown; status?: unknown; activeForm?: unknown };
    const todo: TodoItem = {
      content: typeof item.content === 'string' ? item.content : '',
      status: typeof item.status === 'string' ? item.status : '',
    };
    if (typeof item.activeForm === 'string') todo.activeForm = item.activeForm;
    return todo;
  });
  return { todos };
}

// --- Assistant / user / stream / result handlers -------------------------

function handleAssistantMessage(
  msg: Extract<SDKMessage, { type: 'assistant' }>,
  emit: EmitFn,
  streamTools?: StreamToolIndex,
): void {
  const parentToolUseId =
    (msg as { parent_tool_use_id?: string | null }).parent_tool_use_id ?? undefined;
  for (const block of msg.message.content as BetaContentBlock[]) {
    switch (block.type) {
      case 'text': {
        // §2b gap 1: parentToolUseId rides every message.* / reasoning.* emit
        // (not just tool.*) so a subagent's narration is routable to its Task
        // card instead of interleaving into the main streaming reply.
        // JSON.stringify drops the key when undefined (main thread).
        emit('message.started', { role: 'assistant', content: '', parentToolUseId });
        emit('message.completed', {
          role: 'assistant',
          content: (block as BetaTextBlock).text,
          parentToolUseId,
        });
        break;
      }
      case 'thinking': {
        const tb = block as BetaThinkingBlock;
        emit('reasoning.started', { parentToolUseId });
        emit('reasoning.completed', { content: tb.thinking, parentToolUseId });
        break;
      }
      case 'tool_use': {
        const tu = block as BetaToolUseBlock;
        // D8: remember id→name so the later id-only tool_result can carry `tool`.
        streamTools?.names.set(tu.id, tu.name);
        emit('tool.started', {
          tool: tu.name,
          input: tu.input,
          toolUseId: tu.id,
          parentToolUseId,
          agentName:
            tu.name === 'Task'
              ? (tu.input as { subagent_type?: string } | undefined)?.subagent_type
              : undefined,
        });
        // The TodoWrite tool carries the agent's task list; surface it as a
        // todo.updated event the TUI renders as a checklist (in addition to the
        // generic tool.started above, which still records the raw tool call).
        if (tu.name === 'TodoWrite') {
          const payload = todoUpdatedPayload(tu.input);
          if (payload) emit('todo.updated', payload);
        }
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
  emit: EmitFn,
  streamTools?: StreamToolIndex,
): void {
  const content = msg.message.content;
  const parentToolUseId =
    (msg as { parent_tool_use_id?: string | null }).parent_tool_use_id ?? undefined;
  // MessageParam.content is string | array of block params. Tool results are
  // { type: 'tool_result', tool_use_id, content, is_error } blocks.
  if (!Array.isArray(content)) {
    const text = typeof content === 'string' ? content : '';
    // §2b gap 1: a parented user message is subagent-internal (e.g. the Task
    // prompt injection) — stamp the parent id so clients keep it off the main
    // transcript's user blocks.
    emit('message.started', { role: 'user', content: text, parentToolUseId });
    emit('message.completed', { role: 'user', content: text, parentToolUseId });
    return;
  }
  for (const block of content as Array<{
    type: string;
    tool_use_id?: string;
    content?: unknown;
    is_error?: boolean;
  }>) {
    if (block.type === 'tool_result') {
      const outputStr = capToolOutput(
        typeof block.content === 'string'
          ? block.content
          : Array.isArray(block.content)
            ? (block.content as Array<{ text?: string }>).map((c) => c?.text ?? '').join('')
            : '',
      );
      // D8: a tool_result carries only tool_use_id, but ToolPayload.tool is
      // schema-required — recover the name captured on the matching tool.started.
      const tool = toolNameFor(streamTools, block.tool_use_id);
      if (block.is_error) {
        // Populate `error` (not just `output`) so the documented
        // ToolPayload.Error field carries the failure reason. A consumer that
        // renders only `error` (the Go TUI) would otherwise show a bare "✗"
        // with no message for every SDK-path tool failure.
        emit('tool.failed', {
          tool,
          output: outputStr,
          error: outputStr,
          toolUseId: block.tool_use_id,
          parentToolUseId,
        });
      } else {
        emit('tool.completed', {
          tool,
          output: outputStr,
          toolUseId: block.tool_use_id,
          parentToolUseId,
        });
      }
    }
  }
}

function handleStreamEvent(
  event: BetaRawContentBlockStartEvent | BetaRawContentBlockDeltaEvent | { type: string },
  emit: EmitFn,
  parentToolUseId?: string,
  streamTools?: StreamToolIndex,
): void {
  switch (event.type) {
    case 'content_block_start': {
      const e = event as BetaRawContentBlockStartEvent;
      const block = e.content_block;
      // Track (parent, index) → tool_use id for D6; overwrite/clear on every
      // block start so a later message reusing the index can't inherit a stale
      // id (indexes restart per assistant message within the stream). names is
      // keyed by id (not index), so it is NOT cleared on index reuse — a
      // tool_result for an already-finished tool still needs its name (D8).
      if (block.type === 'tool_use') {
        const tu = block as BetaToolUseBlock;
        streamTools?.byIndex.set(streamToolKey(parentToolUseId, e.index), tu.id);
        streamTools?.names.set(tu.id, tu.name); // D8: id→name for the later tool_result
      } else {
        streamTools?.byIndex.delete(streamToolKey(parentToolUseId, e.index));
      }
      switch (block.type) {
        case 'text':
          // §2b gap 1: parented starts/deltas are routable to the Task card.
          emit('message.started', { role: 'assistant', content: '', parentToolUseId });
          break;
        case 'thinking':
          emit('reasoning.started', { parentToolUseId });
          break;
        case 'tool_use':
          // NOTE: this streaming tool.started is intentionally lighter than the
          // full-message tool.started in handleAssistantMessage — it omits
          // agentName because the subagent_type is not reliably present until
          // the tool input has fully streamed. The full-message tool.started is
          // the authoritative source for agentName.
          emit('tool.started', {
            tool: (block as BetaToolUseBlock).name,
            input: (block as BetaToolUseBlock).input,
            toolUseId: (block as BetaToolUseBlock).id,
            parentToolUseId,
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
          // §2b gap 1: a subagent's narration deltas carry their Task id so
          // the client never streams them into the main reply buffer.
          emit('message.delta', { role: 'assistant', content: delta.text, delta: true, parentToolUseId });
          break;
        case 'thinking_delta':
          emit('reasoning.delta', { content: delta.thinking, delta: true, parentToolUseId });
          break;
        case 'input_json_delta': {
          // D6: attribute the streamed input to its tool_use block so the TUI
          // can target the exact card instead of guessing "newest pending" —
          // a subagent's streaming input otherwise animates onto a main-thread
          // card's argument.
          const toolUseId = streamTools?.byIndex.get(streamToolKey(parentToolUseId, e.index));
          emit('tool.delta', {
            // D8: ToolPayload.tool is schema-required; recover it from the id.
            tool: toolNameFor(streamTools, toolUseId),
            partialJson: delta.partial_json,
            delta: true,
            toolUseId,
            parentToolUseId,
          });
          break;
        }
        default:
          break;
      }
      break;
    }
    // content_block_stop / message_start / message_delta / message_stop are
    // reflected in the full assistant/user/result messages; nothing to emit.
    default:
      break;
  }
}

function handleResultMessage(
  msg: Extract<SDKMessage, { type: 'result' }>,
  emit: EmitFn,
): MapResult {
  if (msg.subtype === 'success') {
    emit('turn.completed', {
      result: msg.result,
      stopReason: msg.stop_reason,
      numTurns: msg.num_turns,
      durationMs: msg.duration_ms,
    });
    // D12: exactly ONE usage.updated for the terminal result, carrying the real
    // cost. Previously emitUsage() stamped totalCostUsd:0 and THEN this re-emitted
    // with the cost — two back-to-back usage rows for every successful turn.
    emitResultUsage(msg.usage, msg.total_cost_usd, emit);
    return { completed: true };
  }
  emit('turn.failed', {
    // Always include `message` so consumers (the Go TUI decodes ErrorPayload by
    // `message` only) render the reason instead of a blank "✗". subtype + errors
    // are kept for richer consumers.
    message: msg.errors.join('; ') || `turn failed: ${msg.subtype}`,
    subtype: msg.subtype,
    errors: msg.errors,
  });
  emit('error', {
    message: msg.errors.join('; ') || `turn failed: ${msg.subtype}`,
    code: msg.subtype,
  });
  // D12: a failed turn was still billed — emit its real cost. emitUsage would
  // stamp totalCostUsd:0, silently dropping the cost the provider charged.
  emitResultUsage(msg.usage, msg.total_cost_usd, emit);
  return {};
}

/** Emit the single terminal usage.updated for a result message, carrying the
 * real total cost (D12). Shared by the success and failure branches so both
 * report cost and neither double-emits. */
function emitResultUsage(
  usage: {
    input_tokens: number;
    output_tokens: number;
    cache_read_input_tokens?: number | null;
    cache_creation_input_tokens?: number | null;
  },
  totalCostUsd: number | undefined,
  emit: EmitFn,
): void {
  emit('usage.updated', {
    inputTokens: usage.input_tokens,
    outputTokens: usage.output_tokens,
    cacheReadTokens: usage.cache_read_input_tokens ?? 0,
    cacheWriteTokens: usage.cache_creation_input_tokens ?? 0,
    totalCostUsd: totalCostUsd ?? 0,
  });
}

function emitUsage(
  usage:
    | {
        input_tokens: number;
        output_tokens: number;
        cache_read_input_tokens?: number | null;
        cache_creation_input_tokens?: number | null;
      }
    | undefined,
  emit: EmitFn,
): void {
  if (!usage) return;
  emit('usage.updated', {
    inputTokens: usage.input_tokens,
    outputTokens: usage.output_tokens,
    cacheReadTokens: usage.cache_read_input_tokens ?? 0,
    cacheWriteTokens: usage.cache_creation_input_tokens ?? 0,
    totalCostUsd: 0,
  });
}

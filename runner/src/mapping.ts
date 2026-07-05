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
 * Map a single SDKMessage to normalized events via `emit`. Pure aside from the
 * emit callback: no I/O, no registry access, no sqlite. Returns the registry
 * observations the caller must apply (model, claude session id, init/completed).
 */
export function mapMessage(msg: SDKMessage, emit: EmitFn): MapResult {
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
      handleAssistantMessage(msg, emit);
      emitUsage(msg.message.usage, emit);
      return {};
    }
    case 'user': {
      handleUserMessage(msg, emit);
      return {};
    }
    case 'stream_event': {
      handleStreamEvent(
        msg.event,
        emit,
        (msg as { parent_tool_use_id?: string | null }).parent_tool_use_id ?? undefined,
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
): void {
  const parentToolUseId =
    (msg as { parent_tool_use_id?: string | null }).parent_tool_use_id ?? undefined;
  for (const block of msg.message.content as BetaContentBlock[]) {
    switch (block.type) {
      case 'text': {
        emit('message.started', { role: 'assistant', content: '' });
        emit('message.completed', { role: 'assistant', content: (block as BetaTextBlock).text });
        break;
      }
      case 'thinking': {
        const tb = block as BetaThinkingBlock;
        emit('reasoning.started', {});
        emit('reasoning.completed', { content: tb.thinking });
        break;
      }
      case 'tool_use': {
        const tu = block as BetaToolUseBlock;
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
): void {
  const content = msg.message.content;
  const parentToolUseId =
    (msg as { parent_tool_use_id?: string | null }).parent_tool_use_id ?? undefined;
  // MessageParam.content is string | array of block params. Tool results are
  // { type: 'tool_result', tool_use_id, content, is_error } blocks.
  if (!Array.isArray(content)) {
    const text = typeof content === 'string' ? content : '';
    emit('message.started', { role: 'user', content: text });
    emit('message.completed', { role: 'user', content: text });
    return;
  }
  for (const block of content as Array<{
    type: string;
    tool_use_id?: string;
    content?: unknown;
    is_error?: boolean;
  }>) {
    if (block.type === 'tool_result') {
      const outputStr =
        typeof block.content === 'string'
          ? block.content
          : Array.isArray(block.content)
            ? (block.content as Array<{ text?: string }>).map((c) => c?.text ?? '').join('')
            : '';
      if (block.is_error) {
        // Populate `error` (not just `output`) so the documented
        // ToolPayload.Error field carries the failure reason. A consumer that
        // renders only `error` (the Go TUI) would otherwise show a bare "✗"
        // with no message for every SDK-path tool failure.
        emit('tool.failed', {
          output: outputStr,
          error: outputStr,
          toolUseId: block.tool_use_id,
          parentToolUseId,
        });
      } else {
        emit('tool.completed', {
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
): void {
  switch (event.type) {
    case 'content_block_start': {
      const e = event as BetaRawContentBlockStartEvent;
      const block = e.content_block;
      switch (block.type) {
        case 'text':
          emit('message.started', { role: 'assistant', content: '' });
          break;
        case 'thinking':
          emit('reasoning.started', {});
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
          emit('message.delta', { role: 'assistant', content: delta.text, delta: true });
          break;
        case 'thinking_delta':
          emit('reasoning.delta', { content: delta.thinking, delta: true });
          break;
        case 'input_json_delta':
          emit('tool.delta', { partialJson: delta.partial_json, delta: true });
          break;
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
    emitUsage(msg.usage, emit);
    emit('usage.updated', {
      inputTokens: msg.usage.input_tokens,
      outputTokens: msg.usage.output_tokens,
      cacheReadTokens: msg.usage.cache_read_input_tokens ?? 0,
      cacheWriteTokens: msg.usage.cache_creation_input_tokens ?? 0,
      totalCostUsd: msg.total_cost_usd,
    });
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
  emitUsage(msg.usage, emit);
  return {};
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

// Passive observer for claude-pane sessions (tasks 3.3 + 3.4).
//
// The interactive `claude` child owns its own turn and approval UX; the runner
// never drives it. What the dashboard needs is the same normalized event feed
// every other backend gets — turn boundaries, streaming assistant text, tool
// one-liners, permission attention, usage/cost/rate-limit metrics — so this
// observer ingests Claude Code's OWN telemetry surfaces and translates them:
//
//   settings command hooks   → POST /observer/claude/hook       (stdin JSON)
//   statusline command       → POST /observer/claude/statusline (stdin JSON)
//   supervisor child exit    → handleChildExit (process-level: Stop/SessionEnd
//                              hooks are GRACEFUL-ONLY and never fire on a
//                              crash — verified empirically 2026-07-20)
//
// Mapping (hook_event_name → normalized events; sequential single-user turns):
//
//   UserPromptSubmit  → turn.started {prompt} (+ status busy, last_turn_id)
//   MessageDisplay    → message.started (first delta) + message.delta
//   PreToolUse        → tool.started (+ audit; resolves a pending permission)
//   PostToolUse       → tool.completed
//   PermissionRequest → permission.requested (→ dashboard waiting/attention)
//   Stop              → message.completed + turn.completed {result} (+ idle)
//   SessionEnd        → turn.interrupted for an open turn (+ idle)
//   child exit        → turn.interrupted for an open turn (+ idle)
//
// Notification is deliberately NOT mapped: PermissionRequest fires reliably on
// real permission prompts (verified), and synthesizing a permission event from
// a notification would fabricate the required `tool` field.
//
// The hook helper scripts run INSIDE the pane child's env (hooks inherit it),
// which is scrubbed of every secret — so ingestion cannot authenticate with the
// runner bearer token from env. Instead provisionPaneObserver mints a random
// observer token, persists it next to the helper scripts (0600, PVC), and the
// server accepts it (or the runner token) exclusively for the two observer
// routes. Helper scripts are Node (the image ships node; curl/jq are not
// guaranteed), never print to stdout on the hook path (hook stdout can be
// injected into claude's context), always exit 0, and time out fast so a wedged
// runner can never block the interactive session.

import { randomBytes } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { appendAudit } from './audit.js';
import { appendEvent } from './events.js';
import { getRegistry } from './session.js';
import { CLAUDE_CONFIG_DIR, PORT, type EventType } from './types.js';

/** Truncation bound for tool output summaries in tool.completed events. The
 * feed renders one line; the pane shows full output. */
const TOOL_OUTPUT_MAX = 2048;

// --- Deps seam (mirrors opencode-observer's ObserverDeps) -------------------

export interface PaneObserverDeps {
  nextTurnId(): string;
  setLastTurn(id: string): void;
  setStatus(s: 'busy' | 'idle' | 'error'): void;
  setModel(model: string): void;
  /** Refresh the synthetic-busy staleness clock for every observed event. */
  noteObserverEvent?(): void;
  emit(turnId: string | undefined, type: EventType, payload: Record<string, unknown>): void;
  audit(turnId: string, tool: string, input: unknown): void;
}

export interface PaneObserverCore {
  handleHook(payload: Record<string, unknown>): void;
  handleStatusline(payload: Record<string, unknown>): void;
  handleChildExit(info: { code: number | null; signal: number | null }): void;
  /** Abandon any open synthetic turn (runner shutdown path). */
  reset(reason: string): void;
}

/** Narrow accessor helpers — hook payloads are externally produced JSON. */
function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}
function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

/** Summarize a PostToolUse tool_response into a bounded display string. */
export function summarizeToolResponse(resp: unknown): string {
  let s: string;
  if (typeof resp === 'string') {
    s = resp;
  } else if (resp && typeof resp === 'object') {
    const r = resp as Record<string, unknown>;
    // Bash-shaped responses carry stdout/stderr; prefer them over raw JSON.
    const stdout = str(r.stdout);
    const stderr = str(r.stderr);
    s = stdout || stderr ? [stdout, stderr].filter(Boolean).join('\n') : JSON.stringify(resp);
  } else {
    s = String(resp ?? '');
  }
  return s.length > TOOL_OUTPUT_MAX ? s.slice(0, TOOL_OUTPUT_MAX) + ' …[truncated]' : s;
}

export function createPaneObserverCore(deps: PaneObserverDeps): PaneObserverCore {
  // The open synthetic turn, or null between turns. Turns are strictly
  // sequential (one human, one pane), so a single slot suffices; hook events
  // between turn.started and Stop all attach to it regardless of their own ids.
  let turnId: string | null = null;
  // Whether message.started has been emitted for the current turn's reply.
  let messageOpen = false;
  // Assistant text accumulated from MessageDisplay deltas — the fallback final
  // text when Stop carries no last_assistant_message.
  let accumulated = '';
  // The pending permission.requested id, resolved on the next observed
  // activity that proves the in-pane prompt was answered.
  let pendingPermission: { id: string; tool: string; input: unknown } | null = null;
  let permSeq = 0;
  // Consecutive-duplicate suppression for the chatty statusline emissions
  // (statusline fires several times per turn with mostly-identical data; the
  // registry setModel persists session.json, so it is deduped too).
  let lastUsageJson = '';
  let lastRateLimitJson = '';
  let lastTitle = '';
  let lastModel = '';

  function closeTurn(kind: 'completed' | 'interrupted', payload: Record<string, unknown>): void {
    if (turnId === null) return;
    deps.emit(turnId, kind === 'completed' ? 'turn.completed' : 'turn.interrupted', payload);
    turnId = null;
    messageOpen = false;
    accumulated = '';
    pendingPermission = null;
    deps.setStatus('idle');
  }

  /** The in-pane prompt was answered (a tool ran / the turn ended): emit the
   * resolution so the dashboard clears waiting/attention. The actual decision
   * is claude's own; "allow-once" is the closest honest mapping for a prompt
   * that visibly proceeded (PermissionDenied would map to deny, if observed). */
  function resolvePendingPermission(decision: 'allow-once' | 'deny'): void {
    if (!pendingPermission) return;
    deps.emit(turnId ?? undefined, 'permission.resolved', {
      permissionId: pendingPermission.id,
      tool: pendingPermission.tool,
      input: pendingPermission.input ?? {},
      decision,
    });
    pendingPermission = null;
  }

  function handleHook(payload: Record<string, unknown>): void {
    deps.noteObserverEvent?.();
    const event = str(payload.hook_event_name);
    switch (event) {
      case 'UserPromptSubmit': {
        // A prompt while a turn is open means we missed its terminal (lost
        // hook, crashed helper): close it as interrupted so the log never
        // wedges busy, then open the new turn.
        closeTurn('interrupted', { reason: 'superseded by a new prompt' });
        turnId = deps.nextTurnId();
        deps.setLastTurn(turnId);
        deps.setStatus('busy');
        deps.emit(turnId, 'turn.started', { prompt: str(payload.prompt) });
        break;
      }
      case 'MessageDisplay': {
        if (turnId === null) break; // boot noise outside a turn
        const delta = str(payload.delta);
        if (delta === '') break;
        if (!messageOpen) {
          deps.emit(turnId, 'message.started', { role: 'assistant', content: '' });
          messageOpen = true;
        }
        accumulated += delta;
        deps.emit(turnId, 'message.delta', { role: 'assistant', content: delta, delta: true });
        break;
      }
      case 'PreToolUse': {
        resolvePendingPermission('allow-once');
        if (turnId === null) break;
        const tool = str(payload.tool_name);
        deps.emit(turnId, 'tool.started', {
          tool,
          input: payload.tool_input ?? {},
          toolUseId: str(payload.tool_use_id),
        });
        deps.audit(turnId, tool, payload.tool_input);
        break;
      }
      case 'PostToolUse': {
        resolvePendingPermission('allow-once');
        if (turnId === null) break;
        const durationMs = num(payload.duration_ms);
        deps.emit(turnId, 'tool.completed', {
          tool: str(payload.tool_name),
          toolUseId: str(payload.tool_use_id),
          output: summarizeToolResponse(payload.tool_response),
          ...(durationMs > 0 ? { elapsedSeconds: durationMs / 1000 } : {}),
        });
        break;
      }
      case 'PermissionRequest': {
        const id = `pane-perm-${++permSeq}`;
        pendingPermission = { id, tool: str(payload.tool_name), input: payload.tool_input };
        deps.emit(turnId ?? undefined, 'permission.requested', {
          permissionId: id,
          tool: str(payload.tool_name),
          input: payload.tool_input ?? {},
        });
        break;
      }
      case 'PermissionDenied': {
        resolvePendingPermission('deny');
        break;
      }
      case 'Stop': {
        resolvePendingPermission('allow-once');
        if (turnId === null) break;
        const finalText = str(payload.last_assistant_message) || accumulated;
        if (finalText !== '') {
          deps.emit(turnId, 'message.completed', { role: 'assistant', content: finalText });
        }
        messageOpen = false;
        closeTurn('completed', finalText !== '' ? { result: finalText } : {});
        break;
      }
      case 'SessionEnd': {
        closeTurn('interrupted', {
          reason: `claude session ended (${str(payload.reason) || 'unknown'})`,
        });
        break;
      }
      default:
        // SessionStart, InstructionsLoaded, PostToolBatch, SubagentStop, … —
        // observed (staleness clock above) but not mapped.
        break;
    }
  }

  function handleStatusline(payload: Record<string, unknown>): void {
    deps.noteObserverEvent?.();
    const model = str((payload.model as { id?: unknown } | undefined)?.id);
    if (model !== '' && model !== lastModel) {
      lastModel = model;
      deps.setModel(model);
    }

    const title = str(payload.session_name);
    if (title !== '' && title !== lastTitle) {
      lastTitle = title;
      deps.emit(undefined, 'session.title', { title });
    }

    const ctx = payload.context_window as
      | { current_usage?: Record<string, unknown> }
      | undefined;
    const cu = ctx?.current_usage;
    const cost = payload.cost as Record<string, unknown> | undefined;
    if (cu) {
      const usage = {
        inputTokens: num(cu.input_tokens),
        outputTokens: num(cu.output_tokens),
        cacheReadTokens: num(cu.cache_read_input_tokens),
        cacheWriteTokens: num(cu.cache_creation_input_tokens),
        totalCostUsd: num(cost?.total_cost_usd),
      };
      const key = JSON.stringify(usage);
      if (key !== lastUsageJson) {
        lastUsageJson = key;
        deps.emit(turnId ?? undefined, 'usage.updated', usage);
      }
    }

    const rl = payload.rate_limits as
      | Record<string, { used_percentage?: unknown; resets_at?: unknown } | undefined>
      | undefined;
    if (rl?.five_hour || rl?.seven_day) {
      const resetsAt = (w?: { resets_at?: unknown }): string =>
        num(w?.resets_at) > 0 ? new Date(num(w?.resets_at) * 1000).toISOString() : '';
      const limits = {
        available: true,
        fiveHourUtil: num(rl.five_hour?.used_percentage),
        ...(resetsAt(rl.five_hour) !== '' ? { fiveHourResetsAt: resetsAt(rl.five_hour) } : {}),
        sevenDayUtil: num(rl.seven_day?.used_percentage),
        ...(resetsAt(rl.seven_day) !== '' ? { sevenDayResetsAt: resetsAt(rl.seven_day) } : {}),
      };
      const key = JSON.stringify(limits);
      if (key !== lastRateLimitJson) {
        lastRateLimitJson = key;
        deps.emit(undefined, 'rate_limit.updated', limits);
      }
    }
  }

  function handleChildExit(info: { code: number | null; signal: number | null }): void {
    deps.noteObserverEvent?.();
    closeTurn('interrupted', {
      reason: `pane process exited (code=${info.code ?? 'null'} signal=${info.signal ?? 'null'})`,
    });
  }

  return {
    handleHook,
    handleStatusline,
    handleChildExit,
    reset(reason: string): void {
      closeTurn('interrupted', { reason });
    },
  };
}

// --- Registry wiring --------------------------------------------------------

function registryDeps(): PaneObserverDeps {
  return {
    nextTurnId: () => getRegistry().nextTurnId(),
    setLastTurn: (id) => getRegistry().setLastTurn(id),
    setStatus: (s) => getRegistry().setStatus(s),
    setModel: (m) => getRegistry().setModel(m),
    noteObserverEvent: () => getRegistry().noteObserverEvent(),
    emit: (turnId, type, payload) =>
      appendEvent(getRegistry().state.sandbox_session_id, turnId, type, payload),
    audit: (turnId, tool, input) =>
      appendAudit({
        time: new Date().toISOString(),
        session_id: getRegistry().state.sandbox_session_id,
        turn_id: turnId,
        tool,
        input,
      }),
  };
}

/** Build the production observer core wired to the live session registry. */
export function startClaudePaneObserver(): PaneObserverCore {
  return createPaneObserverCore(registryDeps());
}

// --- Pod-side provisioning ---------------------------------------------------

/** Injectable fs surface (mirrors ClaudeConfigFs) for provisioning tests. */
export interface PaneObserverFs {
  readFileSync: typeof readFileSync;
  writeFileSync: typeof writeFileSync;
  mkdirSync: typeof mkdirSync;
}

const realFs: PaneObserverFs = { readFileSync, writeFileSync, mkdirSync };

/** Subdirectory of CLAUDE_CONFIG_DIR holding the helper scripts + token. */
export const OBSERVER_DIR = 'pane-observer';

const HOOK_SCRIPT = `#!/usr/bin/env node
// claude-pane observer hook forwarder. Reads the hook's stdin JSON and POSTs it
// to the in-pod runner. MUST stay silent on stdout (hook stdout can be injected
// into claude's context) and MUST always exit 0 fast — the observer is
// best-effort telemetry and may never block the interactive session.
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const chunks = [];
process.stdin.on('data', (c) => chunks.push(c));
process.stdin.on('end', () => {
  let token = '';
  try { token = readFileSync(join(__dirname, 'token'), 'utf8').trim(); } catch {}
  fetch(process.env.SANDBOX_OBSERVER_URL || 'http://127.0.0.1:PORT_PLACEHOLDER/observer/claude/hook', {
    method: 'POST',
    headers: { authorization: 'Bearer ' + token, 'content-type': 'application/json' },
    body: Buffer.concat(chunks),
    signal: AbortSignal.timeout(3000),
  }).catch(() => {}).finally(() => process.exit(0));
});
setTimeout(() => process.exit(0), 5000);
`;

const STATUSLINE_SCRIPT = `#!/usr/bin/env node
// claude-pane statusline: prints the in-pane status string AND forwards the
// stdin metrics JSON to the runner observer (ctx%, cost, rate limits). The
// print happens first so a slow runner never delays the statusline render.
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const chunks = [];
process.stdin.on('data', (c) => chunks.push(c));
process.stdin.on('end', () => {
  const raw = Buffer.concat(chunks);
  let line = 'claude';
  try {
    const j = JSON.parse(raw.toString('utf8'));
    const model = (j.model && j.model.display_name) || 'claude';
    const pct = j.context_window && j.context_window.used_percentage;
    line = typeof pct === 'number' ? model + ' · ctx ' + pct + '%' : model;
  } catch {}
  process.stdout.write(line);
  let token = '';
  try { token = readFileSync(join(__dirname, 'token'), 'utf8').trim(); } catch {}
  fetch(process.env.SANDBOX_OBSERVER_URL || 'http://127.0.0.1:PORT_PLACEHOLDER/observer/claude/statusline', {
    method: 'POST',
    headers: { authorization: 'Bearer ' + token, 'content-type': 'application/json' },
    body: raw,
    signal: AbortSignal.timeout(1500),
  }).catch(() => {}).finally(() => process.exit(0));
});
setTimeout(() => process.exit(0), 3000);
`;

/** The hook events the provisioned settings register. Tool-family events carry
 * a "*" matcher; the rest are plain entries. Must stay in sync with the
 * mapper's switch above. */
export const PROVISIONED_HOOK_EVENTS: ReadonlyArray<{ event: string; matcher: boolean }> = [
  { event: 'UserPromptSubmit', matcher: false },
  { event: 'PreToolUse', matcher: true },
  { event: 'PostToolUse', matcher: true },
  { event: 'PermissionRequest', matcher: false },
  { event: 'PermissionDenied', matcher: false },
  { event: 'MessageDisplay', matcher: false },
  { event: 'Stop', matcher: false },
  { event: 'SessionEnd', matcher: false },
];

export interface ProvisionResult {
  /** The minted (or previously persisted) observer bearer token. */
  token: string;
}

/**
 * Provision the observer surfaces into the claude config dir: the helper
 * scripts + token under pane-observer/, and a settings.json merge that
 * registers the hooks, the statusline command, and disables claude's native
 * command sandbox (the k8s pod IS the sandbox, and native-sandbox auto-approval
 * would suppress the permission hooks — verified gotcha). The merge preserves
 * every unrelated key and any user-added hook entries; our entries are
 * identified by command path and upserted idempotently.
 */
export function provisionPaneObserver(
  configDir: string = process.env.CLAUDE_CONFIG_DIR || CLAUDE_CONFIG_DIR,
  fs: PaneObserverFs = realFs,
): ProvisionResult {
  const dir = join(configDir, OBSERVER_DIR);
  fs.mkdirSync(dir, { recursive: true });

  // Token: reuse the persisted one so already-provisioned settings stay valid
  // across runner restarts; mint on first boot.
  const tokenPath = join(dir, 'token');
  let token = '';
  try {
    token = (fs.readFileSync(tokenPath, 'utf8') as string).trim();
  } catch {
    /* absent — mint below */
  }
  if (token === '') {
    token = randomBytes(24).toString('hex');
    fs.writeFileSync(tokenPath, token + '\n', { mode: 0o600 });
  }

  const hookPath = join(dir, 'hook.js');
  const statuslinePath = join(dir, 'statusline.js');
  fs.writeFileSync(hookPath, HOOK_SCRIPT.replaceAll('PORT_PLACEHOLDER', String(PORT)), {
    mode: 0o755,
  });
  fs.writeFileSync(
    statuslinePath,
    STATUSLINE_SCRIPT.replaceAll('PORT_PLACEHOLDER', String(PORT)),
    { mode: 0o755 },
  );

  mergeSettings(configDir, fs, hookPath, statuslinePath);
  return { token };
}

interface HookEntry {
  matcher?: string;
  hooks?: Array<{ type?: string; command?: string; timeout?: number }>;
}

function mergeSettings(
  configDir: string,
  fs: PaneObserverFs,
  hookPath: string,
  statuslinePath: string,
): void {
  const path = join(configDir, 'settings.json');
  let doc: Record<string, unknown> = {};
  try {
    doc = JSON.parse(fs.readFileSync(path, 'utf8') as string) as Record<string, unknown>;
  } catch {
    doc = {};
  }

  const hookCommand = `node ${hookPath}`;
  const statuslineCommand = `node ${statuslinePath}`;

  // Ours to own: the statusline (it doubles as the metrics tap) and the native
  // sandbox switch. User-synced config inputs live in skills/agents/commands
  // dirs, not these keys.
  doc.statusLine = { type: 'command', command: statuslineCommand, padding: 0 };
  const sandbox = (doc.sandbox as Record<string, unknown> | undefined) ?? {};
  sandbox.enabled = false;
  doc.sandbox = sandbox;

  const hooks = (doc.hooks as Record<string, HookEntry[]> | undefined) ?? {};
  for (const { event, matcher } of PROVISIONED_HOOK_EVENTS) {
    const entries = Array.isArray(hooks[event]) ? hooks[event] : [];
    const present = entries.some((e) =>
      (e.hooks ?? []).some((h) => h.command === hookCommand),
    );
    if (!present) {
      entries.push({
        ...(matcher ? { matcher: '*' } : {}),
        hooks: [{ type: 'command', command: hookCommand, timeout: 10 }],
      });
    }
    hooks[event] = entries;
  }
  doc.hooks = hooks;

  fs.writeFileSync(path, JSON.stringify(doc, null, 2), { mode: 0o600 });
}

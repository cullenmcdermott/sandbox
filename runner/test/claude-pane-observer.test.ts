// Unit tests for the claude-pane observer: hook→normalized-event mapping, the
// permission attention lifecycle, statusline metrics dedupe, crash terminals,
// and the settings/scripts provisioning merge. All through fakes — no server.

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import {
  createPaneObserverCore,
  provisionPaneObserver,
  summarizeToolResponse,
  PROVISIONED_HOOK_EVENTS,
  type PaneObserverDeps,
  type PaneObserverFs,
} from '../src/claude-pane-observer.js';
import type { EventType } from '../src/types.js';

interface Emitted {
  turnId: string | undefined;
  type: EventType;
  payload: Record<string, unknown>;
}

function fakeDeps() {
  const emitted: Emitted[] = [];
  const statuses: string[] = [];
  const audits: Array<{ turnId: string; tool: string }> = [];
  let models: string[] = [];
  let n = 0;
  const deps: PaneObserverDeps = {
    nextTurnId: () => `t-${++n}`,
    setLastTurn: () => undefined,
    setStatus: (s) => statuses.push(s),
    setModel: (m) => models.push(m),
    noteObserverEvent: () => undefined,
    emit: (turnId, type, payload) => emitted.push({ turnId, type, payload }),
    audit: (turnId, tool) => audits.push({ turnId, tool }),
  };
  return { deps, emitted, statuses, audits, models };
}

function types(emitted: Emitted[]): string[] {
  return emitted.map((e) => e.type);
}

test('full turn maps to started/streaming/tools/completed with busy→idle', () => {
  const { deps, emitted, statuses, audits } = fakeDeps();
  const core = createPaneObserverCore(deps);

  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'do the thing' });
  core.handleHook({ hook_event_name: 'MessageDisplay', delta: 'work', index: 0 });
  core.handleHook({
    hook_event_name: 'PreToolUse',
    tool_name: 'Bash',
    tool_input: { command: 'echo hi' },
    tool_use_id: 'tu-1',
  });
  core.handleHook({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'hi', stderr: '' },
    tool_use_id: 'tu-1',
    duration_ms: 120,
  });
  core.handleHook({ hook_event_name: 'MessageDisplay', delta: 'ing done', index: 1, final: true });
  core.handleHook({ hook_event_name: 'Stop', last_assistant_message: 'working done' });

  assert.deepEqual(types(emitted), [
    'turn.started',
    'message.started',
    'message.delta',
    'tool.started',
    'tool.completed',
    'message.delta',
    'message.completed',
    'turn.completed',
  ]);
  assert.equal(emitted[0].payload.prompt, 'do the thing');
  const sameTurn = new Set(emitted.map((e) => e.turnId));
  assert.deepEqual([...sameTurn], ['t-1']); // every event on the one synthetic turn
  assert.equal(emitted[4].payload.output, 'hi');
  assert.equal(emitted[4].payload.elapsedSeconds, 0.12);
  assert.equal(emitted[6].payload.content, 'working done'); // Stop text authoritative
  assert.equal(emitted[7].payload.result, 'working done');
  assert.deepEqual(statuses, ['busy', 'idle']);
  assert.deepEqual(audits, [{ turnId: 't-1', tool: 'Bash' }]);
});

test('permission request raises attention and resolves on next tool activity', () => {
  const { deps, emitted } = fakeDeps();
  const core = createPaneObserverCore(deps);

  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'p' });
  core.handleHook({
    hook_event_name: 'PermissionRequest',
    tool_name: 'Bash',
    tool_input: { command: 'rm x' },
  });
  core.handleHook({ hook_event_name: 'PreToolUse', tool_name: 'Bash', tool_input: {}, tool_use_id: 'tu' });

  const req = emitted.find((e) => e.type === 'permission.requested');
  const res = emitted.find((e) => e.type === 'permission.resolved');
  assert.ok(req && res);
  assert.equal(req.payload.permissionId, res.payload.permissionId);
  assert.equal(res.payload.decision, 'allow-once');
  // Resolution precedes the tool.started that proved it.
  assert.ok(types(emitted).indexOf('permission.resolved') < types(emitted).indexOf('tool.started'));
});

test('permission also resolves on Stop (prompt answered by ending the turn)', () => {
  const { deps, emitted } = fakeDeps();
  const core = createPaneObserverCore(deps);
  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'p' });
  core.handleHook({ hook_event_name: 'PermissionRequest', tool_name: 'Edit', tool_input: {} });
  core.handleHook({ hook_event_name: 'Stop', last_assistant_message: 'ok' });
  assert.ok(types(emitted).includes('permission.resolved'));
});

test('new prompt while a turn is open interrupts the stale turn first', () => {
  const { deps, emitted } = fakeDeps();
  const core = createPaneObserverCore(deps);
  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'one' });
  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'two' });
  assert.deepEqual(types(emitted), ['turn.started', 'turn.interrupted', 'turn.started']);
  assert.equal(emitted[1].turnId, 't-1');
  assert.equal(emitted[2].turnId, 't-2');
});

test('child exit mid-turn emits a synthetic interrupt; no-op when idle', () => {
  const { deps, emitted, statuses } = fakeDeps();
  const core = createPaneObserverCore(deps);
  core.handleChildExit({ code: 137, signal: 9 }); // idle: nothing to close
  assert.deepEqual(types(emitted), []);

  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'p' });
  core.handleChildExit({ code: 1, signal: null });
  assert.deepEqual(types(emitted), ['turn.started', 'turn.interrupted']);
  assert.match(String(emitted[1].payload.reason), /pane process exited/);
  assert.deepEqual(statuses, ['busy', 'idle']);
});

test('SessionEnd closes an open turn as interrupted', () => {
  const { deps, emitted } = fakeDeps();
  const core = createPaneObserverCore(deps);
  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'p' });
  core.handleHook({ hook_event_name: 'SessionEnd', reason: 'prompt_input_exit' });
  assert.deepEqual(types(emitted), ['turn.started', 'turn.interrupted']);
  assert.match(String(emitted[1].payload.reason), /prompt_input_exit/);
});

test('statusline maps usage/rate-limit/model/title with duplicate suppression', () => {
  const { deps, emitted, models } = fakeDeps();
  const core = createPaneObserverCore(deps);
  const payload = {
    session_name: 'Fix the bug',
    model: { id: 'claude-opus-4-8', display_name: 'Opus 4.8' },
    cost: { total_cost_usd: 0.5 },
    context_window: {
      used_percentage: 16,
      current_usage: {
        input_tokens: 10,
        output_tokens: 41,
        cache_creation_input_tokens: 31162,
        cache_read_input_tokens: 7,
      },
    },
    rate_limits: {
      five_hour: { used_percentage: 41, resets_at: 1784528400 },
      seven_day: { used_percentage: 4, resets_at: 1785103200 },
    },
  };
  core.handleStatusline(payload);
  core.handleStatusline(payload); // identical → suppressed
  assert.deepEqual(types(emitted), [
    'session.started',
    'session.title',
    'usage.updated',
    'rate_limit.updated',
  ]);
  assert.deepEqual(models, ['claude-opus-4-8']);
  // The model is ALSO emitted as session.started so the Go read-model resolves
  // Model + CtxLimit (ctx% in the pane status row) — opencode-observer parity.
  assert.deepEqual(emitted[0].payload, { model: 'claude-opus-4-8', cwd: '' });
  const usage = emitted[2].payload;
  assert.deepEqual(usage, {
    inputTokens: 10,
    outputTokens: 41,
    cacheReadTokens: 7,
    cacheWriteTokens: 31162,
    totalCostUsd: 0.5,
  });
  const rl = emitted[3].payload;
  assert.equal(rl.fiveHourUtil, 41);
  assert.equal(rl.sevenDayUtil, 4);
  assert.equal(rl.fiveHourResetsAt, new Date(1784528400 * 1000).toISOString());

  core.handleStatusline({ ...payload, cost: { total_cost_usd: 0.6 } });
  assert.equal(types(emitted).filter((t) => t === 'usage.updated').length, 2);
  // An unchanged model never re-emits session.started…
  assert.equal(types(emitted).filter((t) => t === 'session.started').length, 1);
  // …but an in-pane /model switch does (V45 parity: the dashboard chip/ctx%
  // must track the change, not stay latched to the first-observed model).
  core.handleStatusline({ ...payload, model: { id: 'claude-sonnet-5' } });
  const started = emitted.filter((e) => e.type === 'session.started');
  assert.equal(started.length, 2);
  assert.deepEqual(started[1].payload, { model: 'claude-sonnet-5', cwd: '' });
});

test('summarizeToolResponse prefers stdout/stderr and truncates', () => {
  assert.equal(summarizeToolResponse({ stdout: 'out', stderr: 'err' }), 'out\nerr');
  assert.equal(summarizeToolResponse('plain'), 'plain');
  assert.ok(summarizeToolResponse('x'.repeat(5000)).length < 3000);
  assert.ok(summarizeToolResponse('x'.repeat(5000)).endsWith('…[truncated]'));
});

// --- provisioning -----------------------------------------------------------

function memFs(initial: Record<string, string> = {}) {
  const files = new Map(Object.entries(initial));
  const modes = new Map<string, number | undefined>();
  const fs: PaneObserverFs = {
    readFileSync: ((path: string) => {
      if (!files.has(path)) throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' });
      return files.get(path)!;
    }) as PaneObserverFs['readFileSync'],
    writeFileSync: ((path: string, data: string, opts?: { mode?: number }) => {
      files.set(path, data);
      modes.set(path, opts?.mode);
    }) as PaneObserverFs['writeFileSync'],
    mkdirSync: (() => undefined) as unknown as PaneObserverFs['mkdirSync'],
  };
  return { fs, files, modes };
}

test('provisioning writes scripts + token and registers every hook event', () => {
  const { fs, files, modes } = memFs();
  const { token } = provisionPaneObserver('/cfg', fs);

  assert.ok(token.length >= 32);
  assert.equal(files.get('/cfg/pane-observer/token'), token + '\n');
  assert.equal(modes.get('/cfg/pane-observer/token'), 0o600);
  assert.ok(files.get('/cfg/pane-observer/hook.js')!.includes('/observer/claude/hook'));
  assert.ok(files.get('/cfg/pane-observer/statusline.js')!.includes('/observer/claude/statusline'));
  assert.ok(!files.get('/cfg/pane-observer/hook.js')!.includes('PORT_PLACEHOLDER'));

  const settings = JSON.parse(files.get('/cfg/settings.json')!);
  assert.equal(settings.sandbox.enabled, false);
  assert.match(settings.statusLine.command, /statusline\.js$/);
  for (const { event, matcher } of PROVISIONED_HOOK_EVENTS) {
    const entries = settings.hooks[event];
    assert.ok(Array.isArray(entries) && entries.length === 1, `missing hooks for ${event}`);
    assert.equal('matcher' in entries[0], matcher, `matcher presence for ${event}`);
  }
});

test('provisioning is idempotent and preserves user settings/hook entries', () => {
  const userSettings = JSON.stringify({
    theme: 'dark',
    hooks: {
      Stop: [{ hooks: [{ type: 'command', command: 'my-own-hook.sh' }] }],
    },
  });
  const { fs, files } = memFs({
    '/cfg/settings.json': userSettings,
    '/cfg/pane-observer/token': 'existing-token\n',
  });
  const { token } = provisionPaneObserver('/cfg', fs);
  assert.equal(token, 'existing-token'); // persisted token reused

  const first = JSON.parse(files.get('/cfg/settings.json')!);
  assert.equal(first.theme, 'dark'); // unrelated keys preserved
  assert.equal(first.hooks.Stop.length, 2); // user entry + ours
  assert.equal(first.hooks.Stop[0].hooks[0].command, 'my-own-hook.sh');

  provisionPaneObserver('/cfg', fs); // second run: no duplicate entries
  const second = JSON.parse(files.get('/cfg/settings.json')!);
  assert.equal(second.hooks.Stop.length, 2);
  assert.equal(second.hooks.UserPromptSubmit.length, 1);
});

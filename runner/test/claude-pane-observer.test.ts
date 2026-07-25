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
// The bypassPermissions assertions below read the mode back through the pane's
// own resolver, which lives in claude-pane.ts (the observer only WRITES the
// key). Missing here until 50e568e, which made both of those tests throw
// ReferenceError instead of asserting — tsconfig only covered src/, so nothing
// caught the unbound identifier (closed by tsconfig.test.json).
import { claudePaneArgs, paneDefaultPermissionMode } from '../src/claude-pane.js';
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
  const models: string[] = [];
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
  // deepEqual, not a subset match: this fixture reports no context_window_size,
  // so contextLimitTokens must be ABSENT rather than 0 — a 0 would be read as a
  // real denominator downstream instead of "fall back to the model's window".
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

// The statusline is the ONLY place the real context window is observable: the
// Go side otherwise infers it from the model id, which overstated Claude's
// window 5× (models.dev says 1M, Claude Code runs 200k) and made ctx% read 20%
// on a session whose own in-pane statusline said 100%.
test('statusline forwards the agent-reported context window as the ctx% denominator', () => {
  const { deps, emitted } = fakeDeps();
  const core = createPaneObserverCore(deps);
  const base = {
    model: { id: 'claude-opus-4-8' },
    context_window: {
      context_window_size: 200000,
      current_usage: { input_tokens: 1, output_tokens: 2 },
    },
  };
  core.handleStatusline(base);
  const usage = emitted.find((e) => e.type === 'usage.updated');
  assert.equal(usage?.payload.contextLimitTokens, 200000);

  // `size` is accepted as a rename hedge, but never over the documented field.
  const { deps: d2, emitted: e2 } = fakeDeps();
  const c2 = createPaneObserverCore(d2);
  c2.handleStatusline({
    context_window: { size: 123456, current_usage: { input_tokens: 1 } },
  });
  assert.equal(e2.find((e) => e.type === 'usage.updated')?.payload.contextLimitTokens, 123456);

  const { deps: d3, emitted: e3 } = fakeDeps();
  const c3 = createPaneObserverCore(d3);
  c3.handleStatusline({
    context_window: {
      context_window_size: 200000,
      size: 999,
      current_usage: { input_tokens: 1 },
    },
  });
  assert.equal(e3.find((e) => e.type === 'usage.updated')?.payload.contextLimitTokens, 200000);

  // A non-numeric window is not a window: omit it so the model-derived fallback
  // stays in play. Pinning ctx% to 0 would divide by nothing.
  const { deps: d4, emitted: e4 } = fakeDeps();
  const c4 = createPaneObserverCore(d4);
  c4.handleStatusline({
    context_window: { context_window_size: 'lots', current_usage: { input_tokens: 1 } },
  });
  const u4 = e4.find((e) => e.type === 'usage.updated')?.payload;
  assert.equal('contextLimitTokens' in (u4 as object), false);
});

// --- provisional busy confirm window (L8a) ----------------------------------
//
// UserPromptSubmit fires for EVERY submission — slash/local commands and
// prompts interrupted before the model starts included — and those never
// produce a Stop, so the busy it sets is provisional: without model activity
// inside the confirm window the observer must revert to idle (via setStatus,
// which is what emits session.status_changed in production). The window is
// injected tiny via deps.busyConfirmWindowMs so no test sleeps the real ~10s.

const CONFIRM_MS = 20;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
/** Sleep comfortably past the injected confirm window. */
const sleepPastWindow = () => sleep(CONFIRM_MS * 5);

test('UserPromptSubmit alone reverts to idle after the confirm window (L8a)', async () => {
  const { deps, emitted, statuses } = fakeDeps();
  const core = createPaneObserverCore({ ...deps, busyConfirmWindowMs: CONFIRM_MS });

  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: '/help' });
  assert.deepEqual(statuses, ['busy'], 'busy is set immediately (provisionally)');

  await sleepPastWindow();
  assert.deepEqual(statuses, ['busy', 'idle'], 'no model activity → reverted to idle');
  // Status-only revert: the synthetic turn stays open (no terminal event); the
  // NEXT submission interrupts it exactly like any lost-Stop turn.
  assert.deepEqual(types(emitted), ['turn.started']);
  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'real prompt' });
  assert.deepEqual(types(emitted), ['turn.started', 'turn.interrupted', 'turn.started']);
});

test('UserPromptSubmit + message activity within the window stays busy (L8a)', async () => {
  const { deps, statuses } = fakeDeps();
  const core = createPaneObserverCore({ ...deps, busyConfirmWindowMs: CONFIRM_MS });
  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'do it' });
  core.handleHook({ hook_event_name: 'MessageDisplay', delta: 'wor', index: 0 });
  await sleepPastWindow();
  assert.deepEqual(statuses, ['busy'], 'model activity confirmed the busy — no revert');
});

test('PreToolUse / PostToolUse / PermissionRequest also confirm the busy (L8a)', async () => {
  const activities: Array<Record<string, unknown>> = [
    { hook_event_name: 'PreToolUse', tool_name: 'Bash', tool_input: {}, tool_use_id: 'tu' },
    { hook_event_name: 'PostToolUse', tool_name: 'Bash', tool_response: 'ok', tool_use_id: 'tu' },
    { hook_event_name: 'PermissionRequest', tool_name: 'Bash', tool_input: {} },
  ];
  for (const activity of activities) {
    const { deps, statuses } = fakeDeps();
    const core = createPaneObserverCore({ ...deps, busyConfirmWindowMs: CONFIRM_MS });
    core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'p' });
    core.handleHook(activity);
    await sleepPastWindow();
    assert.deepEqual(statuses, ['busy'], `${String(activity.hook_event_name)} must cancel the pending revert`);
  }
});

test('a turn that ends inside the window cancels the pending revert (L8a)', async () => {
  const { deps, statuses } = fakeDeps();
  const core = createPaneObserverCore({ ...deps, busyConfirmWindowMs: CONFIRM_MS });
  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'p' });
  core.handleHook({ hook_event_name: 'Stop', last_assistant_message: 'fast answer' });
  assert.deepEqual(statuses, ['busy', 'idle']);
  await sleepPastWindow();
  assert.deepEqual(statuses, ['busy', 'idle'], 'no duplicate idle after the window');
});

test('late model activity after a provisional revert re-asserts busy (L8a)', async () => {
  const { deps, statuses } = fakeDeps();
  const core = createPaneObserverCore({ ...deps, busyConfirmWindowMs: CONFIRM_MS });
  core.handleHook({ hook_event_name: 'UserPromptSubmit', prompt: 'slow think' });
  await sleepPastWindow(); // window expires with no activity → reverted
  assert.deepEqual(statuses, ['busy', 'idle']);
  // First visible activity lands AFTER the window (long think / first-token
  // latency): the turn is real after all — put the session back to busy.
  core.handleHook({ hook_event_name: 'MessageDisplay', delta: 'finally', index: 0 });
  assert.deepEqual(statuses, ['busy', 'idle', 'busy']);
  core.handleHook({ hook_event_name: 'Stop', last_assistant_message: 'finally done' });
  assert.deepEqual(statuses, ['busy', 'idle', 'busy', 'idle']);
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

// Yolo default. The regression this guards is specific: every other piece of
// bypassPermissions plumbing (IS_SANDBOX=1, paneDefaultPermissionMode,
// claudePaneArgs' --permission-mode) shipped and was unit-tested while NOTHING
// wrote the key, so the pane silently prompted. Assert the produced settings
// actually drive the spawn, not just that a key exists.
test('provisioning seeds permissions.defaultMode=bypassPermissions (yolo by default)', () => {
  const { fs, files } = memFs();
  provisionPaneObserver('/cfg', fs);

  const settings = JSON.parse(files.get('/cfg/settings.json')!);
  assert.equal(settings.permissions.defaultMode, 'bypassPermissions');

  // End-to-end through the readers the supervisor actually uses: the seeded
  // file must yield --permission-mode on BOTH a fresh and a resume spawn.
  const read = (p: string) => files.get(p)!;
  const mode = paneDefaultPermissionMode({ CLAUDE_CONFIG_DIR: '/cfg' }, read);
  assert.equal(mode, 'bypassPermissions');
  assert.deepEqual(claudePaneArgs('u', false, mode), [
    '--session-id',
    'u',
    '--permission-mode',
    'bypassPermissions',
  ]);
  assert.deepEqual(claudePaneArgs('u', true, mode), [
    '--resume',
    'u',
    '--permission-mode',
    'bypassPermissions',
  ]);
});

// Seeded, not owned: the in-session escape hatch only works if a user's chosen
// mode survives re-provisioning (which runs on every runner boot).
test('an existing permissions.defaultMode is preserved, not clobbered', () => {
  const { fs, files } = memFs({
    '/cfg/settings.json': JSON.stringify({ permissions: { defaultMode: 'default', extra: 1 } }),
  });
  provisionPaneObserver('/cfg', fs);

  const settings = JSON.parse(files.get('/cfg/settings.json')!);
  assert.equal(settings.permissions.defaultMode, 'default');
  assert.equal(settings.permissions.extra, 1); // sibling keys untouched
  assert.equal(
    paneDefaultPermissionMode({ CLAUDE_CONFIG_DIR: '/cfg' }, (p: string) => files.get(p)!),
    'default',
  );
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

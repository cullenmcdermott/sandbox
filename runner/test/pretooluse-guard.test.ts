// F2 (CRITICAL test gap): pin the claude PreToolUse(Bash) hook — the primary
// enforcement path for the Bash blocklist (the SDK Bash tool is what agents use
// constantly). It was previously unexported and untested, so an SDK shape change
// to HookInput/tool_input, or a refactor dropping the bashCommandBlocked call,
// could silently disable Bash blocking for the claude backend with every other
// guard test still green. These tests pin — not change — the guard's behavior:
//
//   §1f: a blocked command returns the MODERN deny shape
//   (hookSpecificOutput.permissionDecision:'deny') AND keeps the legacy top-level
//   decision:'block' alongside it (belt-and-braces across SDK versions);
//   anything else permits.
//
//   D7: the block emits NOTHING. The single tool.failed for a denied call comes
//   from the SDK's own tool_result(is_error) (mapped in mapping.ts). The hook used
//   to ALSO append a synthetic id-less tool.failed — a second terminal that
//   FIFO-corrupted the TUI's tool-card matching. The `emit` seam is injected here
//   only to prove the hook stays silent (a re-added synthetic emit regresses D7).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { HookInput, SyncHookJSONOutput } from '@anthropic-ai/claude-agent-sdk';
import { makePreToolUseBashHook } from '../src/claude.js';

interface Emitted {
  type: string;
  payload: Record<string, unknown>;
}

// Build a real PreToolUseHookInput (the exact shape the SDK passes: Bash's
// tool_input carries a `command` string). tool_name overridable so we can pin
// that the hook itself does NOT gate on it (the SDK `matcher:'Bash'` does).
function preToolUse(command: unknown, toolName = 'Bash'): HookInput {
  return {
    session_id: 's1',
    transcript_path: '/tmp/t',
    cwd: '/tmp',
    hook_event_name: 'PreToolUse',
    tool_name: toolName,
    tool_input: command === undefined ? {} : { command },
    tool_use_id: 'toolu_1',
  } as HookInput;
}

function run(input: HookInput): Promise<{ out: SyncHookJSONOutput; emitted: Emitted[] }> {
  const emitted: Emitted[] = [];
  const hook = makePreToolUseBashHook('s1', 'turn-1', (type, payload) =>
    emitted.push({ type, payload }),
  );
  return hook(input).then((out) => ({ out, emitted }));
}

// Table of representative commands drawn from BLOCKED_BASH_PATTERNS (blocked)
// and benign ones (allowed). Each blocked case must return the deny shape and
// emit NOTHING (D7); each allowed case must permit and emit nothing.
const CASES: Array<{ name: string; command: string; blocked: boolean }> = [
  { name: 'kubectl → block', command: 'kubectl get nodes', blocked: true },
  { name: 'sudo → block', command: 'sudo rm -rf /', blocked: true },
  { name: 'helm → block', command: 'helm list -A', blocked: true },
  { name: 'ANTHROPIC_API_KEY echo → block', command: 'echo $ANTHROPIC_API_KEY', blocked: true },
  { name: 'docker.sock path → block', command: 'ls /var/run/docker.sock', blocked: true },
  { name: 'benign ls → allow', command: 'ls -la', blocked: false },
  { name: 'benign git → allow', command: 'git status', blocked: false },
  { name: 'benign echo → allow', command: 'echo hello world', blocked: false },
];

for (const c of CASES) {
  test(`PreToolUse(Bash) guard: ${c.name}`, async () => {
    const { out, emitted } = await run(preToolUse(c.command));
    if (c.blocked) {
      const o = out as {
        decision?: string;
        continue?: boolean;
        reason?: string;
        hookSpecificOutput?: {
          hookEventName?: string;
          permissionDecision?: string;
          permissionDecisionReason?: string;
        };
      };
      // §1f modern deny shape.
      assert.equal(o.hookSpecificOutput?.hookEventName, 'PreToolUse');
      assert.equal(o.hookSpecificOutput?.permissionDecision, 'deny', 'blocked command must deny');
      assert.match(
        String(o.hookSpecificOutput?.permissionDecisionReason),
        /blocked by sandbox PreToolUse\(Bash\) hook/,
      );
      // Legacy shape kept alongside for compat.
      assert.equal(o.decision, 'block', 'blocked command keeps the legacy decision:block');
      assert.equal(o.continue, false, 'blocked command must set continue:false');
      assert.match(String(o.reason), /blocked by sandbox PreToolUse\(Bash\) hook/);
      // D7: no synthetic tool.failed — the SDK's tool_result is the single terminal.
      assert.equal(emitted.length, 0, 'a block emits no synthetic event (D7)');
    } else {
      assert.deepEqual(out, { continue: true }, 'allowed command must permit');
      assert.equal(emitted.length, 0, 'an allowed command emits nothing');
    }
  });
}

// The SDK passes an empty tool_input (no `command`) in some shapes; the hook
// coerces a missing command to '' and must not throw — it permits.
test('PreToolUse(Bash) guard: missing command permits without throwing', async () => {
  const { out, emitted } = await run(preToolUse(undefined));
  assert.deepEqual(out, { continue: true });
  assert.equal(emitted.length, 0);
});

// A non-string command (defensive against a shape drift) is coerced via String()
// and must not throw. `123` stringifies to a benign token → permit.
test('PreToolUse(Bash) guard: non-string command is coerced, not thrown on', async () => {
  const { out } = await run(preToolUse(123));
  assert.deepEqual(out, { continue: true });
});

// Non-PreToolUse events short-circuit to continue (the hook is defensive even
// though the SDK only wires it under the PreToolUse event).
test('PreToolUse(Bash) guard: a non-PreToolUse event is a no-op continue', async () => {
  const input = {
    session_id: 's1',
    transcript_path: '/tmp/t',
    cwd: '/tmp',
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_input: { command: 'kubectl get nodes' },
  } as unknown as HookInput;
  const { out, emitted } = await run(input);
  assert.deepEqual(out, { continue: true });
  assert.equal(emitted.length, 0);
});

// The hook gates on the COMMAND, not tool_name — it relies on the SDK
// `matcher:'Bash'` to fire only for Bash. Pin that: a blocked command under a
// different tool_name still blocks (documents the matcher dependency; a refactor
// that drops the matcher would not be caught here, but the command gating is).
test('PreToolUse(Bash) guard: does not itself gate on tool_name', async () => {
  const { out } = await run(preToolUse('kubectl get nodes', 'Read'));
  const o = out as { decision?: string };
  assert.equal(o.decision, 'block');
});

// Workstream B — conversation continuity. The runner must resume the persisted
// Claude session on every turn after the first; before this fix every turn was
// a fresh, history-less query() (the model lost the conversation — issues
// #2 model-switch and #3 mid-convo drop, same root cause). The empirical proof
// lives in the B spike (resume restores "teal", a stale id throws "No
// conversation found"); these are the in-repo oracles that would have caught the
// regression had they existed:
//   - effectiveResume       — the defaulting precedence (the fix's core)
//   - buildOptions wiring    — options.resume IS what query() receives
//   - a multi-turn walk      — turn-1 fresh → turn-2 resumes turn-1 → survives
//                              a simulated pod restart (reloaded session.json)
//   - isStaleResumeError     — the fail-soft trigger (matched to the spike string)
//   - shouldCaptureClaudeSession — capture-latest (follow the live head)
//
// buildOptions is side-effect-light (mkdirSync on the project cwd, no sqlite, no
// session.json write), so it runs off-pod given a writable temp projectPath. The
// registry mutators that persist to /session (setClaudeSession) are unwritable
// off-pod, so the walk sets reg.state.claude_session_id directly to simulate the
// captured/reloaded state — what matters is that buildOptions READS it.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  effectiveResume,
  isStaleResumeError,
  isStaleResultMessage,
  shouldRetryStaleResume,
  shouldCaptureClaudeSession,
  buildOptions,
} from '../src/claude.js';
import type { SDKMessage } from '@anthropic-ai/claude-agent-sdk';
import { initRegistry } from '../src/session.js';
import type { RunnerConfig } from '../src/session.js';
import type { SessionState } from '../src/types.js';

const tmpProject = (): string => mkdtempSync(join(tmpdir(), 'resume-test-'));

function freshState(over: Partial<SessionState> = {}): SessionState {
  return {
    sandbox_session_id: 'sess-test',
    backend: 'claude-sdk',
    claude_session_id: '',
    opencode_session_id: '',
    project_path: tmpProject(),
    status: 'idle',
    last_turn_id: '',
    last_activity: '2026-06-23T00:00:00Z',
    ...over,
  };
}

const cfg = (projectPath: string): RunnerConfig => ({
  sessionId: 'sess-test',
  backend: 'claude-sdk',
  projectPath,
  runnerToken: 't',
});

const resumeOf = (
  c: RunnerConfig,
  turnId: string,
  clientResume?: string,
): string | undefined =>
  buildOptions(c, turnId, clientResume, undefined, undefined, undefined, new AbortController()).resume;

// --- effectiveResume: the defaulting precedence --------------------------

test('effectiveResume: defaults to the persisted session id when no client resume', () => {
  assert.equal(effectiveResume(undefined, 'sess-abc'), 'sess-abc');
  assert.equal(effectiveResume('', 'sess-abc'), 'sess-abc');
});

test('effectiveResume: a client-supplied resume id wins over the persisted id', () => {
  assert.equal(effectiveResume('client-xyz', 'sess-abc'), 'client-xyz');
});

test('effectiveResume: undefined (fresh session) when neither is set', () => {
  assert.equal(effectiveResume(undefined, ''), undefined);
  assert.equal(effectiveResume('', ''), undefined);
  assert.equal(effectiveResume(undefined, undefined), undefined);
});

// --- buildOptions wiring: options.resume IS what query() receives --------

test('buildOptions: sets options.resume from the persisted claude_session_id', () => {
  const reg = initRegistry(freshState({ claude_session_id: 'sess-turn1' }));
  assert.equal(resumeOf(cfg(reg.state.project_path), 'turn-2'), 'sess-turn1');
});

test('buildOptions: omits options.resume on the first turn (empty persisted id)', () => {
  const reg = initRegistry(freshState({ claude_session_id: '' }));
  assert.equal(resumeOf(cfg(reg.state.project_path), 'turn-1'), undefined);
});

test('buildOptions: a client-supplied resume overrides the persisted id', () => {
  const reg = initRegistry(freshState({ claude_session_id: 'sess-persisted' }));
  assert.equal(resumeOf(cfg(reg.state.project_path), 'turn-2', 'sess-client'), 'sess-client');
});

// --- multi-turn continuity walk (the behavioral counter) -----------------

test('multi-turn continuity: turn-1 fresh, turn-2 resumes turn-1, survives restart', () => {
  const reg = initRegistry(freshState());
  const project = reg.state.project_path;

  // Turn 1: no persisted id → query() gets a fresh session (no resume).
  assert.equal(resumeOf(cfg(project), 'turn-1'), undefined, 'turn-1 must start a fresh session');

  // SDK init for turn-1 reports session id "A-uuid"; capture-latest persists it.
  // (setClaudeSession writes /session off-pod, so set the field directly to
  // simulate the captured state — buildOptions only READS it.)
  const initA = 'A-uuid';
  assert.equal(shouldCaptureClaudeSession(reg.state.claude_session_id, initA), true);
  reg.state.claude_session_id = initA;

  // Turn 2: query() MUST receive resume = turn-1's id. Pre-fix this was unset →
  // a fresh, history-less query — exactly the regression.
  assert.equal(resumeOf(cfg(project), 'turn-2'), initA, 'turn-2 must resume turn-1');

  // Turn-2 init reports the SAME id (spike: stable across a plain resume) → no rewrite.
  assert.equal(shouldCaptureClaudeSession(reg.state.claude_session_id, initA), false);

  // Pod restart: a fresh runner reloads session.json (claude_session_id='A-uuid').
  const reg2 = initRegistry(freshState({ claude_session_id: initA }));
  assert.equal(
    resumeOf(cfg(reg2.state.project_path), 'turn-3'),
    initA,
    'after restart, the next turn still resumes the persisted session',
  );
});

// --- isStaleResumeError: the fail-soft trigger ---------------------------

test('isStaleResumeError matches the SDK stale-resume error (spike string)', () => {
  assert.equal(
    isStaleResumeError('Claude Code returned an error result: No conversation found with session ID: abc'),
    true,
  );
  assert.equal(isStaleResumeError('no conversation found'), true);
});

test('isStaleResumeError does not match unrelated turn failures', () => {
  assert.equal(isStaleResumeError('rate limit exceeded'), false);
  assert.equal(isStaleResumeError('ECONNRESET'), false);
  assert.equal(isStaleResumeError(''), false);
});

// The SDK yields an is_error `result` for a stale resume id BEFORE it throws.
// runTurn must skip mapping that result (else a spurious turn.failed+error leaks
// into the stream ahead of the successful fail-soft retry).
const resultMsg = (subtype: string, errors?: string[]): SDKMessage =>
  ({ type: 'result', subtype, ...(errors ? { errors } : {}) } as unknown as SDKMessage);

test('isStaleResultMessage detects the stale-resume error result', () => {
  assert.equal(
    isStaleResultMessage(resultMsg('error_during_execution', ['No conversation found with session ID: x'])),
    true,
  );
});

test('isStaleResultMessage ignores success results, unrelated errors, and non-results', () => {
  assert.equal(isStaleResultMessage(resultMsg('success')), false);
  assert.equal(isStaleResultMessage(resultMsg('error_during_execution', ['rate limit exceeded'])), false);
  assert.equal(isStaleResultMessage(resultMsg('error_during_execution', [])), false);
  assert.equal(isStaleResultMessage({ type: 'assistant' } as unknown as SDKMessage), false);
});

// shouldRetryStaleResume: the at-most-once + used-resume guards on the fail-soft
// retry (the inline conjunction in runTurn, extracted so it is unit-testable —
// runTurn binds the sqlite event log and is not).
test('shouldRetryStaleResume: retries only on a stale failure of a resumed turn, at most once', () => {
  const stale = 'No conversation found with session ID: x';
  assert.equal(shouldRetryStaleResume('sess-A', false, stale), true);
  assert.equal(shouldRetryStaleResume('sess-A', true, stale), false); // already retried
  assert.equal(shouldRetryStaleResume(undefined, false, stale), false); // no resume id (fresh turn)
  assert.equal(shouldRetryStaleResume('sess-A', false, 'rate limit exceeded'), false); // not stale
});

// --- shouldCaptureClaudeSession: capture-latest --------------------------

test('shouldCaptureClaudeSession: follow a changed head, ignore empties/repeats', () => {
  assert.equal(shouldCaptureClaudeSession('', 'A'), true); // first capture
  assert.equal(shouldCaptureClaudeSession('A', 'A'), false); // stable resume (spike) → no rewrite
  assert.equal(shouldCaptureClaudeSession('A', 'B'), true); // forked head → follow it
  assert.equal(shouldCaptureClaudeSession('A', ''), false); // no observed id → keep current
  assert.equal(shouldCaptureClaudeSession('A', undefined), false);
});

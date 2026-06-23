// T6: maybeGenerateTitle emits a single session.title event after the first
// assistant turn and is swallow-safe on failure. The SDK summarizer call is
// injected so this stays a pure unit test (no pod, no network).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { maybeGenerateTitle, type TitleDeps } from '../src/claude.js';

interface Emitted {
  type: string;
  payload: Record<string, unknown>;
}

function makeDeps(over: Partial<TitleDeps> & { titleGenerated?: boolean }): {
  deps: TitleDeps;
  emitted: Emitted[];
  marked: boolean[];
} {
  const emitted: Emitted[] = [];
  const marked: boolean[] = [];
  let titleGenerated = over.titleGenerated ?? false;
  const deps: TitleDeps = {
    sessionId: 's1',
    turnId: 'turn-1',
    isTitleGenerated: () => titleGenerated,
    markTitleGenerated: () => {
      titleGenerated = true;
      marked.push(true);
    },
    emit: (type, payload) => emitted.push({ type, payload }),
    summarize: over.summarize ?? (async () => 'Fix the auth race condition.'),
  };
  return { deps, emitted, marked };
}

test('emits exactly one session.title event with a sanitized title', async () => {
  const { deps, emitted, marked } = makeDeps({});
  await maybeGenerateTitle(deps);
  assert.equal(emitted.length, 1);
  assert.equal(emitted[0].type, 'session.title');
  assert.equal(emitted[0].payload.title, 'Fix the auth race condition');
  assert.equal(marked.length, 1, 'must persist the one-shot guard');
});

test('does not emit when the title was already generated (one-shot guard)', async () => {
  const { deps, emitted } = makeDeps({ titleGenerated: true });
  await maybeGenerateTitle(deps);
  assert.equal(emitted.length, 0);
});

test('a failed summarization is swallowed: no throw, no event', async () => {
  const { deps, emitted } = makeDeps({
    summarize: async () => {
      throw new Error('SDK boom');
    },
  });
  await assert.doesNotReject(maybeGenerateTitle(deps));
  assert.equal(emitted.length, 0, 'no event on failure');
});

test('an empty summary is swallowed: guard still set, no event', async () => {
  const { deps, emitted, marked } = makeDeps({ summarize: async () => '   ' });
  await maybeGenerateTitle(deps);
  assert.equal(emitted.length, 0, 'empty title emits nothing');
  assert.equal(marked.length, 1, 'guard set so we never retry a futile summary');
});

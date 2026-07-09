// Regression for B7: a corrupt/truncated session.json (partial write killed by
// an OOM, bad disk) previously threw at boot from JSON.parse; index.ts has no
// catch, so the pod restarted, re-read the SAME bad file, and crash-looped
// forever. readSessionFile now moves the corrupt file aside and returns null so
// loadSessionState falls through to a fresh empty state and the pod boots.
//
// readSessionFile / loadSessionState take an injectable path so we can drive them
// against a tmpdir instead of the production SESSION_JSON_PATH (/session).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync, existsSync, readFileSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { readSessionFile, loadSessionState } from '../src/session.js';
import type { RunnerConfig } from '../src/session.js';

const cfg: RunnerConfig = {
  sessionId: 'sess-B7',
  backend: 'claude-sdk',
  projectPath: '/session/workspace',
  runnerToken: 't',
};

function withTmp(fn: (dir: string, path: string) => void): void {
  const dir = mkdtempSync(join(tmpdir(), 'session-corrupt-'));
  try {
    fn(dir, join(dir, 'session.json'));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test('B7: readSessionFile moves a corrupt file aside and returns null', () => {
  withTmp((dir, path) => {
    writeFileSync(path, '{"status":"busy", trunca', 'utf8'); // truncated JSON

    const parsed = readSessionFile(path);
    assert.equal(parsed, null, 'a corrupt file parses to null (caller reseeds)');
    assert.equal(existsSync(path), false, 'the corrupt file was moved off the live path');

    const aside = readdirSync(dir).filter((f) => f.startsWith('session.json.corrupt-'));
    assert.equal(aside.length, 1, 'exactly one moved-aside copy exists');
    assert.match(readFileSync(join(dir, aside[0]), 'utf8'), /trunca/, 'the bad bytes are preserved');
  });
});

test('B7: readSessionFile returns null for an absent file (no aside copy)', () => {
  withTmp((dir, path) => {
    assert.equal(readSessionFile(path), null);
    assert.equal(readdirSync(dir).length, 0, 'nothing created for a missing file');
  });
});

test('B7: readSessionFile parses a valid file', () => {
  withTmp((_dir, path) => {
    writeFileSync(path, JSON.stringify({ status: 'idle', last_turn_id: 'turn-4' }), 'utf8');
    assert.deepEqual(readSessionFile(path), { status: 'idle', last_turn_id: 'turn-4' });
  });
});

test('B7: loadSessionState recovers from corruption → empty state, no bootEvents, valid reseed', () => {
  withTmp((_dir, path) => {
    writeFileSync(path, 'not json at all', 'utf8');

    const { state, bootEvents } = loadSessionState(cfg, path);

    // Fresh empty state seeded from cfg.
    assert.equal(state.status, 'idle');
    assert.equal(state.sandbox_session_id, 'sess-B7');
    assert.equal(state.last_turn_id, '');
    // A corrupt file's pre-crash state is unrecoverable, so no orphaned-turn
    // recovery events (unlike a clean busy→idle coercion).
    assert.deepEqual(bootEvents, []);

    // The live path now holds a valid, parseable session.json (so the NEXT boot
    // does not crash-loop).
    assert.ok(existsSync(path));
    const reread = JSON.parse(readFileSync(path, 'utf8')) as { status: string };
    assert.equal(reread.status, 'idle');
  });
});

test('B7: loadSessionState on a valid busy file still coerces + emits D2 boot events', () => {
  withTmp((_dir, path) => {
    // A clean (parseable) mid-turn-crash file must keep its D2 orphaned-turn
    // recovery — the corrupt path must not have broken the happy path.
    writeFileSync(path, JSON.stringify({ status: 'busy', last_turn_id: 'turn-9' }), 'utf8');

    const { state, bootEvents } = loadSessionState(cfg, path);
    assert.equal(state.status, 'idle', 'persisted busy coerces to idle on load');
    assert.deepEqual(bootEvents, [
      { turnId: 'turn-9', type: 'turn.interrupted', payload: { reason: 'runner restart' } },
      { type: 'session.status_changed', payload: { status: 'idle' } },
    ]);
  });
});

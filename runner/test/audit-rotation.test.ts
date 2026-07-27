// [T8] audit.jsonl is size-capped with one retained generation. Before this the
// log grew without any bound on the PVC that also holds the event log and the
// agent's state. All through the createAuditWriter seam with in-memory fakes —
// the real AUDIT_JSONL_PATH is the pod's live state dir, which a test must never
// write to.

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { createAuditWriter, type AuditWriterDeps } from '../src/audit.js';
import type { AuditRow } from '../src/types.js';

function row(command: string): AuditRow {
  return {
    time: '2026-07-27T00:00:00.000Z',
    session_id: 's-1',
    turn_id: 't-1',
    tool: 'Bash',
    input: { command },
    exit_code: 0,
  };
}

/** A fake filesystem recording appends, renames, and per-path contents. */
function fakeFs(existingSize?: number) {
  const files = new Map<string, string>();
  const renames: Array<[string, string]> = [];
  const deps: AuditWriterDeps = {
    path: '/fake/audit.jsonl',
    mkdirSync: () => undefined,
    appendFileSync: (p, data) => files.set(p, (files.get(p) ?? '') + data),
    renameSync: (from, to) => {
      renames.push([from, to]);
      files.set(to, files.get(from) ?? '');
      files.delete(from);
    },
    sizeOf: (p) => {
      if (p === '/fake/audit.jsonl' && existingSize !== undefined) return existingSize;
      throw new Error('ENOENT');
    },
  };
  return { deps, files, renames };
}

test('audit appends rows and does not rotate below the cap', () => {
  const { deps, files, renames } = fakeFs();
  const w = createAuditWriter({ ...deps, maxBytes: 10_000 });

  w.append(row('echo one'));
  w.append(row('echo two'));

  assert.equal(renames.length, 0);
  const lines = files.get('/fake/audit.jsonl')!.trimEnd().split('\n');
  assert.equal(lines.length, 2);
  assert.equal(JSON.parse(lines[0]).input.command, 'echo one');
});

test('audit rotates to .1 once the cap is reached, bounding disk at 2x', () => {
  const { deps, files, renames } = fakeFs();
  // A cap smaller than one row forces rotation after every append.
  const w = createAuditWriter({ ...deps, maxBytes: 1 });

  w.append(row('first'));
  w.append(row('second'));

  assert.deepEqual(renames, [
    ['/fake/audit.jsonl', '/fake/audit.jsonl.1'],
    ['/fake/audit.jsonl', '/fake/audit.jsonl.1'],
  ]);
  // Exactly ONE previous generation survives — the second rotation replaced the
  // first, which is what bounds total usage at 2x the cap rather than growing a
  // .1/.2/.3 chain.
  assert.equal(JSON.parse(files.get('/fake/audit.jsonl.1')!.trim()).input.command, 'second');
  assert.equal(files.has('/fake/audit.jsonl.2'), false);
});

test('the row that triggers rotation is written first, never dropped or split', () => {
  const { deps, files } = fakeFs();
  const w = createAuditWriter({ ...deps, maxBytes: 1 });

  w.append(row('trigger'));

  // It landed in the generation that was current when it happened…
  const prev = files.get('/fake/audit.jsonl.1')!;
  assert.equal(JSON.parse(prev.trim()).input.command, 'trigger');
  // …as one complete line, not a fragment either side of the rotation.
  assert.equal(prev.trimEnd().split('\n').length, 1);
});

test('a restarted pod resumes counting from the existing size', () => {
  // Without seeding from the file, a session that restarts often would reset the
  // counter to 0 every boot and never reach the cap.
  const { deps, renames } = fakeFs(900);
  const w = createAuditWriter({ ...deps, maxBytes: 1000 });

  w.append(row('one small row past 900 bytes'));

  assert.equal(renames.length, 1, 'expected rotation from the seeded size, not from 0');
});

test('maxBytes=0 disables rotation entirely', () => {
  const { deps, renames, files } = fakeFs();
  const w = createAuditWriter({ ...deps, maxBytes: 0 });

  for (let i = 0; i < 50; i++) w.append(row(`row ${i}`));

  assert.equal(renames.length, 0);
  assert.equal(files.get('/fake/audit.jsonl')!.trimEnd().split('\n').length, 50);
});

test('a failed rename keeps appending instead of losing rows', () => {
  const { deps, files } = fakeFs();
  const w = createAuditWriter({
    ...deps,
    maxBytes: 1,
    renameSync: () => {
      throw new Error('EBUSY');
    },
  });

  w.append(row('one'));
  w.append(row('two'));

  // An unbounded log is the correct failure mode here; a dropped audit row is not.
  assert.equal(files.get('/fake/audit.jsonl')!.trimEnd().split('\n').length, 2);
});

test('rotation does not bypass redaction', () => {
  const { deps, files } = fakeFs();
  const w = createAuditWriter({ ...deps, maxBytes: 1 });

  w.append(row('curl -H "Authorization: Bearer sk-ant-secret-value"'));

  assert.ok(!files.get('/fake/audit.jsonl.1')!.includes('sk-ant-secret-value'));
});

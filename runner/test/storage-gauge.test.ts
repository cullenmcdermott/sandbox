// [T6] part (a): measure event-log growth and PVC free space. The log never
// VACUUMs and its retention cap is off by default, so it grows monotonically on
// the volume that also holds session.json, the audit log, and the agent's state.
// Part (b) — whether that retention default should change — is meant to be
// decided from these numbers, so the numbers have to be right.

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { sampleStorageStats } from '../src/events.js';

const statfsOK = () => ({ bavail: 250, blocks: 1000, bsize: 4096 });

test('event-log size counts the WAL and shm siblings, not just the main file', () => {
  // The WAL can dwarf the main file between checkpoints, so ignoring it would
  // under-report exactly when the log is growing fastest.
  const sizes: Record<string, number> = {
    '/session/state/sandbox/events.db': 1000,
    '/session/state/sandbox/events.db-wal': 4_000_000,
    '/session/state/sandbox/events.db-shm': 32_768,
  };
  const s = sampleStorageStats({
    sizeOf: (p) => {
      if (!(p in sizes)) throw new Error('ENOENT');
      return sizes[p];
    },
    statfs: statfsOK,
    countRows: () => 4242,
  });

  assert.equal(s.eventLogBytes, 1000 + 4_000_000 + 32_768);
  assert.equal(s.eventLogRows, 4242);
});

test('a missing WAL is not an error — a fresh log reports just the main file', () => {
  const s = sampleStorageStats({
    sizeOf: (p) => {
      if (p.endsWith('events.db')) return 4096;
      throw new Error('ENOENT');
    },
    statfs: statfsOK,
    countRows: () => 0,
  });

  assert.equal(s.eventLogBytes, 4096);
  assert.equal(s.eventLogRows, 0);
});

test('free space uses available blocks, not free blocks', () => {
  // bavail excludes root-reserved blocks; reporting bfree would overstate what
  // the runner can actually write.
  const s = sampleStorageStats({
    sizeOf: () => 0,
    statfs: () => ({ bavail: 250, blocks: 1000, bsize: 4096 }),
    countRows: () => 0,
  });

  assert.equal(s.pvcFreeBytes, 250 * 4096);
  assert.equal(s.pvcTotalBytes, 1000 * 4096);
});

test('every failure degrades to 0 rather than throwing', () => {
  // A gauge that crashes the runner is worse than a gauge that reads 0.
  const s = sampleStorageStats({
    sizeOf: () => {
      throw new Error('EACCES');
    },
    statfs: () => {
      throw new Error('ENOSYS');
    },
    countRows: () => {
      throw new Error('db closed');
    },
  });

  assert.deepEqual(s, {
    eventLogBytes: 0,
    eventLogRows: 0,
    pvcFreeBytes: 0,
    pvcTotalBytes: 0,
  });
});

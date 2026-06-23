// audit.jsonl — append-only tool audit log written by PostToolUse hooks.
//
// One JSON object per line (spec 8.5). PostToolUse(Edit|Write|Bash) appends a
// row capturing the tool, input, and exit code so post-hoc review can
// reconstruct every filesystem/mutating action the agent took.

import { appendFileSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import type { AuditRow } from './types.js';
import { AUDIT_JSONL_PATH } from './types.js';

let initialized = false;

function ensureDir(): void {
  if (initialized) return;
  mkdirSync(dirname(AUDIT_JSONL_PATH), { recursive: true });
  initialized = true;
}

/**
 * Append an audit row to audit.jsonl. Synchronous write: hooks run on the
 * critical path of a tool result, and losing an audit line to an async flush
 * error is worse than a brief blocking write.
 */
export function appendAudit(row: AuditRow): void {
  ensureDir();
  appendFileSync(AUDIT_JSONL_PATH, JSON.stringify(row) + '\n', 'utf8');
}

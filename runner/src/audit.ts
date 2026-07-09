// audit.jsonl — append-only tool audit log written by PostToolUse hooks.
//
// One JSON object per line (spec 8.5). PostToolUse(Edit|Write|Bash) appends a
// row capturing the tool, input, and exit code so post-hoc review can
// reconstruct every filesystem/mutating action the agent took.

import { appendFileSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import type { AuditRow } from './types.js';
import { AUDIT_JSONL_PATH } from './types.js';
// A2: the redactor now lives in a shared module so events.ts can reuse the exact
// same masking (SECRET_KEY_RE + known-token rules). Re-exported here so audit.ts's
// existing M13 callers/tests keep importing redactSecrets from './audit.js'.
import { redactSecrets } from './redact.js';

export { redactSecrets };

let initialized = false;

function ensureDir(): void {
  if (initialized) return;
  mkdirSync(dirname(AUDIT_JSONL_PATH), { recursive: true });
  initialized = true;
}

/**
 * Append an audit row to audit.jsonl. Synchronous write: hooks run on the
 * critical path of a tool result, and losing an audit line to an async flush
 * error is worse than a brief blocking write. Tool inputs are redacted so
 * secrets don't land in the on-disk log (M13).
 */
export function appendAudit(row: AuditRow): void {
  ensureDir();
  const redacted = { ...row, input: redactSecrets(row.input) as AuditRow['input'] };
  appendFileSync(AUDIT_JSONL_PATH, JSON.stringify(redacted) + '\n', 'utf8');
}

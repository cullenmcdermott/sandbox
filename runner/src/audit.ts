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

// Field names whose value is a secret regardless of content.
const SECRET_KEY_RE = /(^|[_-])(key|token|secret|password|passwd|credentials?|authorization|api[_-]?key)$/i;

// The runner's own secret env values, masked if they appear verbatim in a logged
// string (e.g. an `echo $RUNNER_TOKEN`-expanded command).
function runnerSecretValues(): string[] {
  return ['RUNNER_TOKEN', 'ANTHROPIC_API_KEY', 'OPENAI_API_KEY', 'OPENCODE_API_KEY', 'OPENCODE_SERVER_PASSWORD', 'CLAUDE_CODE_OAUTH_TOKEN']
    .map((k) => process.env[k])
    .filter((v): v is string => !!v && v.length >= 8);
}

function redactString(s: string): string {
  let out = s;
  for (const secret of runnerSecretValues()) out = out.split(secret).join('[redacted]');
  out = out.replace(/\bsk-[A-Za-z0-9_-]{8,}/g, '[redacted]');
  out = out.replace(/(Authorization\s*:\s*Bearer\s+)\S+/gi, '$1[redacted]');
  return out;
}

/**
 * Redact secrets from an audit value before it is persisted (M13): values of
 * secret-named fields are masked wholesale, and known secret tokens are masked
 * inside any string. Recurses into objects and arrays.
 */
export function redactSecrets(value: unknown): unknown {
  if (typeof value === 'string') return redactString(value);
  if (Array.isArray(value)) return value.map(redactSecrets);
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = SECRET_KEY_RE.test(k) ? '[redacted]' : redactSecrets(v);
    }
    return out;
  }
  return value;
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

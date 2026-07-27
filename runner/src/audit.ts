// audit.jsonl — append-only tool audit log written by PostToolUse hooks.
//
// One JSON object per line (spec 8.5). PostToolUse(Edit|Write|Bash) appends a
// row capturing the tool, input, and exit code so post-hoc review can
// reconstruct every filesystem/mutating action the agent took.
//
// The log lives on the PVC and, before [T8], grew with no bound at all — a
// long-lived session could fill the volume that also holds the event log and the
// agent's own state. It is now size-capped with ONE previous generation retained
// (audit.jsonl.1), so worst-case on-disk usage is 2× the cap. Rotation rather
// than truncation: the point of an audit log is that a row, once written, is not
// silently edited away.

import { appendFileSync, mkdirSync, renameSync, statSync } from 'node:fs';
import { dirname } from 'node:path';
import type { AuditRow } from './types.js';
import { AUDIT_JSONL_PATH } from './types.js';
// A2: the redactor now lives in a shared module so events.ts can reuse the exact
// same masking (SECRET_KEY_RE + known-token rules). Re-exported here so audit.ts's
// existing M13 callers/tests keep importing redactSecrets from './audit.js'.
import { redactSecrets } from './redact.js';

export { redactSecrets };

/** Rotate the audit log once it reaches this size. 8 MiB matches the host
 * event-cache tail cap ([E8-E10]) and sits far above any plausible session's
 * tool volume, so rotation is a safety net rather than routine behavior.
 * Overridable via SANDBOX_AUDIT_MAX_BYTES; 0 (or a malformed value) disables
 * rotation for an operator who would rather keep an unbounded log than ever
 * lose the oldest generation. */
export const AUDIT_MAX_BYTES = ((): number => {
  const raw = process.env.SANDBOX_AUDIT_MAX_BYTES;
  if (raw === undefined) return 8 * 1024 * 1024;
  const v = parseInt(raw, 10);
  return Number.isFinite(v) && v > 0 ? v : 0;
})();

/** Filesystem + policy seam (mirrors PaneObserverFs) so the rotation behavior is
 * testable without writing to the pod's real state dir. */
export interface AuditWriterDeps {
  path?: string;
  maxBytes?: number;
  mkdirSync?: (p: string, o: { recursive: boolean }) => void;
  appendFileSync?: (p: string, data: string, enc: 'utf8') => void;
  renameSync?: (from: string, to: string) => void;
  /** Size of an existing log, or throws when absent. */
  sizeOf?: (p: string) => number;
}

export interface AuditWriter {
  append(row: AuditRow): void;
}

/**
 * Build an audit writer over a log path. Appends are synchronous: hooks run on
 * the critical path of a tool result, and losing an audit line to an async flush
 * error is worse than a brief blocking write. Tool inputs are redacted so
 * secrets don't land in the on-disk log (M13).
 */
export function createAuditWriter(deps: AuditWriterDeps = {}): AuditWriter {
  const path = deps.path ?? AUDIT_JSONL_PATH;
  const prevPath = `${path}.1`;
  const maxBytes = deps.maxBytes ?? AUDIT_MAX_BYTES;
  const mkdir = deps.mkdirSync ?? mkdirSync;
  const appendFile = deps.appendFileSync ?? appendFileSync;
  const rename = deps.renameSync ?? renameSync;
  const sizeOf = deps.sizeOf ?? ((p: string): number => statSync(p).size);

  let initialized = false;
  // Bytes in the CURRENT generation. Seeded from the file on first use so a pod
  // restart resumes counting from the real size rather than from 0 — otherwise a
  // session that restarts often would never reach the cap and never rotate.
  let currentBytes = 0;

  function ensureReady(): void {
    if (initialized) return;
    mkdir(dirname(path), { recursive: true });
    try {
      currentBytes = sizeOf(path);
    } catch {
      currentBytes = 0; // no log yet
    }
    initialized = true;
  }

  /** Move the current log aside, replacing any older generation. Best-effort: if
   * the rename fails, keep appending to the oversized file and retry on the next
   * append — an unbounded log beats dropping audit rows on the floor. */
  function rotate(): void {
    try {
      rename(path, prevPath);
      currentBytes = 0;
    } catch {
      // Intentionally ignored; see above.
    }
  }

  return {
    append(row: AuditRow): void {
      ensureReady();
      const redacted = { ...row, input: redactSecrets(row.input) as AuditRow['input'] };
      const line = JSON.stringify(redacted) + '\n';
      // Rotation happens AFTER the write, never before: a row always lands in
      // the generation that was current when it happened, so no row is split
      // across files and a failed rotation cannot lose one.
      appendFile(path, line, 'utf8');
      currentBytes += Buffer.byteLength(line, 'utf8');
      if (maxBytes > 0 && currentBytes >= maxBytes) rotate();
    },
  };
}

const defaultWriter = createAuditWriter();

/** Append an audit row to the pod's audit.jsonl. */
export function appendAudit(row: AuditRow): void {
  defaultWriter.append(row);
}

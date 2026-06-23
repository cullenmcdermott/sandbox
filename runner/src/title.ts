// Helpers for the one-time auto session title (T6). Kept pure (no SDK, no I/O)
// so the title-shaping logic and the one-shot guard are unit-testable; the
// SDK summarizer call + event emit live in claude.ts (maybeGenerateTitle).

import type { SessionState } from './types.js';

/** Hardcoded summarizer prompt (design: no per-session configurability). */
export const TITLE_PROMPT = 'Summarize this task in 5 words or fewer, no punctuation.';

/** Max characters kept for a generated title. */
const TITLE_MAX_LEN = 80;

/**
 * Normalize a raw model summary into a display title: collapse whitespace, trim,
 * strip surrounding quotes and trailing sentence punctuation, and bound length.
 * Returns '' for empty/whitespace input so the caller can skip emitting.
 */
export function sanitizeTitle(raw: string): string {
  let s = raw.replace(/\s+/g, ' ').trim();
  // Strip wrapping quotes a model sometimes adds.
  s = s.replace(/^["'`]+/, '').replace(/["'`]+$/, '');
  // Strip trailing sentence punctuation (the prompt asks for none, but be safe).
  s = s.replace(/[.!?,;:]+$/, '').trim();
  if (s.length > TITLE_MAX_LEN) s = s.slice(0, TITLE_MAX_LEN).trim();
  return s;
}

/**
 * True when the one-shot auto title has not yet been generated for this session.
 * A missing flag (older session.json) is treated as not generated.
 */
export function shouldGenerateTitle(state: Pick<SessionState, 'title_generated'>): boolean {
  return state.title_generated !== true;
}

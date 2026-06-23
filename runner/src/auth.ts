// Bearer-token auth primitives. Deliberately sqlite-free (imports nothing that
// loads better-sqlite3 at module load) so the auth logic is unit-testable under
// CI's `npm install --ignore-scripts`, where the native addon is absent. The
// HTTP layer (server.ts) binds the configured token to these pure helpers.

/**
 * Constant-time string comparison (R9). XORs every byte of both strings, padded
 * to the longer length, so the running time is independent of where (or whether)
 * the strings first differ — it must NOT short-circuit on the first mismatch or
 * on a length mismatch, since either would leak information via timing. A length
 * mismatch still fails (the seeded diff is non-zero), but only after the full
 * loop has run.
 */
export function constantTimeEqual(a: string, b: string): boolean {
  const len = Math.max(a.length, b.length);
  let diff = a.length ^ b.length; // non-zero if lengths differ
  for (let i = 0; i < len; i++) {
    diff |= (a.charCodeAt(i) || 0) ^ (b.charCodeAt(i) || 0);
  }
  return diff === 0;
}

/**
 * Validate an HTTP Authorization header against the expected bearer token.
 * Returns false (reject) when:
 *   - no token is configured (expected is empty) — reject ALL requests;
 *   - the header is missing or not a string;
 *   - the scheme is not `Bearer <token>`;
 *   - the token does not match (constant-time).
 * The token comparison is constant-time (constantTimeEqual) so it does not leak
 * the token length or contents via timing.
 */
export function bearerTokenOk(
  expected: string,
  header: string | string[] | undefined,
): boolean {
  if (!expected) return false; // no token configured => reject all non-healthz
  if (!header || typeof header !== 'string') return false;
  const m = /^Bearer\s+(.+)$/.exec(header);
  if (!m) return false;
  return constantTimeEqual(m[1], expected);
}

// Shared secret redaction (M13 / A2).
//
// Factored out of audit.ts so BOTH the audit.jsonl writer and the SQLite event
// log / SSE fan-out (events.ts) mask secrets with byte-identical logic. audit.ts
// re-exports redactSecrets for back-compat (its M13 tests import it from there).
//
// The redactor returns NEW structures — it never mutates the caller's object —
// so a caller can redact a payload for persistence/broadcast while keeping the
// original in hand.

// Field names whose value is a secret regardless of content.
//
// Two boundaries are recognized so structured secrets don't slip through:
//  - snake_case / kebab-case / bare (case-insensitive): key, token, api_key,
//    my-secret, authorization — the secret word at start or after a '_' / '-'.
//  - [V17] camelCase (case-SENSITIVE): a lowercase letter or digit immediately
//    followed by a Capitalized secret word at end — authToken, accessToken,
//    clientSecret, sessionToken, myApiKey. Case-sensitivity is deliberate: it
//    matches the Capital-initial camelCase hump while a fully-lowercase run
//    ('stoken', 'broken', 'monotonic') is NOT a false positive.
const SECRET_KEY_SNAKE_RE = /(^|[_-])(key|token|secret|password|passwd|credentials?|authorization|api[_-]?key)$/i;
const SECRET_KEY_CAMEL_RE = /[a-z0-9](Key|Token|Secret|Password|Passwd|Credentials?|Authorization|ApiKey)$/;

function isSecretKey(k: string): boolean {
  return SECRET_KEY_SNAKE_RE.test(k) || SECRET_KEY_CAMEL_RE.test(k);
}

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
 * Redact secrets from a value before it is persisted (M13): values of
 * secret-named fields are masked wholesale, and known secret tokens are masked
 * inside any string. Recurses into objects and arrays, returning fresh copies
 * (no caller-object mutation).
 */
export function redactSecrets(value: unknown): unknown {
  if (typeof value === 'string') return redactString(value);
  if (Array.isArray(value)) return value.map(redactSecrets);
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = isSecretKey(k) ? '[redacted]' : redactSecrets(v);
    }
    return out;
  }
  return value;
}

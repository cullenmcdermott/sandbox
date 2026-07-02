// Shared Bash-command guardrails.
//
// The SAME blocklist gates both the Claude SDK Bash tool (via the PreToolUse
// hook in claude.ts) and the one-shot `/exec` shell passthrough (exec.ts), so
// neither path is a softer escape than the other (INTEGRATION-GAPS O2).
//
// This is a DEFENSE-IN-DEPTH control only: the real security boundary is the
// Kubernetes NetworkPolicy + the absent service-account token. The word-boundary
// patterns below are trivially evadable (variable aliasing, base64, wrappers) —
// do not treat them as a hard sandbox boundary.

export const BLOCKED_BASH_PATTERNS: RegExp[] = [
  /\bkubectl\b/,
  /\btalosctl\b/,
  /\bhelm\b/,
  /\bargocd\b/,
  /\bop\s+(auth|login|whoami|token|kubeconfig)\b/,
  /\bsudo\b/,
  /\bssh\b/,
  /\bscp\b/,
  /\brsync\b/,
  /\bdocker\b/,
  /\bpodman\b/,
  /\bcrictl\b/,
  /\bctr\b/,
  /\bnsenter\b/,
  /\bchroot\b/,
  /\bmount\b/,
  /\bumount\b/,
  /\bip\s+(addr|link|route)\b/,
  /\biptables\b/,
  /\bchmod\s+[0-7]{4}?\s+\/etc\b/,
  /\bcat\s+\/etc\/shadow\b/,
  /\bcat\s+\/etc\/passwd\b/,
  /~\/\.ssh\//,
  /\/etc\/kubernetes\//,
  /\/var\/run\/docker\.sock/,
  /\/run\/containerd\//,
  /\bANTHROPIC_API_KEY\b/,
  /\bAWS_SECRET_ACCESS_KEY\b/,
  /\bKUBECONFIG\b/,
];

export function bashCommandBlocked(command: string): boolean {
  return BLOCKED_BASH_PATTERNS.some((re) => re.test(command));
}

/**
 * Serialize BLOCKED_BASH_PATTERNS to a JS array-literal source string of
 * `new RegExp(source, flags)` constructors. This is the single-source-of-truth
 * hand-off to the GENERATED opencode guardrail plugin (opencode.ts): opencode
 * runs its in-agent tools inside its own `opencode serve` process, so the same
 * blocklist has to be embedded in a plugin file loaded by that process rather
 * than imported from this module at runtime. Each pattern is reconstructed
 * losslessly from `RegExp.source` + `RegExp.flags` (JSON-escaped), so flags are
 * preserved too — today every pattern is flagless, but this stays correct if a
 * future pattern adds e.g. the `i` flag.
 */
export function serializeBlockedPatterns(): string {
  const lines = BLOCKED_BASH_PATTERNS.map(
    (re) => `  new RegExp(${JSON.stringify(re.source)}, ${JSON.stringify(re.flags)}),`,
  );
  return `[\n${lines.join('\n')}\n]`;
}

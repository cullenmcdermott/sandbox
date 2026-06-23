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

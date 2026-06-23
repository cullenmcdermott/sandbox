// Session-scoped permission grants. A v1, tool-name-level grant store: when a
// permission resolves with scope:'session', the tool's name is recorded here so
// subsequent uses of that tool auto-allow without re-prompting (for this pod's
// single session). Deliberately sqlite-free and dependency-free so the grant
// logic is unit-testable without the native sqlite addon.
//
// Semantics are intentionally coarse (tool name only, not tool input): "always
// allow Bash for this session" rather than "always allow this exact command".
// One session per pod, so a process-lifetime in-memory Set is sufficient; it is
// not persisted across pod restarts (a restart re-prompts, which is the safe
// default).

/** A set of tool names granted "allow for the rest of this session". */
export class SessionGrants {
  private readonly granted = new Set<string>();

  /** Record a session-level grant for a tool name. Idempotent. */
  grant(toolName: string): void {
    if (toolName) this.granted.add(toolName);
  }

  /** True if this tool name has a session-level grant (=> auto-allow). */
  isGranted(toolName: string): boolean {
    return this.granted.has(toolName);
  }

  /** Snapshot of granted tool names (for tests/inspection). */
  list(): string[] {
    return [...this.granted];
  }
}

/**
 * Decide what a permission resolution does given its scope. Pure helper so the
 * grant/auto-allow logic is testable without the registry or sqlite:
 *   - scope 'session' + allow  => record a tool-name grant, decision 'allow-session';
 *   - allow (any other scope)  => decision 'allow-once', no grant;
 *   - deny                     => decision 'deny', no grant.
 * Returns the decision string (matching the event-model PermissionPayload
 * vocabulary) and whether the caller should record a session grant.
 */
export function resolutionOutcome(
  allow: boolean,
  scope: string | undefined,
): { decision: 'allow-session' | 'allow-once' | 'deny'; grantSession: boolean } {
  if (!allow) return { decision: 'deny', grantSession: false };
  if (scope === 'session') return { decision: 'allow-session', grantSession: true };
  return { decision: 'allow-once', grantSession: false };
}

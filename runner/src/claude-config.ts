// claude-pane config materialization (task 2.3 of the claude-pane-first change).
//
// A claude-pane pod boots with the session Secret surfaced as two env vars on
// the RUNNER process only (never the pane child): CLAUDE_CREDENTIALS_JSON — the
// full Claude Code OAuth credential ({"claudeAiOauth": {...}}, the
// .credentials.json shape) — and CLAUDE_OAUTH_ACCOUNT_JSON — the account
// identity envelope ({"oauthAccount": {...}}). Before the interactive child can
// start seamlessly (no login, no trust dialog, subscription mode), those must
// exist as files in CLAUDE_CONFIG_DIR on the PVC:
//
//   .credentials.json  — written only when the PVC has no USABLE credential.
//                        The in-pod claude refreshes this file itself (rotating
//                        access tokens), so overwriting a fresh credential with
//                        stale Secret material would re-break auth. But presence
//                        is the wrong test: claude BLANKS its own claudeAiOauth
//                        block on a failed refresh or a logout — the keys stay
//                        with an empty accessToken and expiresAt: 0 — and that
//                        dead file would otherwise win every boot, pinning the
//                        session at /login forever while a good credential sits
//                        unused in the Secret. So the guard is validity, not
//                        presence (see materializeCredentials).
//   .claude.json       — merged, not overwritten: the boot ensures the minimal
//                        seamless-start seed (hasCompletedOnboarding,
//                        lastOnboardingVersion, oauthAccount, and the workspace
//                        trust entry) while preserving every key claude itself
//                        has written. Safe to do at boot: the pane child spawns
//                        lazily on first attach, so there is no concurrent
//                        writer.
//
// The seed shape was validated empirically (2026-07-20): with exactly these
// keys a fresh config dir boots straight to the composer in Max mode. See the
// claude-auth-provisioning spec for the requirement-level contract.

import { execFileSync, type ExecFileSyncOptions } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { CLAUDE_CONFIG_DIR } from './types.js';

/** Injectable filesystem surface (mirrors CodexAuthFs) so the merge/seed logic
 * is unit-testable off-pod. Production uses node:fs. */
export interface ClaudeConfigFs {
  readFileSync: typeof readFileSync;
  writeFileSync: typeof writeFileSync;
  mkdirSync: typeof mkdirSync;
}

const realFs: ClaudeConfigFs = { readFileSync, writeFileSync, mkdirSync };

/** The per-workspace trust entry that suppresses the trust dialog and project
 * onboarding on first launch. Exported for tests. */
export const WORKSPACE_TRUST_SEED = {
  hasTrustDialogAccepted: true,
  hasCompletedProjectOnboarding: true,
  projectOnboardingSeenCount: 1,
  allowedTools: [] as string[],
};

export interface MaterializeClaudePaneOptions {
  /** The session workspace dir — the key of the projects trust entry. */
  workspaceDir: string;
  /** Env carrying the Secret material (defaults to process.env). */
  env?: NodeJS.ProcessEnv;
  /** Target config dir (defaults to $CLAUDE_CONFIG_DIR, else the shared constant). */
  configDir?: string;
  fs?: ClaudeConfigFs;
  /** Returns the installed claude version ("2.1.215") or '' when unknown;
   * defaults to running `claude --version`. Injectable for tests. */
  claudeVersion?: () => string;
}

/** Leading "X.Y.Z" of s, or '' — `claude --version` prints trailing build text,
 * and an ENV carrying anything unexpected must degrade like a failed exec
 * rather than seed a malformed version. */
function semverPrefix(s: string): string {
  const m = /^(\d+\.\d+\.\d+)/.exec(s.trim());
  return m ? m[1] : '';
}

/** Read the installed claude version for the lastOnboardingVersion seed. Any
 * failure (binary missing in dev images, unexpected output) degrades to '' —
 * the field is then omitted and claude may show a what's-new note, which is
 * cosmetic, so this is deliberately not fail-closed.
 *
 * Prefers $CLAUDE_CODE_VERSION and skips the spawn entirely when it is set: the
 * exec sits on the boot path — execFileSync cold-starts a second ~50MB Node
 * bundle and blocks the :8787 listen, which readiness gates, which Connect
 * blocks on, for 1-3s (worst case the 10s timeout) on every cold start and every
 * resume, purely to seed a cosmetic lastOnboardingVersion. The ENV is baked into
 * the runner image alongside the pinned, sha256-verified binary; the exec path
 * stays as the fallback for dev images and any build that does not set it, so
 * this degrades to the old behavior until the image is rebuilt. */
export function detectClaudeVersion(
  exec: (cmd: string, args: string[], opts: ExecFileSyncOptions) => Buffer | string = execFileSync,
  env: NodeJS.ProcessEnv = process.env,
): string {
  const pinned = semverPrefix(env.CLAUDE_CODE_VERSION ?? '');
  if (pinned !== '') return pinned;
  try {
    return semverPrefix(exec('claude', ['--version'], { timeout: 10_000 }).toString());
  } catch {
    return '';
  }
}

/**
 * Materialize the claude-pane auth + seamless-start state into configDir.
 * Fail-closed on the credential: a pod with neither an existing
 * .credentials.json nor CLAUDE_CREDENTIALS_JSON (or with unparseable Secret
 * material) throws, crashing boot visibly rather than starting a pane that
 * would demand an interactive login. Never logs credential bytes.
 */
export function materializeClaudePaneConfig(opts: MaterializeClaudePaneOptions): void {
  const env = opts.env ?? process.env;
  const fs = opts.fs ?? realFs;
  const dir = opts.configDir ?? (env.CLAUDE_CONFIG_DIR || CLAUDE_CONFIG_DIR);
  fs.mkdirSync(dir, { recursive: true });

  materializeCredentials(dir, env, fs);
  // Resolve the version against the SAME env the rest of this function uses, so a
  // test or an env-overriding caller sees the pinned version rather than the
  // ambient process env.
  const version = opts.claudeVersion ? opts.claudeVersion() : detectClaudeVersion(undefined, env);
  mergeStateSeed(dir, env, fs, opts.workspaceDir, version);
}

/** Whether a claudeAiOauth block can actually log claude in. The one test both
 * the on-PVC file and the Secret material are judged by, so a credential that
 * cannot authenticate is never mistaken for one. */
function usableToken(oauth: { accessToken?: unknown } | undefined): boolean {
  return typeof oauth?.accessToken === 'string' && oauth.accessToken !== '';
}

/** usableToken applied to a whole .credentials.json document. Unparseable input
 * is "not usable" — it cannot log claude in either. */
function hasUsableOAuth(raw: string): boolean {
  try {
    const doc = JSON.parse(raw) as { claudeAiOauth?: { accessToken?: unknown } };
    return usableToken(doc.claudeAiOauth);
  } catch {
    return false;
  }
}

/** existing with its dead claudeAiOauth replaced by oauth, or undefined when
 * existing is not a JSON object — there is then nothing to preserve and the
 * caller falls back to the Secret bytes. */
function withOAuth(existing: string, oauth: unknown): string | undefined {
  let doc: unknown;
  try {
    doc = JSON.parse(existing);
  } catch {
    return undefined;
  }
  if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) return undefined;
  return JSON.stringify({ ...(doc as Record<string, unknown>), claudeAiOauth: oauth });
}

function materializeCredentials(dir: string, env: NodeJS.ProcessEnv, fs: ClaudeConfigFs): void {
  const path = join(dir, '.credentials.json');
  const existing = readIfExists(fs, path);
  // Validity, not presence: a present-but-dead file (blanked claudeAiOauth) must
  // NOT win — it would pin the session at /login while a good Secret credential
  // sits unused. Only an on-PVC file that can actually authenticate short-circuits.
  if (existing !== undefined && hasUsableOAuth(existing)) return; // in-pod refresh wins
  const raw = env.CLAUDE_CREDENTIALS_JSON;
  if (!raw) {
    throw new Error(
      'claude-pane: no usable .credentials.json on the PVC and no CLAUDE_CREDENTIALS_JSON in the env — session Secret material is missing',
    );
  }
  let doc: { claudeAiOauth?: { accessToken?: unknown } };
  try {
    doc = JSON.parse(raw) as typeof doc;
  } catch {
    throw new Error('claude-pane: CLAUDE_CREDENTIALS_JSON is not valid JSON');
  }
  if (!usableToken(doc.claudeAiOauth)) {
    throw new Error('claude-pane: CLAUDE_CREDENTIALS_JSON carries no claudeAiOauth.accessToken');
  }
  // Merge rather than overwrite on recovery: the pod's mcpOAuth server tokens are
  // per-pod and unrecoverable from the Secret, so only claudeAiOauth is swapped
  // in. A fresh dir has nothing to preserve, so it takes the Secret bytes
  // verbatim (no re-serialization) and credential fields this module does not
  // model survive untouched.
  const recovered = existing === undefined ? undefined : withOAuth(existing, doc.claudeAiOauth);
  fs.writeFileSync(path, recovered ?? raw, { mode: 0o600 });
}

/** The subset of .claude.json state the seed touches. Everything else in an
 * existing file is preserved verbatim via the parsed-object merge. */
interface ClaudeStateDoc {
  hasCompletedOnboarding?: unknown;
  lastOnboardingVersion?: unknown;
  oauthAccount?: unknown;
  projects?: Record<string, Record<string, unknown>>;
  [key: string]: unknown;
}

function mergeStateSeed(
  dir: string,
  env: NodeJS.ProcessEnv,
  fs: ClaudeConfigFs,
  workspaceDir: string,
  version: string,
): void {
  const path = join(dir, '.claude.json');
  const existing = readIfExists(fs, path);
  let doc: ClaudeStateDoc = {};
  if (existing !== undefined) {
    try {
      doc = JSON.parse(existing) as ClaudeStateDoc;
    } catch {
      // A corrupt state file would make claude re-onboard anyway; fail-closed is
      // wrong here (it would brick a session over cosmetic state), so start a
      // fresh seed and let claude rebuild what it needs.
      doc = {};
    }
  }

  let changed = existing === undefined;

  if (doc.hasCompletedOnboarding !== true) {
    doc.hasCompletedOnboarding = true;
    changed = true;
  }
  if (version !== '' && doc.lastOnboardingVersion === undefined) {
    doc.lastOnboardingVersion = version;
    changed = true;
  }
  if (doc.oauthAccount === undefined) {
    const acct = parseOauthAccount(env.CLAUDE_OAUTH_ACCOUNT_JSON);
    if (acct !== undefined) {
      doc.oauthAccount = acct;
      changed = true;
    }
  }
  doc.projects ??= {};
  const proj = doc.projects[workspaceDir];
  if (proj === undefined) {
    doc.projects[workspaceDir] = { ...WORKSPACE_TRUST_SEED };
    changed = true;
  } else if (proj.hasTrustDialogAccepted !== true) {
    proj.hasTrustDialogAccepted = true;
    changed = true;
  }

  if (changed) fs.writeFileSync(path, JSON.stringify(doc, null, 2), { mode: 0o600 });
}

/** Extract the oauthAccount object from the {"oauthAccount": {...}} envelope.
 * Identity is seed-only (not auth-critical), so bad material degrades to
 * undefined rather than failing boot; the credential path above is the
 * fail-closed one. */
function parseOauthAccount(raw: string | undefined): unknown {
  if (!raw) return undefined;
  try {
    const doc = JSON.parse(raw) as { oauthAccount?: unknown };
    if (doc.oauthAccount && typeof doc.oauthAccount === 'object') return doc.oauthAccount;
  } catch {
    /* fall through */
  }
  return undefined;
}

function readIfExists(fs: ClaudeConfigFs, path: string): string | undefined {
  try {
    return fs.readFileSync(path, 'utf8') as string;
  } catch {
    return undefined;
  }
}

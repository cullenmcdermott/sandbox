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
//   .credentials.json  — written ONLY when absent. The in-pod claude refreshes
//                        this file itself (rotating access tokens); overwriting
//                        a refreshed credential with stale Secret material
//                        would re-break auth, so presence always wins.
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

/** Read the installed claude version for the lastOnboardingVersion seed. Any
 * failure (binary missing in dev images, unexpected output) degrades to '' —
 * the field is then omitted and claude may show a what's-new note, which is
 * cosmetic, so this is deliberately not fail-closed. */
export function detectClaudeVersion(
  exec: (cmd: string, args: string[], opts: ExecFileSyncOptions) => Buffer | string = execFileSync,
): string {
  try {
    const out = exec('claude', ['--version'], { timeout: 10_000 }).toString();
    const m = /^(\d+\.\d+\.\d+)/.exec(out.trim());
    return m ? m[1] : '';
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
  const version = (opts.claudeVersion ?? detectClaudeVersion)();
  mergeStateSeed(dir, env, fs, opts.workspaceDir, version);
}

function materializeCredentials(dir: string, env: NodeJS.ProcessEnv, fs: ClaudeConfigFs): void {
  const path = join(dir, '.credentials.json');
  if (readIfExists(fs, path) !== undefined) return; // in-pod refresh wins; never clobber
  const raw = env.CLAUDE_CREDENTIALS_JSON;
  if (!raw) {
    throw new Error(
      'claude-pane: no .credentials.json on the PVC and no CLAUDE_CREDENTIALS_JSON in the env — session Secret material is missing',
    );
  }
  let doc: { claudeAiOauth?: { accessToken?: unknown } };
  try {
    doc = JSON.parse(raw) as typeof doc;
  } catch {
    throw new Error('claude-pane: CLAUDE_CREDENTIALS_JSON is not valid JSON');
  }
  if (typeof doc.claudeAiOauth?.accessToken !== 'string' || doc.claudeAiOauth.accessToken === '') {
    throw new Error('claude-pane: CLAUDE_CREDENTIALS_JSON carries no claudeAiOauth.accessToken');
  }
  // Write the Secret bytes verbatim (no re-serialization) so credential fields
  // this module does not model survive untouched.
  fs.writeFileSync(path, raw, { mode: 0o600 });
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

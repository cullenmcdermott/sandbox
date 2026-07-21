// Shared credential-materialization helpers for the supervise-only agent
// backends (codex, opencode). Both inject a full credential document as an env
// var (from a per-session Secret) and expect the runner to write it to a
// PVC-persisted on-disk store that the agent process reads — reconciling the
// operator's seed against a possibly-newer pod-side token refresh, failing
// closed when no usable credential is present, and NEVER logging the credential
// content.
//
// codex uses a WHOLE-FILE store keyed on a top-level `last_refresh` timestamp;
// its materializer (materializeCodexAuth) stays in codex.ts and consumes the
// shared AuthFs + writeAuthFile0600 here. opencode's auth store is a map of
// INDEPENDENT per-provider entries (api/oauth/wellknown) with NO timestamp, so
// its reconciliation is PER-ENTRY against recorded seed hashes
// (materializeOpencodeAuth, below).

import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

/** Injectable filesystem surface for the auth-materialization helpers so the
 * seeding + fail-closed logic is unit-testable off-pod (production uses node:fs).
 * A superset of codex's CodexAuthFs — both backends' materializers accept it. */
export interface AuthFs {
  readFileSync: typeof readFileSync;
  writeFileSync: typeof writeFileSync;
  mkdirSync: typeof mkdirSync;
}

/** The production filesystem surface (node:fs). */
export const realAuthFs: AuthFs = { readFileSync, writeFileSync, mkdirSync };

/** Write an auth/credential file with owner-only (0600) permissions. The log
 * line prints the `label` + `path` ONLY — never the `content` (these are
 * credential documents, or hashes thereof). */
export function writeAuthFile0600(fs: AuthFs, path: string, content: string, label: string): void {
  fs.writeFileSync(path, content, { mode: 0o600 });
  console.log(`${label} materialized at ${path} (mode 0600)`);
}

/** sha256 hex of a string. Used to fingerprint per-provider seed entries so an
 * unchanged seed is recognized across boots (and a pod-side refresh preserved). */
function sha256Hex(s: string): string {
  return createHash('sha256').update(s).digest('hex');
}

/** True when `v` is a plain (non-null, non-array) JSON object. */
function isJsonObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

/** Resolve opencode's on-disk auth store directory. opencode stores its auth at
 * `$XDG_DATA_HOME/opencode` (or `$HOME/.local/share/opencode`); the pod points
 * XDG_DATA_HOME at a PVC-persisted dir so the store survives suspend/resume.
 * Exported so the boot wiring and the fail-closed gate agree on the path. */
export function opencodeStoreDir(env: NodeJS.ProcessEnv = process.env): string {
  const base =
    env.XDG_DATA_HOME && env.XDG_DATA_HOME.trim()
      ? env.XDG_DATA_HOME
      : join(env.HOME ?? homedir(), '.local', 'share');
  return join(base, 'opencode');
}

/** Path of opencode's on-disk auth store (`auth.json`). */
export function opencodeStorePath(env: NodeJS.ProcessEnv = process.env): string {
  return join(opencodeStoreDir(env), 'auth.json');
}

/** Path of the sidecar that records the per-entry seed hashes from the last
 * materialization (`{entryKey: "<sha256 hex>"}`). Used to tell an unchanged seed
 * (preserve a pod-side refresh) from an operator reseed (take the new seed). */
function opencodeSeedHashesPath(env: NodeJS.ProcessEnv = process.env): string {
  return join(opencodeStoreDir(env), 'auth.json.seed-hashes');
}

/** Provider entry key -> the env var opencode auto-detects for that provider,
 * used as the fail-closed fallback when the on-disk store lacks the entry. */
const OPENCODE_PROVIDER_FALLBACK_ENV: Record<string, string> = {
  anthropic: 'ANTHROPIC_API_KEY',
  openai: 'OPENAI_API_KEY',
  opencode: 'OPENCODE_API_KEY',
};

/**
 * Materialize the opencode auth store from env `OPENCODE_AUTH_JSON` (the full
 * `auth.json` document the k8s side injects from a per-session Secret) into the
 * PVC-persisted store dir, then reconcile it PER PROVIDER ENTRY against what is
 * already on disk.
 *
 * Unlike codex, opencode's store is a map of independent provider entries with
 * no timestamp, so we cannot compare a single `last_refresh`. Instead we record,
 * in a sidecar, the sha256 of every seed entry we last materialized. On the next
 * boot each provider reconciles on its own:
 *   - disk lacks the entry            -> take the seed (new provider),
 *   - seed entry UNCHANGED vs recorded -> KEEP disk (may be a newer pod-side
 *                                         token refresh; refresh-preserving),
 *   - seed entry CHANGED vs recorded   -> take the seed (operator reseeded).
 *     The no-record case (unparseable/absent sidecar) is treated as CHANGED so
 *     the seed wins — matching codex's seed-wins-on-unknown posture; it
 *     self-corrects once the sidecar exists.
 * Entries present ONLY on disk are always preserved. The store is rewritten only
 * when the merge changes the on-disk bytes (avoid churning the PVC); the sidecar
 * is rewritten whenever the recorded seed hashes changed. Credential content is
 * NEVER logged.
 *
 * When OPENCODE_AUTH_JSON is absent, this touches nothing — the provider-API-key
 * env path (buildOpencodeConfig + assertOpencodeAuthUsable's fallback) applies.
 * A seed that does not parse to a JSON object throws (operator error; the
 * message never echoes the content). `fs` is injectable for tests.
 */
export function materializeOpencodeAuth(env: NodeJS.ProcessEnv, fs: AuthFs): void {
  const seedRaw = env.OPENCODE_AUTH_JSON;
  if (seedRaw === undefined || seedRaw === '') {
    // No injected auth document: the single-provider-API-key path still applies.
    return;
  }

  // Parse + validate the seed. A malformed seed is operator error — fail loudly,
  // but NEVER echo the (credential) content: a JSON.parse error message quotes a
  // snippet of the input, so the caught error is deliberately discarded.
  let seed: Record<string, unknown> | undefined;
  try {
    const parsed: unknown = JSON.parse(seedRaw);
    if (isJsonObject(parsed)) seed = parsed;
  } catch {
    seed = undefined;
  }
  if (seed === undefined) {
    throw new Error(
      'OPENCODE_AUTH_JSON is not a JSON object of provider auth entries — ' +
        'cannot materialize the opencode auth store (the value is not logged)',
    );
  }

  const storeDir = opencodeStoreDir(env);
  const storePath = opencodeStorePath(env);
  const seedHashesPath = opencodeSeedHashesPath(env);
  fs.mkdirSync(storeDir, { recursive: true });

  // The per-entry seed hashes we would record for THIS seed (one per provider).
  const seedHashes: Record<string, string> = {};
  for (const [k, v] of Object.entries(seed)) {
    seedHashes[k] = sha256Hex(JSON.stringify(v));
  }
  const seedHashesStr = JSON.stringify(seedHashes);

  // Read the on-disk store.
  let diskRaw: string | undefined;
  try {
    diskRaw = fs.readFileSync(storePath, 'utf8') as string;
  } catch {
    diskRaw = undefined; // absent/unreadable
  }
  let disk: Record<string, unknown> | undefined;
  if (diskRaw !== undefined) {
    try {
      const parsed: unknown = JSON.parse(diskRaw);
      disk = isJsonObject(parsed) ? parsed : undefined;
    } catch {
      disk = undefined; // unparseable
    }
  }

  // Disk absent or unparseable → seed it wholesale (the operator document is the
  // only source of truth we have) and record its hashes.
  if (disk === undefined) {
    writeAuthFile0600(fs, storePath, seedRaw, 'opencode: auth.json');
    writeAuthFile0600(fs, seedHashesPath, seedHashesStr, 'opencode: auth.json.seed-hashes');
    return;
  }

  // Recorded seed hashes from the last materialization (unparseable/absent → none).
  let recorded: Record<string, string> = {};
  try {
    const parsed: unknown = JSON.parse(fs.readFileSync(seedHashesPath, 'utf8') as string);
    if (isJsonObject(parsed)) recorded = parsed as Record<string, string>;
  } catch {
    recorded = {};
  }

  // Start from disk so provider entries only on disk are always preserved.
  const merged: Record<string, unknown> = { ...disk };
  for (const [k, seedEntry] of Object.entries(seed)) {
    if (!(k in disk)) {
      merged[k] = seedEntry; // new provider the operator added — take it
    } else if (recorded[k] === seedHashes[k]) {
      // Seed for k is UNCHANGED since we last materialized it; the on-disk entry
      // may be a newer pod-side refresh, so KEEP disk (merged[k] is already it).
    } else {
      // Seed for k differs from what we recorded (operator reseeded), OR we have
      // no record for k. Take the seed. The no-record case matches codex's
      // seed-wins-on-unknown posture and self-corrects once the sidecar exists.
      merged[k] = seedEntry;
    }
  }

  // Rewrite the store only when the merge actually changed the on-disk bytes.
  const mergedStr = JSON.stringify(merged, null, 2);
  if (mergedStr !== diskRaw) {
    writeAuthFile0600(fs, storePath, mergedStr, 'opencode: auth.json');
  }
  // Update the recorded seed hashes whenever they changed, so a future boot
  // recognizes an unchanged seed (preserving a pod-side refresh) and the
  // no-record entries above stop re-winning.
  if (seedHashesStr !== JSON.stringify(recorded)) {
    writeAuthFile0600(fs, seedHashesPath, seedHashesStr, 'opencode: auth.json.seed-hashes');
  }
}

/**
 * FAIL-CLOSED gate: prove a usable opencode provider credential is present
 * before we let `opencode serve` start, mirroring materializeCodexAuth's refusal.
 * The selected provider is env `SANDBOX_OPENCODE_PROVIDER` (anthropic|openai|
 * opencode, default anthropic). Usable when either its fallback API-key env var
 * is non-empty (opencode auto-detects it) OR the on-disk auth store parses and
 * carries that provider's entry. Otherwise throw — an opencode pod with no
 * provider credential would start and fail every turn opaquely. The message
 * names the store path, the missing entry key, and the remediation, but NEVER
 * any credential content. `fs` is injectable for tests.
 */
export function assertOpencodeAuthUsable(env: NodeJS.ProcessEnv, fs: AuthFs): void {
  const entryKey = env.SANDBOX_OPENCODE_PROVIDER || 'anthropic';
  const fallbackVar = OPENCODE_PROVIDER_FALLBACK_ENV[entryKey] ?? 'ANTHROPIC_API_KEY';

  // Fallback provider API key present → opencode auto-detects it from env
  // (buildOpencodeConfig references {env:VAR}); no on-disk store entry required.
  if (env[fallbackVar]) return;

  // Otherwise require the selected provider's entry in the on-disk auth store.
  const storePath = opencodeStorePath(env);
  let haveEntry = false;
  try {
    const parsed: unknown = JSON.parse(fs.readFileSync(storePath, 'utf8') as string);
    haveEntry = isJsonObject(parsed) && entryKey in parsed;
  } catch {
    haveEntry = false; // absent/unparseable store
  }
  if (haveEntry) return;

  throw new Error(
    `refusing to start \`opencode serve\`: no \`${entryKey}\` entry in the opencode auth store at ${storePath} ` +
      `and no ${fallbackVar} fallback — an opencode pod with no provider credential fails every turn opaquely. ` +
      `Re-create the session after \`opencode auth login ${entryKey}\` locally, or use the shared-Secret ${fallbackVar} fallback`,
  );
}

// Operator bootstrap-file materialization (part A of
// docs/design-pod-bootstrap-and-tool-injection.md).
//
// The k8s side projects a session's operator-supplied bootstrap files into the
// pod as a READ-ONLY Secret volume at $SANDBOX_BOOTSTRAP_DIR: a `manifest.json`
// (a JSON array of {path, mode}, index-aligned) plus one numbered file per
// entry holding that file's content. index.ts runs materializeBootstrapFiles()
// at boot BEFORE any agent starts, so the agent finds its tool config / CLAUDE.md
// / skill already in place.
//
// The content rides a mounted Secret, NEVER an env var, so it can't leak into an
// agent child's process environment (unlike a SecretKeyRef, which sanitizedExecEnv
// passes through to opencode/codex). The materializer re-validates every manifest
// path against the pod HOME / /session/state roots — defense in depth against a
// tampered manifest — mirroring exec.ts resolveWorkspaceDir's traversal refusal.
//
// Seed precedence mirrors opencode's per-entry, refresh-preserving reconciliation
// (agent-auth.ts materializeOpencodeAuth): a per-file seed-hash sidecar on the PVC
// lets a pod restart KEEP a file the agent legitimately edited, while an operator
// who ROTATED the seed still wins. File content is NEVER logged.

import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, normalize } from 'node:path';

/** Injectable filesystem surface so the materializer is unit-testable off-pod
 * (production uses node:fs). A superset of the auth helpers' AuthFs — the same
 * three calls plus nothing extra. */
export interface BootstrapFs {
  readFileSync: typeof readFileSync;
  writeFileSync: typeof writeFileSync;
  mkdirSync: typeof mkdirSync;
}

/** The production filesystem surface (node:fs). */
export const realBootstrapFs: BootstrapFs = { readFileSync, writeFileSync, mkdirSync };

/** Pod HOME fallback when $HOME is unset (the runner runs as root). Keep in sync
 * with the client's podHomeDir + the k8s bootstrap path validation. */
const DEFAULT_HOME = '/root';
/** PVC-persisted state root — the second allowed bootstrap root. */
const STATE_ROOT = '/session/state';
/** Where the per-file seed-hash sidecar persists (on the PVC, survives restarts).
 * Distinct from any workspace/agent tree so it never collides with a bootstrap
 * target path. */
const SIDECAR_PATH = '/session/state/sandbox/bootstrap-seed-hashes.json';

/** One manifest entry as written by the k8s side (internal/k8s bootstrapManifestEntry). */
interface ManifestEntry {
  path: string;
  mode?: number;
}

/** sha256 hex of a buffer/string — fingerprints a seed so an unchanged seed is
 * recognized across boots (and a pod-side edit preserved). */
function sha256Hex(s: string): string {
  return createHash('sha256').update(s).digest('hex');
}

/** Resolve $HOME the way the runner's other helpers do (env, then the root
 * fallback). */
function homeDir(env: NodeJS.ProcessEnv): string {
  return env.HOME && env.HOME.trim() ? env.HOME : DEFAULT_HOME;
}

/** True when `p` sits STRICTLY below `root` (a file under the dir, never the dir
 * itself). The trailing separator makes it a true path-prefix test. */
function withinRoot(p: string, root: string): boolean {
  return p.startsWith(root + '/');
}

/**
 * Resolve a manifest path to its absolute pod target, re-validating fail-closed:
 * "~/"-relative expands against $HOME, absolute is taken as-is, and the normalized
 * result must sit strictly below $HOME or /session/state — anything else (a
 * relative path, or a ".." escape that normalize() collapses out of the roots) is
 * rejected. Returns null on rejection (the caller skips that file, loudly). The
 * error path names only the path, never the content.
 */
export function resolveBootstrapTarget(rawPath: string, home: string): string | null {
  let abs: string;
  if (rawPath === '~' || rawPath.startsWith('~/')) {
    abs = home + rawPath.slice(1);
  } else if (rawPath.startsWith('/')) {
    abs = rawPath;
  } else {
    return null; // not absolute or "~/"-relative
  }
  const cleaned = normalize(abs);
  if (!withinRoot(cleaned, home) && !withinRoot(cleaned, STATE_ROOT)) {
    return null; // escapes the allowed roots (incl. ".." traversal)
  }
  return cleaned;
}

/** Parse the sidecar map (target path -> last-materialized seed hash). Any read
 * or parse failure yields an empty map, matching opencode's seed-wins-on-unknown
 * posture (a missing record makes the seed win, self-correcting once written). */
function readSidecar(fs: BootstrapFs): Record<string, string> {
  try {
    const parsed: unknown = JSON.parse(fs.readFileSync(SIDECAR_PATH, 'utf8') as string);
    if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, string>;
    }
  } catch {
    /* absent / unparseable — treat as no record */
  }
  return {};
}

/**
 * Materialize operator bootstrap files from the mounted Secret volume named by
 * $SANDBOX_BOOTSTRAP_DIR, write-if-changed with per-file seed-hash reconciliation.
 * A no-op when the marker is unset (no bootstrap files) or the manifest is absent
 * (volume not mounted). `fs` is injectable for tests.
 *
 * Per file, against the on-disk target:
 *   - absent                         -> write the seed, record its hash,
 *   - on-disk == seed                -> no write (avoid churning the PVC),
 *   - on-disk != seed, seed UNCHANGED
 *     since last materialization      -> KEEP disk (a pod-side agent edit),
 *   - on-disk != seed, seed CHANGED
 *     (or no record)                  -> write the seed (operator rotated it).
 * The sidecar is rewritten only when a recorded hash changed. Content is never
 * logged; a rejected path logs a warning naming the path only.
 */
export function materializeBootstrapFiles(
  env: NodeJS.ProcessEnv = process.env,
  fs: BootstrapFs = realBootstrapFs,
): void {
  const dir = env.SANDBOX_BOOTSTRAP_DIR;
  if (!dir) return; // no bootstrap files for this session

  let manifestRaw: string;
  try {
    manifestRaw = fs.readFileSync(join(dir, 'manifest.json'), 'utf8') as string;
  } catch {
    return; // volume not mounted / manifest absent — nothing to materialize
  }

  let manifest: ManifestEntry[];
  try {
    const parsed: unknown = JSON.parse(manifestRaw);
    if (!Array.isArray(parsed)) throw new Error('manifest is not an array');
    manifest = parsed as ManifestEntry[];
  } catch (err) {
    // Operator/provisioning error — fail loudly but never echo content.
    throw new Error(
      `bootstrap: manifest.json at ${dir} is not a JSON array of {path,mode} entries: ${(err as Error).message}`,
    );
  }

  const home = homeDir(env);
  const recorded = readSidecar(fs);
  const nextHashes: Record<string, string> = {};
  let sidecarChanged = false;

  for (let i = 0; i < manifest.length; i++) {
    const entry = manifest[i];
    const target = resolveBootstrapTarget(entry.path, home);
    if (target === null) {
      console.warn(`bootstrap: skipping file ${i}: path ${entry.path} is not inside $HOME or ${STATE_ROOT}`);
      continue;
    }

    let seed: string;
    try {
      seed = fs.readFileSync(join(dir, String(i)), 'utf8') as string;
    } catch {
      console.warn(`bootstrap: skipping ${target}: content key bootstrap-${i} is not present in the mount`);
      continue;
    }
    const seedHash = sha256Hex(seed);
    nextHashes[target] = seedHash;
    if (recorded[target] !== seedHash) sidecarChanged = true;

    let onDisk: string | undefined;
    try {
      onDisk = fs.readFileSync(target, 'utf8') as string;
    } catch {
      onDisk = undefined; // absent
    }

    const mode = typeof entry.mode === 'number' && entry.mode > 0 ? entry.mode : 0o644;

    if (onDisk === undefined) {
      writeBootstrapFile(fs, target, seed, mode); // new file — seed it
    } else if (onDisk === seed) {
      // Identical — skip the write (avoids churning the PVC).
    } else if (recorded[target] === seedHash) {
      // The seed is UNCHANGED since we last materialized it, so the divergence is
      // a pod-side agent edit — KEEP disk (refresh-preserving).
    } else {
      // The seed CHANGED (operator rotated it) or we have no record — take the seed.
      writeBootstrapFile(fs, target, seed, mode);
    }
  }

  // Persist the recorded seed hashes when they changed, so a future boot
  // recognizes an unchanged seed (preserving a pod-side edit). Best-effort: a
  // sidecar write failure must not crash the boot (the seed just re-wins next time).
  if (sidecarChanged) {
    try {
      fs.mkdirSync(dirname(SIDECAR_PATH), { recursive: true });
      fs.writeFileSync(SIDECAR_PATH, JSON.stringify(nextHashes), { mode: 0o600 });
    } catch (err) {
      console.warn(`bootstrap: could not persist seed-hash sidecar: ${(err as Error).message}`);
    }
  }
}

/** Write one materialized file with its declared mode, creating parent dirs. The
 * log line names the path + mode ONLY — never the content. */
function writeBootstrapFile(fs: BootstrapFs, target: string, content: string, mode: number): void {
  fs.mkdirSync(dirname(target), { recursive: true });
  fs.writeFileSync(target, content, { mode });
  console.log(`bootstrap: materialized ${target} (mode ${mode.toString(8)})`);
}

// Entrypoint: initialize the event log, load session.json, emit the initial
// session.started event, and start the HTTP+SSE server.
//
// One sandbox = one runner pod = one Claude SDK session (spec 8.1). On pod
// resume the runner reloads session.json + events.db from the PVC and the
// next turn continues the same Claude session via resume.

import { openEventLog, appendEvent, closeEventLog } from './events.js';
import { loadConfig, loadSessionState, initRegistry, getRegistry, backendHasAutopilot } from './session.js';
import { startServer } from './server.js';
import { selectAgent } from './agent.js';
import { createAutopilot, type Autopilot } from './autopilot.js';
import { resolveWorkspaceDir } from './exec.js';
import { startOpencodeSupervisor, type OpencodeSupervisor } from './opencode.js';
import { warmupOpencodeSession } from './opencode-turn.js';
import { startOpencodeObserver, type OpencodeObserver } from './opencode-observer.js';

// Seconds before SIGKILL, reported in session.terminating so the TUI can show
// an accurate countdown. Mirrors the pod's terminationGracePeriodSeconds.
const GRACE_SECONDS = parseInt(process.env.TERMINATION_GRACE_SECONDS ?? '60', 10);

let shuttingDown = false;

// Set for opencode-server sessions: the supervised `opencode serve` child.
let opencode: OpencodeSupervisor | null = null;

// Set for opencode-server sessions: the always-on passive metrics observer that
// turns interactive opencode turns into normalized SSE events (Phase 4).
let observer: OpencodeObserver | null = null;

/**
 * Graceful shutdown on SIGTERM (node drain/reboot, suspend, eviction). Warn
 * attached clients via session.terminating, abort in-flight turns, flush the
 * event log, then exit so the pod can be rescheduled. The client reconnects
 * once the pod is back (see docs/session-lifecycle.md).
 */
function shutdown(signal: string): void {
  if (shuttingDown) return;
  shuttingDown = true;

  // Send SIGTERM to the supervised opencode child immediately so it gets the
  // full grace window; we await its exit below (bounded by STOP_GRACE_MS) before
  // process.exit so we never orphan it (O5).
  const opencodeStopped = opencode ? opencode.stop() : Promise.resolve();
  // Stop the passive observer too (closes its event-stream subscription) so its
  // reconnect loop doesn't keep the process alive past the grace window.
  const observerStopped = observer ? observer.stop() : Promise.resolve();

  let turnsAborted = 0;
  try {
    const reg = getRegistry();
    for (const turn of reg.activeTurns.values()) {
      turn.abort.abort();
      turnsAborted++;
    }
    appendEvent(reg.state.sandbox_session_id, undefined, 'session.terminating', {
      reason: `pod terminating (${signal})`,
      graceSeconds: GRACE_SECONDS,
      turnsAborted,
    });
  } catch {
    /* registry may not be initialized yet */
  }

  // Give SSE writes a moment to flush to attached clients, then wait for the
  // opencode child to exit (so it never outlives us) and close cleanly.
  setTimeout(() => {
    void Promise.all([opencodeStopped, observerStopped]).finally(() => {
      closeEventLog();
      process.exit(0);
    });
  }, 500);
}

function main(): void {
  const cfg = loadConfig();
  openEventLog();

  const { state, bootEvents } = loadSessionState(cfg);
  const reg = initRegistry(state);

  // Emit session.started on (re)boot so live SSE clients see the session come
  // up. On resume this is a fresh event after the persisted history; replay via
  // after=0 still yields the full original sequence.
  //
  // This must conform to SessionStartedPayload (model, cwd, [claudeSessionId]) —
  // the same shape the SDK init path emits — so the Go TUI's status line reads a
  // consistent payload (transcript.go reads Model + Cwd). The off-schema
  // backend/projectPath/status fields the reboot emit used to carry are dropped:
  // backend/projectPath are not part of session.started, and the (reconciled)
  // status is already surfaced via /status and session.status_changed. cwd is
  // derived from the project path the same way the SDK turn resolves it; model
  // comes from session.json when a prior turn captured it (empty on first boot,
  // which the consumer guards with `if p.Model != ""`).
  let bootCwd = '';
  try {
    bootCwd = resolveWorkspaceDir(reg.state.project_path);
  } catch {
    // projectPath escapes the workspace root (should not happen on a valid pod):
    // omit cwd rather than crash the boot emit.
  }
  // D2: if the pod died mid-turn, loadSessionState coerced the persisted 'busy'
  // status to 'idle' but the event log still ends with an orphaned turn's events
  // and no terminal. Append that terminal (turn.interrupted + status_changed
  // idle) BEFORE the boot session.started so a client replaying with after=0
  // sees the turn end instead of spinning forever. Normal boots yield [].
  for (const ev of bootEvents) {
    appendEvent(reg.state.sandbox_session_id, ev.turnId, ev.type, ev.payload);
  }

  appendEvent(reg.state.sandbox_session_id, undefined, 'session.started', {
    model: reg.state.model ?? '',
    cwd: bootCwd,
    ...(reg.state.claude_session_id ? { agentSessionId: reg.state.claude_session_id } : {}),
  });

  // Resolve the agent backend up front so an unknown SANDBOX_BACKEND fails at
  // startup rather than on the first turn. Both shipping backends (claude-sdk,
  // opencode-server) implement the turn seam; null is reserved for any future
  // supervise-only backend, whose /turns route then 409s.
  const agent = selectAgent(cfg.backend);

  // Autopilot driver (server-side /loop-/goal loop). Claude-backend-first: only
  // backends with a runner-side driver get one; the PUT/DELETE endpoint 409s for
  // the rest, and the CLI falls back to its local tea.Tick driver. Created after
  // session.started is emitted so the boot re-arm's `armed` event replays AFTER
  // it. bootReArm re-emits armed + reschedules for a still-armed persisted spec
  // (H1); it is a no-op for a stopped or absent spec.
  let autopilot: Autopilot | null = null;
  if (agent && backendHasAutopilot(cfg.backend)) {
    autopilot = createAutopilot(cfg, agent);
    autopilot.bootReArm();
  }

  process.on('SIGTERM', () => shutdown('SIGTERM'));
  process.on('SIGINT', () => shutdown('SIGINT'));

  // opencode-server sessions: the runner stays the control plane and supervises
  // a child `opencode serve`; the local `opencode attach` client drives it. The
  // claude SDK turn path (server.ts /turns) is simply unused for these sessions.
  if (reg.state.backend === 'opencode-server') {
    opencode = startOpencodeSupervisor();
    // Pre-create the opencode session so `opencode attach --continue` finds a
    // valid session on first launch rather than falling back to a "dummy" ID.
    void warmupOpencodeSession();
    // Start the always-on metrics observer so interactive opencode turns surface
    // live status/title/cost/tools on the runner SSE channel (Phase 4). It owns
    // its own reconnect loop, so it must not block boot on serve readiness.
    observer = startOpencodeObserver();
  }

  startServer(agent, autopilot);
}

main();

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
import { shutdownInterruptedEvents } from './turns.js';
import { createAutopilot, type Autopilot } from './autopilot.js';
import { resolveWorkspaceDir } from './exec.js';
import { startOpencodeSupervisor, type OpencodeSupervisor } from './opencode.js';
import { warmupOpencodeSession } from './opencode-turn.js';
import { startOpencodeObserver, type OpencodeObserver } from './opencode-observer.js';
import { materializeCodexAuth, startCodexSupervisor, type CodexSupervisor } from './codex.js';
import { startCodexObserver, type CodexObserver } from './codex-observer.js';
import { startClaudePaneSupervisor, type ClaudePaneSupervisor } from './claude-pane.js';
import { materializeClaudePaneConfig } from './claude-config.js';
import { startBootTrace } from './trace.js';

// Seconds before SIGKILL, reported in session.terminating so the TUI can show
// an accurate countdown. Mirrors the pod's terminationGracePeriodSeconds.
const GRACE_SECONDS = parseInt(process.env.TERMINATION_GRACE_SECONDS ?? '60', 10);

let shuttingDown = false;

// Set for opencode-server sessions: the supervised `opencode serve` child.
let opencode: OpencodeSupervisor | null = null;

// Set for opencode-server sessions: the always-on passive metrics observer that
// turns interactive opencode turns into normalized SSE events (Phase 4).
let observer: OpencodeObserver | null = null;

// Set for codex-app-server sessions: the supervised `codex app-server` child and
// its always-on passive metrics observer (the codex analogue of the opencode
// supervisor/observer pair above).
let codex: CodexSupervisor | null = null;
let codexObserver: CodexObserver | null = null;

// Set for claude-pane sessions: the runner-owned interactive `claude` PTY
// supervisor, spawned lazily on the first pane attach (claude-pane.ts).
let claudePane: ClaudePaneSupervisor | null = null;

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
  // Same ordering for the codex supervisor/observer: SIGTERM the child now so it
  // gets the full grace window, and stop the observer so its ws reconnect loop
  // doesn't outlive us. Both are awaited in the Promise.all below.
  const codexStopped = codex ? codex.stop() : Promise.resolve();
  const codexObserverStopped = codexObserver ? codexObserver.stop() : Promise.resolve();
  // Kill the interactive claude-pane child now so it isn't orphaned past the pod
  // grace window (stop() is synchronous — a PTY kill needs no drain). The pane
  // uuid is persisted, so the next boot respawns with --resume.
  claudePane?.stop();

  let turnsAborted = 0;
  try {
    const reg = getRegistry();
    const activeTurnIds = [...reg.activeTurns.keys()];
    for (const turn of reg.activeTurns.values()) {
      turn.abort.abort();
      turnsAborted++;
    }
    // [V18] Append turn.interrupted for each aborted turn BEFORE
    // session.terminating. This shutdown is the interrupt initiator, and both
    // agents' runTurn emit nothing terminal on abort (R3), so without this a
    // mid-turn graceful suspend leaves the turn with no terminal event and
    // replay-after-resume spins its tool cards forever. See shutdownInterruptedEvents.
    for (const ev of shutdownInterruptedEvents(activeTurnIds, signal)) {
      appendEvent(reg.state.sandbox_session_id, ev.turnId, 'turn.interrupted', ev.payload);
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
    void Promise.all([opencodeStopped, observerStopped, codexStopped, codexObserverStopped]).finally(() => {
      closeEventLog();
      process.exit(0);
    });
  }, 500);
}

function main(): void {
  // §10 observability: per-phase boot timings (trace: boot boot.<phase> …) so a
  // slow pod start is attributable. No-op unless SANDBOX_TRACE is set.
  const boot = startBootTrace();
  const cfg = loadConfig();
  openEventLog();
  boot.phase('event_log');

  const { state, bootEvents } = loadSessionState(cfg);
  boot.phase('session_state');
  const reg = initRegistry(state);
  boot.phase('registry');

  // Emit session.started on (re)boot so live SSE clients see the session come
  // up. On resume this is a fresh event after the persisted history; replay via
  // after=0 still yields the full original sequence.
  //
  // This must conform to SessionStartedPayload (model, cwd, [agentSessionId]) —
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

  // codex-app-server sessions: same supervise-only shape as opencode. Materialize
  // the ChatGPT-OAuth auth.json (fail-closed if no credential), supervise `codex
  // app-server` on its loopback ws, and start the passive observer so interactive
  // codex turns surface live status/tokens/tools on the runner SSE channel. The
  // interactive `codex` TUI attaches to the same app-server over a port-forward.
  if (reg.state.backend === 'codex-app-server') {
    materializeCodexAuth();
    codex = startCodexSupervisor();
    // Owns its own ws reconnect loop, so it must not block boot on server readiness.
    codexObserver = startCodexObserver();
  }

  // claude-pane sessions: the runner owns an interactive `claude` PTY child that
  // the CLI drives over the GET /sessions/:id/pane WebSocket. The supervisor is
  // built here (registering its idle-activity probe) but spawns the child lazily
  // on the first attach, so a detached pod runs no idle TUI. selectAgent returns
  // null for this backend, so the /turns path 409s like any supervise-only one.
  if (reg.state.backend === 'claude-pane') {
    // Materialize .credentials.json (only-if-absent — in-pod refresh wins) and
    // the seamless-start .claude.json seed from the session Secret BEFORE the
    // supervisor exists, so the first attach's lazy spawn finds a fully
    // authenticated, trust-seeded config dir. Fail-closed on missing/invalid
    // credential material (crash boot visibly, mirroring materializeCodexAuth).
    materializeClaudePaneConfig({ workspaceDir: resolveWorkspaceDir(cfg.projectPath) });
    claudePane = startClaudePaneSupervisor(cfg);
  }
  // boot_prep covers everything between registry init and the listen call:
  // orphaned-turn terminals, the session.started emit, agent/autopilot setup,
  // and (opencode) supervisor/observer spawn initiation.
  boot.phase('boot_prep');

  startServer(
    agent,
    autopilot,
    () => {
      // Closes when the socket is accepting (the listen callback), so boot.listen
      // covers the real bind, and boot.total is boot-start → ready-to-serve.
      boot.phase('listen');
      boot.done();
    },
    claudePane,
  );
}

main();

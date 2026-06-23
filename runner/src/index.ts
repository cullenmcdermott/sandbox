// Entrypoint: initialize the event log, load session.json, emit the initial
// session.started event, and start the HTTP+SSE server.
//
// One sandbox = one runner pod = one Claude SDK session (spec 8.1). On pod
// resume the runner reloads session.json + events.db from the PVC and the
// next turn continues the same Claude session via resume.

import { openEventLog, appendEvent } from './events.js';
import { loadConfig, loadSessionState, initRegistry } from './session.js';
import { startServer } from './server.js';

function main(): void {
  const cfg = loadConfig();
  openEventLog();

  const state = loadSessionState(cfg);
  const reg = initRegistry(state);

  // Emit session.started on (re)boot so live SSE clients see the session come
  // up. On resume this is a fresh event after the persisted history; replay
  // via after=0 still yields the full original sequence.
  appendEvent(reg.state.sandbox_session_id, undefined, 'session.started', {
    backend: reg.state.backend,
    projectPath: reg.state.project_path,
    status: reg.state.status,
    claudeSessionId: reg.state.claude_session_id,
  });

  startServer();
}

main();

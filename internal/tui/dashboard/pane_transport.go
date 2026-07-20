package dashboard

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// PaneTransport is the byte-stream seam an ExternalPane renders: raw terminal
// bytes in both directions plus geometry propagation. Two implementations
// exist today — the local child-process OS PTY (`opencode attach`) and the
// claude-pane WebSocket stream (client.PaneStream satisfies this structurally;
// internal/cli hands it across the seam so this package never imports client).
// The same seam is what a future codex pane plugs into.
//
// Read is driven from the pane's single reader goroutine; Write/Resize/Close
// may run concurrently with it. Read returns io.EOF for a clean end of stream
// and a descriptive error for an abnormal one (child exit status, pane
// preemption) — ExternalPane surfaces that error when it bounces back to the
// dashboard.
type PaneTransport interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

// PaneDial establishes a PaneTransport at the given initial geometry. It runs
// synchronously inside ExternalPane.Init — a local spawn or a localhost
// WebSocket dial over an already-established port-forward, both fast — and a
// failure surfaces as the pane's exit error (the App falls back to the
// dashboard and shows it inline).
type PaneDial func(cols, rows int) (PaneTransport, error)

// childProcTransport adapts a local child process running in an OS PTY to the
// PaneTransport seam (the opencode pane). A Read that fails because the child
// exited (EIO on the master) reaps the child and reports the exit: clean exit
// → io.EOF, failure → "<name> exited: …" carrying the wait result, so a child
// that dies on startup (attach hitting a not-yet-ready server, an auth/version
// mismatch) reports a reason instead of silently bouncing to the dashboard.
type childProcTransport struct {
	name string // human label for exit errors, e.g. "opencode attach"
	ptmx *os.File
	cmd  *exec.Cmd

	// waitOnce funnels the post-exit Read and Close into exactly one Wait, so
	// the child is reaped once (O1) and a double Wait can't race.
	waitOnce sync.Once
	waitErr  error
}

func (t *childProcTransport) wait() error {
	t.waitOnce.Do(func() { t.waitErr = t.cmd.Wait() })
	return t.waitErr
}

func (t *childProcTransport) Read(b []byte) (int, error) {
	n, err := t.ptmx.Read(b)
	if err == nil {
		return n, nil
	}
	// The master read fails when the child exits (EIO) or Close tore the master
	// down; either way the stream is over — reap and report the reason.
	if werr := t.wait(); werr != nil {
		return n, fmt.Errorf("%s exited: %w", t.name, werr)
	}
	return n, io.EOF
}

func (t *childProcTransport) Write(b []byte) (int, error) { return t.ptmx.Write(b) }

// Resize propagates the pane geometry to the PTY, which delivers SIGWINCH so
// the child repaints at the new size.
func (t *childProcTransport) Resize(cols, rows int) error {
	return pty.Setsize(t.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Close kills the child and releases the PTY: master first (so a blocked
// reader unblocks), then SIGKILL + reap. SIGKILL is uncatchable, so wait()
// returns promptly and the child never lingers as a <defunct> zombie (O1); on
// an already-exited child the Kill error is irrelevant and wait() is a no-op.
func (t *childProcTransport) Close() error {
	_ = t.ptmx.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	_ = t.wait()
	return nil
}

// opencodeAttachCmd builds the local `opencode attach` invocation. Auth: the
// server URL is positional; basic-auth user via -u and the password via
// OPENCODE_SERVER_PASSWORD in the env (never argv, so it stays out of the host
// process list).
//
// --continue resumes the prior conversation. The opencode session lives on the
// server (XDG_DATA_HOME on the pod's PVC, durable across suspend/resume), but
// the attach client must be told to continue it or it opens an empty session —
// so without this flag every (re)attach loses history. One session per pod
// means "the last session" is unambiguously the user's previous conversation;
// on the first-ever attach (none yet) opencode falls back to a new session.
//
// Split from the dialer so this argv/env contract is unit-testable without
// spawning a real client.
func opencodeAttachCmd(creds OpencodeCreds) *exec.Cmd {
	cmd := exec.Command("opencode", "attach", creds.URL, "-u", creds.Username, "--continue")
	cmd.Env = append(os.Environ(), "OPENCODE_SERVER_PASSWORD="+creds.Password, "TERM=xterm-256color")
	return cmd
}

// dialOpencodePane returns the PaneDial that spawns the local `opencode
// attach` client for an opencode-server session. The runner supervises
// `opencode serve` in the pod; the local child — reached over a localhost
// port-forward — is the interactive TUI.
func dialOpencodePane(creds OpencodeCreds) PaneDial {
	return func(cols, rows int) (PaneTransport, error) {
		// Pre-flight: the local `opencode` client must be installed (and version-
		// matched to the pod's `opencode serve`). Without it the spawn would fail
		// with a bare ENOENT; surface an actionable message instead.
		if _, err := exec.LookPath("opencode"); err != nil {
			return nil, fmt.Errorf("opencode CLI not found on PATH — install it locally (Nix) to attach to opencode sessions")
		}

		cmd := opencodeAttachCmd(creds)
		ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
		if err != nil {
			return nil, fmt.Errorf("opencode attach: %w", err)
		}
		return &childProcTransport{name: "opencode attach", ptmx: ptmx, cmd: cmd}, nil
	}
}

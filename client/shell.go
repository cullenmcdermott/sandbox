package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// SSHTarget is the connection material for reaching a session's pod over SSH:
// a localhost port-forward endpoint, the login user, and the per-session
// OpenSSH identity file. It is the lower seam Session.Shell is built on, and is
// exposed directly so a non-interactive consumer can drive their own tooling
// (scp, rsync, a one-off `ssh <target> <cmd>`, a port tunnel) that the one-call
// interactive Shell doesn't cover — e.g.
//
//	tgt, cleanup, _ := sess.SSHTarget(ctx)
//	defer cleanup()
//	exec.Command("ssh", "-i", tgt.IdentityFile, "-p", strconv.Itoa(tgt.Port),
//	    tgt.User+"@"+tgt.Host, "uname", "-a").Run()
//
// Host key verification is intentionally NOT performed by Shell (it uses
// ssh.InsecureIgnoreHostKey), and a consumer running their own ssh should pass
// StrictHostKeyChecking=no / UserKnownHostsFile=/dev/null to match: the
// transport is a localhost port-forward tunneled through the authenticated
// Kubernetes API server (so it is not exposed on any network), and the pod
// mints an ephemeral host key each boot, so there is no stable key to pin. Do
// not reuse the target over an untrusted network.
type SSHTarget struct {
	// Host is the local end of the port-forward — always "127.0.0.1".
	Host string
	// Port is the local forwarded port bound to the pod's sshd.
	Port int
	// User is the SSH login user in the pod — "root" (matches the Mutagen sync
	// transport; the pod's sshd permits root by key only).
	User string
	// IdentityFile is the path to the per-session OpenSSH private key (the same
	// key baked into the pod's per-session Secret and used for Mutagen sync).
	IdentityFile string
}

// SSHTarget opens a dedicated port-forward to the session's in-pod sshd and
// returns the connection material plus a cleanup func that tears the forward
// down (mirrors DialRunner). It forwards ONLY the SSH port — no runner HTTP or
// opencode forward — so it is cheap to open for a scp/rsync one-off.
//
// It does NOT resume a suspended pod or wait for pod readiness: the pod must
// already be running (PortForward fails otherwise). A caller reaching a
// possibly-suspended session should Client.Resume + wait for readiness first
// (as the `sandbox shell` command does), or go through Session.Connect.
func (s *Session) SSHTarget(ctx context.Context) (*SSHTarget, func(), error) {
	privPath, _, err := s.c.ensureSSHKey(string(s.ref.ID))
	if err != nil {
		return nil, nil, fmt.Errorf("prepare ssh key: %w", err)
	}
	// SSH-only forward: the runner HTTP and opencode ports are pure waste for a
	// shell/scp session, mirroring DialRunner's runner-only forward in reverse.
	handles, err := s.c.backend.PortForward(ctx, s.ref, []session.PortSpec{{Local: 0, Remote: k8s.SSHPort()}})
	if err != nil {
		return nil, nil, fmt.Errorf("ssh port-forward: %w", err)
	}
	if len(handles) == 0 {
		return nil, nil, fmt.Errorf("ssh port-forward: no handle returned")
	}
	cleanup := func() {
		for _, h := range handles {
			h.Close()
		}
	}
	tgt := &SSHTarget{
		Host:         "127.0.0.1",
		User:         "root",
		Port:         handles[0].LocalPort(),
		IdentityFile: privPath,
	}
	return tgt, cleanup, nil
}

// ShellOptions parameterizes Session.Shell. The zero value opens an interactive
// login shell wired to the current process stdio — exactly what `sandbox shell`
// needs.
type ShellOptions struct {
	// Command is the remote command to run. Empty (the default) opens the login
	// shell interactively. When set it is handed to the remote sshd's exec
	// (interpreted by the login shell, so `foo && bar` works).
	Command string
	// Stdin, Stdout, Stderr default to os.Stdin/os.Stdout/os.Stderr when nil. A
	// PTY (raw mode + resize forwarding) is allocated only when Stdin is an *os.File
	// backed by a terminal, so a piped/programmatic Stdin runs non-interactively.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Term is the TERM value advertised to the remote PTY. Empty uses the local
	// $TERM, falling back to "xterm-256color".
	Term string
}

// Shell opens a full interactive shell in the session's pod over SSH. It dials
// the in-pod sshd via SSHTarget, and — when Stdin is a terminal — allocates a
// remote PTY sized to the local terminal, puts the local terminal into raw
// mode, forwards window resizes (SIGWINCH), and streams stdio. It blocks until
// the remote command exits (or ctx is cancelled), then returns the remote exit
// code.
//
// A completed session — including a non-zero remote exit — returns that exit
// code with a nil error (the shell ran). A transport/setup failure returns a
// non-nil error and exit code -1. Cancelling ctx closes the connection and
// returns ctx.Err() with exit code -1.
//
// Like SSHTarget, Shell does not resume a suspended pod: the session must be
// running (see SSHTarget).
func (s *Session) Shell(ctx context.Context, opt ShellOptions) (int, error) {
	tgt, cleanup, err := s.SSHTarget(ctx)
	if err != nil {
		return -1, err
	}
	defer cleanup()
	return runSSHShell(ctx, tgt, opt)
}

// runSSHShell is the transport-agnostic interactive core: it takes a resolved
// SSHTarget (so the port-forward lifecycle is the caller's concern) and runs the
// PTY session. Split out from Shell so the target-resolution seam and the
// interactive machinery are separable.
func runSSHShell(ctx context.Context, tgt *SSHTarget, opt ShellOptions) (int, error) {
	stdin := opt.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := opt.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opt.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	keyBytes, err := os.ReadFile(tgt.IdentityFile)
	if err != nil {
		return -1, fmt.Errorf("read ssh key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return -1, fmt.Errorf("parse ssh key: %w", err)
	}

	cfg := &ssh.ClientConfig{
		User:            tgt.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // localhost forward; ephemeral pod host key (see SSHTarget)
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(tgt.Host, strconv.Itoa(tgt.Port))
	dialer := net.Dialer{Timeout: cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return -1, fmt.Errorf("dial ssh: %w", err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return -1, fmt.Errorf("ssh handshake: %w", err)
	}
	cl := ssh.NewClient(sshConn, chans, reqs)
	defer cl.Close()

	// Cancelling ctx closes the client so a blocked Wait unblocks. The watcher
	// exits on stopCancel (deferred) so it can't outlive the call.
	stopCancel := make(chan struct{})
	defer close(stopCancel)
	go func() {
		select {
		case <-ctx.Done():
			_ = cl.Close()
		case <-stopCancel:
		}
	}()

	sess, err := cl.NewSession()
	if err != nil {
		return -1, fmt.Errorf("open ssh session: %w", err)
	}
	defer sess.Close()
	sess.Stdin = stdin
	sess.Stdout = stdout
	sess.Stderr = stderr

	if fd, isTTY := terminalFD(stdin); isTTY {
		termName := opt.Term
		if termName == "" {
			termName = os.Getenv("TERM")
		}
		if termName == "" {
			termName = "xterm-256color"
		}
		w, h, gerr := term.GetSize(fd)
		if gerr != nil || w == 0 || h == 0 {
			w, h = 80, 24
		}
		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
		if err := sess.RequestPty(termName, h, w, modes); err != nil {
			return -1, fmt.Errorf("request pty: %w", err)
		}
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return -1, fmt.Errorf("set raw terminal: %w", err)
		}
		defer func() { _ = term.Restore(fd, oldState) }()

		stopWinch := make(chan struct{})
		defer close(stopWinch)
		go forwardWinch(fd, sess, stopWinch)
	}

	var runErr error
	if opt.Command == "" {
		runErr = sess.Shell()
	} else {
		runErr = sess.Start(opt.Command)
	}
	if runErr != nil {
		return -1, fmt.Errorf("start remote shell: %w", runErr)
	}

	waitErr := sess.Wait()
	// A cancelled ctx (which closed the client above) surfaces as a Wait error;
	// report the cancellation rather than a misleading transport error.
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	return sshExitCode(waitErr)
}

// sshExitCode maps an ssh Session.Wait error to a process-style exit code. A
// nil error is a clean exit (0); an *ssh.ExitError carries the remote status; an
// *ssh.ExitMissingError means the remote closed without a status (typically
// killed by a signal) — reported as -1 with no error (the shell did run). Any
// other error is a transport failure: -1 plus the error.
func sshExitCode(waitErr error) (int, error) {
	if waitErr == nil {
		return 0, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitStatus(), nil
	}
	var missing *ssh.ExitMissingError
	if errors.As(waitErr, &missing) {
		return -1, nil
	}
	return -1, fmt.Errorf("ssh session: %w", waitErr)
}

// terminalFD reports the file descriptor of r and whether it is a terminal. A
// non-*os.File reader (a pipe, a bytes.Buffer in tests) is never a terminal, so
// Shell runs it non-interactively (no PTY, no raw mode).
func terminalFD(r io.Reader) (int, bool) {
	f, ok := r.(*os.File)
	if !ok {
		return 0, false
	}
	fd := int(f.Fd())
	return fd, term.IsTerminal(fd)
}

// forwardWinch emits the remote PTY's size on every SIGWINCH until stop closes,
// keeping the remote terminal sized to the local window (the SSH analogue of the
// k8s TerminalSizeQueue the old shell used).
func forwardWinch(fd int, sess *ssh.Session, stop <-chan struct{}) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)
	for {
		select {
		case <-stop:
			return
		case <-ch:
			if w, h, err := term.GetSize(fd); err == nil {
				_ = sess.WindowChange(h, w)
			}
		}
	}
}

package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

func newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell <session-id>",
		Short: "Open a debug shell into a remote session pod",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			backend, err := newBackend()
			if err != nil {
				return err
			}
			ref := session.Ref{ID: session.ID(args[0])}

			// Make sure the pod is up before attaching.
			st, err := backend.Status(ctx, ref)
			if err != nil {
				return err
			}
			switch st.Status {
			case session.StatusGone:
				return fmt.Errorf("session %s does not exist", ref.ID)
			case session.StatusSuspended:
				if err := backend.Resume(ctx, ref); err != nil {
					return fmt.Errorf("resume session: %w", err)
				}
			}

			return runShell(ctx, backend, ref)
		},
	}
}

// runShell streams an interactive /bin/bash in the pod, putting the local
// terminal into raw mode and forwarding window resizes.
func runShell(ctx context.Context, backend *k8s.Backend, ref session.Ref) error {
	fd := int(os.Stdin.Fd())
	tty := term.IsTerminal(fd)

	var sizeQueue remotecommand.TerminalSizeQueue
	if tty {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("set raw terminal: %w", err)
		}
		defer func() { _ = term.Restore(fd, oldState) }()
		sizeQueue = newTermSizeQueue(fd)
	}

	return backend.Exec(ctx, ref, []string{"/bin/bash"}, os.Stdin, os.Stdout, os.Stderr, tty, sizeQueue)
}

// termSizeQueue implements remotecommand.TerminalSizeQueue, emitting the
// terminal size on startup and on every SIGWINCH.
type termSizeQueue struct {
	fd     int
	resize chan os.Signal
}

func newTermSizeQueue(fd int) *termSizeQueue {
	q := &termSizeQueue{fd: fd, resize: make(chan os.Signal, 1)}
	signal.Notify(q.resize, syscall.SIGWINCH)
	q.resize <- syscall.SIGWINCH // emit the initial size
	return q
}

func (q *termSizeQueue) Next() *remotecommand.TerminalSize {
	if _, ok := <-q.resize; !ok {
		return nil
	}
	w, h, err := term.GetSize(q.fd)
	if err != nil {
		return nil
	}
	return &remotecommand.TerminalSize{Width: uint16(w), Height: uint16(h)}
}

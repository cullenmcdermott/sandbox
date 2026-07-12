package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// newShellCmd opens an interactive debug shell into a session pod. The heavy
// lifting — SSH dialing, PTY allocation, raw mode, window-resize forwarding,
// exit-code propagation — lives in the SDK (client.Session.Shell); this command
// is a thin wrapper that only resolves the session, resumes it if suspended,
// waits for the pod to be ready, and forwards the shell's exit code.
func newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell <session-id>",
		Short: "Open a debug shell into a remote session pod",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// Share the one backend between the client (which drives Shell) and the
			// direct resume/readiness calls below — the SDK's SSHTarget forward needs
			// a running, ready pod but deliberately doesn't resume one itself.
			cl, backend, err := newClientAndBackend()
			if err != nil {
				return err
			}
			ref := session.Ref{ID: session.ID(args[0])}

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
			// A just-resumed (or still-scheduling) pod is not immediately ready; the
			// SSH port-forward needs it running before it can bind. StartWithProgress
			// returns promptly for an already-ready pod.
			if err := backend.StartWithProgress(ctx, ref, func(string) {}); err != nil {
				return fmt.Errorf("wait for pod: %w", err)
			}

			code, err := cl.Open(ref.ID).Shell(ctx, client.ShellOptions{})
			if err != nil {
				return err
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}

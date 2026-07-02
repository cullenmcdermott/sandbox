package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// newTurnCmd builds the hidden `sandbox turn` command: a headless, scriptable
// way to run a single turn against an existing session's runner and print the
// assistant's reply to stdout. It's the behavioral oracle the integration smoke
// test drives — the CLI walks the full PortForward → health → StartTurn →
// SSE-event loop without the TUI. Progress (including the "turn started:" marker
// and a line per event type) goes to stderr; only the assistant reply text goes
// to stdout so callers can capture it cleanly.
func newTurnCmd() *cobra.Command {
	var (
		prompt  string
		mode    string
		model   string
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:    "turn <session-id>",
		Short:  "Run a single headless turn against a session and print the reply",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			c, err := newClient()
			if err != nil {
				return err
			}
			ref := session.Ref{ID: session.ID(args[0])}

			rc, cleanup, err := c.DialRunner(ctx, ref)
			if err != nil {
				return err
			}
			defer cleanup()

			if err := waitHealthy(ctx, rc); err != nil {
				return fmt.Errorf("runner health: %w", err)
			}

			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			// Open the SSE stream BEFORE starting the turn so the turn's events
			// can't be missed in the gap between StartTurn and subscribing.
			// afterSeq=0 replays from the start; we filter to the started turn
			// below, so replayed events from earlier turns are ignored.
			events, err := rc.Events(ctx, ref, 0)
			if err != nil {
				return fmt.Errorf("open event stream: %w", err)
			}

			turn, err := rc.StartTurn(ctx, ref, session.TurnInput{
				Prompt: prompt,
				Mode:   mode,
				Model:  model,
			})
			if err != nil {
				return fmt.Errorf("start turn: %w", err)
			}
			fmt.Fprintf(errOut, "turn started: %s\n", turn.Turn)

			for {
				select {
				case <-ctx.Done():
					// cmd.Context() is also cancelled by SIGINT/SIGTERM (root
					// uses signal.NotifyContext), so distinguish a Ctrl-C from a
					// genuine deadline.
					if ctx.Err() == context.Canceled {
						return fmt.Errorf("interrupted waiting for turn %s", turn.Turn)
					}
					return fmt.Errorf("timed out after %s waiting for turn %s", timeout, turn.Turn)
				case ev, ok := <-events:
					if !ok {
						return fmt.Errorf("event stream closed before turn %s completed", turn.Turn)
					}
					// React only to events for the turn we started; replayed or
					// concurrent events from other turns are not ours.
					if ev.TurnID != turn.Turn {
						continue
					}
					fmt.Fprintf(errOut, "event: %s (seq=%d)\n", ev.Type, ev.Seq)
					switch ev.Type {
					case session.EventMessageCompleted:
						var msg session.MessagePayload
						if err := json.Unmarshal(ev.Payload, &msg); err != nil {
							return fmt.Errorf("decode message.completed: %w", err)
						}
						if msg.Role == "assistant" {
							fmt.Fprintln(out, msg.Content)
						}
					case session.EventTurnCompleted:
						return nil
					case session.EventTurnFailed:
						return fmt.Errorf("turn %s failed: %s", turn.Turn, string(ev.Payload))
					}
				}
			}
		},
	}
	cmd.Flags().StringVar(&prompt, "prompt", "", "prompt text to send to the agent (required)")
	// Default to bypassPermissions: a headless turn has no one to answer an
	// interactive permission prompt, so if the agent invokes a tool in a gated
	// mode it would hang until --timeout. Callers can still pass --mode default.
	cmd.Flags().StringVar(&mode, "mode", "bypassPermissions", "permission mode: default|acceptEdits|plan|bypassPermissions")
	cmd.Flags().StringVar(&model, "model", "", "model id override for this turn")
	cmd.Flags().DurationVar(&timeout, "timeout", 300*time.Second, "maximum time to wait for the turn to complete")
	_ = cmd.MarkFlagRequired("prompt")
	return cmd
}

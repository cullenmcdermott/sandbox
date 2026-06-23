package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// trace renders a session's normalized event log as a timeline so an operator
// (or an agent) can replay what actually happened in a run instead of reasoning
// read-only. The events come from the runner's /events endpoint (which serves
// the persisted events.db), so trace needs the pod running — it port-forwards,
// passively streams the backlog after `--since`, and prints it.

type traceOptions struct {
	since uint64
	tool  string
	json  bool
}

func newTraceCmd() *cobra.Command {
	var opts traceOptions
	var quiesce time.Duration
	cmd := &cobra.Command{
		Use:   "trace <session-id>",
		Short: "Replay a session's normalized event timeline",
		Long: `Replay a session's normalized event timeline.

trace port-forwards to the running session pod and streams its persisted event
log (the same events.db the TUI consumes over SSE), printing each event as a
one-line timeline entry. Use --json for the raw normalized events (one per line,
greppable / pipeable to jq), --since to skip everything up to a sequence number,
and --tool to focus on a single tool's events.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ref := session.Ref{ID: session.ID(args[0])}

			backend, err := newBackend()
			if err != nil {
				return err
			}
			st, err := backend.Status(ctx, ref)
			if err != nil {
				return err
			}
			switch st.Status {
			case session.StatusGone:
				return fmt.Errorf("session %s no longer exists", ref.ID)
			case session.StatusSuspended:
				return fmt.Errorf("session %s is suspended; run `sandbox resume %s` first", ref.ID, ref.ID)
			}

			handles, err := backend.PortForward(ctx, ref, k8s.ForwardSpecs(0, 0))
			if err != nil {
				return fmt.Errorf("port-forward: %w", err)
			}
			defer func() {
				for _, h := range handles {
					_ = h.Close()
				}
			}()

			endpoint := fmt.Sprintf("http://127.0.0.1:%d", handles[0].LocalPort())
			dbg("trace port-forward established", "session", ref.ID, "endpoint", endpoint)
			token, err := backend.RunnerToken(ctx, ref)
			if err != nil {
				return err
			}
			client := runner.New(endpoint, token)
			if err := waitHealthy(ctx, client); err != nil {
				return fmt.Errorf("runner health: %w", err)
			}

			events, err := collectTrace(ctx, client, ref, opts.since, quiesce)
			if err != nil {
				return err
			}
			dbg("trace collected events", "session", ref.ID, "count", len(events), "since", opts.since)
			return renderTrace(cmd.OutOrStdout(), events, opts)
		},
	}
	cmd.Flags().Uint64Var(&opts.since, "since", 0, "only show events with a sequence number greater than this")
	cmd.Flags().StringVar(&opts.tool, "tool", "", "only show tool events for this tool name (e.g. Bash, Edit)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "emit raw normalized event JSON, one per line")
	cmd.Flags().DurationVar(&quiesce, "quiesce", 750*time.Millisecond, "stop after this idle gap with no new events")
	return cmd
}

// collectTrace passively streams the event backlog and returns once the stream
// has been quiet for `quiesce` (the persisted backlog arrives in a burst, then
// the stream idles waiting for live events). Passive so trace never counts as an
// attached client and cannot hold the idle reaper off.
func collectTrace(ctx context.Context, client *runner.Client, ref session.Ref, since uint64, quiesce time.Duration) ([]session.Event, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch, err := client.EventsPassive(streamCtx, ref, since)
	if err != nil {
		return nil, err
	}

	var out []session.Event
	timer := time.NewTimer(quiesce)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out, nil
			}
			out = append(out, ev)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quiesce)
		case <-timer.C:
			cancel() // backlog drained; stop the stream
			for ev := range ch {
				out = append(out, ev)
			}
			return out, nil
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
}

// renderTrace writes the timeline. Pure (no I/O beyond w) so it is unit-tested
// directly; the command wrapper above only feeds it events.
func renderTrace(w io.Writer, events []session.Event, opts traceOptions) error {
	for _, ev := range events {
		if opts.since > 0 && ev.Seq <= opts.since {
			continue
		}
		if opts.tool != "" && !traceMatchesTool(ev, opts.tool) {
			continue
		}
		if opts.json {
			line, err := json.Marshal(ev)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w, string(line)); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintln(w, formatTraceLine(ev)); err != nil {
			return err
		}
	}
	return nil
}

// traceMatchesTool reports whether ev is a tool.* event for the named tool.
// Non-tool events are excluded when --tool is set (the user is focusing on one
// tool's activity).
func traceMatchesTool(ev session.Event, tool string) bool {
	switch ev.Type {
	case session.EventToolStarted, session.EventToolDelta, session.EventToolCompleted, session.EventToolFailed:
		var p session.ToolPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return false
		}
		return p.Tool == tool
	default:
		return false
	}
}

// formatTraceLine renders one event as "<seq> <hh:mm:ss> <type> — <summary>".
func formatTraceLine(ev session.Event) string {
	ts := ev.Time
	if t, err := time.Parse(time.RFC3339, ev.Time); err == nil {
		ts = t.UTC().Format("15:04:05")
	}
	line := fmt.Sprintf("%-5d %s  %-22s", ev.Seq, ts, ev.Type)
	if s := traceSummary(ev); s != "" {
		line += "  " + s
	}
	return line
}

// traceSummary extracts the most useful payload detail per event type.
func traceSummary(ev session.Event) string {
	switch ev.Type {
	case session.EventToolStarted, session.EventToolDelta, session.EventToolCompleted, session.EventToolFailed:
		var p session.ToolPayload
		_ = json.Unmarshal(ev.Payload, &p)
		parts := []string{p.Tool}
		if cmd := bashCommand(p.Input); cmd != "" {
			parts = append(parts, truncate(cmd, 60))
		}
		if p.ExitCode != nil {
			parts = append(parts, fmt.Sprintf("exit=%d", *p.ExitCode))
		}
		if p.Error != "" {
			parts = append(parts, "error: "+truncate(p.Error, 80))
		} else if p.Output != "" {
			parts = append(parts, truncate(oneLine(p.Output), 60))
		}
		return strings.Join(parts, "  ")
	case session.EventMessageStarted, session.EventMessageDelta, session.EventMessageCompleted:
		var p session.MessagePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Content == "" {
			return p.Role
		}
		return p.Role + ": " + truncate(oneLine(p.Content), 80)
	case session.EventPermissionRequested, session.EventPermissionResolved:
		var p session.PermissionPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Decision != "" {
			return p.Tool + " → " + p.Decision
		}
		return p.Tool
	case session.EventSessionStatusChanged:
		var p session.SessionStatusPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Reason != "" {
			return p.Status + " (" + truncate(p.Reason, 80) + ")"
		}
		return p.Status
	case session.EventSessionStarted:
		var p session.SessionStartedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		return strings.TrimSpace(p.Model + " " + p.Cwd)
	case session.EventError:
		var p session.ErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		return truncate(oneLine(p.Message), 100)
	default:
		return ""
	}
}

// bashCommand pulls the "command" string out of a tool input object, if present.
func bashCommand(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var obj struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &obj); err != nil {
		return ""
	}
	return obj.Command
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

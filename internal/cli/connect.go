package cli

import (
	"context"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/runner"
	"github.com/cullenmcdermott/sandbox/internal/tui/dashboard"
)

// *runner.Client must satisfy the wider dashboard.RunnerClient surface (a
// structural superset of session.RunnerClient that adds EventsPassive for
// status-observer streams). This assertion lives here — where both packages are
// already imported — so the TUI dependency tree isn't pulled into internal/runner.
var _ dashboard.RunnerClient = (*runner.Client)(nil)

// healthChecker is the minimal surface waitHealthy needs — satisfied by both the
// concrete *runner.Client (trace) and the client.RunnerClient interface returned
// by client.DialRunner (turn).
type healthChecker interface {
	Health(ctx context.Context) error
}

// waitHealthy polls the runner /healthz until it responds OK or ctx is done. A
// freshly resumed pod (or new port-forward) may need a moment. Used by the
// headless `turn` and `trace` commands; the dashboard connect path's health wait
// lives in the public client package.
func waitHealthy(ctx context.Context, client healthChecker) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := client.Health(hctx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

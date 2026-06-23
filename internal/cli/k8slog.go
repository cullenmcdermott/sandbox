package cli

import (
	"context"
	"io"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

// silenceKubernetesLogging routes client-go / apimachinery diagnostic output
// away from the terminal.
//
// client-go's port-forwarder reports transient stream failures (the pod was
// deleted, an SPDY copy hit EOF, the API-server blipped) through
// runtime.HandleError, whose default handler is logError → klog → os.Stderr.
// While the dashboard owns the alt-screen, those writes land directly on top of
// the TUI — the "Unhandled Error … portforward.go" spew you get after killing a
// pod — and, because they bypass Bubble Tea, they desync its frame renderer so
// stale cells linger. The same is true of any code path inside client-go that
// logs via klog directly.
//
// We replace the global ErrorHandlers so those errors flow into the structured
// debug log instead, and point klog's own output at io.Discard so nothing
// client-go logs can reach the terminal. The errors are not swallowed silently:
// with --debug / SANDBOX_DEBUG they are emitted in the JSON-line trace. The
// rate-limiting backoff handler is preserved.
func silenceKubernetesLogging() {
	utilruntime.ErrorHandlers = []utilruntime.ErrorHandler{
		func(_ context.Context, err error, msg string, keysAndValues ...interface{}) {
			args := make([]any, 0, len(keysAndValues)+4)
			args = append(args, "err", err)
			if msg != "" {
				args = append(args, "msg", msg)
			}
			args = append(args, keysAndValues...)
			dbg("k8s runtime error", args...)
		},
	}
	klog.LogToStderr(false)
	klog.SetOutput(io.Discard)
}

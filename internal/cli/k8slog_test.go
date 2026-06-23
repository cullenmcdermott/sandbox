package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

// TestSilenceKubernetesLogging is the behavioral oracle for the port-forward
// log-leak bug: a client-go runtime error must be captured into the structured
// debug log instead of being written to the terminal (where it corrupts the
// dashboard alt-screen). It exercises the real runtime.HandleErrorWithContext
// dispatch path that the port-forwarder uses.
func TestSilenceKubernetesLogging(t *testing.T) {
	// Save and restore all the global state this test mutates so it can't leak
	// into other cli tests.
	savedHandlers := utilruntime.ErrorHandlers
	savedDebugEnabled := debugEnabled
	savedDebugOut := debugOut
	savedDebugLogger := debugLogger
	t.Cleanup(func() {
		utilruntime.ErrorHandlers = savedHandlers
		debugEnabled = savedDebugEnabled
		debugOut = savedDebugOut
		debugLogger = savedDebugLogger
		klog.LogToStderr(true)
		klog.SetOutput(io.Discard)
	})

	var buf bytes.Buffer
	debugEnabled = true
	debugOut = &buf
	configureDebugLogging()

	silenceKubernetesLogging()

	// Exactly our handler is installed — the default logError (klog → stderr) is
	// gone, so HandleError can no longer reach the terminal.
	if got := len(utilruntime.ErrorHandlers); got != 1 {
		t.Fatalf("expected exactly 1 error handler, got %d", got)
	}

	utilruntime.HandleErrorWithContext(context.Background(),
		errors.New("error copying from local connection to remote stream: EOF"),
		"forwarding port")

	out := buf.String()
	if !strings.Contains(out, "k8s runtime error") {
		t.Fatalf("runtime error did not reach the debug log; got: %q", out)
	}
	if !strings.Contains(out, "EOF") {
		t.Fatalf("the underlying error was dropped; got: %q", out)
	}
}

// TestSilenceKubernetesLoggingDiscardsWhenQuiet confirms that with debug off the
// k8s error is swallowed rather than leaking — the default production posture.
func TestSilenceKubernetesLoggingDiscardsWhenQuiet(t *testing.T) {
	savedHandlers := utilruntime.ErrorHandlers
	savedDebugEnabled := debugEnabled
	savedDebugLogger := debugLogger
	t.Cleanup(func() {
		utilruntime.ErrorHandlers = savedHandlers
		debugEnabled = savedDebugEnabled
		debugLogger = savedDebugLogger
		klog.LogToStderr(true)
		klog.SetOutput(io.Discard)
	})

	debugEnabled = false
	debugLogger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	silenceKubernetesLogging()

	// Must not panic and must route through our (discarding) handler.
	utilruntime.HandleErrorWithContext(context.Background(), errors.New("boom"), "")
}

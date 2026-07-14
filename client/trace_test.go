package client

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// withTraceSink swaps the trace sink and env for the test, restoring both after.
func withTraceSink(t *testing.T, enabled bool) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := traceOut
	traceOut = &buf
	t.Cleanup(func() { traceOut = old })
	if enabled {
		t.Setenv("SANDBOX_TRACE", "1")
	} else {
		t.Setenv("SANDBOX_TRACE", "")
	}
	return &buf
}

func TestTracerSilentWhenDisabled(t *testing.T) {
	buf := withTraceSink(t, false)

	// newTracer returns nil when off; every span call must be a no-op.
	tr := newTracer()
	if tr != nil {
		t.Fatalf("newTracer() returned non-nil while tracing disabled")
	}
	tr.start("connect.total").end() // must not panic on nil receivers
	sp := tr.start("connect.port_forward")
	sp.end()

	if buf.Len() != 0 {
		t.Fatalf("trace output emitted while disabled: %q", buf.String())
	}
}

// traceID is what Connect propagates to the runner as X-Sandbox-Trace-Id: it
// must be nil-safe ("" when tracing is off, so no header is sent) and must be
// the same correlation id the flow's span lines carry when on.
func TestTracerTraceID(t *testing.T) {
	withTraceSink(t, false)
	if got := newTracer().traceID(); got != "" {
		t.Errorf(`disabled (nil) tracer traceID: got %q, want ""`, got)
	}

	buf := withTraceSink(t, true)
	tr := newTracer()
	tr.start("connect.total").end()
	line := strings.TrimSpace(buf.String())
	if id := tr.traceID(); id == "" || !strings.HasPrefix(line, "trace: "+id+" ") {
		t.Errorf("traceID %q is not the correlation id of span line %q", tr.traceID(), line)
	}
}

func TestTracerEmitsWhenEnabled(t *testing.T) {
	buf := withTraceSink(t, true)

	tr := newTracer()
	if tr == nil {
		t.Fatal("newTracer() returned nil while tracing enabled")
	}
	tr.start("connect.port_forward").end()
	tr.start("connect.runner_health").end()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 trace lines, got %d: %q", len(lines), buf.String())
	}
	// Envelope: "trace: <id> <name> <dur>" with a stable id shared by the flow.
	re := regexp.MustCompile(`^trace: ([0-9a-f]+) (connect\.\w+) \d+(\.\d+)?(ns|µs|ms|s)$`)
	var id string
	for i, line := range lines {
		m := re.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("line %d does not match trace envelope: %q", i, line)
		}
		if i == 0 {
			id = m[1]
		} else if m[1] != id {
			t.Errorf("correlation id drifted across spans: %q vs %q", id, m[1])
		}
	}
	if !strings.Contains(buf.String(), "connect.port_forward") ||
		!strings.Contains(buf.String(), "connect.runner_health") {
		t.Errorf("expected both span names in output: %q", buf.String())
	}
}

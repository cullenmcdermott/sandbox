package client

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Minimal, dependency-free timing spans for the connect/create path (§10
// observability). The goal is to make cold-start cost analysis possible without
// pulling in an OpenTelemetry SDK: each phase of Connect/Create opens a span and
// closes it, emitting one human-scannable line to stderr correlated by a short
// flow id, e.g.
//
//	trace: 3f9a1c2b connect.port_forward 412ms
//	trace: 3f9a1c2b connect.runner_health 88ms
//
// Off by default. Enabled when SANDBOX_TRACE is non-empty (the CLI's --trace
// flag sets it; see internal/cli). When off, newTracer returns nil and every
// span call is a nil-receiver no-op, so the instrumented paths pay ~nothing in
// steady state.

// traceOut is the sink for trace lines (overridable in tests). Stderr so trace
// output never contaminates a command's stdout (JSON, timelines, etc.).
var traceOut io.Writer = os.Stderr

// traceEnabled reports whether connect-path tracing is on. Read per newTracer
// call (not cached) so a test — or the CLI --trace flag, which sets the env var
// in PersistentPreRun — can toggle it after package init.
func traceEnabled() bool { return os.Getenv("SANDBOX_TRACE") != "" }

// tracer emits timing spans for a single connect (or create) flow, correlated
// by a short id shared across every span of the flow — including the ones the
// background-sync goroutine opens after Connect has returned. A nil *tracer
// (tracing disabled) makes start/end cheap no-ops.
type tracer struct {
	id string
	mu sync.Mutex // serialize writes: the background-sync goroutine spans concurrently
	w  io.Writer
}

// newTracer returns a tracer for a fresh flow id, or nil when tracing is off.
func newTracer() *tracer {
	if !traceEnabled() {
		return nil
	}
	return &tracer{id: newTraceID(), w: traceOut}
}

// span is an in-flight timing span. A nil *span (from a nil tracer) is safe to
// end and does nothing.
type span struct {
	tr    *tracer
	name  string
	start time.Time
}

// start opens a span. Nil-safe: a nil tracer yields a nil span.
func (t *tracer) start(name string) *span {
	if t == nil {
		return nil
	}
	return &span{tr: t, name: name, start: time.Now()}
}

// end closes the span and emits one line: "trace: <id> <name> <dur>". Nil-safe,
// so callers can write `defer tr.start("x").end()` unconditionally.
func (s *span) end() {
	if s == nil {
		return
	}
	dur := time.Since(s.start).Round(time.Millisecond)
	s.tr.mu.Lock()
	defer s.tr.mu.Unlock()
	fmt.Fprintf(s.tr.w, "trace: %s %s %s\n", s.tr.id, s.name, dur)
}

// traceID returns the flow's correlation id, or "" for a nil (disabled)
// tracer, so callers can propagate it unconditionally — e.g. into
// runner.Client.SetTraceID, which turns "" into "send no header".
func (t *tracer) traceID() string {
	if t == nil {
		return ""
	}
	return t.id
}

// newTraceID mints a short correlation id for one connect/create flow.
func newTraceID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "conn"
	}
	return hex.EncodeToString(b)
}

package runner

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// --- Pure summary math (no ports; safe in the command sandbox) ---

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// TestSummarizeRTT pins the nearest-rank percentile math on crafted samples.
func TestSummarizeRTT(t *testing.T) {
	cases := []struct {
		name    string
		samples []time.Duration
		n       int
		want    RTTStats
	}{
		{"empty", nil, 0, RTTStats{}},
		// N can exceed len(samples) (ring overwrote); it must carry through.
		{"empty with lifetime count", nil, 3, RTTStats{N: 3}},
		{"single sample is its own p50/p95/max", []time.Duration{ms(10)}, 1,
			RTTStats{N: 1, P50: ms(10), P95: ms(10), Max: ms(10)}},
		// sorted [10 20 30]: p50 = ceil(.5*3)-1 = idx 1; p95 = ceil(.95*3)-1 = idx 2.
		{"odd count", []time.Duration{ms(30), ms(10), ms(20)}, 3,
			RTTStats{N: 3, P50: ms(20), P95: ms(30), Max: ms(30)}},
		// sorted [10 20 30 40]: p50 = ceil(2)-1 = idx 1; p95 = ceil(3.8)-1 = idx 3.
		{"even count", []time.Duration{ms(40), ms(10), ms(30), ms(20)}, 4,
			RTTStats{N: 4, P50: ms(20), P95: ms(40), Max: ms(40)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeRTT(tc.samples, tc.n); got != tc.want {
				t.Errorf("summarizeRTT(%v, %d) = %+v, want %+v", tc.samples, tc.n, got, tc.want)
			}
		})
	}
}

// TestSummarizeRTTDoesNotMutateInput: the summary sorts a copy, never the
// caller's slice (the ring hands out snapshots, but pin it anyway).
func TestSummarizeRTTDoesNotMutateInput(t *testing.T) {
	in := []time.Duration{ms(30), ms(10), ms(20)}
	summarizeRTT(in, 3)
	if in[0] != ms(30) || in[1] != ms(10) || in[2] != ms(20) {
		t.Errorf("summarizeRTT mutated its input: %v", in)
	}
}

// TestRTTRingWraparound: past capacity the ring overwrites oldest, keeps the
// lifetime count, and the summary covers only the retained window.
func TestRTTRingWraparound(t *testing.T) {
	if rttRingCap != 256 {
		t.Fatalf("test expectations assume rttRingCap == 256, got %d", rttRingCap)
	}
	r := &rttRing{}
	total := rttRingCap + 44 // 300 samples: 1ms..300ms
	for i := 1; i <= total; i++ {
		r.record(ms(i))
	}
	samples, n := r.snapshot()
	if n != total {
		t.Errorf("lifetime count = %d, want %d", n, total)
	}
	if len(samples) != rttRingCap {
		t.Fatalf("retained %d samples, want %d", len(samples), rttRingCap)
	}
	// Retained window is 45ms..300ms. Nearest-rank over 256 sorted values:
	// p50 = idx 127 → 172ms, p95 = idx 243 → 288ms.
	got := summarizeRTT(samples, n)
	want := RTTStats{N: 300, P50: ms(172), P95: ms(288), Max: ms(300)}
	if got != want {
		t.Errorf("summarizeRTT over wrapped ring = %+v, want %+v", got, want)
	}
}

// --- Probe integration (binds a local port; run unsandboxed like the rest of
// this package's socket tests) ---

// syncBuffer is a mutex-guarded string sink for paneTraceOut, so -race stays
// quiet even if a summary write raced a read (it shouldn't — Close and the
// assertions run on the test goroutine — but the lock is cheap insurance).
type syncBuffer struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

// probeTestKnobs points paneTraceOut at a capture buffer and shortens the ping
// interval for the duration of the test.
func probeTestKnobs(t *testing.T, interval time.Duration) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	oldOut, oldInterval := paneTraceOut, panePingInterval
	paneTraceOut, panePingInterval = buf, interval
	t.Cleanup(func() { paneTraceOut, panePingInterval = oldOut, oldInterval })
	return buf
}

// TestPaneRTTProbeSamplesAndSummary: with SANDBOX_TRACE set, pings flow and
// pong echoes accumulate as samples (gorilla's default ping handler on the
// test server auto-pongs while its read loop pumps, standing in for the node
// `ws` server's auto-pong); Close stops the pinger and emits exactly one
// summary line in the client trace-line format.
func TestPaneRTTProbeSamplesAndSummary(t *testing.T) {
	t.Setenv("SANDBOX_TRACE", "1")
	buf := probeTestKnobs(t, 5*time.Millisecond)

	accept := make(chan *panePeer, 1)
	srv := paneTestServer(t, "tok", accept)
	defer srv.Close()

	c := New(srv.URL, "tok")
	ps, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 0, 0)
	if err != nil {
		t.Fatalf("AttachPane: %v", err)
	}
	defer ps.Close()
	waitPeer(t, accept) // the peer's read loop pumps, so pings get auto-ponged

	// Pump Read: gorilla dispatches control frames (our pongs) inside
	// NextReader, mirroring how the dashboard's output loop keeps the probe
	// fed in production.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		b := make([]byte, 64)
		for {
			if _, rerr := ps.Read(b); rerr != nil {
				return
			}
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for ps.RTTStats().N < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("probe recorded %d samples, want >= 3", ps.RTTStats().N)
		}
		time.Sleep(2 * time.Millisecond)
	}
	st := ps.RTTStats()
	if st.P50 <= 0 || st.P95 < st.P50 || st.Max < st.P95 {
		t.Errorf("implausible stats ordering: %+v", st)
	}

	if err := ps.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The pinger must exit via the Close path — no goroutine leak, no ticker
	// firing after Close.
	select {
	case <-ps.pingerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("pinger goroutine still running after Close")
	}
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Read loop did not unblock after Close")
	}

	// Exactly one summary line, in the client trace-line family. No trace id
	// was set on the client, so the id column is the "-" placeholder.
	line := buf.String()
	want := regexp.MustCompile(`\Atrace: - pane\.rtt n=\d+ p50=\S+ p95=\S+ max=\S+\n\z`)
	if !want.MatchString(line) {
		t.Errorf("summary output = %q, want one line matching %v", line, want)
	}
	// Double Close must not emit a second line (closeOnce).
	_ = ps.Close()
	if again := buf.String(); again != line {
		t.Errorf("second Close changed output: %q -> %q", line, again)
	}
}

// TestPaneRTTProbeCarriesTraceID: the connect-flow correlation id set via
// SetTraceID stamps the summary line, matching client/trace.go span lines.
func TestPaneRTTProbeCarriesTraceID(t *testing.T) {
	t.Setenv("SANDBOX_TRACE", "1")
	buf := probeTestKnobs(t, 5*time.Millisecond)

	accept := make(chan *panePeer, 1)
	srv := paneTestServer(t, "tok", accept)
	defer srv.Close()

	c := New(srv.URL, "tok")
	c.SetTraceID("f00dfeed")
	ps, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 0, 0)
	if err != nil {
		t.Fatalf("AttachPane: %v", err)
	}
	waitPeer(t, accept)
	if err := ps.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if line := buf.String(); !strings.HasPrefix(line, "trace: f00dfeed pane.rtt n=") {
		t.Errorf("summary output = %q, want prefix %q", line, "trace: f00dfeed pane.rtt n=")
	}
}

// TestPaneRTTProbeDisabledByDefault: without SANDBOX_TRACE the probe never
// starts — no pinger goroutine, zero stats, and Close writes nothing.
func TestPaneRTTProbeDisabledByDefault(t *testing.T) {
	t.Setenv("SANDBOX_TRACE", "") // shield the test from ambient --trace runs
	buf := probeTestKnobs(t, 5*time.Millisecond)

	accept := make(chan *panePeer, 1)
	srv := paneTestServer(t, "tok", accept)
	defer srv.Close()

	c := New(srv.URL, "tok")
	ps, err := c.AttachPane(context.Background(), session.Ref{ID: "s1"}, 0, 0)
	if err != nil {
		t.Fatalf("AttachPane: %v", err)
	}
	waitPeer(t, accept)

	if ps.pingerDone != nil {
		t.Error("pinger goroutine started with tracing off")
	}
	if got := ps.RTTStats(); got != (RTTStats{}) {
		t.Errorf("RTTStats with tracing off = %+v, want zero value", got)
	}
	if err := ps.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if out := buf.String(); out != "" {
		t.Errorf("Close with tracing off wrote %q, want no output", out)
	}
}

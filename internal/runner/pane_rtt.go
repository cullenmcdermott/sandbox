package runner

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Pane-transport RTT probe (TODO §4) — the measure-first prerequisite for any
// slow-link tuning (deflate, coalescing). Off by default; armed when
// SANDBOX_TRACE is non-empty, mirroring client/trace.go traceEnabled (which is
// deliberately unexported there, so the check is re-read here).
//
// While armed, a pinger goroutine sends a WebSocket ping every panePingInterval
// carrying an 8-byte big-endian time.Now().UnixNano() payload. The runner's
// node `ws` server auto-pongs, echoing the payload verbatim, and the pong
// handler (dispatched on the Read goroutine — gorilla processes control frames
// inside NextReader, so samples only accrue while the caller is pumping Read,
// which the dashboard's output loop always does) turns the echo into an RTT
// sample. Samples land in a fixed-capacity overwrite-oldest ring so a
// long-lived pane cannot grow memory.
//
// On Close the stream emits ONE summary line to stderr in the client
// trace-line family ("trace: <id> <name> ...", client/trace.go span.end),
// extended with key=value stats after the name so later probes (e.g. SSE
// first-event latency, TODO §10) can reuse the shape:
//
//	trace: 3f9a1c2b pane.rtt n=12 p50=34ms p95=88ms max=102ms
//
// <id> is the connect-flow correlation id when Client.SetTraceID provided one,
// else "-".

// paneTraceEnabled mirrors client/trace.go traceEnabled: read per attach (not
// cached) so the CLI --trace flag — which sets the env var in
// PersistentPreRun — and tests can toggle it after package init.
func paneTraceEnabled() bool { return os.Getenv("SANDBOX_TRACE") != "" }

// paneTraceOut is the sink for the Close summary line. Stderr, matching
// client/trace.go traceOut, so trace output never contaminates a command's
// stdout. A package var so tests can capture it.
var paneTraceOut io.Writer = os.Stderr

// panePingInterval is how often the armed probe samples the link. A package
// var so tests can shorten it (same pattern as sseReadTimeout).
var panePingInterval = 5 * time.Second

// rttRingCap bounds the retained sample window; once full, the oldest sample
// is overwritten. 256 five-second samples ≈ the last 21 minutes attached.
const rttRingCap = 256

// RTTStats summarizes the pane-transport round-trip samples the SANDBOX_TRACE
// probe has collected: N counts every sample recorded over the stream's
// lifetime, while P50/P95/Max are order statistics over the retained window
// (the most recent rttRingCap samples). Zero-valued when the probe is off or
// no pong has arrived yet.
type RTTStats struct {
	N   int
	P50 time.Duration
	P95 time.Duration
	Max time.Duration
}

// rttRing is the mutex-guarded overwrite-oldest sample buffer. The pong
// handler records from the Read goroutine while RTTStats snapshots from any
// other, hence the lock.
type rttRing struct {
	mu  sync.Mutex
	buf [rttRingCap]time.Duration
	n   int // lifetime samples recorded; min(n, rttRingCap) are retained
}

func (r *rttRing) record(d time.Duration) {
	r.mu.Lock()
	r.buf[r.n%rttRingCap] = d
	r.n++
	r.mu.Unlock()
}

// snapshot copies out the retained samples (insertion order — irrelevant to
// the summary math, which sorts) plus the lifetime count.
func (r *rttRing) snapshot() ([]time.Duration, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := min(r.n, rttRingCap)
	out := make([]time.Duration, k)
	copy(out, r.buf[:k])
	return out, r.n
}

// summarizeRTT computes order statistics over the retained window, with n as
// the lifetime sample count. Percentiles use the nearest-rank method
// (ceil(q*len)-1 into the sorted samples), so a single sample is its own
// p50/p95/max. Empty samples yield zero durations with N carried through.
func summarizeRTT(samples []time.Duration, n int) RTTStats {
	st := RTTStats{N: n}
	if len(samples) == 0 {
		return st
	}
	s := make([]time.Duration, len(samples))
	copy(s, samples)
	slices.Sort(s)
	st.P50 = s[nearestRank(0.50, len(s))]
	st.P95 = s[nearestRank(0.95, len(s))]
	st.Max = s[len(s)-1]
	return st
}

// nearestRank returns the 0-based index of the q-th percentile (nearest-rank
// method) into a sorted slice of length n >= 1.
func nearestRank(q float64, n int) int {
	i := int(math.Ceil(q*float64(n))) - 1
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// startRTTProbe arms the pong handler and starts the pinger goroutine. Called
// from AttachPane, before the stream is shared, only when paneTraceEnabled().
func (p *PaneStream) startRTTProbe(interval time.Duration) {
	p.rtt = &rttRing{}
	p.conn.SetPongHandler(func(appData string) error {
		// Runs on the Read goroutine. The payload is our ping's 8-byte
		// big-endian send timestamp echoed by the server; anything else —
		// wrong length, or a wall-clock step making the delta negative — is
		// dropped rather than polluting the stats.
		if len(appData) != 8 {
			return nil
		}
		sent := int64(binary.BigEndian.Uint64([]byte(appData)))
		if d := time.Duration(time.Now().UnixNano() - sent); d >= 0 {
			p.rtt.record(d)
		}
		return nil
	})
	p.pingerDone = make(chan struct{})
	go func() {
		defer close(p.pingerDone)
		t := time.NewTicker(interval)
		defer t.Stop()
		// A ping that cannot flush within an interval is itself a verdict on
		// the link, but keep a 1s floor so test-shortened intervals don't
		// flake the deadline.
		wait := max(interval, time.Second)
		for {
			select {
			case <-p.done:
				// Close path: stop pinging; Close emits the summary.
				return
			case <-t.C:
				var payload [8]byte
				binary.BigEndian.PutUint64(payload[:], uint64(time.Now().UnixNano()))
				// WriteControl is safe concurrently with the input writer's
				// data writes (gorilla's contract — see Close), so no writeMu.
				if err := p.conn.WriteControl(websocket.PingMessage, payload[:], time.Now().Add(wait)); err != nil {
					// Connection closing or gone; stop probing. Close still
					// summarizes whatever was sampled.
					return
				}
			}
		}
	}()
}

// RTTStats reports the transport round-trip stats the SANDBOX_TRACE probe has
// collected so far (zero-valued when the probe is off). Additive surface for a
// future dashboard debug row; safe to call concurrently with Read/Close.
func (p *PaneStream) RTTStats() RTTStats {
	if p.rtt == nil {
		return RTTStats{}
	}
	samples, n := p.rtt.snapshot()
	return summarizeRTT(samples, n)
}

// emitRTTSummary writes the one-line RTT roll-up on Close (see the package
// comment above for the format contract).
func (p *PaneStream) emitRTTSummary() {
	st := p.RTTStats()
	id := p.traceID
	if id == "" {
		id = "-"
	}
	fmt.Fprintf(paneTraceOut, "trace: %s pane.rtt n=%d p50=%s p95=%s max=%s\n",
		id, st.N,
		st.P50.Round(time.Microsecond),
		st.P95.Round(time.Microsecond),
		st.Max.Round(time.Microsecond))
}

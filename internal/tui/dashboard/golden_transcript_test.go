package dashboard

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// golden_transcript_test.go — the Tier-2B golden transcript pipeline. It replays
// a canonical normalized event stream (testdata/transcript-*.jsonl) through the
// real event→TUI render path (handleEvent → renderTranscript) and snapshots the
// whole transcript, extending the single-widget goldens in golden_test.go to an
// end-to-end transcript. This catches event-mapping and rendering regressions
// that the per-event unit tests miss. Regenerate with `-update`.
//
// Determinism reuses the pins from golden_test.go (withDeterministicRender:
// SANDBOX_REDUCE_MOTION=1, fixed nowFunc, forced gradient capability).

// loadEventStream reads a JSONL fixture of normalized events (one session.Event
// per line). The fixture is the recorded input; the .golden file is the asserted
// rendered output — together they form the pipeline.
func loadEventStream(t *testing.T, name string) []session.Event {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	var events []session.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev session.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("decode fixture line %q: %v", line, err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("fixture %s produced no events", name)
	}
	return events
}

func TestGoldenTranscriptStream(t *testing.T) {
	withDeterministicRender(t, func() {
		tm := goldenTranscript()
		tm.layout()
		for _, ev := range loadEventStream(t, "transcript-basic.jsonl") {
			tm.handleEvent(ev)
		}
		tm.layout()
		// Snapshot bodyView only — the rendered transcript blocks, which are the
		// event→render output this test exists to guard. We deliberately exclude
		// the input row and the rate-limit status line, keeping the snapshot
		// focused on the event→block pipeline (the status line is covered by
		// statusline_ratelimit_test.go, which drives it with seeded rate_limit
		// data). The completed-turn footer's elapsed ("0s") is the inter-event
		// loop time — turn.started and turn.completed are applied microseconds
		// apart, so it is always "0s".
		golden.RequireEqual(t, []byte(tm.bodyView()))
	})
}

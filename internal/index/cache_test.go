package index

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

func ev(seq uint64, typ session.EventType, content string) session.Event {
	payload, _ := json.Marshal(session.MessagePayload{Role: "assistant", Content: content})
	return session.Event{Seq: seq, SessionID: "sess", Type: typ, Payload: payload}
}

// Round-trip: appended events load back in order, by seq and content.
func TestEventCacheRoundTrip(t *testing.T) {
	idx := New(t.TempDir())
	const id = "sess-1"
	want := []session.Event{
		ev(1, session.EventTurnStarted, ""),
		ev(2, session.EventMessageCompleted, "hello"),
		ev(3, session.EventTurnCompleted, ""),
	}
	for _, e := range want {
		if err := idx.AppendCachedEvent(id, e); err != nil {
			t.Fatalf("append seq %d: %v", e.Seq, err)
		}
	}
	got, err := idx.LoadCachedEvents(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Seq != want[i].Seq || got[i].Type != want[i].Type {
			t.Errorf("event %d: got seq=%d type=%s, want seq=%d type=%s",
				i, got[i].Seq, got[i].Type, want[i].Seq, want[i].Type)
		}
	}
	var p session.MessagePayload
	_ = json.Unmarshal(got[1].Payload, &p)
	if p.Content != "hello" {
		t.Errorf("payload content = %q, want hello", p.Content)
	}
}

// A missing cache is a cold first attach: nil, no error.
func TestEventCacheMissingIsEmpty(t *testing.T) {
	idx := New(t.TempDir())
	got, err := idx.LoadCachedEvents("never-written")
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if got != nil {
		t.Errorf("missing cache should load nil, got %d events", len(got))
	}
}

// A corrupt/partial line is skipped, not fatal — the delta stream backfills.
func TestEventCacheSkipsCorruptLine(t *testing.T) {
	idx := New(t.TempDir())
	const id = "sess-corrupt"
	if err := idx.AppendCachedEvent(id, ev(1, session.EventTurnStarted, "")); err != nil {
		t.Fatal(err)
	}
	// Simulate a torn write by appending a raw bad line.
	f, err := os.OpenFile(idx.EventCachePath(id), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{not valid json\n")
	_ = f.Close()
	if err := idx.AppendCachedEvent(id, ev(2, session.EventTurnCompleted, "")); err != nil {
		t.Fatal(err)
	}
	got, err := idx.LoadCachedEvents(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("expected the 2 valid events around the corrupt line, got %d: %+v", len(got), got)
	}
}

package session

import (
	"encoding/json"
	"testing"
)

// T6: the session.title event type and payload must exist and round-trip,
// mirroring runner/src/types.ts ('session.title' + SessionTitlePayload).
func TestSessionTitlePayloadRoundTrip(t *testing.T) {
	if EventSessionTitle != "session.title" {
		t.Fatalf("EventSessionTitle = %q, want %q", EventSessionTitle, "session.title")
	}
	in := SessionTitlePayload{Title: "fix auth race condition"}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"title":"fix auth race condition"}` {
		t.Fatalf("json = %s", raw)
	}
	var out SessionTitlePayload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Title != in.Title {
		t.Fatalf("Title = %q, want %q", out.Title, in.Title)
	}
}

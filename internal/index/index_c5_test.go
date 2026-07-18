package index

import (
	"strings"
	"testing"
)

// Regression for C5: validateID must reject session ids that escape the index
// root via path traversal, before any os.RemoveAll / WriteFile runs on the
// joined path.
func TestValidateIDRejectsTraversal(t *testing.T) {
	root := t.TempDir()

	bad := []string{"../../etc", "..", "a/../../b", "../sibling", "/abs/escape/../../.."}
	for _, id := range bad {
		if err := validateID(root, id); err == nil {
			t.Errorf("validateID(%q) = nil, want an escape error", id)
		} else if !strings.Contains(err.Error(), "escape") {
			t.Errorf("validateID(%q) error = %v, want an 'escapes root' error", id, err)
		}
	}

	good := []string{"session-a", "claude-sdk-test", "abc123"}
	for _, id := range good {
		if err := validateID(root, id); err != nil {
			t.Errorf("validateID(%q) = %v, want nil for a well-formed id", id, err)
		}
	}
}

// [V30] The read side must enforce the same C5 guard as the write side: Load,
// LoadCachedEvents, and DeleteEventCache all reject a traversing id with an
// "escapes root" error instead of touching a file outside the index root.
func TestReadSideRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	idx := New(root)
	const bad = "../../etc"

	if _, err := idx.Load(bad); err == nil || !strings.Contains(err.Error(), "escape") {
		t.Errorf("Load(%q) error = %v, want an 'escapes root' error", bad, err)
	}
	if _, err := idx.LoadCachedEvents(bad); err == nil || !strings.Contains(err.Error(), "escape") {
		t.Errorf("LoadCachedEvents(%q) error = %v, want an 'escapes root' error", bad, err)
	}
	if err := idx.DeleteEventCache(bad); err == nil || !strings.Contains(err.Error(), "escape") {
		t.Errorf("DeleteEventCache(%q) error = %v, want an 'escapes root' error", bad, err)
	}

	// A well-formed id still works on the read side: Load of a missing entry
	// returns a normal (non-escape) error, and the cache readers no-op cleanly.
	if _, err := idx.Load("session-a"); err == nil {
		t.Error("Load of a missing well-formed id: got nil error, want a not-found error")
	} else if strings.Contains(err.Error(), "escape") {
		t.Errorf("Load of a well-formed id must not be rejected as traversal: %v", err)
	}
	if evs, err := idx.LoadCachedEvents("session-a"); err != nil || evs != nil {
		t.Errorf("LoadCachedEvents of a missing well-formed id = (%v, %v), want (nil, nil)", evs, err)
	}
	if err := idx.DeleteEventCache("session-a"); err != nil {
		t.Errorf("DeleteEventCache of a missing well-formed id = %v, want nil", err)
	}
}

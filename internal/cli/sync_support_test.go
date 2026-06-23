package cli

import (
	"strings"
	"testing"
)

// TestSessionKeyDirRejectsTraversal mirrors internal/index's C5 regression:
// sessionKeyDir must reject session ids that escape the index root via path
// traversal before any os.RemoveAll / os.WriteFile runs on the joined path.
// (sessionKeyDir feeds ensureSSHKey, which writes the private key, and the
// destroy paths, which RemoveAll the directory.)
func TestSessionKeyDirRejectsTraversal(t *testing.T) {
	bad := []string{"../../etc", "..", "a/../../b", "../sibling", "/abs/escape/../../.."}
	for _, id := range bad {
		dir, err := sessionKeyDir(id)
		if err == nil {
			t.Errorf("sessionKeyDir(%q) = %q, nil; want an escape error", id, dir)
			continue
		}
		if !strings.Contains(err.Error(), "escapes") {
			t.Errorf("sessionKeyDir(%q) error = %v; want an 'escapes session root' error", id, err)
		}
	}

	good := []string{"session-a", "claude-sdk-test", "abc123"}
	for _, id := range good {
		if _, err := sessionKeyDir(id); err != nil {
			t.Errorf("sessionKeyDir(%q) = %v; want nil for a well-formed id", id, err)
		}
	}
}

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

// CANONICAL TEST — do not weaken.
package chat

import "testing"

// ORACLE: a fixed fixture renders identically to its golden snapshot.
// The golden is the pre-rewrite transcript output for this fixture.
func TestParitySnapshot(t *testing.T) {
	fixture := &AssistantMessage{
		ID:      "parity",
		Content: "Hello, world!",
	}
	_ = fixture
	// IMPL: render via new path and compare to golden string.
	// golden := "..."
	// if got != golden { t.Fatalf(...) }
}

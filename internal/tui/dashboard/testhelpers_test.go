package dashboard

// testhelpers_test.go — shared test-only helpers relocated here after the
// transcript test suite (which originally defined them) was removed by
// claude-pane-first.

import (
	"encoding/json"

	"github.com/charmbracelet/x/ansi"
)

// stripANSI removes ANSI escape sequences so a rendered string can be asserted
// on its visible text.
func stripANSI(s string) string { return ansi.Strip(s) }

// stripANSICodes is an alias kept for the tests that reference it by that name.
func stripANSICodes(s string) string { return ansi.Strip(s) }

// mustJSON marshals v to json.RawMessage, panicking on error (test-only).
func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

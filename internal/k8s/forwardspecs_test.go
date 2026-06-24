package k8s

import "testing"

// Fix F-rest: observer-mode forwards must include the runner HTTP port and omit
// the SSH port (which exists only for mutagen sync), so a backgrounded session
// opens one SPDY stream instead of two.
func TestForwardSpecsRunnerOnly(t *testing.T) {
	specs := ForwardSpecsRunnerOnly(0)
	if len(specs) != 1 {
		t.Fatalf("runner-only forward should have exactly 1 spec, got %d", len(specs))
	}
	if specs[0].Remote != RunnerPort() {
		t.Errorf("runner-only spec remote = %d, want runner port %d", specs[0].Remote, RunnerPort())
	}
	for _, s := range specs {
		if s.Remote == SSHPort() {
			t.Error("runner-only forward must not open the SSH port")
		}
	}

	// The full spec still carries both ports (sync needs SSH).
	full := ForwardSpecs(0, 0)
	if len(full) != 2 || full[0].Remote != RunnerPort() || full[1].Remote != SSHPort() {
		t.Errorf("ForwardSpecs should be [runner, ssh], got %+v", full)
	}
}

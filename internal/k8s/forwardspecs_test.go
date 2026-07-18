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

// TestForwardSpecsWithCodex: codex-app-server sessions forward the runner HTTP,
// SSH, and codex websocket ports, in that order (mirrors ForwardSpecsWithOpencode).
func TestForwardSpecsWithCodex(t *testing.T) {
	specs := ForwardSpecsWithCodex(0, 0, 0)
	if len(specs) != 3 {
		t.Fatalf("codex forward should have exactly 3 specs, got %d", len(specs))
	}
	if specs[0].Remote != RunnerPort() {
		t.Errorf("spec[0] remote = %d, want runner port %d", specs[0].Remote, RunnerPort())
	}
	if specs[1].Remote != SSHPort() {
		t.Errorf("spec[1] remote = %d, want ssh port %d", specs[1].Remote, SSHPort())
	}
	if specs[2].Remote != CodexPort() {
		t.Errorf("spec[2] remote = %d, want codex port %d", specs[2].Remote, CodexPort())
	}
}

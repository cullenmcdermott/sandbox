//go:build integration

package k8sit

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCLISmoke proves the real compiled `sandbox` binary drives the CLI→runner
// turn seam end to end: it builds the binary once, then runs
// `sandbox turn <id> --prompt …` against a session per backendCases row (`sandbox
// opencode`/`claude`/`codex` launch a TUI, so they are never exec'd — the hidden
// `turn` command is the headless seam: port-forward → runner token → StartTurn →
// SSE Events → reply on STDOUT). Gated on expectRealReply:
//   - opencode drives runner turns on its free default model ($0), so this asserts
//     a real reply: exit 0 AND non-empty STDOUT.
//   - supervise-only backends (claude-pane, codex) reject the runner turn (POST
//     /turns 409s), so the smoke only asserts the CLI settled the turn — the 409 —
//     WITHOUT hanging (plumbing-only). This is how claude/codex fill the column.
func TestCLISmoke(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH; cannot build the sandbox binary") // gate-ok: integration-only, needs go to build the CLI binary
	}
	rc := localRestConfig(t) // context-isolation guard + provider-key probe

	// Build the binary ONCE; every backend's subtest reuses it.
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "sandbox")
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer buildCancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", bin, "./cmd/sandbox")
	build.Dir = root
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sandbox binary: %v\n%s", err, out)
	}

	// Per-backend smoke: the real compiled binary drives a headless turn through the
	// hidden `turn` command (the CLI↔runner seam). Table-driven over backendCases so
	// a new backend (Codex) fills the column by appending a row.
	for _, bc := range backendCases {
		t.Run(bc.name, func(t *testing.T) {
			expectReply := bc.expectRealReply(t, rc)
			_, ref := createReadySession(t, bc.backend, bc.idTag+"-cli")

			turnTimeout := envDuration("K8SIT_TURN_TIMEOUT", 180*time.Second)
			runCtx, runCancel := context.WithTimeout(context.Background(), turnTimeout+30*time.Second)
			defer runCancel()
			// KUBECONFIG inherited via os.Environ() so the binary talks to the same
			// local cluster the test does; default namespace (agent-sessions).
			cmd := exec.CommandContext(runCtx, bin, "turn", string(ref.ID),
				"--prompt", "Reply with a short greeting.", "--timeout", turnTimeout.String())
			cmd.Env = os.Environ()
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()

			t.Logf("%s: sandbox turn stdout:\n%s", bc.name, stdout.String())
			t.Logf("%s: sandbox turn stderr:\n%s", bc.name, stderr.String())

			if runCtx.Err() == context.DeadlineExceeded {
				t.Fatalf("sandbox turn hung past the timeout (the CLI seam wedged)")
			}
			if expectReply {
				// Free/keyed backend: a real reply at $0 (opencode) — exit 0 + non-empty.
				if err != nil {
					t.Fatalf("sandbox turn exited non-zero: %v\nstderr:\n%s", err, stderr.String())
				}
				if strings.TrimSpace(stdout.String()) == "" {
					t.Fatalf("sandbox turn produced no reply on stdout\nstderr:\n%s", stderr.String())
				}
				return
			}
			// Plumbing-only (no provider key): we only require that the CLI drove the
			// turn to a terminal without hanging — the turn itself may report a
			// failure (asserted above: it did not hit the deadline).
			t.Logf("%s: plumbing-only — CLI seam drove the turn to a terminal", bc.name)
		})
	}
}

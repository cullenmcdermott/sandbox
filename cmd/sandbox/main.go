// Command sandbox is the remote agent session CLI.
//
// It creates Kubernetes-backed sessions (Sandbox + PVC), port-forwards to
// the runner pod, and opens a local Bubble Tea TUI that streams events.
// This is the homelab-side implementation of the remote agent SDK sandbox
// design. See docs/superpowers/specs/2026-06-18-remote-agent-sdk-sandbox-design.md.
package main

import (
	"fmt"
	"os"

	"github.com/cullenmcdermott/sandbox/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}
}

// Command sandbox is the remote agent session CLI.
//
// It creates Kubernetes-backed sessions (Sandbox + PVC), port-forwards to
// the runner pod, and opens a local Bubble Tea TUI that streams events.
// See docs/architecture.md for the overall design.
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

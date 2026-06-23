// Package cli implements the sandbox CLI's remote session commands.
//
// These commands complement the existing Lima-based CLI (which lives in
// system-config) by adding Kubernetes-backed remote agent sessions. The
// command tree is designed to be mergeable into the existing sandbox CLI
// when the system-config checkout is writable.
package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// namespaceFlag is bound to the root --namespace persistent flag and threaded
// into every backend so all commands address the same namespace consistently.
var namespaceFlag string

// NewRoot builds the remote-sandbox CLI command tree.
func NewRoot() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Run AI coding agents in remote Kubernetes sessions",
	}
	cmd.PersistentFlags().StringVarP(&namespaceFlag, "namespace", "n", "", "Kubernetes namespace (default: agent-sessions)")
	cmd.AddCommand(newClaudeRemoteCmd())
	cmd.AddCommand(newAttachCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newSyncCmd())
	cmd.AddCommand(newSuspendCmd())
	cmd.AddCommand(newResumeCmd())
	cmd.AddCommand(newCancelCmd())
	cmd.AddCommand(newDestroyCmd())
	cmd.AddCommand(newShellCmd())
	return cmd
}

// Execute runs the CLI.
func Execute() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return NewRoot().ExecuteContext(ctx)
}

// newBackend creates a k8s Backend from the default kubeconfig, scoped to the
// namespace from the root --namespace flag (empty means the backend default).
func newBackend() (*k8s.Backend, error) {
	b, err := k8s.New(namespaceFlag)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cluster: %w", err)
	}
	return b, nil
}

// deriveSessionID generates a session ID from the current project path.
// It combines the backend name with a short hash of the absolute path so two
// projects can never collide onto the same session/PVC. The result is a valid
// Kubernetes DNS label.
func deriveSessionID(backend, projectPath string) session.ID {
	sum := sha256.Sum256([]byte(projectPath))
	hash := hex.EncodeToString(sum[:])[:10]
	return session.ID(sanitizeLabel(backend) + "-" + hash)
}

// sanitizeLabel lowercases and replaces any non-[a-z0-9-] rune with '-' so the
// value is safe to use in a Kubernetes resource name.
func sanitizeLabel(s string) string {
	b := make([]byte, 0, len(s))
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z':
			b = append(b, byte(c-'A'+'a'))
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-':
			b = append(b, byte(c))
		default:
			b = append(b, '-')
		}
	}
	return string(b)
}

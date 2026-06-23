package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// titleWriter is the subset of the title store the rename command needs. The
// production indexTitleStore satisfies it; tests use an in-memory fake. Keeping
// this an interface makes runRename testable without touching the local index.
type titleWriter interface {
	SaveTitle(id session.ID, title string)
}

// runRename writes a user-chosen RenamedTitle for the session through the title
// store — the same path the TUI `R` overlay uses — so a custom name overrides
// any runner-generated auto title. An empty/whitespace name is rejected so the
// override can't be silently cleared. exists (may be nil) verifies the session
// is real first, so a typo'd id doesn't silently create a phantom index entry.
func runRename(store titleWriter, exists func(session.ID) bool, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("rename: name must not be empty")
	}
	if exists != nil && !exists(session.ID(id)) {
		return fmt.Errorf("rename: session %q not found — run `sandbox status` to list sessions", id)
	}
	store.SaveTitle(session.ID(id), name)
	return nil
}

// applyCreateName persists a --name override for a freshly created session. An
// empty/whitespace name is a no-op so the auto title still applies. It reuses
// the rename write path (RenamedTitle), so the custom name wins immediately.
func applyCreateName(store titleWriter, id session.ID, name string) {
	if strings.TrimSpace(name) == "" {
		return
	}
	store.SaveTitle(id, strings.TrimSpace(name))
}

func newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <session-id> <name>",
		Short: "Set a custom display name for a session (overrides the auto title)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Verify the session exists in the cluster before writing a title, so
			// a typo'd id doesn't create a phantom local index entry. If the
			// cluster is unreachable, don't block the rename (best-effort).
			exists := func(id session.ID) bool {
				backend, err := newBackend()
				if err != nil {
					return true
				}
				st, err := backend.Status(cmd.Context(), session.Ref{ID: id})
				return err == nil && st.Status != session.StatusGone
			}
			if err := runRename(indexTitleStore{}, exists, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Renamed session %q to %q.\n", args[0], args[1])
			return nil
		},
	}
}

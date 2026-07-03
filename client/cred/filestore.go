package cred

import (
	"fmt"
	"os"
	"path/filepath"
)

// secretsDirName is the per-account secret directory under the store root.
const secretsDirName = "anthropic-secrets"

// fileBackend stores each account's secret bytes in its own 0600 file under a
// 0700 directory, mirroring how per-session SSH keys are stored
// (internal/sync/ssh.go). It is the all-platforms fallback and the CI-testable
// backend.
type fileBackend struct {
	dir string // root/anthropic-secrets
}

func newFileBackend(root string) fileBackend {
	return fileBackend{dir: filepath.Join(root, secretsDirName)}
}

// path returns the secret file path for id. Callers validate id against
// accountIDRe before reaching the backend, so id cannot traverse; filepath.Base
// is a defense-in-depth guard.
func (b fileBackend) path(id string) string {
	return filepath.Join(b.dir, filepath.Base(id))
}

func (b fileBackend) put(id string, secret []byte) error {
	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		return fmt.Errorf("cred: mkdir secrets dir: %w", err)
	}
	// WriteFile does not chmod an existing file down to 0600, so remove any
	// prior file first to guarantee owner-only perms on replace.
	p := b.path(id)
	_ = os.Remove(p)
	if err := os.WriteFile(p, secret, 0o600); err != nil {
		// Never include the secret in the error.
		return fmt.Errorf("cred: write secret for %s: %w", id, err)
	}
	return nil
}

func (b fileBackend) get(id string) ([]byte, error) {
	data, err := os.ReadFile(b.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("cred: read secret for %s: %w", id, err)
	}
	return data, nil
}

func (b fileBackend) delete(id string) error {
	if err := os.Remove(b.path(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cred: remove secret for %s: %w", id, err)
	}
	return nil
}

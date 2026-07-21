// Package projpath holds the pure project-path normalization shared by the CLI
// commands (cwd → ProjectPath, internal/cli.resolveProjectPath) and the
// dashboard's new-session directory picker (user-entered paths). Both surfaces
// must produce the identical canonical form — absolute + symlink-resolved —
// because ProjectPath keys the pod workspace mount and the host-compatible
// transcript paths; factoring it here keeps the two from drifting (TODO §9 T10).
package projpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Canonicalize returns path made absolute and symlink-resolved — the canonical
// ProjectPath form. Symlink resolution is best-effort, mirroring the original
// resolveProjectPath: a path whose symlinks cannot be resolved is returned in
// absolute-but-unresolved form rather than failing, so an exotic mount never
// blocks session creation.
func Canonicalize(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	if p, err := filepath.EvalSymlinks(abs); err == nil {
		return p, nil
	}
	return abs, nil
}

// ExpandUser expands a leading "~" (bare or "~/…") to home. Any other input —
// including the multi-user "~name" form, which would need a passwd lookup — is
// returned unchanged. An empty home disables expansion entirely.
func ExpandUser(path, home string) string {
	if home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// ValidateDir normalizes a user-entered directory path — trim, ~-expansion
// against home, absolutization (a relative path resolves against the process
// cwd), symlink resolution — and verifies it names an existing directory. On
// success it returns the canonical form CreateOptions.ProjectPath expects; on
// failure the error is phrased for inline display in the picker form.
func ValidateDir(input, home string) (string, error) {
	p := strings.TrimSpace(input)
	if p == "" {
		return "", errors.New("enter a directory path")
	}
	p, err := Canonicalize(ExpandUser(p, home))
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			// Reason-first phrasing: the picker renders these truncated to one
			// row, and a long path must not push the reason out of view.
			return "", fmt.Errorf("does not exist: %s", p)
		}
		return "", fmt.Errorf("stat %s: %w", p, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("not a directory: %s", p)
	}
	return p, nil
}

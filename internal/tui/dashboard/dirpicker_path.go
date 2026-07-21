package dashboard

// dirpicker_path.go — pure path helpers for the create overlay's directory
// picker (dirpicker.go): Tab-completion of a partially typed directory path
// (child listing + longest-common-prefix extension) and the home-abbreviated
// display form. Validation/normalization itself lives in internal/projpath so
// the CLI-side Creator canonicalizes identically — only the interactive
// affordances live here.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cullenmcdermott/sandbox/internal/projpath"
)

// completeDirPath Tab-completes a partially typed directory path. It splits
// input into a parent directory and a partial leaf, lists the parent's
// subdirectories matching the partial, and returns:
//
//   - the single match completed with a trailing separator (ready to drill
//     deeper with the next Tab), or
//   - the longest common prefix of all matches when that extends the partial, or
//   - the input unchanged when nothing matches (or the parent is unreadable).
//
// A "~/" prefix is preserved in the completed value (matching happens against
// the expanded path, but the user keeps the form they typed). Hidden
// directories are offered only when the partial itself starts with ".", the
// standard shell convention. Only directories complete — files can't be a
// project path.
func completeDirPath(input, home string) string {
	if strings.TrimSpace(input) == "" {
		return input
	}
	expanded := projpath.ExpandUser(input, home)
	// Split into the parent to list and the partial leaf to match. A trailing
	// separator means "list everything inside".
	parent, partial := filepath.Dir(expanded), filepath.Base(expanded)
	if strings.HasSuffix(expanded, string(filepath.Separator)) {
		parent, partial = filepath.Clean(expanded), ""
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return input
	}
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, partial) {
			continue
		}
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(partial, ".") {
			continue // hidden dirs only when explicitly asked for
		}
		if !isDirEntry(parent, e) {
			continue
		}
		matches = append(matches, name)
	}
	if len(matches) == 0 {
		return input
	}
	sep := string(filepath.Separator)
	completed := longestCommonPrefix(matches)
	trailing := len(matches) == 1 // unique match: append the drill-in separator
	if !trailing && len(completed) <= len(partial) {
		return input // ambiguous with nothing new to add; leave the input alone
	}
	result := filepath.Join(parent, completed)
	// Give the user back the ~ form they were typing in.
	if home != "" && strings.HasPrefix(input, "~") {
		result = abbreviateHome(result, home)
	}
	if trailing {
		result += sep
	}
	return result
}

// isDirEntry reports whether a directory entry names a directory, following a
// symlink (a symlinked project dir must be completable, matching ValidateDir's
// stat-through-symlink acceptance).
func isDirEntry(parent string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	fi, err := os.Stat(filepath.Join(parent, e.Name()))
	return err == nil && fi.IsDir()
}

// longestCommonPrefix returns the longest prefix shared by every name.
// Byte-wise, which is exact for the ASCII-dominant path case and merely
// conservative (never wrong, only shorter) at a multi-byte boundary.
func longestCommonPrefix(names []string) string {
	prefix := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

// abbreviateHome renders path with the home directory collapsed to "~" for
// display (rows stay short); the underlying row path is never abbreviated.
func abbreviateHome(path, home string) string {
	if home == "" || path == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) && rel != "." {
		return "~" + string(filepath.Separator) + rel
	}
	return path
}

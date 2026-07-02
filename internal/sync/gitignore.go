package sync

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// gitignoreIgnoreFlags reads the project root's .gitignore and translates it
// into mutagen --ignore flags for the project sync. This is the primary
// laptop→pod secret boundary: whatever the user keeps out of git (.npmrc with
// a token, secrets/, cloud-cred JSON) must also stay out of the pod, and
// mutagen never reads project files itself (verified against v0.18.1 — the
// old "add more via mutagen.yml" escape hatch did not work).
//
// Mutagen's ignore syntax is modeled on gitignore — `/` anchoring, `!`
// negation, `**` — so patterns pass through verbatim after gitignore line
// processing (comments, blanks, trailing unescaped whitespace). Limitations,
// accepted for a defense-in-depth default: nested .gitignore files and git's
// global excludesFile are not consulted.
//
// A missing .gitignore yields no flags. A .gitignore that exists but cannot
// be read is an error: silently syncing everything is the failure mode this
// exists to prevent.
func gitignoreIgnoreFlags(projectPath string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(projectPath, ".gitignore"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("sync: read project .gitignore: %w", err)
	}
	var flags []string
	for _, line := range strings.Split(string(data), "\n") {
		p := parseGitignoreLine(line)
		if p == "" {
			continue
		}
		flags = append(flags, "--ignore="+p)
	}
	return flags, nil
}

// parseGitignoreLine applies gitignore's line rules and returns the pattern,
// or "" when the line contributes nothing (blank, comment, degenerate).
func parseGitignoreLine(line string) string {
	line = strings.TrimSuffix(line, "\r")
	if strings.HasPrefix(line, "#") {
		return "" // comment; a literal leading # is written \# and passes through below
	}
	line = trimTrailingSpaces(line)
	// Degenerate patterns that match nothing in git but that mutagen may
	// reject outright, failing the whole sync create.
	if line == "" || line == "!" || line == "/" {
		return ""
	}
	return line
}

// trimTrailingSpaces strips trailing spaces unless the final space is
// backslash-escaped, mirroring gitignore's trailing-whitespace rule.
func trimTrailingSpaces(s string) string {
	for len(s) > 0 && s[len(s)-1] == ' ' && !strings.HasSuffix(s, `\ `) {
		s = s[:len(s)-1]
	}
	return s
}

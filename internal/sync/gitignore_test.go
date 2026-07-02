package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGitignoreLine(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"node_modules", "node_modules"},
		{"/dist", "/dist"},
		{"!vendor", "!vendor"},
		{"docs/**/*.tmp", "docs/**/*.tmp"},
		{"# a comment", ""},
		{"#", ""},
		{"", ""},
		{"   ", ""},
		{"pattern   ", "pattern"},    // trailing spaces stripped
		{`pattern\ `, `pattern\ `},   // escaped trailing space kept
		{"crlf\r", "crlf"},           // CRLF line ending
		{"!", ""},                    // degenerate: matches nothing in git
		{"/", ""},                    // degenerate: matches nothing in git
		{`\#literal`, `\#literal`},   // escaped # is a pattern, not a comment
		{"trailing\t", "trailing\t"}, // git strips spaces, not tabs
	}
	for _, c := range cases {
		if got := parseGitignoreLine(c.in); got != c.want {
			t.Errorf("parseGitignoreLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGitignoreIgnoreFlags(t *testing.T) {
	dir := t.TempDir()
	content := "# secrets\nsecrets/\n\n*.tfstate\n!keep.tfstate\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	flags, err := gitignoreIgnoreFlags(dir)
	if err != nil {
		t.Fatalf("gitignoreIgnoreFlags: %v", err)
	}
	want := []string{"--ignore=secrets/", "--ignore=*.tfstate", "--ignore=!keep.tfstate"}
	if len(flags) != len(want) {
		t.Fatalf("got %v, want %v", flags, want)
	}
	for i := range want {
		if flags[i] != want[i] {
			t.Errorf("flag %d: got %q, want %q", i, flags[i], want[i])
		}
	}
}

func TestGitignoreIgnoreFlagsMissingFile(t *testing.T) {
	flags, err := gitignoreIgnoreFlags(t.TempDir())
	if err != nil {
		t.Fatalf("missing .gitignore should not error: %v", err)
	}
	if flags != nil {
		t.Errorf("missing .gitignore should yield no flags, got %v", flags)
	}
}

// A .gitignore that exists but cannot be read must fail the sync create:
// silently syncing everything is exactly the leak this boundary prevents.
func TestGitignoreIgnoreFlagsUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; chmod 0 does not block reads") // gate-ok: root bypasses file modes, the perm-denied path is untestable as uid 0
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("secrets/\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := gitignoreIgnoreFlags(dir); err == nil {
		t.Fatal("unreadable .gitignore should surface an error")
	}
}

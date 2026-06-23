package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression for C7: ensureInclude must match the Include line EXACTLY, not by
// substring — otherwise a pre-existing "Include /foo/bar-old" would be mistaken
// for our "Include /foo/bar" and the real Include would never be written,
// silently breaking sync auth.
func TestEnsureIncludeNoSubstringCollision(t *testing.T) {
	dir := t.TempDir()
	include := filepath.Join(dir, "sandbox", "config") // our include path
	userCfg := filepath.Join(dir, "ssh", "config")
	if err := os.MkdirAll(filepath.Dir(userCfg), 0o700); err != nil {
		t.Fatal(err)
	}
	// Seed a colliding line: our path + "-old" is a superstring of our include.
	collide := "Include " + include + "-old\n\nHost example\n  HostName e.com\n"
	if err := os.WriteFile(userCfg, []byte(collide), 0o600); err != nil {
		t.Fatal(err)
	}

	c := NewSSHConfig(include, userCfg)
	if err := c.ensureInclude(); err != nil {
		t.Fatalf("ensureInclude: %v", err)
	}

	data, err := os.ReadFile(userCfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "Include " + include
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimRight(line, "\r\n") == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ensureInclude skipped our Include due to a substring collision; config:\n%s", data)
	}
}

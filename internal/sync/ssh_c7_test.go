package sync

import (
	"fmt"
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
	want := fmt.Sprintf("Include %q", include)
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

// Regression for C5: a state dir with spaces (macOS "Application Support") must
// produce a quoted, valid Include + IdentityFile; and an existing UNQUOTED
// include line written by an older version must still be recognized so
// ensureInclude doesn't prepend a duplicate.
func TestSSHConfigQuotesSpacedPaths(t *testing.T) {
	dir := t.TempDir()
	include := filepath.Join(dir, "Application Support", "sandbox", "config")
	userCfg := filepath.Join(dir, "ssh", "config")
	c := NewSSHConfig(include, userCfg)

	if err := c.Upsert("abc", 12345, filepath.Join(dir, "key dir", "id_ed25519")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	uc, _ := os.ReadFile(userCfg)
	if !strings.Contains(string(uc), fmt.Sprintf("Include %q", include)) {
		t.Errorf("include line not quoted:\n%s", uc)
	}
	body, _ := os.ReadFile(include)
	if !strings.Contains(string(body), fmt.Sprintf("IdentityFile %q", filepath.Join(dir, "key dir", "id_ed25519"))) {
		t.Errorf("IdentityFile not quoted:\n%s", body)
	}

	// Legacy unquoted include already present → recognized, no duplicate.
	legacyCfg := filepath.Join(dir, "ssh", "config2")
	if err := os.WriteFile(legacyCfg, []byte("Include "+include+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c2 := NewSSHConfig(include, legacyCfg)
	if err := c2.ensureInclude(); err != nil {
		t.Fatalf("ensureInclude: %v", err)
	}
	data, _ := os.ReadFile(legacyCfg)
	if n := strings.Count(string(data), "Include "); n != 1 {
		t.Errorf("expected the legacy include to be recognized (1 line), got %d:\n%s", n, data)
	}
}

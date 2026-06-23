package sync

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateKeyPair(t *testing.T) {
	privPEM, authorized, err := GenerateKeyPair("sandbox-test")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// The private key parses and its public half matches the authorized key.
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	parsedAuth, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorized))
	if err != nil {
		t.Fatalf("parse authorized key: %v", err)
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), parsedAuth.Marshal()) {
		t.Error("authorized key does not match private key")
	}
	if !strings.HasPrefix(authorized, "ssh-ed25519 ") {
		t.Errorf("authorized key should be ed25519, got %q", authorized)
	}
	if !strings.HasSuffix(authorized, " sandbox-test") {
		t.Errorf("authorized key should carry comment, got %q", authorized)
	}
}

func TestSSHConfigUpsertAndRemove(t *testing.T) {
	dir := t.TempDir()
	include := filepath.Join(dir, "sandbox", "config")
	userCfg := filepath.Join(dir, "ssh", "config")
	c := NewSSHConfig(include, userCfg)

	if err := c.Upsert("abc", 12345, "/keys/id_ed25519"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// User config now Include-s our file.
	uc, err := os.ReadFile(userCfg)
	if err != nil {
		t.Fatalf("read user config: %v", err)
	}
	if !strings.Contains(string(uc), "Include "+include) {
		t.Errorf("user config missing include:\n%s", uc)
	}

	// Include file has the host block with the right port and identity.
	body, _ := os.ReadFile(include)
	for _, want := range []string{"Host sandbox-abc", "Port 12345", "IdentityFile /keys/id_ed25519", "StrictHostKeyChecking no"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("include missing %q:\n%s", want, body)
		}
	}

	// Re-upsert with a new port replaces rather than duplicating the block.
	if err := c.Upsert("abc", 54321, "/keys/id_ed25519"); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	body, _ = os.ReadFile(include)
	if n := strings.Count(string(body), "Host sandbox-abc"); n != 1 {
		t.Errorf("expected 1 host block, got %d:\n%s", n, body)
	}
	if !strings.Contains(string(body), "Port 54321") || strings.Contains(string(body), "Port 12345") {
		t.Errorf("port not updated:\n%s", body)
	}

	// A second session coexists.
	if err := c.Upsert("xyz", 9999, "/keys/other"); err != nil {
		t.Fatalf("upsert second: %v", err)
	}
	body, _ = os.ReadFile(include)
	if !strings.Contains(string(body), "Host sandbox-abc") || !strings.Contains(string(body), "Host sandbox-xyz") {
		t.Errorf("both sessions should be present:\n%s", body)
	}

	// Remove drops only the named block.
	if err := c.Remove("abc"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	body, _ = os.ReadFile(include)
	if strings.Contains(string(body), "Host sandbox-abc") {
		t.Errorf("sandbox-abc should be removed:\n%s", body)
	}
	if !strings.Contains(string(body), "Host sandbox-xyz") {
		t.Errorf("sandbox-xyz should remain:\n%s", body)
	}
}

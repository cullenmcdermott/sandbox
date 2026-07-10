package sync

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// sessionIDRe constrains the characters allowed in a session ID before it is
// interpolated into an ssh config Host alias or a --label-selector. This is
// defense-in-depth against directive injection: a session ID containing a
// newline or whitespace could otherwise inject arbitrary ssh config directives
// (e.g. a malicious ProxyCommand) into the per-session block.
var sessionIDRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// validateSessionID rejects a session ID that does not match [a-z0-9-]+.
func validateSessionID(sessionID string) error {
	if !sessionIDRe.MatchString(sessionID) {
		return fmt.Errorf("ssh: invalid session ID %q: must match [a-z0-9-]+", sessionID)
	}
	return nil
}

// GenerateKeyPair creates an ed25519 SSH key pair for a session's Mutagen
// transport. It returns the private key in OpenSSH PEM form (for the local
// IdentityFile) and the public key in authorized_keys form (for the pod's
// per-session Secret).
func GenerateKeyPair(comment string) (privatePEM []byte, authorizedKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("ssh: generate ed25519 key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, "", fmt.Errorf("ssh: marshal private key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", fmt.Errorf("ssh: build public key: %w", err)
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if comment != "" {
		authorized += " " + comment
	}
	return pem.EncodeToMemory(block), authorized, nil
}

// SSHConfig manages a dedicated OpenSSH config file holding one Host alias per
// session. It is kept separate from the user's ~/.ssh/config and pulled in via
// an Include directive so Mutagen (which shells out to the system ssh) can
// resolve the per-session alias without us editing the user's main config body.
type SSHConfig struct {
	// path is the dedicated include file (e.g.
	// ~/.local/share/sandbox/ssh/config).
	path string
	// userConfig is the user's ~/.ssh/config that Include-s path.
	userConfig string
}

// NewSSHConfig returns an SSHConfig writing to includePath, included from
// userConfigPath.
func NewSSHConfig(includePath, userConfigPath string) *SSHConfig {
	return &SSHConfig{path: includePath, userConfig: userConfigPath}
}

// Alias returns the Host alias name for a session.
func Alias(sessionID string) string { return "sandbox-" + sessionID }

// Upsert writes (or replaces) the Host block for a session pointing at the
// local end of the port-forward, using the given identity file. It also
// ensures the user's ssh config Include-s this file.
func (c *SSHConfig) Upsert(sessionID string, localPort int, identityFile string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if err := c.ensureInclude(); err != nil {
		return err
	}
	existing, err := os.ReadFile(c.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("ssh: read include config: %w", err)
	}
	block := c.block(sessionID, localPort, identityFile)
	updated := replaceBlock(string(existing), Alias(sessionID), block)
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	return atomicWrite(c.path, []byte(updated), 0o600)
}

// Remove deletes a session's Host block.
func (c *SSHConfig) Remove(sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	existing, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	updated := replaceBlock(string(existing), Alias(sessionID), "")
	return atomicWrite(c.path, []byte(updated), 0o600)
}

func (c *SSHConfig) block(sessionID string, localPort int, identityFile string) string {
	// StrictHostKeyChecking is disabled and known-hosts discarded because the
	// host is always a fresh 127.0.0.1:<ephemeral-port> port-forward; the
	// authentication boundary is the per-session key, not the host identity.
	var b strings.Builder
	fmt.Fprintf(&b, "Host %s\n", Alias(sessionID))
	fmt.Fprintf(&b, "    HostName 127.0.0.1\n")
	fmt.Fprintf(&b, "    Port %d\n", localPort)
	fmt.Fprintf(&b, "    User root\n")
	// Quoted (C5): the state dir is caller-configurable (client.WithStateDir)
	// and macOS app dirs contain spaces — unquoted, the whole config goes
	// invalid and every sync fails obscurely.
	fmt.Fprintf(&b, "    IdentityFile %q\n", identityFile)
	fmt.Fprintf(&b, "    IdentitiesOnly yes\n")
	fmt.Fprintf(&b, "    StrictHostKeyChecking no\n")
	fmt.Fprintf(&b, "    UserKnownHostsFile /dev/null\n")
	fmt.Fprintf(&b, "    LogLevel ERROR\n")
	return b.String()
}

// ensureInclude makes sure the user's ~/.ssh/config begins with an Include of
// our dedicated file. It is idempotent.
func (c *SSHConfig) ensureInclude() error {
	// Quoted (C5) so a state dir with spaces stays valid; the unquoted form is
	// still accepted below so configs written by older versions don't get a
	// duplicate include prepended.
	includeLine := fmt.Sprintf("Include %q", c.path)
	legacyLine := "Include " + c.path
	data, err := os.ReadFile(c.userConfig)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("ssh: read user config: %w", err)
	}
	// C7: check line-by-line for an exact match to avoid substring collisions
	// (e.g. "Include /foo/bar-old" would incorrectly match "Include /foo/bar").
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if t := strings.TrimRight(line, "\r\n"); t == includeLine || t == legacyLine {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(c.userConfig), 0o700); err != nil {
		return err
	}
	// Include must come before any Host blocks to take effect, so prepend it.
	merged := includeLine + "\n\n" + string(data)
	return atomicWrite(c.userConfig, []byte(merged), 0o600)
}

// replaceBlock returns src with the "Host <alias>" block replaced by
// replacement (or removed if replacement is empty). A block runs from its
// "Host " line until the next "Host " line or end of file.
func replaceBlock(src, alias, replacement string) string {
	lines := strings.Split(src, "\n")
	var out []string
	inBlock := false
	header := "Host " + alias
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Host ") {
			// Leaving any prior matched block; entering a new one.
			inBlock = trimmed == header
			if inBlock {
				continue // drop the old block's header; replacement adds it
			}
		}
		if inBlock {
			continue // drop old block body lines
		}
		out = append(out, line)
	}
	result := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if replacement == "" {
		if result == "" {
			return ""
		}
		return result + "\n"
	}
	if result == "" {
		return replacement
	}
	return result + "\n\n" + replacement
}

// atomicWrite writes data to path via a temp file + rename so a crash mid-write
// cannot corrupt path. Mode is applied to the temp file before rename (C3).
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ssh-config-tmp-*")
	if err != nil {
		return fmt.Errorf("atomic write: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName) // no-op after successful rename
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("atomic write: chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("atomic write: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomic write: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("atomic write: rename: %w", err)
	}
	return nil
}

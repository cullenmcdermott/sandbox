package cli

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/cullenmcdermott/sandbox/internal/index"
	syncpkg "github.com/cullenmcdermott/sandbox/internal/sync"
)

const (
	// remoteWorkspaceRoot mirrors the runner's WORKSPACE_ROOT; the project is
	// synced to remoteWorkspaceRoot + the absolute host project path.
	remoteWorkspaceRoot = "/session/workspace"
	// remoteClaudeDir mirrors the runner's CLAUDE_CONFIG_DIR.
	remoteClaudeDir = "/session/state/claude"
)

// newIndex returns the local session index at the default path.
func newIndex() (*index.Index, error) {
	return index.NewDefault()
}

// sessionKeyDir returns the local directory holding a session's SSH key.
func sessionKeyDir(id string) (string, error) {
	root, err := index.DefaultRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, id), nil
}

// ensureSSHKey returns the local private key path and the authorized (public)
// key for a session, generating and persisting a new ed25519 keypair on first
// use. The same key is reused across reconnects so it keeps matching the public
// key stored in the pod's per-session Secret.
func ensureSSHKey(id string) (privPath, authorizedKey string, err error) {
	dir, err := sessionKeyDir(id)
	if err != nil {
		return "", "", err
	}
	privPath = filepath.Join(dir, "id_ed25519")
	pubPath := privPath + ".pub"

	if priv, rerr := os.ReadFile(privPath); rerr == nil {
		if pub, perr := os.ReadFile(pubPath); perr == nil {
			return privPath, strings.TrimSpace(string(pub)), nil
		}
		// .pub missing — derive it from the private key.
		if signer, serr := ssh.ParsePrivateKey(priv); serr == nil {
			auth := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
			return privPath, auth, nil
		}
	}

	priv, auth, err := syncpkg.GenerateKeyPair("sandbox-" + id)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(privPath, priv, 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(pubPath, []byte(auth+"\n"), 0o644); err != nil {
		return "", "", err
	}
	return privPath, auth, nil
}

// sshConfigManager returns the per-session SSH alias manager.
func sshConfigManager() (*syncpkg.SSHConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root, err := index.DefaultRoot()
	if err != nil {
		return nil, err
	}
	// ~/.local/share/sandbox/ssh/config, included from ~/.ssh/config.
	include := filepath.Join(filepath.Dir(root), "ssh", "config")
	userCfg := filepath.Join(home, ".ssh", "config")
	return syncpkg.NewSSHConfig(include, userCfg), nil
}

// syncManager returns a Mutagen sync Manager backed by the mutagen CLI.
func syncManager() *syncpkg.Manager {
	return syncpkg.New(syncpkg.NewExecRunner(""))
}

// startMutagen writes the SSH alias for the current port-forward and (re)creates
// the session's Mutagen sync sessions. It is idempotent across reconnects.
func startMutagen(ctx context.Context, id, projectPath, privPath string, sshLocalPort int) error {
	cfg, err := sshConfigManager()
	if err != nil {
		return err
	}
	if err := cfg.Upsert(id, sshLocalPort, privPath); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return syncManager().CreateAll(ctx, syncpkg.Spec{
		SessionID:    id,
		ProjectPath:  projectPath,
		RemotePath:   path.Join(remoteWorkspaceRoot, projectPath),
		HomeDir:      home,
		SSHHost:      syncpkg.Alias(id),
		RemoteClaude: remoteClaudeDir,
	})
}

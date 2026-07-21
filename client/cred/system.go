package cred

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// systemKeychainService is the generic-password service name Claude Code itself
// uses for its login credential on macOS. Read-only from our side: the item is
// owned and refreshed by Claude Code.
const systemKeychainService = "Claude Code-credentials"

// ErrNoFullCredential is returned when a credential document is present but
// does not hold a full OAuth credential (access + refresh token). A claude-pane
// session needs the full credential to run the interactive client in
// subscription (Max) mode with in-pod refresh — interactive claude REJECTS
// partial credentials outright (a setup token wrapped in the credentials shape
// boots to a "Not logged in" wall, observed live 2026-07-20), so every
// provisioning path validates fullness fail-closed.
var ErrNoFullCredential = errors.New("cred: not a full Claude Code OAuth credential (accessToken + refreshToken required)")

// SystemMaterial reads the host's own Claude Code login — the credential and
// account identity Claude Code itself maintains — as claude-pane provisioning
// Material. This is the Max-mode source: unlike the store's per-account setup
// tokens (see ProvisionMaterial), the system login carries the full
// claudeAiOauth document (refresh token, scopes, subscription type), which is
// what the interactive pane needs for subscription mode and in-pod refresh.
//
// Sources, matching Claude Code's own layout:
//   - credential: the "Claude Code-credentials" Keychain item on macOS, else
//     <configDir>/.credentials.json
//   - identity: the "oauthAccount" object of <configDir>/../.claude.json when
//     configDir is set, else ~/.claude.json
//
// configDir "" resolves to $CLAUDE_CONFIG_DIR, else ~/.claude. Both returned
// documents are passed through as raw bytes (never re-marshaled) so fields this
// package does not know about survive verbatim. Fail-closed: a missing source
// maps to ErrNotFound, a credential without accessToken+refreshToken to
// ErrNoFullCredential. Errors never echo credential bytes.
func SystemMaterial(configDir string) (Material, error) {
	return systemMaterial(configDir, realExec, runtime.GOOS)
}

// systemMaterial is the injectable core of SystemMaterial: tests supply a fake
// execRunner and a goos to exercise both the Keychain and file paths without a
// real login.
func systemMaterial(configDir string, run execRunner, goos string) (Material, error) {
	dir, stateFile, err := systemPaths(configDir)
	if err != nil {
		return Material{}, err
	}

	creds, err := systemCredentialBytes(dir, run, goos)
	if err != nil {
		return Material{}, err
	}
	if err := ValidateFullCredential(creds); err != nil {
		return Material{}, err
	}

	acct, err := systemAccountBytes(stateFile)
	if err != nil {
		return Material{}, err
	}
	return Material{CredentialsJSON: creds, AccountJSON: acct}, nil
}

// systemPaths resolves the config dir (credential file location) and the state
// file (.claude.json) location for a given configDir argument, mirroring Claude
// Code's own resolution: an explicit config dir keeps .claude.json inside it;
// the default layout keeps ~/.claude.json BESIDE ~/.claude, not inside.
func systemPaths(configDir string) (dir, stateFile string, err error) {
	if configDir == "" {
		configDir = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if configDir != "" {
		return configDir, filepath.Join(configDir, ".claude.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("cred: resolve home for system Claude login: %w", err)
	}
	return filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json"), nil
}

// systemCredentialBytes reads the raw credential document from the platform
// source: the Claude Code Keychain item on darwin (no account selector — the
// single item is keyed by OS user), else <dir>/.credentials.json.
func systemCredentialBytes(dir string, run execRunner, goos string) ([]byte, error) {
	if goos == "darwin" {
		if _, err := os.Stat(securityBin); err == nil {
			out, code, err := run(context.Background(), securityBin,
				[]string{"find-generic-password", "-s", systemKeychainService, "-w"}, nil)
			if code == keychainNotFound {
				return nil, fmt.Errorf("cred: system Claude Code login: %w", ErrNotFound)
			}
			if err != nil {
				return nil, fmt.Errorf("cred: read system Claude Code login from keychain: %w (exit %d)", err, code)
			}
			return trimTrailingNewlines(out), nil
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cred: system Claude Code login: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("cred: read system Claude Code credentials file: %w", err)
	}
	return b, nil
}

// ValidateFullCredential checks — without retaining or logging the values —
// that raw parses as {"claudeAiOauth": {...}} with a non-empty access AND
// refresh token. Partial credentials (e.g. a setup token wrapped in the
// credentials shape) are rejected: see ErrNoFullCredential. Exported so every
// claude-pane provisioning path (SystemMaterial, the client's material setter,
// external SDK consumers) can apply the same fail-closed gate.
func ValidateFullCredential(raw []byte) error {
	var doc struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("cred: system Claude Code credential is not valid JSON: %w", err)
	}
	if doc.ClaudeAiOauth.AccessToken == "" || doc.ClaudeAiOauth.RefreshToken == "" {
		return ErrNoFullCredential
	}
	return nil
}

// systemAccountBytes extracts the oauthAccount object from the Claude Code
// state file and re-wraps it as the {"oauthAccount": {...}} envelope the
// .claude.json seed uses. The object is carried as raw JSON so identity fields
// this package does not model (account uuid, email, organization, tiers)
// survive verbatim.
func systemAccountBytes(stateFile string) ([]byte, error) {
	b, err := os.ReadFile(stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cred: system Claude Code state file %s: %w", filepath.Base(stateFile), ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("cred: read system Claude Code state file: %w", err)
	}
	var doc struct {
		OauthAccount json.RawMessage `json:"oauthAccount"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("cred: system Claude Code state file is not valid JSON: %w", err)
	}
	if len(doc.OauthAccount) == 0 || string(doc.OauthAccount) == "null" {
		return nil, fmt.Errorf("cred: system Claude Code state file has no oauthAccount: %w", ErrNotFound)
	}
	wrapped, err := json.Marshal(struct {
		OauthAccount json.RawMessage `json:"oauthAccount"`
	}{OauthAccount: doc.OauthAccount})
	if err != nil {
		return nil, fmt.Errorf("cred: wrap oauthAccount: %w", err)
	}
	return wrapped, nil
}

// trimTrailingNewlines strips the trailing newline `security -w` appends
// without touching any other byte of the credential document.
func trimTrailingNewlines(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

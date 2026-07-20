package cred

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fullCredFixture = `{"claudeAiOauth":{"accessToken":"at-1","refreshToken":"rt-1","expiresAt":1,"scopes":["user:inference"],"subscriptionType":"max","futureField":"kept"}}`

func writeSystemState(t *testing.T, dir string, oauthAccount string) string {
	t.Helper()
	stateFile := filepath.Join(dir, ".claude.json")
	body := `{"numStartups":3,"oauthAccount":` + oauthAccount + `}`
	if err := os.WriteFile(stateFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return stateFile
}

// The file path (non-darwin): full credential + oauthAccount round-trip with
// unknown fields preserved verbatim.
func TestSystemMaterialFileSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(fullCredFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSystemState(t, dir, `{"accountUuid":"u-1","emailAddress":"a@b.c","organizationUuid":"o-1"}`)

	m, err := systemMaterial(dir, nil, "linux")
	if err != nil {
		t.Fatalf("systemMaterial: %v", err)
	}
	if string(m.CredentialsJSON) != fullCredFixture {
		t.Fatalf("credentials not passed through verbatim:\n%s", m.CredentialsJSON)
	}
	var acct struct {
		OauthAccount map[string]any `json:"oauthAccount"`
	}
	if err := json.Unmarshal(m.AccountJSON, &acct); err != nil {
		t.Fatalf("account JSON invalid: %v", err)
	}
	if acct.OauthAccount["accountUuid"] != "u-1" || acct.OauthAccount["emailAddress"] != "a@b.c" {
		t.Fatalf("oauthAccount fields not preserved: %v", acct.OauthAccount)
	}
}

// The darwin path reads the Claude Code keychain item via the exec seam; the
// -w output's trailing newline is stripped and nothing else is altered.
func TestSystemMaterialKeychainSource(t *testing.T) {
	if _, err := os.Stat(securityBin); err != nil {
		t.Skipf("no %s on this host", securityBin) // gate-ok: darwin-only keychain path, self-skips visibly on non-macOS; the exec seam is faked so darwin/CI run it for real
	}
	dir := t.TempDir()
	writeSystemState(t, dir, `{"accountUuid":"u-2"}`)

	var gotArgs []string
	fake := func(_ context.Context, name string, args []string, stdin []byte) ([]byte, int, error) {
		gotArgs = append([]string{name}, args...)
		if stdin != nil {
			t.Fatal("system credential read must not send stdin")
		}
		return []byte(fullCredFixture + "\n"), 0, nil
	}
	m, err := systemMaterial(dir, fake, "darwin")
	if err != nil {
		t.Fatalf("systemMaterial: %v", err)
	}
	if string(m.CredentialsJSON) != fullCredFixture {
		t.Fatalf("keychain credential altered:\n%s", m.CredentialsJSON)
	}
	want := securityBin + " find-generic-password -s " + systemKeychainService + " -w"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Fatalf("keychain command = %q, want %q", got, want)
	}
}

// A credential without a refresh token (e.g. a setup token wrapped in the
// credentials shape) is rejected fail-closed.
func TestSystemMaterialRejectsPartialCredential(t *testing.T) {
	dir := t.TempDir()
	partial := `{"claudeAiOauth":{"accessToken":"sk-ant-oat-only"}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSystemState(t, dir, `{"accountUuid":"u-3"}`)

	_, err := systemMaterial(dir, nil, "linux")
	if !errors.Is(err, ErrNoFullCredential) {
		t.Fatalf("err = %v, want ErrNoFullCredential", err)
	}
	if strings.Contains(err.Error(), "sk-ant-oat-only") {
		t.Fatal("error echoes credential bytes")
	}
}

// Missing sources map to ErrNotFound: no credentials file, and separately no
// oauthAccount in the state file.
func TestSystemMaterialMissingSources(t *testing.T) {
	dir := t.TempDir()
	if _, err := systemMaterial(dir, nil, "linux"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing credentials: err = %v, want ErrNotFound", err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(fullCredFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	// State file present but without an oauthAccount object.
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"numStartups":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := systemMaterial(dir, nil, "linux"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing oauthAccount: err = %v, want ErrNotFound", err)
	}
}

// An explicit configDir keeps .claude.json inside it; the default layout is
// exercised by the other tests via explicit dirs, and the env fallback is
// honored.
func TestSystemPathsEnvFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	gotDir, gotState, err := systemPaths("")
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != dir || gotState != filepath.Join(dir, ".claude.json") {
		t.Fatalf("systemPaths env fallback = (%q, %q)", gotDir, gotState)
	}
}

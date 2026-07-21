package authstatus

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) Env {
	return func(k string) string { return m[k] }
}

// makeJWT builds an unsigned JWT carrying only an exp claim (for expiry tests).
func makeJWT(exp int64) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))
	return "hdr." + payload + ".sig"
}

func writeCodexAuth(t *testing.T, home, mode, accessToken string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"auth_mode":%q,"tokens":{"access_token":%q}}`, mode, accessToken)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeClaudeCreds drops an (empty-object) .credentials.json under dir — the
// presence-only probe stats it, never parses it.
func writeClaudeCreds(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// keychain fabricates an injected Keychain probe result; keychainUnavailable
// models a darwin host without the security binary. noKeychain asserts the
// probe is never consulted (non-darwin paths).
func keychain(present bool) func() (bool, bool) { return func() (bool, bool) { return present, true } }
func keychainUnavailable() (bool, bool)         { return false, false }
func noKeychain(t *testing.T) func() (bool, bool) {
	return func() (bool, bool) {
		t.Error("Keychain probe consulted on a non-darwin GOOS")
		return false, false
	}
}

func TestClaudeProvider(t *testing.T) {
	tests := []struct {
		name string
		// build returns the provider under test; helpers create temp dirs.
		build      func(t *testing.T) ClaudeProvider
		wantMethod Method
		wantLevel  Level
		wantDetail string // substring
	}{
		{
			name: "darwin keychain hit is the primary source",
			build: func(t *testing.T) ClaudeProvider {
				return ClaudeProvider{GOOS: "darwin", Home: t.TempDir(), Keychain: keychain(true)}
			},
			wantMethod: MethodOAuth, wantLevel: LevelOK, wantDetail: "host Claude Code login (Keychain)",
		},
		{
			name: "darwin keychain hit beats env vars",
			build: func(t *testing.T) ClaudeProvider {
				return ClaudeProvider{
					GOOS: "darwin", Home: t.TempDir(), Keychain: keychain(true),
					Env: env(map[string]string{"ANTHROPIC_API_KEY": "sk-y"}),
				}
			},
			wantMethod: MethodOAuth, wantLevel: LevelOK, wantDetail: "host Claude Code login",
		},
		{
			name: "darwin keychain miss is final — no credentials-file fallback (mirrors cred.SystemMaterial)",
			build: func(t *testing.T) ClaudeProvider {
				home := t.TempDir()
				writeClaudeCreds(t, filepath.Join(home, ".claude"))
				return ClaudeProvider{GOOS: "darwin", Home: home, Keychain: keychain(false)}
			},
			wantMethod: MethodNone, wantLevel: LevelBad, wantDetail: "no host Claude Code login",
		},
		{
			name: "darwin without security binary falls back to the credentials file",
			build: func(t *testing.T) ClaudeProvider {
				home := t.TempDir()
				writeClaudeCreds(t, filepath.Join(home, ".claude"))
				return ClaudeProvider{GOOS: "darwin", Home: home, Keychain: keychainUnavailable}
			},
			wantMethod: MethodOAuth, wantLevel: LevelOK, wantDetail: "host Claude Code login (.credentials.json)",
		},
		{
			name: "non-darwin credentials-file hit",
			build: func(t *testing.T) ClaudeProvider {
				home := t.TempDir()
				writeClaudeCreds(t, filepath.Join(home, ".claude"))
				return ClaudeProvider{GOOS: "linux", Home: home, Keychain: noKeychain(t)}
			},
			wantMethod: MethodOAuth, wantLevel: LevelOK, wantDetail: ".credentials.json",
		},
		{
			name: "CLAUDE_CONFIG_DIR overrides the ~/.claude default",
			build: func(t *testing.T) ClaudeProvider {
				cfg := t.TempDir()
				writeClaudeCreds(t, cfg)
				return ClaudeProvider{
					GOOS: "linux", Home: t.TempDir(), Keychain: noKeychain(t),
					Env: env(map[string]string{"CLAUDE_CONFIG_DIR": cfg}),
				}
			},
			wantMethod: MethodOAuth, wantLevel: LevelOK, wantDetail: ".credentials.json",
		},
		{
			name: "CLAUDE_CONFIG_DIR is exclusive — no ~/.claude fallback (mirrors cred systemPaths)",
			build: func(t *testing.T) ClaudeProvider {
				home := t.TempDir()
				writeClaudeCreds(t, filepath.Join(home, ".claude"))
				return ClaudeProvider{
					GOOS: "linux", Home: home, Keychain: noKeychain(t),
					Env: env(map[string]string{"CLAUDE_CONFIG_DIR": t.TempDir()}),
				}
			},
			wantMethod: MethodNone, wantLevel: LevelBad, wantDetail: "no host Claude Code login",
		},
		{
			name: "setup-token env is secondary: configured but degraded",
			build: func(t *testing.T) ClaudeProvider {
				return ClaudeProvider{
					GOOS: "linux", Home: t.TempDir(), Keychain: noKeychain(t),
					Env: env(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "hunter2oauth"}),
				}
			},
			wantMethod: MethodOAuth, wantLevel: LevelWarn, wantDetail: "headless/SDK only",
		},
		{
			name: "api-key env is secondary: configured but degraded",
			build: func(t *testing.T) ClaudeProvider {
				return ClaudeProvider{
					GOOS: "linux", Home: t.TempDir(), Keychain: noKeychain(t),
					Env: env(map[string]string{"ANTHROPIC_API_KEY": "hunter2apikey"}),
				}
			},
			wantMethod: MethodAPIKey, wantLevel: LevelWarn, wantDetail: "headless/SDK only",
		},
		{
			name: "nothing anywhere",
			build: func(t *testing.T) ClaudeProvider {
				return ClaudeProvider{GOOS: "linux", Home: t.TempDir(), Keychain: noKeychain(t)}
			},
			wantMethod: MethodNone, wantLevel: LevelBad, wantDetail: "run `claude` and log in",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.build(t).Status(context.Background())
			if got.Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", got.Method, tt.wantMethod)
			}
			if got.Level() != tt.wantLevel {
				t.Errorf("level = %d, want %d (detail %q)", got.Level(), tt.wantLevel, got.Detail)
			}
			if (got.Method != MethodNone) != got.Configured {
				t.Errorf("configured=%v inconsistent with method %q", got.Configured, got.Method)
			}
			if !strings.Contains(got.Detail, tt.wantDetail) {
				t.Errorf("detail = %q, want contains %q", got.Detail, tt.wantDetail)
			}
			// The presence probe must never leak credential-ish values.
			for _, secret := range []string{"hunter2oauth", "hunter2apikey", "sk-y"} {
				if strings.Contains(got.Detail, secret) {
					t.Errorf("detail leaked an env credential value: %q", got.Detail)
				}
			}
		})
	}
}

func TestSystemLoginProbeCore(t *testing.T) {
	// systemLoginPresent is the core SystemLogin (the doctor-facing wrapper)
	// delegates to. The wrapper itself has no GOOS seam — calling it on a
	// darwin dev host would shell out to the real `security` — so pin the
	// delegated core on the platform-independent file path instead.
	home := t.TempDir()
	writeClaudeCreds(t, filepath.Join(home, ".claude"))
	present, source := ClaudeProvider{GOOS: "linux", Home: home}.systemLoginPresent()
	if !present || source != ".credentials.json" {
		t.Errorf("systemLoginPresent = %v/%q, want true/.credentials.json", present, source)
	}
	if absent, _ := (ClaudeProvider{GOOS: "linux", Home: t.TempDir()}).systemLoginPresent(); absent {
		t.Error("systemLoginPresent should be false for an empty home")
	}
}

func TestCodexProviderAuthFile(t *testing.T) {
	now := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	future := makeJWT(now.Add(10 * 24 * time.Hour).Unix())
	past := makeJWT(now.Add(-time.Hour).Unix())

	tests := []struct {
		name          string
		mode          string
		token         string
		wantMethod    Method
		wantLevel     Level
		wantExpired   bool
		wantDetailHas string
	}{
		{"chatgpt oauth + valid token", "chatgpt", future, MethodOAuth, LevelOK, false, "expires in 10d"},
		{"chatgpt oauth + expired token", "chatgpt", past, MethodOAuth, LevelWarn, true, "EXPIRED"},
		{"api key mode", "apikey", "", MethodAPIKey, LevelOK, false, "API key"},
		{"unknown mode", "weird", "", MethodUnknown, LevelWarn, false, "auth_mode=weird"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeCodexAuth(t, home, tt.mode, tt.token)
			got := CodexProvider{Home: home, Now: func() time.Time { return now }}.Status(context.Background())
			if got.Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", got.Method, tt.wantMethod)
			}
			if got.Level() != tt.wantLevel {
				t.Errorf("level = %d, want %d (detail %q)", got.Level(), tt.wantLevel, got.Detail)
			}
			if got.Expired != tt.wantExpired {
				t.Errorf("expired = %v, want %v", got.Expired, tt.wantExpired)
			}
			if !strings.Contains(got.Detail, tt.wantDetailHas) {
				t.Errorf("detail = %q, want contains %q", got.Detail, tt.wantDetailHas)
			}
			if got.Detail != "" && strings.Contains(got.Detail, tt.token) && tt.token != "" {
				t.Errorf("detail leaked the access token: %q", got.Detail)
			}
		})
	}
}

func TestCodexProviderFallbackAndMissing(t *testing.T) {
	// No auth.json, but OPENAI_API_KEY set -> api key.
	home := t.TempDir()
	got := CodexProvider{Home: home, Env: env(map[string]string{"OPENAI_API_KEY": "sk-x"})}.Status(context.Background())
	if got.Method != MethodAPIKey || !got.Configured {
		t.Errorf("fallback: got method=%q configured=%v, want api-key/true", got.Method, got.Configured)
	}
	// Nothing at all -> none.
	got = CodexProvider{Home: t.TempDir(), Env: env(map[string]string{})}.Status(context.Background())
	if got.Method != MethodNone || got.Configured {
		t.Errorf("missing: got method=%q configured=%v, want none/false", got.Method, got.Configured)
	}
}

func TestOpenCodeProvider(t *testing.T) {
	got := OpenCodeProvider{Env: env(map[string]string{"ANTHROPIC_API_KEY": "x"})}.Status(context.Background())
	if !got.Configured || got.Method != MethodAPIKey {
		t.Errorf("got configured=%v method=%q, want true/api-key", got.Configured, got.Method)
	}
	if len(got.Sub) != len(opencodeProviders) {
		t.Fatalf("got %d sub-statuses, want %d", len(got.Sub), len(opencodeProviders))
	}
	if !got.Sub[0].Configured || got.Sub[0].Name != "opencode/anthropic" {
		t.Errorf("anthropic sub = %+v, want configured", got.Sub[0])
	}
	if got.Sub[1].Configured { // openai unset
		t.Errorf("openai sub should be unconfigured: %+v", got.Sub[1])
	}
	if !strings.Contains(got.Detail, "1/3") {
		t.Errorf("detail = %q, want 1/3 providers", got.Detail)
	}

	// No providers -> none/bad.
	none := OpenCodeProvider{Env: env(map[string]string{})}.Status(context.Background())
	if none.Configured || none.Level() != LevelBad {
		t.Errorf("empty opencode: configured=%v level=%d, want false/bad", none.Configured, none.Level())
	}
}

func TestReportAndDefaultProviders(t *testing.T) {
	home := t.TempDir()
	provs := DefaultProviders(env(map[string]string{"ANTHROPIC_API_KEY": "x"}), home)
	// Pin the claude provider to the file path so the test never touches the
	// host Keychain when it runs on a darwin machine.
	cp, ok := provs[0].(ClaudeProvider)
	if !ok {
		t.Fatalf("provs[0] is %T, want ClaudeProvider", provs[0])
	}
	if cp.Home != home {
		t.Errorf("DefaultProviders claude Home = %q, want %q", cp.Home, home)
	}
	cp.GOOS = "linux"
	provs[0] = cp
	rep := Report(context.Background(), provs...)
	if len(rep) != 3 {
		t.Fatalf("report has %d entries, want 3", len(rep))
	}
	names := []string{rep[0].Name, rep[1].Name, rep[2].Name}
	want := []string{"claude", "codex", "opencode"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("report[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

package cred

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

func TestClaudeProvider(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		wantMethod Method
		wantLevel  Level
	}{
		{"oauth subscription", map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "x"}, MethodOAuth, LevelOK},
		{"api key", map[string]string{"ANTHROPIC_API_KEY": "x"}, MethodAPIKey, LevelOK},
		{"oauth wins over api key", map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "x", "ANTHROPIC_API_KEY": "y"}, MethodOAuth, LevelOK},
		{"none", map[string]string{}, MethodNone, LevelBad},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClaudeProvider{Env: env(tt.env)}.Status(context.Background())
			if got.Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", got.Method, tt.wantMethod)
			}
			if got.Level() != tt.wantLevel {
				t.Errorf("level = %d, want %d", got.Level(), tt.wantLevel)
			}
			if (got.Method != MethodNone) != got.Configured {
				t.Errorf("configured=%v inconsistent with method %q", got.Configured, got.Method)
			}
		})
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
		wantDetailHas string
	}{
		{"chatgpt oauth + valid token", "chatgpt", future, MethodOAuth, LevelOK, "expires in 10d"},
		{"chatgpt oauth + expired token", "chatgpt", past, MethodOAuth, LevelWarn, "EXPIRED"},
		{"api key mode", "apikey", "", MethodAPIKey, LevelOK, "API key"},
		{"unknown mode", "weird", "", MethodUnknown, LevelWarn, "auth_mode=weird"},
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

package cred

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestProvisionMaterialRoundTrip: a subscription account's stored setup token is
// wrapped into the .credentials.json shape (claudeAiOauth.accessToken), and the
// oauthAccount identity carries the store's account id + label. Both blobs
// round-trip the fields the store actually holds.
func TestProvisionMaterialRoundTrip(t *testing.T) {
	s := NewFileStore(t.TempDir())
	acct := NewAccount("claude.ai", AccountSubscription)
	const token = "sk-ant-oat01-abcDEF_123-xyz"
	if err := s.Add(acct, []byte(token)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	mat, err := ProvisionMaterial(s, acct.ID)
	if err != nil {
		t.Fatalf("ProvisionMaterial: %v", err)
	}

	// Credentials blob: {"claudeAiOauth": {"accessToken": <token>}}.
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(mat.CredentialsJSON, &creds); err != nil {
		t.Fatalf("unmarshal credentials: %v", err)
	}
	if creds.ClaudeAiOauth.AccessToken != token {
		t.Errorf("accessToken: got %q, want %q", creds.ClaudeAiOauth.AccessToken, token)
	}
	// The store holds no refresh token, so it must be omitted (not an empty
	// string masquerading as a real value).
	if creds.ClaudeAiOauth.RefreshToken != "" {
		t.Errorf("refreshToken should be absent, got %q", creds.ClaudeAiOauth.RefreshToken)
	}
	if !strings.Contains(string(mat.CredentialsJSON), "claudeAiOauth") {
		t.Errorf("credentials blob missing claudeAiOauth envelope: %s", mat.CredentialsJSON)
	}

	// Account blob: {"oauthAccount": {"accountId": <id>, "label": <label>}}.
	var ident struct {
		OauthAccount struct {
			AccountID string `json:"accountId"`
			Label     string `json:"label"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(mat.AccountJSON, &ident); err != nil {
		t.Fatalf("unmarshal account: %v", err)
	}
	if ident.OauthAccount.AccountID != acct.ID {
		t.Errorf("accountId: got %q, want %q", ident.OauthAccount.AccountID, acct.ID)
	}
	if ident.OauthAccount.Label != "claude.ai" {
		t.Errorf("label: got %q, want claude.ai", ident.OauthAccount.Label)
	}
}

// TestProvisionMaterialRejectsConsole: a console (API-key) account has no OAuth
// credential to provision — ProvisionMaterial fails closed with a zero Material.
func TestProvisionMaterialRejectsConsole(t *testing.T) {
	s := NewFileStore(t.TempDir())
	acct := NewAccount("work console", AccountConsole)
	if err := s.Add(acct, []byte("sk-ant-api-CONSOLE-KEY")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	mat, err := ProvisionMaterial(s, acct.ID)
	if !errors.Is(err, ErrNotSubscriptionAccount) {
		t.Fatalf("ProvisionMaterial(console) err = %v; want ErrNotSubscriptionAccount", err)
	}
	if mat.CredentialsJSON != nil || mat.AccountJSON != nil {
		t.Error("Material must be zero on error (no partial credential material)")
	}
}

// TestProvisionMaterialUnknownAccount: an id absent from the store is a hard
// error, never a zero-credential success.
func TestProvisionMaterialUnknownAccount(t *testing.T) {
	s := NewFileStore(t.TempDir())
	if _, err := ProvisionMaterial(s, "acct-missing"); !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("ProvisionMaterial(missing) err = %v; want ErrUnknownAccount", err)
	}
}

// TestProvisionMaterialNoTokenLeak: errors never echo the secret bytes.
func TestProvisionMaterialNoTokenLeak(t *testing.T) {
	s := NewFileStore(t.TempDir())
	acct := NewAccount("work console", AccountConsole)
	const secret = "sk-ant-api-DO-NOT-LEAK"
	if err := s.Add(acct, []byte(secret)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_, err := ProvisionMaterial(s, acct.ID)
	if err == nil {
		t.Fatal("expected an error for a console account")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error message leaked the secret: %v", err)
	}
}

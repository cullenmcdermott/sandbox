package client_test

// account_test.go exercises the account→CreateOptions SDK surface exactly as a
// library consumer would (external test package): stored-account resolution is
// fail closed, and the legacy no-account path never mutates the options.

import (
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/client/cred"
)

func TestUseAnthropicAccount(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	sub := cred.NewAccount("claude.ai", cred.AccountSubscription)
	if err := store.Add(sub, []byte("sk-ant-oat-SUBSCRIPTION-TOKEN")); err != nil {
		t.Fatalf("add sub: %v", err)
	}
	con := cred.NewAccount("work", cred.AccountConsole)
	if err := store.Add(con, []byte("sk-ant-api-CONSOLE-KEY")); err != nil {
		t.Fatalf("add console: %v", err)
	}

	t.Run("subscription maps to oauth", func(t *testing.T) {
		var opts client.CreateOptions
		if err := opts.UseAnthropicAccount(store, sub.ID); err != nil {
			t.Fatalf("UseAnthropicAccount: %v", err)
		}
		if opts.AnthropicAccountID != sub.ID {
			t.Errorf("account id: got %q, want %q", opts.AnthropicAccountID, sub.ID)
		}
		if opts.AnthropicAuth != "oauth" {
			t.Errorf("auth: got %q, want oauth", opts.AnthropicAuth)
		}
		if string(opts.AnthropicCredential) != "sk-ant-oat-SUBSCRIPTION-TOKEN" {
			t.Errorf("credential bytes mismatch")
		}
	})

	t.Run("console maps to api-key", func(t *testing.T) {
		var opts client.CreateOptions
		if err := opts.UseAnthropicAccount(store, con.ID); err != nil {
			t.Fatalf("UseAnthropicAccount: %v", err)
		}
		if opts.AnthropicAuth != "api-key" {
			t.Errorf("auth: got %q, want api-key", opts.AnthropicAuth)
		}
	})

	t.Run("unknown id errors with the sentinel", func(t *testing.T) {
		var opts client.CreateOptions
		if err := opts.UseAnthropicAccount(store, "acct-doesnotexist"); !errors.Is(err, cred.ErrUnknownAccount) {
			t.Fatalf("want ErrUnknownAccount, got: %v", err)
		}
		if opts.AnthropicAccountID != "" {
			t.Error("opts must not be mutated on failure")
		}
	})
}

func TestSelectAnthropicAccount(t *testing.T) {
	t.Run("no accounts is legacy no-op", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		var opts client.CreateOptions
		if err := opts.SelectAnthropicAccount(store, ""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.AnthropicAccountID != "" || opts.AnthropicCredential != nil || opts.AnthropicAuth != "" {
			t.Error("no-account path must leave account fields untouched (legacy shared-Secret)")
		}
	})

	t.Run("accounts but no default is the sentinel", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		a := cred.NewAccount("claude.ai", cred.AccountSubscription)
		if err := store.Add(a, []byte("sk-ant-oat-TOKEN")); err != nil {
			t.Fatalf("add: %v", err)
		}
		b := cred.NewAccount("work", cred.AccountConsole)
		if err := store.Add(b, []byte("sk-ant-api-KEY")); err != nil {
			t.Fatalf("add: %v", err)
		}
		// Two accounts, neither is the auto-default (Add does not set it).
		var opts client.CreateOptions
		err := opts.SelectAnthropicAccount(store, "")
		if !errors.Is(err, client.ErrNoDefaultAnthropicAccount) {
			t.Errorf("want ErrNoDefaultAnthropicAccount, got: %v", err)
		}
	})

	t.Run("sole account is used without a default", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		a := cred.NewAccount("claude.ai", cred.AccountSubscription)
		if err := store.Add(a, []byte("sk-ant-oat-TOKEN")); err != nil {
			t.Fatalf("add: %v", err)
		}
		// One account, DefaultID never set: unambiguous, so it is selected.
		var opts client.CreateOptions
		if err := opts.SelectAnthropicAccount(store, ""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.AnthropicAccountID != a.ID {
			t.Errorf("account id: got %q, want %q", opts.AnthropicAccountID, a.ID)
		}
	})

	t.Run("no selector uses the default account", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		a := cred.NewAccount("claude.ai", cred.AccountSubscription)
		if err := store.Add(a, []byte("sk-ant-oat-TOKEN")); err != nil {
			t.Fatalf("add: %v", err)
		}
		if err := store.SetDefault(a.ID); err != nil {
			t.Fatalf("set default: %v", err)
		}
		var opts client.CreateOptions
		if err := opts.SelectAnthropicAccount(store, ""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.AnthropicAccountID != a.ID {
			t.Errorf("account id: got %q, want %q", opts.AnthropicAccountID, a.ID)
		}
	})

	t.Run("explicit selector resolves and never falls back", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		a := cred.NewAccount("claude.ai", cred.AccountSubscription)
		if err := store.Add(a, []byte("sk-ant-oat-TOKEN")); err != nil {
			t.Fatalf("add: %v", err)
		}
		var opts client.CreateOptions
		if err := opts.SelectAnthropicAccount(store, "claude.ai"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.AnthropicAccountID != a.ID {
			t.Errorf("account id: got %q, want %q", opts.AnthropicAccountID, a.ID)
		}

		// A requested-but-missing selector is a hard error, not a fallback.
		var opts2 client.CreateOptions
		if err := opts2.SelectAnthropicAccount(store, "ghost"); err == nil {
			t.Fatal("expected a hard error for an unresolvable selector")
		} else if opts2.AnthropicAccountID != "" {
			t.Error("opts must not be mutated when the selector cannot be resolved")
		}
	})
}

// TestUseClaudePaneMaterial: the claude-pane material setter is fail-closed —
// missing documents reject with the sentinel, and a present-but-partial
// credential (no refresh token: the setup-token shape interactive claude
// rejects at runtime) is caught here as cred.ErrNoFullCredential instead of
// becoming a session that boots to a "Not logged in" wall.
func TestUseClaudePaneMaterial(t *testing.T) {
	const fullCreds = `{"claudeAiOauth":{"accessToken":"at","refreshToken":"rt"}}`
	t.Run("full material is set", func(t *testing.T) {
		var opts client.CreateOptions
		m := cred.Material{CredentialsJSON: []byte(fullCreds), AccountJSON: []byte(`{"oauthAccount":{}}`)}
		if err := opts.UseClaudePaneMaterial(m); err != nil {
			t.Fatalf("UseClaudePaneMaterial: %v", err)
		}
		if string(opts.ClaudeCredentialsJSON) != fullCreds || string(opts.ClaudeOAuthAccountJSON) != `{"oauthAccount":{}}` {
			t.Error("material not set on options")
		}
	})
	t.Run("missing material is the sentinel", func(t *testing.T) {
		var opts client.CreateOptions
		err := opts.UseClaudePaneMaterial(cred.Material{CredentialsJSON: []byte(`{}`)})
		if !errors.Is(err, client.ErrClaudePaneCredentialMissing) {
			t.Fatalf("want ErrClaudePaneCredentialMissing, got %v", err)
		}
		if opts.ClaudeCredentialsJSON != nil {
			t.Error("opts must not be mutated on failure")
		}
	})
	t.Run("partial credential (setup-token shape) is rejected", func(t *testing.T) {
		var opts client.CreateOptions
		m := cred.Material{
			CredentialsJSON: []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat-TOKEN"}}`),
			AccountJSON:     []byte(`{"oauthAccount":{}}`),
		}
		err := opts.UseClaudePaneMaterial(m)
		if !errors.Is(err, cred.ErrNoFullCredential) {
			t.Fatalf("want cred.ErrNoFullCredential, got %v", err)
		}
		if opts.ClaudeCredentialsJSON != nil {
			t.Error("opts must not be mutated on failure")
		}
	})
}

// TestSelectClaudePaneMaterial covers the store-account source: today the store
// holds setup tokens, which interactive claude rejects at runtime (the "Not
// logged in" wall, verified live 2026-07-20), so an explicit selector is a
// HARD create-time error with remediation — never a broken session, never a
// fallback. When the store learns full OAuth documents the same path starts
// working without further changes. (The empty-selector path reads the host's
// real Claude Code login — cred's own systemMaterial tests cover it.)
func TestSelectClaudePaneMaterial(t *testing.T) {
	t.Run("store setup token is a hard error with remediation", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		a := cred.NewAccount("claude.ai", cred.AccountSubscription)
		if err := store.Add(a, []byte("sk-ant-oat-TOKEN")); err != nil {
			t.Fatalf("add: %v", err)
		}
		var opts client.CreateOptions
		err := opts.SelectClaudePaneMaterial(store, "claude.ai")
		if !errors.Is(err, cred.ErrNoFullCredential) {
			t.Fatalf("want cred.ErrNoFullCredential, got %v", err)
		}
		if !strings.Contains(err.Error(), "setup token") || !strings.Contains(err.Error(), "log in with `claude`") {
			t.Errorf("error lacks human remediation: %v", err)
		}
		if opts.ClaudeCredentialsJSON != nil || opts.AnthropicAccountID != "" {
			t.Error("opts must not be mutated on failure")
		}
		// The claude-sdk token fields must stay untouched — a pane session
		// never provisions the inference-scoped env-token path.
		if opts.AnthropicCredential != nil || opts.AnthropicAuth != "" {
			t.Error("pane material selection must not set the claude-sdk token fields")
		}
	})

	t.Run("unresolvable selector is a hard error", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		var opts client.CreateOptions
		if err := opts.SelectClaudePaneMaterial(store, "ghost"); err == nil {
			t.Fatal("expected a hard error for an unresolvable selector")
		}
		if opts.ClaudeCredentialsJSON != nil || opts.AnthropicAccountID != "" {
			t.Error("opts must not be mutated on failure")
		}
	})

	t.Run("console account is rejected (no OAuth credential)", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		a := cred.NewAccount("work", cred.AccountConsole)
		if err := store.Add(a, []byte("sk-ant-api-KEY")); err != nil {
			t.Fatalf("add: %v", err)
		}
		var opts client.CreateOptions
		if err := opts.SelectClaudePaneMaterial(store, "work"); !errors.Is(err, cred.ErrNotSubscriptionAccount) {
			t.Fatalf("want ErrNotSubscriptionAccount, got %v", err)
		}
	})
}

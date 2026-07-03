package client_test

// account_test.go exercises the account→CreateOptions SDK surface exactly as a
// library consumer would (external test package): stored-account resolution is
// fail closed, and the legacy no-account path never mutates the options.

import (
	"errors"
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

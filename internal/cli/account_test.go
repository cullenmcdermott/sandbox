package cli

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/cred"
)

func mkAccount(id, label string, typ cred.AccountType) cred.Account {
	return cred.Account{ID: id, Label: label, Type: typ}
}

func TestResolveAccount(t *testing.T) {
	accounts := []cred.Account{
		mkAccount("acct-1111", "claude.ai", cred.AccountSubscription),
		mkAccount("acct-2222", "work", cred.AccountConsole),
		mkAccount("acct-3333", "work", cred.AccountConsole), // duplicate label
	}

	t.Run("exact id wins", func(t *testing.T) {
		got, err := resolveAccount(accounts, "acct-2222")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "acct-2222" {
			t.Errorf("got %q, want acct-2222", got.ID)
		}
	})

	t.Run("unique label", func(t *testing.T) {
		got, err := resolveAccount(accounts, "claude.ai")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "acct-1111" {
			t.Errorf("got %q, want acct-1111", got.ID)
		}
	})

	t.Run("ambiguous label", func(t *testing.T) {
		_, err := resolveAccount(accounts, "work")
		if err == nil {
			t.Fatal("expected an ambiguity error")
		}
		if !strings.Contains(err.Error(), "ambiguous") ||
			!strings.Contains(err.Error(), "acct-2222") ||
			!strings.Contains(err.Error(), "acct-3333") {
			t.Errorf("ambiguity error should list both matches, got: %v", err)
		}
	})

	t.Run("no match lists available", func(t *testing.T) {
		_, err := resolveAccount(accounts, "nope")
		if err == nil {
			t.Fatal("expected a no-match error")
		}
		if !strings.Contains(err.Error(), "acct-1111") {
			t.Errorf("no-match error should list available accounts, got: %v", err)
		}
	})

	t.Run("no accounts stored", func(t *testing.T) {
		_, err := resolveAccount(nil, "anything")
		if err == nil {
			t.Fatal("expected an error with no accounts stored")
		}
		if !strings.Contains(err.Error(), "auth login") {
			t.Errorf("empty-store error should guide to `auth login`, got: %v", err)
		}
	})
}

func TestSetAnthropicAccount(t *testing.T) {
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
		if err := setAnthropicAccount(store, sub.ID, &opts); err != nil {
			t.Fatalf("setAnthropicAccount: %v", err)
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
		if err := setAnthropicAccount(store, con.ID, &opts); err != nil {
			t.Fatalf("setAnthropicAccount: %v", err)
		}
		if opts.AnthropicAuth != "api-key" {
			t.Errorf("auth: got %q, want api-key", opts.AnthropicAuth)
		}
	})

	t.Run("unknown id errors", func(t *testing.T) {
		var opts client.CreateOptions
		if err := setAnthropicAccount(store, "acct-doesnotexist", &opts); err == nil {
			t.Fatal("expected an error for an unknown account id")
		}
		if opts.AnthropicAccountID != "" {
			t.Error("opts must not be mutated on failure")
		}
	})
}

func TestApplyAccountSelection(t *testing.T) {
	t.Run("no accounts is legacy no-op", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		var opts client.CreateOptions
		if err := applyAccountSelection(store, "", &opts); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.AnthropicAccountID != "" || opts.AnthropicCredential != nil || opts.AnthropicAuth != "" {
			t.Error("no-account path must leave account fields untouched (legacy shared-Secret)")
		}
	})

	t.Run("accounts but no default errors with guidance", func(t *testing.T) {
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
		err := applyAccountSelection(store, "", &opts)
		if err == nil {
			t.Fatal("expected an error when accounts exist but no default is set")
		}
		if !strings.Contains(err.Error(), "auth default") {
			t.Errorf("error should guide to `auth default`, got: %v", err)
		}
	})

	t.Run("no flag uses the default account", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		a := cred.NewAccount("claude.ai", cred.AccountSubscription)
		if err := store.Add(a, []byte("sk-ant-oat-TOKEN")); err != nil {
			t.Fatalf("add: %v", err)
		}
		if err := store.SetDefault(a.ID); err != nil {
			t.Fatalf("set default: %v", err)
		}
		var opts client.CreateOptions
		if err := applyAccountSelection(store, "", &opts); err != nil {
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
		if err := applyAccountSelection(store, "claude.ai", &opts); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.AnthropicAccountID != a.ID {
			t.Errorf("account id: got %q, want %q", opts.AnthropicAccountID, a.ID)
		}

		// A requested-but-missing selector is a hard error, not a fallback.
		var opts2 client.CreateOptions
		if err := applyAccountSelection(store, "ghost", &opts2); err == nil {
			t.Fatal("expected a hard error for an unresolvable --account selector")
		} else if opts2.AnthropicAccountID != "" {
			t.Error("opts must not be mutated when the selector cannot be resolved")
		}
	})
}

package cli

// account_test.go — the account-selection LOGIC is SDK-owned and tested there
// (client/account_test.go, client/cred/resolve_test.go). These tests cover only
// what this layer adds: decorating the SDK sentinel errors with `sandbox`
// command hints.

import (
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/client/cred"
)

func TestResolveAccountAddsLoginHint(t *testing.T) {
	_, err := resolveAccount(nil, "anything")
	if err == nil {
		t.Fatal("expected an error with no accounts stored")
	}
	if !strings.Contains(err.Error(), "sandbox auth login") {
		t.Errorf("empty-store error should guide to `sandbox auth login`, got: %v", err)
	}
}

func TestApplyAccountSelectionAddsDefaultHint(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	for _, a := range []cred.Account{
		cred.NewAccount("claude.ai", cred.AccountSubscription),
		cred.NewAccount("work", cred.AccountConsole),
	} {
		if err := store.Add(a, []byte("sk-ant-oat-TOKEN")); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	// Two accounts, no default: the SDK sentinel must carry the CLI hint.
	var opts client.CreateOptions
	err := applyAccountSelection(store, "", &opts)
	if err == nil {
		t.Fatal("expected an error when accounts exist but no default is set")
	}
	if !strings.Contains(err.Error(), "sandbox auth default") || !strings.Contains(err.Error(), "--account") {
		t.Errorf("error should guide to --account / `sandbox auth default`, got: %v", err)
	}
}

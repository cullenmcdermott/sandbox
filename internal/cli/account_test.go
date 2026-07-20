package cli

// account_test.go — the account-selection LOGIC is SDK-owned and tested there
// (client/account_test.go, client/cred/resolve_test.go). These tests cover only
// what this layer adds: decorating the SDK sentinel errors with `sandbox`
// command hints.

import (
	"strings"
	"testing"
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

package cred

import (
	"errors"
	"strings"
	"testing"
)

func mkAccount(id, label string, typ AccountType) Account {
	return Account{ID: id, Label: label, Type: typ}
}

func TestResolve(t *testing.T) {
	accounts := []Account{
		mkAccount("acct-1111", "claude.ai", AccountSubscription),
		mkAccount("acct-2222", "work", AccountConsole),
		mkAccount("acct-3333", "work", AccountConsole), // duplicate label
	}

	t.Run("exact id wins", func(t *testing.T) {
		got, err := Resolve(accounts, "acct-2222")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "acct-2222" {
			t.Errorf("got %q, want acct-2222", got.ID)
		}
	})

	t.Run("unique label", func(t *testing.T) {
		got, err := Resolve(accounts, "claude.ai")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "acct-1111" {
			t.Errorf("got %q, want acct-1111", got.ID)
		}
	})

	t.Run("ambiguous label", func(t *testing.T) {
		_, err := Resolve(accounts, "work")
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
		_, err := Resolve(accounts, "nope")
		if err == nil {
			t.Fatal("expected a no-match error")
		}
		if !strings.Contains(err.Error(), "acct-1111") {
			t.Errorf("no-match error should list available accounts, got: %v", err)
		}
	})

	t.Run("no accounts stored is the sentinel", func(t *testing.T) {
		_, err := Resolve(nil, "anything")
		if !errors.Is(err, ErrNoAccounts) {
			t.Errorf("want ErrNoAccounts, got: %v", err)
		}
	})
}

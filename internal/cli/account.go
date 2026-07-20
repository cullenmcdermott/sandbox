package cli

import (
	"errors"
	"fmt"

	"github.com/cullenmcdermott/sandbox/client/cred"
)

// account.go — thin CLI shims over the public SDK's account API (client/cred).
// The logic lives in the SDK so library consumers get identical semantics; this
// file only opens the store and decorates SDK sentinel errors with
// `sandbox`-command remediation hints.

// newCredStore builds the platform-appropriate multi-account credential store
// (macOS Keychain, else per-account files). It is the single entry point the
// `auth` subcommands, `claude --account`, and the TUI account picker use so
// every surface shares one store and they never diverge.
func newCredStore() (cred.Store, error) {
	store, err := cred.DefaultStore()
	if err != nil {
		return nil, fmt.Errorf("open credential store: %w", err)
	}
	return store, nil
}

// resolveAccount wraps cred.Resolve, adding the CLI's `auth login` hint when no
// accounts are stored at all.
func resolveAccount(accounts []cred.Account, selector string) (cred.Account, error) {
	acct, err := cred.Resolve(accounts, selector)
	if errors.Is(err, cred.ErrNoAccounts) {
		return cred.Account{}, fmt.Errorf("%w; run `sandbox auth login` first", err)
	}
	return acct, err
}

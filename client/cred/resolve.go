package cred

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNoAccounts is returned by Resolve when no accounts are stored at all.
// Callers can errors.Is on it to add their own remediation hint (the sandbox
// CLI points at `sandbox auth login`).
var ErrNoAccounts = errors.New("cred: no anthropic accounts stored")

// Resolve resolves a user-supplied selector (an account id or display label,
// e.g. from a --account flag or a picker) to a single Account from accounts.
// Resolution is an exact ID match first, then a UNIQUE label match: labels are
// not unique keys, so the id pass always lets a caller disambiguate. An
// ambiguous label (shared by more than one account) is an error listing the
// matching ids; no match is an error listing the available accounts. It reads
// only metadata, never secret bytes.
func Resolve(accounts []Account, selector string) (Account, error) {
	for _, a := range accounts {
		if a.ID == selector {
			return a, nil
		}
	}
	var matches []Account
	for _, a := range accounts {
		if a.Label == selector {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		if len(accounts) == 0 {
			return Account{}, ErrNoAccounts
		}
		return Account{}, fmt.Errorf("no anthropic account matches %q; available: %s", selector, accountIDList(accounts))
	default:
		return Account{}, fmt.Errorf("account label %q is ambiguous; matches %s — select by id", selector, accountIDList(matches))
	}
}

// accountIDList renders "id (label), id (label)" for error messages. It never
// touches secret bytes.
func accountIDList(accounts []Account) string {
	parts := make([]string, 0, len(accounts))
	for _, a := range accounts {
		parts = append(parts, fmt.Sprintf("%s (%s)", a.ID, a.Label))
	}
	return strings.Join(parts, ", ")
}

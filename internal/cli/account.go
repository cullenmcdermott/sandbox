package cli

import (
	"fmt"
	"strings"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/internal/cred"
)

// newCredStore builds the platform-appropriate multi-account credential store
// (macOS Keychain, else per-account files). It is the single entry point the
// `auth` subcommands and `claude --account` use so the CLI and TUI share one
// store and never diverge.
func newCredStore() (cred.Store, error) {
	store, err := cred.DefaultStore()
	if err != nil {
		return nil, fmt.Errorf("open credential store: %w", err)
	}
	return store, nil
}

// resolveAccount resolves a user-supplied selector (from `--account`,
// `auth logout <sel>`, or `auth default <sel>`) to a single stored Account
// against the already-listed accounts. Resolution is an exact ID match first,
// then a UNIQUE label match: labels are not unique keys, so the id pass always
// lets a user disambiguate. An ambiguous label (shared by more than one
// account) is an error listing the matching ids; no match is an error listing
// the available accounts. It reads only metadata, never secret bytes.
func resolveAccount(accounts []cred.Account, selector string) (cred.Account, error) {
	for _, a := range accounts {
		if a.ID == selector {
			return a, nil
		}
	}
	var matches []cred.Account
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
			return cred.Account{}, fmt.Errorf("no anthropic accounts stored; run `sandbox auth login` first")
		}
		return cred.Account{}, fmt.Errorf("no anthropic account matches %q; available: %s", selector, accountIDList(accounts))
	default:
		return cred.Account{}, fmt.Errorf("account label %q is ambiguous; matches %s — select by id", selector, accountIDList(matches))
	}
}

// accountIDList renders "id (label), id (label)" for error messages. It never
// touches secret bytes.
func accountIDList(accounts []cred.Account) string {
	parts := make([]string, 0, len(accounts))
	for _, a := range accounts {
		parts = append(parts, fmt.Sprintf("%s (%s)", a.ID, a.Label))
	}
	return strings.Join(parts, ", ")
}

// setAnthropicAccount resolves the stored account named by accountID (an exact
// id — resolveAccount's output, or a TUI picker selection) into the fail-closed
// Anthropic credential fields on opts: the account id, its secret bytes (read
// from the store), and the AnthropicAuth spelling for the account's type (via
// cred.AuthForType). Because it reads the secret, ANY failure — unknown id,
// unknown type, denied Keychain read, empty bytes — is a HARD error: once an
// account was requested the caller MUST NOT fall back to the shared cluster
// Secret (a wrong-account session, personal vs work billing/data, is a worse
// failure than a refused launch).
//
// This is the single account→CreateOptions helper Stage 4's dashboard Creator
// reuses to thread a picked account id onto CreateOptions, keeping Keychain
// access in the CLI layer and out of the dashboard.
func setAnthropicAccount(store cred.Store, accountID string, opts *client.CreateOptions) error {
	accounts, err := store.List()
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}
	var acct cred.Account
	found := false
	for _, a := range accounts {
		if a.ID == accountID {
			acct, found = a, true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown anthropic account id %q", accountID)
	}
	auth, err := cred.AuthForType(acct.Type)
	if err != nil {
		return fmt.Errorf("account %q: %w", accountID, err)
	}
	secret, err := store.Secret(accountID)
	if err != nil {
		return fmt.Errorf("read credential for account %q: %w", accountID, err)
	}
	if len(secret) == 0 {
		// Defensive: an empty read is treated as a failure, not a silent
		// fall-through to the shared Secret.
		return fmt.Errorf("read credential for account %q: %w", accountID, cred.ErrNotFound)
	}
	opts.AnthropicAccountID = accountID
	opts.AnthropicCredential = secret
	opts.AnthropicAuth = auth
	return nil
}

// applyAccountSelection applies the CLI's `--account` resolution semantics to
// opts for a claude-sdk session. selector is the raw `--account` flag value
// (may be ""):
//
//   - selector != "": resolve id|label → account, then setAnthropicAccount.
//     Any failure is a hard error — it never falls back to the shared Secret.
//   - selector == "" with accounts stored: use the default account. If accounts
//     exist but no default is set, it errors with guidance rather than guessing.
//   - selector == "" with no accounts stored: the legacy shared-Secret path —
//     opts is left untouched (backward compatible).
//
// Callers apply this only for the claude backend; opencode has no account step.
func applyAccountSelection(store cred.Store, selector string, opts *client.CreateOptions) error {
	if selector != "" {
		accounts, err := store.List()
		if err != nil {
			return fmt.Errorf("list accounts: %w", err)
		}
		acct, err := resolveAccount(accounts, selector)
		if err != nil {
			return err
		}
		return setAnthropicAccount(store, acct.ID, opts)
	}
	accounts, err := store.List()
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}
	if len(accounts) == 0 {
		// Legacy shared-Secret path: no account fields set.
		return nil
	}
	def, err := store.Default()
	if err != nil {
		return fmt.Errorf("read default account: %w", err)
	}
	if def == "" {
		return fmt.Errorf("multiple anthropic accounts stored but no default set; pass --account <id|label> or set one with `sandbox auth default <id>`")
	}
	return setAnthropicAccount(store, def, opts)
}

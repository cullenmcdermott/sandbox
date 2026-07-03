package client

import (
	"errors"
	"fmt"

	"github.com/cullenmcdermott/sandbox/client/cred"
)

// account.go — threading a stored Anthropic account (see client/cred) into
// session creation. These helpers are the single account→CreateOptions path:
// the sandbox CLI (`claude --account`), the TUI account picker, and SDK
// consumers all resolve accounts through them, so the fail-closed semantics
// cannot diverge between entry points.

// ErrNoDefaultAnthropicAccount is returned by SelectAnthropicAccount when two
// or more accounts are stored, none is marked default, and no selector was
// given (a sole stored account is used without needing a default). Callers can
// errors.Is on it to add their own remediation hint (the sandbox CLI points at
// `--account` / `sandbox auth default`).
var ErrNoDefaultAnthropicAccount = errors.New("sandbox: multiple anthropic accounts stored but no default set")

// UseAnthropicAccount resolves the stored account named by accountID (an exact
// id — cred.Resolve's output, or a picker selection) into the fail-closed
// Anthropic credential fields on o. An unknown id wraps cred.ErrUnknownAccount.
// Because it reads the secret, ANY failure — unknown id, unknown type, denied
// Keychain read, empty bytes — is a HARD error that leaves o untouched: once an
// account was requested the caller MUST NOT fall back to the shared cluster
// Secret (a wrong-account session, personal vs work billing/data, is a worse
// failure than a refused launch).
func (o *CreateOptions) UseAnthropicAccount(store cred.Store, accountID string) error {
	accounts, err := store.List()
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}
	for _, a := range accounts {
		if a.ID == accountID {
			return o.useAccount(store, a)
		}
	}
	return fmt.Errorf("%w: %q", cred.ErrUnknownAccount, accountID)
}

// useAccount loads acct's secret and sets the three credential fields on o:
// the account id, its secret bytes, and the AnthropicAuth spelling for the
// account's type (via cred.AuthForType). o is mutated only after every check
// passes — all failures leave it untouched (fail closed).
func (o *CreateOptions) useAccount(store cred.Store, acct cred.Account) error {
	auth, err := cred.AuthForType(acct.Type)
	if err != nil {
		return fmt.Errorf("account %q: %w", acct.ID, err)
	}
	secret, err := store.Secret(acct.ID)
	if err != nil {
		return fmt.Errorf("read credential for account %q: %w", acct.ID, err)
	}
	if len(secret) == 0 {
		// Defensive: an empty read is treated as a failure, not a silent
		// fall-through to the shared Secret.
		return fmt.Errorf("read credential for account %q: %w", acct.ID, cred.ErrNotFound)
	}
	o.AnthropicAccountID = acct.ID
	o.AnthropicCredential = secret
	o.AnthropicAuth = auth
	return nil
}

// SelectAnthropicAccount applies account-selection semantics to o for a
// claude-sdk session. selector is a raw user-supplied value (may be ""):
//
//   - selector != "": resolve id|label → account (cred.Resolve), then load its
//     credential. Any failure is a hard error — it never falls back to the
//     shared Secret.
//   - selector == "" with accounts stored: use the default account; a sole
//     stored account is used even without a default (mirrors the login flows'
//     auto-default). Two or more accounts with no default is
//     ErrNoDefaultAnthropicAccount rather than guessing.
//   - selector == "" with no accounts stored: the legacy shared-Secret path —
//     o is left untouched (backward compatible).
//
// Callers apply this only for the claude backend; opencode has no account step.
func (o *CreateOptions) SelectAnthropicAccount(store cred.Store, selector string) error {
	accounts, err := store.List()
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}
	if selector != "" {
		acct, err := cred.Resolve(accounts, selector)
		if err != nil {
			return err
		}
		return o.useAccount(store, acct)
	}
	if len(accounts) == 0 {
		// Legacy shared-Secret path: no account fields set.
		return nil
	}
	if len(accounts) == 1 {
		return o.useAccount(store, accounts[0])
	}
	def, err := store.Default()
	if err != nil {
		return fmt.Errorf("read default account: %w", err)
	}
	if def == "" {
		return ErrNoDefaultAnthropicAccount
	}
	for _, a := range accounts {
		if a.ID == def {
			return o.useAccount(store, a)
		}
	}
	// Manifest drift: DefaultID names an account that is no longer stored.
	return fmt.Errorf("default account: %w: %q", cred.ErrUnknownAccount, def)
}

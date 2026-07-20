package cred

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNotSubscriptionAccount is returned by ProvisionMaterial for a non-subscription
// (console / API-key) account: a claude-pane session runs the interactive Claude
// client against a subscription OAuth credential, and a console API key has no
// claudeAiOauth credential to provision. Callers can errors.Is on it.
var ErrNotSubscriptionAccount = errors.New("cred: account is not a subscription account (no OAuth credential to provision)")

// Material is the per-session credential + identity a claude-pane session needs,
// each as a JSON document the k8s backend writes verbatim into the per-session
// Secret (never an inline env value). CredentialsJSON is the FULL Claude Code
// OAuth credential ({"claudeAiOauth": {...}}, the shape of
// ~/.claude/.credentials.json); AccountJSON is the account identity
// ({"oauthAccount": {...}}). Both carry token / identity material — callers must
// never log or print either blob, mirroring the package's secret-bytes invariant.
type Material struct {
	CredentialsJSON []byte
	AccountJSON     []byte
}

// claudeAiOauth mirrors the "claudeAiOauth" object inside Claude Code's
// .credentials.json. Every field beyond AccessToken is omitempty and populated
// only from what the store holds — the current store holds a single setup-token
// secret, so only AccessToken is emitted (preserving whatever fields the store
// holds). The field names are Claude Code's contract; do not rename them.
type claudeAiOauth struct {
	AccessToken           string   `json:"accessToken"`
	RefreshToken          string   `json:"refreshToken,omitempty"`
	ExpiresAt             int64    `json:"expiresAt,omitempty"`
	RefreshTokenExpiresAt int64    `json:"refreshTokenExpiresAt,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	SubscriptionType      string   `json:"subscriptionType,omitempty"`
	RateLimitTier         string   `json:"rateLimitTier,omitempty"`
}

// credentialsDoc is the top-level {"claudeAiOauth": {...}} envelope written to
// .credentials.json.
type credentialsDoc struct {
	ClaudeAiOauth claudeAiOauth `json:"claudeAiOauth"`
}

// oauthAccount carries the account identity the store actually holds. The local
// store does NOT hold Anthropic's server-side account uuid / email / organization
// (those are not stored by the login flow), so this preserves the store's own
// identity: the DNS-safe local account id and the human-chosen label. When the
// login/storage side later records the real oauthAccount fields, add them here.
type oauthAccount struct {
	AccountID string `json:"accountId"`
	Label     string `json:"label,omitempty"`
}

// accountDoc is the top-level {"oauthAccount": {...}} envelope.
type accountDoc struct {
	OauthAccount oauthAccount `json:"oauthAccount"`
}

// ProvisionMaterial resolves the stored subscription account named by accountID
// into the credential + identity JSON a claude-pane session's per-session Secret
// carries. It is a package-level function (not a Store method) so the Store
// interface stays implementable by external SDK consumers.
//
// DEGRADED-MODE SOURCE: the store holds only per-account setup tokens, so the
// emitted credential carries an accessToken alone — enough for inference but
// not for the interactive pane's subscription (Max) mode or in-pod refresh.
// The Max-mode source is SystemMaterial (the host's own Claude Code login);
// claude-pane session creation prefers it and falls back here only for
// explicitly picked non-default accounts, until the store learns to hold full
// OAuth documents per account.
//
// It reads the account's secret and wraps it into the Claude Code
// .credentials.json shape ({"claudeAiOauth": {"accessToken": <secret>}}). The
// current store holds only the setup-token secret, so only accessToken is
// populated — the other claudeAiOauth fields are omitted (preserving whatever
// fields the store holds). The account identity ({"oauthAccount": {...}}) carries
// the store's local account id and label.
//
// Fail-closed like the rest of the credential path: an unknown id, a non-
// subscription account, a read error, or empty bytes is a HARD error and returns
// a zero Material. Errors never echo the secret bytes.
func ProvisionMaterial(store Store, accountID string) (Material, error) {
	accounts, err := store.List()
	if err != nil {
		return Material{}, fmt.Errorf("list accounts: %w", err)
	}
	var acct Account
	found := false
	for _, a := range accounts {
		if a.ID == accountID {
			acct = a
			found = true
			break
		}
	}
	if !found {
		return Material{}, fmt.Errorf("%w: %q", ErrUnknownAccount, accountID)
	}
	if acct.Type != AccountSubscription {
		return Material{}, fmt.Errorf("account %q: %w", acct.ID, ErrNotSubscriptionAccount)
	}
	secret, err := store.Secret(acct.ID)
	if err != nil {
		return Material{}, fmt.Errorf("read credential for account %q: %w", acct.ID, err)
	}
	if len(secret) == 0 {
		return Material{}, fmt.Errorf("read credential for account %q: %w", acct.ID, ErrNotFound)
	}

	credsJSON, err := json.Marshal(credentialsDoc{
		ClaudeAiOauth: claudeAiOauth{AccessToken: string(secret)},
	})
	if err != nil {
		// Marshal of a plain struct cannot realistically fail; keep the secret out
		// of the error either way.
		return Material{}, fmt.Errorf("marshal credentials for account %q: %w", acct.ID, err)
	}
	acctJSON, err := json.Marshal(accountDoc{
		OauthAccount: oauthAccount{AccountID: acct.ID, Label: acct.Label},
	})
	if err != nil {
		return Material{}, fmt.Errorf("marshal account identity for %q: %w", acct.ID, err)
	}
	return Material{CredentialsJSON: credsJSON, AccountJSON: acctJSON}, nil
}

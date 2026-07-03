package cred

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// AccountType classifies a stored Anthropic account by how it authenticates.
type AccountType string

const (
	// AccountSubscription is a claude.ai subscription, authenticated with a
	// setup-token OAuth credential (from `claude setup-token`). Maps to the
	// Spec.AnthropicAuth spelling "oauth" (CLAUDE_CODE_OAUTH_TOKEN).
	AccountSubscription AccountType = "subscription"
	// AccountConsole is an Anthropic Console account, authenticated with a
	// long-lived API key. Maps to the Spec.AnthropicAuth spelling "api-key"
	// (ANTHROPIC_API_KEY).
	AccountConsole AccountType = "console"
)

// manifestName is the account-metadata file under the store root. It holds only
// enumerable metadata and a single DefaultID — never secret bytes.
const manifestName = "anthropic-accounts.json"

// Sentinel errors. None of these ever embed secret material.
var (
	// ErrNotFound is returned when an account's secret is not stored.
	ErrNotFound = errors.New("cred: account secret not found")
	// ErrAccountExists is returned by Add when the account ID already exists.
	ErrAccountExists = errors.New("cred: account already exists")
	// ErrUnknownAccount is returned when an operation targets an ID that is not
	// present in the manifest (e.g. SetDefault to a missing id).
	ErrUnknownAccount = errors.New("cred: unknown account id")
	// ErrInvalidAccountID is returned when an account ID is not a DNS-label-safe
	// value. IDs are later used as Kubernetes label values.
	ErrInvalidAccountID = errors.New("cred: invalid account id (must be a DNS label: [a-z0-9-], <=63 chars)")
	// ErrInvalidAccountType is returned when an AccountType is not one of the
	// known values (subscription | console). Unknown types must never persist:
	// the type selects which credential env var the k8s layer wires up, so a
	// silently-stored bogus type would misroute the credential kind.
	ErrInvalidAccountType = errors.New("cred: invalid account type (must be subscription or console)")
	// ErrInvalidSecret is returned by Add when the secret fails the shared
	// safety check (empty, whitespace/control/quote bytes, or flag-shaped).
	// The error never includes the secret; real Anthropic credentials
	// (sk-ant-… over [A-Za-z0-9_-]) always pass.
	ErrInvalidSecret = errors.New("cred: secret is empty or contains unsafe bytes")
)

// isSecretSafe reports whether secret is storable by every backend: non-empty,
// printable ASCII excluding whitespace and quote/escape characters (which could
// break or inject into the Keychain `security -i` command parser), and not
// flag-shaped (a leading '-' sits positionally after -w on the security command
// line). Enforced in the shared Add path so the file and Keychain backends
// accept exactly the same secrets on every platform.
func isSecretSafe(secret []byte) bool {
	if len(secret) == 0 || secret[0] == '-' {
		return false
	}
	for _, c := range secret {
		if c < 0x21 || c > 0x7e { // reject control bytes, space (0x20), high bytes
			return false
		}
		switch c {
		case '"', '\'', '\\', '`':
			return false
		}
	}
	return true
}

// accountIDRe matches a DNS-1123 label: lowercase alphanumerics and hyphens,
// starting and ending alphanumeric, <=63 chars. Account IDs must satisfy this
// because they are used verbatim as Kubernetes label values
// (sandbox.dev/anthropic-account=<id>).
var accountIDRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Account is the non-secret metadata for one stored Anthropic account. It is
// what the manifest serializes; the secret bytes live only in the backend
// (Keychain or per-account file), never here.
type Account struct {
	// ID is a stable, DNS-label-safe identifier ([a-z0-9-], <=63 chars). It is
	// used verbatim as a Kubernetes label value on per-session Secrets, so it
	// must never contain characters outside the DNS-1123 label set.
	ID string `json:"id"`
	// Label is a human-chosen display name (e.g. "claude.ai", "Work console").
	// Labels are not unique keys.
	Label string `json:"label"`
	// Type is subscription (OAuth) or console (API key).
	Type AccountType `json:"type"`
	// CreatedAt is when the account was added. Used to show account age (the
	// setup-token expiry is opaque and cannot be decoded).
	CreatedAt time.Time `json:"createdAt"`
}

// NewAccount builds an Account with a freshly generated DNS-label-safe ID and
// CreatedAt set to now. The caller supplies the display label and type, then
// passes the returned Account plus its secret bytes to Store.Add.
func NewAccount(label string, typ AccountType) Account {
	return Account{
		ID:        newAccountID(),
		Label:     label,
		Type:      typ,
		CreatedAt: time.Now().UTC(),
	}
}

// newAccountID returns a short, lowercase, DNS-label-safe id of the form
// "acct-<8 hex>". 128 random bits' worth of collision space is unnecessary for
// a single-operator local store; 32 bits keeps the id short (<=63 chars) while
// remaining collision-safe against the handful of accounts one user stores.
func newAccountID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic and unrecoverable; fall back to a
		// timestamp so we never emit an empty/duplicate id.
		return fmt.Sprintf("acct-%08x", time.Now().UnixNano()&0xffffffff)
	}
	return "acct-" + hex.EncodeToString(b[:])
}

// manifest is the on-disk account index. It carries a single top-level
// DefaultID rather than a per-account bool, so there is exactly one source of
// truth for the default (a per-account flag invites two-defaults drift).
type manifest struct {
	Accounts  []Account `json:"accounts"`
	DefaultID string    `json:"defaultID,omitempty"`
}

// Store is the local multi-account credential store. Metadata lives in the
// manifest; secret bytes live in a backend that differs by platform (Keychain
// on macOS, per-account 0600 files elsewhere). Secret bytes never appear in the
// manifest, in logs, or in error messages.
type Store interface {
	// List returns the stored account metadata in manifest order. It never
	// touches secret storage.
	List() ([]Account, error)
	// Add stores acct's metadata and its secret bytes. It returns
	// ErrAccountExists if acct.ID is already present, ErrInvalidAccountID if
	// acct.ID is not DNS-label-safe, ErrInvalidAccountType if acct.Type is not
	// a known AccountType, and ErrInvalidSecret if the secret fails the shared
	// safety check (identical on all backends). The secret is written to the
	// backend first, then the manifest.
	Add(acct Account, secret []byte) error
	// Secret returns the secret bytes for id: ErrNotFound if absent,
	// ErrInvalidAccountID if id is not DNS-label-safe. The bytes are returned
	// to the caller and never logged.
	Secret(id string) ([]byte, error)
	// Remove deletes id's metadata and secret. Removing the current default
	// clears DefaultID. Removing an absent id is not an error (idempotent).
	Remove(id string) error
	// SetDefault records id as the default account. It returns ErrUnknownAccount
	// if id is not in the manifest.
	SetDefault(id string) error
	// Default returns the current default account ID, or "" if none is set.
	Default() (string, error)
}

// secretBackend abstracts where the secret bytes are stored. Both the file and
// Keychain implementations satisfy it; the manifest logic in localStore is
// shared and platform-independent.
type secretBackend interface {
	// put stores (or replaces) the secret for id.
	put(id string, secret []byte) error
	// get returns the secret for id, or ErrNotFound if absent.
	get(id string) ([]byte, error)
	// delete removes the secret for id. Deleting an absent id must be a no-op
	// (no error).
	delete(id string) error
}

// localStore implements Store by pairing shared manifest logic with a
// pluggable secretBackend.
type localStore struct {
	root    string
	secrets secretBackend
}

// manifestLocks serializes the load-modify-write cycle of each manifest path
// within this process, mirroring internal/index.saveLocks. Cross-process races
// are out of scope: the store is single-operator and single-host.
var manifestLocks sync.Map // map[string]*sync.Mutex

func manifestLock(path string) *sync.Mutex {
	m, _ := manifestLocks.LoadOrStore(path, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func (s *localStore) manifestPath() string {
	return filepath.Join(s.root, manifestName)
}

// loadManifest reads the manifest, returning a zero manifest if the file does
// not exist yet.
func (s *localStore) loadManifest() (manifest, error) {
	data, err := os.ReadFile(s.manifestPath())
	if err != nil {
		if os.IsNotExist(err) {
			return manifest{}, nil
		}
		return manifest{}, fmt.Errorf("cred: read manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, fmt.Errorf("cred: parse manifest: %w", err)
	}
	return m, nil
}

// saveManifest writes the manifest atomically (temp+rename) at 0600 under a
// 0700 root, matching the per-session SSH-key handling.
func (s *localStore) saveManifest(m manifest) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("cred: mkdir store root: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".anthropic-accounts-tmp-*")
	if err != nil {
		return fmt.Errorf("cred: create temp manifest: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cred: chmod temp manifest: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cred: write temp manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cred: close temp manifest: %w", err)
	}
	return os.Rename(tmpName, s.manifestPath())
}

// List implements Store.
func (s *localStore) List() ([]Account, error) {
	m, err := s.loadManifest()
	if err != nil {
		return nil, err
	}
	return m.Accounts, nil
}

// Default implements Store.
func (s *localStore) Default() (string, error) {
	m, err := s.loadManifest()
	if err != nil {
		return "", err
	}
	return m.DefaultID, nil
}

// Add implements Store.
func (s *localStore) Add(acct Account, secret []byte) error {
	if !accountIDRe.MatchString(acct.ID) {
		return ErrInvalidAccountID
	}
	if acct.Type != AccountSubscription && acct.Type != AccountConsole {
		return ErrInvalidAccountType
	}
	if !isSecretSafe(secret) {
		return ErrInvalidSecret
	}
	lock := manifestLock(s.manifestPath())
	lock.Lock()
	defer lock.Unlock()

	m, err := s.loadManifest()
	if err != nil {
		return err
	}
	for _, a := range m.Accounts {
		if a.ID == acct.ID {
			return ErrAccountExists
		}
	}
	// Secret first: an orphaned secret (no manifest entry) is inert, whereas a
	// manifest entry with no secret would read as a valid account that fails at
	// resolution time.
	if err := s.secrets.put(acct.ID, secret); err != nil {
		return err
	}
	m.Accounts = append(m.Accounts, acct)
	if err := s.saveManifest(m); err != nil {
		// Roll back the secret so a failed Add leaves no residue.
		_ = s.secrets.delete(acct.ID)
		return err
	}
	return nil
}

// Secret implements Store.
func (s *localStore) Secret(id string) ([]byte, error) {
	if !accountIDRe.MatchString(id) {
		return nil, ErrInvalidAccountID
	}
	return s.secrets.get(id)
}

// Remove implements Store.
func (s *localStore) Remove(id string) error {
	if !accountIDRe.MatchString(id) {
		return ErrInvalidAccountID
	}
	lock := manifestLock(s.manifestPath())
	lock.Lock()
	defer lock.Unlock()

	m, err := s.loadManifest()
	if err != nil {
		return err
	}
	kept := m.Accounts[:0:0]
	for _, a := range m.Accounts {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	m.Accounts = kept
	// Removing the default account clears the default rather than silently
	// promoting an arbitrary survivor: the operator re-picks explicitly.
	if m.DefaultID == id {
		m.DefaultID = ""
	}
	if err := s.saveManifest(m); err != nil {
		return err
	}
	// Best-effort secret delete; the backend's delete is a no-op for an absent
	// id, so a manifest/secret drift still converges.
	return s.secrets.delete(id)
}

// SetDefault implements Store.
func (s *localStore) SetDefault(id string) error {
	if !accountIDRe.MatchString(id) {
		return ErrInvalidAccountID
	}
	lock := manifestLock(s.manifestPath())
	lock.Lock()
	defer lock.Unlock()

	m, err := s.loadManifest()
	if err != nil {
		return err
	}
	found := false
	for _, a := range m.Accounts {
		if a.ID == id {
			found = true
			break
		}
	}
	if !found {
		return ErrUnknownAccount
	}
	m.DefaultID = id
	return s.saveManifest(m)
}

// defaultRoot returns the production store root (~/.local/share/sandbox). The
// manifest and file-backend secrets live directly under it. Unexported: the
// path is an implementation detail; consumers use DefaultStore (or NewFileStore
// with a root of their own choosing).
func defaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "sandbox"), nil
}

// NewFileStore returns a Store that keeps secret bytes in per-account 0600
// files under root/anthropic-secrets/. Available on all platforms; this is the
// CI-testable backend.
func NewFileStore(root string) Store {
	return &localStore{root: root, secrets: newFileBackend(root)}
}

// DefaultStore builds the platform-appropriate Store at the default root
// (~/.local/share/sandbox): the macOS Keychain backend when /usr/bin/security
// is present, otherwise the file backend. This is the store the sandbox CLI
// and TUI share — use it unless you need an isolated root (NewFileStore).
func DefaultStore() (Store, error) {
	root, err := defaultRoot()
	if err != nil {
		return nil, err
	}
	return storeForRoot(root), nil
}

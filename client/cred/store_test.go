package cred

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)

	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty List = %v, %v; want [], nil", list, err)
	}
	if def, err := s.Default(); err != nil || def != "" {
		t.Fatalf("empty Default = %q, %v; want \"\", nil", def, err)
	}

	acct := NewAccount("claude.ai", AccountSubscription)
	secret := []byte("sk-ant-oat01-abcDEF_123-xyz")
	if err := s.Add(acct, secret); err != nil {
		t.Fatalf("Add: %v", err)
	}

	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List after Add = %v, %v; want 1 account", list, err)
	}
	if list[0].ID != acct.ID || list[0].Label != "claude.ai" || list[0].Type != AccountSubscription {
		t.Fatalf("stored account mismatch: %+v", list[0])
	}
	if list[0].CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero; NewAccount should stamp it")
	}

	got, err := s.Secret(acct.ID)
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("Secret round-trip mismatch")
	}

	// SetDefault / Default.
	if err := s.SetDefault(acct.ID); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if def, err := s.Default(); err != nil || def != acct.ID {
		t.Fatalf("Default = %q, %v; want %q", def, err, acct.ID)
	}

	// Remove clears the secret and (since it was default) the DefaultID.
	if err := s.Remove(acct.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("List after Remove = %v, %v; want []", list, err)
	}
	if def, err := s.Default(); err != nil || def != "" {
		t.Fatalf("Default after removing default = %q, %v; want \"\"", def, err)
	}
	if _, err := s.Secret(acct.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Secret after Remove = %v; want ErrNotFound", err)
	}
	// Remove of an absent id is idempotent.
	if err := s.Remove(acct.ID); err != nil {
		t.Fatalf("Remove(absent) = %v; want nil", err)
	}
}

func TestFileStoreSecretFileMode0600(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	acct := NewAccount("work", AccountConsole)
	if err := s.Add(acct, []byte("sk-ant-api03-secretbytes-000")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	secretFile := filepath.Join(root, secretsDirName, acct.ID)
	fi, err := os.Stat(secretFile)
	if err != nil {
		t.Fatalf("stat secret file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("secret file mode = %o; want 0600", perm)
	}

	di, err := os.Stat(filepath.Join(root, secretsDirName))
	if err != nil {
		t.Fatalf("stat secrets dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("secrets dir mode = %o; want 0700", perm)
	}

	// Manifest is 0600 too.
	mi, err := os.Stat(filepath.Join(root, manifestName))
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	if perm := mi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("manifest mode = %o; want 0600", perm)
	}
}

func TestManifestNeverContainsSecret(t *testing.T) {
	root := t.TempDir()
	s := NewFileStore(root)
	acct := NewAccount("work", AccountConsole)
	secret := []byte("sk-ant-api03-SUPERSECRETVALUE-zzz")
	if err := s.Add(acct, secret); err != nil {
		t.Fatalf("Add: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, manifestName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if bytes.Contains(data, secret) {
		t.Fatal("manifest contains the secret bytes")
	}
	if bytes.Contains(data, []byte("SUPERSECRET")) {
		t.Fatal("manifest contains a secret fragment")
	}
	// It should contain the (non-secret) id and label.
	if !bytes.Contains(data, []byte(acct.ID)) || !bytes.Contains(data, []byte("work")) {
		t.Fatal("manifest missing account metadata")
	}
}

func TestAddDuplicateIDErrors(t *testing.T) {
	s := NewFileStore(t.TempDir())
	acct := NewAccount("a", AccountConsole)
	if err := s.Add(acct, []byte("sk-ant-api03-first-000000000")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(acct, []byte("sk-ant-api03-second-00000000")); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("duplicate Add = %v; want ErrAccountExists", err)
	}
}

func TestAddInvalidIDErrors(t *testing.T) {
	s := NewFileStore(t.TempDir())
	bad := Account{ID: "Bad_ID/../x", Label: "x", Type: AccountConsole}
	if err := s.Add(bad, []byte("sk-ant-api03-000000000000000")); !errors.Is(err, ErrInvalidAccountID) {
		t.Fatalf("Add(bad id) = %v; want ErrInvalidAccountID", err)
	}
}

func TestAddInvalidTypeErrors(t *testing.T) {
	s := NewFileStore(t.TempDir())
	secret := []byte("sk-ant-api03-000000000000000")
	for _, typ := range []AccountType{"", "bogus", "Subscription"} {
		bad := NewAccount("x", typ)
		if err := s.Add(bad, secret); !errors.Is(err, ErrInvalidAccountType) {
			t.Fatalf("Add(type %q) = %v; want ErrInvalidAccountType", typ, err)
		}
	}
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("invalid-type Add persisted: List = %v, %v", list, err)
	}
}

// TestAddInvalidSecretErrors asserts the shared validation runs on the file
// backend too — the same Add call must behave identically on every platform.
func TestAddInvalidSecretErrors(t *testing.T) {
	s := NewFileStore(t.TempDir())
	for _, secret := range [][]byte{
		nil,
		{},
		[]byte("-w-flag-shaped-secret-000000"),
		[]byte("has space in secret 00000000"),
		[]byte("has\nnewline-in-secret-000000"),
	} {
		acct := NewAccount("x", AccountConsole)
		if err := s.Add(acct, secret); !errors.Is(err, ErrInvalidSecret) {
			t.Fatalf("Add(secret %q) = %v; want ErrInvalidSecret", secret, err)
		}
	}
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("invalid-secret Add persisted: List = %v, %v", list, err)
	}
}

func TestSetDefaultMissingErrors(t *testing.T) {
	s := NewFileStore(t.TempDir())
	if err := s.SetDefault("acct-deadbeef"); !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("SetDefault(missing) = %v; want ErrUnknownAccount", err)
	}
}

func TestRemoveNonDefaultKeepsDefault(t *testing.T) {
	s := NewFileStore(t.TempDir())
	a := NewAccount("a", AccountConsole)
	b := NewAccount("b", AccountSubscription)
	if err := s.Add(a, []byte("sk-ant-api03-aaaaaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(b, []byte("sk-ant-oat01-bbbbbbbbbbbbbbb")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefault(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(b.ID); err != nil {
		t.Fatal(err)
	}
	if def, _ := s.Default(); def != a.ID {
		t.Fatalf("Default after removing non-default = %q; want %q", def, a.ID)
	}
}

func TestNewAccountIDShape(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := newAccountID()
		if !accountIDRe.MatchString(id) {
			t.Fatalf("generated id %q is not DNS-label-safe", id)
		}
		if len(id) > 63 {
			t.Fatalf("generated id %q exceeds 63 chars", id)
		}
	}
}

// TestFileStorePersistsAcrossInstances confirms load-modify-write reads the
// on-disk manifest rather than in-memory state.
func TestFileStorePersistsAcrossInstances(t *testing.T) {
	root := t.TempDir()
	a := NewAccount("a", AccountConsole)
	if err := NewFileStore(root).Add(a, []byte("sk-ant-api03-persist-0000000")); err != nil {
		t.Fatal(err)
	}
	list, err := NewFileStore(root).List()
	if err != nil || len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("second instance List = %v, %v; want the added account", list, err)
	}
}

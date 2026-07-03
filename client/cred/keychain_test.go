package cred

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeKeychain is an in-memory stand-in for `security`. It records the argv of
// each call so tests can assert the secret is never passed as an argument.
type fakeKeychain struct {
	items    map[string][]byte // account id -> secret
	lastArgs []string
	lastCmds []string // interactive commands seen on stdin
}

func newFakeKeychain() *fakeKeychain { return &fakeKeychain{items: map[string][]byte{}} }

func (f *fakeKeychain) run(_ context.Context, name string, args []string, stdin []byte) ([]byte, int, error) {
	f.lastArgs = args
	if name != securityBin {
		return nil, -1, errors.New("unexpected binary")
	}
	// Interactive write: `security -i`, command on stdin.
	if len(args) == 1 && args[0] == "-i" {
		line := strings.TrimSpace(string(stdin))
		f.lastCmds = append(f.lastCmds, line)
		fields := strings.Fields(line)
		// add-generic-password -U -s <svc> -a <id> -w <secret>
		var id string
		var secret []byte
		for i := 0; i < len(fields); i++ {
			switch fields[i] {
			case "-a":
				if i+1 < len(fields) {
					id = fields[i+1]
				}
			case "-w":
				if i+1 < len(fields) {
					secret = []byte(fields[i+1])
				}
			}
		}
		f.items[id] = secret
		return nil, 0, nil
	}
	// find-generic-password -s <svc> -a <id> -w
	if len(args) > 0 && args[0] == "find-generic-password" {
		id := argValue(args, "-a")
		secret, ok := f.items[id]
		if !ok {
			return nil, keychainNotFound, errors.New("exit 44")
		}
		return append(secret, '\n'), 0, nil
	}
	// delete-generic-password -s <svc> -a <id>
	if len(args) > 0 && args[0] == "delete-generic-password" {
		id := argValue(args, "-a")
		if _, ok := f.items[id]; !ok {
			return nil, keychainNotFound, errors.New("exit 44")
		}
		delete(f.items, id)
		return nil, 0, nil
	}
	return nil, -1, errors.New("unhandled command")
}

func argValue(args []string, flag string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestKeychainStoreRoundTrip(t *testing.T) {
	fk := newFakeKeychain()
	s := newKeychainStore(t.TempDir(), fk.run)

	acct := NewAccount("claude.ai", AccountSubscription)
	secret := []byte("sk-ant-oat01-keychainsecret-abc")
	if err := s.Add(acct, secret); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := s.Secret(acct.ID)
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("keychain round-trip mismatch")
	}

	if err := s.Remove(acct.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := s.Secret(acct.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Secret after Remove = %v; want ErrNotFound", err)
	}
	// Remove of an absent id maps exit-44 to a no-op.
	if err := s.Remove(acct.ID); err != nil {
		t.Fatalf("Remove(absent) = %v; want nil", err)
	}
}

// TestKeychainSecretNeverInArgv is the core security assertion: on write, the
// secret must arrive only on stdin, never in the process argv.
func TestKeychainSecretNeverInArgv(t *testing.T) {
	fk := newFakeKeychain()
	s := newKeychainStore(t.TempDir(), fk.run)

	acct := NewAccount("work", AccountConsole)
	secret := []byte("sk-ant-api03-DONOTLEAK-000000000")
	if err := s.Add(acct, secret); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// The write call's argv must be exactly {"-i"} — no secret, no metadata.
	if len(fk.lastArgs) != 1 || fk.lastArgs[0] != "-i" {
		t.Fatalf("write argv = %v; want [-i]", fk.lastArgs)
	}
	for _, a := range fk.lastArgs {
		if strings.Contains(a, string(secret)) {
			t.Fatalf("secret leaked into argv element %q", a)
		}
	}
	// The secret must be present on the stdin command line.
	if len(fk.lastCmds) == 0 || !strings.Contains(fk.lastCmds[len(fk.lastCmds)-1], string(secret)) {
		t.Fatalf("secret not delivered on stdin: %v", fk.lastCmds)
	}
}

func TestKeychainRejectsUnsafeSecret(t *testing.T) {
	fk := newFakeKeychain()
	s := newKeychainStore(t.TempDir(), fk.run)
	acct := NewAccount("x", AccountConsole)
	// Contains a space and a newline — could break the -i command parser or
	// inject a second command.
	if err := s.Add(acct, []byte("has space\nadd-generic-password evil")); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("Add(unsafe) = %v; want ErrInvalidSecret", err)
	}
}

// TestKeychainBackendRejectsUnsafeSecretDirectly asserts the defense-in-depth
// check at the injection boundary itself (put), independent of Add's shared
// validation.
func TestKeychainBackendRejectsUnsafeSecretDirectly(t *testing.T) {
	fk := newFakeKeychain()
	b := keychainBackend{service: keychainService, run: fk.run}
	if err := b.put("acct-deadbeef", []byte("evil\nadd-generic-password x")); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("put(unsafe) = %v; want ErrInvalidSecret", err)
	}
	if len(fk.lastCmds) != 0 {
		t.Fatalf("unsafe secret reached the exec seam: %v", fk.lastCmds)
	}
}

func TestIsSecretSafe(t *testing.T) {
	cases := []struct {
		in   string
		safe bool
	}{
		{"sk-ant-oat01-abcDEF_123-xyz", true},
		{"sk-ant-api03-ABC_def-000", true},
		{"", false},
		{"-w-leading-flag-shape", false},
		{"has space", false},
		{"has\ttab", false},
		{"has\nnewline", false},
		{`has"quote`, false},
		{"has'quote", false},
		{`has\backslash`, false},
		{"has`backtick", false},
		{"unicodé", false},
	}
	for _, c := range cases {
		if got := isSecretSafe([]byte(c.in)); got != c.safe {
			t.Errorf("isSecretSafe(%q) = %v; want %v", c.in, got, c.safe)
		}
	}
}

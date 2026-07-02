package cred

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// securityBin is the macOS Security CLI. Writes go through its interactive
// (-i / stdin) mode so the secret is never an argv element.
const securityBin = "/usr/bin/security"

// keychainService is the generic-password service name shared by all accounts;
// each account is one item keyed by Account.ID.
const keychainService = "sandbox-anthropic"

// keychainNotFound is `security`'s exit code for "the specified item could not
// be found" (errSecItemNotFound). Distinguished so reads/deletes of an absent
// item map to ErrNotFound / a no-op rather than an opaque failure.
const keychainNotFound = 44

// execRunner is the injectable exec seam. It runs name with args, feeding stdin
// to the process, and returns stdout, the process exit code, and any error.
// Tests substitute this to assert the secret is passed only via stdin, never in
// args.
type execRunner func(ctx context.Context, name string, args []string, stdin []byte) (stdout []byte, code int, err error)

// realExec runs the command for real via os/exec.
func realExec(ctx context.Context, name string, args []string, stdin []byte) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return out.Bytes(), code, err
}

// keychainBackend stores each account's secret as a generic-password item in
// the macOS login keychain (service=keychainService, account=Account.ID).
type keychainBackend struct {
	service string
	run     execRunner
}

// put writes the secret via `security -i`, assembling the interactive command
// in a buffer so the secret bytes reach the process only on stdin — never as an
// argv element visible in `ps`. -U upserts (replaces an existing item).
// isSecretSafe is re-checked here (already enforced by localStore.Add) because
// put is the injection boundary: the secret is embedded unescaped in the -i
// command line, so this backend must never receive an unsafe byte sequence.
func (k keychainBackend) put(id string, secret []byte) error {
	if !isSecretSafe(secret) {
		return ErrInvalidSecret
	}
	var cmd bytes.Buffer
	cmd.WriteString("add-generic-password -U -s ")
	cmd.WriteString(k.service)
	cmd.WriteString(" -a ")
	cmd.WriteString(id)
	cmd.WriteString(" -w ")
	cmd.Write(secret) // secret stays []byte; only ever on stdin
	cmd.WriteByte('\n')
	_, code, err := k.run(context.Background(), securityBin, []string{"-i"}, cmd.Bytes())
	if err != nil {
		return fmt.Errorf("cred: keychain store for %s: %w (exit %d)", id, err, code)
	}
	return nil
}

// get reads the secret from stdout of `find-generic-password -w`. The service
// and id are not secret, so they may be plain args; the secret arrives on
// stdout and is captured in-process, never echoed.
func (k keychainBackend) get(id string) ([]byte, error) {
	out, code, err := k.run(context.Background(), securityBin,
		[]string{"find-generic-password", "-s", k.service, "-a", id, "-w"}, nil)
	if code == keychainNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cred: keychain read for %s: %w (exit %d)", id, err, code)
	}
	return bytes.TrimRight(out, "\r\n"), nil
}

// delete removes the item, treating "not found" as success (no-op contract).
func (k keychainBackend) delete(id string) error {
	_, code, err := k.run(context.Background(), securityBin,
		[]string{"delete-generic-password", "-s", k.service, "-a", id}, nil)
	if code == keychainNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cred: keychain delete for %s: %w (exit %d)", id, err, code)
	}
	return nil
}

// NewKeychainStore returns a Store whose secrets live in the macOS Keychain and
// whose manifest lives under root. Usable only where /usr/bin/security exists.
func NewKeychainStore(root string) Store {
	return newKeychainStore(root, realExec)
}

// newKeychainStore is the injectable constructor: tests supply a fake
// execRunner to assert command construction (notably that the secret never
// appears in args) without a real Keychain.
func newKeychainStore(root string, run execRunner) Store {
	return &localStore{root: root, secrets: keychainBackend{service: keychainService, run: run}}
}

// storeForRoot selects the platform-appropriate backend: Keychain on darwin
// when /usr/bin/security is present, otherwise the file backend.
func storeForRoot(root string) Store {
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(securityBin); err == nil {
			return NewKeychainStore(root)
		}
	}
	return NewFileStore(root)
}

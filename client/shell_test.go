package client

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/cullenmcdermott/sandbox/internal/k8s"
	"github.com/cullenmcdermott/sandbox/internal/session"
)

// TestSSHTarget pins the SSH-target seam Shell is built on: the material a
// non-interactive consumer feeds to their own ssh/scp. It runs against the fake
// backend (no cluster), so it verifies the resolution — SSH-only forward, the
// per-session key path, host/user/port — and the cleanup teardown.
func TestSSHTarget(t *testing.T) {
	ctx := context.Background()

	t.Run("forwards the ssh port only and resolves the target", func(t *testing.T) {
		be := newFakeBackend()
		h := &closeSpyHandle{port: 2222, done: make(chan struct{})}
		be.handles = []session.ForwardHandle{h}
		c, _, _ := fakeClient(t, be)

		tgt, cleanup, err := c.Open("sess-1").SSHTarget(ctx)
		if err != nil {
			t.Fatalf("SSHTarget: %v", err)
		}

		// Only the SSH port is forwarded — no runner HTTP / opencode waste.
		want := []session.PortSpec{{Local: 0, Remote: k8s.SSHPort()}}
		if !reflect.DeepEqual(be.gotSpecs, want) {
			t.Errorf("PortForward specs = %v, want ssh-only %v", be.gotSpecs, want)
		}
		if tgt.Host != "127.0.0.1" {
			t.Errorf("Host = %q, want 127.0.0.1", tgt.Host)
		}
		if tgt.User != "root" {
			t.Errorf("User = %q, want root", tgt.User)
		}
		if tgt.Port != 2222 {
			t.Errorf("Port = %d, want the forwarded local port 2222", tgt.Port)
		}
		// The identity file is the per-session key under the state dir and exists on
		// disk (ensureSSHKey generated it).
		wantKey := filepath.Join(c.StateDir(), "sess-1", "id_ed25519")
		if tgt.IdentityFile != wantKey {
			t.Errorf("IdentityFile = %q, want %q", tgt.IdentityFile, wantKey)
		}
		if _, err := os.Stat(tgt.IdentityFile); err != nil {
			t.Errorf("identity file not written: %v", err)
		}

		cleanup()
		if h.closes != 1 {
			t.Errorf("cleanup closed the forward %d times, want 1", h.closes)
		}
	})

	t.Run("port-forward error surfaces and opens no forward", func(t *testing.T) {
		be := newFakeBackend()
		be.portErr = errors.New("no pod")
		c, _, _ := fakeClient(t, be)

		if _, _, err := c.Open("sess-2").SSHTarget(ctx); err == nil {
			t.Fatal("SSHTarget: want port-forward error, got nil")
		}
	})
}

// TestSSHExitCode covers the exit-code mapping, the trickiest part of the
// interactive path, without a network: a clean exit, a missing-status close
// (signal kill), and a transport error. The *ssh.ExitError branch (a real
// non-zero remote status) can only be constructed by the ssh package itself
// (unexported fields), so it is exercised only end-to-end against a real pod.
func TestSSHExitCode(t *testing.T) {
	if code, err := sshExitCode(nil); code != 0 || err != nil {
		t.Errorf("nil => (%d, %v), want (0, nil)", code, err)
	}
	if code, err := sshExitCode(&ssh.ExitMissingError{}); code != -1 || err != nil {
		t.Errorf("ExitMissingError => (%d, %v), want (-1, nil)", code, err)
	}
	boom := errors.New("connection reset")
	code, err := sshExitCode(boom)
	if code != -1 || err == nil {
		t.Fatalf("transport error => (%d, %v), want (-1, non-nil)", code, err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("transport error not wrapped: %v", err)
	}
}

// TestTerminalFD confirms a non-file reader is never treated as a terminal, so
// Shell runs a piped/programmatic Stdin non-interactively (no PTY / raw mode).
func TestTerminalFD(t *testing.T) {
	if _, isTTY := terminalFD(bytes.NewBufferString("hi")); isTTY {
		t.Error("a *bytes.Buffer must not be reported as a terminal")
	}
	// A regular file has a valid fd but is not a terminal.
	f, err := os.CreateTemp(t.TempDir(), "notty")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer f.Close()
	if _, isTTY := terminalFD(f); isTTY {
		t.Error("a regular file must not be reported as a terminal")
	}
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/cred"
)

// fakePrompt returns a secretPromptFunc that yields the given values in order.
func fakePrompt(values ...string) secretPromptFunc {
	i := 0
	return func(string) (string, error) {
		if i >= len(values) {
			return "", errors.New("no more prompt input")
		}
		v := values[i]
		i++
		return v, nil
	}
}

func TestRunAuthLogin_Subscription(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	// The captured stdout carries UI noise plus the token line — ParseSetupToken
	// must extract the sk-ant-oat line from it.
	setupOut := "Visit https://claude.ai/... to authorize\nPaste the code: ****\nsk-ant-oat-CAPTURED-FROM-STDOUT\n"
	runSetup := func(context.Context) (string, error) { return setupOut, nil }

	var out bytes.Buffer
	o := authLoginOpts{subscription: true}
	if err := runAuthLogin(context.Background(), &out, store, o, runSetup, fakePrompt()); err != nil {
		t.Fatalf("runAuthLogin: %v", err)
	}

	accounts, _ := store.List()
	if len(accounts) != 1 {
		t.Fatalf("want 1 account stored, got %d", len(accounts))
	}
	if accounts[0].Type != cred.AccountSubscription {
		t.Errorf("type: got %q, want subscription", accounts[0].Type)
	}
	if accounts[0].Label != "claude.ai" {
		t.Errorf("label: got %q, want claude.ai (default)", accounts[0].Label)
	}
	secret, err := store.Secret(accounts[0].ID)
	if err != nil {
		t.Fatalf("read secret: %v", err)
	}
	if string(secret) != "sk-ant-oat-CAPTURED-FROM-STDOUT" {
		t.Errorf("stored token: got %q, want the parsed stdout token", secret)
	}
	// First (only) account auto-becomes the default.
	if def, _ := store.Default(); def != accounts[0].ID {
		t.Errorf("first account should auto-default; got %q", def)
	}
	// Output must not echo the token.
	if strings.Contains(out.String(), "sk-ant-oat") {
		t.Errorf("login output must never echo the token: %q", out.String())
	}
}

// TestRunAuthLogin_SubscriptionParseFailureDoesNotEcho: a malformed capture
// yields an error that steers to --paste and NEVER contains the captured buffer.
func TestRunAuthLogin_SubscriptionParseFailureDoesNotEcho(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	secretNoise := "SUPER-SECRET-NOISE-THAT-MUST-NOT-LEAK"
	runSetup := func(context.Context) (string, error) { return secretNoise + "\n", nil }

	var out bytes.Buffer
	o := authLoginOpts{subscription: true}
	err := runAuthLogin(context.Background(), &out, store, o, runSetup, fakePrompt())
	if err == nil {
		t.Fatal("expected a parse error for a token-less capture")
	}
	if strings.Contains(err.Error(), secretNoise) {
		t.Errorf("parse error must not echo the captured buffer: %v", err)
	}
	if !strings.Contains(err.Error(), "--paste") {
		t.Errorf("parse error should steer to the --paste fallback: %v", err)
	}
	if accounts, _ := store.List(); len(accounts) != 0 {
		t.Errorf("no account should be stored on parse failure, got %d", len(accounts))
	}
}

func TestRunAuthLogin_SubscriptionPaste(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	var out bytes.Buffer
	o := authLoginOpts{subscription: true, paste: true, label: "personal"}
	// --paste reads the token from the prompt; the subprocess seam must not run.
	runSetup := func(context.Context) (string, error) {
		t.Fatal("setup-token subprocess must not run in --paste mode")
		return "", nil
	}
	if err := runAuthLogin(context.Background(), &out, store, o, runSetup, fakePrompt("sk-ant-oat-PASTED-TOKEN")); err != nil {
		t.Fatalf("runAuthLogin: %v", err)
	}
	accounts, _ := store.List()
	if len(accounts) != 1 || accounts[0].Label != "personal" {
		t.Fatalf("want 1 account labeled personal, got %#v", accounts)
	}
	secret, _ := store.Secret(accounts[0].ID)
	if string(secret) != "sk-ant-oat-PASTED-TOKEN" {
		t.Errorf("stored token mismatch: %q", secret)
	}
}

func TestRunAuthLogin_Console(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	var out bytes.Buffer
	o := authLoginOpts{console: true}
	// A trailing newline on the paste must be normalized away by ValidateConsoleKey.
	if err := runAuthLogin(context.Background(), &out, store, o, nil, fakePrompt("sk-ant-api-CONSOLE-KEY\n")); err != nil {
		t.Fatalf("runAuthLogin: %v", err)
	}
	accounts, _ := store.List()
	if len(accounts) != 1 || accounts[0].Type != cred.AccountConsole {
		t.Fatalf("want 1 console account, got %#v", accounts)
	}
	if accounts[0].Label != "console" {
		t.Errorf("label: got %q, want console (default)", accounts[0].Label)
	}
	secret, _ := store.Secret(accounts[0].ID)
	if string(secret) != "sk-ant-api-CONSOLE-KEY" {
		t.Errorf("stored key should be the normalized (trimmed) value, got %q", secret)
	}
}

func TestRunAuthLogin_RequiresExactlyOneMode(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	var out bytes.Buffer
	// Neither mode.
	if err := runAuthLogin(context.Background(), &out, store, authLoginOpts{}, nil, fakePrompt()); err == nil {
		t.Error("expected an error when neither --subscription nor --console is set")
	}
	// Both modes.
	if err := runAuthLogin(context.Background(), &out, store, authLoginOpts{subscription: true, console: true}, nil, fakePrompt()); err == nil {
		t.Error("expected an error when both modes are set")
	}
}

func TestRunAuthList(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())

	// Empty store → friendly message, no error.
	var empty bytes.Buffer
	if err := runAuthList(&empty, store); err != nil {
		t.Fatalf("runAuthList empty: %v", err)
	}
	if !strings.Contains(empty.String(), "No anthropic accounts") {
		t.Errorf("empty list should print a friendly message, got: %q", empty.String())
	}

	a := cred.NewAccount("claude.ai", cred.AccountSubscription)
	_ = store.Add(a, []byte("sk-ant-oat-TOKEN"))
	_ = store.SetDefault(a.ID)
	b := cred.NewAccount("work", cred.AccountConsole)
	_ = store.Add(b, []byte("sk-ant-api-KEY"))

	var out bytes.Buffer
	if err := runAuthList(&out, store); err != nil {
		t.Fatalf("runAuthList: %v", err)
	}
	got := out.String()
	for _, want := range []string{a.ID, "claude.ai", "subscription", b.ID, "work", "console"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q\n%s", want, got)
		}
	}
	// The default row carries the marker; a token must never appear.
	if strings.Contains(got, "sk-ant") {
		t.Errorf("list must never print secret material: %q", got)
	}
}

func TestRunAuthDefault(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	a := cred.NewAccount("claude.ai", cred.AccountSubscription)
	_ = store.Add(a, []byte("sk-ant-oat-TOKEN"))

	var out bytes.Buffer
	if err := runAuthDefault(&out, store, "claude.ai"); err != nil {
		t.Fatalf("runAuthDefault: %v", err)
	}
	if def, _ := store.Default(); def != a.ID {
		t.Errorf("default: got %q, want %q", def, a.ID)
	}

	// Unknown selector errors.
	if err := runAuthDefault(&out, store, "nope"); err == nil {
		t.Error("expected an error for an unknown selector")
	}
}

func TestRunAuthLogout(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	a := cred.NewAccount("work", cred.AccountConsole)
	_ = store.Add(a, []byte("sk-ant-api-KEY"))

	t.Run("removes locally and lists live sessions", func(t *testing.T) {
		called := ""
		lister := func(_ context.Context, id string) ([]string, error) {
			called = id
			return []string{"sess-a", "sess-b"}, nil
		}
		var out bytes.Buffer
		if err := runAuthLogout(context.Background(), &out, store, lister, a.ID); err != nil {
			t.Fatalf("runAuthLogout: %v", err)
		}
		if called != a.ID {
			t.Errorf("session lister called with %q, want %q", called, a.ID)
		}
		if accounts, _ := store.List(); len(accounts) != 0 {
			t.Errorf("account should be removed locally, %d remain", len(accounts))
		}
		got := out.String()
		for _, want := range []string{"sess-a", "sess-b", "does not scrub", "revoke"} {
			if !strings.Contains(got, want) {
				t.Errorf("logout output missing %q\n%s", want, got)
			}
		}
	})

	t.Run("degrades when cluster unreachable", func(t *testing.T) {
		store := cred.NewFileStore(t.TempDir())
		acct := cred.NewAccount("work", cred.AccountConsole)
		_ = store.Add(acct, []byte("sk-ant-api-KEY"))
		lister := func(context.Context, string) ([]string, error) {
			return nil, errors.New("dial tcp: connection refused")
		}
		var out bytes.Buffer
		// Logout must still succeed locally.
		if err := runAuthLogout(context.Background(), &out, store, lister, acct.ID); err != nil {
			t.Fatalf("logout must succeed even when the cluster is unreachable: %v", err)
		}
		if accounts, _ := store.List(); len(accounts) != 0 {
			t.Errorf("account should be removed locally despite the cluster error")
		}
		if !strings.Contains(out.String(), "warning") {
			t.Errorf("expected a degrade warning, got: %q", out.String())
		}
	})
}

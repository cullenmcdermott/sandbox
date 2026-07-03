package sdktest

// conformance_test.go — behavioral expectations an external consumer relies
// on, exercised from outside the main module. Where surface_test.go pins that
// the API still COMPILES, these pin that it still MEANS the same thing: wire
// constants keep their values, the credential store round-trips, and account
// selection stays fail-closed.

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/client/cred"
)

// TestEventWireContract pins the string values of core event types and the
// shape of a payload. These are the SSE wire protocol: a renamed constant value
// breaks every consumer's stored events and switch statements.
func TestEventWireContract(t *testing.T) {
	wire := map[client.EventType]string{
		client.EventSessionStarted:   "session.started",
		client.EventTurnCompleted:    "turn.completed",
		client.EventMessageCompleted: "message.completed",
	}
	for got, want := range wire {
		if string(got) != want {
			t.Errorf("event constant changed value: got %q, want %q", got, want)
		}
	}
	if len(client.AllEventTypes) == 0 {
		t.Error("AllEventTypes is empty")
	}

	// A consumer decodes payloads by event type; MessagePayload must keep
	// unmarshalling from the documented shape.
	var m client.MessagePayload
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":"hi","delta":true}`), &m); err != nil {
		t.Fatalf("MessagePayload unmarshal: %v", err)
	}
	if m.Role != "assistant" || m.Content != "hi" || !m.Delta {
		t.Errorf("MessagePayload fields: got %+v", m)
	}
}

// TestCredStoreRoundTrip exercises the full documented Store lifecycle on the
// portable file backend: add → list → secret → default → remove.
func TestCredStoreRoundTrip(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())

	acct := cred.NewAccount("work", cred.AccountConsole)
	if err := store.Add(acct, []byte("sk-ant-api-CONFORMANCE-KEY")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Add(acct, []byte("sk-ant-api-DUPLICATE")); !errors.Is(err, cred.ErrAccountExists) {
		t.Errorf("duplicate Add: want ErrAccountExists, got %v", err)
	}

	accounts, err := store.List()
	if err != nil || len(accounts) != 1 || accounts[0].ID != acct.ID {
		t.Fatalf("List: got %v, %v", accounts, err)
	}

	secret, err := store.Secret(acct.ID)
	if err != nil || string(secret) != "sk-ant-api-CONFORMANCE-KEY" {
		t.Fatalf("Secret: got %q, %v", secret, err)
	}
	if _, err := store.Secret("acct-absent"); !errors.Is(err, cred.ErrNotFound) {
		t.Errorf("absent Secret: want ErrNotFound, got %v", err)
	}

	if err := store.SetDefault(acct.ID); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if def, err := store.Default(); err != nil || def != acct.ID {
		t.Fatalf("Default: got %q, %v", def, err)
	}

	if err := store.Remove(acct.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if def, _ := store.Default(); def != "" {
		t.Errorf("Default after removing the default account: got %q, want \"\"", def)
	}
	if err := store.Remove(acct.ID); err != nil {
		t.Errorf("Remove must be idempotent, got %v", err)
	}
}

// TestAccountSelectionFailClosed pins the SDK's central account-safety
// contract: once an account is requested, every failure refuses the launch —
// CreateOptions is never silently left on the shared-Secret path.
func TestAccountSelectionFailClosed(t *testing.T) {
	store := cred.NewFileStore(t.TempDir())
	acct := cred.NewAccount("work", cred.AccountConsole)
	if err := store.Add(acct, []byte("sk-ant-api-CONFORMANCE-KEY")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	var opts client.CreateOptions
	if err := opts.UseAnthropicAccount(store, acct.ID); err != nil {
		t.Fatalf("UseAnthropicAccount: %v", err)
	}
	if opts.AnthropicAccountID != acct.ID || opts.AnthropicAuth != "api-key" ||
		string(opts.AnthropicCredential) != "sk-ant-api-CONFORMANCE-KEY" {
		t.Errorf("resolved options mismatch: %+v", opts)
	}

	var opts2 client.CreateOptions
	if err := opts2.UseAnthropicAccount(store, "acct-ghost"); !errors.Is(err, cred.ErrUnknownAccount) {
		t.Fatalf("unknown account id must be a hard ErrUnknownAccount error, got: %v", err)
	}
	if opts2.AnthropicAccountID != "" || opts2.AnthropicCredential != nil {
		t.Error("options must stay untouched on failure (no silent shared-Secret fallback)")
	}

	// Legacy path: no accounts + no selector leaves options untouched.
	empty := cred.NewFileStore(t.TempDir())
	var opts3 client.CreateOptions
	if err := opts3.SelectAnthropicAccount(empty, ""); err != nil {
		t.Fatalf("legacy no-account path errored: %v", err)
	}
	if opts3.AnthropicAccountID != "" || opts3.AnthropicAuth != "" {
		t.Error("legacy path must not set account fields")
	}
}

// TestTokenHelpers pins the credential-parsing helpers a consumer uses to build
// their own login flows.
func TestTokenHelpers(t *testing.T) {
	tok, err := cred.ParseSetupToken("some UI noise\nsk-ant-oat01-EXAMPLETOKENBODY_1234\n")
	if err != nil || tok != "sk-ant-oat01-EXAMPLETOKENBODY_1234" {
		t.Errorf("ParseSetupToken: got %q, %v", tok, err)
	}
	if _, err := cred.ParseSetupToken("no token here"); !errors.Is(err, cred.ErrNoSetupToken) {
		t.Errorf("ParseSetupToken miss: want ErrNoSetupToken, got %v", err)
	}

	key, err := cred.ValidateConsoleKey("  sk-ant-api03-EXAMPLEKEYBODY_1234  ")
	if err != nil || key != "sk-ant-api03-EXAMPLEKEYBODY_1234" {
		t.Errorf("ValidateConsoleKey should trim and accept: got %q, %v", key, err)
	}
	if _, err := cred.ValidateConsoleKey("sk-ant-oat01-SETUPTOKEN_NOT_A_KEY"); !errors.Is(err, cred.ErrInvalidConsoleKey) {
		t.Errorf("setup token must be rejected as a console key, got %v", err)
	}

	// The AnthropicAuth spellings are a consumer contract with the k8s layer.
	if a, _ := cred.AuthForType(cred.AccountSubscription); a != "oauth" {
		t.Errorf("AuthForType(subscription): got %q, want oauth", a)
	}
	if a, _ := cred.AuthForType(cred.AccountConsole); a != "api-key" {
		t.Errorf("AuthForType(console): got %q, want api-key", a)
	}
}

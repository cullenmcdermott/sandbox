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

// TestEventConstantsCoverAllTypes pins that the public client surface
// re-exports a typed constant for EVERY persisted event type. client.AllEventTypes
// is generated from schema/events.json (via internal/session), so when a new
// event type lands there `just gen` grows AllEventTypes and this test fails
// until the constant is added to client/events.go AND to the map below — closing
// the drift that let tool.progress / context.compacted ship in the schema with
// no client constant (V8). Referencing each constant by name also makes this a
// compile-time pin: a removed/renamed constant fails to build.
func TestEventConstantsCoverAllTypes(t *testing.T) {
	// The full set of re-exported client event-type constants, keyed by value.
	// Maintained by hand alongside client/events.go's const block.
	constants := map[client.EventType]bool{
		client.EventSessionStarted:       true,
		client.EventSessionStatusChanged: true,
		client.EventSessionTerminating:   true,
		client.EventTurnStarted:          true,
		client.EventTurnCompleted:        true,
		client.EventTurnFailed:           true,
		client.EventTurnInterrupted:      true,
		client.EventMessageStarted:       true,
		client.EventMessageDelta:         true,
		client.EventMessageCompleted:     true,
		client.EventReasoningStarted:     true,
		client.EventReasoningDelta:       true,
		client.EventReasoningCompleted:   true,
		client.EventToolStarted:          true,
		client.EventToolCompleted:        true,
		client.EventToolFailed:           true,
		client.EventPermissionRequested:  true,
		client.EventPermissionResolved:   true,
		client.EventUsageUpdated:         true,
		client.EventContextCompacted:     true,
		client.EventRateLimitUpdated:     true,
		client.EventWorkspaceStatus:      true,
		client.EventSessionTitle:         true,
		client.EventError:                true,
	}
	// Every persisted type must have a re-exported constant.
	for _, et := range client.AllEventTypes {
		if !constants[et] {
			t.Errorf("no client constant for event type %q — add it to client/events.go and this test", et)
		}
	}
	// And no stray constant that is not a real persisted type (guards a typo or a
	// removed schema type).
	allowed := map[client.EventType]bool{}
	for _, et := range client.AllEventTypes {
		allowed[et] = true
	}
	for et := range constants {
		if !allowed[et] {
			t.Errorf("client constant %q is not in AllEventTypes (schema drift?)", et)
		}
	}
}

// TestEventPayloadAliasesDecode pins the payload type aliases a consumer decodes
// Event.Payload into — including the ones V8 added (context.compacted, the four
// turn.* payloads, and Citation). A removed or retyped alias fails to compile;
// the unmarshals pin the wire shape.
func TestEventPayloadAliasesDecode(t *testing.T) {
	var cc client.ContextCompactedPayload
	if err := json.Unmarshal([]byte(`{"trigger":"auto","preTokens":180000,"postTokens":42000}`), &cc); err != nil {
		t.Fatalf("ContextCompactedPayload unmarshal: %v", err)
	}
	if cc.Trigger != "auto" || cc.PreTokens != 180000 || cc.PostTokens != 42000 {
		t.Errorf("ContextCompactedPayload fields: got %+v", cc)
	}

	var ts client.TurnStartedPayload
	if err := json.Unmarshal([]byte(`{"prompt":"hi"}`), &ts); err != nil || ts.Prompt != "hi" {
		t.Errorf("TurnStartedPayload: got %+v err=%v", ts, err)
	}
	var tc client.TurnCompletedPayload
	if err := json.Unmarshal([]byte(`{"result":"done"}`), &tc); err != nil ||
		tc.Result != "done" {
		t.Errorf("TurnCompletedPayload: got %+v err=%v", tc, err)
	}
	var tf client.TurnFailedPayload
	if err := json.Unmarshal([]byte(`{"message":"boom"}`), &tf); err != nil || tf.Message != "boom" {
		t.Errorf("TurnFailedPayload: got %+v err=%v", tf, err)
	}
	var ti client.TurnInterruptedPayload
	if err := json.Unmarshal([]byte(`{"reason":"client interrupt"}`), &ti); err != nil ||
		ti.Reason != "client interrupt" {
		t.Errorf("TurnInterruptedPayload: got %+v err=%v", ti, err)
	}

	// Citation is nested in MessagePayload.Citations (message.completed): a
	// consumer building a footnote renderer must be able to name and decode it.
	var m client.MessagePayload
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":"x","citations":[{"url":"https://e.x","title":"E"}]}`), &m); err != nil {
		t.Fatalf("MessagePayload with citations: %v", err)
	}
	if len(m.Citations) != 1 || m.Citations[0].Title != "E" || m.Citations[0].URL != "https://e.x" {
		t.Errorf("MessagePayload.Citations: got %+v", m.Citations)
	}
	var c client.Citation = m.Citations[0]
	_ = c
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

package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/cullenmcdermott/sandbox/client"
)

// Example exercises the full public surface the way an external Go program would:
// build a client, create a session, connect, start a turn, and stream events. It
// has no Output directive, so it is compiled (proving the API is usable from
// outside the module) but not executed (it would need a live cluster).
func Example() {
	ctx := context.Background()

	c, err := client.New(
		client.WithContext("my-cluster"),
		client.WithNamespace("agent-sessions"),
	)
	if err != nil {
		return
	}

	sess, err := c.Create(ctx, client.CreateOptions{ProjectPath: "/work/repo"})
	if err != nil {
		return
	}
	defer sess.Close()

	if _, err := sess.Connect(ctx, client.ConnectOptions{}); err != nil {
		if errors.Is(err, client.ErrSessionGone) {
			return
		}
		return
	}

	turn, err := sess.StartTurn(ctx, client.TurnInput{Prompt: "fix the build"})
	if err != nil {
		return
	}
	fmt.Println(turn.Turn)

	events, err := sess.Events(ctx, 0)
	if err != nil {
		return
	}
	for ev := range events {
		switch ev.Type {
		case client.EventMessageCompleted:
			var m client.MessagePayload
			if err := json.Unmarshal(ev.Payload, &m); err == nil {
				fmt.Println(m.Role, m.Content)
			}
		case client.EventTurnCompleted:
			return
		}
	}
}

// TestPublicSurface asserts the public types, aliases, and error sentinels are
// reachable and well-formed from an external importer.
func TestPublicSurface(t *testing.T) {
	if client.ErrSessionGone == nil || client.ErrNoActiveTurn == nil || client.ErrProjectPathRequired == nil || client.ErrNotConnected == nil {
		t.Fatal("expected non-nil error sentinels")
	}
	if client.DefaultRunnerImage == "" {
		t.Fatal("expected a non-empty DefaultRunnerImage")
	}
	if client.DefaultIdleTimeout <= 0 {
		t.Fatal("expected a positive DefaultIdleTimeout")
	}

	// The aliases are the engine types; construct a few to prove field access.
	spec := client.Spec{ID: "x", ProjectPath: "/p", Backend: client.BackendClaudeSDK, ImagePullPolicy: "IfNotPresent"}
	if spec.ID != "x" {
		t.Fatalf("Spec round-trip: got %q", spec.ID)
	}
	_ = client.State{ID: "x", Status: client.StatusRunning}
	_ = client.TurnInput{Prompt: "hi"}
	_ = client.PermissionDecision{Allow: true, Scope: "once"}

	// The event model is consumable: EventType constants + payload aliases exist.
	if len(client.AllEventTypes) == 0 {
		t.Fatal("expected re-exported AllEventTypes")
	}
	seen := map[client.EventType]bool{client.EventToolCompleted: true, client.EventError: true}
	if !seen[client.EventToolCompleted] {
		t.Fatal("EventType usable as a map key")
	}
	_ = client.ToolPayload{}
	_ = client.UsagePayload{}
	_ = client.PermissionPayload{}
}

// TestValidateImagePullPolicy: Create rejects a mis-cased/invalid override rather
// than silently coercing it to the auto policy.
func TestCreateRejectsInvalidImagePullPolicy(t *testing.T) {
	// A Client with an injected-nil backend is fine here: validation happens
	// before any cluster call, and ProjectPath is set so we reach the policy check.
	c := &client.Client{}
	_, err := c.Create(context.Background(), client.CreateOptions{ProjectPath: "/p", ImagePullPolicy: "ifnotpresent"})
	if !errors.Is(err, client.ErrInvalidImagePullPolicy) {
		t.Fatalf("Create with bad pull policy: got %v, want ErrInvalidImagePullPolicy", err)
	}
	if _, err := c.Create(context.Background(), client.CreateOptions{ProjectPath: ""}); !errors.Is(err, client.ErrProjectPathRequired) {
		t.Fatalf("Create with empty path: got %v, want ErrProjectPathRequired", err)
	}
}

// New must reject an invalid reaper pull-policy override at construction (fail
// fast) rather than at first Connect.
func TestNewRejectsInvalidReaperImagePullPolicy(t *testing.T) {
	_, err := client.New(client.WithReaperImagePullPolicy("never"))
	if !errors.Is(err, client.ErrInvalidImagePullPolicy) {
		t.Fatalf("New with bad reaper pull policy: got %v, want ErrInvalidImagePullPolicy", err)
	}
}

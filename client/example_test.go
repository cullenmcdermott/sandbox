package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	agentsfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/cullenmcdermott/sandbox/client"
	"github.com/cullenmcdermott/sandbox/client/cred"
	"github.com/cullenmcdermott/sandbox/internal/k8s"
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

// Example_chat demonstrates the FULL chat loop an external consumer drives — the
// steps the minimal Example above omits: account selection, a connect-progress
// callback, streaming deltas, tool + permission + usage events, interrupt-based
// steering, reattach with replay, and the detach-vs-destroy teardown split. Like
// Example it has no Output directive, so it is compiled (proving every step is
// reachable from outside the module) but not executed (it needs a live cluster).
func Example_chat() {
	ctx := context.Background()

	c, err := client.New(client.WithNamespace("agent-sessions"))
	if err != nil {
		return
	}

	// 1. Account selection. DefaultStore is the multi-account Anthropic credential
	// store; SelectAnthropicAccount resolves that account's credential bytes into
	// the CreateOptions. It is fail-closed: a named-but-unresolvable account is a
	// hard error, never a silent fall-back to the shared cluster Secret. The ""
	// selector picks the stored default (or the sole stored account, or — with no
	// accounts stored — leaves the options on the legacy shared-Secret path).
	opts := client.CreateOptions{ProjectPath: "/work/repo"}
	if store, serr := cred.DefaultStore(); serr == nil {
		if err := opts.SelectAnthropicAccount(store, ""); err != nil {
			return
		}
	}

	sess, err := c.Create(ctx, opts)
	if err != nil {
		return
	}
	// Close only detaches (tears the port-forward down) — the pod keeps running.
	// Deferred as the safety net for every early return; the irreversible Destroy
	// is at the end.
	defer sess.Close()

	// 2. Connect. OnPhase reports coarse progress (cold-pod resume, image pull,
	// file-sync setup) so a splash can show live status; Connection.Warning is a
	// non-fatal advisory (e.g. file sync degraded) to surface rather than discard.
	conn, err := sess.Connect(ctx, client.ConnectOptions{
		OnPhase: func(st client.Stage, detail string) {
			fmt.Printf("connect: %s %s\n", st, detail)
		},
	})
	if err != nil {
		if errors.Is(err, client.ErrSessionGone) {
			return
		}
		return
	}
	if conn.Warning != "" {
		fmt.Println("warning:", conn.Warning)
	}

	// 3. Start a turn and stream its events. Track lastSeq across the loop: it is
	// the replay cursor a later reattach resumes from (step 5).
	turn, err := sess.StartTurn(ctx, client.TurnInput{Prompt: "fix the build"})
	if err != nil {
		return
	}

	var lastSeq uint64
	steer := false // flip to exercise interrupt-based steering mid-turn (step 4)
	events, err := sess.Events(ctx, lastSeq)
	if err != nil {
		return
	}
streamLoop:
	for ev := range events {
		lastSeq = ev.Seq // advance the replay cursor on every persisted event
		switch ev.Type {
		case client.EventMessageDelta:
			// Streaming assistant text: append each delta to the live transcript.
			var m client.MessagePayload
			if json.Unmarshal(ev.Payload, &m) == nil {
				fmt.Print(m.Content)
			}
		case client.EventMessageCompleted:
			// The finalized message block (role + full content).
			var m client.MessagePayload
			if json.Unmarshal(ev.Payload, &m) == nil {
				fmt.Println(m.Role, m.Content)
			}
		case client.EventToolStarted, client.EventToolCompleted, client.EventToolFailed:
			// Tool lifecycle → a tool card. Output is set on completion, Error on
			// failure; ExitCode is the Bash exit status when the tool reports one.
			var t client.ToolPayload
			if json.Unmarshal(ev.Payload, &t) == nil {
				fmt.Println(t.Tool, t.Output, t.Error)
			}
		case client.EventPermissionRequested:
			// The agent is blocked awaiting approval. Since claude-pane-first the
			// decision is made inside the agent's own interactive UI (attach the
			// pane); this event is the attention signal a consumer surfaces —
			// there is no programmatic resolve API.
			var p client.PermissionPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				fmt.Println("attention: permission requested for", p.Tool)
			}
		case client.EventUsageUpdated:
			// Token counts for a ctx% indicator / running cost readout.
			var u client.UsagePayload
			if json.Unmarshal(ev.Payload, &u) == nil {
				fmt.Println(u.InputTokens, u.OutputTokens, u.TotalCostUSD)
			}
		case client.EventTurnCompleted:
			// Normal terminal state — stop reading this turn's stream.
			break streamLoop
		case client.EventTurnFailed, client.EventTurnInterrupted, client.EventError:
			// The other exit conditions: a failed or interrupted turn, or a stream
			// error. All end the loop; a real UI would surface the reason from the
			// matching payload (TurnFailedPayload / TurnInterruptedPayload / ErrorPayload).
			break streamLoop
		}
		// 4. Steering: interrupt the in-flight turn (e.g. the user hit Esc). The
		// runner tears the turn down and emits turn.interrupted, which the case
		// above then uses to end the loop. Gated on a bool so the example compiles
		// and, when run, does not interrupt itself.
		if steer {
			_ = sess.Interrupt(ctx, turn)
		}
	}

	// 5. Reattach. A fresh handle for the same id (Open does no I/O) reconnects and
	// resumes the stream from the saved seq: the runner replays the backlog after
	// lastSeq, then emits the client-internal EventStreamLive marker once caught up
	// to live. That marker is a boundary signal, not data — a UI flips out of its
	// "replaying…" state on it rather than rendering it.
	resumed := c.Open(sess.ID())
	defer resumed.Close()
	if _, err := resumed.Connect(ctx, client.ConnectOptions{}); err != nil {
		return
	}
	replay, err := resumed.Events(ctx, lastSeq)
	if err != nil {
		return
	}
	for ev := range replay {
		if ev.Type == client.EventStreamLive {
			break // caught up to live; the replay backlog is drained
		}
		lastSeq = ev.Seq
	}
	fmt.Println("caught up through seq", lastSeq)

	// 6. Teardown. Close() (deferred above) only detaches — the pod keeps running
	// and can be reattached later. Destroy is the irreversible teardown: it stops
	// file sync, deletes the Sandbox + PVC, and removes local state. There is no
	// undo, so it is the deliberate end of the session's life, not a detach.
	if err := c.Destroy(ctx, sess.ID()); err != nil {
		return
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

// TestValidateAnthropicAuth: Create rejects a mis-spelled auth selector rather
// than silently coercing it to the default OAuth path. (Valid spellings clearing
// the gate is covered by the validateAnthropicAuth unit test in client_test.go —
// routing a valid value through Create here would proceed to the nil backend.)
func TestCreateRejectsInvalidAnthropicAuth(t *testing.T) {
	c := &client.Client{}
	_, err := c.Create(context.Background(), client.CreateOptions{ProjectPath: "/p", AnthropicAuth: "apikey"})
	if !errors.Is(err, client.ErrInvalidAnthropicAuth) {
		t.Fatalf("Create with bad anthropic auth: got %v, want ErrInvalidAnthropicAuth", err)
	}
}

// TestCreateFailsClosedOnAnthropicAccount: Create rejects a named account with
// no resolved credential bytes (fail-closed — never fall back to the shared
// Secret) and credential bytes with no account id, before any cluster call.
func TestCreateFailsClosedOnAnthropicAccount(t *testing.T) {
	c := &client.Client{}
	_, err := c.Create(context.Background(), client.CreateOptions{
		ProjectPath:        "/p",
		AnthropicAccountID: "acct-work",
	})
	if !errors.Is(err, client.ErrAnthropicCredentialMissing) {
		t.Fatalf("account without credential: got %v, want ErrAnthropicCredentialMissing", err)
	}
	_, err = c.Create(context.Background(), client.CreateOptions{
		ProjectPath:         "/p",
		AnthropicCredential: []byte("sk-ant-oat-SECRET"),
	})
	if !errors.Is(err, client.ErrAnthropicAccountRequired) {
		t.Fatalf("credential without account: got %v, want ErrAnthropicAccountRequired", err)
	}
}

// TestCreatePlumbsAnthropicAccount: CreateOptions.AnthropicAccountID /
// AnthropicCredential flow through to the Spec and land in the per-session
// Secret (credential key + account label). Runs against a fake k8s backend so no
// live cluster is needed.
func TestCreatePlumbsAnthropicAccount(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	backend := k8s.NewForClients(agents, core, "agent-sessions")

	c, err := client.New(client.WithBackend(backend), client.WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	const cred = "sk-ant-oat-PLUMBED"
	sess, err := c.Create(context.Background(), client.CreateOptions{
		ProjectPath:         "/work/repo",
		AnthropicAuth:       "oauth",
		AnthropicAccountID:  "acct-work",
		AnthropicCredential: []byte(cred),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	secret, err := core.CoreV1().Secrets("agent-sessions").Get(context.Background(), string(sess.ID())+"-runner", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret.Data["anthropic-credential"]) != cred {
		t.Errorf("credential did not plumb through: got %q, want %q", secret.Data["anthropic-credential"], cred)
	}
	if secret.Labels["sandbox.cullen.dev/anthropic-account"] != "acct-work" {
		t.Errorf("account label did not plumb through: got %q", secret.Labels["sandbox.cullen.dev/anthropic-account"])
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

// TestCreateFailsClosedOnClaudePaneMaterial: a claude-pane session without the
// full credential material is rejected before any cluster call — there is no
// shared-Secret fallback for the pane, and a material-less create would stall
// the pod in CreateContainerConfigError instead of failing actionably.
func TestCreateFailsClosedOnClaudePaneMaterial(t *testing.T) {
	c := &client.Client{}
	_, err := c.Create(context.Background(), client.CreateOptions{
		ProjectPath: "/p",
		Backend:     client.BackendClaudePane,
	})
	if !errors.Is(err, client.ErrClaudePaneCredentialMissing) {
		t.Fatalf("claude-pane without material: got %v, want ErrClaudePaneCredentialMissing", err)
	}
	// Half the material is still missing material.
	_, err = c.Create(context.Background(), client.CreateOptions{
		ProjectPath:           "/p",
		Backend:               client.BackendClaudePane,
		ClaudeCredentialsJSON: []byte(`{"claudeAiOauth":{}}`),
	})
	if !errors.Is(err, client.ErrClaudePaneCredentialMissing) {
		t.Fatalf("claude-pane with partial material: got %v, want ErrClaudePaneCredentialMissing", err)
	}
}

// TestCreatePlumbsClaudePaneMaterial: the full-material documents flow into the
// per-session Secret (both keys + the account label), and the claude-sdk
// token-path key is NOT written for a pane session even if a caller set the
// sdk fields — the pane's only auth path is the materialized credentials file.
func TestCreatePlumbsClaudePaneMaterial(t *testing.T) {
	agents := agentsfake.NewSimpleClientset()
	core := fake.NewSimpleClientset()
	backend := k8s.NewForClients(agents, core, "agent-sessions")

	c, err := client.New(client.WithBackend(backend), client.WithStateDir(t.TempDir()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	sess, err := c.Create(context.Background(), client.CreateOptions{
		ProjectPath:            "/work/repo",
		Backend:                client.BackendClaudePane,
		AnthropicAccountID:     "acct-work",
		ClaudeCredentialsJSON:  []byte(`{"claudeAiOauth":{"accessToken":"AT","refreshToken":"RT"}}`),
		ClaudeOAuthAccountJSON: []byte(`{"oauthAccount":{"emailAddress":"me@example.com"}}`),
		// Stray sdk-path fields must not leak into a pane session's Secret.
		AnthropicAuth:       "oauth",
		AnthropicCredential: []byte("sk-ant-oat-STRAY"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	secret, err := core.CoreV1().Secrets("agent-sessions").Get(context.Background(), string(sess.ID())+"-runner", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret.Data["claude-credentials-json"]) == "" {
		t.Error("claude-credentials-json key missing from the per-session Secret")
	}
	if string(secret.Data["claude-oauth-account-json"]) == "" {
		t.Error("claude-oauth-account-json key missing from the per-session Secret")
	}
	if secret.Labels["sandbox.cullen.dev/anthropic-account"] != "acct-work" {
		t.Errorf("account label did not plumb through: got %q", secret.Labels["sandbox.cullen.dev/anthropic-account"])
	}
	if _, ok := secret.Data["anthropic-credential"]; ok {
		t.Error("claude-sdk token key written for a claude-pane session (the pane must never carry the env-token path)")
	}
}

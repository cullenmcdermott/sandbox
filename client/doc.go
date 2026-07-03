// Package client is the public Go API for driving sandbox sessions: create and
// connect to Kubernetes-backed agent session pods, start turns, stream the
// normalized event model, and manage Mutagen file sync — all without the
// interactive TUI.
//
// It is a thin, curated façade over the unexported engines (internal/k8s,
// internal/runner, internal/session, internal/sync, internal/index). The
// normalized session model is re-exported here as type aliases (Spec, State,
// Event, TurnInput, …) so callers and the engines share the exact same types.
//
// The sandbox CLI and TUI consume this same package, so the public API is
// exercised by the project's own use rather than living on a parallel path.
//
// The client/cred subpackage manages stored Anthropic accounts (claude.ai
// subscription or Console API key); CreateOptions.UseAnthropicAccount and
// SelectAnthropicAccount thread one into session creation, fail closed (see
// account.go).
//
// Typical use:
//
//	c, err := client.New(client.WithContext("my-cluster"), client.WithNamespace("agent-sessions"))
//	if err != nil { ... }
//	sess, err := c.Create(ctx, client.CreateOptions{ProjectPath: "/work/repo"})
//	if err != nil { ... }
//	if _, err := sess.Connect(ctx, client.ConnectOptions{}); err != nil { ... }
//	defer sess.Close()
//	if _, err := sess.StartTurn(ctx, client.TurnInput{Prompt: "fix the build"}); err != nil { ... }
//	events, err := sess.Events(ctx, 0)
//	if err != nil { ... }
//	for ev := range events {
//		switch ev.Type {
//		case client.EventMessageCompleted:
//			var m client.MessagePayload
//			_ = json.Unmarshal(ev.Payload, &m)
//		case client.EventTurnCompleted:
//			return
//		}
//	}
package client

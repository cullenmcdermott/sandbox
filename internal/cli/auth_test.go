package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/cred"
)

// stripANSI removes color escape sequences so assertions match the plain text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestRenderAuthStatus_Unreachable(t *testing.T) {
	var buf bytes.Buffer
	cs := clusterStatus{reachable: false, namespace: "agent-sessions", detail: "dial tcp: i/o timeout"}
	agents := []cred.Status{
		{Name: "claude", Configured: true, Method: cred.MethodOAuth, Detail: "CLAUDE_CODE_OAUTH_TOKEN (setup-token)"},
		{Name: "codex", Configured: true, Method: cred.MethodOAuth, Detail: "ChatGPT OAuth; expires in 7d"},
		{Name: "opencode", Configured: true, Method: cred.MethodAPIKey, Detail: "1/3 providers configured", Sub: []cred.Status{
			{Name: "opencode/anthropic", Configured: true, Method: cred.MethodAPIKey, Detail: "ANTHROPIC_API_KEY set"},
			{Name: "opencode/openai", Method: cred.MethodNone, Detail: "OPENAI_API_KEY unset"},
		}},
	}
	renderAuthStatus(&buf, cs, agents)
	out := stripANSI(buf.String())

	for _, want := range []string{
		"auth status",
		"kubernetes", "unreachable", "dial tcp: i/o timeout",
		"claude", "oauth-subscription", "setup-token",
		"codex", "expires in 7d",
		"opencode", "1/3 providers configured",
		"anthropic", "ANTHROPIC_API_KEY set",
		"openai", "OPENAI_API_KEY unset",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderAuthStatus_Reachable(t *testing.T) {
	var buf bytes.Buffer
	cs := clusterStatus{reachable: true, namespace: "agent-sessions", host: "https://k8s.example:6443"}
	renderAuthStatus(&buf, cs, nil)
	out := stripANSI(buf.String())
	for _, want := range []string{"kubernetes", "reachable", "ns: agent-sessions", "https://k8s.example:6443"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

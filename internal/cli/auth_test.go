package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/cullenmcdermott/sandbox/client/cred"
	"github.com/cullenmcdermott/sandbox/internal/authstatus"
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
	agents := []authstatus.Status{
		{Name: "claude", Configured: true, Method: authstatus.MethodOAuth, Detail: "CLAUDE_CODE_OAUTH_TOKEN (setup-token)"},
		{Name: "codex", Configured: true, Method: authstatus.MethodOAuth, Detail: "ChatGPT OAuth; expires in 7d"},
		{Name: "opencode", Configured: true, Method: authstatus.MethodAPIKey, Detail: "1/3 providers configured", Sub: []authstatus.Status{
			{Name: "opencode/anthropic", Configured: true, Method: authstatus.MethodAPIKey, Detail: "ANTHROPIC_API_KEY set"},
			{Name: "opencode/openai", Method: authstatus.MethodNone, Detail: "OPENAI_API_KEY unset"},
		}},
	}
	renderAuthStatus(&buf, cs, agents, nil, "", nil)
	out := stripANSI(buf.String())

	for _, want := range []string{
		"auth status",
		"kubernetes", "unreachable", "dial tcp: i/o timeout",
		"claude", "oauth-subscription", "setup-token",
		"codex", "expires in 7d",
		"opencode", "1/3 providers configured",
		"anthropic", "ANTHROPIC_API_KEY set",
		"openai", "OPENAI_API_KEY unset",
		"anthropic accounts", "none stored",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderAuthStatus_Reachable(t *testing.T) {
	var buf bytes.Buffer
	cs := clusterStatus{reachable: true, namespace: "agent-sessions", host: "https://k8s.example:6443"}
	renderAuthStatus(&buf, cs, nil, nil, "", nil)
	out := stripANSI(buf.String())
	for _, want := range []string{"kubernetes", "reachable", "ns: agent-sessions", "https://k8s.example:6443"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

// TestRenderAuthStatus_Accounts: stored accounts are enumerated (label, type,
// default marker) and a store error degrades to a warning line, not a crash.
func TestRenderAuthStatus_Accounts(t *testing.T) {
	var buf bytes.Buffer
	accounts := []cred.Account{
		{ID: "acct-aaaa", Label: "personal", Type: cred.AccountSubscription},
		{ID: "acct-bbbb", Label: "work", Type: cred.AccountConsole},
	}
	renderAuthStatus(&buf, clusterStatus{}, nil, accounts, "acct-bbbb", nil)
	out := stripANSI(buf.String())
	for _, want := range []string{
		"anthropic accounts",
		"personal", "subscription",
		"work", "console", "(default)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Count(out, "(default)") != 1 {
		t.Errorf("want exactly one default marker\n---\n%s", out)
	}

	buf.Reset()
	renderAuthStatus(&buf, clusterStatus{}, nil, nil, "", errors.New("manifest corrupt"))
	out = stripANSI(buf.String())
	if !strings.Contains(out, "account store unreadable: manifest corrupt") {
		t.Errorf("store error not surfaced\n---\n%s", out)
	}
}

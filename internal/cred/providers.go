package cred

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func envOr(e Env) Env {
	if e == nil {
		return os.Getenv
	}
	return e
}

// ClaudeProvider reports Claude's auth: a setup-token OAuth (subscription) via
// CLAUDE_CODE_OAUTH_TOKEN, or an API key via ANTHROPIC_API_KEY.
type ClaudeProvider struct{ Env Env }

// Name implements Provider.
func (ClaudeProvider) Name() string { return "claude" }

// Status implements Provider.
func (p ClaudeProvider) Status(_ context.Context) Status {
	get := envOr(p.Env)
	s := Status{Name: "claude"}
	switch {
	case get("CLAUDE_CODE_OAUTH_TOKEN") != "":
		s.Configured, s.Method, s.Detail = true, MethodOAuth, "CLAUDE_CODE_OAUTH_TOKEN (setup-token)"
	case get("ANTHROPIC_API_KEY") != "":
		s.Configured, s.Method, s.Detail = true, MethodAPIKey, "ANTHROPIC_API_KEY"
	default:
		s.Method, s.Detail = MethodNone, "no CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY"
	}
	return s
}

// CodexProvider reports Codex's auth from ~/.codex/auth.json (ChatGPT OAuth or
// API key) with a fallback to OPENAI_API_KEY, and surfaces the access-token
// expiry when present. Now is injectable for tests (defaults to time.Now).
//
// Note: plan eligibility (free vs paid) and live rate-limit state require the
// app-server and are NOT checked here — that's the opt-in live `--check` path.
type CodexProvider struct {
	Env  Env
	Home string
	Now  func() time.Time
}

// Name implements Provider.
func (CodexProvider) Name() string { return "codex" }

type codexAuthFile struct {
	AuthMode string `json:"auth_mode"`
	Tokens   struct {
		AccessToken string `json:"access_token"`
	} `json:"tokens"`
}

// Status implements Provider.
func (p CodexProvider) Status(_ context.Context) Status {
	get := envOr(p.Env)
	now := p.Now
	if now == nil {
		now = time.Now
	}
	s := Status{Name: "codex"}

	path := filepath.Join(p.Home, ".codex", "auth.json")
	if data, err := os.ReadFile(path); err == nil {
		var af codexAuthFile
		if json.Unmarshal(data, &af) == nil && af.AuthMode != "" {
			switch af.AuthMode {
			case "chatgpt", "chatgptAuthTokens":
				s.Configured, s.Method, s.Detail = true, MethodOAuth, "ChatGPT OAuth (~/.codex/auth.json)"
			case "apikey":
				s.Configured, s.Method, s.Detail = true, MethodAPIKey, "API key (~/.codex/auth.json)"
			default:
				s.Configured, s.Method, s.Detail = true, MethodUnknown, "auth.json auth_mode="+af.AuthMode
			}
			if exp, ok := jwtExp(af.Tokens.AccessToken); ok {
				s.Detail += "; " + expiryNote(exp, now())
			}
			return s
		}
	}
	if get("OPENAI_API_KEY") != "" {
		s.Configured, s.Method, s.Detail = true, MethodAPIKey, "OPENAI_API_KEY"
		return s
	}
	s.Method, s.Detail = MethodNone, "no ~/.codex/auth.json or OPENAI_API_KEY"
	return s
}

// expiryNote renders a coarse, secret-free expiry hint.
func expiryNote(exp, now time.Time) string {
	d := exp.Sub(now)
	if d <= 0 {
		return "token EXPIRED"
	}
	if days := int(d.Hours() / 24); days >= 1 {
		return fmt.Sprintf("expires in %dd", days)
	}
	return fmt.Sprintf("expires in %dh", int(d.Hours()))
}

// opencodeProviders maps an env var to a provider id, matching the runner's
// buildOpencodeConfig (anthropic / openai / opencode Zen).
var opencodeProviders = []struct{ env, id string }{
	{"ANTHROPIC_API_KEY", "anthropic"},
	{"OPENAI_API_KEY", "openai"},
	{"OPENCODE_API_KEY", "opencode-zen"},
}

// OpenCodeProvider reports opencode auth per configured provider key — opencode
// is multi-provider, so its Status carries one Sub entry per provider.
type OpenCodeProvider struct{ Env Env }

// Name implements Provider.
func (OpenCodeProvider) Name() string { return "opencode" }

// Status implements Provider.
func (p OpenCodeProvider) Status(_ context.Context) Status {
	get := envOr(p.Env)
	s := Status{Name: "opencode"}
	n := 0
	for _, pr := range opencodeProviders {
		sub := Status{Name: "opencode/" + pr.id}
		if get(pr.env) != "" {
			sub.Configured, sub.Method, sub.Detail = true, MethodAPIKey, pr.env+" set"
			n++
		} else {
			sub.Method, sub.Detail = MethodNone, pr.env+" unset"
		}
		s.Sub = append(s.Sub, sub)
	}
	s.Configured = n > 0
	if n > 0 {
		s.Method = MethodAPIKey
		s.Detail = fmt.Sprintf("%d/%d providers configured", n, len(opencodeProviders))
	} else {
		s.Method = MethodNone
		s.Detail = "no provider keys set"
	}
	return s
}

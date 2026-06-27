// Package cred reports and (incrementally) manages the credentials the sandbox
// CLI uses to authenticate each supported agent backend.
//
// Today it provides the READ side: a cheap, offline validation of whether each
// agent's auth is configured and, for Claude/Codex, whether that auth is an API
// key or a subscription OAuth token. This powers `sandbox auth status` and a
// red/green preflight surface. The WRITE side — a local Keychain-backed store
// that seeds and reconciles the per-session cluster Secret, with renewal prompts
// — layers on top of these same Provider abstractions later (see
// docs/codex-integration-plan.md "Authentication").
//
// Checks never read or emit token material — only derived facts (configured,
// method, expiry).
package cred

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// Method classifies how an agent is authenticated.
type Method string

const (
	// MethodNone means no usable credential is configured.
	MethodNone Method = "none"
	// MethodAPIKey means a long-lived API key (billed as API credits).
	MethodAPIKey Method = "api-key"
	// MethodOAuth means a subscription OAuth/setup token (uses the plan).
	MethodOAuth Method = "oauth-subscription"
	// MethodUnknown means a credential is present but its kind is unrecognized.
	MethodUnknown Method = "unknown"
)

// Status is the auth state of one agent, or one provider within an agent.
type Status struct {
	Name       string   // "claude", "codex", "opencode", or "opencode/anthropic"
	Configured bool     // is any usable credential present?
	Method     Method   // how it authenticates (for leaf statuses)
	Detail     string   // human note: source env var, token expiry, etc.
	Sub        []Status // nested per-provider statuses (opencode)
}

// Level summarizes a Status for rendering: OK (green), Warn (yellow — present
// but stale/unknown), or Bad (red — not configured).
type Level int

const (
	LevelOK Level = iota
	LevelWarn
	LevelBad
)

// Level derives the render level from the status fields.
func (s Status) Level() Level {
	if !s.Configured || s.Method == MethodNone {
		return LevelBad
	}
	if s.Method == MethodUnknown || strings.Contains(s.Detail, "EXPIRED") {
		return LevelWarn
	}
	return LevelOK
}

// Provider reports the auth status for one agent backend. Status is cheap and
// offline; it must not make network calls or print secrets.
type Provider interface {
	Name() string
	Status(ctx context.Context) Status
}

// Env abstracts environment lookup so providers are testable.
type Env func(string) string

// Report runs every provider and returns their statuses in order.
func Report(ctx context.Context, providers ...Provider) []Status {
	out := make([]Status, 0, len(providers))
	for _, p := range providers {
		out = append(out, p.Status(ctx))
	}
	return out
}

// DefaultProviders is the standard set: Claude, Codex, OpenCode. home is the
// user's home dir (for Codex's ~/.codex/auth.json); env is the environment
// lookup (os.Getenv in production).
func DefaultProviders(env Env, home string) []Provider {
	return []Provider{
		ClaudeProvider{Env: env},
		CodexProvider{Env: env, Home: home},
		OpenCodeProvider{Env: env},
	}
}

// jwtExp extracts the `exp` claim from a JWT without verifying it. It never
// returns or logs the token. ok=false if the token isn't a decodable JWT with an
// exp claim.
func jwtExp(token string) (exp time.Time, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return time.Time{}, false
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

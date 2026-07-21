package authstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func envOr(e Env) Env {
	if e == nil {
		return os.Getenv
	}
	return e
}

// claudeKeychainService / claudeSecurityBin mirror client/cred's
// systemKeychainService (system.go) and securityBin (keychain.go): the
// generic-password service name Claude Code itself uses for its login on
// macOS, and the Security CLI that reads it. Duplicated here because cred's
// resolution is unexported and importing its secret-READING path into a
// presence-only status probe would be the wrong dependency; keep in sync.
const (
	claudeKeychainService = "Claude Code-credentials"
	claudeSecurityBin     = "/usr/bin/security"
)

// ClaudeProvider reports Claude's auth. The PRIMARY source is the host's own
// Claude Code login — what `sandbox claude` provisions pane sessions from
// (client/account.go SelectClaudePaneMaterial → cred.SystemMaterial): the
// "Claude Code-credentials" Keychain item on darwin, else
// $CLAUDE_CONFIG_DIR/.credentials.json (default ~/.claude/.credentials.json).
// The env vars are SECONDARY signals — a setup-token OAuth via
// CLAUDE_CODE_OAUTH_TOKEN or an API key via ANTHROPIC_API_KEY serve
// headless/SDK consumers only and cannot drive the interactive pane, so
// without the host login they render Degraded (yellow), not green.
type ClaudeProvider struct {
	Env  Env
	Home string
	// GOOS selects the platform probe; "" means runtime.GOOS. Injectable so
	// tests exercise the Keychain and credentials-file branches
	// deterministically.
	GOOS string
	// Keychain probes for the Claude Code Keychain item: present is the
	// answer, ok=false means the probe is unavailable on this host (no
	// security binary) and the credentials-file fallback applies. nil uses
	// the real metadata-only probe (keychainLoginPresent). Tests MUST inject
	// it whenever GOOS is "darwin" so they never shell out to `security`.
	Keychain func() (present, ok bool)
}

// Name implements Provider.
func (ClaudeProvider) Name() string { return "claude" }

// Status implements Provider.
func (p ClaudeProvider) Status(_ context.Context) Status {
	get := envOr(p.Env)
	s := Status{Name: "claude"}
	if present, source := p.systemLoginPresent(); present {
		s.Configured, s.Method = true, MethodOAuth
		s.Detail = "host Claude Code login (" + source + ")"
		return s
	}
	switch {
	case get("CLAUDE_CODE_OAUTH_TOKEN") != "":
		s.Configured, s.Method, s.Degraded = true, MethodOAuth, true
		s.Detail = "CLAUDE_CODE_OAUTH_TOKEN (setup-token) — headless/SDK only; `sandbox claude` needs the host Claude Code login"
	case get("ANTHROPIC_API_KEY") != "":
		s.Configured, s.Method, s.Degraded = true, MethodAPIKey, true
		s.Detail = "ANTHROPIC_API_KEY — headless/SDK only; `sandbox claude` needs the host Claude Code login"
	default:
		s.Method, s.Detail = MethodNone, "no host Claude Code login — run `claude` and log in (Max mode)"
	}
	return s
}

// systemLoginPresent reports PRESENCE of the host's Claude Code login without
// reading a single credential byte, plus a short source label for the status
// detail. It mirrors cred's source resolution (client/cred/system.go,
// systemCredentialBytes + systemPaths) — keep the two in sync: on darwin the
// Keychain answer is final whenever the probe is available (no
// credentials-file fallback on a miss, exactly like cred.SystemMaterial); the
// credentials file applies elsewhere, and CLAUDE_CONFIG_DIR overrides the
// ~/.claude default exclusively (no home fallback when it is set).
func (p ClaudeProvider) systemLoginPresent() (present bool, source string) {
	goos := p.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "darwin" {
		probe := p.Keychain
		if probe == nil {
			probe = keychainLoginPresent
		}
		if present, ok := probe(); ok {
			return present, "Keychain"
		}
		// Probe unavailable (no security binary): fall through to the
		// credentials file, as cred.SystemMaterial does.
	}
	dir := envOr(p.Env)("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(p.Home, ".claude")
	}
	if _, err := os.Stat(filepath.Join(dir, ".credentials.json")); err == nil {
		return true, ".credentials.json"
	}
	return false, ""
}

// keychainLoginPresent checks the Keychain item's existence via the
// metadata-only form of `security find-generic-password` — deliberately
// WITHOUT -w, so the credential is never read; the exit code is the whole
// signal and the (attribute-only) output is discarded. ok=false when the
// security binary itself is missing.
func keychainLoginPresent() (present, ok bool) {
	if _, err := os.Stat(claudeSecurityBin); err != nil {
		return false, false
	}
	err := exec.Command(claudeSecurityBin, "find-generic-password", "-s", claudeKeychainService).Run()
	return err == nil, true
}

// SystemLogin reports whether the host's own Claude Code login — the
// claude-pane primary credential source — is present, plus a short source
// label ("Keychain" / ".credentials.json"). Presence-only: no secret bytes
// are read. env/home are as in DefaultProviders. Shared with `sandbox doctor`
// so both surfaces agree on what "claude auth" means.
func SystemLogin(env Env, home string) (present bool, source string) {
	return ClaudeProvider{Env: env, Home: home}.systemLoginPresent()
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
				s.Expired = !exp.After(now())
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

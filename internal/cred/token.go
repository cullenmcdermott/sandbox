package cred

import (
	"errors"
	"strings"
)

// Anthropic credential prefixes. These are loose matchers: Anthropic has
// changed key formats before, so validation checks the prefix, charset, and a
// plausible length rather than an exact grammar.
const (
	// setupTokenPrefix marks a `claude setup-token` OAuth credential.
	setupTokenPrefix = "sk-ant-oat"
	// consoleKeyPrefix marks any Anthropic Console credential family
	// (sk-ant-api…). setupTokenPrefix is a subset and is rejected as a console
	// key.
	consoleKeyPrefix = "sk-ant-"
)

// Plausible credential length bounds. Wide enough to survive format changes,
// tight enough to reject obvious garbage. Bounds count the whole token.
const (
	minTokenLen = 20
	maxTokenLen = 500
)

// Token-material errors never echo the offending value.
var (
	// ErrNoSetupToken means no setup-token line was found in the output.
	ErrNoSetupToken = errors.New("cred: no sk-ant-oat setup token found in output")
	// ErrMalformedToken means a candidate had the right prefix but failed shape
	// validation (charset or length).
	ErrMalformedToken = errors.New("cred: setup token is malformed")
	// ErrInvalidConsoleKey means a console API key failed the loose format check.
	ErrInvalidConsoleKey = errors.New("cred: invalid Anthropic console API key")
)

// isTokenCharset reports whether s is entirely within the credential body
// charset [A-Za-z0-9_-]. (The "sk-ant-…" prefix's hyphens are within this set.)
func isTokenCharset(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// ParseSetupToken extracts the OAuth setup token from captured `claude
// setup-token` stdout. It returns the last line whose trimmed content is a
// valid setup token (prefix sk-ant-oat, charset [A-Za-z0-9_-], plausible
// length). It never includes the raw output in its error, so callers can log
// the error safely.
func ParseSetupToken(output string) (string, error) {
	var found string
	sawPrefix := false
	for _, line := range strings.Split(output, "\n") {
		tok := strings.TrimSpace(line)
		if !strings.HasPrefix(tok, setupTokenPrefix) {
			continue
		}
		sawPrefix = true
		if len(tok) < minTokenLen || len(tok) > maxTokenLen || !isTokenCharset(tok) {
			continue
		}
		found = tok // last valid wins
	}
	if found == "" {
		if sawPrefix {
			return "", ErrMalformedToken
		}
		return "", ErrNoSetupToken
	}
	return found, nil
}

// ValidateConsoleKey performs a loose format check on a pasted Anthropic Console
// API key and returns the normalized (whitespace-trimmed) key — callers MUST
// store the returned value, not their original input, or a pasted trailing
// newline persists into the credential. The key must start with sk-ant-, must
// NOT be an OAuth setup token (sk-ant-oat…), and must be a plausible length
// over the token charset. Errors never echo the key. The check is intentionally
// loose because Anthropic changes key formats; live validation is out of scope
// for v1.
func ValidateConsoleKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if !strings.HasPrefix(key, consoleKeyPrefix) ||
		strings.HasPrefix(key, setupTokenPrefix) ||
		len(key) < minTokenLen || len(key) > maxTokenLen ||
		!isTokenCharset(key) {
		return "", ErrInvalidConsoleKey
	}
	return key, nil
}

// AuthForType maps an AccountType to the Spec.AnthropicAuth spelling the k8s
// backend expects (see internal/session/types.go): subscription→"oauth"
// (CLAUDE_CODE_OAUTH_TOKEN), console→"api-key" (ANTHROPIC_API_KEY). These
// spellings are a consumer contract. An unknown type returns
// ErrInvalidAccountType — never a silent "" (the k8s layer reads "" as the
// oauth default, which would misroute a console key).
func AuthForType(t AccountType) (string, error) {
	switch t {
	case AccountSubscription:
		return "oauth", nil
	case AccountConsole:
		return "api-key", nil
	default:
		return "", ErrInvalidAccountType
	}
}

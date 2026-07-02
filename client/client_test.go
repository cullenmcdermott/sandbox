package client

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateAnthropicAuth: the valid selectors ("", "oauth", "api-key") clear
// the gate; anything else (including a case/spacing typo) is rejected with
// ErrInvalidAnthropicAuth rather than silently coercing to the default OAuth path.
func TestValidateAnthropicAuth(t *testing.T) {
	for _, ok := range []string{"", "oauth", "api-key"} {
		if err := validateAnthropicAuth(ok); err != nil {
			t.Errorf("validateAnthropicAuth(%q): unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{"apikey", "OAuth", "api_key", "console", "api-key "} {
		if err := validateAnthropicAuth(bad); !errors.Is(err, ErrInvalidAnthropicAuth) {
			t.Errorf("validateAnthropicAuth(%q): got %v, want ErrInvalidAnthropicAuth", bad, err)
		}
	}
}

// TestSanitizeLabel checks that sanitizeLabel produces values safe to embed in a
// Kubernetes resource name: uppercase is lowercased, [a-z0-9-] passes through,
// and every other rune (including multi-byte ones) is replaced by '-'.
func TestSanitizeLabel(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"claude-sdk", "claude-sdk"},
		{"OpenCode", "opencode"},
		{"Claude_SDK", "claude-sdk"},
		{"a.b/c", "a-b-c"},
		{"My Project!", "my-project-"},
		{"abc123", "abc123"},
		{"--keep--", "--keep--"},
		{"naïve", "na-ve"}, // the loop ranges over runes, so 'ï' -> one '-'
	}
	for _, c := range cases {
		if got := sanitizeLabel(c.in); got != c.want {
			t.Errorf("sanitizeLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSanitizeLabelOnlyAllowedRunes is a property-style guard: every byte of the
// output must be in [a-z0-9-], regardless of input.
func TestSanitizeLabelOnlyAllowedRunes(t *testing.T) {
	inputs := []string{"ABC", "x_y_z", "🚀rocket", "tab\tspace", "MiXeD-123/v2"}
	for _, in := range inputs {
		out := sanitizeLabel(in)
		for i := 0; i < len(out); i++ {
			b := out[i]
			ok := (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-'
			if !ok {
				t.Errorf("sanitizeLabel(%q) = %q has disallowed byte %q at %d", in, out, b, i)
			}
		}
	}
}

// TestNewIDFormat checks that NewID yields a valid DNS-label-shaped id with the
// backend prefix and a stable project-path hash, and that two calls for the same
// inputs differ (the random suffix guarantees uniqueness).
func TestNewIDFormat(t *testing.T) {
	id1, err := NewID("claude-sdk", "/work/repo")
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	s := string(id1)
	if !strings.HasPrefix(s, "claude-sdk-") {
		t.Errorf("NewID = %q, want claude-sdk- prefix", s)
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		ok := (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-'
		if !ok {
			t.Errorf("NewID = %q has disallowed byte %q at %d", s, b, i)
		}
	}

	id2, err := NewID("claude-sdk", "/work/repo")
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if id1 == id2 {
		t.Errorf("NewID returned the same id twice (%q); the random suffix should make them unique", id1)
	}

	// The project-path hash segment must be stable across calls for the same path.
	seg1 := strings.Split(s, "-")
	seg2 := strings.Split(string(id2), "-")
	if len(seg1) < 3 || len(seg2) < 3 {
		t.Fatalf("NewID = %q / %q, want at least 3 dash-separated segments", id1, id2)
	}
	// segments: [claude sdk <pathhash> <rand>] — the backend "claude-sdk" has a
	// dash, so the path hash is the second-to-last segment.
	if seg1[len(seg1)-2] != seg2[len(seg2)-2] {
		t.Errorf("project-path hash differs across calls: %q vs %q", seg1[len(seg1)-2], seg2[len(seg2)-2])
	}
}

// NewID promises a valid Kubernetes DNS label for ANY backend input: no leading
// or trailing dash, non-empty, <= 63 chars, [a-z0-9-] only.
func TestNewIDAlwaysValidDNSLabel(t *testing.T) {
	backends := []string{
		"",                       // sanitizes to nothing -> "session" fallback
		"---",                    // all dashes -> "session" fallback
		"日本語",                    // every rune replaced -> trimmed away
		"-leading-and-trailing-", // dashes at the edges
		strings.Repeat("verylongbackendname", 10), // must truncate to fit 63
		"UPPER.case_Backend",
	}
	for _, b := range backends {
		id, err := NewID(b, "/work/repo")
		if err != nil {
			t.Fatalf("NewID(%q): %v", b, err)
		}
		s := string(id)
		if s == "" || len(s) > 63 {
			t.Errorf("NewID(%q) = %q: length %d, want 1..63", b, s, len(s))
		}
		if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
			t.Errorf("NewID(%q) = %q: leading/trailing dash", b, s)
		}
		for i := 0; i < len(s); i++ {
			c := s[i]
			valid := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
			if !valid {
				t.Errorf("NewID(%q) = %q: disallowed byte %q at %d", b, s, c, i)
			}
		}
	}
}

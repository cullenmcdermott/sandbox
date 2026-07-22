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

// TestValidateOpencodeProvider: the valid selectors ("", the three
// session.OpencodeProvider* spellings) clear the gate; anything else — notably
// the tempting shorthand "zen" — is rejected with ErrInvalidOpencodeProvider
// rather than silently falling through to the backend's Anthropic default
// (which would inject a DIFFERENT provider's credential than intended).
func TestValidateOpencodeProvider(t *testing.T) {
	for _, ok := range []string{"", "anthropic", "openai", "opencode-zen"} {
		if err := validateOpencodeProvider(ok); err != nil {
			t.Errorf("validateOpencodeProvider(%q): unexpected error %v", ok, err)
		}
	}
	for _, bad := range []string{"zen", "Anthropic", "open-ai", "opencode_zen", "opencode-zen "} {
		if err := validateOpencodeProvider(bad); !errors.Is(err, ErrInvalidOpencodeProvider) {
			t.Errorf("validateOpencodeProvider(%q): got %v, want ErrInvalidOpencodeProvider", bad, err)
		}
	}
}

// TestValidateAnthropicAccount covers the fail-closed account/credential
// contract: a named account with empty bytes errors (ErrAnthropicCredentialMissing),
// bytes with no account id error (ErrAnthropicAccountRequired), an id that is
// not a valid Kubernetes label value errors (ErrInvalidAnthropicAccountID —
// the id labels the per-session Secret, so it must fail fast here rather than
// as an apiserver Invalid mid-create), and the both-set / both-empty cases
// clear the gate. No error path echoes the credential bytes.
func TestValidateAnthropicAccount(t *testing.T) {
	cred := []byte("sk-ant-oat-SECRET")
	cases := []struct {
		name    string
		id      string
		cred    []byte
		wantErr error
	}{
		{"both empty (shared fallback)", "", nil, nil},
		{"account with credential", "acct-work", cred, nil},
		{"account without credential", "acct-work", nil, ErrAnthropicCredentialMissing},
		{"account with empty credential", "acct-work", []byte{}, ErrAnthropicCredentialMissing},
		{"credential without account", "", cred, ErrAnthropicAccountRequired},
		{"id with slash", "acct/work", cred, ErrInvalidAnthropicAccountID},
		{"id with space", "acct work", cred, ErrInvalidAnthropicAccountID},
		{"id too long for a label", strings.Repeat("a", 64), cred, ErrInvalidAnthropicAccountID},
		{"id with leading dash", "-acct", cred, ErrInvalidAnthropicAccountID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateAnthropicAccount(c.id, c.cred)
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("got %v, want %v", err, c.wantErr)
			}
			if strings.Contains(err.Error(), string(cred)) {
				t.Fatalf("error message leaked the credential bytes: %v", err)
			}
		})
	}
}

// TestValidateCodexAccount mirrors TestValidateAnthropicAccount for the codex
// fail-closed contract: a named account with empty auth.json bytes errors
// (ErrCodexCredentialMissing), bytes with no account id error
// (ErrCodexAccountRequired), a non-label-safe id errors (ErrInvalidCodexAccountID),
// and the both-set / both-empty cases clear the gate. No error path echoes the
// credential bytes.
func TestValidateCodexAccount(t *testing.T) {
	auth := []byte(`{"tokens":{"access_token":"CODEX-SECRET"}}`)
	cases := []struct {
		name    string
		id      string
		auth    []byte
		wantErr error
	}{
		{"both empty (shared fallback)", "", nil, nil},
		{"account with credential", "acct-chatgpt", auth, nil},
		{"account without credential", "acct-chatgpt", nil, ErrCodexCredentialMissing},
		{"account with empty credential", "acct-chatgpt", []byte{}, ErrCodexCredentialMissing},
		{"credential without account", "", auth, ErrCodexAccountRequired},
		{"id with slash", "acct/gpt", auth, ErrInvalidCodexAccountID},
		{"id with space", "acct gpt", auth, ErrInvalidCodexAccountID},
		{"id too long for a label", strings.Repeat("a", 64), auth, ErrInvalidCodexAccountID},
		{"id with leading dash", "-acct", auth, ErrInvalidCodexAccountID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCodexAccount(c.id, c.auth)
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("got %v, want %v", err, c.wantErr)
			}
			if strings.Contains(err.Error(), string(auth)) {
				t.Fatalf("error message leaked the credential bytes: %v", err)
			}
		})
	}
}

// TestValidateExtraEnv covers the generic env-injection gate (part B): valid
// names in either map clear it; a reserved name (the SANDBOX_ prefix, RUNNER_TOKEN,
// or a credential var) is ErrReservedEnvName; a malformed name is
// ErrInvalidExtraEnvName; a name in BOTH maps is ErrDuplicateExtraEnv; and
// oversized secret bytes are ErrExtraSecretEnvTooLarge. No error path echoes a
// secret value.
func TestValidateExtraEnv(t *testing.T) {
	const secretVal = "glpat-SUPER-SECRET-VALUE"
	cases := []struct {
		name    string
		plain   map[string]string
		secret  map[string][]byte
		wantErr error
	}{
		{"both nil", nil, nil, nil},
		{
			"valid plain + secret",
			map[string]string{"TOOL_ENDPOINT": "https://tool.internal"},
			map[string][]byte{"GITLAB_TOKEN": []byte(secretVal)},
			nil,
		},
		{"reserved SANDBOX_ prefix (plain)", map[string]string{"SANDBOX_X": "v"}, nil, ErrReservedEnvName},
		{"reserved RUNNER_TOKEN (secret)", nil, map[string][]byte{"RUNNER_TOKEN": []byte(secretVal)}, ErrReservedEnvName},
		{"reserved credential var (secret)", nil, map[string][]byte{"ANTHROPIC_API_KEY": []byte(secretVal)}, ErrReservedEnvName},
		{"reserved HOME (plain)", map[string]string{"HOME": "/x"}, nil, ErrReservedEnvName},
		{"invalid name shape: leading digit", map[string]string{"1BAD": "v"}, nil, ErrInvalidExtraEnvName},
		{"invalid name shape: dash", map[string]string{"BAD-NAME": "v"}, nil, ErrInvalidExtraEnvName},
		{"invalid name shape: empty", map[string]string{"": "v"}, nil, ErrInvalidExtraEnvName},
		{
			"duplicate across maps",
			map[string]string{"SHARED": "v"},
			map[string][]byte{"SHARED": []byte(secretVal)},
			ErrDuplicateExtraEnv,
		},
		{
			"oversize secret bytes",
			nil,
			map[string][]byte{"BIG": make([]byte, maxExtraSecretEnvBytes+1)},
			ErrExtraSecretEnvTooLarge,
		},
		{
			"at the size cap is allowed",
			nil,
			map[string][]byte{"BIG": make([]byte, maxExtraSecretEnvBytes)},
			nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateExtraEnv(c.plain, c.secret)
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("got %v, want %v", err, c.wantErr)
			}
			if strings.Contains(err.Error(), secretVal) {
				t.Fatalf("error message leaked a secret value: %v", err)
			}
		})
	}
}

// TestValidateBootstrapFiles covers the fail-closed bootstrap-file gate (part A):
// absolute and "~/"-relative paths inside the pod HOME or /session/state clear it;
// a relative path or a malformed shape is ErrInvalidBootstrapPath; a path that
// escapes the allowed roots — including a ".." traversal or a workspace path — is
// ErrBootstrapPathOutsideRoots; two files resolving to one path is
// ErrDuplicateBootstrapPath; and oversized summed content is
// ErrBootstrapFilesTooLarge. No error path echoes the file content.
func TestValidateBootstrapFiles(t *testing.T) {
	const secretContent = "SUPER-SECRET-FILE-BODY"
	cases := []struct {
		name    string
		files   []BootstrapFile
		wantErr error
	}{
		{"nil", nil, nil},
		{"absolute in HOME", []BootstrapFile{{Path: "/root/.claude/CLAUDE.md", Content: []byte("x")}}, nil},
		{"tilde in HOME", []BootstrapFile{{Path: "~/.claude/skills/tool/SKILL.md", Content: []byte("x")}}, nil},
		{"absolute in state root", []BootstrapFile{{Path: "/session/state/tool/config.json", Content: []byte("x")}}, nil},
		{"multiple distinct", []BootstrapFile{
			{Path: "~/.claude/CLAUDE.md", Content: []byte("a")},
			{Path: "/session/state/tool.cfg", Content: []byte("b")},
		}, nil},

		{"empty path", []BootstrapFile{{Path: "", Content: []byte("x")}}, ErrInvalidBootstrapPath},
		{"relative path", []BootstrapFile{{Path: "relative/file", Content: []byte("x")}}, ErrInvalidBootstrapPath},

		{"outside roots: /etc", []BootstrapFile{{Path: "/etc/passwd", Content: []byte("x")}}, ErrBootstrapPathOutsideRoots},
		{"root dir itself (HOME)", []BootstrapFile{{Path: "/root", Content: []byte("x")}}, ErrBootstrapPathOutsideRoots},
		{"root dir itself (tilde)", []BootstrapFile{{Path: "~", Content: []byte("x")}}, ErrBootstrapPathOutsideRoots},
		{"traversal escapes state root", []BootstrapFile{{Path: "/session/state/../workspace/repo/x", Content: []byte("x")}}, ErrBootstrapPathOutsideRoots},
		{"traversal escapes HOME via tilde", []BootstrapFile{{Path: "~/../../etc/cron.d/x", Content: []byte("x")}}, ErrBootstrapPathOutsideRoots},
		{"workspace path is outside roots", []BootstrapFile{{Path: "/Users/dev/git/repo/CLAUDE.md", Content: []byte("x")}}, ErrBootstrapPathOutsideRoots},
		{"session workspace (not state) rejected", []BootstrapFile{{Path: "/session/workspace/repo/x", Content: []byte("x")}}, ErrBootstrapPathOutsideRoots},

		{"duplicate resolved path", []BootstrapFile{
			{Path: "~/.claude/CLAUDE.md", Content: []byte("a")},
			{Path: "/root/.claude/CLAUDE.md", Content: []byte("b")},
		}, ErrDuplicateBootstrapPath},
		{"duplicate via traversal-normalized path", []BootstrapFile{
			{Path: "/session/state/a/b", Content: []byte("a")},
			{Path: "/session/state/a/./b", Content: []byte("b")},
		}, ErrDuplicateBootstrapPath},

		{"oversize summed content", []BootstrapFile{{Path: "~/big", Content: make([]byte, maxBootstrapBytes+1)}}, ErrBootstrapFilesTooLarge},
		{"at the size cap is allowed", []BootstrapFile{{Path: "~/big", Content: make([]byte, maxBootstrapBytes)}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Give the content-leak guard something to detect on the secret-content case.
			if c.wantErr != nil && len(c.files) > 0 && len(c.files[0].Content) < 64 {
				c.files[0].Content = []byte(secretContent)
			}
			err := validateBootstrapFiles(c.files)
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("got %v, want %v", err, c.wantErr)
			}
			if strings.Contains(err.Error(), secretContent) {
				t.Fatalf("error message leaked file content: %v", err)
			}
		})
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

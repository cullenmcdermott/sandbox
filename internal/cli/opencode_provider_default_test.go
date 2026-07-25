package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cullenmcdermott/sandbox/internal/session"
)

// defaultOpencodeProvider: the remembered last-used provider wins when still
// logged in (and in-filter); otherwise the preference order anthropic >
// opencode-zen > openai applies; anything else yields "" so the caller keeps
// the anthropic-default remediation.
func TestDefaultOpencodeProvider(t *testing.T) {
	cases := []struct {
		name          string
		entries       []string // auth.json entry keys ("opencode" == Zen)
		lastUsed      string
		seedProviders string
		want          string
	}{
		{"last used wins over preference", []string{"anthropic", "openai"}, session.OpencodeProviderOpenAI, "", session.OpencodeProviderOpenAI},
		{"logged-out last used ignored", []string{"opencode"}, session.OpencodeProviderAnthropic, "", session.OpencodeProviderZen},
		{"filtered-out last used ignored", []string{"anthropic", "openai"}, session.OpencodeProviderAnthropic, "openai", session.OpencodeProviderOpenAI},
		{"anthropic leads the preference order", []string{"anthropic", "opencode", "openai"}, "", "", session.OpencodeProviderAnthropic},
		{"zen beats openai", []string{"opencode", "openai"}, "", "", session.OpencodeProviderZen},
		{"sole provider picked", []string{"openai"}, "", "", session.OpencodeProviderOpenAI},
		{"non-selectable entries cannot be picked", []string{"github-copilot", "opencode-go"}, "", "", ""},
		{"non-selectable entries skipped over", []string{"github-copilot", "opencode"}, "", "", session.OpencodeProviderZen},
		{"invalid last used ignored", []string{"anthropic"}, "bogus", "", session.OpencodeProviderAnthropic},
		{"filter narrows the pick", []string{"anthropic", "openai"}, "", "openai", session.OpencodeProviderOpenAI},
		{"filter with no harvested match yields nothing", []string{"anthropic"}, "", "openai", ""},
		{"bad filter yields nothing (its error surfaces downstream)", []string{"anthropic"}, "", "bogus", ""},
		{"empty login yields nothing", nil, session.OpencodeProviderOpenAI, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testMaterial(t, tc.entries...)
			if got := defaultOpencodeProvider(m, tc.lastUsed, tc.seedProviders); got != tc.want {
				t.Errorf("defaultOpencodeProvider(%v, %q, %q) = %q, want %q",
					tc.entries, tc.lastUsed, tc.seedProviders, got, tc.want)
			}
		})
	}
}

// The remembered-provider preference round-trips through
// ~/.local/share/sandbox/last-opencode-provider under the user's HOME (redirected
// here, the same trick the index title store tests use).
func TestLastOpencodeProviderPrefRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Nothing recorded yet → "".
	if got := readLastOpencodeProvider(); got != "" {
		t.Fatalf("read with no pref file = %q, want \"\"", got)
	}

	writeLastOpencodeProvider(session.OpencodeProviderZen)
	if got := readLastOpencodeProvider(); got != session.OpencodeProviderZen {
		t.Fatalf("read after write = %q, want %q", got, session.OpencodeProviderZen)
	}

	// The file lands at the documented path and holds just the wire value.
	b, err := os.ReadFile(filepath.Join(home, ".local", "share", "sandbox", "last-opencode-provider"))
	if err != nil {
		t.Fatalf("pref file not written at the documented path: %v", err)
	}
	if string(b) != session.OpencodeProviderZen+"\n" {
		t.Fatalf("pref file content = %q, want %q", string(b), session.OpencodeProviderZen+"\n")
	}

	// A newer launch overwrites the older preference.
	writeLastOpencodeProvider(session.OpencodeProviderAnthropic)
	if got := readLastOpencodeProvider(); got != session.OpencodeProviderAnthropic {
		t.Fatalf("read after overwrite = %q, want %q", got, session.OpencodeProviderAnthropic)
	}
}

// Corrupt / hand-edited / empty preferences are ignored, never surfaced: the
// pref is best-effort and must not break a launch.
func TestLastOpencodeProviderPrefIgnoresInvalid(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".local", "share", "sandbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "last-opencode-provider")
	for _, content := range []string{"bogus\n", "opencode\n", "\n", "anthropic\nextra\n"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readLastOpencodeProvider(); got != "" {
			t.Errorf("read with content %q = %q, want \"\"", content, got)
		}
	}

	// "" (the shared-Secret fallback) and non-enum values are never recorded:
	// write is a no-op, so a previously recorded preference survives.
	writeLastOpencodeProvider(session.OpencodeProviderOpenAI)
	writeLastOpencodeProvider("")
	writeLastOpencodeProvider("bogus")
	if got := readLastOpencodeProvider(); got != session.OpencodeProviderOpenAI {
		t.Fatalf("read after ignored writes = %q, want the surviving %q", got, session.OpencodeProviderOpenAI)
	}
}

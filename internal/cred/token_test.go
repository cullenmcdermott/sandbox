package cred

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSetupToken(t *testing.T) {
	good := "sk-ant-oat01-" + strings.Repeat("a", 40)
	other := "sk-ant-oat01-" + strings.Repeat("b", 40)

	cases := []struct {
		name    string
		output  string
		want    string
		wantErr error
	}{
		{"clean line", good, good, nil},
		{"trailing whitespace", "  " + good + "  \n", good, nil},
		{
			name:   "preceded by other output",
			output: "Opening browser...\nPaste code: 1234\nYour token:\n" + good + "\n",
			want:   good,
		},
		{
			name:   "last matching line wins",
			output: good + "\nregenerated:\n" + other + "\n",
			want:   other,
		},
		{"missing token", "no token here\njust logs\n", "", ErrNoSetupToken},
		{"wrong prefix", "sk-ant-api03-" + strings.Repeat("a", 40), "", ErrNoSetupToken},
		{"malformed charset", "sk-ant-oat01-has space here toolong enough", "", ErrMalformedToken},
		{"too short", "sk-ant-oat", "", ErrMalformedToken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseSetupToken(c.output)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v; want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != c.want {
				t.Fatalf("token = %q; want %q", got, c.want)
			}
		})
	}
}

// ParseSetupToken must never echo the raw output in its error.
func TestParseSetupTokenErrorDoesNotEchoOutput(t *testing.T) {
	secretish := "sk-ant-oat01-partial-but-has a space so malformed"
	_, err := ParseSetupToken(secretish)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "partial") || strings.Contains(err.Error(), secretish) {
		t.Fatalf("error echoes output: %v", err)
	}
}

func TestValidateConsoleKey(t *testing.T) {
	valid := "sk-ant-api03-" + strings.Repeat("a", 40)
	cases := []struct {
		name string
		key  string
		ok   bool
	}{
		{"valid api key", valid, true},
		{"valid with whitespace", "  " + valid + "\n", true},
		{"reject oauth token", "sk-ant-oat01-" + strings.Repeat("a", 40), false},
		{"wrong prefix", "sk-proj-" + strings.Repeat("a", 40), false},
		{"too short", "sk-ant-x", false},
		{"bad charset", "sk-ant-api03-has space", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ValidateConsoleKey(c.key)
			if c.ok {
				if err != nil {
					t.Fatalf("ValidateConsoleKey(%q) = %v; want nil", c.key, err)
				}
				// The returned key is normalized: exactly the trimmed input.
				if got != strings.TrimSpace(c.key) {
					t.Fatalf("normalized key = %q; want %q", got, strings.TrimSpace(c.key))
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateConsoleKey(%q) = nil; want error", c.key)
			}
			if got != "" {
				t.Fatalf("ValidateConsoleKey(%q) returned %q with error; want \"\"", c.key, got)
			}
		})
	}
}

func TestValidateConsoleKeyErrorDoesNotEchoKey(t *testing.T) {
	_, err := ValidateConsoleKey("sk-ant-oat01-secretleak")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secretleak") {
		t.Fatalf("error echoes key: %v", err)
	}
}

func TestAuthForType(t *testing.T) {
	if got, err := AuthForType(AccountSubscription); err != nil || got != "oauth" {
		t.Fatalf("AuthForType(subscription) = %q, %v; want oauth, nil", got, err)
	}
	if got, err := AuthForType(AccountConsole); err != nil || got != "api-key" {
		t.Fatalf("AuthForType(console) = %q, %v; want api-key, nil", got, err)
	}
	if got, err := AuthForType(AccountType("bogus")); !errors.Is(err, ErrInvalidAccountType) || got != "" {
		t.Fatalf("AuthForType(bogus) = %q, %v; want \"\", ErrInvalidAccountType", got, err)
	}
	if got, err := AuthForType(AccountType("")); !errors.Is(err, ErrInvalidAccountType) || got != "" {
		t.Fatalf("AuthForType(\"\") = %q, %v; want \"\", ErrInvalidAccountType", got, err)
	}
}

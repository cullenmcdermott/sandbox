package client

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// leakSecret is a distinctive fake credential value planted in the fixtures
// below. No error message and no OpencodeAuthEntry field may ever contain it —
// only OpencodeAuthMaterial.JSON is allowed to carry credential bytes.
const leakSecret = "SUPERSECRETVALUE-do-not-leak-abc123"

// fakeEnv builds a getenv that reads from a fixed map.
func fakeEnv(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

// fakeReadFileAt builds a readFile that asserts the resolved path and returns
// the given bytes/error for it.
func fakeReadFileAt(t *testing.T, want string, data []byte, err error) func(string) ([]byte, error) {
	t.Helper()
	return func(p string) ([]byte, error) {
		if p != want {
			t.Fatalf("read path = %q, want %q", p, want)
		}
		return data, err
	}
}

func fakeHome(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

func TestHarvestOpencodeAuthXDGPath(t *testing.T) {
	doc := `{"openai":{"type":"api","key":"` + leakSecret + `"},"anthropic":{"type":"oauth","access":"` + leakSecret + `"}}`
	env := fakeEnv(map[string]string{"XDG_DATA_HOME": "/xdg"})
	read := fakeReadFileAt(t, "/xdg/opencode/auth.json", []byte(doc), nil)

	mat, err := harvestOpencodeAuth(env, fakeHome("/home/user"), read)
	if err != nil {
		t.Fatalf("harvest: %v", err)
	}
	// JSON is the ORIGINAL bytes, verbatim.
	if string(mat.JSON) != doc {
		t.Fatalf("JSON = %q, want original bytes %q", mat.JSON, doc)
	}
	// Entries are sorted by Provider.
	if len(mat.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(mat.Entries))
	}
	if mat.Entries[0].Provider != "anthropic" || mat.Entries[1].Provider != "openai" {
		t.Fatalf("entries not sorted by provider: %+v", mat.Entries)
	}
	if mat.Entries[0].Type != "oauth" || mat.Entries[1].Type != "api" {
		t.Fatalf("entry types wrong: %+v", mat.Entries)
	}
	// Secret hygiene: no entry field carries the value.
	for _, e := range mat.Entries {
		if strings.Contains(e.Provider, leakSecret) || strings.Contains(e.Type, leakSecret) {
			t.Fatalf("entry leaked secret value: %+v", e)
		}
	}
}

func TestHarvestOpencodeAuthHomeFallback(t *testing.T) {
	doc := `{"anthropic":{"type":"oauth"}}`
	// XDG_DATA_HOME unset → ~/.local/share/opencode/auth.json.
	env := fakeEnv(map[string]string{})
	read := fakeReadFileAt(t, "/home/user/.local/share/opencode/auth.json", []byte(doc), nil)

	mat, err := harvestOpencodeAuth(env, fakeHome("/home/user"), read)
	if err != nil {
		t.Fatalf("harvest: %v", err)
	}
	if len(mat.Entries) != 1 || mat.Entries[0].Provider != "anthropic" {
		t.Fatalf("entries = %+v", mat.Entries)
	}
}

func TestHarvestOpencodeAuthMissingFile(t *testing.T) {
	env := fakeEnv(map[string]string{"XDG_DATA_HOME": "/xdg"})
	read := fakeReadFileAt(t, "/xdg/opencode/auth.json", nil, os.ErrNotExist)

	_, err := harvestOpencodeAuth(env, fakeHome("/home/user"), read)
	if !errors.Is(err, ErrOpencodeAuthNotFound) {
		t.Fatalf("err = %v, want ErrOpencodeAuthNotFound", err)
	}
	// Remediation must survive into the wrapped message.
	if !strings.Contains(err.Error(), "opencode auth login") {
		t.Fatalf("missing remediation in %q", err.Error())
	}
	// Path is named.
	if !strings.Contains(err.Error(), "/xdg/opencode/auth.json") {
		t.Fatalf("missing path in %q", err.Error())
	}
}

func TestHarvestOpencodeAuthUnreadable(t *testing.T) {
	env := fakeEnv(map[string]string{"XDG_DATA_HOME": "/xdg"})
	read := fakeReadFileAt(t, "/xdg/opencode/auth.json", nil, os.ErrPermission)

	_, err := harvestOpencodeAuth(env, fakeHome("/home/user"), read)
	if err == nil {
		t.Fatal("want error for unreadable file")
	}
	if errors.Is(err, ErrOpencodeAuthNotFound) {
		t.Fatalf("permission error should not be ErrOpencodeAuthNotFound: %v", err)
	}
	if !strings.Contains(err.Error(), "/xdg/opencode/auth.json") {
		t.Fatalf("missing path in %q", err.Error())
	}
}

func TestHarvestOpencodeAuthNonObject(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"array", `["anthropic"]`},
		{"null", `null`},
		{"string", `"nope"`},
		{"garbage", `{not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := fakeEnv(map[string]string{"XDG_DATA_HOME": "/xdg"})
			read := fakeReadFileAt(t, "/xdg/opencode/auth.json", []byte(tc.body), nil)
			_, err := harvestOpencodeAuth(env, fakeHome("/home/user"), read)
			if err == nil {
				t.Fatalf("want error for %s body", tc.name)
			}
			if !strings.Contains(err.Error(), "/xdg/opencode/auth.json") {
				t.Fatalf("missing path in %q", err.Error())
			}
		})
	}
}

func TestHarvestOpencodeAuthEntryMissingType(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		// A distinctive secret is present but there is no valid string "type".
		{"no type", `{"anthropic":{"access":"` + leakSecret + `"}}`},
		{"non-string type", `{"anthropic":{"type":123,"access":"` + leakSecret + `"}}`},
		{"entry not object", `{"anthropic":"` + leakSecret + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := fakeEnv(map[string]string{"XDG_DATA_HOME": "/xdg"})
			read := fakeReadFileAt(t, "/xdg/opencode/auth.json", []byte(tc.body), nil)
			_, err := harvestOpencodeAuth(env, fakeHome("/home/user"), read)
			if err == nil {
				t.Fatalf("want error for %s", tc.name)
			}
			// Names the provider key and the path only — never the value.
			if !strings.Contains(err.Error(), `"anthropic"`) {
				t.Fatalf("missing provider key in %q", err.Error())
			}
			if strings.Contains(err.Error(), leakSecret) {
				t.Fatalf("error leaked secret value: %q", err.Error())
			}
		})
	}
}

func harvestFixture(t *testing.T) OpencodeAuthMaterial {
	t.Helper()
	doc := `{"anthropic":{"type":"oauth","access":"` + leakSecret + `"},"openai":{"type":"api","key":"` + leakSecret + `"}}`
	env := fakeEnv(map[string]string{"XDG_DATA_HOME": "/xdg"})
	read := fakeReadFileAt(t, "/xdg/opencode/auth.json", []byte(doc), nil)
	mat, err := harvestOpencodeAuth(env, fakeHome("/home/user"), read)
	if err != nil {
		t.Fatalf("harvest: %v", err)
	}
	return mat
}

func TestFilterEmpty(t *testing.T) {
	mat := harvestFixture(t)
	_, err := mat.Filter()
	if err == nil {
		t.Fatal("want error for empty filter")
	}
	if strings.Contains(err.Error(), leakSecret) {
		t.Fatalf("error leaked secret: %q", err.Error())
	}
}

func TestFilterUnknownProvider(t *testing.T) {
	mat := harvestFixture(t)
	_, err := mat.Filter("anthropic", "does-not-exist")
	if err == nil {
		t.Fatal("want error for unknown provider")
	}
	// Lists available provider NAMES (never values).
	if !strings.Contains(err.Error(), "anthropic") || !strings.Contains(err.Error(), "openai") {
		t.Fatalf("missing available provider names in %q", err.Error())
	}
	if strings.Contains(err.Error(), leakSecret) {
		t.Fatalf("error leaked secret: %q", err.Error())
	}
}

func TestFilterSubset(t *testing.T) {
	mat := harvestFixture(t)
	got, err := mat.Filter("anthropic")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	// Entries narrowed to the requested provider.
	if len(got.Entries) != 1 || got.Entries[0].Provider != "anthropic" {
		t.Fatalf("entries = %+v", got.Entries)
	}
	// JSON is just the subset (openai dropped) — deterministic (Go sorts map keys).
	if strings.Contains(string(got.JSON), "openai") {
		t.Fatalf("subset JSON still carries dropped provider: %s", got.JSON)
	}
	if !strings.Contains(string(got.JSON), "anthropic") {
		t.Fatalf("subset JSON missing kept provider: %s", got.JSON)
	}
	// The kept provider's credential value survives in JSON (that is the seed).
	if !strings.Contains(string(got.JSON), leakSecret) {
		t.Fatalf("subset JSON dropped the credential value it must carry: %s", got.JSON)
	}

	// Determinism: re-filter and compare bytes.
	got2, err := mat.Filter("anthropic")
	if err != nil {
		t.Fatalf("filter (2nd): %v", err)
	}
	if string(got.JSON) != string(got2.JSON) {
		t.Fatalf("filter not deterministic: %q vs %q", got.JSON, got2.JSON)
	}
}

// TestFilterMultiProviderJSONDeterministic pins that a multi-provider subset
// marshals with sorted keys regardless of the requested order.
func TestFilterMultiProviderJSONDeterministic(t *testing.T) {
	mat := harvestFixture(t)
	a, err := mat.Filter("openai", "anthropic")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	b, err := mat.Filter("anthropic", "openai")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if string(a.JSON) != string(b.JSON) {
		t.Fatalf("subset JSON order-dependent: %q vs %q", a.JSON, b.JSON)
	}
}

// TestHarvestNoEntryLeaksSecret is the broad leak guard over a fully-valid
// harvest: the JSON field may carry the secret, but no entry field may.
func TestHarvestNoEntryLeaksSecret(t *testing.T) {
	mat := harvestFixture(t)
	for _, e := range mat.Entries {
		if strings.Contains(e.Provider, leakSecret) || strings.Contains(e.Type, leakSecret) {
			t.Fatalf("entry leaked secret: %+v", e)
		}
	}
	if !strings.Contains(string(mat.JSON), leakSecret) {
		t.Fatal("JSON should carry the credential value (it is the seed)")
	}
}
